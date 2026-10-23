package coding

import (
	"fmt"
	"slices"
	"strings"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// Loop repository access values.
const (
	LoopAccessNone     = "none"
	LoopAccessAll      = "all"
	LoopAccessSelected = "selected"
)

// NormalizeAgentSettings returns the coding policy with defaults applied and
// legacy values normalized (harness auto → codex, claude → claudeCode).
func NormalizeAgentSettings(settings domain.CodingAgentSettings) domain.CodingAgentSettings {
	settings.Harness = NormalizeHarness(settings.Harness)
	settings.Model = strings.TrimSpace(settings.Model)
	if settings.Model == "" {
		settings.Model = domain.CodingModelAuto
	}
	settings.ReasoningEffort = NormalizeEffort(settings.ReasoningEffort)
	settings.PinnedRegion = strings.TrimSpace(settings.PinnedRegion)
	switch settings.LoopRepositoryAccess {
	case LoopAccessAll, LoopAccessSelected:
	default:
		settings.LoopRepositoryAccess = LoopAccessNone
	}
	settings.TrustedExternalSources = uniqueTrimmed(settings.TrustedExternalSources)
	settings.LoopRepositories = uniqueTrimmed(settings.LoopRepositories)
	return settings
}

// ValidateAgentSettings checks a policy an admin saves.
func ValidateAgentSettings(settings domain.CodingAgentSettings, c config.CodingConfig) error {
	if !ValidHarness(settings.Harness) {
		return fmt.Errorf("%w: harness must be codex, claudeCode, openSource or pi", ErrInvalidInput)
	}
	if !ValidEffort(settings.ReasoningEffort) {
		return fmt.Errorf("%w: reasoningEffort must be low, medium or high", ErrInvalidInput)
	}
	switch settings.LoopRepositoryAccess {
	case "", LoopAccessNone, LoopAccessAll, LoopAccessSelected:
	default:
		return fmt.Errorf("%w: loopRepositoryAccess must be none, all or selected", ErrInvalidInput)
	}
	if region := strings.TrimSpace(settings.PinnedRegion); region != "" && !slices.Contains(c.Regions, region) {
		return fmt.Errorf("%w: pinnedRegion must be one of FLOW_CODING_REGIONS", ErrInvalidInput)
	}
	model := strings.TrimSpace(settings.Model)
	if model != "" && model != domain.CodingModelAuto {
		if catalog := ModelCatalog(c.LLM, settings.Harness); len(catalog) > 0 && !slices.Contains(catalog, model) {
			return fmt.Errorf("%w: model %q is not in the %s catalog", ErrInvalidInput, model, NormalizeHarness(settings.Harness))
		}
	}
	if len(settings.TrustedExternalSources) > 100 || len(settings.LoopRepositories) > 500 {
		return fmt.Errorf("%w: too many entries", ErrInvalidInput)
	}
	return nil
}

// LoopAllowed reports whether a Loop may start a session on an environment.
func LoopAllowed(settings domain.CodingAgentSettings, environmentID string) bool {
	switch NormalizeAgentSettings(settings).LoopRepositoryAccess {
	case LoopAccessAll:
		return true
	case LoopAccessSelected:
		return slices.Contains(settings.LoopRepositories, environmentID)
	}
	return false
}

func uniqueTrimmed(values []string) []string {
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}
