package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestProvidersParseResults(t *testing.T) {
	var seen *http.Request
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(context.Background())
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/search":
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"results":[{"title":"Tavily hit","url":"https://example.com/a","content":"About A"},{"title":"bad","url":"javascript:alert(1)","content":"x"},{"title":"dup","url":"https://example.com/a","content":"x"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"title":"Searx hit","url":"https://example.org/","content":"From searx"}]}`))
		case "/res/v1/web/search":
			_, _ = w.Write([]byte(`{"web":{"results":[{"title":"Brave <strong>hit</strong>","url":"https://example.net/","description":"Uses <strong>bold</strong> &amp; more"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tavilyProvider := New(Config{Provider: "tavily", APIKey: "tvly-key", URL: server.URL}, server.Client())
	results, err := tavilyProvider.Search(context.Background(), "flow loops", 3)
	if err != nil || len(results) != 1 || results[0].Title != "Tavily hit" || results[0].Snippet != "About A" {
		t.Fatalf("tavily = %#v, %v", results, err)
	}
	if seen.Header.Get("Authorization") != "Bearer tvly-key" || body["query"] != "flow loops" || body["max_results"] != float64(3) {
		t.Fatalf("tavily request header=%q body=%v", seen.Header.Get("Authorization"), body)
	}

	braveProvider := New(Config{Provider: "brave", APIKey: "brave-key", URL: server.URL}, server.Client())
	results, err = braveProvider.Search(context.Background(), "flow", 50)
	if err != nil || len(results) != 1 || results[0].Title != "Brave hit" || results[0].Snippet != "Uses bold & more" {
		t.Fatalf("brave = %#v, %v", results, err)
	}
	if seen.Header.Get("X-Subscription-Token") != "brave-key" || seen.URL.Query().Get("q") != "flow" || seen.URL.Query().Get("count") != "10" {
		t.Fatalf("brave request = %v %v", seen.Header, seen.URL)
	}

	searx := New(Config{Provider: "searxng", URL: server.URL + "/"}, server.Client())
	results, err = searx.Search(context.Background(), "flow", 0)
	if err != nil || len(results) != 1 || results[0].URL != "https://example.org/" {
		t.Fatalf("searxng = %#v, %v", results, err)
	}
	if seen.URL.Query().Get("format") != "json" {
		t.Fatalf("searxng query = %v", seen.URL)
	}
	if New(Config{}, nil) != nil || New(Config{Provider: "searxng"}, nil) != nil {
		t.Fatal("disabled providers must be nil")
	}
}

func TestProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota", http.StatusTooManyRequests)
	}))
	defer server.Close()
	_, err := New(Config{Provider: "brave", APIKey: "k", URL: server.URL}, server.Client()).Search(context.Background(), "x", 5)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %v", err)
	}
}

func TestPublicAddr(t *testing.T) {
	for _, blocked := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "224.0.0.1", "255.255.255.255", "64:ff9b::7f00:1"} {
		if PublicAddr(netip.MustParseAddr(blocked)) {
			t.Errorf("%s should be blocked", blocked)
		}
	}
	for _, public := range []string{"93.184.216.34", "2606:4700::1111", "8.8.8.8"} {
		if !PublicAddr(netip.MustParseAddr(public)) {
			t.Errorf("%s should be allowed", public)
		}
	}
}

func TestFetchBlocksInternalAddressesAndSchemes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer server.Close()
	fetcher := &Fetcher{}
	if _, err := fetcher.Fetch(context.Background(), server.URL); err == nil || !strings.Contains(err.Error(), "internal address") && !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("loopback fetch error = %v", err)
	}
	// A hostname that resolves to loopback is refused at connect time.
	if _, err := fetcher.Fetch(context.Background(), strings.Replace(server.URL, "127.0.0.1", "localhost", 1)); err == nil || !strings.Contains(err.Error(), "internal address") {
		t.Fatalf("localhost fetch error = %v", err)
	}
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com", "http://user:pw@example.com/", "http://169.254.169.254/latest/meta-data"} {
		if _, err := fetcher.Fetch(context.Background(), raw); err == nil {
			t.Fatalf("%s was fetched", raw)
		}
	}
}

func loopbackOnly(addr netip.Addr) bool { return addr.Unmap() == netip.MustParseAddr("127.0.0.1") }

func TestFetchRedirectsAreRecheckedAndLimited(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal":
			http.Redirect(w, r, "http://10.0.0.1/admin", http.StatusFound)
		case "/scheme":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/chain":
			hops := len(r.URL.Query().Get("n"))
			if hops < 4 {
				http.Redirect(w, r, "/chain?n="+strings.Repeat("x", hops+1), http.StatusFound)
				return
			}
			_, _ = w.Write([]byte("end"))
		case "/three":
			hops := len(r.URL.Query().Get("n"))
			if hops < 3 {
				http.Redirect(w, r, "/three?n="+strings.Repeat("x", hops+1), http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("arrived"))
		}
	}))
	defer server.Close()
	fetcher := &Fetcher{AllowAddr: loopbackOnly}
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/internal"); err == nil || !strings.Contains(err.Error(), "internal address") {
		t.Fatalf("redirect to internal address error = %v", err)
	}
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/scheme"); err == nil || !strings.Contains(err.Error(), "http and https") {
		t.Fatalf("redirect to file error = %v", err)
	}
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/chain"); err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("4 redirects error = %v", err)
	}
	page, err := fetcher.Fetch(context.Background(), server.URL+"/three")
	if err != nil || page.Text != "arrived" || !strings.Contains(page.FinalURL, "n=xxx") {
		t.Fatalf("3 redirects = %#v, %v", page, err)
	}
}

func TestFetchReadableTextAndLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>Release &amp; notes</title><style>body{color:red}</style><script>alert("x")</script></head>
<body><nav>Home | About</nav><main><h1>Version 2</h1><p>Loops can   now <b>search</b> the web.</p><ul><li>First</li><li>Second</li></ul></main><footer>© Example</footer></body></html>`))
		case "/big":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(strings.Repeat("a", 3<<20)))
		case "/image":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG"))
		}
	}))
	defer server.Close()
	fetcher := &Fetcher{AllowAddr: loopbackOnly}
	page, err := fetcher.Fetch(context.Background(), server.URL+"/page")
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Release & notes" || !strings.Contains(page.Text, "Version 2") || !strings.Contains(page.Text, "Loops can now search the web.") || !strings.Contains(page.Text, "- First") {
		t.Fatalf("page = %#v", page)
	}
	for _, hidden := range []string{"alert", "color:red", "Home | About", "© Example"} {
		if strings.Contains(page.Text, hidden) {
			t.Fatalf("page text kept %q: %q", hidden, page.Text)
		}
	}
	big, err := fetcher.Fetch(context.Background(), server.URL+"/big")
	if err != nil || !big.Truncated || len(big.Text) != 60000 {
		t.Fatalf("big page = %d truncated=%v err=%v", len(big.Text), big.Truncated, err)
	}
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/image"); err == nil || !strings.Contains(err.Error(), "image/png") {
		t.Fatalf("image error = %v", err)
	}
}
