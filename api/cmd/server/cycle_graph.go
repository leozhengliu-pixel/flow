package main

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Linear-style cycle graph. The issue snapshot only holds the current cycle and
// state, so every day of the cycle is reconstructed by replaying recorded issue
// activity backwards from the snapshot:
//   - cycle membership from "cycle" / "cycleBefore" (and "initialCycle" on
//     issue.created),
//   - workflow state from "stateBeforeId" / "stateBeforeType", falling back to
//     startedAt / completedAt / canceledAt when an issue has no state history,
//   - estimate from "estimateBefore".
// A day's value is the state at the end of that day in the team timezone (or
// now, for today). Days after today are marked future and carry no actuals.

const cycleGraphMaxIssues = 10000

type cycleGraphPoint struct {
	Date            string  `json:"date"`
	Future          bool    `json:"future,omitempty"`
	Scope           int     `json:"scope"`
	Started         int     `json:"started"`
	Completed       int     `json:"completed"`
	ScopePoints     float64 `json:"scopePoints"`
	StartedPoints   float64 `json:"startedPoints"`
	CompletedPoints float64 `json:"completedPoints"`
}

type cycleGraphSummary struct {
	Scope           int     `json:"scope"`
	Started         int     `json:"started"`
	Completed       int     `json:"completed"`
	Added           int     `json:"added"`
	Removed         int     `json:"removed"`
	ScopePoints     float64 `json:"scopePoints"`
	StartedPoints   float64 `json:"startedPoints"`
	CompletedPoints float64 `json:"completedPoints"`
	AddedPoints     float64 `json:"addedPoints"`
	RemovedPoints   float64 `json:"removedPoints"`
}

type cycleGraphBreakdownRow struct {
	ID              string  `json:"id"`
	Scope           int     `json:"scope"`
	Started         int     `json:"started"`
	Completed       int     `json:"completed"`
	ScopePoints     float64 `json:"scopePoints"`
	StartedPoints   float64 `json:"startedPoints"`
	CompletedPoints float64 `json:"completedPoints"`
}

type cycleGraphBreakdown struct {
	Assignees []cycleGraphBreakdownRow `json:"assignees"`
	Labels    []cycleGraphBreakdownRow `json:"labels"`
	Projects  []cycleGraphBreakdownRow `json:"projects"`
}

type cycleGraph struct {
	CycleID   string              `json:"cycleId"`
	Timezone  string              `json:"timezone"`
	StartDate string              `json:"startDate"`
	EndDate   string              `json:"endDate"`
	Today     string              `json:"today,omitempty"`
	Estimates bool                `json:"estimates"`
	Days      []cycleGraphPoint   `json:"days"`
	Summary   cycleGraphSummary   `json:"summary"`
	Breakdown cycleGraphBreakdown `json:"breakdown"`
}

type cycleGraphInput struct {
	Cycle    domain.Cycle
	Issues   []domain.Issue
	Events   map[string][]domain.ActivityEvent
	States   []domain.WorkflowState
	Settings domain.TeamSettings
	Now      time.Time
}

// cycleGraphTimezone resolves the team timezone, falling back to UTC.
func cycleGraphTimezone(name string) (*time.Location, string) {
	name = strings.TrimSpace(name)
	if name != "" {
		if location, err := time.LoadLocation(name); err == nil {
			return location, name
		}
	}
	return time.UTC, "UTC"
}

type cycleGraphTracker struct {
	issue        domain.Issue
	cycleEvents  []domain.ActivityEvent
	stateEvents  []domain.ActivityEvent
	estimateEvts []domain.ActivityEvent
	initialCycle string
	states       map[string]domain.WorkflowState
	unestimated  float64
	allowZero    bool
}

