package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/objectstore"

	"golang.org/x/net/html"
	"golang.org/x/sync/singleflight"
)

const (
	linkFaviconTimeout        = 5 * time.Second
	linkFaviconMaxPageBytes   = 256 << 10
	linkFaviconMaxIconBytes   = 24 << 10
	linkFaviconMaxCandidates  = 4
	linkFaviconCacheTTL       = 7 * 24 * time.Hour
	linkFaviconNegativeTTL    = 30 * time.Minute
	linkFaviconIndexMaxBytes  = 512
	linkFaviconRateRequests   = 120
	linkFaviconNegativeMaxAge = 5 * time.Minute
	linkFaviconUserAgent      = "Mozilla/5.0 (compatible; FlowFaviconFetcher/1.0)"
)

var (
	// linkFaviconSkipHosts are sites the app draws its own brand icon for.
	linkFaviconSkipHosts = map[string]bool{"github.com": true, "figma.com": true, "slack.com": true}

	linkFaviconFetches singleflight.Group
	linkFaviconSlots   = make(chan struct{}, 8)
	linkFaviconObject  = regexp.MustCompile(`^linkicon_[0-9a-f]{64}_[0-9a-f]{64}\.(png|jpg|gif|webp|ico)$`)
	errLinkFaviconBusy = errors.New("favicon lookup busy")
)

var linkFaviconExtensions = map[string]string{
	"image/png":                "png",
	"image/jpeg":               "jpg",
	"image/gif":                "gif",
	"image/webp":               "webp",
	"image/x-icon":             "ico",
	"image/vnd.microsoft.icon": "ico",
}

type linkFaviconCacheRecord struct {
	Object    string    `json:"object,omitempty"` // Empty for a negative cache entry.
	ExpiresAt time.Time `json:"expiresAt"`
}

type linkFaviconResult struct {
	Icon        []byte
	ContentType string
}

func newLinkFaviconClient() *http.Client {
	client := secureOutboundClient(linkFaviconTimeout, outboundPublicOnly)
	client.Transport.(*http.Transport).MaxResponseHeaderBytes = 64 << 10
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.User != nil {
			return errors.New("unsafe redirect")
		}
		req.Header.Del("Referer")
		// Use the public-address check for HTTP redirects too. The dialer
		// checks the actual destination again and connects to a checked IP.
		checked := *req.URL
		checked.Scheme = "https"
		check := *req
		check.URL = &checked
		return checkRedirect(&check, via)
	}
	return client
}

// Discover a site-wide icon without sending any saved path, query or fragment.
func linkFaviconSite(raw string) (*url.URL, bool) {
	normalized, ok := validHTTPURL(raw, 2048)
	if !ok {
		return nil, false
	}
	page, err := url.Parse(normalized)
	if err != nil || page.Hostname() == "" {
		return nil, false
	}
	host := strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(page.Hostname()), "."), "www.")
	if linkFaviconSkipHosts[host] || strings.HasSuffix(host, ".slack.com") {
		return nil, false
	}
	return &url.URL{Scheme: page.Scheme, Host: strings.ToLower(page.Host)}, true
}

func linkFaviconDigest(site string) string {
	sum := sha256.Sum256([]byte(site))
	return hex.EncodeToString(sum[:])
}

func linkFaviconIndexObject(site string) string {
	return "linkicon_index_" + linkFaviconDigest(site) + ".json"
}

func linkFaviconObjectPrefix(site string) string {
	return "linkicon_" + linkFaviconDigest(site) + "_"
}

func linkFaviconObjectName(site string, icon []byte, contentType string) string {
	iconDigest := sha256.Sum256(icon)
	return linkFaviconObjectPrefix(site) + hex.EncodeToString(iconDigest[:]) + "." + linkFaviconExtensions[contentType]
}

