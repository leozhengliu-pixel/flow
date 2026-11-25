package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"golang.org/x/net/html"
)

// A link attached to an issue shows its site's favicon, like the design reference. Once a link has been created (so a
// refused request never reaches the network) the icon is fetched in the background through the SSRF-safe outbound
// client (public addresses only, a few redirects, http or https), stored once in the upload storage under its content
// hash, and set on the attachment with an ordinary workspace mutation. Nothing from the request is logged.

const (
	linkFaviconTimeout       = 5 * time.Second
	linkFaviconMaxPageBytes  = 256 << 10
	linkFaviconMaxIconBytes  = 24 << 10
	linkFaviconMaxRedirects  = 3
	linkFaviconMaxCandidates = 4
	linkFaviconMaxJobs       = 4
	linkFaviconUserAgent     = "Mozilla/5.0 (compatible; FlowFaviconFetcher/1.0)"
)

// linkFaviconSkipHosts are sites the app draws its own brand icon for, so no favicon is fetched.
var linkFaviconSkipHosts = map[string]bool{"github.com": true, "figma.com": true, "slack.com": true}

var linkFaviconHTTPClient = func() *http.Client {
	client := secureOutboundClient(linkFaviconTimeout, outboundPublicOnly)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= linkFaviconMaxRedirects || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.User != nil {
			return errors.New("unsafe redirect")
		}
		return nil
	}
	return client
}

// linkFaviconBytes returns the favicon of the page's site and its media type, or nothing when the site has none that
// is a small PNG, JPEG, GIF, WebP or ICO image.
func linkFaviconBytes(ctx context.Context, page *url.URL) ([]byte, string) {
	if page == nil || (page.Scheme != "http" && page.Scheme != "https") || page.Host == "" || page.User != nil {
		return nil, ""
	}
	host := strings.TrimPrefix(strings.ToLower(page.Hostname()), "www.")
	if linkFaviconSkipHosts[host] || strings.HasSuffix(host, ".slack.com") {
		return nil, ""
	}
	ctx, cancel := context.WithTimeout(ctx, linkFaviconTimeout)
	defer cancel()
	client := linkFaviconHTTPClient()
	candidates := linkFaviconCandidates(ctx, client, page)
	if len(candidates) > linkFaviconMaxCandidates {
		candidates = candidates[:linkFaviconMaxCandidates]
	}
	for _, candidate := range candidates {
		if icon, contentType := fetchLinkFavicon(ctx, client, candidate); len(icon) > 0 {
			return icon, contentType
		}
	}
	return nil, ""
}

// linkFaviconCandidates lists the icon URLs to try: the page's own <link rel="icon"> first, then /favicon.ico.
func linkFaviconCandidates(ctx context.Context, client *http.Client, page *url.URL) []*url.URL {
	candidates := declaredLinkFavicons(ctx, client, page)
	return append(candidates, &url.URL{Scheme: page.Scheme, Host: page.Host, Path: "/favicon.ico"})
}

// declaredLinkFavicons are the page's <link rel="icon"> URLs in document order, then its apple-touch icons (usually larger).
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "html") {
		return nil
	}
	base := resp.Request.URL
	var icons, touch []*url.URL
	tokens := html.NewTokenizer(io.LimitReader(resp.Body, linkFaviconMaxPageBytes))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return append(icons, touch...)
		case html.EndTagToken:
			if name, _ := tokens.TagName(); string(name) == "head" {
				return append(icons, touch...)
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tokens.TagName()
			if string(name) == "body" {
				return append(icons, touch...)
			}
			if string(name) != "link" || !hasAttr {
				continue
			}
			var rel, href string
			for {
				key, value, more := tokens.TagAttr()
				switch string(key) {
				case "rel":
					rel = strings.ToLower(string(value))
				case "href":
					href = strings.TrimSpace(string(value))
				}
				if !more {
					break
				}
			}
			if href == "" || !linkRelIsIcon(rel) {
				continue
			}
			resolved, err := base.Parse(href)
			if err != nil || (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" || resolved.User != nil {
				continue
			}
			if strings.Contains(rel, "apple-touch-icon") {
				touch = append(touch, resolved)
			} else {
				icons = append(icons, resolved)
			}
		}
	}
}