func newCycleGraphTracker(issue domain.Issue, events []domain.ActivityEvent, states map[string]domain.WorkflowState, unestimated float64, allowZero bool) cycleGraphTracker {
	sorted := slices.Clone(events)
	slices.SortStableFunc(sorted, func(a, b domain.ActivityEvent) int { return a.CreatedAt.Compare(b.CreatedAt) })
	tracker := cycleGraphTracker{issue: issue, states: states, unestimated: unestimated, allowZero: allowZero}
	for _, event := range sorted {
		if event.Type == "issue.created" {
			if value, ok := event.Metadata["initialCycle"]; ok {
				tracker.initialCycle = value
			}
			continue
		}
		if event.Type != "issue.updated" {
			continue
		}
		if _, ok := event.Metadata["cycle"]; ok {
			tracker.cycleEvents = append(tracker.cycleEvents, event)
		}
		if event.Metadata["stateBeforeId"] != "" || event.Metadata["stateBeforeType"] != "" || event.Metadata["stateBefore"] != "" {
			tracker.stateEvents = append(tracker.stateEvents, event)
		}
		if _, ok := event.Metadata["estimateBefore"]; ok {
			tracker.estimateEvts = append(tracker.estimateEvts, event)
		}
	}
	return tracker
}

func (t cycleGraphTracker) exists(at time.Time) bool {
	return !t.issue.CreatedAt.After(at)
}

// cycleAt is the cycle the issue belonged to at the instant.
func (t cycleGraphTracker) cycleAt(at time.Time) string {
	if !t.exists(at) {
		return ""
	}
	cycle := optionalID(t.issue.CycleID)
	for index := len(t.cycleEvents) - 1; index >= 0; index-- {
		event := t.cycleEvents[index]
		if !event.CreatedAt.After(at) {
			break
		}
		if before, ok := event.Metadata["cycleBefore"]; ok {
			cycle = before
		} else if index > 0 {
			cycle = t.cycleEvents[index-1].Metadata["cycle"]
		} else {
			// Legacy events without cycleBefore: before the first recorded
			// change the issue was in its creation cycle, or no cycle.
			cycle = t.initialCycle
		}
	}
	return cycle
}

func (t cycleGraphTracker) stateTypeAt(at time.Time) string {
	current := t.issue.State.Type
	if len(t.stateEvents) == 0 {
		started := t.issue.StartedAt != nil && !t.issue.StartedAt.After(at)
		fallback := "unstarted"
		if started {
			fallback = "started"
		}
		switch current {
		case "completed":
			if t.issue.CompletedAt != nil && t.issue.CompletedAt.After(at) {
				return fallback
			}
		case "canceled", "duplicate":
			if t.issue.CanceledAt != nil && t.issue.CanceledAt.After(at) {
				return fallback
			}
		case "started":
			if t.issue.StartedAt != nil && t.issue.StartedAt.After(at) {
				return "unstarted"
			}
		}
		return current
	}
	state := current
	for index := len(t.stateEvents) - 1; index >= 0; index-- {
		event := t.stateEvents[index]
		if !event.CreatedAt.After(at) {
			break
		}
		if value := strings.TrimSpace(event.Metadata["stateBeforeType"]); value != "" {
			state = value
		} else if previous, ok := t.states[strings.TrimSpace(event.Metadata["stateBeforeId"])]; ok {
			state = previous.Type
		} else if name := strings.TrimSpace(event.Metadata["stateBefore"]); name != "" {
			for _, candidate := range t.states {
				if candidate.Name == name && (candidate.TeamID == "" || candidate.TeamID == t.issue.Team.ID) {
					state = candidate.Type
					break
				}
			}
		}
	}
	return state
}

func (t cycleGraphTracker) weightAt(at time.Time) float64 {
	weight := func(estimate *float64) float64 {
		if estimate == nil || (*estimate <= 0 && !t.allowZero) {
			return t.unestimated
		}
		return max(*estimate, 0)
	}
	value := weight(t.issue.Estimate)
	for index := len(t.estimateEvts) - 1; index >= 0; index-- {
		event := t.estimateEvts[index]
		if !event.CreatedAt.After(at) {
			break
		}
		if before, err := strconv.ParseFloat(strings.TrimSpace(event.Metadata["estimateBefore"]), 64); err == nil {
			value = weight(&before)
		}
	}
	return value
}

