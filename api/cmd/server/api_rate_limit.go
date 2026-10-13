package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// API rate limiting follows the reference app's published model: a leaky bucket per
// credential subject that refills at limit/period, a request budget and a
// complexity budget, X-RateLimit-* headers on every limited response and a
// RATELIMITED error when a budget is exhausted. First-party browser sessions
// are exempt unless FLOW_API_RATE_LIMIT_SESSION_REQUESTS is set.

type apiRateCategory string

const (
	apiRateAPIKey          apiRateCategory = "apiKey"
	apiRateOAuth           apiRateCategory = "oauth"
	apiRateUnauthenticated apiRateCategory = "unauthenticated"
	apiRateSession         apiRateCategory = "session"
)

type apiRateBudget struct {
	Requests   int
	Complexity int
}

type apiRateLimitConfig struct {
	Disabled      bool
	Period        time.Duration
	MaxComplexity int
	Budgets       map[apiRateCategory]apiRateBudget
}

// tokenBucketBackend is satisfied by the in-memory limiter and by the Redis
// coordinator, which shares buckets between API instances.
type tokenBucketBackend interface {
	TakeTokens(ctx context.Context, key string, capacity float64, period time.Duration, cost float64, now time.Time) (bool, float64, error)
}

type apiRateLimiter struct {
	config  apiRateLimitConfig
	backend tokenBucketBackend
	now     func() time.Time
}

func defaultAPIRateLimitConfig() apiRateLimitConfig {
	return apiRateLimitConfig{
		Period:        time.Hour,
		MaxComplexity: 10_000,
		Budgets: map[apiRateCategory]apiRateBudget{
			apiRateAPIKey:          {Requests: 2_500, Complexity: 3_000_000},
			apiRateOAuth:           {Requests: 5_000, Complexity: 2_000_000},
			apiRateUnauthenticated: {Requests: 600, Complexity: 100_000},
			apiRateSession:         {},
		},
	}
}

func apiRateLimitConfigFromEnv() apiRateLimitConfig {
	config := defaultAPIRateLimitConfig()
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_API_RATE_LIMIT"))) {
	case "off", "false", "disabled", "0":
		config.Disabled = true
	}
	read := func(name string, fallback int) int {
		value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
		if err != nil || value < 0 {
			return fallback
		}
		return value
	}
	config.MaxComplexity = read("FLOW_API_MAX_COMPLEXITY", config.MaxComplexity)
	for category, prefix := range map[apiRateCategory]string{apiRateAPIKey: "API_KEY", apiRateOAuth: "OAUTH", apiRateUnauthenticated: "UNAUTHENTICATED", apiRateSession: "SESSION"} {
		budget := config.Budgets[category]
		budget.Requests = read("FLOW_API_RATE_LIMIT_"+prefix+"_REQUESTS", budget.Requests)
		budget.Complexity = read("FLOW_API_RATE_LIMIT_"+prefix+"_COMPLEXITY", budget.Complexity)
		config.Budgets[category] = budget
	}
	return config
}

func newAPIRateLimiter(config apiRateLimitConfig, backend tokenBucketBackend) *apiRateLimiter {
	if backend == nil {
		backend = newMemoryTokenBuckets()
	}
	if config.Period <= 0 {
		config.Period = time.Hour
	}
	return &apiRateLimiter{config: config, backend: backend, now: time.Now}
}

// memoryTokenBuckets is the single-instance backend. Full buckets carry no
// state, so idle entries are swept once they would have refilled.
type memoryTokenBuckets struct {
	mu        sync.Mutex
	buckets   map[string]memoryTokenBucket
	lastSweep time.Time
}

type memoryTokenBucket struct {
	tokens  float64
	updated time.Time
	period  time.Duration
}

func newMemoryTokenBuckets() *memoryTokenBuckets {
	return &memoryTokenBuckets{buckets: map[string]memoryTokenBucket{}}
}

