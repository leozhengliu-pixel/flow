package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const presenceTTL = 45 * time.Second

type realtimeHub struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[string]map[uint64]chan domain.RealtimeEvent
	presence    map[string]map[string]domain.Presence
}

func newRealtimeHub() *realtimeHub {
	return &realtimeHub{subscribers: map[string]map[uint64]chan domain.RealtimeEvent{}, presence: map[string]map[string]domain.Presence{}}
}

func (h *realtimeHub) subscribe(workspace string) (<-chan domain.RealtimeEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id := h.nextID
	if h.subscribers[workspace] == nil {
		h.subscribers[workspace] = map[uint64]chan domain.RealtimeEvent{}
	}
	channel := make(chan domain.RealtimeEvent, 64)
	h.subscribers[workspace][id] = channel
	return channel, func() {
		h.mu.Lock()
		if subscribers := h.subscribers[workspace]; subscribers != nil {
			delete(subscribers, id)
			if len(subscribers) == 0 {
				delete(h.subscribers, workspace)
			}
		}
		h.mu.Unlock()
	}
}

func (h *realtimeHub) publish(workspace string, event domain.RealtimeEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, channel := range h.subscribers[workspace] {
		select {
		case channel <- event:
		default:
			select {
			case <-channel:
			default:
			}
			select {
			case channel <- domain.RealtimeEvent{ID: event.ID, Type: "workspace.resync_required", CreatedAt: event.CreatedAt}:
			default:
			}
		}
	}
}

func (h *realtimeHub) updatePresence(workspace, clientID string, user domain.User, issueID, route string) []domain.Presence {
	h.mu.Lock()
	if h.presence[workspace] == nil {
		h.presence[workspace] = map[string]domain.Presence{}
	}
	h.presence[workspace][clientID] = domain.Presence{ClientID: clientID, User: user, IssueID: issueID, Route: route, LastSeenAt: time.Now().UTC()}
	presence := h.presenceLocked(workspace, time.Now())
	h.mu.Unlock()
	h.publishPresence(workspace, presence)
	return presence
}

func (h *realtimeHub) removePresence(workspace, clientID string) []domain.Presence {
	h.mu.Lock()
	delete(h.presence[workspace], clientID)
	presence := h.presenceLocked(workspace, time.Now())
	h.mu.Unlock()
	h.publishPresence(workspace, presence)
	return presence
}

func (h *realtimeHub) snapshotPresence(workspace string) []domain.Presence {
	h.mu.Lock()
	presence := h.presenceLocked(workspace, time.Now())
	h.mu.Unlock()
	return presence
}

func (h *realtimeHub) cleanupPresence(workspace string) {
	h.mu.Lock()
	before := len(h.presence[workspace])
	presence := h.presenceLocked(workspace, time.Now())
	changed := before != len(presence)
	h.mu.Unlock()
	if changed {
		h.publishPresence(workspace, presence)
	}
}

func (h *realtimeHub) presenceLocked(workspace string, now time.Time) []domain.Presence {
	for clientID, item := range h.presence[workspace] {
		if now.Sub(item.LastSeenAt) > presenceTTL {
			delete(h.presence[workspace], clientID)
		}
	}
	result := make([]domain.Presence, 0, len(h.presence[workspace]))
	for _, item := range h.presence[workspace] {
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right domain.Presence) int { return left.LastSeenAt.Compare(right.LastSeenAt) })
	return result
}

func (h *realtimeHub) publishPresence(workspace string, presence []domain.Presence) {
	payload, _ := json.Marshal(map[string]any{"presence": presence})
	h.publish(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("presence_%d", time.Now().UnixNano()), Type: "presence.updated", Payload: payload, CreatedAt: time.Now().UTC()})
}

func (s *server) realtimeEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "Streaming is unavailable")
		return
	}
	workspace := workspaceKey(r)
	presence, err := s.snapshotPresence(r.Context(), workspace)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	channel, unsubscribe := s.realtime.subscribe(workspace)
	defer unsubscribe()
	initial, _ := json.Marshal(map[string]any{"presence": presence})
	if !writeSSE(w, domain.RealtimeEvent{ID: fmt.Sprintf("connected_%d", time.Now().UnixNano()), Type: "connected", Payload: initial, CreatedAt: time.Now().UTC()}) {
		return
	}
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-channel:
			if !writeSSE(w, event) {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if s.coordinator != nil {
				presence, changed, err := s.coordinator.CleanupPresence(r.Context(), workspace, presenceTTL)
				if err == nil && changed {
					s.publishPresence(workspace, presence)
				}
			} else {
				s.realtime.cleanupPresence(workspace)
			}
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, event domain.RealtimeEvent) bool {
	raw, err := json.Marshal(event)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", event.ID, raw)
	return err == nil
}

func (s *server) updatePresence(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ClientID string `json:"clientId"`
		IssueID  string `json:"issueId"`
		Route    string `json:"route"`
		Active   *bool  `json:"active"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ClientID, input.IssueID, input.Route = strings.TrimSpace(input.ClientID), strings.TrimSpace(input.IssueID), strings.TrimSpace(input.Route)
	if input.ClientID == "" || len(input.ClientID) > 128 || len(input.Route) > 500 {
		writeError(w, http.StatusBadRequest, "clientId is required")
		return
	}
	if input.Active != nil && !*input.Active {
		workspace := workspaceKey(r)
		if s.coordinator != nil {
			presence, err := s.coordinator.RemovePresence(r.Context(), workspace, input.ClientID, presenceTTL)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
				return
			}
			s.publishPresence(workspace, presence)
			writeJSON(w, http.StatusOK, presence)
			return
		}
		writeJSON(w, http.StatusOK, s.realtime.removePresence(workspace, input.ClientID))
		return
	}
	if input.IssueID != "" {
		data := s.workspaceData(r)
		if !slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == input.IssueID }) {
			writeError(w, http.StatusForbidden, "Issue is outside your teams")
			return
		}
	}
	workspace := workspaceKey(r)
	if s.coordinator != nil {
		presence, err := s.coordinator.UpdatePresence(r.Context(), workspace, input.ClientID, domain.Presence{ClientID: input.ClientID, User: authUser(r), IssueID: input.IssueID, Route: input.Route, LastSeenAt: time.Now().UTC()}, presenceTTL)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
			return
		}
		s.publishPresence(workspace, presence)
		writeJSON(w, http.StatusOK, presence)
		return
	}
	writeJSON(w, http.StatusOK, s.realtime.updatePresence(workspace, input.ClientID, authUser(r), input.IssueID, input.Route))
}

func (s *server) snapshotPresence(ctx context.Context, workspace string) ([]domain.Presence, error) {
	if s.coordinator != nil {
		return s.coordinator.Presence(ctx, workspace, presenceTTL)
	}
	return s.realtime.snapshotPresence(workspace), nil
}

func (s *server) publishPresence(workspace string, presence []domain.Presence) {
	payload, _ := json.Marshal(map[string]any{"presence": presence})
	s.publishRealtime(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("presence_%d", time.Now().UnixNano()), Type: "presence.updated", Payload: payload, CreatedAt: time.Now().UTC()})
}
