package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type notificationStateRetryKey struct{}

// P10 registers GET /api/notifications/state inside the authenticated
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
	var state store.NotificationState
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
		if !acquireProjection(r) {
			if retryAfter, refused := projectionRefused(r); refused {
				writeProjectionBusy(w, retryAfter)
			}
			return
		}
		access, accessErr := s.store.ResolveAccess(r.Context(), data.Workspace.URLKey, viewer, &key)
		if accessErr != nil {
			issueRecordsError(w, accessErr)
			return
		}
		state, err = s.scopedNotificationState(r.Context(), access, nil)
	} else {
		state, err = s.store.NotificationState(r.Context(), data.Workspace.URLKey, viewer)
	}
	if err != nil {
		// Credential-use telemetry also advances the current metadata epoch.
		// Discard a raced snapshot and authenticate again before retrying, so
		// changed or revoked credentials cannot retain their previous scope.
		attempt, _ := r.Context().Value(notificationStateRetryKey{}).(int)
		if attempt < 2 && r.Header.Get("Authorization") != "" && errors.Is(err, store.ErrAuthForbidden) {
			ctx := context.WithValue(r.Context(), notificationStateRetryKey{}, attempt+1)
			s.authenticate(http.HandlerFunc(s.notificationState)).ServeHTTP(w, r.WithContext(ctx))
			return
		}
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
	_, key, err := s.store.NotificationStateCredential(access)
	if err != nil {
		return store.NotificationState{}, false, err
	}
	if key != nil && apiKeyTeamRestrictionSelected(*key) {
		scoped, err := s.scopedNotificationState(ctx, access, &state)
		return scoped, err == nil, err
	}
	state.ChangedIDs = slices.Clone(state.ChangedIDs)
	return state, true, nil
}

// Use the notification list's credential projection for both recovery and
// pushes. A single read snapshot binds the scoped count, IDs and revision;
// revalidate outside that snapshot before releasing any sensitive result.
func (s *server) scopedNotificationState(ctx context.Context, access store.AccessContext, source *store.NotificationState) (store.NotificationState, error) {
	workspace, key, err := s.store.NotificationStateCredential(access)
	if err != nil {
		return store.NotificationState{}, err
	}
	if key == nil || !apiKeyTeamRestrictionSelected(*key) {
		return store.NotificationState{}, store.ErrAuthForbidden
	}
	read, release, err := s.store.BeginWorkspaceRead(ctx, workspace)
	if err != nil {
		return store.NotificationState{}, err
	}
	state, err := func() (store.NotificationState, error) {
		defer release()
		read = context.WithValue(read, authUserContextKey{}, domain.User{ID: access.PrincipalID})
		read = context.WithValue(read, credentialReadPrincipalKey{}, access.PrincipalID)
		read = context.WithValue(read, apiKeyContextKey{}, *key)
		request := (&http.Request{}).WithContext(read)
		data, err := s.store.PagedWorkspaceMetadata(read, workspace, access.PrincipalID)
		if err != nil {
			return store.NotificationState{}, err
		}
		filterBootstrapForAPIKey(&data, request)
		if err := s.filterPreferenceIssueTeams(request, &data); err != nil {
			return store.NotificationState{}, err
		}
		var result store.NotificationState
		result.Revision, err = s.store.NotificationStateRevision(read, workspace, access.PrincipalID)
		if err != nil {
			return result, err
		}
		result.UnreadCount, err = s.scopedNotificationUnreadCount(read, *key, data, store.NotificationQuery{Workspace: workspace, UserID: access.PrincipalID})
		if err != nil || source == nil {
			return result, err
		}
		result.Invalidated = source.Invalidated || source.Revision != result.Revision
		if result.Invalidated {
			return result, nil
		}
		view := data
		view.Notifications = nil
		for _, id := range source.ChangedIDs {
			item, err := s.store.NotificationRecord(read, workspace, access.PrincipalID, id)
			if errors.Is(err, sql.ErrNoRows) {
				result.Invalidated = true
				continue
			}
			if err != nil {
				return result, err
			}
			view.Notifications = append(view.Notifications, item)
		}
		if result.Invalidated {
			return result, nil
		}
		if err := s.filterPreferenceIssueTeams(request, &view); err != nil {
			return result, err
		}
		for _, item := range view.Notifications {
			result.ChangedIDs = append(result.ChangedIDs, item.ID)
		}
		return result, nil
	}()
	if err != nil {
		return store.NotificationState{}, err
	}
	if err := s.store.ValidateAccess(ctx, access); err != nil {
		return store.NotificationState{}, err
	}
	return state, nil
}

func notificationStateRealtimeEvent(id, recipient string, state store.NotificationState) domain.RealtimeEvent {
	payload, _ := json.Marshal(state)
	return domain.RealtimeEvent{ID: id, Type: store.NotificationStateEvent, AggregateID: recipient, Payload: payload, CreatedAt: time.Now().UTC()}
}
