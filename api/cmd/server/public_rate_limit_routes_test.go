package main

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// publicRouteRequest sends an unauthenticated request the way an MCP client
// does before it holds a token, or the OAuth code exchange that issues one.
func publicRouteRequest(t *testing.T, host, path string) *http.Response {
	t.Helper()
	var request *http.Request
	var err error
	if path == "/api/oauth/token" {
		form := url.Values{"grant_type": {"authorization_code"}, "code": {"unknown"}, "client_id": {"unknown"}}
		request, err = http.NewRequest(http.MethodPost, host+path, strings.NewReader(form.Encode()))
		if err == nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		request, err = http.NewRequest(http.MethodPost, host+path, strings.NewReader(mcpPing))
		if err == nil {
			request.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response
}

func smallPublicBudget() apiRateLimitConfig {
	config := defaultAPIRateLimitConfig()
	budgets := map[apiRateCategory]apiRateBudget{}
	for category, budget := range config.Budgets {
		budgets[category] = budget
	}
	budgets[apiRateUnauthenticated] = apiRateBudget{Requests: 5, Complexity: budgets[apiRateUnauthenticated].Complexity}
	config.Budgets = budgets
	return config
}

func exhaustMCP(t *testing.T, host string) *http.Response {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		if response := publicRouteRequest(t, host, "/mcp"); response.StatusCode == http.StatusTooManyRequests {
			return response
		}
	}
	t.Fatal("MCP requests without a token were never limited")
	return nil
}

// An MCP client retrying /mcp before it holds a token must not use up the
// OAuth exchange that issues the token. Each route keeps its own budget and
// reports its own Retry-After.
func TestPublicRateLimitKeepsOAuthAvailableWhenMCPIsExhausted(t *testing.T) {
	f := newPlatformFixture(t, newAPIRateLimiter(smallPublicBudget(), nil))
	limited := exhaustMCP(t, f.host.URL)
	if limited.Header.Get("Retry-After") == "" {
		t.Fatal("MCP 429 has no Retry-After")
	}
	if response := publicRouteRequest(t, f.host.URL, "/api/oauth/token"); response.StatusCode == http.StatusTooManyRequests {
		t.Fatal("the OAuth token exchange was limited by the MCP budget")
	}
	if response := publicRouteRequest(t, f.host.URL, "/mcp"); response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("MCP after exhaustion = %d, want 429", response.StatusCode)
	}
}

// scriptedBuckets is a stub limiter backend: listed key fragments are
// exhausted with the given remaining tokens, every other key is available.
type scriptedBuckets struct {
	mu        sync.Mutex
	exhausted map[string]float64
	keys      []string
}

func (b *scriptedBuckets) TakeTokens(_ context.Context, key string, capacity float64, _ time.Duration, _ float64, _ time.Time) (bool, float64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keys = append(b.keys, key)
	for fragment, remaining := range b.exhausted {
		if strings.Contains(key, fragment) {
			return false, remaining, nil
		}
	}
	return true, capacity - 1, nil
}

func TestPublicRateLimitReportsEachRouteBudget(t *testing.T) {
	config := smallPublicBudget()
	limit := config.Budgets[apiRateUnauthenticated].Requests
	for _, tc := range []struct {
		name, exhausted, limitedPath, openPath string
		remaining                              float64
	}{
		{"mcp exhausted", ":ip:mcp:", "/mcp", "/api/oauth/token", 0},
		{"oauth exhausted", ":ip:oauth:", "/api/oauth/token", "/mcp", 0.25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &scriptedBuckets{exhausted: map[string]float64{"requests" + tc.exhausted: tc.remaining}}
			f := newPlatformFixture(t, newAPIRateLimiter(config, backend))
			limited := publicRouteRequest(t, f.host.URL, tc.limitedPath)
			if limited.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("%s = %d, want 429", tc.limitedPath, limited.StatusCode)
			}
			// Retry-After comes from the limited route's own bucket.
			want := max(1, int(math.Ceil(math.Max(1-tc.remaining, 1)/float64(limit)*config.Period.Seconds())))
			if got, _ := strconv.Atoi(limited.Header.Get("Retry-After")); got != want {
				t.Fatalf("%s Retry-After = %q, want %d", tc.limitedPath, limited.Header.Get("Retry-After"), want)
			}
			if remaining := limited.Header.Get("X-RateLimit-Requests-Remaining"); remaining != strconv.Itoa(int(tc.remaining)) {
				t.Fatalf("%s remaining = %q", tc.limitedPath, remaining)
			}
			if open := publicRouteRequest(t, f.host.URL, tc.openPath); open.StatusCode == http.StatusTooManyRequests {
				t.Fatalf("%s was limited by the other route's budget", tc.openPath)
			}
		})
	}
}

// Replicas share buckets through the limiter backend: an MCP budget used up
// on one replica limits MCP on the other, while OAuth stays available on both.
func TestPublicRateLimitRouteBudgetsAcrossReplicas(t *testing.T) {
	config := smallPublicBudget()
	first := newAPIRateLimiter(config, nil)
	second := newAPIRateLimiter(config, first.backend)
	a, b := newPlatformFixture(t, first), newPlatformFixture(t, second)
	exhaustMCP(t, a.host.URL)
	if response := publicRouteRequest(t, b.host.URL, "/mcp"); response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("MCP on the second replica = %d, want 429 from the shared bucket", response.StatusCode)
	}
	for _, host := range []string{a.host.URL, b.host.URL} {
		if response := publicRouteRequest(t, host, "/api/oauth/token"); response.StatusCode == http.StatusTooManyRequests {
			t.Fatal("the OAuth token exchange was limited by the shared MCP budget")
		}
	}
}

func TestPublicRateRouteClasses(t *testing.T) {
	for path, want := range map[string]string{
		"/mcp": "mcp", "/mcp/readonly": "mcp",
		"/oauth/authorize": "oauth", "/oauth/revoke": "oauth", "/api/oauth/token": "oauth",
		"/api/files/signed/x": "signed", "/api/mcp/uploads/x": "upload", "/api/files/uploads/x": "upload",
	} {
		if got := publicRateRoute(path); got != want {
			t.Errorf("publicRateRoute(%q) = %q, want %q", path, got, want)
		}
	}
}
