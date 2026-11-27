package main

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// An empty destination means the label is gone or unavailable in this scope.
type workflowLabelRemap func(string) (string, bool)

func rewriteWorkflowLabelReferences(data *domain.Bootstrap, remap workflowLabelRemap) {
	now := time.Now().UTC()
	for i, workflow := range data.WorkflowDefinitions {
		if next, changed := remapWorkflowLabels(workflow, remap); changed {
			next.UpdatedAt = now
			data.WorkflowDefinitions[i] = next
		}
	}
	for i, loop := range data.Loops {
		if next, changed := remapLoopLabels(loop, remap); changed {
			next.UpdatedAt = now
			data.Loops[i] = next
		}
	}
}

func remapWorkflowLabels(workflow domain.WorkflowDefinition, remap workflowLabelRemap) (domain.WorkflowDefinition, bool) {
	conditionsChanged := false
	for key, value := range workflow.Conditions {
		next, changed, _ := remapWorkflowLabelParam(key, value, remap)
		if !changed {
			continue
		}
		if !conditionsChanged {
			workflow.Conditions = maps.Clone(workflow.Conditions)
			conditionsChanged = true
		}
		// Flat conditions are ANDed. An empty labelId still matches no issue.
		workflow.Conditions[key] = next
	}
	var actions []domain.WorkflowAction
	for i, action := range workflow.Actions {
		configChanged, missing := false, false
		for key, value := range action.Config {
			if !workflowLabelField(key) && !workflowFilterField(key) {
				continue
			}
			next, changed, empty := remapWorkflowLabelParam(key, value, remap)
			if !changed {
				continue
			}
			if !configChanged {
				action.Config = maps.Clone(action.Config)
			}
			action.Config[key] = next
			configChanged = true
			missing = missing || workflowLabelField(key) && empty
		}
		if configChanged && actions == nil {
			actions = append(make([]domain.WorkflowAction, 0, len(workflow.Actions)), workflow.Actions[:i]...)
		}
		if actions != nil && !missing {
			actions = append(actions, action)
		}
	}
	if actions != nil {
		workflow.Actions = actions
	}
	return workflow, conditionsChanged || actions != nil
}

func remapWorkflowLabelParam(key, value string, remap workflowLabelRemap) (string, bool, bool) {
	isLabel := workflowLabelField(key)
	if !isLabel && !workflowFilterField(key) {
		return value, false, false
	}
	if isLabel {
		if next, changed := remapLabelOperand(value, remap); changed {
			if next == nil {
				return "", true, true
			}
			return next.(string), true, false
		}
	}
	// Workflow definitions store config values as strings, including JSON
	// operands and filter trees supplied by clients.
	var raw any
	if json.Unmarshal([]byte(value), &raw) != nil {
		return value, false, false
	}
	var next any
	var changed, empty bool
	if isLabel {
		next, changed = remapLabelOperand(raw, remap)
		empty = next == nil
		if empty {
			next = []any{}
		}
	} else {
		next, changed, empty = remapWorkflowFilterField(key, raw, remap)
	}
	if !changed {
		return value, false, false
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return value, false, false
	}
	return string(encoded), true, empty
}

func remapLoopLabels(loop domain.Loop, remap workflowLabelRemap) (domain.Loop, bool) {
	config, changed, _ := remapWorkflowFilter(loop.TriggerConfig, remap)
	trigger := config.(map[string]any)
	if loop.TriggerType == "issue" && trigger["event"] == "labels" {
		if next, updated := remapLabelOperand(trigger["value"], remap); updated {
			if !changed {
				trigger = maps.Clone(trigger)
			}
			// An explicit null matches no label. An absent value means any label.
			trigger["value"] = next
			changed = true
		}
	}
	if _, legacy := trigger["filter"]; legacy && workflowLabelField(trigger["filterField"]) {
		// Legacy filters evaluate their operand as a trimmed string.
		value, _ := trigger["filterValue"].(string)
		if next, updated := remapLabelOperand(strings.TrimSpace(value), remap); updated {
			if !changed {
				trigger = maps.Clone(trigger)
			}
			if next != nil {
				trigger["filterValue"] = next
			} else {
				// Legacy empty values are ignored. Keep a retired value in
				// the current filter shape so it stays a required predicate.
				var filters []any
				switch value := trigger["filters"].(type) {
				case []any:
					filters = slices.Clone(value)
				case map[string]any:
					filters = []any{value}
				}
				operator := "is"
				if trigger["filterOperator"] == "isNot" {
					operator = "isNot"
				}
				trigger["filters"] = append(filters, map[string]any{"field": "label", "operator": operator, "value": ""})
				for _, key := range []string{"filter", "filterField", "filterOperator", "filterValue"} {
					delete(trigger, key)
				}
			}
			changed = true
		}
	}
	loop.TriggerConfig = trigger
	return loop, changed
}

