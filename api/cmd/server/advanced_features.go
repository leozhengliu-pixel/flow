package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type documentInput struct {
	TemplateID    *string        `json:"templateId,omitempty"`
	Title         *string        `json:"title,omitempty"`
	Icon          *string        `json:"icon,omitempty"`
	Color         *string        `json:"color,omitempty"`
	Content       *string        `json:"content,omitempty"`
	ContentState  *string        `json:"contentState,omitempty"`
	ContentData   map[string]any `json:"contentData,omitempty"`
	ProjectIDs    *[]string      `json:"projectIds,omitempty"`
	TeamIDs       *[]string      `json:"teamIds,omitempty"`
	IssueID       *string        `json:"issueId,omitempty"`
	SubscriberIDs *[]string      `json:"subscriberIds,omitempty"`
	Favorite      *bool          `json:"favorite,omitempty"`
	Archived      *bool          `json:"archived,omitempty"`
}

// documentVisibleToViewer mirrors Flow's document access rule: an unscoped
// document is workspace-visible, while a team document is visible only to a
// member of one of its teams (admins retain workspace access).
func documentVisibleToViewer(s *server, data domain.Bootstrap, document domain.Document) bool {
	return documentRole(s, data, document) != "none"
}

func documentRole(s *server, data domain.Bootstrap, document domain.Document) string {
	if s.authDisabled || workspaceAdminRole(data.ViewerRole) || document.Creator.ID == data.Viewer.ID {
		return "owner"
	}
	best := "none"
	hasExplicit := false
	for _, permission := range document.Permissions {
		if !(permission.SubjectType == "user" && permission.SubjectID == document.Creator.ID && strings.EqualFold(permission.Role, "owner")) {
			hasExplicit = true
		}
		matched := permission.SubjectType == "user" && permission.SubjectID == data.Viewer.ID
		if !matched && permission.SubjectType == "workspace" {
			matched = permission.SubjectID == "" || permission.SubjectID == data.Workspace.ID || permission.SubjectID == data.Workspace.URLKey
		}
		if !matched && permission.SubjectType == "team" {
			matched = slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool {
				return member.UserID == data.Viewer.ID && member.TeamID == permission.SubjectID
			})
		}
		if matched && documentRoleRank(permission.Role) > documentRoleRank(best) {
			best = permission.Role
		}
	}
	if best != "none" {
		return best
	}
	if hasExplicit {
		return "none"
	}
	if len(document.TeamIDs) == 0 {
		return "viewer"
	}
	if slices.ContainsFunc(document.TeamIDs, func(teamID string) bool {
		return slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool { return member.UserID == data.Viewer.ID && member.TeamID == teamID })
	}) {
		return "viewer"
	}
	return "none"
}

func documentRoleRank(role string) int {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner":
		return 4
	case "editor":
		return 3
	case "commenter":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

func canEditDocument(role string) bool    { return documentRoleRank(role) >= 3 }
func canCommentDocument(role string) bool { return documentRoleRank(role) >= 2 }

func (s *server) listDocuments(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	query := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("q")))
	teamID := strings.TrimSpace(r.URL.Query().Get("teamId"))
	archived := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("archived")))
	result := make([]domain.Document, 0, len(data.Documents))
	for _, document := range data.Documents {
		if !documentVisibleToViewer(s, data, document) {
			continue
		}
		if teamID != "" && !slices.Contains(document.TeamIDs, teamID) {
			continue
		}
		if archived != "all" && (archived == "true") != (document.ArchivedAt != nil) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(document.Title+" "+document.Content), query) {
			continue
		}
		result = append(result, document)
	}
	slices.SortStableFunc(result, func(a, b domain.Document) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	writeJSON(w, http.StatusOK, result)
}

type customerRequestInput struct {
	CustomerID string  `json:"customerId,omitempty"`
	Body       *string `json:"body,omitempty"`
	Source     *string `json:"source,omitempty"`
	SourceURL  *string `json:"sourceUrl,omitempty"`
	IssueID    *string `json:"issueId,omitempty"`
	ProjectID  *string `json:"projectId,omitempty"`
}

type releaseInput struct {
	Name          *string                   `json:"name,omitempty"`
	Version       *string                   `json:"version,omitempty"`
	Description   *string                   `json:"description,omitempty"`
	Status        *string                   `json:"status,omitempty"`
	PipelineID    *string                   `json:"pipelineId,omitempty"`
	Stage         *string                   `json:"stage,omitempty"`
	CommitSHA     *string                   `json:"commitSha,omitempty"`
	ReleaseNotes  *string                   `json:"releaseNotes,omitempty"`
	TargetDate    *string                   `json:"targetDate,omitempty"`
	ProjectIDs    *[]string                 `json:"projectIds,omitempty"`
	IssueIDs      *[]string                 `json:"issueIds,omitempty"`
	SubscriberIDs *[]string                 `json:"subscriberIds,omitempty"`
	Resources     *[]domain.ReleaseResource `json:"resources,omitempty"`
	StageFrozen   *bool                     `json:"stageFrozen,omitempty"`
	Archived      *bool                     `json:"archived,omitempty"`
}

func uniqueReleasePipelineSlug(data *domain.Bootstrap, name string) string {
	base := slug(name)
	if base == "" {
		base = "pipeline"
	}
	candidate := base
	for suffix := 2; slices.ContainsFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.SlugID == candidate }); suffix++ {
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
	return candidate
}

func uniqueReleaseSlug(data *domain.Bootstrap, name string, now time.Time) string {
	base := slug(name)
	if base == "" {
		base = "release"
	}
	candidate := fmt.Sprintf("%s-%x", base, now.UnixNano()&0xffffffffffff)
	for slices.ContainsFunc(data.Releases, func(item domain.Release) bool { return item.SlugID == candidate }) {
		now = now.Add(time.Nanosecond)
		candidate = fmt.Sprintf("%s-%x", base, now.UnixNano()&0xffffffffffff)
	}
	return candidate
}

type askInput struct {
	Title      *string `json:"title,omitempty"`
	Body       *string `json:"body,omitempty"`
	Source     *string `json:"source,omitempty"`
	TeamID     *string `json:"teamId,omitempty"`
	TemplateID *string `json:"templateId,omitempty"`
	IssueID    *string `json:"issueId,omitempty"`
}

type projectTemplateInput struct {
	Name                *string                     `json:"name,omitempty"`
	ProjectName         *string                     `json:"projectName,omitempty"`
	TemplateDescription *string                     `json:"templateDescription,omitempty"`
	Description         *string                     `json:"description,omitempty"`
	Summary             *string                     `json:"summary,omitempty"`
	Icon                *string                     `json:"icon,omitempty"`
	Color               *string                     `json:"color,omitempty"`
	StatusID            *string                     `json:"statusId,omitempty"`
	Priority            *int                        `json:"priority,omitempty"`
	TeamIDs             *[]string                   `json:"teamIds,omitempty"`
	LabelIDs            *[]string                   `json:"labelIds,omitempty"`
	LeadID              *string                     `json:"leadId,omitempty"`
	MemberIDs           *[]string                   `json:"memberIds,omitempty"`
	DependencyIDs       *[]string                   `json:"dependencyIds,omitempty"`
	InitiativeIDs       *[]string                   `json:"initiativeIds,omitempty"`
	IssueIDs            *[]string                   `json:"issueIds,omitempty"`
	Milestones          *[]domain.TemplateMilestone `json:"milestones,omitempty"`
	Visibility          *string                     `json:"visibility,omitempty"`
	VisibilityTeamID    *string                     `json:"visibilityTeamId,omitempty"`
}

type slaRuleInput struct {
	Name          *string        `json:"name,omitempty"`
	TeamIDs       *[]string      `json:"teamIds,omitempty"`
	Filters       map[string]any `json:"filters,omitempty"`
	TargetMinutes *int           `json:"targetMinutes,omitempty"`
	PauseStatuses *[]string      `json:"pauseStatuses,omitempty"`
	BusinessHours *bool          `json:"businessHours,omitempty"`
	Enabled       *bool          `json:"enabled,omitempty"`
}

