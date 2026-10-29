//go:build securitye2e

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestSecurityFixtureOutboundAllowList(t *testing.T) {
	var hits atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Host != "www.figma.com" || r.URL.RequestURI() != "/design/fixture?node-id=1" {
			t.Errorf("fixture lost provider host or path: %s %s", r.Host, r.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fixture.Close()
	origin, _ := url.Parse(fixture.URL)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: securityFixtureTransport{origin: origin, transport: transport}}
	response, err := client.Get("https://www.figma.com/design/fixture?node-id=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Request.URL.Host != "www.figma.com" {
		t.Fatal("fixture response lost its public URL")
	}
	for _, target := range []string{
		fixture.URL, "https://127.0.0.1/", "https://169.254.169.254/",
		"https://evil.example/", "https://www.figma.com.evil.example/",
		"http://www.figma.com/", "https://www.figma.com:443/", "https://user@www.figma.com/",
	} {
		if response, err := client.Get(target); err == nil {
			_ = response.Body.Close()
			t.Errorf("fixture allowed %s", target)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("fixture received %d requests, want only the allowed one", hits.Load())
	}
}
