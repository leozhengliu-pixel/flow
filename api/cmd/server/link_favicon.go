package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// A link attached to an issue shows its site's favicon, like the design reference. The icon is fetched once, when the
// link is created, through the SSRF-safe outbound client (public addresses only, a few redirects, http or https), and
// kept on the attachment as a small data URL so it needs no stored object of its own. Nothing from the request is logged.

const (
	linkFaviconTimeout       = 5 * time.Second
	linkFaviconMaxPageBytes  = 256 << 10
	linkFaviconMaxIconBytes  = 24 << 10
	linkFaviconMaxRedirects  = 3
	linkFaviconMaxCandidates = 4
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

// linkFaviconDataURL returns the favicon of the page's site as a data URL, or "" when the site has none that is a small
// PNG, JPEG, GIF, WebP or ICO image.
func linkFaviconDataURL(ctx context.Context, page *url.URL) string {
	if page == nil || (page.Scheme != "http" && page.Scheme != "https") || page.Host == "" || page.User != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(page.Hostname()), "www.")
	if linkFaviconSkipHosts[host] || strings.HasSuffix(host, ".slack.com") {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, linkFaviconTimeout)
	defer cancel()
	client := linkFaviconHTTPClient()
	candidates := linkFaviconCandidates(ctx, client, page)
	if len(candidates) > linkFaviconMaxCandidates {
		candidates = candidates[:linkFaviconMaxCandidates]
	}
	for _, candidate := range candidates {
		if icon := fetchLinkFavicon(ctx, client, candidate); icon != "" {
			return icon
		}
	}
	return ""
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

func fetchLinkFavicon(ctx context.Context, client *http.Client, icon *url.URL) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, icon.String(), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", linkFaviconUserAgent)
	req.Header.Set("Accept", "image/*")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, linkFaviconMaxIconBytes+1))
	if err != nil || len(body) == 0 || len(body) > linkFaviconMaxIconBytes {
		return ""
	}
	// The bytes decide the type, not the header: an SVG or an HTML error page is never kept.
	switch contentType := http.DetectContentType(body); contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon":
		return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(body)
	}
	return ""
}