type draftInput struct {
	Type        *string        `json:"type,omitempty"`
	ResourceID  *string        `json:"resourceId,omitempty"`
	Title       *string        `json:"title,omitempty"`
	Body        *string        `json:"body,omitempty"`
	ContentData map[string]any `json:"contentData,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type deletedIssuePayload struct {
	Issue      domain.Issue           `json:"issue"`
	Comments   []domain.Comment       `json:"comments"`
	Activities []domain.ActivityEvent `json:"activities"`
}
type deletedProjectPayload struct {
	Project domain.Project         `json:"project"`
	Updates []domain.ProjectUpdate `json:"updates"`
}
type deletedInitiativePayload struct {
	Initiative domain.Initiative         `json:"initiative"`
	Updates    []domain.InitiativeUpdate `json:"updates"`
}

func appendAudit(data *domain.Bootstrap, action, resourceType, resourceID string, metadata map[string]any) {
	now := time.Now().UTC()
	data.AuditLog = append([]domain.AuditLogEntry{{ID: fmt.Sprintf("audit_%d", now.UnixNano()), Actor: data.Viewer, Action: action, ResourceType: resourceType, ResourceID: resourceID, Metadata: metadata, CreatedAt: now}}, data.AuditLog...)
	if len(data.AuditLog) > 1000 {
		data.AuditLog = data.AuditLog[:1000]
	}
}

func appendTrash(data *domain.Bootstrap, resourceType, resourceID, title string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	data.Trash = append([]domain.TrashEntry{{ID: fmt.Sprintf("trash_%d", now.UnixNano()), ResourceType: resourceType, ResourceID: resourceID, Title: title, Payload: payload, TeamIDs: trashTeamIDs(data, value), DeletedBy: data.Viewer, DeletedAt: now, ExpiresAt: now.AddDate(0, 0, 30)}}, data.Trash...)
	appendAudit(data, "deleted", resourceType, resourceID, map[string]any{"title": title})
	return nil
}

func trashTeamIDs(data *domain.Bootstrap, value any) []string {
	var ids []string
	switch item := value.(type) {
	case deletedIssuePayload:
		ids = []string{item.Issue.Team.ID}
	case deletedProjectPayload:
		ids = slices.Clone(item.Project.TeamIDs)
	case deletedInitiativePayload:
		ids = append([]string{item.Initiative.LeadTeamID}, item.Initiative.ContributingTeamIDs...)
		for _, projectID := range item.Initiative.ProjectIDs {
			if project, err := fullProjectByID(data, projectID); err == nil {
				ids = append(ids, project.TeamIDs...)
			}
		}
	case domain.Document:
		ids = slices.Clone(item.TeamIDs)
	case domain.Release:
		for _, projectID := range item.ProjectIDs {
			if project, err := fullProjectByID(data, projectID); err == nil {
				ids = append(ids, project.TeamIDs...)
			}
		}
	case domain.ReleasePipeline:
		ids = slices.Clone(item.TeamIDs)
	}
	return normalizedStrings(ids)
}

func validateResourceIDs(data *domain.Bootstrap, resourceType string, ids []string) bool {
	for _, id := range ids {
		valid := false
		switch resourceType {
		case "project":
			valid = slices.ContainsFunc(data.Projects, func(item domain.Project) bool { return item.ID == id })
		case "issue":
			valid = slices.ContainsFunc(data.Issues, func(item domain.Issue) bool { return item.ID == id })
		case "team":
			valid = slices.ContainsFunc(data.Teams, func(item domain.Team) bool { return item.ID == id })
		case "label":
			valid = labelExistsForResource(data, id, "issue")
		case "project-label":
			valid = labelExistsForResource(data, id, "project")
		case "user":
			valid = userByID(data, id) != nil
		case "document":
			_, err := documentByID(data, id)
			valid = err == nil
		case "initiative":
			_, err := initiativeByID(data, id)
			valid = err == nil
		}
		if !valid {
			return false
		}
	}
	return true
}

func documentByID(data *domain.Bootstrap, id string) (*domain.Document, error) {
	index := slices.IndexFunc(data.Documents, func(item domain.Document) bool { return item.ID == id || item.SlugID == id })
	if index < 0 {
		return nil, errNotFound
	}
	return &data.Documents[index], nil
}

func saveDocumentRevision(document *domain.Document, author domain.User) {
	if len(document.Revisions) > 0 {
		last := document.Revisions[0]
		if last.Title == document.Title && last.Content == document.Content && last.ContentState == document.ContentState {
			return
		}
	}
	now := time.Now().UTC()
	revision := domain.DocumentRevision{ID: fmt.Sprintf("revision_%d", now.UnixNano()), DocumentID: document.ID, Title: document.Title, Content: document.Content, ContentState: document.ContentState, ContentData: document.ContentData, Author: author, CreatedAt: now}
	document.Revisions = append([]domain.DocumentRevision{revision}, document.Revisions...)
	if len(document.Revisions) > 100 {
		document.Revisions = document.Revisions[:100]
	}
}

func syncDocumentProjectResources(data *domain.Bootstrap, document domain.Document) {
	for projectIndex := range data.Projects {
		project := &data.Projects[projectIndex]
		linked := slices.Contains(document.ProjectIDs, project.ID)
		resourceIndex := slices.IndexFunc(project.Resources, func(item domain.ProjectResource) bool { return item.ID == document.ID })
		if !linked {
			if resourceIndex >= 0 {
				project.Resources = slices.Delete(project.Resources, resourceIndex, resourceIndex+1)
			}
			continue
		}
		url := "/" + data.Workspace.URLKey + "/document/" + document.SlugID
		if resourceIndex >= 0 {
			project.Resources[resourceIndex].Title = document.Title
			project.Resources[resourceIndex].URL = url
			continue
		}
		project.Resources = append(project.Resources, domain.ProjectResource{ID: document.ID, ProjectID: project.ID, Type: "document", Title: document.Title, URL: url, PinnedTeamIDs: []string{}, CreatedAt: document.CreatedAt})
	}
}

func (s *server) createDocument(w http.ResponseWriter, r *http.Request) {
	var input documentInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var created domain.Document
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "document.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		title := "New document"
		var template *domain.DocumentTemplate
		if input.TemplateID != nil && *input.TemplateID != "" {
			index := slices.IndexFunc(data.DocumentTemplates, func(item domain.DocumentTemplate) bool { return item.ID == *input.TemplateID })
			if index < 0 {
				return "", errInvalid
			}
			template = &data.DocumentTemplates[index]
			if template.Title != "" {
				title = template.Title
			}
		}
		if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
			title = strings.TrimSpace(*input.Title)
		}
		projects, teams := []string{}, []string{}
		if template != nil && template.TeamID != "" {
			teams = []string{template.TeamID}
		}
		if input.ProjectIDs != nil {
			projects = normalizedStrings(*input.ProjectIDs)
		}
		if input.TeamIDs != nil {
			teams = normalizedStrings(*input.TeamIDs)
		}
		if !validateResourceIDs(data, "project", projects) || !validateResourceIDs(data, "team", teams) {
			return "", errInvalid
		}
		issueID := ""
		if input.IssueID != nil {
			issueID = strings.TrimSpace(*input.IssueID)
			if issueID != "" && !validateResourceIDs(data, "issue", []string{issueID}) {
				return "", errInvalid
			}
		}
		created = domain.Document{ID: fmt.Sprintf("document_%d", now.UnixNano()), SlugID: slug(title) + "-" + strconv.FormatInt(now.UnixNano()%0xffffff, 16), Title: title, Color: "#8b8b90", Creator: data.Viewer, ProjectIDs: projects, TeamIDs: teams, IssueID: issueID, SubscriberIDs: []string{data.Viewer.ID}, ContentData: map[string]any{"type": "doc", "content": []any{}}, CreatedAt: now, UpdatedAt: now, Revisions: []domain.DocumentRevision{}, Permissions: []domain.DocumentPermission{{ID: fmt.Sprintf("document_permission_%d", now.UnixNano()), DocumentID: "", SubjectType: "user", SubjectID: data.Viewer.ID, Role: "owner", CreatedAt: now, UpdatedAt: now}}}
		created.Permissions[0].DocumentID = created.ID
		if template != nil {
			created.Icon, created.Content, created.ContentState = template.Icon, template.Content, template.ContentState
			if template.ContentData != nil {
				created.ContentData = template.ContentData
			}
		}
		if input.Icon != nil {
			created.Icon = *input.Icon
		}
		if input.Color != nil {
			created.Color = *input.Color
		}
		if input.Content != nil {
			created.Content = *input.Content
		}
		if input.ContentState != nil {
			created.ContentState = *input.ContentState
		}
		if input.ContentData != nil {
			created.ContentData = input.ContentData
		}
		if input.SubscriberIDs != nil && validateResourceIDs(data, "user", *input.SubscriberIDs) {
			created.SubscriberIDs = normalizedStrings(*input.SubscriberIDs)
		}
		if input.Favorite != nil {
			created.Favorite = *input.Favorite
		}
		data.Documents = append([]domain.Document{created}, data.Documents...)
		syncDocumentProjectResources(data, created)
		appendAudit(data, "created", "document", created.ID, map[string]any{"title": created.Title})
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateDocument(w http.ResponseWriter, r *http.Request) {
	var input documentInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.Document
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.updated", id, input, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		contentChange := input.Title != nil || input.Content != nil || input.ContentState != nil || input.ContentData != nil
		if contentChange {
			saveDocumentRevision(document, data.Viewer)
		}
		if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
			document.Title = strings.TrimSpace(*input.Title)
		}
		if input.Icon != nil {
			document.Icon = *input.Icon
		}
		if input.Color != nil {
			document.Color = *input.Color
		}
		if input.Content != nil {
			document.Content = *input.Content
		}
		if input.ContentState != nil {
			document.ContentState = *input.ContentState
		}
		if input.ContentData != nil {
			document.ContentData = input.ContentData
		}
		if input.ProjectIDs != nil {
			values := normalizedStrings(*input.ProjectIDs)
			if !validateResourceIDs(data, "project", values) {
				return errInvalid
			}
			document.ProjectIDs = values
		}
		if input.TeamIDs != nil {
			values := normalizedStrings(*input.TeamIDs)
			if !validateResourceIDs(data, "team", values) {
				return errInvalid
			}
			document.TeamIDs = values
		}
		if input.IssueID != nil {
			value := strings.TrimSpace(*input.IssueID)
			if value != "" && !validateResourceIDs(data, "issue", []string{value}) {
				return errInvalid
			}
			document.IssueID = value
		}
		if input.SubscriberIDs != nil {
			if !validateResourceIDs(data, "user", *input.SubscriberIDs) {
				return errInvalid
			}
			document.SubscriberIDs = normalizedStrings(*input.SubscriberIDs)
		}
		if input.Favorite != nil {
			document.Favorite = *input.Favorite
			setFavoriteRecord(data, "document", document.ID, *input.Favorite)
		}
		if input.Archived != nil {
			now := time.Now().UTC()
			if *input.Archived {
				document.ArchivedAt = &now
			} else {
				document.ArchivedAt = nil
			}
		}
		document.UpdatedAt = time.Now().UTC()
		syncDocumentProjectResources(data, *document)
		appendAudit(data, "updated", "document", document.ID, nil)
		updated = *document
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) restoreDocumentRevision(w http.ResponseWriter, r *http.Request) {
	id, revisionID := r.PathValue("id"), r.PathValue("revisionId")
	var updated domain.Document
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.revision_restored", id, map[string]string{"revisionId": revisionID}, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		index := slices.IndexFunc(document.Revisions, func(item domain.DocumentRevision) bool { return item.ID == revisionID })
		if index < 0 {
			return errNotFound
		}
		saveDocumentRevision(document, data.Viewer)
		revision := document.Revisions[index]
		document.Title, document.Content, document.ContentState, document.ContentData = revision.Title, revision.Content, revision.ContentState, revision.ContentData
		document.UpdatedAt = time.Now().UTC()
		appendAudit(data, "revision_restored", "document", document.ID, map[string]any{"revisionId": revisionID})
		updated = *document
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Documents, func(item domain.Document) bool { return item.ID == id || item.SlugID == id })
		if index < 0 {
			return errNotFound
		}
		if documentRole(s, *data, data.Documents[index]) != "owner" {
			return store.ErrAuthForbidden
		}
		removed := data.Documents[index]
		if err := appendTrash(data, "document", removed.ID, removed.Title, removed); err != nil {
			return err
		}
		data.Documents = slices.Delete(data.Documents, index, index+1)
		removeResourcePreferences(data, "document", removed.ID)
		data.Drafts = slices.DeleteFunc(data.Drafts, func(item domain.Draft) bool { return draftBelongsToResource(item, "document", removed.ID) })
		for projectIndex := range data.Projects {
			data.Projects[projectIndex].Resources = slices.DeleteFunc(data.Projects[projectIndex].Resources, func(item domain.ProjectResource) bool { return item.ID == removed.ID })
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

// Documents keep their comment threads in the same workspace comment index as
// issues. This gives standalone docs the same threaded comment semantics
// (replies, edits, reactions and deletion) without introducing a second model.
func (s *server) createDocumentComment(w http.ResponseWriter, r *http.Request) {
	var input domain.CommentCreateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	id := r.PathValue("id")
	var created domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.comment_created", id, input, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if !canCommentDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		if input.ParentID != nil && slices.IndexFunc(data.Comments[id], func(item domain.Comment) bool { return item.ID == *input.ParentID }) < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		created = domain.Comment{ID: fmt.Sprintf("document_comment_%d", now.UnixNano()), Version: 1, Body: strings.TrimSpace(input.Body), BodyData: input.BodyData, ParentID: input.ParentID, Reactions: map[string][]string{}, CreatedAt: now, User: data.Viewer}
		data.Comments[id] = append(data.Comments[id], created)
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateDocumentComment(w http.ResponseWriter, r *http.Request) {
	var input domain.CommentUpdateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	var updated, current domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.comment_updated", id, input, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if !canCommentDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		index := slices.IndexFunc(data.Comments[id], func(item domain.Comment) bool { return item.ID == commentID })
		if index < 0 {
			return errNotFound
		}
		if input.ExpectedVersion != nil && data.Comments[id][index].Version != *input.ExpectedVersion {
			current = data.Comments[id][index]
			return errConflict
		}
		now := time.Now().UTC()
		data.Comments[id][index].Body = strings.TrimSpace(input.Body)
		data.Comments[id][index].BodyData = input.BodyData
		data.Comments[id][index].EditedAt = &now
		data.Comments[id][index].Version++
		updated = data.Comments[id][index]
		return nil
	})
	if errors.Is(err, errConflict) {
		writeVersionConflict(w, current)
		return
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteDocumentComment(w http.ResponseWriter, r *http.Request) {
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.comment_deleted", id, map[string]string{"commentId": commentID}, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if !canCommentDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		before := len(data.Comments[id])
		data.Comments[id] = slices.DeleteFunc(data.Comments[id], func(item domain.Comment) bool {
			return item.ID == commentID || item.ParentID != nil && *item.ParentID == commentID
		})
		if len(data.Comments[id]) == before {
			return errNotFound
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) toggleDocumentCommentReaction(w http.ResponseWriter, r *http.Request) {
	var input domain.ReactionInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Emoji) == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	var updated domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.comment_reaction_toggled", id, input, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if !canCommentDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		index := slices.IndexFunc(data.Comments[id], func(item domain.Comment) bool { return item.ID == commentID })
		if index < 0 {
			return errNotFound
		}
		comment := &data.Comments[id][index]
		if comment.Reactions == nil {
			comment.Reactions = map[string][]string{}
		}
		users := comment.Reactions[input.Emoji]
		if slices.Contains(users, data.Viewer.ID) {
			users = removeString(users, data.Viewer.ID)
		} else {
			users = append(users, data.Viewer.ID)
		}
		if len(users) == 0 {
			delete(comment.Reactions, input.Emoji)
		} else {
			comment.Reactions[input.Emoji] = users
		}
		updated = *comment
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) createCustomerRequest(w http.ResponseWriter, r *http.Request) {
	var input customerRequestInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.CustomerID) == "" || input.Body == nil || strings.TrimSpace(*input.Body) == "" {
		writeError(w, http.StatusBadRequest, "customerId and body are required")
		return
	}
	var created domain.CustomerRequest
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "customer_request.created", input, func(data *domain.Bootstrap) (string, error) {
		if !slices.ContainsFunc(data.Customers, func(item domain.Customer) bool { return item.ID == input.CustomerID }) {
			return "", errNotFound
		}
		if input.IssueID != nil && *input.IssueID != "" && !validateResourceIDs(data, "issue", []string{*input.IssueID}) {
			return "", errInvalid
		}
		if input.ProjectID != nil && *input.ProjectID != "" && !validateResourceIDs(data, "project", []string{*input.ProjectID}) {
			return "", errInvalid
		}
		now := time.Now().UTC()
		source := "manual"
		if input.Source != nil && *input.Source != "" {
			source = *input.Source
		}
		created = domain.CustomerRequest{ID: fmt.Sprintf("customer_request_%d", now.UnixNano()), CustomerID: input.CustomerID, Body: strings.TrimSpace(*input.Body), Source: source, Creator: data.Viewer, Attachments: []domain.Attachment{}, CreatedAt: now, UpdatedAt: now}
		if input.SourceURL != nil {
			created.SourceURL = *input.SourceURL
		}
		if input.IssueID != nil {
			created.IssueID = *input.IssueID
		}
		if input.ProjectID != nil {
			created.ProjectID = *input.ProjectID
		}
		data.CustomerRequests = append([]domain.CustomerRequest{created}, data.CustomerRequests...)
		appendAudit(data, "created", "customer_request", created.ID, map[string]any{"customerId": created.CustomerID})
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateCustomerRequest(w http.ResponseWriter, r *http.Request) {
	var input customerRequestInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.CustomerRequest
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_request.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		item := &data.CustomerRequests[index]
		if input.Body != nil && strings.TrimSpace(*input.Body) != "" {
			item.Body = strings.TrimSpace(*input.Body)
		}
		if input.Source != nil {
			item.Source = *input.Source
		}
		if input.SourceURL != nil {
			item.SourceURL = *input.SourceURL
		}
		if input.IssueID != nil {
			if *input.IssueID != "" && !validateResourceIDs(data, "issue", []string{*input.IssueID}) {
				return errInvalid
			}
			item.IssueID = *input.IssueID
		}
		if input.ProjectID != nil {
			if *input.ProjectID != "" && !validateResourceIDs(data, "project", []string{*input.ProjectID}) {
				return errInvalid
			}
			item.ProjectID = *input.ProjectID
		}
		item.UpdatedAt = time.Now().UTC()
		updated = *item
		appendAudit(data, "updated", "customer_request", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteCustomerRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_request.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		item := data.CustomerRequests[index]
		if err := appendTrash(data, "customer_request", item.ID, item.Body, item); err != nil {
			return err
		}
		data.CustomerRequests = slices.Delete(data.CustomerRequests, index, index+1)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) createCustomerRequestAttachment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, (20<<20)+(1<<20))
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid attachment")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	requestID := r.PathValue("id")
	attachmentID := fmt.Sprintf("customer_request_attachment_%d", time.Now().UnixNano())
	safeName := attachmentID + "_" + filepath.Base(header.Filename)
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not store attachment")
		return
	}
	size, copyErr := storage.Put(r.Context(), safeName, io.LimitReader(file, (20<<20)+1), header.Header.Get("Content-Type"))
	if copyErr != nil || size > 20<<20 {
		_ = storage.Delete(r.Context(), safeName)
		if size > 20<<20 {
			writeError(w, http.StatusRequestEntityTooLarge, "attachment exceeds 20 MB")
		} else {
			writeError(w, http.StatusInternalServerError, "upload failed")
		}
		return
	}
	var attachment domain.Attachment
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_request.attachment_created", requestID, map[string]string{"name": header.Filename}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == requestID })
		if index < 0 {
			return errNotFound
		}
		attachment = domain.Attachment{ID: attachmentID, Title: header.Filename, URL: "/uploads/" + safeName, ContentType: header.Header.Get("Content-Type"), Size: size, CreatedAt: time.Now().UTC(), Creator: data.Viewer}
		data.CustomerRequests[index].Attachments = append(data.CustomerRequests[index].Attachments, attachment)
		data.CustomerRequests[index].UpdatedAt = attachment.CreatedAt
		appendAudit(data, "attachment_created", "customer_request", requestID, map[string]any{"attachmentId": attachment.ID})
		return nil
	})
	if err != nil {
		_ = storage.Delete(r.Context(), safeName)
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}

func (s *server) deleteCustomerRequestAttachment(w http.ResponseWriter, r *http.Request) {
	requestID, attachmentID := r.PathValue("id"), r.PathValue("attachmentId")
	var objectKey string
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_request.attachment_deleted", requestID, map[string]string{"attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == requestID })
		if index < 0 {
			return errNotFound
		}
		request := &data.CustomerRequests[index]
		attachmentIndex := slices.IndexFunc(request.Attachments, func(item domain.Attachment) bool { return item.ID == attachmentID })
		if attachmentIndex < 0 {
			return errNotFound
		}
		objectKey = filepath.Base(request.Attachments[attachmentIndex].URL)
		request.Attachments = slices.Delete(request.Attachments, attachmentIndex, attachmentIndex+1)
		request.UpdatedAt = time.Now().UTC()
		appendAudit(data, "attachment_deleted", "customer_request", requestID, map[string]any{"attachmentId": attachmentID})
		return nil
	})
	if err == nil && objectKey != "" {
		if storage, storageErr := s.storage(); storageErr == nil {
			_ = storage.Delete(r.Context(), objectKey)
		}
	}
	respondMutation(w, err, http.StatusNoContent, nil)
}

func applyReleaseInput(data *domain.Bootstrap, release *domain.Release, input releaseInput) error {
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" {
			return errInvalid
		}
		release.Name = strings.TrimSpace(*input.Name)
	}
	if input.Version != nil {
		release.Version = strings.TrimSpace(*input.Version)
	}
	if input.Description != nil {
		release.Description = *input.Description
	}
	if input.PipelineID != nil {
		pipelineID := strings.TrimSpace(*input.PipelineID)
		if pipelineID == "" {
			release.PipelineID, release.Stage = "", ""
		} else {
			pipeline := releasePipelineByID(data, pipelineID)
			if pipeline == nil {
				return errInvalid
			}
			release.PipelineID = pipelineID
		}
	}
	if input.Stage != nil {
		release.Stage = strings.TrimSpace(*input.Stage)
	}
	if release.Stage != "" {
		pipeline := releasePipelineByID(data, release.PipelineID)
		if pipeline == nil || !slices.Contains(pipeline.Stages, release.Stage) {
			return errInvalid
		}
	}
	if input.CommitSHA != nil {
		release.CommitSHA = strings.TrimSpace(*input.CommitSHA)
	}
	if input.ReleaseNotes != nil {
		release.ReleaseNotes = *input.ReleaseNotes
	}
	if input.Status != nil {
		if !slices.Contains([]string{"planned", "inProgress", "released", "canceled"}, *input.Status) {
			return errInvalid
		}
		release.Status = *input.Status
		now := time.Now().UTC()
		if *input.Status == "inProgress" && release.StartedAt == nil {
			release.StartedAt = &now
		}
		if *input.Status == "released" {
			if release.StartedAt == nil {
				release.StartedAt = &now
			}
			if release.ReleasedAt == nil {
				release.ReleasedAt = &now
			}
		}
	}
	if input.TargetDate != nil {
		if *input.TargetDate == "" {
			release.TargetDate = nil
		} else {
			value := strings.TrimSpace(*input.TargetDate)
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return errInvalid
			}
			release.TargetDate = &value
		}
	}
	if input.ProjectIDs != nil {
		values := normalizedStrings(*input.ProjectIDs)
		if !validateResourceIDs(data, "project", values) {
			return errInvalid
		}
		release.ProjectIDs = values
	}
	if input.IssueIDs != nil {
		values := normalizedStrings(*input.IssueIDs)
		if !validateResourceIDs(data, "issue", values) {
			return errInvalid
		}
		willRemainFrozen := release.StageFrozenAt != nil && (input.StageFrozen == nil || *input.StageFrozen)
		if willRemainFrozen && slices.ContainsFunc(values, func(id string) bool { return !slices.Contains(release.IssueIDs, id) }) {
			return errConflict
		}
		release.IssueIDs = values
	}
	if input.SubscriberIDs != nil {
		values := normalizedStrings(*input.SubscriberIDs)
		if !validateResourceIDs(data, "user", values) {
			return errInvalid
		}
		release.SubscriberIDs = values
	}
	if input.Resources != nil {
		resources := make([]domain.ReleaseResource, 0, len(*input.Resources))
		seen := map[string]bool{}
		for index, resource := range *input.Resources {
			resource.Title, resource.URL, resource.DocumentID = strings.TrimSpace(resource.Title), strings.TrimSpace(resource.URL), strings.TrimSpace(resource.DocumentID)
			if resource.Type != "link" && resource.Type != "document" || resource.Title == "" || resource.Type == "link" && resource.URL == "" || resource.Type == "document" && !validateResourceIDs(data, "document", []string{resource.DocumentID}) {
				return errInvalid
			}
			if resource.ID == "" {
				resource.ID = fmt.Sprintf("release_resource_%d_%d", time.Now().UTC().UnixNano(), index)
			}
			if seen[resource.ID] {
				return errInvalid
			}
			seen[resource.ID] = true
			if resource.CreatedAt.IsZero() {
				resource.CreatedAt = time.Now().UTC()
			}
			resources = append(resources, resource)
		}
		release.Resources = resources
	}
	if input.StageFrozen != nil {
		if *input.StageFrozen {
			if release.PipelineID == "" || release.Stage == "" {
				return errInvalid
			}
			if release.StageFrozenAt == nil {
				now := time.Now().UTC()
				release.StageFrozenAt = &now
			}
		} else {
			release.StageFrozenAt = nil
		}
	}
	if release.StageFrozenAt != nil && (release.PipelineID == "" || release.Stage == "") {
		return errInvalid
	}
	if input.Archived != nil {
		now := time.Now().UTC()
		if *input.Archived {
			release.ArchivedAt = &now
		} else {
			release.ArchivedAt = nil
		}
	}
	return nil
}

func (s *server) createRelease(w http.ResponseWriter, r *http.Request) {
	var input releaseInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var created domain.Release
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "release.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		if input.PipelineID != nil {
			pipeline := releasePipelineByID(data, stringValue(input.PipelineID))
			if pipeline != nil && pipeline.Type == "continuous" {
				return "", errConflict
			}
		}
		created = domain.Release{ID: fmt.Sprintf("release_%d", now.UnixNano()), SlugID: uniqueReleaseSlug(data, strings.TrimSpace(*input.Name), now), Name: strings.TrimSpace(*input.Name), Status: "planned", Position: nextReleasePosition(data, stringValue(input.PipelineID)), ProjectIDs: []string{}, IssueIDs: []string{}, SubscriberIDs: []string{data.Viewer.ID}, Resources: []domain.ReleaseResource{}, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
		if err := applyReleaseInput(data, &created, input); err != nil {
			return "", err
		}
		data.Releases = append([]domain.Release{created}, data.Releases...)
		data.ReleaseHistory = append(data.ReleaseHistory, domain.ReleaseHistory{ID: fmt.Sprintf("release_history_%d", now.UnixNano()), ReleaseID: created.ID, Actor: data.Viewer, Action: "created", Metadata: map[string]any{"name": created.Name}, CreatedAt: now})
		appendAudit(data, "created", "release", created.ID, map[string]any{"name": created.Name})
		return created.ID, nil
	})
	if errors.Is(err, errConflict) {
		writeError(w, http.StatusConflict, "continuous pipelines create releases through CI/CD integrations")
		return
	}
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateRelease(w http.ResponseWriter, r *http.Request) {
	var input releaseInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.Release
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Releases, func(item domain.Release) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		previousPipelineID := data.Releases[index].PipelineID
		targetPosition := data.Releases[index].Position
		if input.PipelineID != nil && stringValue(input.PipelineID) != previousPipelineID {
			targetPosition = nextReleasePosition(data, stringValue(input.PipelineID))
		}
		if err := applyReleaseInput(data, &data.Releases[index], input); err != nil {
			return err
		}
		if data.Releases[index].PipelineID != previousPipelineID {
			data.Releases[index].Position = targetPosition
		}
		now := time.Now().UTC()
		data.Releases[index].UpdatedAt = now
		updated = data.Releases[index]
		appendAudit(data, "updated", "release", id, nil)
		data.ReleaseHistory = append(data.ReleaseHistory, domain.ReleaseHistory{ID: fmt.Sprintf("release_history_%d", now.UnixNano()), ReleaseID: id, Actor: data.Viewer, Action: "updated", Metadata: map[string]any{}, CreatedAt: now})
		return nil
	})
	if errors.Is(err, errConflict) {
		writeError(w, http.StatusConflict, "frozen release stages do not accept new issues")
		return
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Releases, func(item domain.Release) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		item := data.Releases[index]
		if err := appendTrash(data, "release", item.ID, item.Name, item); err != nil {
			return err
		}
		data.Releases = slices.Delete(data.Releases, index, index+1)
		removeResourcePreferences(data, "release", item.ID)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func applyAskInput(data *domain.Bootstrap, ask *domain.Ask, input askInput) error {
	if input.Title != nil {
		if strings.TrimSpace(*input.Title) == "" {
			return errInvalid
		}
		ask.Title = strings.TrimSpace(*input.Title)
	}
	if input.Body != nil {
		ask.Body = *input.Body
	}
	if input.Source != nil {
		ask.Source = *input.Source
	}
	if input.TeamID != nil {
		if *input.TeamID != "" && !validateResourceIDs(data, "team", []string{*input.TeamID}) {
			return errInvalid
		}
		ask.TeamID = *input.TeamID
	}
	if input.TemplateID != nil {
		if *input.TemplateID != "" && !slices.ContainsFunc(data.IssueTemplates, func(item domain.IssueTemplate) bool { return item.ID == *input.TemplateID }) {
			return errInvalid
		}
		ask.TemplateID = *input.TemplateID
	}
	if input.IssueID != nil {
		if *input.IssueID != "" && !validateResourceIDs(data, "issue", []string{*input.IssueID}) {
			return errInvalid
		}
		ask.IssueID = *input.IssueID
	}
	return nil
}

func (s *server) createAsk(w http.ResponseWriter, r *http.Request) {
	var input askInput
	if !decodeJSON(w, r, &input) || input.Title == nil || strings.TrimSpace(*input.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	var created domain.Ask
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "ask.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.Ask{ID: fmt.Sprintf("ask_%d", now.UnixNano()), Title: strings.TrimSpace(*input.Title), Source: "web", Requester: data.Viewer, Status: "pending", Approvals: []domain.AskApproval{}, CreatedAt: now, UpdatedAt: now}
		if err := applyAskInput(data, &created, input); err != nil {
			return "", err
		}
		data.Asks = append([]domain.Ask{created}, data.Asks...)
		appendAudit(data, "created", "ask", created.ID, map[string]any{"source": created.Source})
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateAsk(w http.ResponseWriter, r *http.Request) {
	var input askInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.Ask
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "ask.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Asks, func(item domain.Ask) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		if err := applyAskInput(data, &data.Asks[index], input); err != nil {
			return err
		}
		data.Asks[index].UpdatedAt = time.Now().UTC()
		updated = data.Asks[index]
		appendAudit(data, "updated", "ask", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) decideAsk(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if !decodeJSON(w, r, &input) || !slices.Contains([]string{"approved", "rejected"}, input.Decision) {
		writeError(w, http.StatusBadRequest, "decision must be approved or rejected")
		return
	}
	id := r.PathValue("id")
	var updated domain.Ask
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "ask.decided", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Asks, func(item domain.Ask) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		ask := &data.Asks[index]
		now := time.Now().UTC()
		approval := domain.AskApproval{ID: fmt.Sprintf("ask_approval_%d", now.UnixNano()), AskID: id, Approver: data.Viewer, Decision: input.Decision, Note: strings.TrimSpace(input.Note), DecidedAt: &now}
		ask.Approvals = append(ask.Approvals, approval)
		ask.Status, ask.UpdatedAt = input.Decision, now
		if input.Decision == "approved" && ask.IssueID == "" {
			issue, err := createIssueFromAsk(data, *ask, now)
			if err != nil {
				return err
			}
			ask.IssueID = issue.ID
		}
		updated = *ask
		appendAudit(data, input.Decision, "ask", id, map[string]any{"note": approval.Note})
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func createIssueFromAsk(data *domain.Bootstrap, ask domain.Ask, now time.Time) (domain.Issue, error) {
	teamID, description := ask.TeamID, ask.Body
	priority, templatePriority := 0, false
	var stateID, assigneeID, projectID string
	labelIDs := []string{}
	if ask.TemplateID != "" {
		index := slices.IndexFunc(data.IssueTemplates, func(item domain.IssueTemplate) bool { return item.ID == ask.TemplateID })
		if index < 0 {
			return domain.Issue{}, errNotFound
		}
		template := data.IssueTemplates[index]
		if teamID == "" {
			teamID = template.TeamID
		}
		if description == "" {
			description = template.Body
		}
		priority, templatePriority, stateID, assigneeID, projectID = template.Priority, true, template.StateID, template.AssigneeID, template.ProjectID
		labelIDs = slices.Clone(template.LabelIDs)
	}
	if teamID == "" && len(data.Teams) > 0 {
		teamID = data.Teams[0].ID
	}
	teamIndex := slices.IndexFunc(data.Teams, func(item domain.Team) bool { return item.ID == teamID })
	if teamIndex < 0 {
		return domain.Issue{}, errInvalid
	}
	team := data.Teams[teamIndex]
	settings := teamSettings(data, team.ID)
	if !templatePriority {
		priority = settings.DefaultPriority
	}
	state := stateForTeam(data, team.ID, stateID)
	if state == nil {
		state = stateForTeam(data, team.ID, settings.DefaultStateID)
	}
	if state == nil {
		states := statesForTeam(data, team.ID)
		if len(states) == 0 {
			return domain.Issue{}, errInvalid
		}
		state = &states[0]
	}
	number := nextIssueNumber(data.Issues)
	issue := domain.Issue{ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number, Title: ask.Title, Description: description, Priority: priority, PriorityLabel: priorityLabel(priority), SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: team, State: *state, Assignee: &data.Viewer, Creator: ask.Requester, Labels: []domain.IssueLabel{}, SubscriberIDs: []string{ask.Requester.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
	update := domain.IssueUpdateInput{}
	if assigneeID != "" {
		update.AssigneeID = &assigneeID
	}
	if projectID != "" {
		update.ProjectID = &projectID
	}
	if len(labelIDs) > 0 {
		update.LabelIDs = &labelIDs
	}
	if _, err := applyUpdate(data, &issue, update); err != nil {
		return domain.Issue{}, err
	}
	applySLARules(data, &issue, now)
	data.Issues = append([]domain.Issue{issue}, data.Issues...)
	appendActivity(data, issue.ID, "issue.created_from_ask", data.Viewer, map[string]string{"askId": ask.ID})
	return issue, nil
}

func (s *server) deleteAsk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "ask.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Asks, func(item domain.Ask) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		item := data.Asks[index]
		if err := appendTrash(data, "ask", item.ID, item.Title, item); err != nil {
			return err
		}
		data.Asks = slices.Delete(data.Asks, index, index+1)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) listProjectTemplates(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, data.ProjectTemplates)
}

func applyProjectTemplateInput(data *domain.Bootstrap, template *domain.ProjectTemplate, input projectTemplateInput) error {
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" {
			return errInvalid
		}
		template.Name = strings.TrimSpace(*input.Name)
	}
	if input.ProjectName != nil {
		template.ProjectName = strings.TrimSpace(*input.ProjectName)
	}
	if input.TemplateDescription != nil {
		template.TemplateDescription = strings.TrimSpace(*input.TemplateDescription)
	}
	if input.Description != nil {
		template.Description = *input.Description
	}
	if input.Summary != nil {
		template.Summary = *input.Summary
	}
	if input.Icon != nil {
		template.Icon = *input.Icon
	}
	if input.Color != nil {
		template.Color = *input.Color
	}
	if input.StatusID != nil {
		if *input.StatusID != "" && !slices.ContainsFunc(data.ProjectStatuses, func(item domain.ProjectStatus) bool { return item.ID == *input.StatusID }) {
			return errInvalid
		}
		template.StatusID = *input.StatusID
	}
	if input.Priority != nil {
		if *input.Priority < 0 || *input.Priority > 4 {
			return errInvalid
		}
		template.Priority = *input.Priority
	}
	if input.TeamIDs != nil {
		values := normalizedStrings(*input.TeamIDs)
		if !validateResourceIDs(data, "team", values) {
			return errInvalid
		}
		template.TeamIDs = values
	}
	if input.LabelIDs != nil {
		values := normalizedStrings(*input.LabelIDs)
		labels := labelsByIDForResource(data, values, "project")
		if len(labels) != len(values) || !validLabelGroupSelection(labels) {
			return errInvalid
		}
		template.LabelIDs = values
	}
	if input.LeadID != nil {
		if *input.LeadID != "" && userByID(data, *input.LeadID) == nil {
			return errInvalid
		}
		template.LeadID = *input.LeadID
	}
	if input.MemberIDs != nil {
		values := normalizedStrings(*input.MemberIDs)
		if !validateResourceIDs(data, "user", values) {
			return errInvalid
		}
		template.MemberIDs = values
	}
	if input.DependencyIDs != nil {
		values := normalizedStrings(*input.DependencyIDs)
		if !validateResourceIDs(data, "project", values) {
			return errInvalid
		}
		template.DependencyIDs = values
	}
	if input.InitiativeIDs != nil {
		values := normalizedStrings(*input.InitiativeIDs)
		if !validateResourceIDs(data, "initiative", values) {
			return errInvalid
		}
		template.InitiativeIDs = values
	}
	if input.IssueIDs != nil {
		values := normalizedStrings(*input.IssueIDs)
		if !validateResourceIDs(data, "issue", values) {
			return errInvalid
		}
		template.IssueIDs = values
	}
	if input.Milestones != nil {
		milestones := make([]domain.TemplateMilestone, 0, len(*input.Milestones))
		for index, milestone := range *input.Milestones {
			milestone.Name = strings.TrimSpace(milestone.Name)
			milestone.Description = strings.TrimSpace(milestone.Description)
			if milestone.Name == "" {
				return errInvalid
			}
			if strings.TrimSpace(milestone.ID) == "" {
				milestone.ID = fmt.Sprintf("template_milestone_%d_%d", time.Now().UnixNano(), index)
			}
			milestones = append(milestones, milestone)
		}
		template.Milestones = milestones
	}
	if input.Visibility != nil {
		if !slices.Contains([]string{"workspace", "teams"}, *input.Visibility) {
			return errInvalid
		}
		template.Visibility = *input.Visibility
	}
	if input.VisibilityTeamID != nil {
		if *input.VisibilityTeamID != "" && !validateResourceIDs(data, "team", []string{*input.VisibilityTeamID}) {
			return errInvalid
		}
		template.VisibilityTeamID = *input.VisibilityTeamID
		template.Visibility = "workspace"
		if *input.VisibilityTeamID != "" {
			template.Visibility = "teams"
		}
	}
	return nil
}

func (s *server) createProjectTemplate(w http.ResponseWriter, r *http.Request) {
	var input projectTemplateInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var created domain.ProjectTemplate
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "project_template.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.ProjectTemplate{ID: fmt.Sprintf("project_template_%d", now.UnixNano()), Name: strings.TrimSpace(*input.Name), Color: "#5e6ad2", TeamIDs: []string{}, LabelIDs: []string{}, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
		if err := applyProjectTemplateInput(data, &created, input); err != nil {
			return "", err
		}
		data.ProjectTemplates = append([]domain.ProjectTemplate{created}, data.ProjectTemplates...)
		appendAudit(data, "created", "project_template", created.ID, nil)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateProjectTemplate(w http.ResponseWriter, r *http.Request) {
	var input projectTemplateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.ProjectTemplate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project_template.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ProjectTemplates, func(item domain.ProjectTemplate) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		if err := applyProjectTemplateInput(data, &data.ProjectTemplates[index], input); err != nil {
			return err
		}
		data.ProjectTemplates[index].UpdatedAt = time.Now().UTC()
		updated = data.ProjectTemplates[index]
		appendAudit(data, "updated", "project_template", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteProjectTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project_template.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.ProjectTemplates)
		data.ProjectTemplates = slices.DeleteFunc(data.ProjectTemplates, func(item domain.ProjectTemplate) bool { return item.ID == id })
		if before == len(data.ProjectTemplates) {
			return errNotFound
		}
		appendAudit(data, "deleted", "project_template", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func applySLARuleInput(data *domain.Bootstrap, rule *domain.SLARule, input slaRuleInput) error {
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" {
			return errInvalid
		}
		rule.Name = strings.TrimSpace(*input.Name)
	}
	if input.TeamIDs != nil {
		values := normalizedStrings(*input.TeamIDs)
		if !validateResourceIDs(data, "team", values) {
			return errInvalid
		}
		rule.TeamIDs = values
	}
	if input.Filters != nil {
		rule.Filters = input.Filters
	}
	if input.TargetMinutes != nil {
		if *input.TargetMinutes < 1 {
			return errInvalid
		}
		rule.TargetMinutes = *input.TargetMinutes
	}
	if input.PauseStatuses != nil {
		rule.PauseStatuses = normalizedStrings(*input.PauseStatuses)
	}
	if input.BusinessHours != nil {
		rule.BusinessHours = *input.BusinessHours
	}
	if input.Enabled != nil {
		rule.Enabled = *input.Enabled
	}
	return nil
}

func (s *server) createSLARule(w http.ResponseWriter, r *http.Request) {
	var input slaRuleInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var created domain.SLARule
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "sla_rule.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.SLARule{ID: fmt.Sprintf("sla_rule_%d", now.UnixNano()), Name: strings.TrimSpace(*input.Name), TeamIDs: []string{}, Filters: map[string]any{}, TargetMinutes: 1440, PauseStatuses: []string{}, Enabled: true, CreatedAt: now, UpdatedAt: now}
		if err := applySLARuleInput(data, &created, input); err != nil {
			return "", err
		}
		data.SLARules = append([]domain.SLARule{created}, data.SLARules...)
		for index := range data.Issues {
			applySLARules(data, &data.Issues[index], now)
		}
		appendAudit(data, "created", "sla_rule", created.ID, nil)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateSLARule(w http.ResponseWriter, r *http.Request) {
	var input slaRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.SLARule
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "sla_rule.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.SLARules, func(item domain.SLARule) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		if err := applySLARuleInput(data, &data.SLARules[index], input); err != nil {
			return err
		}
		data.SLARules[index].UpdatedAt = time.Now().UTC()
		updated = data.SLARules[index]
		for issueIndex := range data.Issues {
			applySLARules(data, &data.Issues[issueIndex], time.Now().UTC())
		}
		appendAudit(data, "updated", "sla_rule", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteSLARule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "sla_rule.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.SLARules)
		data.SLARules = slices.DeleteFunc(data.SLARules, func(item domain.SLARule) bool { return item.ID == id })
		if before == len(data.SLARules) {
			return errNotFound
		}
		data.IssueSLAs = slices.DeleteFunc(data.IssueSLAs, func(item domain.IssueSLA) bool { return item.RuleID == id })
		appendAudit(data, "deleted", "sla_rule", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func slaMatches(rule domain.SLARule, issue domain.Issue) bool {
	if !rule.Enabled || len(rule.TeamIDs) > 0 && !slices.Contains(rule.TeamIDs, issue.Team.ID) {
		return false
	}
	for key, raw := range rule.Filters {
		value := fmt.Sprint(raw)
		switch key {
		case "priority":
			if strconv.Itoa(issue.Priority) != value {
				return false
			}
		case "status":
			if issue.State.ID != value && issue.State.Type != value {
				return false
			}
		case "label":
			if !slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return label.ID == value || strings.EqualFold(label.Name, value) }) {
				return false
			}
		case "project":
			if issue.Project == nil || issue.Project.ID != value {
				return false
			}
		}
	}
	return true
}

func applySLARules(data *domain.Bootstrap, issue *domain.Issue, now time.Time) {
	for _, rule := range data.SLARules {
		index := slices.IndexFunc(data.IssueSLAs, func(item domain.IssueSLA) bool { return item.IssueID == issue.ID && item.RuleID == rule.ID })
		matches := slaMatches(rule, *issue)
		if !matches {
			if index >= 0 && data.IssueSLAs[index].Status != "completed" {
				data.IssueSLAs[index].Status = "removed"
			}
			continue
		}
		if index < 0 {
			value := domain.IssueSLA{ID: fmt.Sprintf("issue_sla_%d", now.UnixNano()+int64(len(data.IssueSLAs))), IssueID: issue.ID, RuleID: rule.ID, StartedAt: now, DueAt: now.Add(time.Duration(rule.TargetMinutes) * time.Minute), RemainingMinutes: rule.TargetMinutes, Status: "active"}
			if slices.Contains(rule.PauseStatuses, issue.State.ID) || slices.Contains(rule.PauseStatuses, issue.State.Type) {
				value.PausedAt, value.Status = &now, "paused"
			}
			data.IssueSLAs = append(data.IssueSLAs, value)
			recordSLAEvent(data, issue.ID, value.ID, "started", now)
			if value.Status == "paused" {
				recordSLAEvent(data, issue.ID, value.ID, "paused", now)
			}
			continue
		}
		sla := &data.IssueSLAs[index]
		paused := slices.Contains(rule.PauseStatuses, issue.State.ID) || slices.Contains(rule.PauseStatuses, issue.State.Type)
		if paused && sla.PausedAt == nil {
			sla.PausedAt, sla.Status = &now, "paused"
			recordSLAEvent(data, issue.ID, sla.ID, "paused", now)
		}
		if !paused && sla.PausedAt != nil {
			minutes := int(now.Sub(*sla.PausedAt).Minutes())
			sla.PausedMinutes += max(0, minutes)
			sla.DueAt = sla.DueAt.Add(time.Duration(max(0, minutes)) * time.Minute)
			sla.PausedAt, sla.Status = nil, "active"
			recordSLAEvent(data, issue.ID, sla.ID, "resumed", now)
		}
		if issue.State.Type == "completed" || issue.State.Type == "canceled" {
			if sla.CompletedAt == nil {
				sla.CompletedAt, sla.Status = &now, "completed"
				recordSLAEvent(data, issue.ID, sla.ID, "completed", now)
			}
			continue
		}
		if sla.PausedAt == nil {
			sla.RemainingMinutes = int(sla.DueAt.Sub(now).Minutes())
			if now.After(sla.DueAt) && sla.BreachedAt == nil {
				sla.BreachedAt, sla.Status = &now, "breached"
				recordSLAEvent(data, issue.ID, sla.ID, "breached", now)
				activity := appendActivity(data, issue.ID, "issue.sla_breached", data.Viewer, map[string]string{"slaId": sla.ID, "ruleId": rule.ID})
				appendIssueNotifications(data, *issue, activity, nil)
			}
		}
	}
}

func recordSLAEvent(data *domain.Bootstrap, issueID, slaID, eventType string, now time.Time) {
	data.SLAEvents = append(data.SLAEvents, domain.SLAEvent{ID: fmt.Sprintf("sla_event_%d_%d", now.UnixNano(), len(data.SLAEvents)), IssueID: issueID, SLAID: slaID, Type: eventType, CreatedAt: now})
}

func (s *server) updateProjectUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var input map[string]any
	if !decodeJSON(w, r, &input) {
		return
	}
	var result map[string]any
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project_update_settings.updated", "project_update_settings", input, func(data *domain.Bootstrap) error {
		current, _ := data.Settings["projectUpdates"].(map[string]any)
		if current == nil {
			current = map[string]any{}
		}
		for key, value := range input {
			current[key] = value
		}
		data.Settings["projectUpdates"] = current
		result = current
		appendAudit(data, "updated", "project_update_settings", "workspace", input)
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}

func (s *server) updateSLASettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &input) || input.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	result := map[string]any{"enabled": *input.Enabled}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "sla_settings.updated", "workspace", input, func(data *domain.Bootstrap) error {
		if data.Settings == nil {
			data.Settings = map[string]any{}
		}
		data.Settings["sla"] = result
		appendAudit(data, "updated", "sla_settings", "workspace", result)
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}

func (s *server) createDraft(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var input draftInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var created domain.Draft
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "draft.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		kind := "issue"
		if input.Type != nil && *input.Type != "" {
			kind = *input.Type
		}
		created = domain.Draft{ID: fmt.Sprintf("draft_%d", now.UnixNano()), UserID: data.Viewer.ID, Type: kind, ContentData: input.ContentData, Metadata: input.Metadata, CreatedAt: now, UpdatedAt: now}
		applyDraftInput(&created, input)
		if err := validateDraft(data, created); err != nil {
			return "", err
		}
		data.Drafts = append([]domain.Draft{created}, data.Drafts...)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateDraft(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var input draftInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.Draft
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "draft.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Drafts, func(item domain.Draft) bool { return item.ID == id && item.UserID == data.Viewer.ID })
		if index < 0 {
			return errNotFound
		}
		candidate := data.Drafts[index]
		applyDraftInput(&candidate, input)
		if err := validateDraft(data, candidate); err != nil {
			return err
		}
		candidate.UpdatedAt = time.Now().UTC()
		data.Drafts[index], updated = candidate, candidate
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func applyDraftInput(item *domain.Draft, input draftInput) {
	if input.Type != nil {
		item.Type = normalizeDraftType(*input.Type)
	}
	if input.ResourceID != nil {
		item.ResourceID = strings.TrimSpace(*input.ResourceID)
	}
	if input.Title != nil {
		item.Title = *input.Title
	}
	if input.Body != nil {
		item.Body = *input.Body
	}
	if input.ContentData != nil {
		item.ContentData = input.ContentData
	}
	if input.Metadata != nil {
		item.Metadata = input.Metadata
	}
}

// normalizeDraftType keeps the public API tolerant of the entity-style names
// emitted by clients while storing one canonical value for filtering and UI.
func normalizeDraftType(value string) string {
	switch strings.TrimSpace(value) {
	case "projectUpdate":
		return "project_update"
	case "initiativeUpdate":
		return "initiative_update"
	case "customerNeed":
		return "customer_need"
	case "pullRequestComment":
		return "pull_request_comment"
	default:
		return strings.TrimSpace(value)
	}
}

func draftBelongsToResource(item domain.Draft, resourceType, resourceID string) bool {
	if item.ResourceID != resourceID {
		return false
	}
	if item.Type == resourceType {
		return true
	}
	if item.Type != "comment" {
		return false
	}
	parentType := "issue"
	if value, ok := item.Metadata["resourceType"].(string); ok && strings.TrimSpace(value) != "" {
		parentType = normalizeDraftType(value)
	}
	switch parentType {
	case "project_update":
		parentType = "project"
	case "initiative_update":
		parentType = "initiative"
	case "customer_need":
		parentType = "customer"
	case "pull_request_comment":
		parentType = "review"
	}
	return parentType == resourceType
}

func validateDraft(data *domain.Bootstrap, item domain.Draft) error {
	if !slices.Contains([]string{"issue", "comment", "document", "loop", "project_update", "initiative_update", "customer_need", "pull_request_comment"}, item.Type) {
		return fmt.Errorf("%w: unsupported draft type", errInvalid)
	}
	if len(item.Title) > 512 || len(item.Body) > 1<<20 {
		return fmt.Errorf("%w: draft content is too large", errInvalid)
	}
	if item.Type == "comment" {
		parentType := "issue"
		if value, ok := item.Metadata["resourceType"].(string); ok && strings.TrimSpace(value) != "" {
			parentType = normalizeDraftType(value)
		}
		if parentType == "project_update" {
			parentType = "project"
		} else if parentType == "initiative_update" {
			parentType = "initiative"
		} else if parentType == "customer_need" {
			parentType = "customer"
		} else if parentType == "pull_request_comment" {
			parentType = "review"
		}
		if item.ResourceID == "" || !resourceExists(data, parentType, item.ResourceID) {
			return fmt.Errorf("%w: comment draft requires a valid parent resource", errInvalid)
		}
	}
	if item.Type == "project_update" && (item.ResourceID == "" || !resourceExists(data, "project", item.ResourceID)) {
		return fmt.Errorf("%w: project update draft requires a project", errInvalid)
	}
	if item.Type == "initiative_update" && (item.ResourceID == "" || !resourceExists(data, "initiative", item.ResourceID)) {
		return fmt.Errorf("%w: initiative update draft requires an initiative", errInvalid)
	}
	if item.Type == "customer_need" && (item.ResourceID == "" || !resourceExists(data, "customer", item.ResourceID)) {
		return fmt.Errorf("%w: customer request draft requires a customer", errInvalid)
	}
	if item.Type == "pull_request_comment" && (item.ResourceID == "" || !resourceExists(data, "review", item.ResourceID)) {
		return fmt.Errorf("%w: pull request comment draft requires a review", errInvalid)
	}
	resourceType := item.Type
	if item.Type == "project_update" {
		resourceType = "project"
	} else if item.Type == "initiative_update" {
		resourceType = "initiative"
	} else if item.Type == "customer_need" {
		resourceType = "customer"
	} else if item.Type == "pull_request_comment" {
		resourceType = "review"
	}
	if item.ResourceID != "" && item.Type != "comment" && item.Type != "project_update" && item.Type != "initiative_update" && item.Type != "customer_need" && item.Type != "pull_request_comment" && !resourceExists(data, resourceType, item.ResourceID) {
		return fmt.Errorf("%w: draft resource does not exist", errInvalid)
	}
	return nil
}

func (s *server) deleteDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "draft.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.Drafts)
		data.Drafts = slices.DeleteFunc(data.Drafts, func(item domain.Draft) bool { return item.ID == id && item.UserID == data.Viewer.ID })
		if before == len(data.Drafts) {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) deleteAllDrafts(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "drafts.deleted", "all", nil, func(data *domain.Bootstrap) error {
		viewerID := data.Viewer.ID
		data.Drafts = slices.DeleteFunc(data.Drafts, func(item domain.Draft) bool { return item.UserID == viewerID })
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func resourceExists(data *domain.Bootstrap, kind, id string) bool {
	switch kind {
	case "issue":
		return validateResourceIDs(data, "issue", []string{id})
	case "project":
		return validateResourceIDs(data, "project", []string{id})
	case "team":
		return slices.ContainsFunc(data.Teams, func(item domain.Team) bool { return item.ID == id && item.RetiredAt == nil })
	case "document":
		return slices.ContainsFunc(data.Documents, func(item domain.Document) bool { return item.ID == id })
	case "label":
		return slices.ContainsFunc(data.Labels, func(item domain.IssueLabel) bool { return item.ID == id && item.ArchivedAt == nil })
	case "cycle":
		return slices.ContainsFunc(data.Cycles, func(item domain.Cycle) bool { return item.ID == id })
	case "release":
		return slices.ContainsFunc(data.Releases, func(item domain.Release) bool { return item.ID == id })
	case "release_pipeline":
		return slices.ContainsFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == id })
	case "customer":
		return slices.ContainsFunc(data.Customers, func(item domain.Customer) bool { return item.ID == id })
	case "initiative":
		return slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id })
	case "review":
		return slices.ContainsFunc(data.Reviews, func(item domain.CodeReview) bool { return item.ID == id || item.SlugID == id })
	case "view":
		return slices.ContainsFunc(data.SavedViews, func(item domain.SavedView) bool { return item.ID == id })
	case "dashboard":
		return slices.ContainsFunc(settingCollection[domain.Dashboard](*data, dashboardsSettingsKey), func(item domain.Dashboard) bool { return item.ID == id })
	}
	return false
}

func setResourceFavoriteFlag(data *domain.Bootstrap, kind, id string, favorite bool) {
	switch kind {
	case "document":
		if index := slices.IndexFunc(data.Documents, func(item domain.Document) bool { return item.ID == id }); index >= 0 {
			data.Documents[index].Favorite = favorite
		}
	case "cycle":
		if index := slices.IndexFunc(data.Cycles, func(item domain.Cycle) bool { return item.ID == id }); index >= 0 {
			data.Cycles[index].Favorite = favorite
		}
	case "initiative":
		if index := slices.IndexFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id }); index >= 0 {
			data.Initiatives[index].Favorite = favorite
		}
	case "view":
		if index := slices.IndexFunc(data.SavedViews, func(item domain.SavedView) bool { return item.ID == id }); index >= 0 {
			data.SavedViews[index].Favorite = favorite
		}
	}
}

func removeResourcePreferences(data *domain.Bootstrap, kind, id string) {
	data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool {
		return item.ResourceType == kind && item.ResourceID == id
	})
	data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool {
		return item.ResourceType == kind && item.ResourceID == id
	})
}

func setFavoriteRecord(data *domain.Bootstrap, kind, id string, favorite bool) {
	index := slices.IndexFunc(data.Favorites, func(item domain.Favorite) bool {
		return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
	})
	if !favorite {
		if index >= 0 {
			data.Favorites = append(data.Favorites[:index], data.Favorites[index+1:]...)
		}
		return
	}
	if index >= 0 {
		return
	}
	now := time.Now().UTC()
	position := 0.0
	for _, item := range data.Favorites {
		if item.UserID == data.Viewer.ID && item.FolderID == "" && item.Position >= position {
			position = item.Position + 1
		}
	}
	data.Favorites = append(data.Favorites, domain.Favorite{ID: fmt.Sprintf("favorite_%d", now.UnixNano()), UserID: data.Viewer.ID, ResourceType: kind, ResourceID: id, Position: position, CreatedAt: now})
}

func (s *server) addFavorite(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	var created domain.Favorite
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "favorite.added", id, map[string]string{"type": kind}, func(data *domain.Bootstrap) error {
		if !resourceExists(data, kind, id) {
			return errNotFound
		}
		if kind == "document" {
			document, _ := documentByID(data, id)
			if document == nil || documentRole(s, *data, *document) == "none" {
				return store.ErrAuthForbidden
			}
		}
		setResourceFavoriteFlag(data, kind, id, true)
		index := slices.IndexFunc(data.Favorites, func(item domain.Favorite) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		if index >= 0 {
			created = data.Favorites[index]
			return nil
		}
		setFavoriteRecord(data, kind, id, true)
		index = slices.IndexFunc(data.Favorites, func(item domain.Favorite) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		if index >= 0 {
			created = data.Favorites[index]
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, created)
}

func (s *server) removeFavorite(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "favorite.removed", id, map[string]string{"type": kind}, func(data *domain.Bootstrap) error {
		if kind == "document" {
			document, _ := documentByID(data, id)
			if document == nil || documentRole(s, *data, *document) == "none" {
				return store.ErrAuthForbidden
			}
		}
		setResourceFavoriteFlag(data, kind, id, false)
		data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

type favoriteUpdateInput struct {
	FolderID *string  `json:"folderId,omitempty"`
	Position *float64 `json:"position,omitempty"`
}

func (s *server) updateFavorite(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	var input favoriteUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Position != nil && (math.IsNaN(*input.Position) || math.IsInf(*input.Position, 0)) {
		writeError(w, http.StatusBadRequest, "position must be finite")
		return
	}
	var updated domain.Favorite
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "favorite.updated", id, input, func(data *domain.Bootstrap) error {
		if kind == "document" {
			document, _ := documentByID(data, id)
			if document == nil || documentRole(s, *data, *document) == "none" {
				return store.ErrAuthForbidden
			}
		}
		index := slices.IndexFunc(data.Favorites, func(item domain.Favorite) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		if index < 0 {
			return errNotFound
		}
		if input.FolderID != nil {
			folderID := strings.TrimSpace(*input.FolderID)
			if folderID != "" && !slices.ContainsFunc(data.FavoriteFolders, func(folder domain.FavoriteFolder) bool {
				return folder.ID == folderID && folder.UserID == data.Viewer.ID
			}) {
				return errNotFound
			}
			data.Favorites[index].FolderID = folderID
		}
		if input.Position != nil {
			data.Favorites[index].Position = *input.Position
		}
		updated = data.Favorites[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

type favoriteFolderInput struct {
	Name     *string  `json:"name,omitempty"`
	Position *float64 `json:"position,omitempty"`
}

func (s *server) createFavoriteFolder(w http.ResponseWriter, r *http.Request) {
	var input favoriteFolderInput
	if !decodeJSON(w, r, &input) || input.Name == nil {
		return
	}
	name := strings.TrimSpace(*input.Name)
	if name == "" || len([]rune(name)) > 80 {
		writeError(w, http.StatusBadRequest, "folder name is required and must not exceed 80 characters")
		return
	}
	var created domain.FavoriteFolder
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "favorite_folder.created", "", input, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		position := 0.0
		for _, folder := range data.FavoriteFolders {
			if folder.UserID == data.Viewer.ID && folder.Position >= position {
				position = folder.Position + 1
			}
		}
		created = domain.FavoriteFolder{ID: fmt.Sprintf("favorite_folder_%d", now.UnixNano()), UserID: data.Viewer.ID, Name: name, Position: position, CreatedAt: now, UpdatedAt: now}
		data.FavoriteFolders = append(data.FavoriteFolders, created)
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateFavoriteFolder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input favoriteFolderInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Position != nil && (math.IsNaN(*input.Position) || math.IsInf(*input.Position, 0)) {
		writeError(w, http.StatusBadRequest, "position must be finite")
		return
	}
	var updated domain.FavoriteFolder
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "favorite_folder.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.FavoriteFolders, func(folder domain.FavoriteFolder) bool { return folder.ID == id && folder.UserID == data.Viewer.ID })
		if index < 0 {
			return errNotFound
		}
		if input.Name != nil {
			name := strings.TrimSpace(*input.Name)
			if name == "" || len([]rune(name)) > 80 {
				return fmt.Errorf("%w: folder name is required and must not exceed 80 characters", errInvalid)
			}
			data.FavoriteFolders[index].Name = name
		}
		if input.Position != nil {
			data.FavoriteFolders[index].Position = *input.Position
		}
		data.FavoriteFolders[index].UpdatedAt = time.Now().UTC()
		updated = data.FavoriteFolders[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteFavoriteFolder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "favorite_folder.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.FavoriteFolders, func(folder domain.FavoriteFolder) bool { return folder.ID == id && folder.UserID == data.Viewer.ID })
		if index < 0 {
			return errNotFound
		}
		position := 0.0
		for _, favorite := range data.Favorites {
			if favorite.UserID == data.Viewer.ID && favorite.FolderID == "" && favorite.Position >= position {
				position = favorite.Position + 1
			}
		}
		for favoriteIndex := range data.Favorites {
			if data.Favorites[favoriteIndex].UserID == data.Viewer.ID && data.Favorites[favoriteIndex].FolderID == id {
				data.Favorites[favoriteIndex].FolderID = ""
				data.Favorites[favoriteIndex].Position = position
				position++
			}
		}
		data.FavoriteFolders = append(data.FavoriteFolders[:index], data.FavoriteFolders[index+1:]...)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) addSubscription(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	var input domain.SubscriptionMutationInput
	if r.ContentLength > 0 && !decodeJSON(w, r, &input) {
		return
	}
	var created domain.Subscription
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "subscription.added", id, map[string]string{"type": kind}, func(data *domain.Bootstrap) error {
		if !resourceExists(data, kind, id) {
			return errNotFound
		}
		if kind == "document" {
			document, _ := documentByID(data, id)
			if document == nil || !canCommentDocument(documentRole(s, *data, *document)) {
				return store.ErrAuthForbidden
			}
		}
		setDocumentSubscription(data, kind, id, data.Viewer.ID, true)
		index := slices.IndexFunc(data.Subscriptions, func(item domain.Subscription) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		if index >= 0 {
			if input.Events != nil {
				data.Subscriptions[index].Events = slices.Clone(*input.Events)
			}
			created = data.Subscriptions[index]
			return nil
		}
		now := time.Now().UTC()
		created = domain.Subscription{ID: fmt.Sprintf("subscription_%d", now.UnixNano()), UserID: data.Viewer.ID, ResourceType: kind, ResourceID: id, CreatedAt: now}
		if input.Events != nil {
			created.Events = slices.Clone(*input.Events)
		}
		data.Subscriptions = append(data.Subscriptions, created)
		return nil
	})
	respondMutation(w, err, http.StatusOK, created)
}

func (s *server) removeSubscription(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "subscription.removed", id, map[string]string{"type": kind}, func(data *domain.Bootstrap) error {
		if kind == "document" {
			document, _ := documentByID(data, id)
			if document == nil || !canCommentDocument(documentRole(s, *data, *document)) {
				return store.ErrAuthForbidden
			}
		}
		setDocumentSubscription(data, kind, id, data.Viewer.ID, false)
		data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func setDocumentSubscription(data *domain.Bootstrap, kind, id, userID string, subscribed bool) {
	if kind != "document" {
		return
	}
	index := slices.IndexFunc(data.Documents, func(item domain.Document) bool { return item.ID == id })
	if index < 0 {
		return
	}
	if subscribed {
		if !slices.Contains(data.Documents[index].SubscriberIDs, userID) {
			data.Documents[index].SubscriberIDs = append(data.Documents[index].SubscriberIDs, userID)
		}
		return
	}
	data.Documents[index].SubscriberIDs = slices.DeleteFunc(data.Documents[index].SubscriberIDs, func(id string) bool { return id == userID })
}

func (s *server) restoreTrashEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var restored any
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "trash.restored", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Trash, func(item domain.TrashEntry) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		entry := data.Trash[index]
		switch entry.ResourceType {
		case "document":
			var value domain.Document
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			if !workspaceAdminRole(data.ViewerRole) && value.Creator.ID != data.Viewer.ID {
				return store.ErrAuthForbidden
			}
			data.Documents = append([]domain.Document{value}, data.Documents...)
			syncDocumentProjectResources(data, value)
			restored = value
		case "release":
			var value domain.Release
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			if slices.ContainsFunc(data.Releases, func(item domain.Release) bool { return item.ID == value.ID }) || value.PipelineID != "" && releasePipelineByID(data, value.PipelineID) == nil {
				return errConflict
			}
			data.Releases = append([]domain.Release{value}, data.Releases...)
			restored = value
		case "release_pipeline":
			var value domain.ReleasePipeline
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			if releasePipelineByID(data, value.ID) != nil {
				return errConflict
			}
			data.ReleasePipelines = append([]domain.ReleasePipeline{value}, data.ReleasePipelines...)
			restored = publicReleasePipeline(value)
		case "ask":
			var value domain.Ask
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			data.Asks = append([]domain.Ask{value}, data.Asks...)
			restored = value
		case "customer_request":
			var value domain.CustomerRequest
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			data.CustomerRequests = append([]domain.CustomerRequest{value}, data.CustomerRequests...)
			restored = value
		case "customer":
			var value domain.Customer
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			data.Customers = append([]domain.Customer{value}, data.Customers...)
			restored = value
		case "issue":
			var value deletedIssuePayload
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			data.Issues = append([]domain.Issue{value.Issue}, data.Issues...)
			data.Comments[value.Issue.ID], data.Activities[value.Issue.ID] = value.Comments, value.Activities
			restored = value.Issue
		case "project":
			var value deletedProjectPayload
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			data.Projects = append([]domain.Project{value.Project}, data.Projects...)
			data.ProjectUpdates[value.Project.ID] = value.Updates
			restored = value.Project
		case "initiative":
			var value deletedInitiativePayload
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			data.Initiatives = append([]domain.Initiative{value.Initiative}, data.Initiatives...)
			data.InitiativeUpdates[value.Initiative.ID] = value.Updates
			syncInitiativeProjects(data, value.Initiative.ID, nil, value.Initiative.ProjectIDs)
			restored = value.Initiative
		default:
			return errInvalid
		}
		data.Trash = slices.Delete(data.Trash, index, index+1)
		appendAudit(data, "restored", entry.ResourceType, entry.ResourceID, nil)
		return nil
	})
	if errors.Is(err, errConflict) {
		writeError(w, http.StatusConflict, "resource cannot be restored until its dependencies are available")
		return
	}
	respondMutation(w, err, http.StatusOK, restored)
}

func (s *server) purgeTrashEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "trash.purged", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.Trash)
		data.Trash = slices.DeleteFunc(data.Trash, func(item domain.TrashEntry) bool { return item.ID == id })
		if before == len(data.Trash) {
			return errNotFound
		}
		appendAudit(data, "purged", "trash", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func parseImportFile(filename string, reader io.Reader) (string, []string, []map[string]string, error) {
	if strings.HasSuffix(strings.ToLower(filename), ".json") {
		var raw []map[string]any
		if err := json.NewDecoder(io.LimitReader(reader, 20<<20)).Decode(&raw); err != nil {
			return "", nil, nil, err
		}
		headers := []string{}
		rows := make([]map[string]string, 0, len(raw))
		for _, item := range raw {
			row := map[string]string{}
			for key, value := range item {
				if !slices.Contains(headers, key) {
					headers = append(headers, key)
				}
				row[key] = fmt.Sprint(value)
			}
			rows = append(rows, row)
		}
		sort.Strings(headers)
		return "json", headers, rows, nil
	}
	records, err := csv.NewReader(io.LimitReader(reader, 20<<20)).ReadAll()
	if err != nil || len(records) == 0 {
		return "", nil, nil, errInvalid
	}
	headers := records[0]
	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		row := map[string]string{}
		for index, header := range headers {
			if index < len(record) {
				row[header] = record[index]
			}
		}
		rows = append(rows, row)
	}
	return "csv", headers, rows, nil
}

func (s *server) previewImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 21<<20)
	if err := r.ParseMultipartForm(21 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid import file")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	format, headers, rows, err := parseImportFile(header.Filename, file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not parse import file")
		return
	}
	if len(rows) > 5000 {
		writeError(w, http.StatusBadRequest, "imports are limited to 5000 rows")
		return
	}
	var created domain.ImportJob
	err = s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "import.previewed", map[string]any{"filename": header.Filename, "rows": len(rows)}, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.ImportJob{ID: fmt.Sprintf("import_%d", now.UnixNano()), UserID: data.Viewer.ID, Filename: header.Filename, Format: format, Status: "mapping", Headers: headers, Rows: rows, RowsTotal: len(rows), Checkpoint: 0, Mapping: map[string]string{}, Errors: []string{}, RowErrors: []domain.ImportRowError{}, CreatedAt: now, UpdatedAt: now}
		data.ImportJobs = append([]domain.ImportJob{created}, data.ImportJobs...)
		appendAudit(data, "previewed", "import", created.ID, map[string]any{"filename": created.Filename, "rows": len(rows)})
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

// commitImport acknowledges quickly and processes rows in a background worker.
// The worker uses a detached context so a browser closing the mapping dialog
// cannot abort a job that was already accepted.
func (s *server) commitImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid import request")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var input struct {
		Mapping map[string]string `json:"mapping"`
		TeamID  string            `json:"teamId"`
	}
	if json.Unmarshal(body, &input) != nil || input.Mapping["title"] == "" || strings.TrimSpace(input.TeamID) == "" {
		writeError(w, http.StatusBadRequest, "title mapping and teamId are required")
		return
	}
	id := r.PathValue("id")
	var job domain.ImportJob
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "import.queued", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ImportJobs, func(item domain.ImportJob) bool { return item.ID == id && item.UserID == data.Viewer.ID })
		if index < 0 {
			return errNotFound
		}
		if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == input.TeamID }) {
			return errInvalid
		}
		if data.ImportJobs[index].Status == "completed" || data.ImportJobs[index].Status == "running" {
			return errInvalid
		}
		data.ImportJobs[index].Status = "running"
		data.ImportJobs[index].Mapping = input.Mapping
		data.ImportJobs[index].TeamID = input.TeamID
		data.ImportJobs[index].Progress = 0
		data.ImportJobs[index].Error = ""
		data.ImportJobs[index].UpdatedAt = time.Now().UTC()
		job = data.ImportJobs[index]
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusBadRequest, nil)
		return
	}
	go func() {
		request := r.Clone(context.Background())
		request.Body = io.NopCloser(bytes.NewReader(body))
		request = request.WithContext(context.Background())
		s.commitImportSync(httptest.NewRecorder(), request)
	}()
	writeJSON(w, http.StatusAccepted, job)
}

func (s *server) commitImportSync(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Mapping map[string]string `json:"mapping"`
		TeamID  string            `json:"teamId"`
	}
	if !decodeJSON(w, r, &input) || input.Mapping["title"] == "" {
		writeError(w, http.StatusBadRequest, "title mapping is required")
		return
	}
	id := r.PathValue("id")
	var updated domain.ImportJob
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "import.committed", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ImportJobs, func(item domain.ImportJob) bool { return item.ID == id && item.UserID == data.Viewer.ID })
		if index < 0 {
			return errNotFound
		}
		job := &data.ImportJobs[index]
		teamIndex := slices.IndexFunc(data.Teams, func(item domain.Team) bool { return item.ID == input.TeamID })
		if teamIndex < 0 {
			return errInvalid
		}
		team := data.Teams[teamIndex]
		settings := teamSettings(data, team.ID)
		state := stateForTeam(data, team.ID, settings.DefaultStateID)
		if state == nil {
			values := statesForTeam(data, team.ID)
			if len(values) == 0 {
				return errInvalid
			}
			state = &values[0]
		}
		// A commit is idempotent at the job level: completed jobs must be
		// explicitly reset through /retry before they can be replayed. This
		// prevents an accidental double-click from creating duplicate issues.
		if job.Status == "completed" {
			return errInvalid
		}
		job.Status, job.Mapping, job.Errors, job.RowErrors, job.TeamID = "running", input.Mapping, []string{}, []domain.ImportRowError{}, input.TeamID
		job.Imported, job.Progress, job.Error = 0, 0, ""
		job.Checkpoint = 0
		sourceToIssueID, pendingParents := map[string]string{}, map[string]string{}
		nextNumber := nextIssueNumber(data.Issues)
		newIssues := make([]domain.Issue, 0, len(job.Rows))
		for _, existing := range data.Issues {
			if existing.ExternalSource != "" {
				sourceToIssueID[strings.TrimPrefix(existing.ExternalSource, "csv:")] = existing.ID
			}
		}
		for rowIndex, row := range job.Rows {
			title := strings.TrimSpace(row[input.Mapping["title"]])
			if title == "" {
				job.Errors = append(job.Errors, fmt.Sprintf("Row %d: title is empty", rowIndex+2))
				continue
			}
			sourceID := strings.TrimSpace(row[input.Mapping["sourceId"]])
			if sourceID != "" {
				if _, exists := sourceToIssueID[sourceID]; exists {
					job.Errors = append(job.Errors, fmt.Sprintf("Row %d: source issue %q was already imported", rowIndex+2, sourceID))
					continue
				}
			}
			number := nextNumber
			nextNumber++
			now := time.Now().UTC()
			createdAt := importedTime(row[input.Mapping["createdAt"]], now)
			updatedAt := importedTime(row[input.Mapping["updatedAt"]], createdAt)
			startedAt, triagedAt := importedOptionalTime(row[input.Mapping["startedAt"]]), importedOptionalTime(row[input.Mapping["triagedAt"]])
			completedAt, canceledAt := importedOptionalTime(row[input.Mapping["completedAt"]]), importedOptionalTime(row[input.Mapping["canceledAt"]])
			issueState := *state
			if value := strings.TrimSpace(row[input.Mapping["status"]]); value != "" {
				if matched := slices.IndexFunc(statesForTeam(data, team.ID), func(item domain.WorkflowState) bool { return item.ID == value || strings.EqualFold(item.Name, value) }); matched >= 0 {
					issueState = statesForTeam(data, team.ID)[matched]
				} else {
					stateType := "unstarted"
					if completedAt != nil {
						stateType = "completed"
					} else if canceledAt != nil {
						stateType = "canceled"
					} else if startedAt != nil {
						stateType = "started"
					}
					issueState = domain.WorkflowState{ID: fmt.Sprintf("state_import_%d", time.Now().UnixNano()), TeamID: team.ID, Name: value, Color: map[string]string{"completed": "#5e6ad2", "canceled": "#95a2b3", "started": "#f2c94c", "unstarted": "#bec2c8"}[stateType], Type: stateType, Position: float64(len(statesForTeam(data, team.ID)) + 1)}
					data.States = append(data.States, issueState)
				}
			}
			assignee := &data.Viewer
			if value := strings.TrimSpace(row[input.Mapping["assignee"]]); value != "" {
				if matched := slices.IndexFunc(data.Users, func(item domain.User) bool {
					return item.ID == value || strings.EqualFold(item.Email, value) || strings.EqualFold(item.Name, value) || strings.EqualFold(item.DisplayName, value)
				}); matched >= 0 {
					assignee = &data.Users[matched]
				} else {
					job.Errors = append(job.Errors, fmt.Sprintf("Row %d: assignee %q was not found; used %s", rowIndex+2, value, data.Viewer.DisplayName))
				}
			}
			issue := domain.Issue{ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number, Title: stripSpreadsheetQuote(title), Description: stripSpreadsheetQuote(row[input.Mapping["description"]]), Priority: settings.DefaultPriority, PriorityLabel: priorityLabel(settings.DefaultPriority), SortOrder: float64(number), CreatedAt: createdAt, UpdatedAt: updatedAt, StartedAt: startedAt, TriagedAt: triagedAt, CompletedAt: completedAt, CanceledAt: canceledAt, Team: team, State: issueState, Assignee: assignee, Creator: data.Viewer, Labels: []domain.IssueLabel{}, SubscriberIDs: []string{data.Viewer.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
			if sourceID != "" {
				issue.ExternalSource = "csv:" + sourceID
			}
			if value := strings.TrimSpace(row[input.Mapping["estimate"]]); value != "" {
				if parsed, parseErr := strconv.ParseFloat(value, 64); parseErr == nil {
					issue.Estimate = &parsed
				} else {
					job.Errors = append(job.Errors, fmt.Sprintf("Row %d: estimate %q was not recognized", rowIndex+2, value))
				}
			}
			if value := strings.TrimSpace(row[input.Mapping["archivedAt"]]); value != "" {
				if parsed := importedOptionalTime(value); parsed != nil {
					issue.ArchivedAt = parsed
				} else {
					archivedAt := now
					issue.ArchivedAt = &archivedAt
				}
			}
			if value := row[input.Mapping["priority"]]; value != "" {
				if parsed, ok := importedPriority(value); ok {
					issue.Priority, issue.PriorityLabel = parsed, priorityLabel(parsed)
				} else {
					job.Errors = append(job.Errors, fmt.Sprintf("Row %d: priority %q was not recognized; used %s", rowIndex+2, value, issue.PriorityLabel))
				}
			}
			if value := strings.TrimSpace(row[input.Mapping["project"]]); value != "" {
				if matched := slices.IndexFunc(data.Projects, func(item domain.Project) bool {
					return item.ID == value || item.SlugID == value || strings.EqualFold(item.Name, value)
				}); matched >= 0 {
					project := data.Projects[matched]
					issue.Project = &domain.ProjectSummary{ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color}
				} else {
					job.Errors = append(job.Errors, fmt.Sprintf("Row %d: project %q was not found", rowIndex+2, value))
				}
			}
			if value := strings.TrimSpace(row[input.Mapping["labels"]]); value != "" {
				for _, labelValue := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
					labelValue = strings.TrimSpace(labelValue)
					if matched := slices.IndexFunc(data.Labels, func(item domain.IssueLabel) bool {
						return item.ID == labelValue || strings.EqualFold(item.Name, labelValue)
					}); matched >= 0 && !slices.ContainsFunc(issue.Labels, func(item domain.IssueLabel) bool { return item.ID == data.Labels[matched].ID }) {
						issue.Labels = append(issue.Labels, data.Labels[matched])
					} else if matched < 0 {
						groupID, labelName := "", labelValue
						if separator := strings.LastIndex(labelValue, "/"); separator > 0 && separator < len(labelValue)-1 {
							groupName := strings.TrimSpace(labelValue[:separator])
							labelName = strings.TrimSpace(labelValue[separator+1:])
							if groupIndex := slices.IndexFunc(data.LabelGroups, func(item domain.LabelGroup) bool {
								return item.ResourceType == "issue" && strings.EqualFold(item.Name, groupName)
							}); groupIndex >= 0 {
								groupID = data.LabelGroups[groupIndex].ID
							} else {
								groupID = fmt.Sprintf("label_group_import_%d", time.Now().UnixNano())
								data.LabelGroups = append(data.LabelGroups, domain.LabelGroup{ID: groupID, Name: groupName, Color: "#5e6ad2", Scope: team.ID, ResourceType: "issue", CreatedAt: now})
							}
						}
						createdLabel := domain.IssueLabel{ID: fmt.Sprintf("label_import_%d", time.Now().UnixNano()), Name: labelName, Color: "#5e6ad2", Scope: team.ID, ResourceType: "issue", GroupID: groupID, CreatorID: data.Viewer.ID, CreatedAt: now}
						data.Labels = append(data.Labels, createdLabel)
						issue.Labels = append(issue.Labels, createdLabel)
					}
				}
			}
			if value := strings.TrimSpace(row[input.Mapping["dueDate"]]); value != "" {
				if _, err := time.Parse("2006-01-02", value); err == nil {
					issue.DueDate = &value
				} else {
					job.Errors = append(job.Errors, fmt.Sprintf("Row %d: due date %q must use YYYY-MM-DD", rowIndex+2, value))
				}
			}
			if sourceID != "" {
				sourceToIssueID[sourceID] = issue.ID
			}
			if parentSource := strings.TrimSpace(row[input.Mapping["parentId"]]); parentSource != "" {
				pendingParents[issue.ID] = parentSource
			}
			appendActivity(data, issue.ID, "issue.imported", data.Viewer, map[string]string{"importId": id})
			applySLARules(data, &issue, now)
			newIssues = append(newIssues, issue)
			job.Imported++
			job.Progress = int(float64(rowIndex+1) / float64(max(1, len(job.Rows))) * 100)
		}
		// Preserve the previous newest-first import ordering, but grow the issue
		// slice once instead of copying the complete workspace for every row.
		slices.Reverse(newIssues)
		data.Issues = append(newIssues, data.Issues...)
		issueIndexByID := make(map[string]int, len(data.Issues))
		for issueIndex := range data.Issues {
			issueIndexByID[data.Issues[issueIndex].ID] = issueIndex
		}
		for issueID, parentSource := range pendingParents {
			parentID := sourceToIssueID[parentSource]
			if parentID == "" {
				job.Errors = append(job.Errors, fmt.Sprintf("Parent issue %q was not found", parentSource))
				continue
			}
			if issueIndex, ok := issueIndexByID[issueID]; ok {
				data.Issues[issueIndex].ParentID = &parentID
			}
			if parentIndex, ok := issueIndexByID[parentID]; ok && !slices.Contains(data.Issues[parentIndex].SubIssueIDs, issueID) {
				data.Issues[parentIndex].SubIssueIDs = append(data.Issues[parentIndex].SubIssueIDs, issueID)
			}
		}
		job.Status = "completed"
		if job.Imported == 0 && len(job.Errors) > 0 {
			job.Status = "failed"
			job.Error = "no rows could be imported"
		}
		job.Progress = 100
		job.Checkpoint = len(job.Rows)
		job.RowsTotal = len(job.Rows)
		job.RowErrors = make([]domain.ImportRowError, 0, len(job.Errors))
		for _, message := range job.Errors {
			var row int
			if _, scanErr := fmt.Sscanf(message, "Row %d:", &row); scanErr == nil && row > 0 {
				job.RowErrors = append(job.RowErrors, domain.ImportRowError{Row: row, Message: message})
			}
		}
		job.UpdatedAt = time.Now().UTC()
		if job.Status == "completed" {
			job.Rows = nil
		}
		updated = *job
		appendAudit(data, "completed", "import", id, map[string]any{"imported": job.Imported, "errors": len(job.Errors)})
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func importedPriority(value string) (int, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 && parsed <= 4 {
		return parsed, true
	}
	priorities := map[string]int{"no priority": 0, "none": 0, "urgent": 1, "high": 2, "medium": 3, "low": 4}
	priority, ok := priorities[value]
	return priority, ok
}

func importedOptionalTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

func importedTime(value string, fallback time.Time) time.Time {
	if parsed := importedOptionalTime(value); parsed != nil {
		return *parsed
	}
	return fallback
}

func stripSpreadsheetQuote(value string) string {
	runes := []rune(value)
	if len(runes) > 1 && runes[0] == '\'' && strings.ContainsRune("+-=@∑√∏<>＜＞≤≥＝≠±÷×", runes[1]) {
		return string(runes[1:])
	}
	return value
}

func (s *server) createExport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Format         string `json:"format"`
		IncludePrivate bool   `json:"includePrivate"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Format == "" {
		input.Format = "json"
	}
	if !slices.Contains([]string{"json", "csv"}, input.Format) {
		writeError(w, http.StatusBadRequest, "format must be json or csv")
		return
	}
	if input.IncludePrivate && !s.authDisabled && !workspaceAdminRole(s.workspaceData(r).ViewerRole) {
		writeError(w, http.StatusForbidden, "Only workspace admins can export private data")
		return
	}
	var created domain.ExportJob
	key := workspaceKey(r)
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), key, "export.queued", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.ExportJob{ID: fmt.Sprintf("export_%d", now.UnixNano()), UserID: data.Viewer.ID, Format: input.Format, IncludePrivate: input.IncludePrivate, Status: "queued", CreatedAt: now}
		data.ExportJobs = append([]domain.ExportJob{created}, data.ExportJobs...)
		appendAudit(data, "queued", "export", created.ID, map[string]any{"format": input.Format})
		return created.ID, nil
	})
	if err == nil {
		go s.completeExport(key, created.ID)
	}
	respondMutation(w, err, http.StatusAccepted, created)
}

