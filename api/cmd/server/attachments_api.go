package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
	"flow/api/internal/store"
)

// Link attachments follow the reference app's attachment model: an external URL with a
// title, optional subtitle, icon and key/value metadata. Creating an
// attachment whose URL already exists on the issue updates that attachment
// instead (URL idempotency), so integrations can re-sync safely.

type linkAttachmentInput = publicapi.LinkAttachmentInput

const maxAttachmentMetadataBytes = 16 << 10

func validHTTPURL(value string, limit int) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > limit {
		return "", false
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	return parsed.String(), true
}

func validateLinkAttachmentInput(input *linkAttachmentInput, requireURL bool) error {
	if requireURL {
		normalized, ok := validHTTPURL(input.URL, 2048)
		if !ok {
			return fmt.Errorf("url must be an http or https URL of at most 2048 characters")
		}
		input.URL = normalized
	}
	for name, value := range map[string]*string{"title": input.Title, "subtitle": input.Subtitle} {
		if value == nil {
			continue
		}
		*value = strings.TrimSpace(*value)
		if utf8.RuneCountInString(*value) > 255 {
			return fmt.Errorf("%s must be at most 255 characters", name)
		}
	}
	if input.IconURL != nil && strings.TrimSpace(*input.IconURL) != "" {
		normalized, ok := validHTTPURL(*input.IconURL, 2048)
		if !ok {
			return fmt.Errorf("iconUrl must be an http or https URL")
		}
		*input.IconURL = normalized
	}
	if input.Metadata != nil {
		raw, err := json.Marshal(input.Metadata)
		if err != nil || len(raw) > maxAttachmentMetadataBytes {
			return fmt.Errorf("metadata must be a JSON object of at most 16 KB")
		}
	}
	return nil
}

// defaultApplicationIcon uses the OAuth application's logo for links created
// by an application token (the reference app shows the app icon by default).
func (s *server) defaultApplicationIcon(r *http.Request) string {
	key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
	if !ok || key.OAuthClientID == "" {
		return ""
	}
	data, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r))
	if !ok {
		return ""
	}
	for _, application := range data.OAuthApplications {
		if application.ClientID == key.OAuthClientID {
			return application.LogoURL
		}
	}
	return ""
}

func applyLinkAttachmentInput(attachment *domain.Attachment, input linkAttachmentInput) {
	if input.Title != nil {
		attachment.Title = *input.Title
	}
	if input.Subtitle != nil {
		attachment.Subtitle = *input.Subtitle
	}
	if input.IconURL != nil {
		attachment.IconURL = strings.TrimSpace(*input.IconURL)
	}
	if input.Metadata != nil {
		attachment.Metadata = input.Metadata
	}
}

// POST /api/issues/{id}/link-attachments

func (s *server) upsertLinkAttachment(w http.ResponseWriter, r *http.Request) {
	var input linkAttachmentInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateLinkAttachmentInput(&input, true); err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
		return
	}
	issue, _, ok := s.authorizedPublicIssue(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	attachment, created, err := s.saveLinkAttachment(r.Context(), r, workspaceKey(r), issue, input)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, attachment)
}

