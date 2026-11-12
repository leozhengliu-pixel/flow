package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// P10 registers GET /api/sync/notification-state inside the authenticated
// workspace middleware and adds it to the internal route inventory.
func (s *server) notificationState(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	viewer := requestActor(s, r).ID
	_, status, err := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, viewer)
	if err != nil || status != "active" {
		writeError(w, http.StatusForbidden, "You don't have access to this workspace")
		return
	}
	state, err := s.store.NotificationState(r.Context(), data.Workspace.URLKey, viewer)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, state)
}

// P12 calls this before its generic notification entity projection. A source
// envelope uses AggregateID=recipient; its payload is exactly NotificationState.
// Access must be resolved per sending batch, not retained from SSE connect.
// Non-recipients are rejected before authorization or unread SQL is performed.
func (s *server) projectNotificationState(ctx context.Context, access store.AccessContext, workspaceID, recipient string, state store.NotificationState) (store.NotificationState, bool, error) {
	if workspaceID == "" || workspaceID != access.WorkspaceID || recipient == "" || recipient != access.PrincipalID {
		return store.NotificationState{}, false, nil
	}
	if err := s.store.ValidateAccess(ctx, access); err != nil {
		return store.NotificationState{}, false, err
	}
	if _, err := strconv.ParseUint(state.Revision, 10, 63); err != nil || state.UnreadCount < 0 || len(state.ChangedIDs) > store.NotificationStateMaxChangedIDs {
		return store.NotificationState{}, false, nil
	}
	for _, id := range state.ChangedIDs {
		if id == "" || len(id) > 191 {
			return store.NotificationState{}, false, nil
		}
	}
	state.ChangedIDs = slices.Clone(state.ChangedIDs)
	return state, true, nil
}

func notificationStateRealtimeEvent(id, recipient string, state store.NotificationState) domain.RealtimeEvent {
	payload, _ := json.Marshal(state)
	return domain.RealtimeEvent{ID: id, Type: store.NotificationStateEvent, AggregateID: recipient, Payload: payload, CreatedAt: time.Now().UTC()}
}