func workflowLabelField(field any) bool {
	text, _ := field.(string)
	switch text {
	case "label", "labelId", "labelIds", "labels":
		return true
	}
	return false
}

func workflowFilterField(key string) bool {
	switch key {
	case "filters", "filter", "conditions", "tree", "and", "or", "items":
		return true
	}
	return false
}

func workflowLabelFilterOperand(value any) any {
	if value == nil {
		// In Loops, null in a filter means "no labels". An empty string
		// represents a retired operand and matches neither is nor isNot.
		return ""
	}
	return value
}

func remapWorkflowFilterField(key string, value any, remap workflowLabelRemap) (any, bool, bool) {
	if items, ok := value.([]any); ok {
		return remapWorkflowFilterList(items, key == "or", remap)
	}
	return remapWorkflowFilter(value, remap)
}

// remapWorkflowFilter understands filter leaves and both nested group forms:
// {conjunction, items} and {and: [...]} / {or: [...]}. An impossible branch
// can be removed from an OR. In an AND it must continue to match nothing.
func remapWorkflowFilter(value any, remap workflowLabelRemap) (any, bool, bool) {
	if items, ok := value.([]any); ok {
		return remapWorkflowFilterList(items, false, remap)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value, false, false
	}
	next := object
	changed, empty := false, false
	for key, item := range object {
		var mapped any
		var updated, missing bool
		switch {
		case workflowLabelField(key):
			mapped, updated = remapLabelOperand(item, remap)
			missing = mapped == nil
			mapped = workflowLabelFilterOperand(mapped)
		case workflowLabelField(object["field"]) && (key == "value" || key == "values"):
			mapped, updated = remapLabelOperand(item, remap)
			_, hasValues := object["values"]
			missing = mapped == nil && (key == "values" || !hasValues)
			mapped = workflowLabelFilterOperand(mapped)
		case workflowFilterField(key):
			group := key
			if key == "items" && object["conjunction"] == "or" {
				group = "or"
			}
			mapped, updated, missing = remapWorkflowFilterField(group, item, remap)
		}
		if updated {
			if !changed {
				next = maps.Clone(object)
				changed = true
			}
			next[key] = mapped
			empty = empty || missing
		}
	}
	return next, changed, empty
}

func remapWorkflowFilterList(items []any, or bool, remap workflowLabelRemap) (any, bool, bool) {
	next := make([]any, 0, len(items))
	changed, empty := false, false
	for _, item := range items {
		mapped, updated, missing := remapWorkflowFilter(item, remap)
		changed = changed || updated
		if or && missing {
			continue
		}
		empty = empty || missing
		next = append(next, mapped)
	}
	if changed {
		next = dedupeWorkflowFilterItems(next)
	}
	return next, changed, empty || or && changed && len(next) == 0
}

func dedupeWorkflowFilterItems(items []any) []any {
	deduped := items[:0]
	for _, item := range items {
		if !slices.ContainsFunc(deduped, func(existing any) bool { return reflect.DeepEqual(existing, item) }) {
			deduped = append(deduped, item)
		}
	}
	return deduped
}

func splitWorkflowLabelID(value string) (prefix, id string) {
	for _, prefix := range []string{"label:", "labels:", "project-label:"} {
		if id, ok := strings.CutPrefix(value, prefix); ok {
			return prefix, id
		}
	}
	return "", value
}

func remapLabelOperand(value any, remap workflowLabelRemap) (any, bool) {
	switch value := value.(type) {
	case string:
		prefix, id := splitWorkflowLabelID(value)
		next, changed := remap(id)
		if !changed {
			return value, false
		}
		if next == "" {
			return nil, true
		}
		return prefix + next, true
	case []any:
		next := make([]any, 0, len(value))
		changed := false
		for _, item := range value {
			mapped, updated := remapLabelOperand(item, remap)
			changed = changed || updated
			if !updated || mapped != nil {
				next = append(next, mapped)
			}
		}
		if !changed {
			return value, false
		}
		if len(next) == 0 {
			return nil, true
		}
		seen := map[string]bool{}
		deduped := next[:0]
		for _, item := range next {
			if id, ok := workflowLabelOperandID(item); ok {
				if seen[id] {
					continue
				}
				seen[id] = true
			}
			deduped = append(deduped, item)
		}
		return dedupeWorkflowFilterItems(deduped), true
	case map[string]any:
		next := value
		changed := false
		for _, key := range []string{"id", "value"} {
			mapped, updated := remapLabelOperand(value[key], remap)
			if !updated {
				continue
			}
			if mapped == nil {
				return nil, true
			}
			if !changed {
				next = maps.Clone(value)
				changed = true
			}
			next[key] = mapped
		}
		return next, changed
	}
	return value, false
}