func (s *server) completeExport(workspaceKey, id string) {
	time.Sleep(100 * time.Millisecond)
	_ = s.store.MutateWorkspace(context.Background(), workspaceKey, "export.completed", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ExportJobs, func(item domain.ExportJob) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		data.ExportJobs[index].Status, data.ExportJobs[index].CompletedAt = "completed", &now
		suffix := data.ExportJobs[index].Format
		data.ExportJobs[index].Filename = data.Workspace.URLKey + "-export-" + now.Format("2006-01-02") + "." + suffix
		appendAudit(data, "completed", "export", id, nil)
		return nil
	})
}

func (s *server) downloadExport(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id := r.PathValue("id")
	index := slices.IndexFunc(data.ExportJobs, func(item domain.ExportJob) bool {
		return item.ID == id && (s.authDisabled || item.UserID == authUser(r).ID)
	})
	if index < 0 {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	job := data.ExportJobs[index]
	if job.Status != "completed" {
		writeError(w, http.StatusConflict, "export is still being prepared")
		return
	}
	filename := job.Filename
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if job.Format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		writer := csv.NewWriter(w)
		_ = writer.Write([]string{"ID", "Team", "Title", "Description", "Status", "Estimate", "Priority", "Project ID", "Project", "Creator", "Assignee", "Labels", "Cycle Number", "Cycle Name", "Cycle Start", "Cycle End", "Created", "Updated", "Started", "Triaged", "Completed", "Canceled", "Archived", "Due Date", "Parent issue", "Initiatives", "Project Milestone ID", "Project Milestone", "SLA Status"})
		for _, issue := range data.Issues {
			assignee, projectID, projectName := "", "", ""
			if issue.Assignee != nil {
				assignee = issue.Assignee.DisplayName
			}
			if issue.Project != nil {
				projectID, projectName = issue.Project.ID, issue.Project.Name
			}
			estimate := ""
			if issue.Estimate != nil {
				estimate = strconv.FormatFloat(*issue.Estimate, 'f', -1, 64)
			}
			labels := make([]string, 0, len(issue.Labels))
			for _, label := range issue.Labels {
				labels = append(labels, label.Name)
			}
			cycleNumber, cycleName, cycleStart, cycleEnd := "", "", "", ""
			if issue.CycleID != nil {
				if cycleIndex := slices.IndexFunc(data.Cycles, func(item domain.Cycle) bool { return item.ID == *issue.CycleID }); cycleIndex >= 0 {
					cycle := data.Cycles[cycleIndex]
					cycleNumber, cycleName = strconv.Itoa(cycle.Number), cycle.Name
					cycleStart, cycleEnd = cycle.StartsAt.Format(time.RFC3339), cycle.EndsAt.Format(time.RFC3339)
				}
			}
			parent := ""
			if issue.ParentID != nil {
				if parentIndex := slices.IndexFunc(data.Issues, func(item domain.Issue) bool { return item.ID == *issue.ParentID }); parentIndex >= 0 {
					parent = data.Issues[parentIndex].Identifier
				}
			}
			milestoneID, milestoneName, initiativeNames := "", "", []string{}
			if issue.ProjectMilestoneID != nil {
				milestoneID = *issue.ProjectMilestoneID
			}
			if projectID != "" {
				if projectIndex := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == projectID }); projectIndex >= 0 {
					project := data.Projects[projectIndex]
					if milestoneID != "" {
						if milestoneIndex := slices.IndexFunc(project.Milestones, func(item domain.ProjectMilestone) bool { return item.ID == milestoneID }); milestoneIndex >= 0 {
							milestoneName = project.Milestones[milestoneIndex].Name
						}
					}
					for _, initiativeID := range project.Initiatives {
						if initiativeIndex := slices.IndexFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == initiativeID }); initiativeIndex >= 0 {
							initiativeNames = append(initiativeNames, data.Initiatives[initiativeIndex].Name)
						}
					}
				}
			}
			slaStatus := ""
			if slaIndex := slices.IndexFunc(data.IssueSLAs, func(item domain.IssueSLA) bool { return item.IssueID == issue.ID }); slaIndex >= 0 {
				slaStatus = data.IssueSLAs[slaIndex].Status
			}
			_ = writer.Write([]string{issue.Identifier, issue.Team.Name, csvText(issue.Title), csvText(issue.Description), issue.State.Name, estimate, issue.PriorityLabel, projectID, projectName, issue.Creator.DisplayName, assignee, strings.Join(labels, ", "), cycleNumber, cycleName, cycleStart, cycleEnd, formatExportTime(&issue.CreatedAt), formatExportTime(&issue.UpdatedAt), formatExportTime(issue.StartedAt), formatExportTime(issue.TriagedAt), formatExportTime(issue.CompletedAt), formatExportTime(issue.CanceledAt), formatExportTime(issue.ArchivedAt), stringValue(issue.DueDate), parent, strings.Join(initiativeNames, ", "), milestoneID, milestoneName, slaStatus})
		}
		writer.Flush()
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	packageData := map[string]any{"workspace": data.Workspace, "teams": data.Teams, "users": data.Users, "issues": data.Issues, "projects": data.Projects, "documents": data.Documents, "customers": data.Customers, "customerRequests": data.CustomerRequests, "releases": data.Releases, "asks": data.Asks, "comments": data.Comments, "activities": data.Activities, "labels": data.Labels, "workflowStates": data.States, "cycles": data.Cycles, "templates": map[string]any{"issues": data.IssueTemplates, "projects": data.ProjectTemplates}, "exportedAt": time.Now().UTC()}
	_ = json.NewEncoder(w).Encode(packageData)
}

func formatExportTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func csvText(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("+-=@∑√∏<>＜＞≤≥＝≠±÷×", []rune(trimmed)[0]) {
		return "'" + value
	}
	return value
}

func (s *server) maintainAdvancedSchedules(ctx context.Context, key string) {
	data, ok := s.store.BootstrapFor(key)
	if !ok {
		return
	}
	now := time.Now().UTC()
	needsMutation := slices.ContainsFunc(data.IssueSLAs, func(item domain.IssueSLA) bool { return item.Status == "active" && now.After(item.DueAt) })
	needsMutation = needsMutation || slices.ContainsFunc(data.Trash, func(item domain.TrashEntry) bool { return now.After(item.ExpiresAt) })
	settings, _ := data.Settings["projectUpdates"].(map[string]any)
	defaultCadence := intFromAny(settings["cadenceDays"])
	leadReminders := boolFromAny(settings["reminders"])
	missingNotifications := settings["missingNotifications"] == nil || boolFromAny(settings["missingNotifications"])
	for _, project := range data.Projects {
		cadence := projectCadenceDays(project.UpdateCadence, defaultCadence)
		if cadence > 0 {
			updates := data.ProjectUpdates[project.ID]
			reference := project.CreatedAt
			if len(updates) > 0 && updates[0].CreatedAt.After(reference) {
				reference = updates[0].CreatedAt
			}
			dueAt := reference.AddDate(0, 0, cadence)
			missing := now.After(dueAt)
			dueSoon := leadReminders && !missing && now.After(dueAt.Add(-24*time.Hour))
			latestOutdated := len(updates) > 0 && (updates[0].DueAt == nil || !updates[0].DueAt.Equal(dueAt) || updates[0].Missing != missing)
			activeMissingReminder := slices.ContainsFunc(data.Notifications, func(item domain.Notification) bool {
				return item.ProjectID == project.ID && item.Type == "projectUpdateReminder" && item.ArchivedAt == nil && item.DeletedAt == nil
			})
			activeDueReminder := slices.ContainsFunc(data.Notifications, func(item domain.Notification) bool {
				return item.ProjectID == project.ID && item.Type == "projectUpdateDueReminder" && item.ArchivedAt == nil && item.DeletedAt == nil
			})
			if latestOutdated || (missing && (project.Health != "noUpdate" || (missingNotifications && !activeMissingReminder))) || (!missing && activeMissingReminder) || dueSoon != activeDueReminder {
				needsMutation = true
				break
			}
		}
	}
	if !needsMutation {
		return
	}
	_ = s.store.MutateWorkspace(ctx, key, "schedules.maintained", "advanced_schedules", nil, func(next *domain.Bootstrap) error {
		for index := range next.Issues {
			applySLARules(next, &next.Issues[index], now)
		}
		for projectIndex := range next.Projects {
			project := &next.Projects[projectIndex]
			cadence := projectCadenceDays(project.UpdateCadence, defaultCadence)
			if cadence <= 0 {
				continue
			}
			updates := next.ProjectUpdates[project.ID]
			reference := project.CreatedAt
			if len(updates) > 0 && updates[0].CreatedAt.After(reference) {
				reference = updates[0].CreatedAt
			}
			dueAt := reference.AddDate(0, 0, cadence)
			missing := now.After(dueAt)
			dueSoon := leadReminders && !missing && now.After(dueAt.Add(-24*time.Hour))
			if len(updates) > 0 {
				updates[0].DueAt, updates[0].Missing = &dueAt, missing
				next.ProjectUpdates[project.ID] = updates
			}
			if missing {
				project.Health = "noUpdate"
				archiveProjectUpdateReminders(next, project.ID, "projectUpdateDueReminder", now)
				if missingNotifications {
					appendProjectUpdateReminders(next, *project, dueAt, now, false)
				}
			} else {
				archiveProjectUpdateReminders(next, project.ID, "projectUpdateReminder", now)
				if dueSoon {
					appendProjectUpdateReminders(next, *project, dueAt, now, true)
				} else {
					archiveProjectUpdateReminders(next, project.ID, "projectUpdateDueReminder", now)
				}
			}
		}
		next.Trash = slices.DeleteFunc(next.Trash, func(item domain.TrashEntry) bool { return now.After(item.ExpiresAt) })
		return nil
	})
}

