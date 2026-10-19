// Package llmproxy is the LLM proxy sandboxes call instead of model providers
// (track E, docs/coding-sessions-design.md §12). It authenticates the sandbox
// token, enforces the session's model and spend limit, injects the real
// provider key, streams the upstream response through unchanged, and meters
// token usage from the response (Anthropic message_start/message_delta usage,
// OpenAI Responses response.completed usage, chat completions usage chunk).
//
// Request flow (ServeHTTP):
//
//  1. Route: /internal/llm-proxy/{anthropic|openai|opensource}/v1/...
//  2. Authenticate the sandbox token (Authorization: Bearer or x-api-key).
//  3. Per-session rate limit and in-flight cap (429).
//  4. Spend check (402 {"error":{"type":"quota_exceeded"}}).
//  5. Body size limit (413), model enforcement and rewrite, usage options.
//  6. Forward with the real upstream key; Auto sessions retry once on the next
//     catalog model when the upstream reports the model missing or overloaded.
//  7. Stream the response back unbuffered while metering usage, then record
//     a coding_usage_records row priced with FLOW_CODING_MODEL_PRICES.
//
// Logs carry ids, model, status, durations and token counts only: never
// prompt or completion content, tokens or keys.
package llmproxy

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// Upstreams.
const (
	UpstreamAnthropic  = "anthropic"
	UpstreamOpenAI     = "openai"
	UpstreamOpenSource = "opensource"
)

// Route prefix served by Proxy.
const PathPrefix = "/internal/llm-proxy/"

var (
	// ErrSpendLimit makes the proxy answer 402 and the session fail with quotaExceeded.
	ErrSpendLimit = errors.New("llmproxy: spend limit reached")
	// ErrModelNotAllowed makes the proxy answer 403 (a model outside the session's selection).
	ErrModelNotAllowed = errors.New("llmproxy: model not allowed for this session")
	// ErrUnauthenticated makes the proxy answer 401: the token is unknown,
	// expired or revoked, its sandbox is not running, or its session is terminal.
	ErrUnauthenticated = errors.New("llmproxy: invalid sandbox token")
)

// Caller is an authenticated sandbox.
type Caller struct {
	WorkspaceKey string
	SessionID    string
	SandboxID    string
	UserID       string
	LoopID       string
	Harness      string
	Model        string // the resolved model; requests for other models are rewritten or refused
	// AutoModel is true when the session's model selection is "auto": the
	// proxy may then fall back to the next catalog model (§12 Auto routing).
	AutoModel bool
}

// Authenticator resolves the bearer/x-api-key sandbox token.
type Authenticator interface {
	AuthenticateLLMCaller(ctx context.Context, token string) (Caller, error)
}

// Meter records usage and checks spend before a request.
type Meter interface {
	CheckSpend(ctx context.Context, caller Caller) error
	RecordUsage(ctx context.Context, record domain.CodingUsageRecord) error
}

// ModelSwitcher is told when an Auto session falls back to another model, so
// the switch is persisted on the session and shown as a thought activity
// ("Switched to X"). Optional.
type ModelSwitcher interface {
	RecordModelSwitch(ctx context.Context, caller Caller, from, to, reason string) error
}

// Limits bounds each request and each session. Zero values take the defaults.
type Limits struct {
	// MaxBodyBytes caps request bodies (default 32 MiB, §12).
	MaxBodyBytes int64
	// RequestsPerMinute and Burst rate-limit each session (defaults 240 / 60).
	RequestsPerMinute int
	Burst             int
	// MaxConcurrent caps in-flight requests per session (default 16).
	MaxConcurrent int
	// MaxMeteredBytes caps how much of a non-streamed response body is kept to
	// read its usage (default 16 MiB); larger bodies pass through unmetered.
	MaxMeteredBytes int64
}

// Default limits.
const (
	DefaultMaxBodyBytes      = 32 << 20
	DefaultRequestsPerMinute = 240
	DefaultBurst             = 60
	DefaultMaxConcurrent     = 16
	DefaultMaxMeteredBytes   = 16 << 20
	defaultTimeout           = 10 * time.Minute
)

func (l Limits) withDefaults() Limits {
	if l.MaxBodyBytes <= 0 {
		l.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if l.RequestsPerMinute <= 0 {
		l.RequestsPerMinute = DefaultRequestsPerMinute
	}
	if l.Burst <= 0 {
		l.Burst = DefaultBurst
	}
	if l.MaxConcurrent <= 0 {
		l.MaxConcurrent = DefaultMaxConcurrent
	}
	if l.MaxMeteredBytes <= 0 {
		l.MaxMeteredBytes = DefaultMaxMeteredBytes
	}
	return l
}

// Proxy serves /internal/llm-proxy/{upstream}/...
type Proxy struct {
	Config config.CodingLLMConfig
	Auth   Authenticator
	Meter  Meter
	Client *http.Client

	// Switcher records Auto fallbacks (optional).
	Switcher ModelSwitcher
	// Limits bounds requests and sessions (zero = defaults).
	Limits Limits
	// Logger receives structured request logs (default slog.Default()).
	Logger *slog.Logger
	// Now is the clock (default time.Now).
	Now func() time.Time

	initOnce  sync.Once
	limits    Limits
	limiter   *sessionLimiter
	overrides sync.Map // session id -> model chosen by an Auto fallback
	switched  sync.Map // session|from|to -> struct{}: switch already recorded
}

func (p *Proxy) init() {
	p.initOnce.Do(func() {
		p.limits = p.Limits.withDefaults()
		p.limiter = newSessionLimiter(p.limits.RequestsPerMinute, p.limits.Burst, p.limits.MaxConcurrent, p.now)
	})
}

func (p *Proxy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Proxy) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

func (p *Proxy) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func (p *Proxy) timeout() time.Duration {
	if p.Config.Timeout > 0 {
		return p.Config.Timeout
	}
	return defaultTimeout
}

// CostMicros prices usage in micro-USD from FLOW_CODING_MODEL_PRICES.
func CostMicros(price config.ModelPrice, input, output, cacheRead, cacheWrite int64) int64 {
	// USD per million tokens × tokens = micro-USD.
	micros := float64(input)*price.Input + float64(output)*price.Output + float64(cacheRead)*price.CacheRead + float64(cacheWrite)*price.CacheWrite
	return int64(math.Round(micros))
}
