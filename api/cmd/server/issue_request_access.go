package main

import (
	"context"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type issueRequestAccessKey struct{}
type issueRequestAccess struct {
	workspace string
	userID    string
	metadata  domain.Bootstrap
	access    store.IssueRecordAccess
	err       error
}

// Request lifetime only: membership revocation is checked again on the next
// request, while middleware and the handler share this one policy evaluation.
func (s *server) requestIssueQueryAccess(r *http.Request) (domain.Bootstrap, store.IssueRecordAccess, error) {
	workspace, userID := workspaceKey(r), authUser(r).ID
	// SSE reuses the HTTP request for its entire connection. Its individual
	// events must observe membership changes instead of retaining login access.
	cacheable := !pagedRealtimeRequest(r)
	if cached, ok := r.Context().Value(issueRequestAccessKey{}).(issueRequestAccess); cacheable && ok && cached.workspace == workspace && cached.userID == userID {
		return cached.metadata, cached.access, cached.err
	}
	cached := issueRequestAccess{workspace: workspace, userID: userID}
	if s.authDisabled {
		var ok bool
		cached.metadata, ok = s.store.IssueAccessMetadata(r.Context(), workspace)
		cached.metadata.ViewerRole = "admin"
		if !ok {
			cached.err = store.ErrAuthForbidden
		}
	} else {
		cached.metadata, cached.access, cached.err = s.store.IssueQueryAccess(r.Context(), workspace, userID)
	}
	if cacheable {
		*r = *r.WithContext(context.WithValue(r.Context(), issueRequestAccessKey{}, cached))
	}
	return cached.metadata, cached.access, cached.err
}

func boundedIssueAuthorizationRequest(r *http.Request) bool {
	return isIssueRecordsRequest(r) && issueRecordQueryOnly(r) || r.URL.Path == "/api/recent" || r.URL.Path == "/api/search" || r.URL.Path == "/api/search/semantic" || r.URL.Path == "/api/search/history"
}

func boundedSettingsDeletionRequest(r *http.Request) bool {
	if r.Method != http.MethodDelete {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) == 3 && (parts[1] == "labels" || parts[1] == "label-groups") || len(parts) == 5 && parts[1] == "teams" && (parts[3] == "labels" || parts[3] == "label-groups" || parts[3] == "states")
}
