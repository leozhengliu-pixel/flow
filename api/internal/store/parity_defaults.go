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
		now := time.Now().UTC()
		data.CustomerStatuses = []domain.CustomerStatus{{ID: "customer_status_active", Name: "Active", Color: "#4cb782", Position: 0, CreatedAt: now, UpdatedAt: now}, {ID: "customer_status_prospect", Name: "Prospect", Color: "#5e6ad2", Position: 1, CreatedAt: now, UpdatedAt: now}, {ID: "customer_status_churned", Name: "Churned", Color: "#f2c94c", Position: 2, CreatedAt: now, UpdatedAt: now}}
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
}
