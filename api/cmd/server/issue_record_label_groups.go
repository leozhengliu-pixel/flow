package main

import (
	"context"
	"slices"
	"strings"

	"flow/api/internal/store"
)

// noIssueGroupLabel matches no label: a label group without active issue
// labels (or an unknown group) puts every issue in "No label".
const noIssueGroupLabel = "\x00"

// issueGroupLabelIDs is the active issue labels of a label group.
func (s *server) issueGroupLabelIDs(ctx context.Context, workspace, groupID string) ([]string, error) {
	ids := []string{}
	if groupID != "" {
		var err error
		if ids, err = s.store.LabelGroupIssueLabelIDs(ctx, workspace, groupID); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return []string{noIssueGroupLabel}, nil
	}
	return ids, nil
}

// statusTypeGroupOrder is the design reference's status type group order.
var statusTypeGroupOrder = []string{"started", "unstarted", "backlog", "triage", "completed", "canceled"}

// orderIssueRecordGroups is the label and status type groups in the design
// reference's order (see sortIssueRecordGroups), as a copy: the counts may be
// a cached slice.
func (s *server) orderIssueRecordGroups(ctx context.Context, query store.IssueRecordQuery, groups []store.IssueRecordGroup) ([]store.IssueRecordGroup, error) {
	if query.GroupBy != "labels" && query.GroupBy != "statusType" {
		return groups, nil
	}
	groups = slices.Clone(groups)
	var labels map[string]store.IssueGroupLabel
	if query.GroupBy == "labels" {
		ids := make([]string, 0, len(groups))
		for _, group := range groups {
			if group.Value != "" {
				ids = append(ids, group.Value)
			}
		}
		var err error
		if labels, err = s.store.IssueGroupLabels(ctx, query.Workspace, ids); err != nil {
			return nil, err
		}
	}
	sortIssueRecordGroups(labels, query, groups)
	return groups, nil
}

// sortIssueRecordGroups orders label groups as team labels, then labels
// outside a group (by name), then grouped labels (by group name, then name),
// and "No label" last; a label group's labels by name; status types started
// first.
func sortIssueRecordGroups(labels map[string]store.IssueGroupLabel, query store.IssueRecordQuery, groups []store.IssueRecordGroup) {
	switch query.GroupBy {
	case "statusType":
		rank := func(value string) int {
			if index := slices.Index(statusTypeGroupOrder, value); index >= 0 {
				return index
			}
			return len(statusTypeGroupOrder)
		}
		slices.SortStableFunc(groups, func(a, b store.IssueRecordGroup) int { return rank(a.Value) - rank(b.Value) })
	case "labels":
		key := func(value string) string {
			if value == "" {
				return "2"
			}
			label, ok := labels[value]
			if !ok {
				return "1￿" + value
			}
			team, grouped, groupName := "1", "0", ""
			if label.Scope != "" && !strings.EqualFold(label.Scope, "workspace") {
				team = "0"
			}
			if label.GroupID != "" && len(query.GroupLabelIDs) == 0 {
				grouped, groupName = "1", strings.ToLower(label.GroupName)
			}
			return "0" + team + grouped + groupName + "\x01" + strings.ToLower(label.Name)
		}
		slices.SortStableFunc(groups, func(a, b store.IssueRecordGroup) int { return strings.Compare(key(a.Value), key(b.Value)) })
	}
}
