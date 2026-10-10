package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Link previews back the rich-text editor's card for a pasted Figma or X post
// link: GET /api/integrations/link-preview?url=... returns the page's Open Graph
// title, description and image so the editor can draw a link card (those two
// providers do not allow a live frame the way video players do). Only a short
// list of provider hosts is ever fetched, over https, through the SSRF-safe
// outbound client (public addresses only, redirects stay on the list), and the
// response is read up to a small limit.

const (
	linkPreviewMaxURLLength   = 2048
	linkPreviewMaxBytes       = 2 << 20
	linkPreviewTimeout        = 6 * time.Second
	linkPreviewCacheTTL       = 10 * time.Minute
	linkPreviewNegativeTTL    = 30 * time.Second
	linkPreviewCacheEntries   = 256
	linkPreviewMaxTitleRunes  = 200
	linkPreviewMaxDescRunes   = 500
	linkPreviewUserAgent      = "Mozilla/5.0 (compatible; FlowLinkPreview/1.0; +https://flow.app)"
	linkPreviewMaxRedirectHop = 3
)

// linkPreviewHosts are the hosts a preview may be fetched from (and redirected to).
var linkPreviewHosts = map[string]string{
	"figma.com":          "Figma",
	"www.figma.com":      "Figma",
	"x.com":              "X",
	"www.x.com":          "X",
	"twitter.com":        "X",
	"www.twitter.com":    "X",
	"mobile.twitter.com": "X",
}

type linkPreviewResponse struct {
	URL         string `json:"url"`
	SiteName    string `json:"siteName"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ImageURL    string `json:"imageUrl"`
}

var (
	errLinkPreviewInvalid  = previewError(http.StatusBadRequest, "invalid_url", "Only Figma and X links can be previewed")
	errLinkPreviewUpstream = previewError(http.StatusBadGateway, "upstream", "Could not load the link preview")
)

var linkPreviewHTTPClient = func() *http.Client {
	client := secureOutboundClient(linkPreviewTimeout)
	client.CheckRedirect = linkPreviewRedirect
	return client
}

// linkPreviewOEmbedHosts serve X's oEmbed endpoint (publish.twitter.com redirects to publish.x.com). They are fetched, never accepted as input.
var linkPreviewOEmbedHosts = map[string]bool{"publish.x.com": true, "publish.twitter.com": true}

func linkPreviewRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= linkPreviewMaxRedirectHop || req.URL.Scheme != "https" || req.URL.Port() != "" || req.URL.User != nil {
		return errors.New("unsafe redirect")
	}
	host := strings.ToLower(req.URL.Hostname())
	if _, ok := linkPreviewHosts[host]; !ok && !linkPreviewOEmbedHosts[host] {
		return errors.New("unsafe redirect")
	}
	return nil
}

// parseLinkPreviewURL accepts one http(s) link on a listed host, and returns it as https without a fragment.
func parseLinkPreviewURL(raw string) (*url.URL, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > linkPreviewMaxURLLength || !utf8.ValidString(raw) {
		return nil, "", errLinkPreviewInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Port() != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, "", errLinkPreviewInvalid
	}
	site, ok := linkPreviewHosts[strings.ToLower(parsed.Hostname())]
	if !ok || strings.ContainsAny(raw, "\r\n\t ") {
		return nil, "", errLinkPreviewInvalid
	}
	parsed.Scheme = "https"
	parsed.Host = strings.ToLower(parsed.Hostname())
	parsed.Fragment = ""
	return parsed, site, nil
}

type linkPreviewMeta struct {
	title, description, image, site string
}

func linkPreviewTruncate(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit-1]) + "…"
}

// parseLinkPreviewHTML reads the Open Graph (then Twitter card, then plain) metadata out of the head of a page.
func parseLinkPreviewHTML(body io.Reader, base *url.URL) linkPreviewMeta {
	tokens := html.NewTokenizer(body)
	var (
		meta         = map[string]string{}
		pageTitle    string
		inTitle      bool
		headFinished bool
	)
	for !headFinished {
		switch tokens.Next() {
		case html.ErrorToken:
			headFinished = true
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			switch token.Data {
			case "title":
				inTitle = pageTitle == ""
			case "meta":
				var name, content string
				for _, attr := range token.Attr {
					switch strings.ToLower(attr.Key) {
					case "property", "name":
						if name == "" {
							name = strings.ToLower(strings.TrimSpace(attr.Val))
						}
					case "content":
						content = attr.Val
					}
				}
				if name != "" && content != "" {
					if _, seen := meta[name]; !seen {
						meta[name] = content
					}
				}
			case "body":
				headFinished = true
			}
		case html.TextToken:
			if inTitle {
				pageTitle += string(tokens.Text())
			}
		case html.EndTagToken:
			if name, _ := tokens.TagName(); string(name) == "title" {
				inTitle = false
			} else if string(name) == "head" {
				headFinished = true
			}
		}
	}
	first := func(keys ...string) string {
		for _, key := range keys {
			if value := strings.TrimSpace(meta[key]); value != "" {
				return value
			}
		}
		return ""
	}
	out := linkPreviewMeta{
		title:       linkPreviewTruncate(first("og:title", "twitter:title", "title"), linkPreviewMaxTitleRunes),
		description: linkPreviewTruncate(first("og:description", "twitter:description", "description"), linkPreviewMaxDescRunes),
		site:        linkPreviewTruncate(first("og:site_name"), 80),
	}
	if out.title == "" {
		out.title = linkPreviewTruncate(pageTitle, linkPreviewMaxTitleRunes)
	}
	if image := first("og:image:secure_url", "og:image", "twitter:image", "twitter:image:src"); image != "" && len(image) <= linkPreviewMaxURLLength {
		if ref, err := url.Parse(image); err == nil {
			resolved := base.ResolveReference(ref)
			if resolved.Scheme == "https" && resolved.User == nil && resolved.Hostname() != "" {
				out.image = resolved.String()
			}
		}
	}
	return out
}

type linkPreviewResult struct {
	response linkPreviewResponse
	err      *filePreviewError
	at       time.Time
}

type linkPreviewCacheStore struct {
	mu      sync.Mutex
	entries map[string]linkPreviewResult
}

func (c *linkPreviewCacheStore) reset() {
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}

func (c *linkPreviewCacheStore) load(key string, fetch func() linkPreviewResult) linkPreviewResult {
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		ttl := linkPreviewCacheTTL
		if entry.err != nil {
			ttl = linkPreviewNegativeTTL
		}
		if time.Since(entry.at) < ttl {
			c.mu.Unlock()
			return entry
		}
		delete(c.entries, key)
	}
	c.mu.Unlock()
	result := fetch()
	result.at = time.Now()
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]linkPreviewResult{}
	}
	if len(c.entries) >= linkPreviewCacheEntries {
		for stale := range c.entries {
			delete(c.entries, stale)
			break
		}
	}
	c.entries[key] = result
	c.mu.Unlock()
	return result
}

var linkPreviewCache linkPreviewCacheStore

// linkPreviewPlainText is the text of an HTML fragment with its whitespace collapsed (an oEmbed blockquote becomes the post's text).
func linkPreviewPlainText(fragment string) string {
	var text strings.Builder
	tokens := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(text.String()), " ")
		case html.TextToken:
			text.Write(tokens.Text())
		}
	}
}

// fetchTweetPreview reads an X post through X's oEmbed endpoint (the post page itself is client-rendered and carries no
// tags for a link-preview fetch): "<author> on Twitter / X" over the post's text.
func fetchTweetPreview(ctx context.Context, target *url.URL) (linkPreviewMeta, bool) {
	endpoint := "https://publish.x.com/oembed?omit_script=1&dnt=true&url=" + url.QueryEscape(target.String())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return linkPreviewMeta{}, false
	}
	request.Header.Set("User-Agent", linkPreviewUserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := linkPreviewHTTPClient().Do(request)
	if err != nil {
		return linkPreviewMeta{}, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return linkPreviewMeta{}, false
	}
	var payload struct {
		AuthorName string `json:"author_name"`
		HTML       string `json:"html"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, linkPreviewMaxBytes)).Decode(&payload); err != nil || strings.TrimSpace(payload.AuthorName) == "" {
		return linkPreviewMeta{}, false
	}
	return linkPreviewMeta{
		title:       linkPreviewTruncate(strings.TrimSpace(payload.AuthorName)+" on Twitter / X", linkPreviewMaxTitleRunes),
		description: linkPreviewTruncate(linkPreviewPlainText(payload.HTML), linkPreviewMaxDescRunes),
		site:        "X",
	}, true
}

