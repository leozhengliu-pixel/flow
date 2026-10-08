package store

import (
	"flow/api/internal/domain"
	"time"
)

func normalizeParity(data *domain.Bootstrap) {
	if data.ProjectRelations == nil {
		data.ProjectRelations = []domain.ProjectRelation{}
	}
	if data.InitiativeRelations == nil {
		data.InitiativeRelations = []domain.InitiativeRelation{}
	}
	if data.DocumentContentDrafts == nil {
		data.DocumentContentDrafts = []domain.DocumentContentDraft{}
	}
	if data.CustomerStatuses == nil {
		data.CustomerStatuses = DefaultCustomerStatuses(time.Now().UTC())
	}
	if data.CustomerTiers == nil {
		data.CustomerTiers = []domain.CustomerTier{}
	}
	if data.ReleaseNotes == nil {
		data.ReleaseNotes = []domain.ReleaseNote{}
	}
	if data.ReleaseHistory == nil {
		data.ReleaseHistory = []domain.ReleaseHistory{}
	}
	if data.TeamResourceSections == nil {
		data.TeamResourceSections = []domain.TeamResourceSection{}
	}
	if data.TeamPinnedResources == nil {
		data.TeamPinnedResources = []domain.TeamPinnedResource{}
	}
	if data.AgentActivities == nil {
		data.AgentActivities = []domain.AgentActivity{}
	}
	if data.AIConversations == nil {
		data.AIConversations = []domain.AIConversation{}
	}
	if data.AIPromptProgress == nil {
		data.AIPromptProgress = []domain.AIPromptProgress{}
	}
	if data.IssueSuggestions == nil {
		data.IssueSuggestions = []domain.IssueSuggestion{}
	}
	normalizeTriageIntelligenceSettings(&data.WorkspaceSettings.FeatureSettings.TriageIntelligence)
}

func normalizeTriageIntelligenceSettings(settings *domain.TriageIntelligenceSettings) {
	defaults := map[*string]string{
		&settings.AssigneeAction:  "suggest",
		&settings.ProjectAction:   "suggest",
		&settings.LabelAction:     "suggest",
		&settings.TeamAction:      "suggest",
		&settings.DuplicateAction: "suggest",
		&settings.RelatedAction:   "suggest",
	}
	for target, fallback := range defaults {
		if *target == "" {
			*target = fallback
		}
	}
}

// DefaultCustomerStatuses are the customer statuses a new workspace starts
// with (Active, Prospect, Churned, Lost), in order.
func DefaultCustomerStatuses(now time.Time) []domain.CustomerStatus {
	defaults := []struct{ id, name, color string }{
		{"customer_status_active", "Active", "#5e6ad2"},
		{"customer_status_prospect", "Prospect", "#4cb782"},
		{"customer_status_churned", "Churned", "#eb5757"},
		{"customer_status_lost", "Lost", "#f2994a"},
	}
	statuses := make([]domain.CustomerStatus, len(defaults))
	for index, item := range defaults {
		statuses[index] = domain.CustomerStatus{ID: item.id, Name: item.name, Color: item.color, Position: float64(index), CreatedAt: now, UpdatedAt: now}
	}
	return statuses
}
