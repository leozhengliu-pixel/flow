package main

import (
	"bufio"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) serveDocumentRecordList(w http.ResponseWriter, r *http.Request) bool {
	if documentRecordMode("FLOW_DOCUMENT_RECORDS") != "on" {
		return false
	}
	// The authorization handoff contains policy only for the list route.
	// Consume it before any canonical fallback can reach workspaceData.
	_, _ = takePagedMetadata(r, workspaceKey(r), authUser(r).ID)
	userID := authUser(r).ID
	if s.authDisabled {
		userID = ""
	}
	var directory store.DocumentRecordDirectory
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		directory, err = s.store.DocumentDirectoryMetadata(r.Context(), workspaceKey(r), userID, documentRecordCredential(r), documentRecordReadFields)
		if !errors.Is(err, store.ErrResourceAccessChanged) || !errors.Is(err, store.ErrAuthForbidden) {
			break
		}
	}
	if err != nil {
		if errors.Is(err, store.ErrAuthForbidden) {
			writeError(w, http.StatusForbidden, "This resource is outside your teams")
			return true
		}
		return false
	}
	filterBootstrapForAPIKey(&directory.Data, r)
	query := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("q")))
	teamID := strings.TrimSpace(r.URL.Query().Get("teamId"))
	archived := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("archived")))
	headers := slices.DeleteFunc(directory.Data.Documents, func(document domain.Document) bool {
		return !documentVisibleToViewer(s, directory.Data, document) || teamID != "" && !slices.Contains(document.TeamIDs, teamID) || archived != "all" && (archived == "true") != (document.ArchivedAt != nil)
	})
	slices.SortStableFunc(headers, func(a, b domain.Document) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	directory.Data.Documents = headers
	serveDetachedLegacyJSON(w, r, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buffer := bufio.NewWriterSize(w, 32<<10)
		buffer.WriteByte('[')
		first := true
		for _, header := range headers {
			if r.Context().Err() != nil {
				panic(http.ErrAbortHandler)
			}
			document, ok := directory.Load(header.ID)
			if !ok {
				panic(http.ErrAbortHandler)
			}
			if query != "" && !strings.Contains(strings.ToLower(document.Title+" "+document.Content), query) {
				continue
			}
			view := directory.Data
			view.Documents = []domain.Document{document}
			filterBootstrapForAPIKey(&view, r)
			if len(view.Documents) == 0 {
				continue
			}
			document = s.withDocumentVersions(r, view.Documents[0])
			if !first {
				buffer.WriteByte(',')
			}
			first = false
			if err := encodeHydratedJSON(r.Context(), buffer, reflect.ValueOf(document), 0); err != nil {
				panic(http.ErrAbortHandler)
			}
		}
		buffer.WriteString("]\n")
		if err := buffer.Flush(); err != nil {
			panic(http.ErrAbortHandler)
		}
	}))
	return true
}
