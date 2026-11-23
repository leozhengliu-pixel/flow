package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type issueReadSnapshotKey struct{}
type issueReadRetryKey struct{}

type issueReadAttempt struct {
	*store.IssueReadSnapshot
	request http.Request
}

func (s *server) issueReadUsesSQL(r *http.Request, workspace string) bool {
	if r.Method != http.MethodGet || workspace == "" || s.coordinator == nil ||
		!s.store.BoundedMetadataEnabled() || !s.store.RecordJournalReady() || !boundedIssueAuthorizationRequest(r) {
		return false
	}
	mux, _ := r.Context().Value(coldAuthorizationMuxKey{}).(*http.ServeMux)
	if mux == nil {
		return false
	}
	_, pattern := mux.Handler(r)
	switch pattern {
	case "GET /api/issue-records/{id}", "GET /api/issue-records/{id}/history",
		"GET /api/issue-records", "GET /api/issue-records/groups":
		args := r.URL.Query()
		return args.Get("releaseId") == "" && args.Get("includeStatusHistory") != "true"
	}
	return false
}

func (s *server) pinIssueRead(w http.ResponseWriter, r *http.Request, workspace string) bool {
	read, err := s.store.BeginIssueReadSnapshot(r.Context(), workspace)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return false
	}
	if err != nil {
		issueRecordsErrorFor(w, r, err)
		return false
	}
	if !registerRequestReadClose(r.Context(), read.Close) {
		read.Close()
		issueRecordsErrorFor(w, r, store.ErrBoundedMetadataCoverage)
		return false
	}
	attempt := &issueReadAttempt{IssueReadSnapshot: read, request: *r}
	*r = *r.WithContext(context.WithValue(read.Context(), issueReadSnapshotKey{}, attempt))
	return true
}

func (s *server) writeIssueRead(w http.ResponseWriter, r *http.Request, data any) {
	if read, ok := r.Context().Value(issueReadSnapshotKey{}).(*issueReadAttempt); ok {
		live, err := read.End()
		if err != nil {
			issueRecordsErrorFor(w, r, err)
			return
		}
		if !s.authDisabled {
			if err := s.issueReadCredentialValid(live, r); err != nil {
				issueRecordsErrorFor(w, r, err)
				return
			}
			// Identity provider rules and session MFA can change independently
			// of the workspace epoch.
			if _, key := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); !key {
				data, _, err := s.requestIssueQueryAccess(r)
				if err != nil {
					issueRecordsErrorFor(w, r, err)
					return
				}
				if !s.authorizeAuthenticationPolicy(w, r.WithContext(live), data, data.ViewerRole) {
					return
				}
			}
		}
		if err := read.Validate(); err != nil {
			if errors.Is(err, store.ErrResourceAccessChanged) {
				if s.retryIssueRead(w, live, read) {
					return
				}
				err = store.ErrAuthForbidden
			}
			issueRecordsErrorFor(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, data)
}

// The epoch advances on ordinary edits as well as on revocations. Reauthorize
// and rerun the GET once using a fresh snapshot. The request's rate limit was
// already charged, and the first attempt wrote no response.
func (s *server) retryIssueRead(w http.ResponseWriter, live context.Context, read *issueReadAttempt) bool {
	mux, ok := live.Value(coldAuthorizationMuxKey{}).(*http.ServeMux)
	if !ok || live.Value(issueReadRetryKey{}) != nil || live.Err() != nil {
		return false
	}
	read.Close()
	req := read.request.WithContext(context.WithValue(live, issueReadRetryKey{}, true))
	if s.authorizeWorkspaceRequest(w, req, authUser(req)) {
		mux.ServeHTTP(w, req)
	}
	return true
}

// Authentication preceded the SQL snapshot. Recheck the credential as well
// as the epoch so a revocation in that interval cannot authorize delivery.
func (s *server) issueReadCredentialValid(ctx context.Context, r *http.Request) error {
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
			return store.ErrAuthForbidden
		}
		secret := strings.TrimSpace(header[len("Bearer "):])
		current, err := s.store.AuthenticateAPIKeyRecord(ctx, workspaceKey(r), secretHash(secret), applicationApproved)
		if err != nil {
			return err
		}
		if current.Key.ID != key.ID || current.User.ID != authUser(r).ID || !apiKeyAllowsRequest(r, current.Key) ||
			apiKeyTeamRestrictionSelected(current.Key) != apiKeyTeamRestrictionSelected(key) || !slices.Equal(current.Key.TeamIDs, key.TeamIDs) {
			return store.ErrAuthForbidden
		}
		return nil
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return store.ErrAuthForbidden
	}
	valid, err := s.store.SessionValid(ctx, cookie.Value, authUser(r).ID)
	if err != nil {
		return err // A failed session read is not a denial (database outages answer 503).
	}
	if !valid {
		return store.ErrAuthForbidden
	}
	return nil
}
