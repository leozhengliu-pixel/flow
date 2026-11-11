package main

import (
	"fmt"
	"slices"
	"strconv"

	"flow/api/internal/domain"
)

func validSLAFilters(data *domain.Bootstrap, filters map[string]any) bool {
	for key, raw := range filters {
		value := fmt.Sprint(raw)
		switch raw.(type) {
		case string, int, float64:
		default:
			return false
		}
		switch key {
		case "priority":
			priority, err := strconv.Atoi(value)
			if err != nil || priority < 0 || priority > 4 {
				return false
			}
		case "status":
			if !slices.Contains([]string{"triage", "backlog", "unstarted", "started", "completed", "canceled"}, value) && !slices.ContainsFunc(data.States, func(state domain.WorkflowState) bool { return state.ID == value }) {
				return false
			}
		case "label":
			if !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == value || label.Name == value }) {
				return false
			}
		case "project":
			if !validateResourceIDs(data, "project", []string{value}) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
