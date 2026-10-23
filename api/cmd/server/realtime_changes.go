package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

const (
	changeCursorHeader   = "X-Flow-Change-Cursor"
	defaultChangesLimit  = 200
	maxChangesLimit      = 500
	maxChangesScanRounds = 8
)

// ephemeralRealtimeEvent reports events that are never in the change log:
// they keep their own channels and never carry or advance a cursor.
func ephemeralRealtimeEvent(eventType string) bool {
	return eventType == "connected" || eventType == "presence.updated" || eventType == "workspace.resync_required" ||
		strings.HasPrefix(eventType, "agent_task.") && eventType != "agent_task.replied" || strings.HasPrefix(eventType, "loop_run.") || eventType == "notification.created" ||
		strings.HasPrefix(eventType, "coding_session.")
}

// stampChangeCursor attaches the persisted event's cursor, so SSE, socket and
// cross-node subscribers all see the same seq.
func (s *server) stampChangeCursor(event *domain.RealtimeEvent) {
	if event.Seq == 0 && !ephemeralRealtimeEvent(event.Type) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		event.Seq = s.store.DomainEventSeq(ctx, event.ID)
	}
}

// setChangeCursor exposes the workspace's current cursor on a snapshot
// response. Call it before reading the snapshot: the cursor is then never
// ahead of the data, and replaying from it only re-applies changes.
func (s *server) setChangeCursor(w http.ResponseWriter, r *http.Request, workspace string) {
	if head, err := s.store.WorkspaceChangeHead(r.Context(), workspace); err == nil {
		w.Header().Set(changeCursorHeader, strconv.FormatInt(head, 10))
	}
}

// realtimeChanges serves GET /api/realtime/changes?since=<cursor>&limit=N: the
// persisted changes after a cursor, filtered and projected for the viewer
// exactly like the SSE stream. next is the cursor to resume from; hasMore says
// to ask again immediately. resyncRequired means the cursor is unknown or
// older than retention: reload the snapshot (its cursor is in the bootstrap
// X-Flow-Change-Cursor header) and resume from head.
func (s *server) realtimeChanges(w http.ResponseWriter, r *http.Request) {
	data, _, err := s.pagedRealtimeMetadata(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	limit := defaultChangesLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, maxChangesLimit)
		}
	}
	since, parseErr := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("since")), 10, 64)
	type response struct {
		Events         []domain.RealtimeEvent `json:"events"`
		Next           int64                  `json:"next"`
		Head           int64                  `json:"head"`
		HasMore        bool                   `json:"hasMore"`
		ResyncRequired bool                   `json:"resyncRequired"`
	}
	result := response{Events: []domain.RealtimeEvent{}}
	if parseErr != nil {
		head, _ := s.store.WorkspaceChangeHead(r.Context(), data.Workspace.URLKey)
		result.Head, result.Next, result.ResyncRequired = head, head, true
		writeJSON(w, http.StatusOK, result)
		return
	}
	cursor := since
	for round := 0; round < maxChangesScanRounds && len(result.Events) < limit; round++ {
		page, err := s.store.WorkspaceChanges(r.Context(), data.Workspace.ID, cursor, limit-len(result.Events))
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Changes are temporarily unavailable")
			return
		}
		result.Head = page.Head
		if page.ResyncRequired {
			result.Events, result.Next, result.HasMore, result.ResyncRequired = []domain.RealtimeEvent{}, page.Head, false, true
			writeJSON(w, http.StatusOK, result)
			return
		}
		for _, record := range page.Records {
			cursor = record.Seq
			event := domain.RealtimeEvent{ID: record.Event.ID, Seq: record.Seq, Type: record.Event.Type, AggregateID: record.Event.AggregateID, ActorID: record.ActorID, Payload: record.Event.Payload, CreatedAt: record.Event.CreatedAt}
			projected, visible, err := s.pagedRealtimeEvent(r, event)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "Changes are temporarily unavailable")
				return
			}
			if visible {
				projected.Seq = record.Seq
				result.Events = append(result.Events, projected)
			}
		}
		result.HasMore = page.HasMore
		if !page.HasMore {
			break
		}
	}
	result.Next = cursor
	writeJSON(w, http.StatusOK, result)
}

const maxReplayPages = 20

// replayChanges writes the persisted changes after since to an SSE stream,
// filtered like live events. It returns the cursor it reached; a cursor that
// is unknown, older than retention or too far behind gets a resync marker.
func (s *server) replayChanges(w http.ResponseWriter, r *http.Request, workspaceID string, since int64, flusher http.Flusher) (int64, bool) {
	cursor := since
	resync := func() (int64, bool) {
		ok := writeSSE(w, domain.RealtimeEvent{ID: "resync_" + strconv.FormatInt(time.Now().UnixNano(), 10), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()}, s.realtime.node)
		flusher.Flush()
		return cursor, ok
	}
	for page := 0; page < maxReplayPages; page++ {
		changes, err := s.store.WorkspaceChanges(r.Context(), workspaceID, cursor, maxChangesLimit)
		if err != nil {
			return cursor, false
		}
		if changes.ResyncRequired {
			cursor = max(cursor, changes.Head)
			return resync()
		}
		for _, record := range changes.Records {
			cursor = record.Seq
			event := domain.RealtimeEvent{ID: record.Event.ID, Seq: record.Seq, Type: record.Event.Type, AggregateID: record.Event.AggregateID, ActorID: record.ActorID, Payload: record.Event.Payload, CreatedAt: record.Event.CreatedAt}
			projected, visible, err := s.pagedRealtimeEvent(r, event)
			if err != nil {
				return cursor, false
			}
			if visible {
				projected.Seq = record.Seq
				if !writeSSE(w, projected, s.realtime.node) {
					return cursor, false
				}
			}
		}
		flusher.Flush()
		if !changes.HasMore {
			return cursor, true
		}
	}
	return resync()
}
