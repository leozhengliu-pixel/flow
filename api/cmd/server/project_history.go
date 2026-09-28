package main

import (
	"flow/api/internal/domain"
	"strings"
)

// projectPropertyChange is one tracked property transition recorded in the
// project audit history. Empty values mean the property was unset.
type projectPropertyChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

func projectTrackedProperties(project domain.Project) map[string]string {
	lead := ""
	if project.Lead != nil {
		lead = project.Lead.DisplayName
		if strings.TrimSpace(lead) == "" {
			lead = project.Lead.Name
		}
	}
	optional := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	priority := project.PriorityLabel
	if project.Priority == 0 {
		priority = ""
	}
	return map[string]string{
		"name":       project.Name,
		"status":     project.Status.Name,
		"priority":   priority,
		"lead":       lead,
		"startDate":  optional(project.StartDate),
		"targetDate": optional(project.TargetDate),
	}
}

var projectTrackedPropertyOrder = []string{"name", "status", "priority", "lead", "startDate", "targetDate"}

func projectPropertyChanges(previous map[string]string, after domain.Project) []projectPropertyChange {
	next := projectTrackedProperties(after)
	changes := []projectPropertyChange{}
	for _, field := range projectTrackedPropertyOrder {
		if previous[field] != next[field] {
			changes = append(changes, projectPropertyChange{Field: field, From: previous[field], To: next[field]})
		}
	}
	return changes
}

// appendProjectPropertyAudit records property changes in the project history
// so "changes since last update" can be derived from GET /projects/{id}/history.
// previous must be captured with projectTrackedProperties before mutating.
func appendProjectPropertyAudit(data *domain.Bootstrap, previous map[string]string, after domain.Project) {
	changes := projectPropertyChanges(previous, after)
	if len(changes) == 0 {
		return
	}
	appendAudit(data, "updated", "project", after.ID, map[string]any{"changes": changes})
}
