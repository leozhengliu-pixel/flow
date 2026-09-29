package main

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func graphTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func graphIssue(id, cycleID, stateType string, estimate *float64) domain.Issue {
	issue := domain.Issue{ID: id, CreatedAt: graphTime("2026-08-30T00:00:00Z"), Team: domain.Team{ID: "team"}, State: domain.WorkflowState{ID: "state_" + stateType, Type: stateType, TeamID: "team"}, Estimate: estimate}
	if cycleID != "" {
		issue.CycleID = stringPointer(cycleID)
	}
	return issue
}

func graphEvent(at string, eventType string, metadata map[string]string) domain.ActivityEvent {
	return domain.ActivityEvent{ID: at + eventType, Type: eventType, CreatedAt: graphTime(at), Metadata: metadata}
}

func float(value float64) *float64 { return &value }

var graphCycle = domain.Cycle{ID: "cycle", TeamID: "team", StartsAt: graphTime("2026-09-01T00:00:00Z"), EndsAt: graphTime("2026-09-05T00:00:00Z")}

func graphDay(t *testing.T, graph cycleGraph, date string) cycleGraphPoint {
	t.Helper()
	for _, day := range graph.Days {
		if day.Date == date {
			return day
		}
	}
	t.Fatalf("missing day %s in %#v", date, graph.Days)
	return cycleGraphPoint{}
}

func TestCycleGraphScopeHistory(t *testing.T) {
	created := func(cycle string) domain.ActivityEvent {
		return graphEvent("2026-08-30T00:00:00Z", "issue.created", map[string]string{"initialCycle": cycle})
	}
	issues := []domain.Issue{
		graphIssue("steady", "cycle", "unstarted", nil),
		graphIssue("added", "cycle", "started", nil),
		graphIssue("removed", "", "unstarted", nil),
		graphIssue("reopened", "cycle", "unstarted", nil),
		graphIssue("canceled", "cycle", "canceled", nil),
		graphIssue("elsewhere", "other", "completed", nil),
	}
	events := map[string][]domain.ActivityEvent{
		"steady":  {created("cycle")},
		"added":   {created(""), graphEvent("2026-09-02T10:00:00Z", "issue.updated", map[string]string{"cycle": "cycle", "cycleBefore": ""}), graphEvent("2026-09-03T08:00:00Z", "issue.updated", map[string]string{"stateBeforeType": "unstarted", "stateType": "started"})},
		"removed": {created("cycle"), graphEvent("2026-09-02T10:00:00Z", "issue.updated", map[string]string{"cycle": "", "cycleBefore": "cycle"})},
		"reopened": {created("cycle"),
			graphEvent("2026-09-01T09:00:00Z", "issue.updated", map[string]string{"stateBeforeType": "unstarted", "stateType": "completed"}),
			graphEvent("2026-09-02T09:00:00Z", "issue.updated", map[string]string{"stateBeforeType": "completed", "stateType": "unstarted"})},
		"canceled":  {created("cycle"), graphEvent("2026-09-02T09:00:00Z", "issue.updated", map[string]string{"stateBeforeType": "started", "stateType": "canceled"})},
		"elsewhere": {created("other")},
	}
	graph := buildCycleGraph(cycleGraphInput{Cycle: graphCycle, Issues: issues, Events: events, Settings: domain.TeamSettings{Timezone: "Etc/UTC"}, Now: graphTime("2026-09-03T12:00:00Z")})
	if len(graph.Days) != 5 || graph.StartDate != "2026-09-01" || graph.EndDate != "2026-09-05" || graph.Today != "2026-09-03" {
		t.Fatalf("graph frame=%#v", graph)
	}
	// Sep 1: steady, removed, reopened (completed), canceled (started).
	if day := graphDay(t, graph, "2026-09-01"); day.Scope != 4 || day.Started != 2 || day.Completed != 1 {
		t.Fatalf("day 1=%#v", day)
	}
	// Sep 2: added joins, removed leaves, reopened is open again, canceled drops out.
	if day := graphDay(t, graph, "2026-09-02"); day.Scope != 3 || day.Started != 0 || day.Completed != 0 {
		t.Fatalf("day 2=%#v", day)
	}
	if day := graphDay(t, graph, "2026-09-03"); day.Scope != 3 || day.Started != 1 || day.Completed != 0 {
		t.Fatalf("day 3=%#v", day)
	}
	if day := graphDay(t, graph, "2026-09-04"); !day.Future || day.Scope != 0 {
		t.Fatalf("future day=%#v", day)
	}
	summary := graph.Summary
	if summary.Scope != 3 || summary.Started != 1 || summary.Completed != 0 || summary.Added != 1 || summary.Removed != 2 {
		t.Fatalf("summary=%#v", summary)
	}
}

