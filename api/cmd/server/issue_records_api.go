package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The records API is the bounded query contract. The legacy /issues endpoint
// remains available while clients migrate their richer filter expressions.
func isIssueRecordsRequest(r *http.Request) bool {
	if pagedRealtimeRequest(r) {
		return true
	}
	return r.URL.Path == "/api/issue-records" || strings.HasPrefix(r.URL.Path, "/api/issue-records/")
}

func (s *server) issueRecordsQuery(r *http.Request) (domain.Bootstrap, store.IssueRecordQuery, error) {
	key := workspaceKey(r)
	query := store.IssueRecordQuery{Workspace: key, Archived: r.URL.Query().Get("archived"), Sort: r.URL.Query().Get("sort"), Direction: r.URL.Query().Get("direction"), Cursor: r.URL.Query().Get("cursor"), Text: r.URL.Query().Get("q"), IncludeTotal: r.URL.Query().Get("includeTotal") == "true", GroupBy: r.URL.Query().Get("groupBy")}
	query.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	query.Summary = r.URL.Query().Get("projection") == "list"
	data, access, err := s.requestIssueQueryAccess(r)
	if err != nil {
		return data, query, err
	}
	query.Workspace = data.Workspace.URLKey
	if s.authDisabled && r.Method != http.MethodGet && r.Method != http.MethodHead && r.URL.Path != "/api/issue-records/visibility" {
		data, _ = s.store.WorkspaceMetadata(query.Workspace)
		data.ViewerRole = "admin"
	}
	if !s.authDisabled {
		query.Access = &access
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
			query.AllowedTeamIDs = slices.Clone(key.TeamIDs)
			if query.AllowedTeamIDs == nil {
				query.AllowedTeamIDs = []string{}
			}
		}
	}
	query.TeamIDs = splitQueryValues(r.URL.Query().Get("teamId"))
	if len(query.TeamIDs) > 0 && r.URL.Query().Get("includeSubTeams") == "true" {
		query.TeamIDs = domain.TeamSubtreeIDs(&data, query.TeamIDs)
	}
	query.ProjectIDs = splitQueryValues(r.URL.Query().Get("projectId"))
	query.StateIDs = splitQueryValues(r.URL.Query().Get("stateId"))
	if value, ok := r.URL.Query()["groupValue"]; ok {
		v := ""
		if len(value) > 0 {
			v = value[0]
		}
		query.GroupValue = &v
	}
	root, err := decodeIssueQuery(r.URL.Query().Get("filter"))
	if err != nil {
		return data, query, store.ErrIssueQuery
	}
	var convert func(issueQueryNode) store.IssueFilter
	convert = func(node issueQueryNode) store.IssueFilter {
		result := store.IssueFilter{Field: node.Field, Operator: node.Operator, Values: node.values()}
		for _, child := range node.And {
			result.And = append(result.And, convert(child))
		}
		for _, child := range node.Or {
			result.Or = append(result.Or, convert(child))
		}
		return result
	}
	query.Filter = convert(root)
	return data, query, nil
}

func (s *server) listIssueRecords(w http.ResponseWriter, r *http.Request) {
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	page, err := s.store.QueryIssueRecords(r.Context(), query)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	page.Items, err = s.projectIssueRecordReferences(r, metadata, query, page.Items)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) issueRecordsBootstrap(w http.ResponseWriter, r *http.Request) {
	key := workspaceKey(r)
	var data domain.Bootstrap
	if s.authDisabled {
		var ok bool
		data, ok = s.store.WorkspaceMetadata(key)
		if !ok {
			writeError(w, 404, "workspace not found")
			return
		}
		data.ViewerRole = "admin"
	} else {
		var err error
		data, err = s.store.PagedWorkspaceMetadata(r.Context(), key, authUser(r).ID)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
		filterBootstrapForAPIKey(&data, r)
	}
	data.IssueCollectionPaged = true
	if err := s.filterPreferenceIssueTeams(r, &data); err != nil {
		issueRecordsError(w, err)
		return
	}
	sanitizeBootstrap(&data)
	writeJSON(w, 200, data)
}

