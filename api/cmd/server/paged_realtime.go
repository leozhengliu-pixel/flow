package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func pagedRealtimeRequest(r *http.Request) bool {
	// Realtime payloads have the same wire contract for both clients. Never
	// retain a full workspace for an SSE connection or hydrate it on heartbeats.
	return strings.HasPrefix(r.URL.Path, "/api/realtime/")
}

func (s *server) pagedRealtimeMetadata(r *http.Request) (domain.Bootstrap, store.IssueRecordQuery, error) {
	return s.issueRecordsQuery(r)
}

func (s *server) pagedPresence(r *http.Request, values []domain.Presence) ([]domain.Presence, error) {
	data, query, err := s.pagedRealtimeMetadata(r)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, presence := range values {
		if presence.IssueID != "" {
			ids = append(ids, presence.IssueID)
		}
	}
	visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, ids)
	if err != nil {
		return nil, err
	}
	for id := range visible {
		data.Issues = append(data.Issues, domain.Issue{ID: id})
	}
	values = slices.DeleteFunc(values, func(item domain.Presence) bool {
		return item.DocumentID != "" && !s.documentPresenceAllowed(r, item.DocumentID)
	})
	return filterPresenceForViewer(data, values), nil
}

func (s *server) pagedRealtimeEvent(r *http.Request, event domain.RealtimeEvent) (domain.RealtimeEvent, bool, error) {
	// Older nodes or replay buffers may still send credential telemetry. Reject
	// it before any workspace permission/directory projection is attempted.
	if domain.RoutineCredentialEvent(event.Type) {
		return event, false, nil
	}
	if event.Type == "agent_task.replied" {
		// An agent replied in an issue thread; viewers of the issue refresh their Inbox.
		return event, s.pagedIssueVisible(r, event.AggregateID), nil
	}
	if event.Type == "agent_task.updated" {
		var payload struct {
			TaskID string `json:"taskId"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || payload.TaskID == "" {
			return event, false, nil
		}
		return event, s.agentTaskRealtimeVisible(r, payload.TaskID), nil
	}
	if event.Type == "workspace_preferences.updated" {
		metadata, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r))
		if !ok {
			return event, false, nil
		}
		if !s.authDisabled {
			_, status, err := s.store.WorkspaceRole(r.Context(), metadata.Workspace.ID, authUser(r).ID)
			if err != nil || status != "active" {
				return event, false, err
			}
		}
		// Clients refresh the small settings endpoint. Do not project unrelated
		// entity collections or include arbitrary preference payloads in SSE.
		event.Payload = json.RawMessage(`{"settingsChanged":true}`)
		return event, true, nil
	}
	if strings.HasPrefix(event.Type, "loop_run.") {
		// Run progress is a small signal ({loopId, runId, status}); members
		// refetch the run. Skip the workspace projection for it.
		if s.authDisabled {
			return event, true, nil
		}
		metadata, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r))
		if !ok {
			return event, false, nil
		}
		_, status, err := s.store.WorkspaceRole(r.Context(), metadata.Workspace.ID, authUser(r).ID)
		return event, err == nil && status == "active", err
	}
	data, query, err := s.pagedRealtimeMetadata(r)
	if err != nil {
		return event, false, err
	}
	if strings.HasPrefix(event.Type, "integration.") {
		event.Payload = redactIntegrationEvent(event.Payload)
	}
	if strings.HasPrefix(event.Type, "favorite.") || strings.HasPrefix(event.Type, "favorite_folder.") || strings.HasPrefix(event.Type, "subscription.") || strings.HasPrefix(event.Type, "notifications.") {
		return event, event.ActorID == data.Viewer.ID || s.authDisabled && event.ActorID == "", nil
	}
	if strings.HasPrefix(event.Type, "notification.") && event.Type != "notification.desktop_acknowledged" && event.Type != "notification.delivery_retried" {
		notification, err := s.store.NotificationRecord(r.Context(), query.Workspace, data.Viewer.ID, event.AggregateID)
		if errors.Is(err, sql.ErrNoRows) {
			return event, false, nil
		}
		if err != nil {
			return event, false, err
		}
		event.Payload = enrichNotificationEvent(event.Payload, notification)
		return event, true, nil
	}
	if event.Type == "presence.updated" {
		var payload struct {
			Presence []domain.Presence `json:"presence"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return event, false, err
		}
		payload.Presence, err = s.pagedPresence(r, payload.Presence)
		if err != nil {
			return event, false, err
		}
		event.Payload, _ = json.Marshal(payload)
		return event, true, nil
	}
	if event.Type == "issue.deleted" {
		// The rows are gone, so visibility is decided from the team and grants the
		// delete recorded in the payload. No entry, or no match, sends nothing.
		ids := visibleDeletedIssueIDs(event, query)
		if len(ids) == 0 {
			return event, false, nil
		}
		return pagedIssueIDsEvent(event, ids, nil, map[string]any{"deleted": true}), true, nil
	}
	if event.Type == "issue.batch_updated" {
		ev, err := s.pagedBatchEvent(r, data, query, event)
		return ev, err == nil, err
	}
	if pagedIssuePermissionEvent(event.Type) {
		visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, []string{event.AggregateID})
		if err != nil {
			return event, false, err
		}
		if !visible[event.AggregateID] {
			// The viewer lost (or never had) access: tell the client to drop the row.
			return pagedIssueIDsEvent(event, nil, []string{event.AggregateID}, nil), true, nil
		}
	} else if strings.Contains(event.Type, "permission") && !strings.HasPrefix(event.Type, "issue.") {
		// Document and other permission changes carry subject lists the viewer may
		// not see: send the bare signal, clients reload the affected metadata.
		return domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ClientID: event.ClientID, ActorID: event.ActorID, CreatedAt: event.CreatedAt}, true, nil
	}
	if strings.HasPrefix(event.Type, "issue.") || strings.HasPrefix(event.Type, "comment.") || strings.HasPrefix(event.Type, "attachment.") {
		if strings.HasPrefix(event.Type, "comment.") && slices.ContainsFunc(data.Documents, func(document domain.Document) bool { return document.ID == event.AggregateID }) {
			return event, realtimeEventVisible(data, event), nil
		}
		visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, []string{event.AggregateID})
		if err != nil || !visible[event.AggregateID] {
			return event, false, err
		}
		// Replace the entity envelope with the current authorized projection.
		issue, err := s.store.IssueRecord(r.Context(), query.Workspace, event.AggregateID)
		if err != nil {
			return event, false, err
		}
		projected, err := s.projectIssueRecordReferences(r, data, query, []domain.Issue{issue})
		if err != nil {
			return event, false, err
		}
		var payload map[string]any
		_ = json.Unmarshal(event.Payload, &payload)
		if payload == nil {
			payload = map[string]any{}
		}
		payload["entity"] = projected[0]
		if pagedIssuePermissionEvent(event.Type) {
			// Permission inputs list subjects; the viewer only needs the issue it can still see.
			payload = map[string]any{"entity": projected[0], "issueIds": []string{event.AggregateID}}
		}
		if _, ok := payload["issue"]; ok {
			payload["issue"] = projected[0]
		}
		event.Payload, _ = json.Marshal(payload)
		return event, true, nil
	}
	if visible, decided := s.pagedPulseSourceVisible(r, data, event); decided {
		return event, visible, nil
	}
	return event, realtimeEventVisible(data, event), nil
}