func cycleGraphCountsInScope(stateType string) bool {
	switch stateType {
	case "canceled", "duplicate", "triage":
		return false
	default:
		return true
	}
}

// inScope reports whether the issue counted towards the cycle scope at the
// instant, plus its state type and weight at that instant.
func (t cycleGraphTracker) inScope(cycleID string, at time.Time) (bool, string, float64) {
	if t.cycleAt(at) != cycleID {
		return false, "", 0
	}
	state := t.stateTypeAt(at)
	if !cycleGraphCountsInScope(state) {
		return false, state, 0
	}
	return true, state, t.weightAt(at)
}

func buildCycleGraph(input cycleGraphInput) cycleGraph {
	location, zone := cycleGraphTimezone(input.Settings.Timezone)
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	states := make(map[string]domain.WorkflowState, len(input.States))
	for _, state := range input.States {
		states[state.ID] = state
	}
	unestimated := 1.0
	if input.Settings.EstimateCountUnestimated != nil && !*input.Settings.EstimateCountUnestimated {
		unestimated = 0
	}
	trackers := make([]cycleGraphTracker, 0, len(input.Issues))
	for _, issue := range input.Issues {
		trackers = append(trackers, newCycleGraphTracker(issue, input.Events[issue.ID], states, unestimated, input.Settings.EstimateAllowZero))
	}
	start := input.Cycle.StartsAt.UTC()
	end := input.Cycle.EndsAt.UTC()
	if end.Before(start) {
		end = start
	}
	firstDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	lastDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	graph := cycleGraph{
		CycleID:   input.Cycle.ID,
		Timezone:  zone,
		StartDate: firstDay.Format(time.DateOnly),
		EndDate:   lastDay.Format(time.DateOnly),
		Estimates: input.Settings.EstimateType != "" && input.Settings.EstimateType != "notUsed",
		Days:      []cycleGraphPoint{},
	}
	var asOf time.Time
	for day := firstDay; !day.After(lastDay) && len(graph.Days) < 400; day = day.AddDate(0, 0, 1) {
		// Cycle dates are calendar dates; the team timezone decides where each
		// calendar day begins and ends.
		dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location)
		point := cycleGraphPoint{Date: day.Format(time.DateOnly)}
		if dayStart.After(now) {
			point.Future = true
			graph.Days = append(graph.Days, point)
			continue
		}
		sample := dayStart.AddDate(0, 0, 1).Add(-time.Nanosecond)
		if sample.After(now) {
			sample = now
			graph.Today = point.Date
		}
		asOf = sample
		for _, tracker := range trackers {
			counted, state, weight := tracker.inScope(input.Cycle.ID, sample)
			if !counted {
				continue
			}
			point.Scope++
			point.ScopePoints += weight
			switch state {
			case "completed":
				point.Completed++
				point.CompletedPoints += weight
				point.Started++
				point.StartedPoints += weight
			case "started":
				point.Started++
				point.StartedPoints += weight
			}
		}
		graph.Days = append(graph.Days, point)
	}
	if asOf.IsZero() {
		// Upcoming cycle: report the planned scope as of now.
		asOf = now
	}
	baseline := time.Date(firstDay.Year(), firstDay.Month(), firstDay.Day(), 0, 0, 0, 0, location)
	breakdown := map[string]map[string]*cycleGraphBreakdownRow{"assignees": {}, "labels": {}, "projects": {}}
	add := func(kind, id, state string, weight float64) {
		row := breakdown[kind][id]
		if row == nil {
			row = &cycleGraphBreakdownRow{ID: id}
			breakdown[kind][id] = row
		}
		row.Scope++
		row.ScopePoints += weight
		if state == "started" || state == "completed" {
			row.Started++
			row.StartedPoints += weight
		}
		if state == "completed" {
			row.Completed++
			row.CompletedPoints += weight
		}
	}
	for _, tracker := range trackers {
		counted, state, weight := tracker.inScope(input.Cycle.ID, asOf)
		wasCounted := false
		var baselineWeight float64
		if baseline.Before(asOf) {
			wasCounted, _, baselineWeight = tracker.inScope(input.Cycle.ID, baseline)
		} else {
			wasCounted, baselineWeight = counted, weight
		}
		switch {
		case counted && !wasCounted:
			graph.Summary.Added++
			graph.Summary.AddedPoints += weight
		case !counted && wasCounted:
			graph.Summary.Removed++
			graph.Summary.RemovedPoints += baselineWeight
		}
		if !counted {
			continue
		}
		graph.Summary.Scope++
		graph.Summary.ScopePoints += weight
		if state == "started" || state == "completed" {
			graph.Summary.Started++
			graph.Summary.StartedPoints += weight
		}
		if state == "completed" {
			graph.Summary.Completed++
			graph.Summary.CompletedPoints += weight
		}
		// Breakdowns describe the issues as they are now.
		assignee := ""
		if tracker.issue.Assignee != nil {
			assignee = tracker.issue.Assignee.ID
		}
		add("assignees", assignee, state, weight)
		for _, label := range tracker.issue.Labels {
			add("labels", label.ID, state, weight)
		}
		project := ""
		if tracker.issue.Project != nil {
			project = tracker.issue.Project.ID
		}
		add("projects", project, state, weight)
	}
	rows := func(kind string) []cycleGraphBreakdownRow {
		result := make([]cycleGraphBreakdownRow, 0, len(breakdown[kind]))
		for _, row := range breakdown[kind] {
			result = append(result, *row)
		}
		slices.SortFunc(result, func(a, b cycleGraphBreakdownRow) int {
			if a.Scope != b.Scope {
				return b.Scope - a.Scope
			}
			return strings.Compare(a.ID, b.ID)
		})
		return result
	}
	graph.Breakdown = cycleGraphBreakdown{Assignees: rows("assignees"), Labels: rows("labels"), Projects: rows("projects")}
	return graph
}

