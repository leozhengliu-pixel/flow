package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The HTTP stream owns opaque body references. In-process bootstrap consumers
// continue to use their independent typed input contract.
func (s *server) issueRecordsBootstrapBounded(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	workspace := workspaceKey(r)
	s.setChangeCursor(w, r, workspace)
	principal := ""
	if !s.authDisabled {
		principal = authUser(r).ID
		if principal == "" {
			issueRecordsError(w, store.ErrAuthForbidden)
			return
		}
	}
	var credential *domain.APIKey
	if key, found := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); found {
		credential = &key
	}
	read, err := s.store.BeginIssueBootstrapRead(r.Context(), workspace, principal, credential)
	if err != nil {
		if principal == "" && errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		issueRecordsError(w, err)
		return
	}
	defer read.Close()
	data := read.Data
	if !s.authDisabled {
		filterBootstrapForAPIKey(&data, r)
	}
	data.IssueCollectionPaged = true
	data.CommentActions = nil
	if err := s.filterPreferenceIssueTeams(r, &data); err != nil {
		issueRecordsError(w, err)
		return
	}
	if projectListBootstrapRequested(r) {
		store.ProjectListBootstrapProjection(&data)
	} else if r.Header.Get("X-Flow-Projection") == "issue-detail" {
		store.IssueDetailBootstrapProjection(&data)
	} else if err := s.store.PopulateReleaseProgress(r.Context(), &data); err != nil {
		issueRecordsError(w, err)
		return
	}
	if err := s.decorateCycleCapacity(r.Context(), &data); err != nil {
		issueRecordsError(w, err)
		return
	}
	if err := scaleP21SanitizeBootstrap(r.Context(), &data, read.StreamString); err != nil {
		issueRecordsError(w, err)
		return
	}
	s.applyCalendarAway(r.Context(), &data)
	s.applyLoopRunCounts(r.Context(), data.Workspace.URLKey, data.Loops)
	data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	read.Data = data
	w.Header().Add("Server-Timing", fmt.Sprintf("bootstrap;dur=%.2f", float64(time.Since(started).Microseconds())/1000))
	ctx := hydratedSigningContext(r.Context(), w)
	signing, _ := ctx.Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
	var rewrite store.MetadataJSONRewriter
	if signing != nil && signing.key != nil {
		rewrite = func(ctx context.Context, source store.MetadataJSONSource, dst io.Writer) error {
			return rewriteHydratedURLSource(ctx, source, source.Size(), dst, signing)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := read.WriteJSON(ctx, w, rewrite); err != nil {
		log.Printf("encode bounded bootstrap: %v", err)
		panic(http.ErrAbortHandler)
	}
}
