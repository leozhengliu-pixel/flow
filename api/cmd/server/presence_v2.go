package main

import (
	"context"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func minimalPresenceRequest(r *http.Request) bool {
	flag := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_PRESENCE_MINIMAL")))
	return flag != "off" && flag != "false" && flag != "0" && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Flow-Presence")), "minimal")
}

func (s *server) touchMinimalPresence(w http.ResponseWriter, r *http.Request, value domain.Presence, active bool) {
	workspace := workspaceKey(r)
	changed := false
	var err error
	if s.coordinator != nil {
		if active {
			changed, err = s.coordinator.TouchPresence(r.Context(), workspace, value.ClientID, value, presenceTTL)
		} else {
			changed, err = s.coordinator.DeletePresence(r.Context(), workspace, value.ClientID)
		}
	} else {
		h := s.realtime
		h.mu.Lock()
		if h.presence[workspace] == nil {
			h.presence[workspace] = map[string]domain.Presence{}
		}
		previous, exists := h.presence[workspace][value.ClientID]
		if active {
			expired := value.LastSeenAt.Sub(previous.LastSeenAt) > presenceTTL
			previous.LastSeenAt = value.LastSeenAt
			changed = !exists || expired || !reflect.DeepEqual(previous, value)
			h.presence[workspace][value.ClientID] = value
		} else {
			changed = exists
			delete(h.presence[workspace], value.ClientID)
		}
		h.mu.Unlock()
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
		return
	}
	if changed {
		// Old streams still consume full rosters. Read one only for a changed
		// resource/identity, coalesced per workspace, never for a TTL refresh.
		publish := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if roster, err := s.snapshotPresence(ctx, workspace); err == nil {
				s.sendPresence(workspace, roster)
			}
		}
		s.realtime.broadcastPresence(workspace, publish, publish)
	}
	w.WriteHeader(http.StatusNoContent)
}
