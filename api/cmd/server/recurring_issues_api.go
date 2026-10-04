package main

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// recurringIssueView is a recurring issue in the team's Recurring issues
// settings list.
type recurringIssueView struct {
	domain.Issue
	SubIssueCount int `json:"subIssueCount"`
}

// listTeamRecurringIssues serves GET /api/teams/{id}/recurring-issues: the
// team's non-archived issues that currently own a schedule, by next due date.
// Recurring issues are found through the sparse nextOccurrenceAt attribute
// index and then read by primary key under the viewer's issue visibility, so
// the cost never depends on the workspace's issue count (paged mode safe).
func (s *server) listTeamRecurringIssues(w http.ResponseWriter, r *http.Request) {
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	teamID := r.PathValue("id")
	if !recurringTeamVisible(metadata, query, teamID) {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}
	ids, err := s.store.RecurringIssueIDs(r.Context(), query.Workspace, time.Time{}, 0)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	query.Filter, query.Text, query.Cursor, query.GroupBy, query.GroupValue = store.IssueFilter{}, "", "", "", nil
	query.ProjectIDs, query.StateIDs, query.ReleaseIDs = nil, nil, nil
	query.TeamIDs, query.Archived, query.Sort, query.Direction, query.Summary, query.IncludeTotal, query.Limit = []string{teamID}, "", "", "", false, false, 500
	query.RestrictToIssueIDs = true
	issues := []domain.Issue{}
	for start := 0; start < len(ids); start += 500 {
		query.IssueIDs = ids[start:min(start+500, len(ids))]
		page, err := s.store.QueryIssueRecords(r.Context(), query)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
		for _, issue := range page.Items {
			if issue.Recurrence != "" {
				issues = append(issues, issue)
			}
		}
	}
	issues, err = s.projectIssueRecordReferences(r, metadata, query, issues)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	slices.SortStableFunc(issues, func(a, b domain.Issue) int {
		if order := cmp.Compare(optionalID(a.DueDate), optionalID(b.DueDate)); order != 0 {
			return order
		}
		return cmp.Compare(a.Number, b.Number)
	})
	views := make([]recurringIssueView, len(issues))
	for i, issue := range issues {
		views[i] = recurringIssueView{Issue: issue, SubIssueCount: len(issue.SubIssueIDs)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": views})
}

func recurringTeamVisible(metadata domain.Bootstrap, query store.IssueRecordQuery, teamID string) bool {
	if teamID == "" {
		return false
	}
	if index := slices.IndexFunc(metadata.Teams, func(team domain.Team) bool { return team.ID == teamID }); index < 0 || metadata.Teams[index].ArchivedAt != nil {
		return false
	}
	if query.Access != nil && !query.Access.Admin && !slices.Contains(query.Access.VisibleTeamIDs, teamID) {
		return false
	}
	return query.AllowedTeamIDs == nil || slices.Contains(query.AllowedTeamIDs, teamID)
}

type recurringSubIssueInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    *int     `json:"priority,omitempty"`
	AssigneeID  *string  `json:"assigneeId,omitempty"`
	LabelIDs    []string `json:"labelIds,omitempty"`
}

type recurringIssueCreateInput struct {
	Title            string                   `json:"title"`
	Description      string                   `json:"description"`
	DescriptionState *string                  `json:"descriptionState,omitempty"`
	StateID          *string                  `json:"stateId,omitempty"`
	Priority         *int                     `json:"priority,omitempty"`
	AssigneeID       *string                  `json:"assigneeId,omitempty"`
	ProjectID        *string                  `json:"projectId,omitempty"`
	LabelIDs         []string                 `json:"labelIds,omitempty"`
	Icon             *string                  `json:"icon,omitempty"`
	DueDate          string                   `json:"dueDate"`
	Recurrence       string                   `json:"recurrence"`
	SubIssues        []recurringSubIssueInput `json:"subIssues,omitempty"`
}

const maxRecurringSubIssues = 50

// createTeamRecurringIssue serves POST /api/teams/{id}/recurring-issues
// (Linear's "New recurring issue"): it creates the first instance, due on
// dueDate, and its sub-issues in one scoped issue-creation mutation. The auth
// middleware applies the team's templatePermission ("Who can manage team
// templates and recurring issues").
func (s *server) createTeamRecurringIssue(w http.ResponseWriter, r *http.Request) {
	var input recurringIssueCreateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Title, input.DueDate, input.Recurrence = strings.TrimSpace(input.Title), strings.TrimSpace(input.DueDate), strings.TrimSpace(input.Recurrence)
	switch {
	case input.Title == "":
		writeError(w, http.StatusBadRequest, "title is required")
		return
	case input.Recurrence == "":
		writeError(w, http.StatusBadRequest, "recurrence is required")
		return
	case len(input.SubIssues) > maxRecurringSubIssues:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d sub-issues", maxRecurringSubIssues))
		return
	}
	if _, err := time.Parse("2006-01-02", input.DueDate); err != nil {
		writeError(w, http.StatusBadRequest, "dueDate must be a YYYY-MM-DD date")
		return
	}
	if _, err := parseRecurrence(input.Recurrence); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, child := range input.SubIssues {
		if strings.TrimSpace(child.Title) == "" {
			writeError(w, http.StatusBadRequest, "sub-issue title is required")
			return
		}
	}
	teamID := r.PathValue("id")
	if !s.authDisabled {
		metadata, err := s.store.PagedWorkspaceMetadata(r.Context(), workspaceKey(r), authUser(r).ID)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
		filterBootstrapForAPIKey(&metadata, r)
		if !slices.ContainsFunc(metadata.Teams, func(team domain.Team) bool { return team.ID == teamID }) {
			writeError(w, http.StatusNotFound, "team not found")
			return
		}
		if input.ProjectID != nil && *input.ProjectID != "" && !slices.ContainsFunc(metadata.Projects, func(project domain.Project) bool { return project.ID == *input.ProjectID }) {
			writeError(w, http.StatusForbidden, "Project is outside your teams")
			return
		}
		assignees := []*string{input.AssigneeID}
		for _, child := range input.SubIssues {
			assignees = append(assignees, child.AssigneeID)
		}
		for _, assignee := range assignees {
			if assignee != nil && *assignee != "" && !slices.ContainsFunc(metadata.Users, func(user domain.User) bool { return user.ID == *assignee }) {
				writeError(w, http.StatusForbidden, "Assignee is outside this workspace")
				return
			}
		}
	}
	var created domain.Issue
	var children []domain.Issue
	ctx := issueCreationScope(r.Context(), nil)
	err := s.store.MutateWorkspaceWithAggregate(ctx, workspaceKey(r), "issue.created", input, func(data *domain.Bootstrap) (string, error) {
		created, children = domain.Issue{}, nil
		teamIndex := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID })
		if teamIndex < 0 || data.Teams[teamIndex].ArchivedAt != nil {
			return "", errNotFound
		}
		team := data.Teams[teamIndex]
		if team.RetiredAt != nil {
			return "", fmt.Errorf("%w: team is retired", errInvalid)
		}
		now := time.Now().UTC()
		number := max(data.NextIssueNumber, nextIssueNumber(data.Issues))
		issue, err := newTeamIssue(data, team, number, data.Viewer, now)
		if err != nil {
			return "", err
		}
		settings := teamSettings(data, team.ID)
		issue.Title, issue.Description = input.Title, strings.TrimSpace(input.Description)
		issue.Priority, issue.PriorityLabel = settings.DefaultPriority, priorityLabel(settings.DefaultPriority)
		update := domain.IssueUpdateInput{DescriptionState: input.DescriptionState, StateID: input.StateID, Priority: input.Priority, AssigneeID: input.AssigneeID, ProjectID: input.ProjectID, Icon: input.Icon, DueDate: &input.DueDate, Recurrence: &input.Recurrence}
		if len(input.LabelIDs) > 0 {
			update.LabelIDs = &input.LabelIDs
		}
		if _, err := applyUpdate(data, &issue, update); err != nil {
			return "", err
		}
		applyCycleAutomation(data, &issue)
		applySLARules(data, &issue, now)
		applyTriageRouting(data, &issue, now)
		issue.RecurrenceSeriesID, issue.RecurrenceOccurrence = issue.ID, input.DueDate
		for index, item := range input.SubIssues {
			child, err := newTeamIssue(data, team, number+index+1, data.Viewer, now)
			if err != nil {
				return "", err
			}
			child.Title, child.Description, child.ParentID = strings.TrimSpace(item.Title), strings.TrimSpace(item.Description), &issue.ID
			childUpdate := domain.IssueUpdateInput{Priority: item.Priority, AssigneeID: item.AssigneeID}
			if len(item.LabelIDs) > 0 {
				childUpdate.LabelIDs = &item.LabelIDs
			}
			if _, err := applyUpdate(data, &child, childUpdate); err != nil {
				return "", err
			}
			skipRecurringTriage(data, &child, now)
			applySLARules(data, &child, now)
			issue.SubIssueIDs = append(issue.SubIssueIDs, child.ID)
			children = append(children, child)
		}
		created = issue
		data.Issues = append(append([]domain.Issue{created}, children...), data.Issues...)
		appendActivity(data, created.ID, "issue.created", data.Viewer, map[string]string{"stateId": created.State.ID, "state": created.State.Name, "recurrence": created.Recurrence})
		for _, child := range children {
			appendActivity(data, child.ID, "issue.created", data.Viewer, map[string]string{"stateId": child.State.ID, "state": child.State.Name})
		}
		return created.ID, nil
	})
	if err != nil {
		if errors.Is(err, errNotFound) {
			writeError(w, http.StatusNotFound, "team not found")
			return
		}
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	for _, child := range children {
		s.publishIssueEntity(workspaceKey(r), "issue.created", child, map[string]any{"parentId": created.ID})
	}
	if children == nil {
		children = []domain.Issue{}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"issue": created, "subIssues": children})
}
