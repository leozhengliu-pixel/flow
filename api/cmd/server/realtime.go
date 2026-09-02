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
	sockets     map[string]map[uint64]*realtimeSocketClient
}

func newRealtimeHub() *realtimeHub {
	return &realtimeHub{subscribers: map[string]map[uint64]chan domain.RealtimeEvent{}, presence: map[string]map[string]domain.Presence{}, sockets: map[string]map[uint64]*realtimeSocketClient{}}
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
			for len(channel) > 0 {
				<-channel
			}
			select {
			case channel <- domain.RealtimeEvent{ID: event.ID, Type: "workspace.resync_required", CreatedAt: event.CreatedAt}:
			default:
			}
		}
	}
}

func (h *realtimeHub) updatePresence(workspace, clientID string, user domain.User, issueID, route string) []domain.Presence {
	return h.updatePresenceWithDocument(workspace, clientID, user, issueID, "", route)
}

func (h *realtimeHub) updatePresenceWithDocument(workspace, clientID string, user domain.User, issueID, documentID, route string) []domain.Presence {
	h.mu.Lock()
	if h.presence[workspace] == nil {
		h.presence[workspace] = map[string]domain.Presence{}
	}
	h.presence[workspace][clientID] = domain.Presence{ClientID: clientID, User: user, IssueID: issueID, DocumentID: documentID, Route: route, LastSeenAt: time.Now().UTC()}
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
	data := s.workspaceData(r)
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
	presence = filterPresenceForViewer(data, presence)
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
			if !realtimeEventVisible(data, event) {
				continue
			}
			if event.Type == "presence.updated" {
				var payload struct {
					Presence []domain.Presence `json:"presence"`
				}
				if json.Unmarshal(event.Payload, &payload) == nil {
					payload.Presence = filterPresenceForViewer(data, payload.Presence)
					event.Payload, _ = json.Marshal(payload)
				}
			}
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

func filterPresenceForViewer(data domain.Bootstrap, values []domain.Presence) []domain.Presence {
	return slices.DeleteFunc(slices.Clone(values), func(item domain.Presence) bool {
		if item.IssueID != "" && !slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == item.IssueID }) {
			return true
		}
		if item.DocumentID != "" && !slices.ContainsFunc(data.Documents, func(document domain.Document) bool {
			return document.ID == item.DocumentID || document.SlugID == item.DocumentID
		}) {
			return true
		}
		return false
	})
}

func realtimeEventVisible(data domain.Bootstrap, event domain.RealtimeEvent) bool {
	if event.Type == "connected" || event.Type == "presence.updated" || strings.HasPrefix(event.Type, "workspace.") {
		return true
	}
	id := event.AggregateID
	if id == "" {
		return true
	}
	if strings.HasPrefix(event.Type, "issue.") || strings.HasPrefix(event.Type, "comment.") || strings.HasPrefix(event.Type, "attachment.") {
		return slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == id }) || slices.ContainsFunc(data.Documents, func(document domain.Document) bool { return document.ID == id })
	}
	if strings.HasPrefix(event.Type, "project.") || strings.HasPrefix(event.Type, "project_update.") || strings.HasPrefix(event.Type, "release.") {
		return slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == id }) || slices.ContainsFunc(data.Releases, func(release domain.Release) bool { return release.ID == id })
	}
	if strings.HasPrefix(event.Type, "initiative.") || strings.HasPrefix(event.Type, "initiative_update.") {
		return slices.ContainsFunc(data.Initiatives, func(initiative domain.Initiative) bool { return initiative.ID == id })
	}
	if strings.HasPrefix(event.Type, "document.") {
		return slices.ContainsFunc(data.Documents, func(document domain.Document) bool { return document.ID == id || document.SlugID == id })
	}
	if strings.HasPrefix(event.Type, "cycle.") {
		return slices.ContainsFunc(data.Cycles, func(cycle domain.Cycle) bool { return cycle.ID == id })
	}
	if strings.HasPrefix(event.Type, "customer_request.") {
		return slices.ContainsFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == id })
	}
	if strings.HasPrefix(event.Type, "ask.") {
		return slices.ContainsFunc(data.Asks, func(item domain.Ask) bool { return item.ID == id })
	}
	return true
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
		ClientID   string `json:"clientId"`
		IssueID    string `json:"issueId"`
		DocumentID string `json:"documentId"`
		Route      string `json:"route"`
		Active     *bool  `json:"active"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ClientID, input.IssueID, input.DocumentID, input.Route = strings.TrimSpace(input.ClientID), strings.TrimSpace(input.IssueID), strings.TrimSpace(input.DocumentID), strings.TrimSpace(input.Route)
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
	if input.DocumentID != "" {
		data := s.workspaceData(r)
		index := slices.IndexFunc(data.Documents, func(document domain.Document) bool {
			return document.ID == input.DocumentID || document.SlugID == input.DocumentID
		})
		if index < 0 || documentRole(s, data, data.Documents[index]) == "none" {
			writeError(w, http.StatusForbidden, "Document is outside your permissions")
			return
		}
	}
	workspace := workspaceKey(r)
	if s.coordinator != nil {
		presence, err := s.coordinator.UpdatePresence(r.Context(), workspace, input.ClientID, domain.Presence{ClientID: input.ClientID, User: authUser(r), IssueID: input.IssueID, DocumentID: input.DocumentID, Route: input.Route, LastSeenAt: time.Now().UTC()}, presenceTTL)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
			return
		}
		s.publishPresence(workspace, presence)
		writeJSON(w, http.StatusOK, presence)
		return
	}
	writeJSON(w, http.StatusOK, s.realtime.updatePresenceWithDocument(workspace, input.ClientID, authUser(r), input.IssueID, input.DocumentID, input.Route))
}

func (s *server) listPresence(w http.ResponseWriter, r *http.Request) {
	workspace := workspaceKey(r)
	presence, err := s.snapshotPresence(r.Context(), workspace)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
		return
	}
	issueID := strings.TrimSpace(r.URL.Query().Get("issueId"))
	documentID := strings.TrimSpace(r.URL.Query().Get("documentId"))
	route := strings.TrimSpace(r.URL.Query().Get("route"))
	if issueID != "" {
		data := s.workspaceData(r)
		if !slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == issueID }) {
			writeError(w, http.StatusForbidden, "Issue is outside your teams")
			return
		}
	}
	if documentID != "" {
		data := s.workspaceData(r)
		index := slices.IndexFunc(data.Documents, func(document domain.Document) bool { return document.ID == documentID || document.SlugID == documentID })
		if index < 0 || documentRole(s, data, data.Documents[index]) == "none" {
			writeError(w, http.StatusForbidden, "Document is outside your permissions")
			return
		}
	}
	if issueID != "" || documentID != "" || route != "" {
		presence = slices.DeleteFunc(presence, func(item domain.Presence) bool {
			return issueID != "" && item.IssueID != issueID || documentID != "" && item.DocumentID != documentID || route != "" && item.Route != route
		})
	}
	writeJSON(w, http.StatusOK, presence)
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