func (m *memoryTokenBuckets) TakeTokens(_ context.Context, key string, capacity float64, period time.Duration, cost float64, now time.Time) (bool, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now.Sub(m.lastSweep) > time.Minute || len(m.buckets) > 200_000 {
		for name, bucket := range m.buckets {
			if now.Sub(bucket.updated) >= bucket.period {
				delete(m.buckets, name)
			}
		}
		m.lastSweep = now
	}
	bucket, ok := m.buckets[key]
	if !ok {
		bucket = memoryTokenBucket{tokens: capacity, updated: now}
	}
	bucket.period = period
	if elapsed := now.Sub(bucket.updated); elapsed > 0 {
		bucket.tokens = math.Min(capacity, bucket.tokens+elapsed.Seconds()*capacity/period.Seconds())
		bucket.updated = now
	}
	allowed := bucket.tokens >= cost
	if allowed {
		bucket.tokens -= cost
	}
	m.buckets[key] = bucket
	return allowed, bucket.tokens, nil
}

// apiRateSubject identifies whose budget a request spends.
type apiRateSubject struct {
	Category apiRateCategory
	Key      string
}

// subjectForAuthenticatedRequest runs after authentication. Personal API keys
// share one budget per user; OAuth tokens per user (or app user)
// and application; sessions per user.
func subjectForAuthenticatedRequest(r *http.Request) (apiRateSubject, bool) {
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		user := authUser(r).ID
		if key.OAuthClientID != "" || key.AuthorizationID != "" || key.Actor == "app" || key.GrantType != "" {
			return apiRateSubject{Category: apiRateOAuth, Key: "oauth:" + key.OAuthClientID + ":" + user}, true
		}
		if user == "" {
			user = key.ID
		}
		return apiRateSubject{Category: apiRateAPIKey, Key: "apikey:" + user}, true
	}
	if user := authUser(r).ID; user != "" {
		return apiRateSubject{Category: apiRateSession, Key: "session:" + user}, true
	}
	return apiRateSubject{}, false
}

// subjectForPublicRequest covers routes that authenticate inside the handler
// (MCP, OAuth, upload tokens, signed file links). Bearer tokens there are
// bucketed by token hash; everything else by client address.
func subjectForPublicRequest(r *http.Request) (apiRateSubject, bool) {
	if !publicAPIRateLimitedPath(r.URL.Path) {
		return apiRateSubject{}, false
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		token := strings.TrimSpace(header[len("Bearer "):])
		digest := sha256.Sum256([]byte(token))
		switch {
		case strings.HasPrefix(token, "flow_api_"):
			return apiRateSubject{Category: apiRateAPIKey, Key: "token:" + hex.EncodeToString(digest[:16])}, true
		case strings.HasPrefix(token, "flow_oauth_"):
			return apiRateSubject{Category: apiRateOAuth, Key: "token:" + hex.EncodeToString(digest[:16])}, true
		}
	}
	return apiRateSubject{Category: apiRateUnauthenticated, Key: "ip:" + authClientAddress(r)}, true
}

// Public routes that integrations call without a session. Browser sign-in
// routes keep their own per-action limits (limitAuth); inbound provider
// webhooks are never limited here.
func publicAPIRateLimitedPath(path string) bool {
	return path == "/mcp" || path == "/mcp/readonly" || strings.HasPrefix(path, "/oauth/") || path == "/api/oauth/token" || strings.HasPrefix(path, "/api/mcp/uploads/") || strings.HasPrefix(path, "/api/files/uploads/") || strings.HasPrefix(path, "/api/files/signed/")
}

// apiRequestComplexity is the REST equivalent of the reference app's query complexity:
// one point per request plus one per object a list may return (the requested
// page size, default 50). Bootstrap-style snapshots cost a fixed large amount.
func apiRequestComplexity(r *http.Request) int {
	path := r.URL.Path
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if path == "/api/issues/batch" || path == "/api/issue-records/batch" {
			return 100
		}
		return 1
	}
	switch path {
	case "/api/bootstrap", "/api/issue-records/bootstrap":
		return 5_000
	case "/api/search", "/api/search/semantic":
		return 100
	}
	if strings.HasPrefix(path, "/api/exports/") && strings.HasSuffix(path, "/download") {
		return 5_000
	}
	query := r.URL.Query()
	size := -1
	for _, name := range []string{"first", "last", "limit"} {
		if value, err := strconv.Atoi(query.Get(name)); err == nil && value > 0 {
			size = value
			break
		}
	}
	if size < 0 {
		if !publicConnectionPath(path) {
			return 1
		}
		size = defaultPageSize
	}
	return 1 + min(size, maxPageSize)
}