func projectCadenceDays(value string, fallback int) int {
	switch value {
	case "none":
		return 0
	case "weekly":
		return 7
	case "biweekly":
		return 14
	case "monthly":
		return 30
	default:
		return fallback
	}
}

func appendProjectUpdateReminders(data *domain.Bootstrap, project domain.Project, dueAt, now time.Time, leadOnly bool) {
	recipients := []string{}
	if !leadOnly {
		recipients = append(recipients, project.MemberIDs...)
	}
	if project.Lead != nil {
		recipients = appendUnique(recipients, project.Lead.ID)
	}
	if len(recipients) == 0 {
		recipients = []string{data.Viewer.ID}
	}
	for _, recipientID := range recipients {
		if recipientID == "" || userByID(data, recipientID) == nil {
			continue
		}
		kind, keyPart := "projectUpdateReminder", "update-missing"
		if leadOnly {
			kind, keyPart = "projectUpdateDueReminder", "update-due"
		}
		groupKey := fmt.Sprintf("%s:project:%s:%s:%d", recipientID, project.ID, keyPart, dueAt.Unix())
		if slices.ContainsFunc(data.Notifications, func(item domain.Notification) bool { return item.GroupKey == groupKey && item.DeletedAt == nil }) {
			continue
		}
		preferences, ok := data.NotificationPreferences[recipientID]
		if !ok {
			preferences = defaultPreferences(recipientID)
		}
		if !preferences.Inbox.Enabled || !categoryEnabled(preferences.Inbox, "reminders") {
			continue
		}
		notification := domain.Notification{
			ID: "notification_project_update_" + keyPart + "_" + project.ID + "_" + recipientID + "_" + strconv.FormatInt(dueAt.Unix(), 10), RecipientID: recipientID,
			Type: kind, SourceType: "project", SourceID: project.ID, ProjectID: project.ID, Actor: data.Viewer,
			Category: "reminders", GroupKey: groupKey, OccurrenceCount: 1, LatestActorIDs: []string{data.Viewer.ID}, CreatedAt: now, UpdatedAt: now,
		}
		data.Notifications = append(data.Notifications, notification)
		enqueueNotificationDeliveries(data, notification, preferences)
	}
}

func archiveProjectUpdateReminders(data *domain.Bootstrap, projectID, kind string, now time.Time) {
	for index := range data.Notifications {
		item := &data.Notifications[index]
		if item.ProjectID == projectID && item.Type == kind && item.ArchivedAt == nil {
			item.ArchivedAt, item.ReadAt, item.UpdatedAt = &now, &now, now
		}
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	}
	return 0
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		result, _ := strconv.ParseBool(typed)
		return result
	case float64:
		return typed != 0
	case int:
		return typed != 0
	}
	return false
}
