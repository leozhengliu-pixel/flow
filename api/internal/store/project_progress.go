package store

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func progressEvent(eventType string) bool {
	return strings.HasPrefix(eventType, "issue.") || strings.HasPrefix(eventType, "import.") || eventType == "project.created" || eventType == "project.updated" || eventType == "project.deleted"
}

// refreshProjectProgressHistories rebuilds the compact weekly history used by
// project progress charts. The workspace snapshot is the durable store, so a
// restart retains the same series instead of recomputing it in the browser.
func refreshProjectProgressHistories(data *domain.Bootstrap, now time.Time) bool {
	issuesByProject := make(map[string][]progressIssue, len(data.Projects))
	for _, issue := range data.Issues {
		if issue.Project != nil {
			issuesByProject[issue.Project.ID] = append(issuesByProject[issue.Project.ID], progressIssueOf(issue))
		}
	}
	states := progressStates(*data)
	changed := false
	for index := range data.Projects {
		changed = refreshProjectProgress(&data.Projects[index], issuesByProject[data.Projects[index].ID], states, data.Activities, data.TeamSettings, now) || changed
	}
	return changed
}

// progressIssue holds the issue fields progress histories are computed from.
type progressIssue struct {
	id       string
	created  time.Time
	state    domain.WorkflowState
	estimate *float64
	teamID   string
}

func progressIssueOf(issue domain.Issue) progressIssue {
	return progressIssue{id: issue.ID, created: issue.CreatedAt, state: issue.State, estimate: issue.Estimate, teamID: issue.Team.ID}
}

// refreshProjectProgress rebuilds one project's weekly history from its
// issues and reports whether it changed.
func refreshProjectProgress(project *domain.Project, issues []progressIssue, states map[string]domain.WorkflowState, activities map[string][]domain.ActivityEvent, teamSettings map[string]domain.TeamSettings, now time.Time) bool {
	start := progressStartDate(project, issues)
	if start.IsZero() {
		return clearProjectProgressHistories(project)
	}
	end := utcDay(now)
	if end.Before(start) {
		end = start
	}
	var series progressSeries
	for _, date := range weeklyProgressDates(start, end) {
		series.append(date, progressTotalsAt(states, activities, issues, date, teamSettings))
	}
	return series.store(project)
}

// progressTotals are the counts behind one progress history point.
type progressTotals struct {
	scope, started, completed, backlog, unstarted     int
	scopeEstimate, startedEstimate, completedEstimate float64
}

// add counts an issue in a historical state with a historical estimate
// (sign -1 removes a previously counted issue).
func (totals *progressTotals) add(stateType string, estimate float64, sign int) {
	if !countedProgressState(stateType) {
		return
	}
	weight := float64(sign)
	totals.scope += sign
	totals.scopeEstimate += estimate * weight
	switch stateType {
	case "started":
		totals.started += sign
		totals.startedEstimate += estimate * weight
	case "backlog":
		totals.backlog += sign
	case "unstarted":
		totals.unstarted += sign
	case "completed":
		totals.completed += sign
		totals.completedEstimate += estimate * weight
	}
}

// progressSeries holds the five parallel project progress histories.
type progressSeries struct {
	issueCount, scope, completed, started, progress []domain.ProjectProgressHistoryPoint
}

func (series *progressSeries) append(date time.Time, totals progressTotals) {
	series.issueCount = append(series.issueCount, domain.ProjectProgressHistoryPoint{Date: date, Value: float64(totals.scope), ScopeEstimate: totals.scopeEstimate, ScopeCount: totals.scope})
	series.scope = append(series.scope, domain.ProjectProgressHistoryPoint{Date: date, Value: totals.scopeEstimate, ScopeEstimate: totals.scopeEstimate, ScopeCount: totals.scope})
	series.completed = append(series.completed, domain.ProjectProgressHistoryPoint{Date: date, Value: totals.completedEstimate, CompletedIssueCount: totals.completed, CompletedEstimate: totals.completedEstimate})
	series.started = append(series.started, domain.ProjectProgressHistoryPoint{Date: date, Value: totals.startedEstimate, StartedIssueCount: totals.started, StartedEstimate: totals.startedEstimate})
	series.progress = append(series.progress, domain.ProjectProgressHistoryPoint{Date: date, Value: totals.completedEstimate + totals.startedEstimate*.25, BacklogEstimate: float64(totals.backlog), UnstartedEstimate: float64(totals.unstarted), StartedEstimate: totals.startedEstimate, CompletedEstimate: totals.completedEstimate, ScopeEstimate: totals.scopeEstimate, ScopeCount: totals.scope, CompletedIssueCount: totals.completed, StartedIssueCount: totals.started})
}