func (s *server) getCycleGraph(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	cycle, err := cycleByID(&data, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "cycle not found")
		return
	}
	_, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	query.Archived = "all"
	query.Limit = 500
	issues := []domain.Issue{}
	seen := map[string]bool{}
	load := func(q store.IssueRecordQuery) error {
		for {
			page, err := s.store.QueryIssueRecords(r.Context(), q)
			if err != nil {
				return err
			}
			for _, issue := range page.Items {
				if !seen[issue.ID] {
					seen[issue.ID] = true
					issues = append(issues, issue)
				}
			}
			if !page.HasMore || page.NextCursor == "" || len(issues) >= cycleGraphMaxIssues {
				return nil
			}
			q.Cursor = page.NextCursor
		}
	}
	current := query
	current.Filter = store.IssueFilter{Field: "cycle", Operator: "in", Values: []string{cycle.ID}}
	if err := load(current); err != nil {
		issueRecordsError(w, err)
		return
	}
	historic, err := s.store.CycleHistoryIssueIDs(r.Context(), query.Workspace, cycle.ID)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	historic = slices.DeleteFunc(historic, func(id string) bool { return seen[id] })
	for chunk := range slices.Chunk(historic, 500) {
		past := query
		past.IssueIDs = chunk
		past.RestrictToIssueIDs = true
		if err := load(past); err != nil {
			issueRecordsError(w, err)
			return
		}
	}
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.ID)
	}
	events, err := s.store.IssueHistoryEvents(r.Context(), query.Workspace, ids)
	if err != nil {
		if errors.Is(err, store.ErrAuthForbidden) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		issueRecordsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildCycleGraph(cycleGraphInput{Cycle: *cycle, Issues: issues, Events: events, States: data.States, Settings: data.TeamSettings[cycle.TeamID], Now: time.Now()}))
}
