package main

import (
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestMCPInvalidSecondaryOperationsDoNotPartiallyWrite(t *testing.T) {
	f := newMCPContractFixture(t)
	before := len(f.data.Issues)
	for _, extra := range []map[string]any{
		{"blocks": []string{"missing-issue"}},
		{"duplicateOf": "missing-issue"},
		{"setReleases": []string{"missing-release"}},
		{"links": []map[string]string{{"title": "Bad URL", "url": "javascript:alert(1)"}}},
	} {
		args := map[string]any{"title": "Must not persist", "team": f.data.Teams[0].ID}
		for key, value := range extra {
			args[key] = value
		}
		response := f.call(t, "save_issue", args)
		if response["result"].(map[string]any)["isError"] != true {
			t.Fatalf("invalid secondary operation succeeded: %v", extra)
		}
		args["id"] = f.data.Issues[0].ID
		response = f.call(t, "save_issue", args)
		if response["result"].(map[string]any)["isError"] != true {
			t.Fatal("invalid update succeeded")
		}
	}
	if got := len(f.repository.Bootstrap().Issues); got != before {
		t.Fatalf("invalid calls left %d partial issues", got-before)
	}
	issue, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, f.data.Issues[0].ID)
	if err != nil || issue.Title != f.data.Issues[0].Title {
		t.Fatal("failed update changed the title", err)
	}
}

func TestMCPIssuePropertyRoundTripAndFilters(t *testing.T) {
	f := newMCPContractFixture(t)
	object := func(name string, args map[string]any) map[string]any {
		t.Helper()
		return mcpSuccess(t, f.call(t, name, args)).(map[string]any)
	}
	milestone := object("save_milestone", map[string]any{"project": f.data.Projects[0].ID, "name": "Contract milestone"})
	created := object("save_issue", map[string]any{
		"team": f.data.Teams[0].Key, "title": "All properties", "description": "first\nlast",
		"state": "In Progress", "priority": 1, "assignee": "me", "delegate": f.data.Users[1].ID,
		"project": f.data.Projects[0].ID, "milestone": milestone["id"], "cycle": f.data.Cycles[0].Name,
		"labels": []string{f.data.Labels[0].Name}, "parentId": f.data.Issues[1].Identifier,
		"estimate": 3, "dueDate": "2026-12-01", "relatedTo": []string{f.data.Issues[0].Identifier},
		"links": []map[string]string{{"title": "Reference", "url": "https://example.test/reference"}},
	})
	var issue domain.Issue
	if err := jsonClone(created, &issue); err != nil {
		t.Fatal(err)
	}
	if issue.Assignee == nil || issue.Assignee.ID != f.data.Viewer.ID || issue.Delegate == nil || issue.Priority != 1 || issue.State.Type != "started" || len(issue.Labels) != 1 || len(issue.Relations) < 1 || issue.Estimate == nil || *issue.Estimate != 3 || issue.ProjectMilestoneID == nil || *issue.ProjectMilestoneID != milestone["id"] {
		t.Fatalf("properties not persisted: %+v", issue)
	}
	for _, filter := range []map[string]any{
		{"assignee": "me"}, {"delegate": f.data.Users[1].Name}, {"state": "started"}, {"project": f.data.Projects[0].Name},
		{"parentId": f.data.Issues[1].Identifier}, {"label": f.data.Labels[0].Name}, {"cycle": f.data.Cycles[0].Name}, {"priority": 1},
	} {
		filter["query"] = "All properties"
		items := object("list_issues", filter)["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["id"] != issue.ID {
			t.Fatalf("filter did not find saved issue: %v => %v", filter, items)
		}
	}
	updated := object("save_issue", map[string]any{"id": issue.ID, "assignee": nil, "delegate": nil, "project": nil, "cycle": nil, "parentId": nil, "dueDate": nil, "estimate": nil, "labels": []string{}, "removeRelatedTo": []string{f.data.Issues[0].ID}, "patch": []map[string]any{{"op": "replace", "old_string": "last", "new_string": "updated"}}})
	issue = domain.Issue{}
	if err := jsonClone(updated, &issue); err != nil {
		t.Fatal(err)
	}
	if issue.Assignee != nil || issue.Delegate != nil || issue.Project != nil || issue.CycleID != nil || issue.ParentID != nil || issue.Estimate != nil || len(issue.Labels) != 0 || len(issue.Relations) != 0 || issue.Description != "first\nupdated" {
		t.Fatalf("clearing fields failed: %+v", issue)
	}
	items := object("list_issues", map[string]any{"query": "All properties", "assignee": nil})["items"].([]any)
	if len(items) != 1 {
		t.Fatal("unassigned filter ignored")
	}
}

func TestMCPRelativeDatePagesKeepTheirAnchor(t *testing.T) {
	f := newMCPContractFixture(t)
	args := map[string]any{"createdAt": "-P1D", "limit": 1}
	first := mcpSuccess(t, f.call(t, "list_issues", args)).(map[string]any)
	if first["nextCursor"] == "" {
		t.Fatal("fixture needs at least two issues")
	}
	args["cursor"] = first["nextCursor"]
	second := mcpSuccess(t, f.call(t, "list_issues", args)).(map[string]any)
	if first["items"].([]any)[0].(map[string]any)["id"] == second["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("relative date repeated first page")
	}
	anchor := time.Date(2026, 9, 11, 14, 23, 0, 0, time.UTC)
	date, err := mcpDateAt("-P1DT2H", anchor)
	if err != nil || !date.Equal(anchor.Add(-26*time.Hour)) {
		t.Fatal("relative duration changed its meaning", date, err)
	}
	for _, value := range []string{"-P", "yesterday", "-P99999999999999999999D"} {
		if _, err := mcpDateAt(value, anchor); err == nil {
			t.Fatalf("invalid date accepted: %s", value)
		}
	}
}