func (s *server) createIssueRecord(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueCreateInput
	if !peekRequestJSON(r, &input) {
		writeError(w, 400, "invalid issue input")
		return
	}
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	if !s.authDisabled {
		metadata, err = s.store.PagedWorkspaceMetadata(r.Context(), query.Workspace, authUser(r).ID)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
	}
	filterBootstrapForAPIKey(&metadata, r)
	teamAllowed := func(id string) bool {
		return id == "" || slices.ContainsFunc(metadata.Teams, func(team domain.Team) bool { return team.ID == id })
	}
	if !teamAllowed(input.TeamID) {
		writeError(w, 403, "Team is outside your workspace access")
		return
	}
	if input.AssigneeID != nil && *input.AssigneeID != "" && !slices.ContainsFunc(metadata.Users, func(user domain.User) bool { return user.ID == *input.AssigneeID }) {
		writeError(w, 403, "Assignee is outside this workspace")
		return
	}
	if input.ProjectID != nil && *input.ProjectID != "" && !slices.ContainsFunc(metadata.Projects, func(project domain.Project) bool { return project.ID == *input.ProjectID }) {
		writeError(w, 403, "Project is outside your teams")
		return
	}
	if input.CycleID != nil && *input.CycleID != "" && !slices.ContainsFunc(metadata.Cycles, func(cycle domain.Cycle) bool { return cycle.ID == *input.CycleID }) {
		writeError(w, 403, "Cycle is outside your teams")
		return
	}
	if input.TemplateID != "" {
		index := slices.IndexFunc(metadata.IssueTemplates, func(template domain.IssueTemplate) bool { return template.ID == input.TemplateID })
		if index < 0 {
			writeError(w, 403, "Template is outside your teams")
			return
		}
		for _, child := range metadata.IssueTemplates[index].SubIssues {
			if !teamAllowed(child.TeamID) {
				writeError(w, 403, "Template includes an inaccessible team")
				return
			}
		}
	}
	if input.ParentID != nil && *input.ParentID != "" {
		query.Filter = store.IssueFilter{Field: "id", Values: []string{*input.ParentID}}
		query.Archived = "all"
		query.Limit = 1
		page, err := s.store.QueryIssueRecords(r.Context(), query)
		if err != nil || len(page.Items) == 0 {
			writeError(w, 403, "Parent issue is outside your teams")
			return
		}
	}
	s.createIssue(w, r.WithContext(store.WithIssueRecordMutations(r.Context())))
}

