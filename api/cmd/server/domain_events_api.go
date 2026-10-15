package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"flow/api/internal/store"
)

const (
	defaultEventPageSize = 100
	maxEventPageSize     = 500
)

// events lists the domain events the requesting workspace recorded for one
// aggregate, for workspace administrators (adminOnlyRequest). Aggregate IDs
// repeat across workspaces, so events are matched on the recording workspace
// as well. The response stays an array of events, oldest first, and holds at
// most limit (default 100, max 500) of them; X-Next-Cursor continues it.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	aggregateID := strings.TrimSpace(r.URL.Query().Get("aggregateId"))
	if aggregateID == "" {
		writeError(w, http.StatusBadRequest, "aggregateId is required")
		return
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = defaultEventPageSize
	}
	limit = min(limit, maxEventPageSize)
	workspaceID, err := s.store.WorkspaceIDForKey(r.Context(), workspaceKey(r))
	if errors.Is(err, store.ErrAuthForbidden) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	events, next, err := s.store.EventsPage(r.Context(), workspaceID, aggregateID, r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, store.ErrInvalidEventCursor) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for index := range events {
		// Previous values name offloaded bodies; the log returns them whole.
		if loaded, loadErr := s.store.LoadOffloadedJSON(r.Context(), workspaceKey(r), events[index].PreviousValues); loadErr == nil {
			events[index].PreviousValues = loaded
		}
	}
	if next != "" {
		w.Header().Set("X-Next-Cursor", next)
	}
	writeJSON(w, http.StatusOK, events)
}
