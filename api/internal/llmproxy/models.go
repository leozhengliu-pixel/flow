package llmproxy

import (
	"slices"
	"strings"
)

// HarnessUpstream is the only upstream a harness may call: Claude Code talks
// Anthropic Messages, Codex OpenAI, and the open-source and Pi harnesses the
// OpenAI-compatible upstream.
func HarnessUpstream(harness string) string {
	switch harness {
	case "claudeCode", "claude":
		return UpstreamAnthropic
	case "openSource", "pi":
		return UpstreamOpenSource
	default:
		return UpstreamOpenAI
	}
}

// catalog returns the harness's configured models (the first is Auto's pick).
func (p *Proxy) catalog(caller Caller) []string {
	return p.Config.ModelsFor(normalizeHarness(caller.Harness))
}

func normalizeHarness(harness string) string {
	switch harness {
	case "claude":
		return "claudeCode"
	case "", "auto":
		return "codex"
	}
	return harness
}

// sessionModel is the model the session may use right now: an Auto fallback
// chosen earlier in this process, else the resolved model, else Auto's pick.
func (p *Proxy) sessionModel(caller Caller) string {
	if override, ok := p.overrides.Load(caller.SessionID); ok {
		return override.(string)
	}
	if caller.Model != "" {
		return caller.Model
	}
	if models := p.catalog(caller); len(models) > 0 {
		return models[0]
	}
	return ""
}

// isSmallModel is the fixed allowlist of internal small models: Claude Code
// sends background calls (titles, summaries, command checks) to a Haiku model.
func isSmallModel(upstream, model string) bool {
	return upstream == UpstreamAnthropic && strings.Contains(strings.ToLower(model), "haiku")
}

// cheapestModel picks the cheapest priced model of the catalog by input plus
// output price; with no prices it returns fallback.
func (p *Proxy) cheapestModel(models []string, fallback string) string {
	best, bestPrice := "", 0.0
	for _, model := range models {
		price, ok := p.Config.Prices[model]
		if !ok {
			continue
		}
		total := price.Input + price.Output
		if best == "" || total < bestPrice {
			best, bestPrice = model, total
		}
	}
	if best == "" {
		return fallback
	}
	return best
}

// modelDecision is how a requested model was mapped.
type modelDecision struct {
	Requested string
	Model     string // sent upstream
	Rewritten bool
	Small     bool // a small-model rewrite: never falls back
}

// enforceModel maps the requested model to the one the session may use
// (§12): the session's model passes; small background models are rewritten to
// the cheapest configured model of the harness; anything else is rewritten to
// the session's model. It fails only when the session has no usable model.
func (p *Proxy) enforceModel(caller Caller, upstream, requested string) (modelDecision, error) {
	session := p.sessionModel(caller)
	if session == "" {
		return modelDecision{}, ErrModelNotAllowed
	}
	decision := modelDecision{Requested: requested, Model: session}
	switch {
	case requested == session:
	case isSmallModel(upstream, requested):
		decision.Model, decision.Small = p.cheapestModel(p.catalog(caller), session), true
	}
	decision.Rewritten = decision.Model != requested
	return decision, nil
}

// nextModel is the catalog entry after current, for Auto fallback.
func (p *Proxy) nextModel(caller Caller, current string) string {
	models := p.catalog(caller)
	index := slices.Index(models, current)
	if index < 0 {
		if len(models) > 0 && models[0] != current {
			return models[0]
		}
		return ""
	}
	if index+1 < len(models) {
		return models[index+1]
	}
	return ""
}

// listedModels is what GET /v1/models returns: the session's model first,
// then the rest of the catalog the proxy would accept (and rewrite).
func (p *Proxy) listedModels(caller Caller) []string {
	result := []string{}
	if session := p.sessionModel(caller); session != "" {
		result = append(result, session)
	}
	for _, model := range p.catalog(caller) {
		if !slices.Contains(result, model) {
			result = append(result, model)
		}
	}
	return result
}