func workflowLabelOperandID(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		_, id := splitWorkflowLabelID(value)
		return id, true
	case map[string]any:
		if id, ok := value["id"]; ok {
			return workflowLabelOperandID(id)
		}
		return workflowLabelOperandID(value["value"])
	}
	return "", false
}

func workflowLabelTeam(data *domain.Bootstrap, workflow domain.WorkflowDefinition) string {
	if workflow.TeamID != "" {
		if active, _ := chatTeamPrivacy(*data, workflow.TeamID); active {
			return workflow.TeamID
		}
		return ""
	}
	// Only the issue-created entry point checks Conditions.
	if workflow.Trigger != "issueCreated" {
		return ""
	}
	return singlePublicAutomationTeam(data, func(team domain.Team) bool {
		for key, value := range workflow.Conditions {
			if key == "teamId" && (team.ID == "" || team.ID != value) {
				return false
			}
			if key == "team" && !automationTeamMatches(team, value) {
				return false
			}
			if workflowFilterField(key) && !automationFilterTeamMatches(team, key, workflowParamValue(value)) {
				return false
			}
		}
		return true
	})
}

func loopLabelTeam(data *domain.Bootstrap, loop domain.Loop) string {
	if loop.TeamID != "" && (loop.Level == "team" || loop.Level == "") {
		if active, _ := chatTeamPrivacy(*data, loop.TeamID); active && loopScopeMatches(*data, loop, []string{loop.TeamID}) {
			return loop.TeamID
		}
		return ""
	}
	if loop.TriggerType != "issue" {
		return ""
	}
	config := normalizeLoopTriggerConfig(loop.TriggerType, loop.TriggerConfig)
	explicit := loopConfigStrings(config, "teamIds")
	teamID := singlePublicAutomationTeam(data, func(team domain.Team) bool {
		if len(explicit) != 0 && !slices.Contains(explicit, team.ID) {
			return false
		}
		if value, set := config["value"]; config["event"] == "team" && set && !loopAnyValue(value) && !automationTeamMatches(team, value) {
			return false
		}
		for key, value := range config {
			if workflowFilterField(key) && !automationFilterTeamMatches(team, key, value) {
				return false
			}
		}
		return true
	})
	if teamID != "" && loopScopeMatches(*data, loop, []string{teamID}) {
		return teamID
	}
	return ""
}

// Published versions are historical snapshots. Clear unavailable operands
// when a definition from history is made live again.
func restoreLoopLabelReferences(data *domain.Bootstrap, loop domain.Loop) domain.Loop {
	available := func(label domain.IssueLabel) bool {
		if !labelAvailableForResource(data, label, "issue") {
			return false
		}
		if labelScopeIsWorkspace(label.Scope) {
			return true
		}
		if loop.TeamID != "" && (loop.Level == "team" || loop.Level == "") {
			return labelsAvailableToTeam(data, []domain.IssueLabel{label}, loop.TeamID)
		}
		return chatPublicTeam(*data, label.Scope)
	}
	loop, _ = remapLoopLabels(loop, func(id string) (string, bool) {
		if id == "" || strings.EqualFold(id, "any") {
			return id, false
		}
		for _, label := range data.Labels {
			if label.ID == id {
				return "", !available(label)
			}
		}
		if slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool {
			return strings.EqualFold(label.Name, id) && available(label)
		}) {
			return id, false
		}
		return "", true
	})
	return loop
}

// Workspace automations are visible workspace-wide. Team conditions can
// narrow the events they receive, but cannot grant access to private labels.
func singlePublicAutomationTeam(data *domain.Bootstrap, matches func(domain.Team) bool) string {
	// An unrestricted automation stays unrestricted even in a workspace
	// that happens to have just one team today.
	if matches(domain.Team{}) {
		return ""
	}
	teamID := ""
	for _, team := range data.Teams {
		if !matches(team) {
			continue
		}
		if teamID != "" {
			return ""
		}
		teamID = team.ID
	}
	if active, privacy := chatTeamPrivacy(*data, teamID); active && privacy == "" {
		return teamID
	}
	return ""
}

func automationFilterTeamMatches(team domain.Team, key string, filter any) bool {
	return workflowFilterMatches(key, filter, func(field, operator string, value any) bool {
		switch field {
		case "team":
			return automationTeamMatches(team, value) != negativeAutomationFilter(operator)
		case "teamId":
			return automationOperandMatches(value, func(id string) bool { return id != "" && id == team.ID }) != negativeAutomationFilter(operator)
		}
		return true
	})
}