// saveLinkAttachment creates or updates (same URL on the same issue) a link
// attachment. It is shared by the REST endpoint and the MCP save_attachment tool.
func (s *server) saveLinkAttachment(ctx context.Context, r *http.Request, workspace string, issue domain.Issue, input linkAttachmentInput) (domain.Attachment, bool, error) {
	existing := slices.IndexFunc(issue.Attachments, func(item domain.Attachment) bool { return item.URL == input.URL })
	eventType := "attachment.created"
	if existing >= 0 {
		eventType = "attachment.updated"
	}
	defaultIcon := ""
	if r != nil && input.IconURL == nil && existing < 0 {
		defaultIcon = s.defaultApplicationIcon(r)
	}
	var saved domain.Attachment
	created := false
	err := s.store.MutateWorkspace(store.WithIssueRecordMutations(ctx, issue.ID), workspace, eventType, issue.ID, input, func(data *domain.Bootstrap) error {
		target, err := issueByID(data, issue.ID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		index := slices.IndexFunc(target.Attachments, func(item domain.Attachment) bool { return item.URL == input.URL })
		if index >= 0 {
			applyLinkAttachmentInput(&target.Attachments[index], input)
			target.Attachments[index].UpdatedAt = &now
			saved = target.Attachments[index]
			return nil
		}
		parsed, _ := url.Parse(input.URL)
		attachment := domain.Attachment{ID: fmt.Sprintf("attachment_%d", now.UnixNano()), IssueID: target.ID, Title: parsed.Host, URL: input.URL, ContentType: "text/uri-list", CreatedAt: now, UpdatedAt: &now, Creator: data.Viewer, IconURL: defaultIcon}
		applyLinkAttachmentInput(&attachment, input)
		if attachment.Title == "" {
			attachment.Title = parsed.Host
		}
		target.Attachments = append(target.Attachments, attachment)
		appendActivity(data, target.ID, "issue.link_created", data.Viewer, map[string]string{"attachmentId": attachment.ID, "title": attachment.Title, "url": attachment.URL})
		saved, created = attachment, true
		return nil
	})
	return saved, created, err
}

// PATCH /api/issues/{id}/attachments/{attachmentId}

func (s *server) updateLinkAttachment(w http.ResponseWriter, r *http.Request) {
	var input linkAttachmentInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.URL != "" {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "url cannot be changed; create a new attachment instead")
		return
	}
	if err := validateLinkAttachmentInput(&input, false); err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
		return
	}
	issue, _, ok := s.authorizedPublicIssue(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	attachmentID := r.PathValue("attachmentId")
	var saved domain.Attachment
	err := s.store.MutateWorkspace(store.WithIssueRecordMutations(r.Context(), issue.ID), workspaceKey(r), "attachment.updated", issue.ID, input, func(data *domain.Bootstrap) error {
		target, err := issueByID(data, issue.ID)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(target.Attachments, func(item domain.Attachment) bool { return item.ID == attachmentID })
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		applyLinkAttachmentInput(&target.Attachments[index], input)
		target.Attachments[index].UpdatedAt = &now
		saved = target.Attachments[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, saved)
}

func attachmentFields() map[string]func(domain.Attachment) any {
	return map[string]func(domain.Attachment) any{
		"id":          func(a domain.Attachment) any { return a.ID },
		"title":       func(a domain.Attachment) any { return a.Title },
		"subtitle":    func(a domain.Attachment) any { return a.Subtitle },
		"url":         func(a domain.Attachment) any { return a.URL },
		"contentType": func(a domain.Attachment) any { return a.ContentType },
		"sourceType":  func(a domain.Attachment) any { return attachmentSourceType(a) },
		"issue":       func(a domain.Attachment) any { return filterRelation(a.IssueID, nil) },
		"creator":     func(a domain.Attachment) any { return userRelation(&a.Creator) },
		"createdAt":   func(a domain.Attachment) any { return a.CreatedAt },
		"updatedAt":   func(a domain.Attachment) any { return attachmentUpdatedAt(a) },
	}
}

func attachmentUpdatedAt(a domain.Attachment) time.Time {
	if a.UpdatedAt != nil {
		return *a.UpdatedAt
	}
	return a.CreatedAt
}

// attachmentSourceType is "file" for uploaded files, the provider for
// integration links and "url" for other links.
func attachmentSourceType(a domain.Attachment) string {
	switch {
	case a.Provider != "":
		return a.Provider
	case strings.HasPrefix(a.URL, "/uploads/"):
		return "file"
	}
	return "url"
}

// GET /api/attachments?issueId=|url=

func (s *server) listAttachmentsConnection(w http.ResponseWriter, r *http.Request) {
	issueID, link := strings.TrimSpace(r.URL.Query().Get("issueId")), strings.TrimSpace(r.URL.Query().Get("url"))
	if issueID == "" && link == "" {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Provide issueId or url")
		return
	}
	key := func(a domain.Attachment, orderBy string) pageKey {
		return orderedKey(orderBy, a.ID, a.CreatedAt, attachmentUpdatedAt(a))
	}
	if issueID != "" {
		issue, _, ok := s.authorizedPublicIssue(w, r, issueID)
		if !ok {
			return
		}
		items := issue.Attachments
		if link != "" {
			items = slices.DeleteFunc(slices.Clone(items), func(a domain.Attachment) bool { return a.URL != link })
		}
		writeConnection(w, r, items, attachmentFields(), key)
		return
	}
	if len(link) > 2048 {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "url is too long")
		return
	}
	_, query, err := s.publicIssueQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	ids, err := s.store.AttachmentIssueIDs(r.Context(), query, link, maxPageSize)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	items := []domain.Attachment{}
	for _, id := range ids {
		issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, id)
		if err != nil {
			continue
		}
		for _, attachment := range issue.Attachments {
			if attachment.URL == link {
				items = append(items, attachment)
			}
		}
	}
	writeConnection(w, r, items, attachmentFields(), key)
}

// GET /api/attachments/{id}

func (s *server) getAttachment(w http.ResponseWriter, r *http.Request) {
	_, query, err := s.publicIssueQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	id := r.PathValue("id")
	parent, err := s.store.IssueAttachmentParent(r.Context(), query, id)
	if err != nil || parent == "" {
		if err != nil && !isNoRows(err) {
			issueRecordsError(w, err)
			return
		}
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "attachment not found")
		return
	}
	issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, parent)
	if err == nil {
		for _, attachment := range issue.Attachments {
			if attachment.ID == id {
				writeJSON(w, http.StatusOK, attachment)
				return
			}
		}
	}
	writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "attachment not found")
}

// saveMCPLinkAttachment is the MCP save_attachment tool.
func (s *server) saveMCPLinkAttachment(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	issue, err := mcpFindIssue(data, stringArg(args, "issue"))
	if err != nil {
		return nil, err
	}
	input := linkAttachmentInput{URL: stringArg(args, "url")}
	for name, target := range map[string]**string{"title": &input.Title, "subtitle": &input.Subtitle, "iconUrl": &input.IconURL} {
		if value, ok := args[name].(string); ok {
			*target = &value
		}
	}
	if metadata, ok := args["metadata"].(map[string]any); ok {
		input.Metadata = metadata
	}
	if err := validateLinkAttachmentInput(&input, true); err != nil {
		return nil, err
	}
	if current, err := s.store.IssueRecord(ctx, actor.WorkspaceKey, issue.ID); err == nil {
		issue = current
	}
	attachment, _, err := s.saveLinkAttachment(ctx, nil, actor.WorkspaceKey, issue, input)
	return attachment, err
}
