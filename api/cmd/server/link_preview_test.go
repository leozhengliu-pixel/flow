package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseLinkPreviewURLAcceptsOnlyListedHosts(t *testing.T) {
	cases := map[string]string{
		"https://www.figma.com/proto/abc123/File?node-id=1-2#frag": "https://www.figma.com/proto/abc123/File?node-id=1-2",
		"http://figma.com/design/abc123/File":                      "https://figma.com/design/abc123/File",
		"https://x.com/jack/status/20":                             "https://x.com/jack/status/20",
		"https://Twitter.com/jack/status/20":                       "https://twitter.com/jack/status/20",
	}
	for raw, want := range cases {
		got, _, err := parseLinkPreviewURL(raw)
		if err != nil || got.String() != want {
			t.Fatalf("%s => %v %v, want %s", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"",
		"ftp://figma.com/file/a",
		"javascript:alert(1)",
		"https://evil.example/figma.com",
		"https://figma.com.evil.example/file/a",
		"https://user:pass@figma.com/file/a",
		"https://figma.com@169.254.169.254/file/a",
		"https://figma.com:8443/file/a",
		"https://127.0.0.1/file/a",
		"https://localhost/file/a",
		"https://[::1]/file/a",
		"https://example.com/",
		"https://figma.com/file/a b",
	} {
		if _, _, err := parseLinkPreviewURL(raw); err == nil {
			t.Fatalf("%q was accepted", raw)
		}
	}
}

func TestLinkPreviewRedirectsStayOnListedHosts(t *testing.T) {
	via := []*http.Request{{}}
	ok, _ := http.NewRequest(http.MethodGet, "https://www.figma.com/design/a", nil)
	if err := linkPreviewRedirect(ok, via); err != nil {
		t.Fatalf("figma redirect: %v", err)
	}
	for _, raw := range []string{"http://www.figma.com/design/a", "https://evil.example/", "https://127.0.0.1/", "https://www.figma.com:444/a", "https://u@www.figma.com/a"} {
		req, _ := http.NewRequest(http.MethodGet, raw, nil)
		if err := linkPreviewRedirect(req, via); err == nil {
			t.Fatalf("redirect to %s was allowed", raw)
		}
	}
	if err := linkPreviewRedirect(ok, make([]*http.Request, linkPreviewMaxRedirectHop)); err == nil {
		t.Fatal("redirect chain was unbounded")
	}
}

func TestParseLinkPreviewHTMLReadsOpenGraphThenFallbacks(t *testing.T) {
	base, _ := url.Parse("https://www.figma.com/proto/abc")
	og := `<html><head><title>Plain title</title><meta property="og:title" content="Figma: The collaborative canvas"><meta property="og:description" content="  From first   idea
to product  "><meta property="og:image" content="/og/cover.png"><meta property="og:site_name" content="Figma"><meta name="description" content="ignored"></head><body><meta property="og:title" content="late"></body></html>`
	meta := parseLinkPreviewHTML(strings.NewReader(og), base)
	if meta.title != "Figma: The collaborative canvas" || meta.description != "From first idea to product" || meta.image != "https://www.figma.com/og/cover.png" || meta.site != "Figma" {
		t.Fatalf("og meta: %#v", meta)
	}
	twitter := `<head><meta name="twitter:title" content="jack on X"><meta name="twitter:description" content="just setting up my twttr"><meta name="twitter:image" content="http://insecure.example/a.png"></head>`
	meta = parseLinkPreviewHTML(strings.NewReader(twitter), base)
	if meta.title != "jack on X" || meta.description != "just setting up my twttr" || meta.image != "" {
		t.Fatalf("twitter meta (insecure images are dropped): %#v", meta)
	}
	meta = parseLinkPreviewHTML(strings.NewReader(`<html><head><title>Only a title &amp; more</title></head>`), base)
	if meta.title != "Only a title & more" || meta.description != "" || meta.image != "" {
		t.Fatalf("title fallback: %#v", meta)
	}
	long := strings.Repeat("a", 600)
	meta = parseLinkPreviewHTML(strings.NewReader(`<meta property="og:description" content="`+long+`">`), base)
	if len([]rune(meta.description)) != linkPreviewMaxDescRunes || !strings.HasSuffix(meta.description, "…") {
		t.Fatalf("description is capped: %d", len([]rune(meta.description)))
	}
}

type linkPreviewRoundTripper struct {
	fake *httptest.Server
}

func (r linkPreviewRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	target, _ := url.Parse(r.fake.URL)
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func useFakeLinkPreview(t *testing.T, fake *httptest.Server) {
	t.Helper()
	original := linkPreviewHTTPClient
	linkPreviewHTTPClient = func() *http.Client { return &http.Client{Transport: linkPreviewRoundTripper{fake}} }
	linkPreviewCache.reset()
	t.Cleanup(func() {
		linkPreviewHTTPClient = original
		linkPreviewCache.reset()
	})
}

func linkPreviewRequest(t *testing.T, handler http.Handler, link string) (*httptest.ResponseRecorder, linkPreviewResponse, map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/link-preview?workspace=test-workspace&url="+url.QueryEscape(link), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var preview linkPreviewResponse
	var failure map[string]string
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
			t.Fatalf("decode preview: %v %s", err, rec.Body.String())
		}
	} else {
		_ = json.Unmarshal(rec.Body.Bytes(), &failure)
	}
	return rec, preview, failure
}

func TestLinkPreviewEndpointFetchesCachesAndRejects(t *testing.T) {
	_, handler, _ := gitlabTestServer(t)
	var hits atomic.Int32
	var userAgent atomic.Value
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		userAgent.Store(r.Header.Get("User-Agent"))
		switch {
		case strings.HasPrefix(r.URL.Path, "/proto/"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<head><meta property="og:title" content="Figma: The collaborative canvas"><meta property="og:description" content="Design together"><meta property="og:image" content="https://static.figma.com/cover.png"></head>`))
		case strings.HasPrefix(r.URL.Path, "/design/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "down", http.StatusBadGateway)
		}
	}))
	t.Cleanup(fake.Close)
	useFakeLinkPreview(t, fake)

	rec, preview, _ := linkPreviewRequest(t, handler, "https://www.figma.com/proto/abc123/File")
	if rec.Code != http.StatusOK || preview.Title != "Figma: The collaborative canvas" || preview.Description != "Design together" || preview.ImageURL != "https://static.figma.com/cover.png" || preview.SiteName != "Figma" || preview.URL != "https://www.figma.com/proto/abc123/File" {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "max-age") || !strings.HasPrefix(userAgent.Load().(string), "Mozilla/5.0 (compatible; FlowLinkPreview") {
		t.Fatalf("headers: %q %q", rec.Header().Get("Cache-Control"), userAgent.Load())
	}
	if rec, _, _ = linkPreviewRequest(t, handler, "https://www.figma.com/proto/abc123/File#other"); rec.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("cached fetch: %d hits=%d", rec.Code, hits.Load())
	}

	if rec, _, failure := linkPreviewRequest(t, handler, "https://www.figma.com/design/abc123/File"); rec.Code != http.StatusBadGateway || failure["code"] != "upstream" {
		t.Fatalf("non-html page: %d %v", rec.Code, failure)
	}
	if rec, _, failure := linkPreviewRequest(t, handler, "https://x.com/jack/status/20"); rec.Code != http.StatusBadGateway || failure["code"] != "upstream" {
		t.Fatalf("upstream failure: %d %v", rec.Code, failure)
	}
	before := hits.Load()
	for _, link := range []string{"", "https://example.com/", "https://127.0.0.1/proto/a", "https://figma.com@127.0.0.1/proto/a", "file:///etc/passwd"} {
		if rec, _, failure := linkPreviewRequest(t, handler, link); rec.Code != http.StatusBadRequest || failure["code"] != "invalid_url" {
			t.Fatalf("%q: %d %v", link, rec.Code, failure)
		}
	}
	if hits.Load() != before {
		t.Fatal("a rejected link reached the network")
	}
}

func TestLinkPreviewReadsXPostsThroughOEmbedAndFallsBackToThePage(t *testing.T) {
	_, handler, _ := gitlabTestServer(t)
	var oembed atomic.Int32
	var queried atomic.Value
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oembed" && strings.Contains(r.URL.Query().Get("url"), "/status/20"):
			oembed.Add(1)
			queried.Store(r.URL.Query().Get("url"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"author_name":"jack","html":"<blockquote class=\"twitter-tweet\"><p lang=\"en\" dir=\"ltr\">just setting up my twttr</p>&mdash; jack (@jack) <a href=\"https://twitter.com/jack/status/20\">March 21, 2006</a></blockquote>\n"}`))
		case r.URL.Path == "/oembed":
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<head><meta property="og:title" content="flow (@flow) on X"><meta property="og:description" content="Shipping"></head>`))
		}
	}))
	t.Cleanup(fake.Close)
	useFakeLinkPreview(t, fake)

	rec, preview, _ := linkPreviewRequest(t, handler, "https://twitter.com/jack/status/20")
	if rec.Code != http.StatusOK || preview.Title != "jack on Twitter / X" || preview.Description != "just setting up my twttr— jack (@jack) March 21, 2006" || preview.SiteName != "X" || preview.ImageURL != "" {
		t.Fatalf("oembed preview: %d %s", rec.Code, rec.Body.String())
	}
	if queried.Load() != "https://twitter.com/jack/status/20" || oembed.Load() != 1 {
		t.Fatalf("oembed request: %v %d", queried.Load(), oembed.Load())
	}
	rec, preview, _ = linkPreviewRequest(t, handler, "https://x.com/flow/status/99")
	if rec.Code != http.StatusOK || preview.Title != "flow (@flow) on X" || preview.Description != "Shipping" {
		t.Fatalf("page fallback: %d %s", rec.Code, rec.Body.String())
	}
	for _, redirect := range []string{"https://publish.x.com/oembed", "https://publish.twitter.com/oembed"} {
		req, _ := http.NewRequest(http.MethodGet, redirect, nil)
		if err := linkPreviewRedirect(req, []*http.Request{{}}); err != nil {
			t.Fatalf("oembed redirect %s: %v", redirect, err)
		}
	}
	if _, _, err := parseLinkPreviewURL("https://publish.x.com/oembed?url=https://x.com/a/status/1"); err == nil {
		t.Fatal("the oEmbed host was accepted as a link to preview")
	}
}