func TestCycleGraphPointsAndLegacyTimestamps(t *testing.T) {
	legacy := graphIssue("legacy", "cycle", "completed", float(3))
	legacy.StartedAt = pointerTime(graphTime("2026-09-01T12:00:00Z"))
	legacy.CompletedAt = pointerTime(graphTime("2026-09-02T12:00:00Z"))
	resized := graphIssue("resized", "cycle", "unstarted", float(5))
	unestimated := graphIssue("unestimated", "cycle", "unstarted", nil)
	events := map[string][]domain.ActivityEvent{
		"resized": {graphEvent("2026-09-02T06:00:00Z", "issue.updated", map[string]string{"estimate": "5", "estimateBefore": "2"})},
	}
	count := true
	graph := buildCycleGraph(cycleGraphInput{Cycle: graphCycle, Issues: []domain.Issue{legacy, resized, unestimated}, Events: events, Settings: domain.TeamSettings{EstimateType: "fibonacci", EstimateCountUnestimated: &count}, Now: graphTime("2026-09-10T00:00:00Z")})
	if !graph.Estimates || graph.Today != "" {
		t.Fatalf("graph=%#v", graph)
	}
	if day := graphDay(t, graph, "2026-09-01"); day.ScopePoints != 3+2+1 || day.StartedPoints != 3 || day.CompletedPoints != 0 || day.Scope != 3 {
		t.Fatalf("day 1=%#v", day)
	}
	if day := graphDay(t, graph, "2026-09-02"); day.ScopePoints != 3+5+1 || day.CompletedPoints != 3 || day.Completed != 1 {
		t.Fatalf("day 2=%#v", day)
	}
	if graph.Summary.ScopePoints != 9 || graph.Summary.CompletedPoints != 3 {
		t.Fatalf("summary=%#v", graph.Summary)
	}
	count = false
	graph = buildCycleGraph(cycleGraphInput{Cycle: graphCycle, Issues: []domain.Issue{legacy, resized, unestimated}, Events: events, Settings: domain.TeamSettings{EstimateType: "fibonacci", EstimateCountUnestimated: &count}, Now: graphTime("2026-09-10T00:00:00Z")})
	if graph.Summary.ScopePoints != 8 || graph.Summary.Scope != 3 {
		t.Fatalf("unestimated excluded summary=%#v", graph.Summary)
	}
}

func TestCycleGraphTimezoneDayBoundaries(t *testing.T) {
	// 03:00 UTC on Sep 2 is still the evening of Sep 1 in Los Angeles.
	issue := graphIssue("late", "cycle", "unstarted", nil)
	events := map[string][]domain.ActivityEvent{"late": {graphEvent("2026-09-02T03:00:00Z", "issue.updated", map[string]string{"cycle": "cycle", "cycleBefore": ""})}}
	input := cycleGraphInput{Cycle: graphCycle, Issues: []domain.Issue{issue}, Events: events, Now: graphTime("2026-09-10T00:00:00Z")}
	utc := buildCycleGraph(input)
	if utc.Timezone != "UTC" || graphDay(t, utc, "2026-09-01").Scope != 0 || graphDay(t, utc, "2026-09-02").Scope != 1 {
		t.Fatalf("utc=%#v", utc)
	}
	input.Settings.Timezone = "America/Los_Angeles"
	pacific := buildCycleGraph(input)
	if pacific.Timezone != "America/Los_Angeles" || graphDay(t, pacific, "2026-09-01").Scope != 1 {
		t.Fatalf("pacific=%#v", pacific)
	}
	// Today is judged in the team timezone as well.
	input.Now = graphTime("2026-09-02T05:00:00Z")
	if graph := buildCycleGraph(input); graph.Today != "2026-09-01" || !graphDay(t, graph, "2026-09-02").Future {
		t.Fatalf("pacific today=%#v", graph)
	}
	input.Settings.Timezone = "Not/AZone"
	if graph := buildCycleGraph(input); graph.Timezone != "UTC" {
		t.Fatalf("invalid timezone=%#v", graph)
	}
}

func TestCycleGraphEndpointTracksRemovedIssues(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	var cycle domain.Cycle
	for _, candidate := range bootstrap.Cycles {
		if candidate.Status == "current" {
			cycle = candidate
			break
		}
	}
	if cycle.ID == "" {
		t.Skip("fixture has no current cycle")
	}
	var issue domain.Issue
	for _, candidate := range bootstrap.Issues {
		if candidate.Team.ID == cycle.TeamID && optionalID(candidate.CycleID) != cycle.ID && candidate.State.Type != "canceled" && candidate.State.Type != "triage" {
			issue = candidate
			break
		}
	}
	if issue.ID == "" {
		t.Skip("fixture has no issue outside the current cycle")
	}
	before := requestJSON[cycleGraph](t, handler, http.MethodGet, "/api/cycles/"+cycle.ID+"/graph", nil, http.StatusOK)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"cycleId": cycle.ID}, http.StatusOK)
	added := requestJSON[cycleGraph](t, handler, http.MethodGet, "/api/cycles/"+cycle.ID+"/graph", nil, http.StatusOK)
	if added.Summary.Scope != before.Summary.Scope+1 || added.Today == "" {
		t.Fatalf("before=%#v added=%#v", before.Summary, added.Summary)
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"cycleId": ""}, http.StatusOK)
	_, events, err := repository.IssueContent(t.Context(), bootstrap.Workspace.URLKey, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Metadata["cycle"] != "" || last.Metadata["cycleBefore"] != cycle.ID {
		t.Fatalf("removal activity=%#v", last)
	}
	ids, err := repository.CycleHistoryIssueIDs(t.Context(), bootstrap.Workspace.URLKey, cycle.ID)
	if err != nil || !contains(ids, issue.ID) {
		t.Fatalf("history ids=%v err=%v", ids, err)
	}
	removed := requestJSON[cycleGraph](t, handler, http.MethodGet, "/api/cycles/"+cycle.ID+"/graph", nil, http.StatusOK)
	if removed.Summary.Scope != before.Summary.Scope {
		t.Fatalf("removed=%#v", removed.Summary)
	}
	requestJSON[any](t, handler, http.MethodGet, "/api/cycles/missing/graph", nil, http.StatusNotFound)
}

func pointerTime(value time.Time) *time.Time { return &value }
