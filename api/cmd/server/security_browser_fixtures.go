//go:build securitye2e

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Only the browser fixture binary includes this transport. Production builds
// retain the public-only dialer, which must never allow loopback previews.
func init() {
	raw := os.Getenv("FLOW_SECURITY_FIXTURE_ORIGIN")
	if raw == "" {
		return
	}
	origin, err := url.Parse(raw)
	if err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		panic("FLOW_SECURITY_FIXTURE_ORIGIN must be an HTTP loopback origin")
	}
	transport := securityFixtureTransport{origin: origin, transport: &http.Transport{Proxy: nil}}
	linkPreviewHTTPClient = func() *http.Client {
		return &http.Client{Transport: transport, Timeout: linkPreviewTimeout, CheckRedirect: linkPreviewRedirect}
	}
	filePreviewHTTPClient = func() *http.Client {
		return &http.Client{Transport: transport, Timeout: filePreviewTimeout, CheckRedirect: filePreviewSameHostRedirect}
	}
	gitlabFilePreviewHTTPClient = func(_ *server, _ context.Context, _ string) (*http.Client, error) {
		return &http.Client{Transport: transport, Timeout: 10 * time.Second}, nil
	}
}

type securityFixtureTransport struct {
	origin    *url.URL
	transport http.RoundTripper
}

func (t securityFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	allowed := map[string]bool{
		"www.figma.com": true, "x.com": true, "publish.x.com": true,
		"raw.githubusercontent.com": true, "api.github.com": true, "gitlab.com": true,
	}
	if request.URL.Scheme != "https" || request.URL.Port() != "" || request.URL.User != nil || !allowed[request.URL.Host] {
		return nil, fmt.Errorf("security fixture outbound host is not allowed")
	}
	clone := request.Clone(request.Context())
	clone.Host = request.URL.Host
	clone.URL.Scheme, clone.URL.Host = t.origin.Scheme, t.origin.Host
	response, err := t.transport.RoundTrip(clone)
	if response != nil {
		response.Request = request
	}
	return response, err
}
