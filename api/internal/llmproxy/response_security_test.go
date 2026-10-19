package llmproxy

import (
	"io"
	"net/http"
	"strconv"
	"testing"
)

func TestUpstreamResponsesCannotServeActiveDocuments(t *testing.T) {
	const markup = `<script>alert(document.domain)</script>`
	for _, test := range []struct {
		name, contentType, body, wantType string
	}{
		{"JSON", "application/json; charset=utf-8", `{"error":"` + markup + `"}`, "application/json; charset=utf-8"},
		{"vendor JSON", "application/problem+json", `{"detail":"` + markup + `"}`, "application/problem+json"},
		{"SSE", "text/event-stream; charset=utf-8", "data: " + markup + "\n\n", "text/event-stream; charset=utf-8"},
		{"HTML", "text/html", markup, "text/plain; charset=utf-8"},
		{"XHTML", "application/xhtml+xml", markup, "text/plain; charset=utf-8"},
		{"SVG", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`, "text/plain; charset=utf-8"},
		{"missing type", "", markup, "text/plain; charset=utf-8"},
		{"invalid type", "application/json; charset", markup, "text/plain; charset=utf-8"},
	} {
		for _, status := range []int{http.StatusOK, http.StatusBadGateway} {
			t.Run(test.name+"/"+strconv.Itoa(status), func(t *testing.T) {
				upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
					w.Header().Set("Content-Type", test.contentType)
					w.Header().Set("X-Content-Type-Options", "unsafe-upstream-value")
					w.Header().Set("Content-Security-Policy", "script-src 'unsafe-inline'")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, test.body)
				})
				cfg := baseConfig()
				cfg.AnthropicBaseURL = upstream.URL
				h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})
				response := h.do(t, http.MethodPost, PathPrefix+"anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5", "messages": []any{}}, bearer())
				body := readAll(t, response)
				if response.StatusCode != status || body != test.body {
					t.Fatalf("upstream response changed: status=%d body=%q", response.StatusCode, body)
				}
				if response.Header.Get("Content-Type") != test.wantType || response.Header.Get("X-Content-Type-Options") != "nosniff" || response.Header.Get("Content-Security-Policy") != "sandbox; default-src 'none'" {
					t.Fatalf("unsafe response headers: %v", response.Header)
				}
			})
		}
	}
}