// Endpoint limits (the reference app's X-RateLimit-Endpoint-*) for expensive operations.
type apiEndpointLimit struct {
	Name     string
	Requests int
}

func apiEndpointLimitFor(r *http.Request) (apiEndpointLimit, bool) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/api/files":
		return apiEndpointLimit{Name: "fileUpload", Requests: 1_000}, true
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/attachments/from-url"):
		return apiEndpointLimit{Name: "attachmentFromUrl", Requests: 250}, true
	case r.Method == http.MethodPost && path == "/api/exports":
		return apiEndpointLimit{Name: "exportCreate", Requests: 30}, true
	}
	return apiEndpointLimit{}, false
}

type apiRateDecision struct {
	header     http.Header
	limited    string
	retryAfter time.Duration
	status     int
	message    string
}

// check spends the request and complexity budgets of one subject and returns
// the headers to send. Backend failures fail open and are logged.
func (l *apiRateLimiter) check(ctx context.Context, r *http.Request, subject apiRateSubject) apiRateDecision {
	decision := apiRateDecision{header: http.Header{}}
	budget := l.config.Budgets[subject.Category]
	now := l.now()
	period := l.config.Period
	take := func(name string, limit int, cost float64) (bool, float64, bool) {
		if limit <= 0 {
			return true, 0, false
		}
		allowed, remaining, err := l.backend.TakeTokens(ctx, string(subject.Category)+":"+name+":"+subject.Key, float64(limit), period, cost, now)
		if err != nil {
			slog.WarnContext(ctx, "api rate limit backend unavailable", "error", err)
			return true, 0, false
		}
		return allowed, remaining, true
	}
	reset := func(limit int, remaining float64) string {
		missing := float64(limit) - remaining
		return strconv.FormatInt(now.Add(time.Duration(missing/float64(limit)*float64(period))).UnixMilli(), 10)
	}
	retry := func(limit int, remaining, cost float64) time.Duration {
		return time.Duration(math.Max(cost-remaining, 1) / float64(limit) * float64(period))
	}
	if allowed, remaining, ok := take("requests", budget.Requests, 1); ok {
		decision.header.Set("X-RateLimit-Requests-Limit", strconv.Itoa(budget.Requests))
		decision.header.Set("X-RateLimit-Requests-Remaining", strconv.Itoa(int(math.Floor(remaining))))
		decision.header.Set("X-RateLimit-Requests-Reset", reset(budget.Requests, remaining))
		if !allowed {
			decision.limited, decision.retryAfter = "requests", retry(budget.Requests, remaining, 1)
			decision.status, decision.message = http.StatusTooManyRequests, "Rate limit exceeded: too many API requests. Retry after the X-RateLimit-Requests-Reset time."
			return decision
		}
	}
	complexity := apiRequestComplexity(r)
	decision.header.Set("X-Complexity", strconv.Itoa(complexity))
	if l.config.MaxComplexity > 0 && complexity > l.config.MaxComplexity {
		decision.status, decision.message = http.StatusBadRequest, fmt.Sprintf("Request complexity %d exceeds the maximum of %d; request fewer items per page.", complexity, l.config.MaxComplexity)
		return decision
	}
	if allowed, remaining, ok := take("complexity", budget.Complexity, float64(complexity)); ok {
		decision.header.Set("X-RateLimit-Complexity-Limit", strconv.Itoa(budget.Complexity))
		decision.header.Set("X-RateLimit-Complexity-Remaining", strconv.Itoa(int(math.Floor(remaining))))
		decision.header.Set("X-RateLimit-Complexity-Reset", reset(budget.Complexity, remaining))
		if !allowed {
			decision.limited, decision.retryAfter = "complexity", retry(budget.Complexity, remaining, float64(complexity))
			decision.status, decision.message = http.StatusTooManyRequests, "Rate limit exceeded: request complexity budget exhausted. Request fewer items per page or retry later."
			return decision
		}
	}
	if endpoint, ok := apiEndpointLimitFor(r); ok && subject.Category != apiRateSession {
		if allowed, remaining, ok := take("endpoint:"+endpoint.Name, endpoint.Requests, 1); ok {
			decision.header.Set("X-RateLimit-Endpoint-Name", endpoint.Name)
			decision.header.Set("X-RateLimit-Endpoint-Requests-Limit", strconv.Itoa(endpoint.Requests))
			decision.header.Set("X-RateLimit-Endpoint-Requests-Remaining", strconv.Itoa(int(math.Floor(remaining))))
			decision.header.Set("X-RateLimit-Endpoint-Requests-Reset", reset(endpoint.Requests, remaining))
			if !allowed {
				decision.limited, decision.retryAfter = "endpoint", retry(endpoint.Requests, remaining, 1)
				decision.status, decision.message = http.StatusTooManyRequests, "Rate limit exceeded for "+endpoint.Name+". Retry after the X-RateLimit-Endpoint-Requests-Reset time."
			}
		}
	}
	return decision
}