// store writes the series onto the project and reports whether it changed.
func (series progressSeries) store(project *domain.Project) bool {
	if slices.Equal(project.IssueCountHistory, series.issueCount) && slices.Equal(project.ScopeHistory, series.scope) && slices.Equal(project.CompletedScopeHistory, series.completed) && slices.Equal(project.InProgressScopeHistory, series.started) && slices.Equal(project.ProgressHistory, series.progress) {
		return false
	}
	project.IssueCountHistory = series.issueCount
	project.ScopeHistory = series.scope
	project.CompletedScopeHistory = series.completed
	project.InProgressScopeHistory = series.started
	project.ProgressHistory = series.progress
	return true
}

// progressTotalsAt evaluates the issue state as of a historical day. The
// issue snapshot contains only the current state, so recorded state
// transition activities are replayed backwards to avoid flattening every
// history point to today's state.
func progressTotalsAt(states map[string]domain.WorkflowState, activities map[string][]domain.ActivityEvent, issues []progressIssue, date time.Time, teamSettings map[string]domain.TeamSettings) progressTotals {
	var totals progressTotals
	for _, issue := range issues {
		if utcDay(issue.created).After(date) {
			continue
		}
		state := historicalIssueState(activities[issue.id], issue.state, states, date)
		if !countedProgressState(state.Type) {
			continue
		}
		totals.add(state.Type, historicalIssueEstimate(activities[issue.id], issue.estimate, date, unestimatedPoints(teamSettings, issue.teamID)), 1)
	}
	return totals
}

func progressStartDate(project *domain.Project, issues []progressIssue) time.Time {
	if project.StartDate != nil {
		if date, err := time.Parse("2006-01-02", *project.StartDate); err == nil {
			return date.UTC()
		}
	}
	var earliest time.Time
	for _, issue := range issues {
		created := utcDay(issue.created)
		if created.IsZero() || (!earliest.IsZero() && !created.Before(earliest)) {
			continue
		}
		earliest = created
	}
	return earliest
}

func weeklyProgressDates(start, end time.Time) []time.Time {
	start = utcDay(start)
	end = utcDay(end)
	dates := make([]time.Time, 0, int(end.Sub(start)/(7*24*time.Hour))+2)
	for date := start; !date.After(end); date = date.AddDate(0, 0, 7) {
		dates = append(dates, date)
	}
	if len(dates) == 0 || !dates[len(dates)-1].Equal(end) {
		dates = append(dates, end)
	}
	return dates
}

func countedProgressIssue(issue domain.Issue) bool {
	return countedProgressState(issue.State.Type)
}

func countedProgressState(stateType string) bool {
	switch stateType {
	case "canceled", "duplicate", "triage":
		return false
	default:
		return true
	}
}

// unestimatedPoints is what an unestimated issue counts as: 1 point unless the
// team turned off "Count unestimated issues".
func unestimatedPoints(teamSettings map[string]domain.TeamSettings, teamID string) float64 {
	if settings, ok := teamSettings[teamID]; ok && settings.EstimateCountUnestimated != nil && !*settings.EstimateCountUnestimated {
		return 0
	}
	return 1
}

func issueEstimate(issue domain.Issue, unestimated float64) float64 {
	return estimateValue(issue.Estimate, unestimated)
}

func estimateValue(estimate *float64, unestimated float64) float64 {
	if estimate == nil {
		return unestimated
	}
	return max(*estimate, 0)
}

func historicalIssueState(events []domain.ActivityEvent, current domain.WorkflowState, states map[string]domain.WorkflowState, date time.Time) domain.WorkflowState {
	state := current
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if utcDay(event.CreatedAt).After(date) && event.Type == "issue.updated" {
			beforeID := strings.TrimSpace(event.Metadata["stateBeforeId"])
			if beforeID != "" {
				if previous, ok := states[beforeID]; ok {
					state = previous
				} else if beforeType := strings.TrimSpace(event.Metadata["stateBeforeType"]); beforeType != "" {
					state = domain.WorkflowState{ID: beforeID, Name: event.Metadata["stateBefore"], Type: beforeType}
				}
			}
		}
	}
	return state
}

func historicalIssueEstimate(events []domain.ActivityEvent, current *float64, date time.Time, unestimated float64) float64 {
	estimate := estimateValue(current, unestimated)
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if utcDay(event.CreatedAt).After(date) && event.Type == "issue.updated" {
			if before, err := strconv.ParseFloat(strings.TrimSpace(event.Metadata["estimateBefore"]), 64); err == nil {
				estimate = max(before, 0)
			}
		}
	}
	return estimate
}

func clearProjectProgressHistories(project *domain.Project) bool {
	if len(project.IssueCountHistory) == 0 && len(project.ScopeHistory) == 0 && len(project.CompletedScopeHistory) == 0 && len(project.InProgressScopeHistory) == 0 && len(project.ProgressHistory) == 0 {
		return false
	}
	project.IssueCountHistory = nil
	project.ScopeHistory = nil
	project.CompletedScopeHistory = nil
	project.InProgressScopeHistory = nil
	project.ProgressHistory = nil
	return true
}

func utcDay(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
