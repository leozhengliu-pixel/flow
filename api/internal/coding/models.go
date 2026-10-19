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

// Model picker groups of GET /api/coding/models, in display order.
const (
	ModelGroupAuto     = "auto"
	ModelGroupFrontier = config.ModelGroupFrontier
	ModelGroupOther    = config.ModelGroupOther
)

// AutoModelDescription is the Auto option's one-line description.
const AutoModelDescription = "Uses adaptive routing to pick the best model"

// defaultFrontierModels is how many leading catalog entries count as
// frontier when neither FLOW_CODING_MODEL_INFO nor the built-ins say.
const defaultFrontierModels = 3

// builtinModelInfo labels and describes the models Flow ships catalogs for.
// FLOW_CODING_MODEL_INFO overrides any field.
var builtinModelInfo = map[string]config.ModelInfo{
	"claude-opus-4-1":   {Label: "Claude Opus 4.1", Description: "Most capable model for complex tasks"},
	"claude-opus-4-5":   {Label: "Claude Opus 4.5", Description: "Most capable model for complex tasks"},
	"claude-sonnet-4-5": {Label: "Claude Sonnet 4.5", Description: "Strong reasoning for multi-file changes"},
	"claude-haiku-4-5":  {Label: "Claude Haiku 4.5", Description: "Fast model for small fixes and routine edits"},
	"claude-sonnet-4":   {Label: "Claude Sonnet 4", Group: config.ModelGroupOther},
	"claude-opus-4":     {Label: "Claude Opus 4", Group: config.ModelGroupOther},
	"gpt-5":             {Label: "GPT-5", Description: "Most capable model for complex tasks"},
	"gpt-5-codex":       {Label: "GPT-5 Codex", Description: "Strong reasoning for multi-file changes"},
	"gpt-5-mini":        {Label: "GPT-5 mini", Description: "Best for small fixes and routine edits"},
	"gpt-4.1":           {Label: "GPT-4.1", Group: config.ModelGroupOther},
}

// DescribeModel resolves the label, group and description of the catalog
// entry at index: configured fields first, then built-ins, then the id and a
// positional group (the first three entries are frontier).
func DescribeModel(llm config.CodingLLMConfig, model string, index int) config.ModelInfo {
	info := llm.ModelInfo[model]
	builtin := builtinModelInfo[model]
	if info.Label == "" {
		info.Label = builtin.Label
	}
	if info.Label == "" {
		info.Label = model
	}
	if info.Description == "" {
		info.Description = builtin.Description
	}
	if info.Group == "" {
		info.Group = builtin.Group
	}
	if info.Group == "" {
		info.Group = ModelGroupOther
		if index < defaultFrontierModels {
			info.Group = ModelGroupFrontier
		}
	}
	return info
}

// ModelOption is one entry of GET /api/coding/models (web CodingModelOption).
// Group and Description were added for the grouped model picker; older
// clients ignore them.
type ModelOption struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Harness          string   `json:"harness"`
	Auto             bool     `json:"auto,omitempty"`
	Group            string   `json:"group,omitempty"`
	Description      string   `json:"description,omitempty"`
	ReasoningEfforts []string `json:"reasoningEfforts,omitempty"`
}

// ModelOptions lists Auto plus every catalog model, per harness, frontier
// models before the others (catalog order within a group).
func ModelOptions(llm config.CodingLLMConfig) []ModelOption {
	efforts := []string{EffortDefault, EffortLow, EffortMedium, EffortHigh}
	options := []ModelOption{}
	for _, harness := range []string{domain.CodingHarnessCodex, domain.CodingHarnessClaudeCode, domain.CodingHarnessOpenSource} {
		catalog := ModelCatalog(llm, harness)
		if len(catalog) == 0 {
			continue
		}
		options = append(options, ModelOption{ID: domain.CodingModelAuto, Label: "Auto (" + catalog[0] + ")", Harness: harness, Auto: true, Group: ModelGroupAuto, Description: AutoModelDescription, ReasoningEfforts: efforts})
		var frontier, other []ModelOption
		for index, model := range catalog {
			info := DescribeModel(llm, model, index)
			option := ModelOption{ID: model, Label: info.Label, Harness: harness, Group: info.Group, Description: info.Description, ReasoningEfforts: efforts}
			if info.Group == ModelGroupFrontier {
				frontier = append(frontier, option)
			} else {
				other = append(other, option)
			}
		}
		options = append(options, frontier...)
		options = append(options, other...)
	}
	return options
}
