package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

// MCP tools for the document page's menu: delete and restore, version
// history, and sharing. Each runs the routed HTTP handler, so the document's
// own access list, API-key scopes and notifications apply as they do in the
// app.

// mcpDocumentRevisionContentLimit caps the Markdown returned per version so a
// page of history stays within an agent's context.
const mcpDocumentRevisionContentLimit = 20000

func (s *server) deleteMCPDocument(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	document, err := mcpFindDocument(data, stringArg(args, "id"))
	if err != nil {
		return nil, err
	}
	if _, err := s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/documents/"+pathID(document.ID), map[string]string{"id": document.ID}, nil, s.deleteDocument); err != nil {
		return nil, err
	}
	result := map[string]any{"deleted": true, "id": document.ID, "slugId": document.SlugID, "title": document.Title, "restorable": true}
	if fresh, err := s.mcpWorkspaceData(ctx, actor); err == nil {
		if entry, _, ok := mcpFindDeletedDocument(actor, fresh, document.ID); ok {
			result["restorableUntil"] = entry.ExpiresAt
		}
	}
	return result, nil
}

// mcpFindDeletedDocument finds a document in "Recently deleted" by ID, slug,
// earlier slug or title, hiding documents of teams a team-restricted key
// cannot see.
func mcpFindDeletedDocument(actor mcpActor, data domain.Bootstrap, query string) (domain.TrashEntry, domain.Document, bool) {
	restricted := apiKeyTeamRestrictionSelected(actor.APIKey)
	for _, pass := range []func(domain.Document) bool{
		func(document domain.Document) bool { return equalFoldAny(query, document.ID, document.SlugID) },
		func(document domain.Document) bool {
			return equalFoldAny(query, document.PreviousSlugIDs...) || equalFoldAny(query, document.Title)
		},
	} {
		for _, entry := range data.Trash {
			if entry.ResourceType != "document" {
				continue
			}
			var document domain.Document
			if json.Unmarshal(entry.Payload, &document) != nil || !pass(document) {
				continue
			}
			if restricted && len(document.TeamIDs) > 0 && !slices.ContainsFunc(document.TeamIDs, func(id string) bool { return slices.Contains(actor.APIKey.TeamIDs, id) }) {
				continue
			}
			return entry, document, true
		}
	}
	return domain.TrashEntry{}, domain.Document{}, false
}

func (s *server) restoreMCPDocument(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	query := stringArg(args, "id")
	if _, err := mcpFindDocument(data, query); err == nil {
		return nil, fmt.Errorf("document %q is not deleted", query)
	}
	_, deleted, ok := mcpFindDeletedDocument(actor, data, query)
	if !ok {
		return nil, fmt.Errorf("deleted document %q not found", query)
	}
	result, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/documents/"+pathID(deleted.ID)+"/restore", map[string]string{"id": deleted.ID}, nil, s.restoreDeletedDocument)
	if err != nil {
		return nil, err
	}
	var restored domain.Document
	if err := jsonClone(result, &restored); err != nil {
		return nil, err
	}
	return map[string]any{"restored": true, "id": restored.ID, "slugId": restored.SlugID, "title": restored.Title, "url": mcpWorkspaceURL(actor.WorkspaceKey, args, "document", restored.SlugID)}, nil
}

func (s *server) listMCPDocumentHistory(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	document, err := mcpFindDocument(data, stringArg(args, "id"))
	if err != nil {
		return nil, err
	}
	query := url.Values{"limit": {strconv.Itoa(min(max(intArg(args, "limit", 20), 1), 50))}}
	cursor := stringArg(args, "cursor")
	if cursor != "" {
		if offset, err := strconv.Atoi(cursor); err != nil || offset < 0 {
			return nil, fmt.Errorf("invalid history cursor")
		}
		query.Set("cursor", cursor)
	}
	result, err := s.invokeMCPRoute(ctx, actor, http.MethodGet, "/api/documents/"+pathID(document.ID)+"/history?"+query.Encode(), map[string]string{"id": document.ID}, nil, s.documentContentHistory)
	if err != nil {
		return nil, err
	}
	var page struct {
		Nodes      []domain.DocumentRevision `json:"nodes"`
		NextCursor string                    `json:"nextCursor"`
		Total      int                       `json:"total"`
	}
	if err := jsonClone(result, &page); err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(page.Nodes))
	for index, revision := range page.Nodes {
		content, truncated := revision.Content, false
		if len(content) > mcpDocumentRevisionContentLimit {
			content, truncated = strings.ToValidUTF8(content[:mcpDocumentRevisionContentLimit], ""), true
		}
		authors := []map[string]any{}
		for _, id := range revision.AuthorIDs {
			if user, err := mcpFindUser(data, id); err == nil {
				authors = append(authors, mcpUserRef(&user))
			}
		}
		item := map[string]any{
			"id": revision.ID, "title": revision.Title, "content": content, "contentTruncated": truncated,
			"author": mcpUserRef(&revision.Author), "authors": authors, "createdAt": revision.CreatedAt, "startedAt": revision.StartedAt,
			// Versions are newest first and the newest one is the document's current state.
			"current": cursor == "" && index == 0,
		}
		items = append(items, item)
	}
	return map[string]any{"document": map[string]any{"id": document.ID, "slugId": document.SlugID, "title": document.Title}, "items": items, "nextCursor": page.NextCursor, "total": page.Total}, nil
}

