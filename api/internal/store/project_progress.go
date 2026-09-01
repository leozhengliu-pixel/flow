package store

import (
	"slices"
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
			scopeCount, startedCount, completedCount, backlogCount, unstartedCount := progressCounts(issues, date)
			stamp := domain.ProjectProgressHistoryPoint{Date: date, Value: float64(scopeCount), ScopeEstimate: float64(scopeCount)}
			issueCount = append(issueCount, stamp)
			scope = append(scope, stamp)
			completed = append(completed, domain.ProjectProgressHistoryPoint{Date: date, Value: float64(completedCount)})
			started = append(started, domain.ProjectProgressHistoryPoint{Date: date, Value: float64(startedCount)})
			progress = append(progress, domain.ProjectProgressHistoryPoint{Date: date, Value: float64(completedCount) + float64(startedCount-completedCount)*.25, BacklogEstimate: float64(backlogCount), UnstartedEstimate: float64(unstartedCount), StartedEstimate: float64(startedCount - completedCount), CompletedEstimate: float64(completedCount), ScopeEstimate: float64(scopeCount)})
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

func progressCounts(issues []domain.Issue, date time.Time) (scope, started, completed, backlog, unstarted int) {
	for _, issue := range issues {
		if !countedProgressIssue(issue) || utcDay(issue.CreatedAt).After(date) {
			continue
		}
		scope++
		if issue.State.Type == "started" || issue.State.Type == "completed" {
			started++
		}
		if issue.State.Type == "backlog" {
			backlog++
		}
		if issue.State.Type == "unstarted" {
			unstarted++
		}
		if issue.State.Type == "completed" {
			completedAt := issue.CompletedAt
			if completedAt == nil {
				fallback := issue.UpdatedAt
				completedAt = &fallback
			}
			if !utcDay(*completedAt).After(date) {
				completed++
			}
		}
	}
	return scope, started, completed, backlog, unstarted
}

func countedProgressIssue(issue domain.Issue) bool {
	switch issue.State.Type {
	case "canceled", "duplicate", "triage":
		return false
	default:
		return true
	}
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
