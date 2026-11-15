package main

import (
	"bufio"
	"errors"
	"log"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) serveDocumentRecordList(w http.ResponseWriter, r *http.Request) bool {
	if s.store.BoundedMetadataEnabled() {
		s.serveBoundedDocumentDirectoryList(w, r)
		return true
	}
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

func (s *server) serveBoundedDocumentDirectoryList(w http.ResponseWriter, r *http.Request) {
	_, _ = takePagedMetadata(r, workspaceKey(r), authUser(r).ID)
	userID := authUser(r).ID
	if s.authDisabled {
		userID = ""
	}
	var read *store.DocumentDirectoryRead
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		read, err = s.store.BeginDocumentDirectoryRead(r.Context(), workspaceKey(r), userID, documentRecordCredential(r))
		if !errors.Is(err, store.ErrResourceAccessChanged) || !errors.Is(err, store.ErrAuthForbidden) {
			break
		}
	}
	if err != nil {
		// The literal development accessor returns an empty list when its
		// installed workspace is absent.
		if s.authDisabled && errors.Is(err, store.ErrAuthForbidden) && !errors.Is(err, store.ErrResourceAccessChanged) {
			writeJSON(w, http.StatusOK, []domain.Document{})
			return
		}
		documentDirectoryError(w, err)
		return
	}
	defer read.Close()
	policy := read.Data
	policy.Documents = nil
	filterBootstrapForAPIKey(&policy, r)
	query := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("q")))
	teamID := strings.TrimSpace(r.URL.Query().Get("teamId"))
	archived := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("archived")))
	entries := slices.DeleteFunc(read.Entries, func(entry store.DocumentDirectoryEntry) bool {
		view := policy
		view.Documents = []domain.Document{entry.Document}
		filterBootstrapForAPIKey(&view, r)
		if len(view.Documents) == 0 {
			return true
		}
		document := view.Documents[0]
		return !documentVisibleToViewer(s, policy, document) || teamID != "" && !slices.Contains(document.TeamIDs, teamID) || archived != "all" && (archived == "true") != (document.ArchivedAt != nil)
	})
	slices.SortStableFunc(entries, func(a, b store.DocumentDirectoryEntry) int {
		return b.Document.UpdatedAt.Compare(a.Document.UpdatedAt)
	})
	release, err := legacyStreamLease(r.Context(), 256<<10)
	if err != nil {
		documentDirectoryError(w, err)
		return
	}
	defer release()
	file, _ := newLegacySpool()
	defer removeLegacySpool(file)
	spool := &legacyResponseSpool{ResponseWriter: w, ctx: r.Context(), file: file}
	spool.Header().Set("Content-Type", "application/json; charset=utf-8")
	spool.WriteHeader(http.StatusOK)
	buffer := bufio.NewWriterSize(spool, 32<<10)
	ctx := hydratedSigningContext(r.Context(), spool)
	err = func() error {
		if err := buffer.WriteByte('['); err != nil {
			return err
		}
		first := true
		for _, entry := range entries {
			matched, err := read.MatchSearch(r.Context(), entry.Ordinal, query)
			if err != nil {
				return err
			}
			if !matched {
				continue
			}
			if err := func() error {
				value, err := read.Load(r.Context(), entry.Ordinal)
				if err != nil {
					return err
				}
				defer value.Close()
				if value.RevisionError != nil {
					log.Printf("load document versions document=%s: %v", value.Document.ID, value.RevisionError)
				}
				if !first {
					if err := buffer.WriteByte(','); err != nil {
						return err
					}
				}
				first = false
				return encodeHydratedJSON(ctx, buffer, reflect.ValueOf(value.Document), 0)
			}(); err != nil {
				return err
			}
		}
		if err := read.Validate(r.Context()); err != nil {
			return err
		}
		if _, err := buffer.WriteString("]\n"); err != nil {
			return err
		}
		return buffer.Flush()
	}()
	read.Close()
	releaseHydratedProducer(r.Context())
	if err != nil || spool.err != nil || r.Context().Err() != nil {
		if err != nil && !spool.passthrough && r.Context().Err() == nil {
			documentDirectoryError(w, err)
			return
		}
		panic(http.ErrAbortHandler)
	}
	if !spool.passthrough && file != nil {
		if err := sendLegacySpool(r.Context(), w, spool.status, file); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}

func documentDirectoryError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrAuthForbidden) {
		writeError(w, http.StatusForbidden, "This resource is outside your teams")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "Workspace snapshot is busy; retry later")
}
