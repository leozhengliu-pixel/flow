// Package llmproxy is the LLM proxy sandboxes call instead of model providers
// (track E, docs/coding-sessions-design.md §12). It authenticates the sandbox
// token, enforces the session's model and spend limit, injects the real
// provider key, streams the upstream response through unchanged, and meters
// token usage from the response (Anthropic message_start/message_delta usage,
// OpenAI Responses response.completed usage, chat completions usage chunk).
package llmproxy

import (
	"context"
	"errors"
	"net/http"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// Upstreams.
const (
	UpstreamAnthropic  = "anthropic"
	UpstreamOpenAI     = "openai"
	UpstreamOpenSource = "opensource"
)

var (
	// ErrSpendLimit makes the proxy answer 402 and the session fail with quotaExceeded.
	ErrSpendLimit = errors.New("llmproxy: spend limit reached")
	// ErrModelNotAllowed makes the proxy answer 403 (a model outside the session's selection).
	ErrModelNotAllowed = errors.New("llmproxy: model not allowed for this session")
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

// Proxy serves /internal/llm-proxy/{upstream}/...
type Proxy struct {
	Config config.CodingLLMConfig
	Auth   Authenticator
	Meter  Meter
	Client *http.Client
}

// ServeHTTP is implemented by track E.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "LLM proxy is not implemented yet", http.StatusNotImplemented)
}

// CostMicros prices usage in micro-USD from FLOW_CODING_MODEL_PRICES.
func CostMicros(price config.ModelPrice, input, output, cacheRead, cacheWrite int64) int64 {
	usd := (float64(input)*price.Input + float64(output)*price.Output + float64(cacheRead)*price.CacheRead + float64(cacheWrite)*price.CacheWrite) / 1e6
	return int64(usd * 1e6)
}
