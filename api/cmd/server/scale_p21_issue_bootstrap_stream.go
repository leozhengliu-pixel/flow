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

// issueBootstrapAttempts bounds the reads of one request when workspace
// authorization keeps changing under them (store.ErrAccessChanged).
const issueBootstrapAttempts = 3

// errIssueBootstrapWorkspaceNotFound is a development read of a missing
// workspace (answered 404 "workspace not found").
var errIssueBootstrapWorkspaceNotFound = errors.New("workspace not found")

// The HTTP stream owns opaque body references. In-process bootstrap consumers
// continue to use their independent typed input contract.
func (s *server) issueRecordsBootstrapBounded(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	// The read may come from the workspace's resident decoded image; this
	// handler then encodes Data itself, exactly as flag-off does.
	resident := r.WithContext(store.WithResidentBootstrap(r.Context()))
	var read *store.IssueBootstrapRead
	for attempt := 1; ; attempt++ {
		var err error
		read, err = s.openIssueRecordsBootstrapRead(w, resident)
		if err == nil {
			// Authority and the output's admission are settled before the
			// status is written: a refusal is still a response.
			if err = read.Prepare(r.Context()); err != nil {
				read.Close()
			}
		}
		if err == nil {
			break
		}
		// The flag-off bootstrap reads one consistent workspace; a bounded
		// read whose authorization moved under it is read again.
		if errors.Is(err, store.ErrAccessChanged) && attempt < issueBootstrapAttempts && r.Context().Err() == nil {
			continue
		}
		writeIssueBootstrapReadError(w, err)
		return
	}
	defer read.Close()
	w.Header().Add("Server-Timing", fmt.Sprintf("bootstrap;dur=%.2f", float64(time.Since(started).Microseconds())/1000))
	if read.Resident() {
		writeStreamedJSON(r.Context(), w, http.StatusOK, read.Data)
		return
	}
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
func (s *server) beginIssueRecordsBootstrapRead(w http.ResponseWriter, r *http.Request) (*store.IssueBootstrapRead, bool) {
	read, err := s.openIssueRecordsBootstrapRead(w, r)
	if err != nil {
		writeIssueBootstrapReadError(w, err)
		return nil, false
	}
	return read, true
}

func writeIssueBootstrapReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, errIssueBootstrapWorkspaceNotFound) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	issueBootstrapError(w, err)
}

// openIssueRecordsBootstrapRead is beginIssueRecordsBootstrapRead without
// answering its failure.
func (s *server) openIssueRecordsBootstrapRead(w http.ResponseWriter, r *http.Request) (_ *store.IssueBootstrapRead, err error) {
	workspace := workspaceKey(r)
	s.setChangeCursor(w, r, workspace)
	principal := ""
	if !s.authDisabled {
		principal = authUser(r).ID
		if principal == "" {
			return nil, store.ErrAuthForbidden
		}
	}
	var credential *domain.APIKey
	if key, found := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); found {
		credential = &key
	}
	read, err := s.store.BeginIssueBootstrapRead(r.Context(), workspace, principal, credential)
	if err != nil {
		if principal == "" && errors.Is(err, sql.ErrNoRows) {
			return nil, errIssueBootstrapWorkspaceNotFound
		}
		return nil, err
	}
	defer func() {
		if err != nil {
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
		return nil, err
	}
	if projectListBootstrapRequested(r) {
		store.ProjectListBootstrapProjection(&data)
	} else if r.Header.Get("X-Flow-Projection") == "issue-detail" {
		store.IssueDetailBootstrapProjection(&data)
	} else if err := s.store.PopulateReleaseProgress(r.Context(), &data); err != nil {
		return nil, err
	}
	if err := s.decorateCycleCapacity(r.Context(), &data); err != nil {
		return nil, err
	}
	if err := scaleP21SanitizeBootstrap(r.Context(), &data, read.StreamString); err != nil {
		return nil, err
	}
	s.applyCalendarAway(r.Context(), &data)
	s.hideUnreadableLoops(&data)
	s.applyLoopRunCounts(r.Context(), data.Workspace.URLKey, data.Loops)
	data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	read.Data = data
	return read, nil
}

// issueBootstrapError answers a bounded work refusal like the other bounded
// reads (503 with Retry-After); the original accessors had no budget. Every
// other failure keeps the original response.
func issueBootstrapError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrAccessChanged) {
		// Authorization kept changing under every read: a later read finds
		// it settled (a principal who lost access is refused by that read).
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusServiceUnavailable, "The workspace is busy loading for other people; retry in a moment")
		return
	}
	if !readLeafBusy(w, err) {
		issueRecordsError(w, err)
	}
}
