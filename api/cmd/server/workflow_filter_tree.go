package main

import (
	"encoding/json"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

func workflowParamValue(value string) any {
	if text := strings.TrimSpace(value); strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") || strings.HasPrefix(text, `"`) {
		var parsed any
		if json.Unmarshal([]byte(text), &parsed) == nil {
			return parsed
		}
	}
	return value
}

// workflowFilterMatches applies the same AND/OR structure used when
// rewriting label references. An empty OR still has no matching branch.
func workflowFilterMatches(key string, value any, match func(field, operator string, value any) bool) bool {
	switch node := value.(type) {
	case []any:
		if key == "or" {
			return slices.ContainsFunc(node, func(item any) bool { return workflowFilterMatches("", item, match) })
		}
		return !slices.ContainsFunc(node, func(item any) bool { return !workflowFilterMatches("", item, match) })
	case map[string]any:
		if field, ok := node["field"].(string); ok {
			operator, _ := node["operator"].(string)
			if field == "advanced" {
				return workflowFilterMatches("tree", node["tree"], match)
			}
			value, exists := node["values"]
			if !exists {
				value = node["value"]
			}
			return match(field, operator, value)
		}
		found := false
		for key, value := range node {
			switch {
			case workflowLabelField(key), key == "teamId":
				found = true
				if !match(key, "is", value) {
					return false
				}
			case workflowFilterField(key):
				found = true
				group := key
				if key == "items" && node["conjunction"] == "or" {
					group = "or"
				}
				if !workflowFilterMatches(group, value, match) {
					return false
				}
			}
		}
		return found
	}
	return false
}

func negativeAutomationFilter(operator string) bool {
	return operator == "isNot" || operator == "notIn"
}

func automationOperandMatches(value any, match func(string) bool) bool {
	switch value := value.(type) {
	case string:
		return match(value)
	case []any:
		return slices.ContainsFunc(value, func(item any) bool { return automationOperandMatches(item, match) })
	case map[string]any:
		if id, ok := value["id"]; ok {
			return automationOperandMatches(id, match)
		}
		return automationOperandMatches(value["value"], match)
	}
	return false
}

func automationTeamMatches(team domain.Team, value any) bool {
	return automationOperandMatches(value, func(id string) bool {
		return id != "" && (team.ID == id || strings.EqualFold(team.Key, id))
	})
}

func automationLabelMatches(labels []domain.IssueLabel, value any, matchNames bool) bool {
	return slices.ContainsFunc(labels, func(label domain.IssueLabel) bool {
		return automationOperandMatches(value, func(operand string) bool {
			_, id := splitWorkflowLabelID(operand)
			return id != "" && (label.ID == id || matchNames && strings.EqualFold(label.Name, id))
		})
	})
}

func automationLabelFilterMatches(labels []domain.IssueLabel, value any, operator string, loop bool) bool {
	negative := negativeAutomationFilter(operator) || operator == "excludesAll"
	// Null in a Loop asks for no labels. Retired operands are empty strings
	// and have no match, including in a negative filter.
	if value == nil {
		return loop && (len(labels) == 0) != negative
	}
	if !automationOperandMatches(value, func(operand string) bool { return operand != "" }) {
		return false
	}
	if operands, ok := value.([]any); ok && operator == "includesAll" {
		return !slices.ContainsFunc(operands, func(operand any) bool { return !automationLabelMatches(labels, operand, loop) })
	}
	return automationLabelMatches(labels, value, loop) != negative
}
