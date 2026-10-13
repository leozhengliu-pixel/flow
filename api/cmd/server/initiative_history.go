package main

import (
	"strings"

	"flow/api/internal/domain"
)

// initiativeTrackedProperties are the initiative fields its history
// (GET /initiatives/{id}/history) records, like project history.
func initiativeTrackedProperties(initiative domain.Initiative) map[string]string {
	owner := ""
	if initiative.Owner != nil {
		owner = firstNonEmpty(strings.TrimSpace(initiative.Owner.DisplayName), initiative.Owner.Name)
	}
	priority := initiative.PriorityLabel
	if initiative.Priority == 0 {
		priority = ""
	}
	targetDate := ""
	if initiative.TargetDate != nil {
		targetDate = *initiative.TargetDate
	}
	return map[string]string{
		"name":        initiative.Name,
		"status":      initiative.Status,
		"priority":    priority,
		"owner":       owner,
		"targetDate":  targetDate,
		"description": initiative.Description, // only whether it changed is recorded
	}
}

var initiativeTrackedPropertyOrder = []string{"name", "status", "priority", "owner", "targetDate", "description"}

// appendInitiativeAudit records an initiative's creation or property changes
// in its history with the request's attribution (viewerBotActor via appendAudit).
// previous is nil for a new initiative.
func appendInitiativeAudit(data *domain.Bootstrap, previous map[string]string, after domain.Initiative) {
	if previous == nil {
		appendAudit(data, "created", "initiative", after.ID, map[string]any{"name": after.Name})
		return
	}
	next := initiativeTrackedProperties(after)
	changes := []projectPropertyChange{}
	for _, field := range initiativeTrackedPropertyOrder {
		if previous[field] == next[field] {
			continue
		}
		if field == "description" {
			changes = append(changes, projectPropertyChange{Field: field})
			continue
		}
		changes = append(changes, projectPropertyChange{Field: field, From: previous[field], To: next[field]})
	}
	if len(changes) > 0 {
		appendAudit(data, "updated", "initiative", after.ID, map[string]any{"changes": changes})
	}
}