func (l *apiRateLimiter) exempt(subject apiRateSubject) bool {
	budget := l.config.Budgets[subject.Category]
	return l.config.Disabled || budget.Requests <= 0 && budget.Complexity <= 0
}

func (d apiRateDecision) write(w http.ResponseWriter) bool {
	for name, values := range d.header {
		for _, value := range values {
			w.Header().Set(name, value)
		}
	}
	w.Header().Add("Access-Control-Expose-Headers", "X-RateLimit-Requests-Limit, X-RateLimit-Requests-Remaining, X-RateLimit-Requests-Reset, X-Complexity, X-RateLimit-Complexity-Limit, X-RateLimit-Complexity-Remaining, X-RateLimit-Complexity-Reset, Retry-After")
	if d.status == 0 {
		return true
	}
	code := apiErrorRateLimited
	if d.status == http.StatusBadRequest {
		code = apiErrorInvalidInput
	} else {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(d.retryAfter.Seconds())))))
	}
	writeAPIError(w, d.status, code, d.message)
	return false
}

func (s *server) apiRateLimits() *apiRateLimiter {
	s.apiRateLimiterOnce.Do(func() {
		if s.apiRateLimiter == nil {
			var backend tokenBucketBackend
			if s.coordinator != nil {
				backend = s.coordinator
			}
			s.apiRateLimiter = newAPIRateLimiter(apiRateLimitConfigFromEnv(), backend)
		}
	})
	return s.apiRateLimiter
}

func apiRateLimitedRoute(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/uploads/") || path == "/mcp" || path == "/mcp/readonly" || strings.HasPrefix(path, "/oauth/")
}

// limitAuthenticatedAPI runs after authentication so the bucket belongs to
// the resolved user or application rather than to an individual token.
func (s *server) limitAuthenticatedAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authDisabled || r.Method == http.MethodOptions || r.URL.Path == "/api/health" || !apiRateLimitedRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		limiter := s.apiRateLimits()
		subject, ok := subjectForAuthenticatedRequest(r)
		if !ok {
			subject, ok = subjectForPublicRequest(r)
		}
		if !ok || limiter.exempt(subject) {
			next.ServeHTTP(w, r)
			return
		}
		if !limiter.check(r.Context(), r, subject).write(w) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitFailedAuthentication spends the unauthenticated (per address) budget on
// rejected credentials so a client cannot probe tokens at full speed; once the
// budget is empty, further credentialed requests from that address get 429.
func (s *server) limitFailedAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authDisabled || strings.TrimSpace(r.Header.Get("Authorization")) == "" || !strings.HasPrefix(r.URL.Path, "/api/") || publicAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		limiter := s.apiRateLimits()
		subject := apiRateSubject{Category: apiRateUnauthenticated, Key: "failed-auth:" + authClientAddress(r)}
		budget := limiter.config.Budgets[apiRateUnauthenticated]
		if limiter.exempt(subject) || budget.Requests <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		key := string(subject.Category) + ":requests:" + subject.Key
		if _, remaining, err := limiter.backend.TakeTokens(r.Context(), key, float64(budget.Requests), limiter.config.Period, 0, limiter.now()); err == nil && remaining < 1 {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(limiter.config.Period.Seconds()/float64(budget.Requests)))))
			writeAPIError(w, http.StatusTooManyRequests, apiErrorRateLimited, "Too many requests with invalid credentials. Retry later.")
			return
		}
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status == http.StatusUnauthorized {
			_, _, _ = limiter.backend.TakeTokens(context.WithoutCancel(r.Context()), key, float64(budget.Requests), limiter.config.Period, 1, limiter.now())
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}
func (w *statusRecorder) Flush() { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