// pagedPulseSourceVisible authorizes project and initiative events (updates,
// comments, reactions, edits) for paged clients, whose request metadata
// carries no projects or initiatives: the aggregate's visibility is checked
// against the shared project/initiative snapshot with the viewer's teams.
// decided is false for events of other kinds or unknown aggregates.
func (s *server) pagedPulseSourceVisible(r *http.Request, data domain.Bootstrap, event domain.RealtimeEvent) (visible, decided bool) {
	kind := ""
	switch {
	case strings.HasPrefix(event.Type, "project.") || strings.HasPrefix(event.Type, "project_update."):
		kind = "project"
	case strings.HasPrefix(event.Type, "initiative.") || strings.HasPrefix(event.Type, "initiative_update."):
		kind = "initiative"
	default:
		return false, false
	}
	if event.AggregateID == "" {
		return false, false
	}
	snapshot, ok := s.store.PulseSources(workspaceKey(r))
	if !ok {
		return false, true
	}
	rules := newPulseRules(snapshot)
	role := data.ViewerRole
	if s.authDisabled && role == "" {
		role = "admin"
	}
	viewer := newPulseViewer(snapshot, data.Viewer.ID, role, data.TeamMembers, pulseRestrictTeams(r))
	if kind == "project" {
		if project := rules.projects[event.AggregateID]; project != nil {
			return rules.projectVisible(viewer, project), true
		}
		return false, false
	}
	if initiative := rules.initiatives[event.AggregateID]; initiative != nil {
		return rules.initiativeVisible(viewer, initiative), true
	}
	return false, true
}

