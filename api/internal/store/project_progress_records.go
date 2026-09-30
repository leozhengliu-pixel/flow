package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sort"
	"strconv"
	"time"

	"flow/api/internal/domain"
)

// Project progress histories are derived from the project's issues and their
// state/estimate transition activities. Issue records live outside the
// workspace snapshot, so the record-backed write paths keep the histories of
// just the projects they touch current:
//
//   - When the stored weekly grid already ends today, an issue write adjusts
//     today's point by the before/after contribution of the changed issues.
//   - When only today's point is missing (first write of the day), today's
//     point is recomputed from the project's current issues.
//   - Otherwise (no history yet, a new weekly point, a new start date) the
//     project's history is rebuilt from its issues and their transition
//     activities.
//
// Every step reads only the affected project's records.

// progressReader is satisfied by both *sql.DB wrappers and transactions.
type progressReader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// projectProgressIssues reads the fields progress needs (creation day,
// workflow state, team and estimate) from the indexed issue columns and the
// sparse estimate attribute, without decoding issue documents.
func projectProgressIssues(ctx context.Context, reader progressReader, workspace, projectID string, states map[string]domain.WorkflowState) ([]progressIssue, error) {
	rows, err := reader.QueryContext(ctx, `SELECT i.id,i.created_at,i.state_id,i.state_type,i.team_id,COALESCE(a.value,'') FROM issue_records i LEFT JOIN issue_attribute_records a ON a.workspace_key=i.workspace_key AND a.issue_id=i.id AND a.field='estimate' WHERE i.workspace_key=? AND i.project_id=?`, workspace, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issues := []progressIssue{}
	for rows.Next() {
		var id, created, stateID, stateType, teamID, estimateText string
		if err := rows.Scan(&id, &created, &stateID, &stateType, &teamID, &estimateText); err != nil {
			return nil, err
		}
		createdAt, err := time.Parse(issueRecordTimestamp, created)
		if err != nil {
			createdAt, _ = time.Parse(time.RFC3339Nano, created)
		}
		// Rows are selected by the indexed project_id column, which is
		// authoritative for membership.
		issue := progressIssue{id: id, created: createdAt, state: domain.WorkflowState{ID: stateID, Type: stateType}, teamID: teamID}
		if state, ok := states[stateID]; ok {
			issue.state = state
		}
		if estimateText != "" {
			if estimate, err := strconv.ParseFloat(estimateText, 64); err == nil {
				issue.estimate = &estimate
			}
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

// progressActivities loads the state and estimate transitions of the given
// issues, which is all historicalIssueState/Estimate replay.
func progressActivities(ctx context.Context, reader progressReader, workspace string, ids []string) (map[string][]domain.ActivityEvent, error) {
	result := map[string][]domain.ActivityEvent{}
	for start := 0; start < len(ids); start += 500 {
		clause, args := bindList("resource_id", ids[start:min(start+500, len(ids))])
		rows, err := reader.QueryContext(ctx, `SELECT resource_id,data FROM workspace_content_records WHERE workspace_key=? AND kind='activity' AND `+clause+` ORDER BY created_at,id`, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var resource string
			var raw []byte
			if err := rows.Scan(&resource, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var activity struct {
				Type      string            `json:"type"`
				CreatedAt time.Time         `json:"createdAt"`
				Metadata  map[string]string `json:"metadata"`
			}
			if err := json.Unmarshal(raw, &activity); err != nil {
				rows.Close()
				return nil, err
			}
			if activity.Type != "issue.updated" || activity.Metadata["stateBeforeId"] == "" && activity.Metadata["estimateBefore"] == "" {
				continue
			}
			metadata := map[string]string{}
			for _, key := range []string{"stateBeforeId", "stateBeforeType", "stateBefore", "estimateBefore"} {
				if value, ok := activity.Metadata[key]; ok {
					metadata[key] = value
				}
			}
			result[resource] = append(result[resource], domain.ActivityEvent{Type: activity.Type, CreatedAt: activity.CreatedAt, Metadata: metadata})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}

// projectProgressStart mirrors progressStartDate: the project's start date, or
// the day its earliest issue was created.
func projectProgressStart(ctx context.Context, reader progressReader, workspace string, project domain.Project) (time.Time, error) {
	if project.StartDate != nil {
		if date, err := time.Parse("2006-01-02", *project.StartDate); err == nil {
			return date.UTC(), nil
		}
	}
	var earliest sql.NullString
	if err := reader.QueryRowContext(ctx, `SELECT MIN(created_at) FROM issue_records WHERE workspace_key=? AND project_id=?`, workspace, project.ID).Scan(&earliest); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	if !earliest.Valid || earliest.String == "" {
		return time.Time{}, nil
	}
	created, err := time.Parse(issueRecordTimestamp, earliest.String)
	if err != nil {
		created, err = time.Parse(time.RFC3339Nano, earliest.String)
		if err != nil {
			return time.Time{}, nil
		}
	}
	return utcDay(created), nil
}

func progressStates(data domain.Bootstrap) map[string]domain.WorkflowState {
	states := make(map[string]domain.WorkflowState, len(data.States))
	for _, state := range data.States {
		states[state.ID] = state
	}
	return states
}

// progressGrid is the weekly date grid refreshProjectProgressHistories uses.
func progressGrid(start, now time.Time) []time.Time {
	end := utcDay(now)
	if end.Before(start) {
		end = start
	}
	return weeklyProgressDates(start, end)
}

func progressSeriesMatch(project domain.Project, grid []time.Time, prefixOnly bool) bool {
	for _, series := range [][]domain.ProjectProgressHistoryPoint{project.IssueCountHistory, project.ScopeHistory, project.CompletedScopeHistory, project.InProgressScopeHistory, project.ProgressHistory} {
		if len(series) != len(grid) {
			return false
		}
		for index, point := range series {
			if prefixOnly && index == len(grid)-1 {
				continue
			}
			if !point.Date.Equal(grid[index]) {
				return false
			}
		}
	}
	return len(grid) > 0
}

func totalsFromProgressPoint(point domain.ProjectProgressHistoryPoint) progressTotals {
	return progressTotals{scope: point.ScopeCount, started: point.StartedIssueCount, completed: point.CompletedIssueCount, backlog: int(point.BacklogEstimate), unstarted: int(point.UnstartedEstimate), scopeEstimate: point.ScopeEstimate, startedEstimate: point.StartedEstimate, completedEstimate: point.CompletedEstimate}
}

// replaceLastProgressPoint keeps the stored weekly points and rewrites the
// final (today's) point from totals.
func replaceLastProgressPoint(project *domain.Project, grid []time.Time, totals progressTotals) bool {
	keep := len(grid) - 1
	series := progressSeries{
		issueCount: slices.Clone(project.IssueCountHistory[:keep]),
		scope:      slices.Clone(project.ScopeHistory[:keep]),
		completed:  slices.Clone(project.CompletedScopeHistory[:keep]),
		started:    slices.Clone(project.InProgressScopeHistory[:keep]),
		progress:   slices.Clone(project.ProgressHistory[:keep]),
	}
	// Incremental adjustments add and subtract estimates; keep the sums free
	// of floating point residue.
	for _, estimate := range []*float64{&totals.scopeEstimate, &totals.startedEstimate, &totals.completedEstimate} {
		*estimate = math.Round(*estimate*1e9) / 1e9
	}
	series.append(grid[keep], totals)
	return series.store(project)
}

// rebuildProjectProgress recomputes one project's history from its records.
func rebuildProjectProgress(ctx context.Context, reader progressReader, workspace string, data *domain.Bootstrap, project *domain.Project, states map[string]domain.WorkflowState, now time.Time) (bool, error) {
	issues, err := projectProgressIssues(ctx, reader, workspace, project.ID, states)
	if err != nil {
		return false, err
	}
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.id)
	}
	activities, err := progressActivities(ctx, reader, workspace, ids)
	if err != nil {
		return false, err
	}
	return refreshProjectProgress(project, issues, states, activities, data.TeamSettings, now), nil
}

type progressContribution struct {
	day      time.Time
	state    string
	estimate float64
}

func issueProgressContribution(issue domain.Issue, teamSettings map[string]domain.TeamSettings) progressContribution {
	return progressContribution{day: utcDay(issue.CreatedAt), state: issue.State.Type, estimate: issueEstimate(issue, unestimatedPoints(teamSettings, issue.Team.ID))}
}

// refreshIssueProjectProgress updates the histories of the projects whose
// issues changed between before and after. It reads the database through
// reader (the write transaction, so it sees the new issue rows).
func refreshIssueProjectProgress(ctx context.Context, reader progressReader, workspace string, data *domain.Bootstrap, before, after []domain.Issue, now time.Time) (bool, error) {
	type change struct{ before, after []domain.Issue }
	changes := map[string]*change{}
	entry := func(id string) *change {
		if changes[id] == nil {
			changes[id] = &change{}
		}
		return changes[id]
	}
	for _, issue := range before {
		if issue.Project != nil && issue.Project.ID != "" {
			entry(issue.Project.ID).before = append(entry(issue.Project.ID).before, issue)
		}
	}
	for _, issue := range after {
		if issue.Project != nil && issue.Project.ID != "" {
			entry(issue.Project.ID).after = append(entry(issue.Project.ID).after, issue)
		}
	}
	if len(changes) == 0 {
		return false, nil
	}
	states := progressStates(*data)
	changed := false
	projectIDs := make([]string, 0, len(changes))
	for id := range changes {
		projectIDs = append(projectIDs, id)
	}
	sort.Strings(projectIDs)
	for _, projectID := range projectIDs {
		index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == projectID })
		if index < 0 {
			continue
		}
		project := &data.Projects[index]
		change := changes[projectID]
		contributions := func(issues []domain.Issue) []progressContribution {
			result := make([]progressContribution, 0, len(issues))
			for _, issue := range issues {
				result = append(result, issueProgressContribution(issue, data.TeamSettings))
			}
			sort.Slice(result, func(i, j int) bool {
				a, b := result[i], result[j]
				if !a.day.Equal(b.day) {
					return a.day.Before(b.day)
				}
				if a.state != b.state {
					return a.state < b.state
				}
				return a.estimate < b.estimate
			})
			return result
		}
		if len(project.ProgressHistory) > 0 && slices.Equal(contributions(change.before), contributions(change.after)) {
			continue
		}
		updated, err := updateProjectProgress(ctx, reader, workspace, data, project, change.before, change.after, states, now)
		if err != nil {
			return changed, err
		}
		changed = updated || changed
	}
	return changed, nil
}

func updateProjectProgress(ctx context.Context, reader progressReader, workspace string, data *domain.Bootstrap, project *domain.Project, before, after []domain.Issue, states map[string]domain.WorkflowState, now time.Time) (bool, error) {
	start, err := projectProgressStart(ctx, reader, workspace, *project)
	if err != nil {
		return false, err
	}
	if start.IsZero() {
		return clearProjectProgressHistories(project), nil
	}
	grid := progressGrid(start, now)
	last := grid[len(grid)-1]
	if progressSeriesMatch(*project, grid, false) {
		totals := totalsFromProgressPoint(project.ProgressHistory[len(grid)-1])
		for _, issue := range before {
			if !utcDay(issue.CreatedAt).After(last) {
				totals.add(issue.State.Type, issueEstimate(issue, unestimatedPoints(data.TeamSettings, issue.Team.ID)), -1)
			}
		}
		for _, issue := range after {
			if !utcDay(issue.CreatedAt).After(last) {
				totals.add(issue.State.Type, issueEstimate(issue, unestimatedPoints(data.TeamSettings, issue.Team.ID)), 1)
			}
		}
		return replaceLastProgressPoint(project, grid, totals), nil
	}
	if progressSeriesMatch(*project, grid, true) {
		issues, err := projectProgressIssues(ctx, reader, workspace, project.ID, states)
		if err != nil {
			return false, err
		}
		// No transition activity is dated after today, so the final point is
		// the current state of the project's issues.
		return replaceLastProgressPoint(project, grid, progressTotalsAt(states, nil, issues, last, data.TeamSettings)), nil
	}
	return rebuildProjectProgress(ctx, reader, workspace, data, project, states, now)
}

// persistIssueProjectProgress refreshes the progress of projects touched by
// an issue write on the stored metadata and writes it in tx when a history
// changed. The caller holds s.mu and installs the returned snapshot after the
// transaction commits.
func (s *SQLiteStore) persistIssueProjectProgress(ctx context.Context, tx *sqlTx, workspace string, before, after []domain.Issue) (*domain.Bootstrap, error) {
	stored, ok := s.workspaces[workspace]
	if !ok {
		return nil, nil
	}
	next := stored
	next.Projects = slices.Clone(stored.Projects)
	changed, err := refreshIssueProjectProgress(ctx, tx, workspace, &next, before, after, time.Now().UTC())
	if err != nil || !changed {
		return nil, err
	}
	raw, err := s.encodeWorkspaceMetadata(next)
	if err != nil {
		return nil, err
	}
	if err := writeWorkspaceMetadata(ctx, tx, workspace, next.Workspace.ID, raw); err != nil {
		return nil, err
	}
	return &next, nil
}

// backfillAllProjectProgress builds the history of projects that have none
// yet: record-backed workspaces never ran the snapshot refresh, so projects
// from older builds would otherwise never show a progress graph. It runs after
// the record migrations and reads one project's rows at a time, so memory stays
// bounded by the largest project; later issue writes keep histories current.
func (s *SQLiteStore) backfillAllProjectProgress(ctx context.Context) error {
	now := time.Now().UTC()
	for _, workspace := range s.WorkspaceKeys() {
		s.mu.Lock()
		data, ok := s.workspaces[workspace]
		if !ok || !slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return len(project.ProgressHistory) == 0 }) {
			s.mu.Unlock()
			continue
		}
		data.Projects = slices.Clone(data.Projects)
		states := progressStates(data)
		changed := false
		var err error
		for index := range data.Projects {
			if len(data.Projects[index].ProgressHistory) > 0 {
				continue
			}
			var rebuilt bool
			if rebuilt, err = rebuildProjectProgress(ctx, s.db, workspace, &data, &data.Projects[index], states, now); err != nil {
				break
			}
			changed = rebuilt || changed
		}
		if err == nil && changed {
			if err = s.persistWorkspace(ctx, workspace, data, nil); err == nil {
				s.workspaces[workspace] = data
			}
		}
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func optionalValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
