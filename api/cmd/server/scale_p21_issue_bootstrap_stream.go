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
	read, ok := s.beginIssueRecordsBootstrapRead(w, r)
	if !ok {
		return
	}
	defer read.Close()
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

// The caller owns the read through streaming or through its declared typed
// projection. All route filters and decoration errors share this boundary.
func (s *server) beginIssueRecordsBootstrapRead(w http.ResponseWriter, r *http.Request) (_ *store.IssueBootstrapRead, ok bool) {
	workspace := workspaceKey(r)
	s.setChangeCursor(w, r, workspace)
	principal := ""
	if !s.authDisabled {
		principal = authUser(r).ID
		if principal == "" {
			issueRecordsError(w, store.ErrAuthForbidden)
			return nil, false
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
			return nil, false
		}
		issueBootstrapError(w, err)
		return nil, false
	}
	defer func() {
		if !ok {
			read.Close()
		}
	}()
	data := read.Data
	if !s.authDisabled {
		filterBootstrapForAPIKey(&data, r)
	}
	data.IssueCollectionPaged = true
	data.CommentActions = nil
	if err := s.filterPreferenceIssueTeams(r, &data); err != nil {
		issueBootstrapError(w, err)
		return nil, false
	}
	if projectListBootstrapRequested(r) {
		store.ProjectListBootstrapProjection(&data)
	} else if r.Header.Get("X-Flow-Projection") == "issue-detail" {
		store.IssueDetailBootstrapProjection(&data)
	} else if err := s.store.PopulateReleaseProgress(r.Context(), &data); err != nil {
		issueBootstrapError(w, err)
		return nil, false
	}
	if err := s.decorateCycleCapacity(r.Context(), &data); err != nil {
		issueBootstrapError(w, err)
		return nil, false
	}
	if err := scaleP21SanitizeBootstrap(r.Context(), &data, read.StreamString); err != nil {
		issueBootstrapError(w, err)
		return nil, false
	}
	s.applyCalendarAway(r.Context(), &data)
	s.applyLoopRunCounts(r.Context(), data.Workspace.URLKey, data.Loops)
	data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	read.Data = data
	return read, true
}

// issueBootstrapError answers a bounded work refusal like the other bounded
// reads (503 with Retry-After); the original accessors had no budget. Every
// other failure keeps the original response.
func issueBootstrapError(w http.ResponseWriter, err error) {
	if !readLeafBusy(w, err) {
		issueRecordsError(w, err)
	}
}