func (s *server) pagedIssueVisible(r *http.Request, id string) bool {
	_, query, err := s.pagedRealtimeMetadata(r)
	if err != nil {
		return false
	}
	visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, []string{id})
	return err == nil && visible[id]
}

func (s *server) writePresenceResponse(w http.ResponseWriter, r *http.Request, presence []domain.Presence) {
	if pagedRealtimeRequest(r) {
		var err error
		presence, err = s.pagedPresence(r, presence)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, presence)
}

func enrichNotificationEvent(raw json.RawMessage, notification domain.Notification) json.RawMessage {
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["entity"] = notification
	encoded, _ := json.Marshal(payload)
	return encoded
}

// agentTaskRealtimeVisible reports whether the stream's viewer may see a push
// for an agent session. The stream's projection carries issue access only, so
// a document or project session is checked against its resource directly
// (cheaply: chunks of a streamed reply each push one event).
func (s *server) agentTaskRealtimeVisible(r *http.Request, taskID string) bool {
	workspace := workspaceKey(r)
	if workspace == "" {
		if metadata, ok := s.store.WorkspaceMetadata(""); ok {
			workspace = metadata.Workspace.URLKey
		}
	}
	task, err := s.store.AgentTask(r.Context(), workspace, taskID)
	if err != nil {
		return false
	}
	switch task.ResourceType {
	case "document":
		if viewer := authUser(r); viewer.App && task.AppUserID != viewer.ID {
			return false
		}
		return s.documentPresenceAllowed(r, task.ResourceID)
	case "project":
		if viewer := authUser(r); viewer.App && task.AppUserID != viewer.ID {
			return false
		}
		project, ok := s.store.ProjectByID(workspace, task.ResourceID)
		if !ok {
			return false
		}
		_, query, err := s.pagedRealtimeMetadata(r)
		if err != nil {
			return false
		}
		access := query.Access
		return access == nil || access.Admin || len(project.TeamIDs) == 0 || slices.ContainsFunc(project.TeamIDs, func(id string) bool { return slices.Contains(access.VisibleTeamIDs, id) })
	}
	_, _, err = s.authorizedAgentTask(r, taskID, false)
	return err == nil
}

// maxBatchProjection caps how many projected issues a batch event carries;
// larger batches send ids and clients refetch just those issues.
const maxBatchProjection = 50

func pagedIssuePermissionEvent(eventType string) bool {
	switch eventType {
	case "issue.permissions_updated", "issue.permission_updated", "issue.permission_deleted", "issue.shared", "issue.unshared":
		return true
	}
	return false
}

// deletedIssueAccess is what an issue.deleted event keeps of a trashed issue's
// visibility rules: its team plus its own and inherited issue grants.
type deletedIssueAccess struct {
	ID     string                `json:"id"`
	TeamID string                `json:"teamId"`
	Grants []deletedIssueSubject `json:"grants,omitempty"`
}

