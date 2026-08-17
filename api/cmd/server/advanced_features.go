package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type documentInput struct {
	Title         *string        `json:"title,omitempty"`
	Icon          *string        `json:"icon,omitempty"`
	Content       *string        `json:"content,omitempty"`
	ContentState  *string        `json:"contentState,omitempty"`
	ContentData   map[string]any `json:"contentData,omitempty"`
	ProjectIDs    *[]string      `json:"projectIds,omitempty"`
	TeamIDs       *[]string      `json:"teamIds,omitempty"`
	SubscriberIDs *[]string      `json:"subscriberIds,omitempty"`
	Favorite      *bool          `json:"favorite,omitempty"`
	Archived      *bool          `json:"archived,omitempty"`
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
	Name          *string   `json:"name,omitempty"`
	Version       *string   `json:"version,omitempty"`
	Description   *string   `json:"description,omitempty"`
	Status        *string   `json:"status,omitempty"`
	TargetDate    *string   `json:"targetDate,omitempty"`
	ProjectIDs    *[]string `json:"projectIds,omitempty"`
	IssueIDs      *[]string `json:"issueIds,omitempty"`
	SubscriberIDs *[]string `json:"subscriberIds,omitempty"`
	Archived      *bool     `json:"archived,omitempty"`
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
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Summary     *string   `json:"summary,omitempty"`
	Icon        *string   `json:"icon,omitempty"`
	Color       *string   `json:"color,omitempty"`
	StatusID    *string   `json:"statusId,omitempty"`
	Priority    *int      `json:"priority,omitempty"`
	TeamIDs     *[]string `json:"teamIds,omitempty"`
	LabelIDs    *[]string `json:"labelIds,omitempty"`
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
	data.Trash = append([]domain.TrashEntry{{ID: fmt.Sprintf("trash_%d", now.UnixNano()), ResourceType: resourceType, ResourceID: resourceID, Title: title, Payload: payload, DeletedBy: data.Viewer, DeletedAt: now, ExpiresAt: now.AddDate(0, 0, 30)}}, data.Trash...)
	appendAudit(data, "deleted", resourceType, resourceID, map[string]any{"title": title})
	return nil
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
			valid = slices.ContainsFunc(data.Labels, func(item domain.IssueLabel) bool { return item.ID == id })
		case "user":
			valid = userByID(data, id) != nil
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
		project.Resources = append(project.Resources, domain.ProjectResource{ID: document.ID, ProjectID: project.ID, Type: "document", Title: document.Title, URL: url, CreatedAt: document.CreatedAt})
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
		if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
			title = strings.TrimSpace(*input.Title)
		}
		projects, teams := []string{}, []string{}
		if input.ProjectIDs != nil {
			projects = normalizedStrings(*input.ProjectIDs)
		}
		if input.TeamIDs != nil {
			teams = normalizedStrings(*input.TeamIDs)
		}
		if !validateResourceIDs(data, "project", projects) || !validateResourceIDs(data, "team", teams) {
			return "", errInvalid
		}
		created = domain.Document{ID: fmt.Sprintf("document_%d", now.UnixNano()), SlugID: slug(title) + "-" + strconv.FormatInt(now.UnixNano()%0xffffff, 16), Title: title, Creator: data.Viewer, ProjectIDs: projects, TeamIDs: teams, SubscriberIDs: []string{data.Viewer.ID}, ContentData: map[string]any{"type": "doc", "content": []any{}}, CreatedAt: now, UpdatedAt: now, Revisions: []domain.DocumentRevision{}}
		if input.Icon != nil {
			created.Icon = *input.Icon
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
		if input.SubscriberIDs != nil {
			if !validateResourceIDs(data, "user", *input.SubscriberIDs) {
				return errInvalid
			}
			document.SubscriberIDs = normalizedStrings(*input.SubscriberIDs)
		}
		if input.Favorite != nil {
			document.Favorite = *input.Favorite
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
		removed := data.Documents[index]
		if err := appendTrash(data, "document", removed.ID, removed.Title, removed); err != nil {
			return err
		}
		data.Documents = slices.Delete(data.Documents, index, index+1)
		for projectIndex := range data.Projects {
			data.Projects[projectIndex].Resources = slices.DeleteFunc(data.Projects[projectIndex].Resources, func(item domain.ProjectResource) bool { return item.ID == removed.ID })
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
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
	diskPath := filepath.Join(s.uploadPath, safeName)
	out, err := os.Create(diskPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not store attachment")
		return
	}
	size, copyErr := io.Copy(out, io.LimitReader(file, 20<<20))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || size >= 20<<20 {
		_ = os.Remove(diskPath)
		if size >= 20<<20 {
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
		_ = os.Remove(diskPath)
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}

func (s *server) deleteCustomerRequestAttachment(w http.ResponseWriter, r *http.Request) {
	requestID, attachmentID := r.PathValue("id"), r.PathValue("attachmentId")
	var diskPath string
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
		diskPath = filepath.Join(s.uploadPath, filepath.Base(request.Attachments[attachmentIndex].URL))
		request.Attachments = slices.Delete(request.Attachments, attachmentIndex, attachmentIndex+1)
		request.UpdatedAt = time.Now().UTC()
		appendAudit(data, "attachment_deleted", "customer_request", requestID, map[string]any{"attachmentId": attachmentID})
		return nil
	})
	if err == nil && diskPath != "" {
		_ = os.Remove(diskPath)
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
	if input.Status != nil {
		if !slices.Contains([]string{"planned", "inProgress", "released", "canceled"}, *input.Status) {
			return errInvalid
		}
		release.Status = *input.Status
	}
	if input.TargetDate != nil {
		if *input.TargetDate == "" {
			release.TargetDate = nil
		} else {
			release.TargetDate = input.TargetDate
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
		release.IssueIDs = values
	}
	if input.SubscriberIDs != nil {
		values := normalizedStrings(*input.SubscriberIDs)
		if !validateResourceIDs(data, "user", values) {
			return errInvalid
		}
		release.SubscriberIDs = values
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
		created = domain.Release{ID: fmt.Sprintf("release_%d", now.UnixNano()), Name: strings.TrimSpace(*input.Name), Status: "planned", ProjectIDs: []string{}, IssueIDs: []string{}, SubscriberIDs: []string{data.Viewer.ID}, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
		if err := applyReleaseInput(data, &created, input); err != nil {
			return "", err
		}
		data.Releases = append([]domain.Release{created}, data.Releases...)
		appendAudit(data, "created", "release", created.ID, map[string]any{"name": created.Name})
		return created.ID, nil
	})
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
		if err := applyReleaseInput(data, &data.Releases[index], input); err != nil {
			return err
		}
		data.Releases[index].UpdatedAt = time.Now().UTC()
		updated = data.Releases[index]
		appendAudit(data, "updated", "release", id, nil)
		return nil
	})
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
		if !validateResourceIDs(data, "label", values) {
			return errInvalid
		}
		template.LabelIDs = values
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

func (s *server) createDraft(w http.ResponseWriter, r *http.Request) {
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
		if input.ResourceID != nil {
			created.ResourceID = *input.ResourceID
		}
		if input.Title != nil {
			created.Title = *input.Title
		}
		if input.Body != nil {
			created.Body = *input.Body
		}
		data.Drafts = append([]domain.Draft{created}, data.Drafts...)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateDraft(w http.ResponseWriter, r *http.Request) {
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
		item := &data.Drafts[index]
		if input.Type != nil {
			item.Type = *input.Type
		}
		if input.ResourceID != nil {
			item.ResourceID = *input.ResourceID
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
		item.UpdatedAt = time.Now().UTC()
		updated = *item
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
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

func resourceExists(data *domain.Bootstrap, kind, id string) bool {
	switch kind {
	case "issue":
		return validateResourceIDs(data, "issue", []string{id})
	case "project":
		return validateResourceIDs(data, "project", []string{id})
	case "document":
		return slices.ContainsFunc(data.Documents, func(item domain.Document) bool { return item.ID == id })
	case "release":
		return slices.ContainsFunc(data.Releases, func(item domain.Release) bool { return item.ID == id })
	case "customer":
		return slices.ContainsFunc(data.Customers, func(item domain.Customer) bool { return item.ID == id })
	case "initiative":
		return slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id })
	case "view":
		return slices.ContainsFunc(data.SavedViews, func(item domain.SavedView) bool { return item.ID == id })
	}
	return false
}

func (s *server) addFavorite(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	var created domain.Favorite
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "favorite.added", id, map[string]string{"type": kind}, func(data *domain.Bootstrap) error {
		if !resourceExists(data, kind, id) {
			return errNotFound
		}
		index := slices.IndexFunc(data.Favorites, func(item domain.Favorite) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		if index >= 0 {
			created = data.Favorites[index]
			return nil
		}
		now := time.Now().UTC()
		created = domain.Favorite{ID: fmt.Sprintf("favorite_%d", now.UnixNano()), UserID: data.Viewer.ID, ResourceType: kind, ResourceID: id, Position: float64(len(data.Favorites)), CreatedAt: now}
		data.Favorites = append(data.Favorites, created)
		return nil
	})
	respondMutation(w, err, http.StatusOK, created)
}

func (s *server) removeFavorite(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "favorite.removed", id, map[string]string{"type": kind}, func(data *domain.Bootstrap) error {
		data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) addSubscription(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	var created domain.Subscription
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "subscription.added", id, map[string]string{"type": kind}, func(data *domain.Bootstrap) error {
		if !resourceExists(data, kind, id) {
			return errNotFound
		}
		index := slices.IndexFunc(data.Subscriptions, func(item domain.Subscription) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		if index >= 0 {
			created = data.Subscriptions[index]
			return nil
		}
		now := time.Now().UTC()
		created = domain.Subscription{ID: fmt.Sprintf("subscription_%d", now.UnixNano()), UserID: data.Viewer.ID, ResourceType: kind, ResourceID: id, CreatedAt: now}
		data.Subscriptions = append(data.Subscriptions, created)
		return nil
	})
	respondMutation(w, err, http.StatusOK, created)
}

func (s *server) removeSubscription(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "subscription.removed", id, map[string]string{"type": kind}, func(data *domain.Bootstrap) error {
		data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool {
			return item.UserID == data.Viewer.ID && item.ResourceType == kind && item.ResourceID == id
		})
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
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
			data.Documents = append([]domain.Document{value}, data.Documents...)
			syncDocumentProjectResources(data, value)
			restored = value
		case "release":
			var value domain.Release
			if json.Unmarshal(entry.Payload, &value) != nil {
				return errInvalid
			}
			data.Releases = append([]domain.Release{value}, data.Releases...)
			restored = value
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
		default:
			return errInvalid
		}
		data.Trash = slices.Delete(data.Trash, index, index+1)
		appendAudit(data, "restored", entry.ResourceType, entry.ResourceID, nil)
		return nil
	})
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
	if err := r.ParseMultipartForm(21 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid import file")
		return
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
		created = domain.ImportJob{ID: fmt.Sprintf("import_%d", now.UnixNano()), UserID: data.Viewer.ID, Filename: header.Filename, Format: format, Status: "mapping", Headers: headers, Rows: rows, Mapping: map[string]string{}, Errors: []string{}, CreatedAt: now, UpdatedAt: now}
		data.ImportJobs = append([]domain.ImportJob{created}, data.ImportJobs...)
		appendAudit(data, "previewed", "import", created.ID, map[string]any{"filename": created.Filename, "rows": len(rows)})
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) commitImport(w http.ResponseWriter, r *http.Request) {
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
		job.Status, job.Mapping, job.Errors = "running", input.Mapping, []string{}
		for rowIndex, row := range job.Rows {
			title := strings.TrimSpace(row[input.Mapping["title"]])
			if title == "" {
				job.Errors = append(job.Errors, fmt.Sprintf("Row %d: title is empty", rowIndex+2))
				continue
			}
			number := nextIssueNumber(data.Issues)
			now := time.Now().UTC()
			issueState := *state
			if value := strings.TrimSpace(row[input.Mapping["status"]]); value != "" {
				if matched := slices.IndexFunc(statesForTeam(data, team.ID), func(item domain.WorkflowState) bool { return item.ID == value || strings.EqualFold(item.Name, value) }); matched >= 0 {
					issueState = statesForTeam(data, team.ID)[matched]
				} else {
					job.Errors = append(job.Errors, fmt.Sprintf("Row %d: status %q was not found; used %s", rowIndex+2, value, issueState.Name))
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
			issue := domain.Issue{ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number, Title: title, Description: row[input.Mapping["description"]], Priority: settings.DefaultPriority, PriorityLabel: priorityLabel(settings.DefaultPriority), SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: team, State: issueState, Assignee: assignee, Creator: data.Viewer, Labels: []domain.IssueLabel{}, SubscriberIDs: []string{data.Viewer.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
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
						job.Errors = append(job.Errors, fmt.Sprintf("Row %d: label %q was not found", rowIndex+2, labelValue))
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
			data.Issues = append([]domain.Issue{issue}, data.Issues...)
			appendActivity(data, issue.ID, "issue.imported", data.Viewer, map[string]string{"importId": id})
			applySLARules(data, &data.Issues[0], now)
			job.Imported++
		}
		job.Status = "completed"
		job.UpdatedAt = time.Now().UTC()
		job.Rows = nil
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
		_ = writer.Write([]string{"identifier", "title", "description", "status", "priority", "assignee", "project", "due_date", "created_at", "updated_at"})
		for _, issue := range data.Issues {
			assignee, project := "", ""
			if issue.Assignee != nil {
				assignee = issue.Assignee.Email
			}
			if issue.Project != nil {
				project = issue.Project.Name
			}
			dueDate := ""
			if issue.DueDate != nil {
				dueDate = *issue.DueDate
			}
			_ = writer.Write([]string{issue.Identifier, issue.Title, issue.Description, issue.State.Name, issue.PriorityLabel, assignee, project, dueDate, issue.CreatedAt.Format(time.RFC3339), issue.UpdatedAt.Format(time.RFC3339)})
		}
		writer.Flush()
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	packageData := map[string]any{"workspace": data.Workspace, "teams": data.Teams, "users": data.Users, "issues": data.Issues, "projects": data.Projects, "documents": data.Documents, "customers": data.Customers, "customerRequests": data.CustomerRequests, "releases": data.Releases, "asks": data.Asks, "comments": data.Comments, "activities": data.Activities, "labels": data.Labels, "workflowStates": data.States, "cycles": data.Cycles, "templates": map[string]any{"issues": data.IssueTemplates, "projects": data.ProjectTemplates}, "exportedAt": time.Now().UTC()}
	_ = json.NewEncoder(w).Encode(packageData)
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
	cadence := intFromAny(settings["cadenceDays"])
	leadReminders := boolFromAny(settings["reminders"])
	missingNotifications := settings["missingNotifications"] == nil || boolFromAny(settings["missingNotifications"])
	if cadence > 0 {
		for _, project := range data.Projects {
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
		if cadence > 0 {
			for projectIndex := range next.Projects {
				project := &next.Projects[projectIndex]
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
		}
		next.Trash = slices.DeleteFunc(next.Trash, func(item domain.TrashEntry) bool { return now.After(item.ExpiresAt) })
		return nil
	})
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
