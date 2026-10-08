package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type labelMergeInput struct {
	ToLabelID    string   `json:"toLabelId"`
	FromLabelIDs []string `json:"fromLabelIds"`
}

// mergeLabels folds labels into one target label, like Linear's "Merge labels…":
// every issue, project and initiative carrying a merged label gets the target
// instead, templates, triage and SLA rules and saved views are re-pointed, and
// the merged labels are deleted. Labels must share a resource type and either
// all be ungrouped or all belong to the target's group; a team label can only
// be merged into its own team or a workspace label.
func (s *server) mergeLabels(w http.ResponseWriter, r *http.Request) {
	var input labelMergeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ToLabelID = strings.TrimSpace(input.ToLabelID)
	from := []string{}
	for _, id := range input.FromLabelIDs {
		id = strings.TrimSpace(id)
		if id != "" && id != input.ToLabelID && !slices.Contains(from, id) {
			from = append(from, id)
		}
	}
	if input.ToLabelID == "" || len(from) == 0 {
		writeError(w, http.StatusBadRequest, "toLabelId and fromLabelIds are required")
		return
	}
	ids := append([]string{input.ToLabelID}, from...)
	// Only issues carrying one of the labels change.
	ctx := store.WithMutationScope(r.Context(), store.MutationScope{LabelIssues: ids})
	var merged domain.IssueLabel
	err := s.store.MutateWorkspaceWithAggregate(ctx, workspaceKey(r), "label.merged", input, func(data *domain.Bootstrap) (string, error) {
		target, err := applyLabelMerge(data, input.ToLabelID, from)
		merged = target
		return input.ToLabelID, err
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	s.pruneIssueSuggestionTargets(r.Context(), workspaceKey(r), func(item domain.IssueSuggestion) bool {
		return item.Type == "label" && slices.Contains(from, item.SuggestedLabelID)
	})
	writeJSON(w, http.StatusOK, merged)
}

func applyLabelMerge(data *domain.Bootstrap, toID string, fromIDs []string) (domain.IssueLabel, error) {
	targetIndex := slices.IndexFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == toID })
	if targetIndex < 0 {
		return domain.IssueLabel{}, errNotFound
	}
	target := data.Labels[targetIndex]
	resource := labelResourceType(target)
	merged := map[string]struct{}{}
	for _, id := range fromIDs {
		index := slices.IndexFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == id })
		if index < 0 {
			return domain.IssueLabel{}, errNotFound
		}
		source := data.Labels[index]
		if labelResourceType(source) != resource || source.GroupID != target.GroupID {
			return domain.IssueLabel{}, errInvalid
		}
		if !labelScopeIsWorkspace(target.Scope) && !labelScopesMatch(source.Scope, target.Scope) {
			return domain.IssueLabel{}, errInvalid
		}
		if source.LastAppliedAt != nil && (target.LastAppliedAt == nil || source.LastAppliedAt.After(*target.LastAppliedAt)) {
			value := *source.LastAppliedAt
			target.LastAppliedAt = &value
		}
		merged[id] = struct{}{}
	}
	data.Labels[targetIndex] = target
	replace := func(values []string) []string {
		if !slices.ContainsFunc(values, func(id string) bool { _, ok := merged[id]; return ok }) {
			return values
		}
		next := make([]string, 0, len(values))
		for _, id := range values {
			if _, ok := merged[id]; ok {
				id = toID
			}
			if !slices.Contains(next, id) {
				next = append(next, id)
			}
		}
		return next
	}
	for index := range data.Issues {
		issue := &data.Issues[index]
		if slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { _, ok := merged[label.ID]; return ok }) {
			labels := make([]domain.IssueLabel, 0, len(issue.Labels))
			for _, label := range issue.Labels {
				if _, ok := merged[label.ID]; ok {
					label = target
				} else if label.ID == toID {
					label = target
				}
				if !slices.ContainsFunc(labels, func(item domain.IssueLabel) bool { return item.ID == label.ID }) {
					labels = append(labels, label)
				}
			}
			issue.Labels = labels
		}
		issue.SuggestedLabelIDs = replace(issue.SuggestedLabelIDs)
	}
	for index := range data.Projects {
		data.Projects[index].LabelIDs = replace(data.Projects[index].LabelIDs)
	}
	for index := range data.Initiatives {
		data.Initiatives[index].LabelIDs = replace(data.Initiatives[index].LabelIDs)
	}
	for index := range data.IssueTemplates {
		data.IssueTemplates[index].LabelIDs = replace(data.IssueTemplates[index].LabelIDs)
		for child := range data.IssueTemplates[index].SubIssues {
			data.IssueTemplates[index].SubIssues[child].LabelIDs = replace(data.IssueTemplates[index].SubIssues[child].LabelIDs)
		}
	}
	for index := range data.ProjectTemplates {
		data.ProjectTemplates[index].LabelIDs = replace(data.ProjectTemplates[index].LabelIDs)
	}
	for index := range data.TriageRoutingRules {
		rule := &data.TriageRoutingRules[index]
		rule.LabelIDs = replace(rule.LabelIDs)
		if _, ok := merged[rule.Conditions["labelId"]]; ok {
			rule.Conditions["labelId"] = toID
		}
	}
	for index := range data.SLARules {
		if value, ok := data.SLARules[index].Filters["label"].(string); ok {
			if _, isMerged := merged[value]; isMerged {
				data.SLARules[index].Filters["label"] = toID
			}
		}
	}
	for index := range data.SavedViews {
		data.SavedViews[index].Filters = replaceLabelReferencesInJSON(data.SavedViews[index].Filters, merged, toID)
	}
	data.Labels = slices.DeleteFunc(data.Labels, func(label domain.IssueLabel) bool { _, ok := merged[label.ID]; return ok })
	for id := range merged {
		removeResourcePreferences(data, "label", id)
	}
	return target, nil
}

// replaceLabelReferencesInJSON re-points saved filter values (bare ids or
// "label:"-style prefixed ids) from merged labels to the target label.
func replaceLabelReferencesInJSON(raw json.RawMessage, ids map[string]struct{}, toID string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	changed := false
	var walk func(any) any
	walk = func(value any) any {
		switch typed := value.(type) {
		case string:
			if _, ok := ids[typed]; ok {
				changed = true
				return toID
			}
			for _, prefix := range []string{"label:", "labels:", "project-label:"} {
				if _, ok := ids[strings.TrimPrefix(typed, prefix)]; ok && strings.HasPrefix(typed, prefix) {
					changed = true
					return prefix + toID
				}
			}
			return typed
		case []any:
			next := make([]any, 0, len(typed))
			for _, item := range typed {
				item = walk(item)
				if text, ok := item.(string); ok && slices.ContainsFunc(next, func(existing any) bool { return existing == text }) {
					continue
				}
				next = append(next, item)
			}
			return next
		case map[string]any:
			for key, item := range typed {
				typed[key] = walk(item)
			}
			return typed
		}
		return value
	}
	next := walk(value)
	if !changed {
		return raw
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return raw
	}
	return encoded
}