// linkRelIsIcon reports whether a rel value names a bitmap icon (icon, shortcut icon, apple-touch-icon), not a mask icon.
func linkRelIsIcon(rel string) bool {
	for _, token := range strings.Fields(rel) {
		if token == "icon" || token == "apple-touch-icon" {
			return true
		}
	}
	return false
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
	// The bytes decide the type, not the header: an SVG or an HTML error page is never kept.
	contentType := http.DetectContentType(body)
	if _, ok := linkFaviconExtensions[contentType]; !ok {
		return nil, ""
	}
	return body, contentType
}

var linkFaviconExtensions = map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp", "image/x-icon": "ico"}

// linkFaviconObject names an icon in the upload storage: linkicon_<sha-256 of the bytes>.<extension>.
var linkFaviconObject = regexp.MustCompile(`^linkicon_[0-9a-f]{64}\.(png|jpg|gif|webp|ico)$`)

// A favicon is public artwork of a site, not workspace content, so any signed-in user may read one (see serveUpload).
func isLinkFaviconObject(name string) bool { return linkFaviconObject.MatchString(name) }

// storeLinkFavicon keeps the icon once under its content hash and returns the path /uploads serves it from.
func (s *server) storeLinkFavicon(ctx context.Context, icon []byte, contentType string) string {
	sum := sha256.Sum256(icon)
	name := "linkicon_" + hex.EncodeToString(sum[:]) + "." + linkFaviconExtensions[contentType]
	storage, err := s.storage()
	if err != nil {
		return ""
	}
	if existing, _, _, err := storage.Open(ctx, name); err == nil {
		_ = existing.Close()
		return "/uploads/" + name
	}
	if _, err := storage.Put(ctx, name, bytes.NewReader(icon), contentType); err != nil {
		return ""
	}
	return "/uploads/" + name
}

var (
	linkFaviconSlots  = make(chan struct{}, linkFaviconMaxJobs)
	linkFaviconActive sync.Map
)

// queueLinkFavicon fetches the favicon of a link that was just created and, if the attachment still exists and has no
// icon, sets one. It never delays the request that created the link: the work runs detached from the request (keeping
// its values, not its cancellation), within a small global limit, and is skipped when the same host is already being
// fetched or every slot is busy.
func (s *server) queueLinkFavicon(request context.Context, workspace, issueID, attachmentID string, page *url.URL) {
	if page == nil || issueID == "" || attachmentID == "" {
		return
	}
	host := strings.ToLower(page.Host)
	if _, busy := linkFaviconActive.LoadOrStore(host, struct{}{}); busy {
		return
	}
	select {
	case linkFaviconSlots <- struct{}{}:
	default:
		linkFaviconActive.Delete(host)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(request), linkFaviconTimeout)
	go func() {
		defer func() { <-linkFaviconSlots; linkFaviconActive.Delete(host); cancel() }()
		s.setLinkFavicon(ctx, workspace, issueID, attachmentID, page)
	}()
}

func (s *server) setLinkFavicon(ctx context.Context, workspace, issueID, attachmentID string, page *url.URL) {
	icon, contentType := linkFaviconBytes(ctx, page)
	if len(icon) == 0 {
		return
	}
	path := s.storeLinkFavicon(ctx, icon, contentType)
	if path == "" {
		return
	}
	mutation := store.WithIssueRecordMutations(s.issueAttachmentContext(ctx), issueID)
	_ = s.store.MutateWorkspace(mutation, workspace, "attachment.updated", issueID, linkAttachmentInput{IconURL: &path}, func(data *domain.Bootstrap) error {
		target, err := issueByID(data, issueID)
		if err != nil {
			return store.ErrNoMutation
		}
		for index := range target.Attachments {
			if target.Attachments[index].ID != attachmentID {
				continue
			}
			if target.Attachments[index].IconURL != "" {
				return store.ErrNoMutation
			}
			target.Attachments[index].IconURL = path
			return nil
		}
		return store.ErrNoMutation
	})
}