func linkFaviconBytes(ctx context.Context, site *url.URL) linkFaviconResult {
	ctx, cancel := context.WithTimeout(ctx, linkFaviconTimeout)
	defer cancel()
	client := newLinkFaviconClient()
	defer client.CloseIdleConnections()
	candidates := declaredLinkFavicons(ctx, client, site)
	candidates = append(candidates, site.ResolveReference(&url.URL{Path: "/favicon.ico"}))
	for _, candidate := range candidates {
		if icon, contentType := fetchLinkFavicon(ctx, client, candidate); len(icon) > 0 {
			return linkFaviconResult{Icon: icon, ContentType: contentType}
		}
	}
	return linkFaviconResult{}
}

// declaredLinkFavicons are the page's link icons in document order, followed
// by apple-touch icons, which are usually larger but still useful fallbacks.
func declaredLinkFavicons(ctx context.Context, client *http.Client, page *url.URL) []*url.URL {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", linkFaviconUserAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return nil
	}
	base := resp.Request.URL
	var icons, touch []*url.URL
	tokens := html.NewTokenizer(io.LimitReader(resp.Body, linkFaviconMaxPageBytes))
head:
	for len(icons) < linkFaviconMaxCandidates-1 {
		switch tokens.Next() {
		case html.ErrorToken:
			break head
		case html.EndTagToken:
			if name, _ := tokens.TagName(); string(name) == "head" {
				break head
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			if token.Data == "body" {
				break head
			}
			if token.Data != "link" {
				continue
			}
			var rel, href string
			for _, attr := range token.Attr {
				switch attr.Key {
				case "rel":
					rel = strings.ToLower(attr.Val)
				case "href":
					href = strings.TrimSpace(attr.Val)
				}
			}
			if href == "" || len(href) > 2048 {
				continue
			}
			rels := strings.Fields(rel)
			regular := slices.Contains(rels, "icon")
			if !regular && (len(touch) >= linkFaviconMaxCandidates-1 || !slices.Contains(rels, "apple-touch-icon")) {
				continue
			}
			resolved, err := base.Parse(href)
			if err != nil || (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Hostname() == "" || resolved.User != nil {
				continue
			}
			if regular {
				icons = append(icons, resolved)
			} else {
				touch = append(touch, resolved)
			}
		}
	}
	return append(icons, touch[:min(len(touch), linkFaviconMaxCandidates-1-len(icons))]...)
}

func fetchLinkFavicon(ctx context.Context, client *http.Client, icon *url.URL) ([]byte, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, icon.String(), nil)
	if err != nil {
		return nil, ""
	}
	req.Header.Set("User-Agent", linkFaviconUserAgent)
	req.Header.Set("Accept", "image/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, linkFaviconMaxIconBytes+1))
	if err != nil || len(body) == 0 || len(body) > linkFaviconMaxIconBytes {
		return nil, ""
	}
	contentType := http.DetectContentType(body)
	if _, ok := linkFaviconExtensions[contentType]; !ok {
		return nil, ""
	}
	return body, contentType
}

func readLinkFaviconObject(ctx context.Context, storage objectstore.Store, name string, limit int64) []byte {
	reader, _, size, err := storage.Open(ctx, name)
	if err != nil {
		return nil
	}
	defer reader.Close()
	if size <= 0 || size > limit {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil
	}
	return body
}

func readStoredLinkFavicon(ctx context.Context, storage objectstore.Store, site string) (linkFaviconResult, bool) {
	if storage == nil {
		return linkFaviconResult{}, false
	}
	raw := readLinkFaviconObject(ctx, storage, linkFaviconIndexObject(site), linkFaviconIndexMaxBytes)
	var record linkFaviconCacheRecord
	if json.Unmarshal(raw, &record) != nil || !record.ExpiresAt.After(time.Now()) {
		return linkFaviconResult{}, false
	}
	if record.Object == "" {
		return linkFaviconResult{}, true
	}
	if !strings.HasPrefix(record.Object, linkFaviconObjectPrefix(site)) || !linkFaviconObject.MatchString(record.Object) {
		return linkFaviconResult{}, false
	}
	icon := readLinkFaviconObject(ctx, storage, record.Object, linkFaviconMaxIconBytes)
	contentType := http.DetectContentType(icon)
	if _, ok := linkFaviconExtensions[contentType]; !ok || record.Object != linkFaviconObjectName(site, icon, contentType) {
		return linkFaviconResult{}, false
	}
	return linkFaviconResult{Icon: icon, ContentType: contentType}, true
}

func storeLinkFavicon(ctx context.Context, storage objectstore.Store, site string, result linkFaviconResult) {
	if storage == nil || ctx.Err() != nil {
		return
	}
	record := linkFaviconCacheRecord{ExpiresAt: time.Now().Add(linkFaviconNegativeTTL)}
	if len(result.Icon) > 0 {
		record.ExpiresAt = time.Now().Add(linkFaviconCacheTTL)
		record.Object = linkFaviconObjectName(site, result.Icon, result.ContentType)
		if existing := readLinkFaviconObject(ctx, storage, record.Object, linkFaviconMaxIconBytes); !bytes.Equal(existing, result.Icon) {
			if _, err := storage.Put(ctx, record.Object, bytes.NewReader(result.Icon), result.ContentType); err != nil {
				return
			}
		}
	}
	if raw, err := json.Marshal(record); err == nil {
		_, _ = storage.Put(ctx, linkFaviconIndexObject(site), bytes.NewReader(raw), "application/json")
	}
}

func (s *server) cachedLinkFavicon(ctx context.Context, site *url.URL) (linkFaviconResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*linkFaviconTimeout)
	defer cancel()
	key := site.String()
	storage, _ := s.storage()
	value, err, _ := linkFaviconFetches.Do(key, func() (any, error) {
		if result, ok := readStoredLinkFavicon(ctx, storage, key); ok {
			return result, nil
		}
		select {
		case linkFaviconSlots <- struct{}{}:
			defer func() { <-linkFaviconSlots }()
		default:
			return linkFaviconResult{}, errLinkFaviconBusy
		}
		result := linkFaviconBytes(ctx, site)
		if err := ctx.Err(); err != nil {
			return linkFaviconResult{}, err
		}
		storeLinkFavicon(ctx, storage, key, result)
		return result, nil
	})
	return value.(linkFaviconResult), err
}

