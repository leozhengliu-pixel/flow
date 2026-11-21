package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const mcpPing = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

// The prefix of a bearer token proves nothing on the routes that verify it
// inside the handler: unverified tokens spend the caller address's
// unauthenticated budget, so random tokens neither get a budget of their own
// nor grow the bucket map.
func TestPublicRateLimitIgnoresUnverifiedTokenPrefixes(t *testing.T) {
	for _, target := range []struct{ name, method, path, prefix string }{
		{"mcp api key", http.MethodPost, "/mcp", "flow_api_"},
		{"mcp readonly oauth", http.MethodPost, "/mcp/readonly", "flow_oauth_"},
		{"oauth token endpoint", http.MethodPost, "/api/oauth/token", "flow_api_"},
		{"oauth revoke", http.MethodPost, "/oauth/revoke", "flow_oauth_"},
		{"signed file", http.MethodGet, "/api/files/signed/x", "flow_api_"},
	} {
		t.Run(target.name, func(t *testing.T) {
			limiter := newAPIRateLimiter(defaultAPIRateLimitConfig(), nil)
			f := newPlatformFixture(t, limiter)
			limit := defaultAPIRateLimitConfig().Budgets[apiRateUnauthenticated].Requests
			first := 0
			for attempt := 1; attempt <= limit+100; attempt++ {
				request, err := http.NewRequest(target.method, f.host.URL+target.path, strings.NewReader(mcpPing))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Authorization", "Bearer "+target.prefix+"x"+strconv.Itoa(attempt))
				request.Header.Set("Content-Type", "application/json")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode == http.StatusTooManyRequests && first == 0 {
					first = attempt
				}
			}
			if first == 0 || first > limit+3 {
				t.Fatalf("first 429 at request %d of %d, want just after the anonymous limit %d", first, limit+100, limit)
			}
			buckets := limiter.backend.(*memoryTokenBuckets)
			buckets.mu.Lock()
			defer buckets.mu.Unlock()
			if len(buckets.buckets) > 4 {
				t.Fatalf("%d rate limit buckets after one address sent random tokens", len(buckets.buckets))
			}
		})
	}
}

// A key the MCP handler verified spends the user's key budget, the one the
// REST API uses, not the address's unauthenticated budget.
func TestMCPVerifiedKeySpendsAuthenticatedBudget(t *testing.T) {
	f := newPlatformFixture(t, newAPIRateLimiter(defaultAPIRateLimitConfig(), nil))
	token := f.apiKey(t, []string{"read"}, nil)
	response, _ := bearer(t, http.MethodPost, f.host.URL+"/mcp", token, nil, 0)
	request, _ := http.NewRequest(http.MethodPost, f.host.URL+"/mcp", strings.NewReader(mcpPing))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	var err error
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("X-RateLimit-Requests-Limit") != "2500" {
		t.Fatalf("mcp ping = %d, limit %q, want 200 under the authenticated 2500 budget", response.StatusCode, response.Header.Get("X-RateLimit-Requests-Limit"))
	}
	mcpRemaining, _ := strconv.Atoi(response.Header.Get("X-RateLimit-Requests-Remaining"))
	rest, _ := bearer(t, http.MethodGet, f.host.URL+"/api/viewer", token, nil, http.StatusOK)
	restRemaining, _ := strconv.Atoi(rest.Header.Get("X-RateLimit-Requests-Remaining"))
	if mcpRemaining == 0 || restRemaining != mcpRemaining-1 {
		t.Fatalf("REST remaining %d after MCP remaining %d: the budget is not shared (%s)", restRemaining, mcpRemaining, fmt.Sprint(rest.Header))
	}
}