func (s *server) restoreMCPDocumentVersion(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	document, err := mcpFindDocument(data, stringArg(args, "id"))
	if err != nil {
		return nil, err
	}
	revisionID := stringArg(args, "revisionId")
	if !slices.ContainsFunc(document.Revisions, func(item domain.DocumentRevision) bool { return item.ID == revisionID }) {
		return nil, fmt.Errorf("version %q not found on document %q", revisionID, document.Title)
	}
	path := "/api/documents/" + pathID(document.ID) + "/restore/" + pathID(revisionID)
	result, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, path, map[string]string{"id": document.ID, "revisionId": revisionID}, nil, s.restoreDocumentRevision)
	if err != nil {
		return nil, err
	}
	var restored domain.Document
	if err := jsonClone(result, &restored); err != nil {
		return nil, err
	}
	return map[string]any{"restored": true, "id": restored.ID, "slugId": restored.SlugID, "title": restored.Title, "revisionId": revisionID, "updatedAt": restored.UpdatedAt, "url": mcpWorkspaceURL(actor.WorkspaceKey, args, "document", restored.SlugID)}, nil
}

func (s *server) getMCPDocumentPermissions(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	document, err := mcpFindDocument(data, stringArg(args, "id"))
	if err != nil {
		return nil, err
	}
	result, err := s.invokeMCPRoute(ctx, actor, http.MethodGet, "/api/documents/"+pathID(document.ID)+"/permissions", map[string]string{"id": document.ID}, nil, s.listDocumentPermissions)
	if err != nil {
		return nil, err
	}
	return mcpDocumentPermissionsView(data, document, result)
}

func (s *server) saveMCPDocumentPermissions(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	document, err := mcpFindDocument(data, stringArg(args, "id"))
	if err != nil {
		return nil, err
	}
	entries, _ := args["permissions"].([]any)
	permissions := make([]domain.DocumentPermission, 0, len(entries))
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		permission, err := mcpResolveDocumentPermission(data, entry)
		if err != nil {
			return nil, err
		}
		permissions = append(permissions, permission)
	}
	input := map[string]any{"permissions": permissions}
	result, err := s.invokeMCPRoute(ctx, actor, http.MethodPut, "/api/documents/"+pathID(document.ID)+"/permissions", map[string]string{"id": document.ID}, input, s.replaceDocumentPermissions)
	if err != nil {
		if err.Error() == errInvalid.Error() {
			return nil, fmt.Errorf("invalid access list: each subject may appear once, and only users can be owners")
		}
		return nil, err
	}
	return mcpDocumentPermissionsView(data, document, result)
}

// mcpResolveDocumentPermission turns a friendly access entry ("me", a team
// key, "workspace") into the subject IDs the permissions endpoint stores.
func mcpResolveDocumentPermission(data domain.Bootstrap, entry map[string]any) (domain.DocumentPermission, error) {
	kind, subject, role := stringArg(entry, "subjectType"), stringArg(entry, "subject"), stringArg(entry, "role")
	if kind == "" && (subject == "" || equalFoldAny(subject, "workspace", "everyone", data.Workspace.ID, data.Workspace.URLKey, data.Workspace.Name)) {
		kind = "workspace"
	}
	permission := domain.DocumentPermission{SubjectType: kind, Role: role}
	switch kind {
	case "workspace":
		permission.SubjectID = data.Workspace.ID
	case "user":
		user, err := mcpFindUser(data, subject)
		if err != nil {
			return permission, err
		}
		permission.SubjectID = user.ID
	case "team":
		team, err := mcpFindTeam(data, subject)
		if err != nil {
			return permission, err
		}
		permission.SubjectID = team.ID
	default:
		user, userErr := mcpFindUser(data, subject)
		team, teamErr := mcpFindTeam(data, subject)
		switch {
		case userErr == nil && teamErr == nil:
			return permission, fmt.Errorf("%q names both a user and a team; set subjectType", subject)
		case userErr == nil:
			permission.SubjectType, permission.SubjectID = "user", user.ID
		case teamErr == nil:
			permission.SubjectType, permission.SubjectID = "team", team.ID
		default:
			return permission, fmt.Errorf("no user or team matches %q", subject)
		}
	}
	if role == "owner" && permission.SubjectType != "user" {
		return permission, fmt.Errorf("only users can be document owners")
	}
	return permission, nil
}

func mcpDocumentPermissionsView(data domain.Bootstrap, document domain.Document, result any) (any, error) {
	var permissions []domain.DocumentPermission
	if err := jsonClone(result, &permissions); err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(permissions))
	for _, permission := range permissions {
		var subject map[string]any
		switch permission.SubjectType {
		case "user":
			if user, err := mcpFindUser(data, permission.SubjectID); err == nil {
				subject = mcpUserRef(&user)
			}
		case "team":
			if team, err := mcpFindTeam(data, permission.SubjectID); err == nil {
				subject = map[string]any{"id": team.ID, "key": team.Key, "name": team.Name}
			}
		case "workspace":
			subject = map[string]any{"id": data.Workspace.ID, "name": data.Workspace.Name}
		}
		items = append(items, map[string]any{"id": permission.ID, "subjectType": permission.SubjectType, "subjectId": permission.SubjectID, "subject": subject, "role": permission.Role, "updatedAt": permission.UpdatedAt})
	}
	return map[string]any{"document": map[string]any{"id": document.ID, "slugId": document.SlugID, "title": document.Title}, "explicit": len(items) > 0, "items": items}, nil
}

// mcpDocumentCommentViews spells out a document comment's inline anchor:
// anchorId and quotedText are null for page-level comments and replies.
func mcpDocumentCommentViews(comments []domain.Comment) []map[string]any {
	views := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		view := map[string]any{}
		_ = jsonClone(comment, &view)
		view["resolved"] = comment.Resolved
		view["anchorId"], view["quotedText"] = nil, nil
		if comment.AnchorID != "" {
			view["anchorId"] = comment.AnchorID
		}
		if comment.QuotedText != "" {
			view["quotedText"] = comment.QuotedText
		}
		views = append(views, view)
	}
	return views
}