type deletedIssueSubject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func deletedIssueAccessEntries(data *domain.Bootstrap, ids []string) []deletedIssueAccess {
	entries := make([]deletedIssueAccess, 0, len(ids))
	for _, id := range ids {
		entry := deletedIssueAccess{ID: id}
		seen := map[string]bool{}
		// Grants are inherited by sub-issues, so walk up through every ancestor.
		for current := id; current != "" && !seen[current]; {
			seen[current] = true
			index := issueIndexByID(data, current)
			if index < 0 {
				break
			}
			issue := data.Issues[index]
			if current == id {
				entry.TeamID = issue.Team.ID
			}
			for _, grant := range issue.Permissions {
				entry.Grants = append(entry.Grants, deletedIssueSubject{Type: grant.SubjectType, ID: grant.SubjectID})
			}
			current = ""
			if issue.ParentID != nil {
				current = *issue.ParentID
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

// visibleDeletedIssueIDs keeps the deleted ids this viewer could read, applying
// the same team/grant rules as the issue record queries.
func visibleDeletedIssueIDs(event domain.RealtimeEvent, query store.IssueRecordQuery) []string {
	var payload struct {
		IssueAccess []deletedIssueAccess `json:"issueAccess"`
	}
	_ = json.Unmarshal(event.Payload, &payload)
	access := query.Access
	ids := []string{}
	for _, entry := range payload.IssueAccess {
		if access != nil {
			if slices.Contains(access.ArchivedTeamIDs, entry.TeamID) {
				continue
			}
			if !access.Admin && !slices.Contains(access.VisibleTeamIDs, entry.TeamID) && !slices.ContainsFunc(entry.Grants, func(grant deletedIssueSubject) bool {
				switch grant.Type {
				case "user":
					return grant.ID == access.UserID
				case "workspace":
					return grant.ID == "" || grant.ID == access.WorkspaceID || grant.ID == query.Workspace
				case "team":
					return slices.Contains(access.VisibleTeamIDs, grant.ID)
				}
				return false
			}) {
				continue
			}
		}
		if query.AllowedTeamIDs != nil && !slices.Contains(query.AllowedTeamIDs, entry.TeamID) {
			continue
		}
		ids = append(ids, entry.ID)
	}
	return ids
}

// pagedIssueIDsEvent rebuilds an event with an ids-only payload: issueIds are
// the issues it affects, removedIssueIds the ones the viewer must drop.
func pagedIssueIDsEvent(event domain.RealtimeEvent, issueIDs, removed []string, extra map[string]any) domain.RealtimeEvent {
	payload := map[string]any{}
	for key, value := range extra {
		payload[key] = value
	}
	if len(issueIDs) > 0 {
		payload["issueIds"] = issueIDs
	}
	if len(removed) > 0 {
		payload["removedIssueIds"] = removed
		if _, ok := payload["issueIds"]; !ok {
			payload["issueIds"] = removed
		}
	}
	event.Payload, _ = json.Marshal(payload)
	return event
}

// pagedBatchEvent narrows a bulk update to the issues this viewer can see.
// Hidden ids become removedIssueIds (the batch may have moved them out of view)
// and visible ones are projected inline up to maxBatchProjection, else listed
// for a targeted refetch.
func (s *server) pagedBatchEvent(r *http.Request, data domain.Bootstrap, query store.IssueRecordQuery, event domain.RealtimeEvent) (domain.RealtimeEvent, error) {
	var input struct {
		IssueIDs []string `json:"issueIds"`
	}
	_ = json.Unmarshal(event.Payload, &input)
	visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, input.IssueIDs)
	if err != nil {
		return event, err
	}
	shown, hidden := []string{}, []string{}
	for _, id := range input.IssueIDs {
		if visible[id] {
			shown = append(shown, id)
		} else {
			hidden = append(hidden, id)
		}
	}
	extra := map[string]any{}
	if len(shown) > 0 && len(shown) <= maxBatchProjection {
		issues := make([]domain.Issue, 0, len(shown))
		for _, id := range shown {
			issue, err := s.store.IssueRecord(r.Context(), query.Workspace, id)
			if err != nil {
				issues = nil
				break
			}
			issues = append(issues, issue)
		}
		if issues != nil {
			if projected, err := s.projectIssueRecordReferences(r, data, query, issues); err == nil {
				extra["issues"] = projected
			}
		}
	}
	return pagedIssueIDsEvent(event, shown, hidden, extra), nil
}
