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
	issuesByProject := make(map[string][]domain.Issue, len(data.Projects))
	states := make(map[string]domain.WorkflowState, len(data.States))
	for _, state := range data.States {
		states[state.ID] = state
	}
	for _, issue := range data.Issues {
		if issue.Project != nil {
			issuesByProject[issue.Project.ID] = append(issuesByProject[issue.Project.ID], issue)
		}
	}
	changed := false
	for index := range data.Projects {
		project := &data.Projects[index]
		issues := issuesByProject[project.ID]
		start := progressStartDate(project, issues)
		if start.IsZero() {
			changed = clearProjectProgressHistories(project) || changed
			continue
		}
		end := utcDay(now)
		if end.Before(start) {
			end = start
		}
		points := weeklyProgressDates(start, end)
		issueCount := make([]domain.ProjectProgressHistoryPoint, 0, len(points))
		scope := make([]domain.ProjectProgressHistoryPoint, 0, len(points))
		completed := make([]domain.ProjectProgressHistoryPoint, 0, len(points))
		started := make([]domain.ProjectProgressHistoryPoint, 0, len(points))
		progress := make([]domain.ProjectProgressHistoryPoint, 0, len(points))
		for _, date := range points {
			scopeCount, startedCount, completedCount, backlogCount, unstartedCount, scopeEstimate, startedEstimate, completedEstimate := progressCounts(states, data.Activities, issues, date)
			issueCount = append(issueCount, domain.ProjectProgressHistoryPoint{Date: date, Value: float64(scopeCount), ScopeEstimate: scopeEstimate, ScopeCount: scopeCount})
			scope = append(scope, domain.ProjectProgressHistoryPoint{Date: date, Value: scopeEstimate, ScopeEstimate: scopeEstimate, ScopeCount: scopeCount})
			completed = append(completed, domain.ProjectProgressHistoryPoint{Date: date, Value: completedEstimate, CompletedIssueCount: completedCount, CompletedEstimate: completedEstimate})
			started = append(started, domain.ProjectProgressHistoryPoint{Date: date, Value: startedEstimate, StartedIssueCount: startedCount, StartedEstimate: startedEstimate})
			progress = append(progress, domain.ProjectProgressHistoryPoint{Date: date, Value: completedEstimate + startedEstimate*.25, BacklogEstimate: float64(backlogCount), UnstartedEstimate: float64(unstartedCount), StartedEstimate: startedEstimate, CompletedEstimate: completedEstimate, ScopeEstimate: scopeEstimate, ScopeCount: scopeCount, CompletedIssueCount: completedCount, StartedIssueCount: startedCount})
		}
		if !slices.Equal(project.IssueCountHistory, issueCount) || !slices.Equal(project.ScopeHistory, scope) || !slices.Equal(project.CompletedScopeHistory, completed) || !slices.Equal(project.InProgressScopeHistory, started) || !slices.Equal(project.ProgressHistory, progress) {
			project.IssueCountHistory = issueCount
			project.ScopeHistory = scope
			project.CompletedScopeHistory = completed
			project.InProgressScopeHistory = started
			project.ProgressHistory = progress
			changed = true
		}
	}
	return changed
}

func progressStartDate(project *domain.Project, issues []domain.Issue) time.Time {
	if project.StartDate != nil {
		if date, err := time.Parse("2006-01-02", *project.StartDate); err == nil {
			return date.UTC()
		}
	}
	var earliest time.Time
	for _, issue := range issues {
		created := utcDay(issue.CreatedAt)
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

// progressCounts evaluates the issue state as of a historical day. The issue
// snapshot contains only the current state, so recorded state transition
// activities are replayed backwards to avoid flattening every history point
// to today's state.
func progressCounts(states map[string]domain.WorkflowState, activities map[string][]domain.ActivityEvent, issues []domain.Issue, date time.Time) (scope, started, completed, backlog, unstarted int, scopeEstimate, startedEstimate, completedEstimate float64) {
	for _, issue := range issues {
		if utcDay(issue.CreatedAt).After(date) {
			continue
		}
		state := historicalIssueState(activities[issue.ID], issue.State, states, date)
		if !countedProgressState(state.Type) {
			continue
		}
		scope++
		estimate := historicalIssueEstimate(activities[issue.ID], issue, date)
		scopeEstimate += estimate
		switch state.Type {
		case "started":
			started++
			startedEstimate += estimate
		case "backlog":
			backlog++
		case "unstarted":
			unstarted++
		case "completed":
			completed++
			completedEstimate += estimate
		}
	}
	return scope, started, completed, backlog, unstarted, scopeEstimate, startedEstimate, completedEstimate
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

func issueEstimate(issue domain.Issue) float64 {
	if issue.Estimate == nil {
		return 1
	}
	return max(*issue.Estimate, 0)
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

func historicalIssueEstimate(events []domain.ActivityEvent, issue domain.Issue, date time.Time) float64 {
	estimate := issueEstimate(issue)
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