func fetchLinkPreview(ctx context.Context, target *url.URL, site string) linkPreviewResult {
	ctx, cancel := context.WithTimeout(ctx, 2*linkPreviewTimeout)
	defer cancel()
	if site == "X" {
		if meta, ok := fetchTweetPreview(ctx, target); ok {
			return linkPreviewResult{response: linkPreviewResponse{URL: target.String(), SiteName: meta.site, Title: meta.title, Description: meta.description}}
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return linkPreviewResult{err: errLinkPreviewInvalid}
	}
	request.Header.Set("User-Agent", linkPreviewUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	request.Header.Set("Accept-Language", "en")
	response, err := linkPreviewHTTPClient().Do(request)
	if err != nil {
		return linkPreviewResult{err: errLinkPreviewUpstream}
	}
	defer response.Body.Close()
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests || !strings.Contains(contentType, "html") {
		return linkPreviewResult{err: errLinkPreviewUpstream}
	}
	// A login wall or missing file still carries the site's own tags, which is what the card shows.
	final := target
	if response.Request != nil && response.Request.URL != nil {
		final = response.Request.URL
	}
	meta := parseLinkPreviewHTML(io.LimitReader(response.Body, linkPreviewMaxBytes), final)
	if meta.site == "" {
		meta.site = site
	}
	return linkPreviewResult{response: linkPreviewResponse{URL: target.String(), SiteName: meta.site, Title: meta.title, Description: meta.description, ImageURL: meta.image}}
}

// linkPreview serves GET /api/integrations/link-preview?url=<figma or x link>.
func (s *server) linkPreview(w http.ResponseWriter, r *http.Request) {
	target, site, err := parseLinkPreviewURL(r.URL.Query().Get("url"))
	if err != nil {
		writeFilePreviewError(w, err)
		return
	}
	result := linkPreviewCache.load(target.String(), func() linkPreviewResult {
		// The result is shared with concurrent callers, so it does not follow this request's cancellation.
		return fetchLinkPreview(context.WithoutCancel(r.Context()), target, site)
	})
	if result.err != nil {
		writeFilePreviewError(w, result.err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	writeJSON(w, http.StatusOK, result.response)
}
