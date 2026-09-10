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
	data, query, err := s.issueRecordsQuery(r)
	if err != nil {
		return data, query, err
	}
	if s.authDisabled {
		var ok bool
		data, ok = s.store.WorkspaceMetadata(query.Workspace)
		if !ok {
			return data, query, store.ErrAuthForbidden
		}
		data.ViewerRole = "admin"
	} else {
		data, err = s.store.PagedWorkspaceMetadata(r.Context(), query.Workspace, authUser(r).ID)
		if err != nil {
			return data, query, err
		}
		filterBootstrapForAPIKey(&data, r)
	}
	return data, query, nil
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
	return filterPresenceForViewer(data, values), nil
}

func (s *server) pagedRealtimeEvent(r *http.Request, event domain.RealtimeEvent) (domain.RealtimeEvent, bool, error) {
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
	if strings.HasPrefix(event.Type, "attachment.") || event.Type == "issue.deleted" || event.Type == "issue.batch_updated" || strings.Contains(event.Type, "permission") {
		return domain.RealtimeEvent{ID: event.ID, Type: "workspace.resync_required", ClientID: event.ClientID, ActorID: event.ActorID, CreatedAt: event.CreatedAt}, true, nil
	}
	if strings.HasPrefix(event.Type, "issue.") || strings.HasPrefix(event.Type, "comment.") {
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
		if _, ok := payload["issue"]; ok {
			payload["issue"] = projected[0]
		}
		event.Payload, _ = json.Marshal(payload)
		return event, true, nil
	}
	return event, realtimeEventVisible(data, event), nil
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
