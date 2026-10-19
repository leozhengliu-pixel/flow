package coding

import (
	"slices"
	"strings"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// Model routing (design §12). "auto" resolves to the first model of the
// harness catalog (FLOW_CODING_MODELS_*); a pinned model must be in the
// catalog when the catalog is non-empty.

// Reasoning efforts.
const (
	EffortDefault = ""
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
)

// NormalizeHarness maps legacy values (auto → codex, claude → claudeCode)
// and unknown or empty values to the default harness, codex.
func NormalizeHarness(harness string) string {
	switch strings.TrimSpace(harness) {
	case domain.CodingHarnessClaudeCode, "claude":
		return domain.CodingHarnessClaudeCode
	case domain.CodingHarnessOpenSource:
		return domain.CodingHarnessOpenSource
	default:
		return domain.CodingHarnessCodex
	}
}

// ValidHarness reports whether harness is one of the three harnesses (or a legacy alias).
func ValidHarness(harness string) bool {
	switch harness {
	case "", "auto", "claude", domain.CodingHarnessCodex, domain.CodingHarnessClaudeCode, domain.CodingHarnessOpenSource:
		return true
	}
	return false
}

// NormalizeEffort returns a valid reasoning effort ("" for anything unknown).
func NormalizeEffort(effort string) string {
	switch effort {
	case EffortLow, EffortMedium, EffortHigh:
		return effort
	}
	return EffortDefault
}

// ValidEffort reports whether effort is "", low, medium or high.
func ValidEffort(effort string) bool { return effort == NormalizeEffort(effort) }

// ModelCatalog is the model list of one harness; the first entry is Auto's pick.
func ModelCatalog(llm config.CodingLLMConfig, harness string) []string {
	switch NormalizeHarness(harness) {
	case domain.CodingHarnessClaudeCode:
		return llm.ClaudeCodeModels
	case domain.CodingHarnessOpenSource:
		return llm.OpenSourceModels
	default:
		return llm.CodexModels
	}
}

// ResolveModel turns a selection (auto or an id) into the model a session
// runs with. ok is false when a pinned model is not in a non-empty catalog,
// or when Auto has nothing to pick (modelUnavailable).
func ResolveModel(llm config.CodingLLMConfig, harness, selection string) (model string, ok bool) {
	catalog := ModelCatalog(llm, harness)
	selection = strings.TrimSpace(selection)
	if selection == "" || selection == domain.CodingModelAuto {
		if len(catalog) == 0 {
			return "", false
		}
		return catalog[0], true
	}
	if len(catalog) > 0 && !slices.Contains(catalog, selection) {
		return "", false
	}
	return selection, true
}

// NextAutoModel is the catalog model after current (Auto's fallback when the
// upstream rejects or is overloaded); ok is false at the end of the catalog.
func NextAutoModel(llm config.CodingLLMConfig, harness, current string) (string, bool) {
	catalog := ModelCatalog(llm, harness)
	index := slices.Index(catalog, current)
	if index < 0 || index+1 >= len(catalog) {
		return "", false
	}
	return catalog[index+1], true
}

// ClaudeThinkingTokens is MAX_THINKING_TOKENS for a reasoning effort (0 = harness default).
func ClaudeThinkingTokens(effort string) int {
	switch effort {
	case EffortLow:
		return 4000
	case EffortMedium:
		return 16000
	case EffortHigh:
		return 32000
	}
	return 0
}

// ModelOption is one entry of GET /api/coding/models (web CodingModelOption).
type ModelOption struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Harness          string   `json:"harness"`
	Auto             bool     `json:"auto,omitempty"`
	ReasoningEfforts []string `json:"reasoningEfforts,omitempty"`
}

// ModelOptions lists Auto plus every catalog model, per harness.
func ModelOptions(llm config.CodingLLMConfig) []ModelOption {
	efforts := []string{EffortDefault, EffortLow, EffortMedium, EffortHigh}
	options := []ModelOption{}
	for _, harness := range []string{domain.CodingHarnessCodex, domain.CodingHarnessClaudeCode, domain.CodingHarnessOpenSource} {
		catalog := ModelCatalog(llm, harness)
		if len(catalog) == 0 {
			continue
		}
		options = append(options, ModelOption{ID: domain.CodingModelAuto, Label: "Auto (" + catalog[0] + ")", Harness: harness, Auto: true, ReasoningEfforts: efforts})
		for _, model := range catalog {
			options = append(options, ModelOption{ID: model, Label: model, Harness: harness, ReasoningEfforts: efforts})
		}
	}
	return options
}
