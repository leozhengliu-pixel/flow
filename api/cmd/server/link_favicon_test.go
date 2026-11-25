package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// pngBytes is a 1x1 PNG.
var pngBytes = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89}

func withPlainFaviconClient(t *testing.T) {
	t.Helper()
	previous := linkFaviconHTTPClient
	linkFaviconHTTPClient = func() *http.Client { return http.DefaultClient }
	t.Cleanup(func() { linkFaviconHTTPClient = previous })
}

func TestLinkFaviconUsesTheDeclaredIcon(t *testing.T) {
	withPlainFaviconClient(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/docs/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><link rel="mask-icon" href="/mask.svg"><link rel="shortcut icon" href="../assets/icon.png"></head><body></body></html>`))
	})
	mux.HandleFunc("/assets/icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(pngBytes)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	page, _ := url.Parse(server.URL + "/docs/page")
	icon := linkFaviconDataURL(context.Background(), page)
	if !strings.HasPrefix(icon, "data:image/png;base64,") {
		t.Fatalf("declared icon not used: %q", icon)
	}
}

func TestLinkFaviconFallsBackToFaviconICO(t *testing.T) {
	withPlainFaviconClient(t)
	ico := append([]byte{0, 0, 1, 0, 1, 0}, make([]byte, 32)...)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			_, _ = w.Write(ico)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>x</title></head><body></body></html>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	page, _ := url.Parse(server.URL + "/")
	if icon := linkFaviconDataURL(context.Background(), page); !strings.HasPrefix(icon, "data:image/x-icon;base64,") {
		t.Fatalf("favicon.ico fallback not used: %q", icon)
	}
}

func TestLinkFaviconRejectsOversizedAndNonImages(t *testing.T) {
	withPlainFaviconClient(t)
	icons := map[string][]byte{
		"oversized": append(append([]byte{}, pngBytes...), make([]byte, linkFaviconMaxIconBytes)...),
		"svg":       []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":      []byte(`<html><body>not found</body></html>`),
	}
	for name, body := range icons {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/favicon.ico" {
				_, _ = w.Write(body)
				return
			}
			http.NotFound(w, r)
		}))
		page, _ := url.Parse(server.URL + "/page")
		icon := linkFaviconDataURL(context.Background(), page)
		server.Close()
		if icon != "" {
			t.Fatalf("%s: unexpected icon %q", name, icon)
		}
	}
	// Control: the same server shape with a real PNG is accepted.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			_, _ = w.Write(pngBytes)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	page, _ := url.Parse(server.URL + "/page")
	if icon := linkFaviconDataURL(context.Background(), page); !strings.HasPrefix(icon, "data:image/png;base64,") {
		t.Fatalf("control icon missing: %q", icon)
	}
}

func TestLinkFaviconNeverReachesPrivateAddresses(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(pngBytes)
	}))
	defer server.Close()
	page, _ := url.Parse(server.URL + "/page")
	if icon := linkFaviconDataURL(context.Background(), page); icon != "" || hits != 0 {
		t.Fatalf("a loopback site was fetched (hits=%d, icon=%q)", hits, icon)
	}
}

func TestLinkFaviconSkipsBrandedSitesAndOtherSchemes(t *testing.T) {
	withPlainFaviconClient(t)
	for _, raw := range []string{"https://github.com/a/b/issues/1", "https://www.figma.com/file/x", "https://acme.slack.com/archives/C1/p1", "ftp://example.com/x", "javascript:alert(1)"} {
		page, _ := url.Parse(raw)
		if icon := linkFaviconDataURL(context.Background(), page); icon != "" {
			t.Fatalf("%s: unexpected icon", raw)
		}
	}
}