func (s *server) listIssueRecordGroups(w http.ResponseWriter, r *http.Request) {
	_, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	groups, err := s.store.QueryIssueGroups(r.Context(), query)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (s *server) issueRecordProjectSummary(w http.ResponseWriter, r *http.Request) {
	_, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	if len(query.ProjectIDs) != 1 {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	metadata, err := s.store.IssueReferenceMetadata(r.Context(), query, []domain.Issue{{Project: &domain.ProjectSummary{ID: query.ProjectIDs[0]}}})
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	if len(metadata.Projects) == 0 {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	summary, err := s.store.QueryIssueRecordSummary(r.Context(), query)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *server) getIssueRecord(w http.ResponseWriter, r *http.Request) {
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	id := r.PathValue("id")
	issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, id)
	if err != nil {
		issueDetailReadError(w, err)
		return
	}
	items, err := s.projectIssueRecordReferences(r, metadata, query, []domain.Issue{issue})
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items[0])
}

func (s *server) updateIssueRecord(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if legacy, _ := r.Context().Value(legacyIssueWriteContext{}).(bool); legacy {
		// Legacy clients merge scalar patches by field. Keep that contract while
		// still enforcing the document snapshot version below.
		input.ExpectedVersion = nil
	}
	if len(input.DocumentUpdateIDs) > 10_000 || slices.ContainsFunc(input.DocumentUpdateIDs, func(id string) bool { return len(id) > 191 }) {
		writeError(w, http.StatusBadRequest, "Too many document updates")
		return
	}
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	id := r.PathValue("id")
	query.Filter = store.IssueFilter{Field: "id", Values: []string{id}}
	query.Limit = 1
	query.Archived = "all"
	page, err := s.store.QueryIssueRecords(r.Context(), query)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	if len(page.Items) == 0 {
		writeError(w, 404, "issue not found")
		return
	}
	if !s.authDisabled {
		metadata, err = s.store.PagedWorkspaceMetadata(r.Context(), query.Workspace, authUser(r).ID)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
	}
	filterBootstrapForAPIKey(&metadata, r)
	if input.ProjectID != nil && *input.ProjectID != "" && !slices.ContainsFunc(metadata.Projects, func(project domain.Project) bool { return project.ID == *input.ProjectID }) {
		writeError(w, 403, "Project is outside your teams")
		return
	}
	if input.CycleID != nil && *input.CycleID != "" && !slices.ContainsFunc(metadata.Cycles, func(cycle domain.Cycle) bool { return cycle.ID == *input.CycleID }) {
		writeError(w, 403, "Cycle is outside your teams")
		return
	}
	if input.AssigneeID != nil && *input.AssigneeID != "" && !slices.ContainsFunc(metadata.Users, func(user domain.User) bool { return user.ID == *input.AssigneeID }) {
		writeError(w, 403, "Assignee is outside this workspace")
		return
	}
	extra := []string{}
	if input.ParentID != nil && *input.ParentID != "" {
		parentQuery := query
		parentQuery.Filter = store.IssueFilter{Field: "id", Values: []string{*input.ParentID}}
		parent, err := s.store.QueryIssueRecords(r.Context(), parentQuery)
		if err != nil || len(parent.Items) == 0 {
			writeError(w, 403, "Parent issue is outside your teams")
			return
		}
		extra = append(extra, *input.ParentID)
	}
	if input.StateID != nil {
		for _, direction := range []string{"asc", "desc"} {
			boundary, err := s.store.QueryIssueRecords(r.Context(), store.IssueRecordQuery{Workspace: query.Workspace, TeamIDs: []string{page.Items[0].Team.ID}, StateIDs: []string{*input.StateID}, Sort: "sortOrder", Direction: direction, Archived: "all", Limit: 1})
			if err != nil {
				issueRecordsError(w, err)
				return
			}
			if len(boundary.Items) > 0 {
				extra = append(extra, boundary.Items[0].ID)
			}
		}
	}
	mutationScope := store.IssueMutationScope{RelatedIDs: extra, IncludeFamily: input.StateID != nil || input.ParentID != nil, Payload: input}
	if input.ParentID != nil {
		mutationScope.NewParentID = *input.ParentID
	}
	previousDocumentID := ""
	updated, err := s.store.UpdateIssueRecord(r.Context(), query.Workspace, id, input.ExpectedVersion, mutationScope, func(data *domain.Bootstrap, issue *domain.Issue) error {
		accessData := metadata
		accessData.Issues = data.Issues
		if issuePermissionRank(issueRole(s, accessData, *issue)) < issuePermissionRank("editor") {
			return store.ErrAuthForbidden
		}
		if input.ExpectedDocumentVersion != nil {
			var version int64
			if issue.DocumentContent != nil {
				version = issue.DocumentContent.Version
			}
			if version != *input.ExpectedDocumentVersion {
				return store.ErrIssueVersion
			}
		}
		if issue.DocumentContent != nil {
			previousDocumentID = issue.DocumentContent.ID
		} else {
			previousDocumentID = "document_content_" + issue.ID
		}
		changes, err := applyUpdate(data, issue, input)
		if err != nil {
			return err
		}
		issue.UpdatedAt = time.Now().UTC()
		applySLARules(data, issue, issue.UpdatedAt)
		activity := appendActivity(data, id, "issue.updated", data.Viewer, changes)
		appendIssueNotifications(data, *issue, activity, nil)
		return nil
	})
	if updated.ID != "" {
		projected, projectionErr := s.projectIssueRecordReferences(r, metadata, query, []domain.Issue{updated})
		if projectionErr != nil {
			issueRecordsError(w, projectionErr)
			return
		}
		updated = projected[0]
	}
	if errors.Is(err, store.ErrIssueVersion) {
		writeVersionConflict(w, updated)
		return
	}
	if errors.Is(err, store.ErrAuthForbidden) {
		issueRecordsError(w, err)
		return
	}
	if err == nil && updated.DocumentContent != nil && previousDocumentID != "" && previousDocumentID != updated.DocumentContent.ID {
		if cleanupErr := s.store.DeleteDocumentCollaborationDocument(r.Context(), query.Workspace, previousDocumentID); cleanupErr != nil {
			log.Printf("discard replaced collaboration document=%s: %v", previousDocumentID, cleanupErr)
		}
	}
	if err == nil && updated.DocumentContent != nil && len(input.DocumentUpdateIDs) > 0 {
		if deleteErr := s.store.DeleteDocumentCollaborationUpdates(r.Context(), query.Workspace, updated.DocumentContent.ID, input.DocumentUpdateIDs); deleteErr != nil {
			log.Printf("compact collaboration updates document=%s: %v", updated.DocumentContent.ID, deleteErr)
		}
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) getIssueRecordContext(w http.ResponseWriter, r *http.Request) {
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, r.PathValue("id"))
	if err != nil {
		issueDetailReadError(w, err)
		return
	}
	items, err := s.projectIssueRecordReferences(r, metadata, query, []domain.Issue{issue})
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	issue = items[0]
	history, err := s.store.IssueHistoryPage(r.Context(), query.Workspace, issue.ID, r.URL.Query().Get("commentsCursor"), r.URL.Query().Get("activitiesCursor"))
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	ids := []string{}
	if issue.ParentID != nil {
		ids = append(ids, *issue.ParentID)
	}
	ids = append(ids, issue.SubIssueIDs...)
	if len(ids) > 1000 {
		ids = ids[:1000]
	}
	related := []domain.Issue{}
	if len(ids) > 0 {
		query.Filter = store.IssueFilter{Field: "id", Values: ids}
		query.Limit = 500
		result, err := s.store.QueryIssueRecords(r.Context(), query)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
		related, err = s.projectIssueRecordReferences(r, metadata, query, result.Items)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"issue": issue, "relatedIssues": related, "comments": history.Comments, "activities": history.Activities, "commentsCursor": history.CommentsCursor, "activitiesCursor": history.ActivitiesCursor})
}