func (s *server) allowLinkFaviconRequest(w http.ResponseWriter, r *http.Request) bool {
	if s.authDisabled {
		return true
	}
	userID := authUser(r).ID
	if userID == "" {
		http.NotFound(w, r)
		return false
	}
	subject := apiRateSubject{Category: apiRateSession, Key: "link-favicon:" + userID, Budget: &apiRateBudget{Requests: linkFaviconRateRequests}}
	return s.apiRateLimits().check(r.Context(), r, subject).write(w)
}

// GET /api/link-icon?workspace=<workspace>&issueId=<issue>&attachmentId=<link>
func (s *server) getLinkIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Add("Vary", "Cookie, Authorization, X-Workspace-Key")
	query := r.URL.Query()
	issueID := strings.TrimSpace(query.Get("issueId"))
	attachmentID := strings.TrimSpace(query.Get("attachmentId"))
	if issueID == "" || attachmentID == "" || workspaceKey(r) == "" {
		http.NotFound(w, r)
		return
	}
	if !s.allowLinkFaviconRequest(w, r) {
		return
	}
	issue, _, ok := s.authorizedPublicIssue(w, r, issueID)
	if !ok {
		return
	}
	index := slices.IndexFunc(issue.Attachments, func(attachment domain.Attachment) bool {
		return attachment.ID == attachmentID && attachment.ContentType == "text/uri-list" && attachment.IconURL == ""
	})
	if index < 0 {
		http.NotFound(w, r)
		return
	}
	site, ok := linkFaviconSite(issue.Attachments[index].URL)
	if !ok {
		http.NotFound(w, r)
		return
	}
	result, err := s.cachedLinkFavicon(r.Context(), site)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if len(result.Icon) == 0 {
		w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(int(linkFaviconNegativeMaxAge/time.Second)))
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(int(linkFaviconCacheTTL/time.Second)))
	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(result.Icon)), 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(result.Icon)
}