func (s *server) projectIssueRecordReferences(r *http.Request, metadata domain.Bootstrap, query store.IssueRecordQuery, issues []domain.Issue) ([]domain.Issue, error) {
	if (query.Access == nil || query.Access.Admin) && query.AllowedTeamIDs == nil {
		return issues, nil
	}
	var err error
	metadata, err = s.store.IssueReferenceMetadata(r.Context(), query, issues)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, issue := range issues {
		if issue.ParentID != nil {
			ids = append(ids, *issue.ParentID)
		}
		ids = append(ids, issue.SubIssueIDs...)
		for _, relation := range issue.Relations {
			ids = append(ids, relation.RelatedIssueID)
		}
	}
	visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, ids)
	if err != nil {
		return nil, err
	}
	projects, labels := map[string]bool{}, map[string]bool{}
	for _, project := range metadata.Projects {
		projects[project.ID] = true
	}
	for _, label := range metadata.Labels {
		labels[label.ID] = true
	}
	for index := range issues {
		issue := &issues[index]
		if issue.ParentID != nil && !visible[*issue.ParentID] {
			issue.ParentID = nil
		}
		issue.SubIssueIDs = slices.DeleteFunc(issue.SubIssueIDs, func(id string) bool { return !visible[id] })
		issue.Relations = slices.DeleteFunc(issue.Relations, func(relation domain.IssueRelation) bool { return !visible[relation.RelatedIssueID] })
		issue.Labels = slices.DeleteFunc(issue.Labels, func(label domain.IssueLabel) bool { return !labels[label.ID] })
		if issue.Project != nil && !projects[issue.Project.ID] {
			issue.Project = nil
			issue.ProjectMilestoneID = nil
		}
	}
	return issues, nil
}

func issueRecordsError(w http.ResponseWriter, err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, store.ErrIssueQuery) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, store.ErrAuthForbidden) {
		writeError(w, http.StatusForbidden, "You don't have access to this workspace")
		return
	}
	writeError(w, http.StatusInternalServerError, "Could not query issues")
}
