package main

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"flow/api/internal/domain"
)

// A viewer's workspace projection (PagedWorkspaceMetadata) is a deep copy of
// the workspace metadata filtered for one user: tens of MiB in a large
// workspace. resourceAllowed builds one to authorize a resource route, and the
// route's handler used to build the same projection again moments later. The
// authorization hands its copy to the handler instead, within the request:
//
//   - it is keyed by workspace and user, so it is never served to anyone else;
//   - it is taken at most once, so two consumers never share (and mutate) one
//     copy; a second consumer builds its own as before;
//   - it is only handed on when the authorization did not change it.
type pagedMetadataHandoffKey struct{}

type pagedMetadataHandoff struct {
	workspace, userID string
	data              domain.Bootstrap
	taken             atomic.Bool
}

// issueWriteMetadataKey carries the projection the authorization of an issue
// create or update built to its handler's read before the write (the GET-only
// rule above protects reads after a change; this read precedes it). Only those
// two handlers take it.
type issueWriteMetadataKey struct{}

func handOffPagedMetadata(r *http.Request, workspace, userID string, data domain.Bootstrap) {
	handOffMetadata(r, pagedMetadataHandoffKey{}, workspace, userID, data)
}

func takePagedMetadata(r *http.Request, workspace, userID string) (domain.Bootstrap, bool) {
	return takeMetadata(r, pagedMetadataHandoffKey{}, workspace, userID)
}

func handOffIssueWriteMetadata(r *http.Request, workspace, userID string, data domain.Bootstrap) {
	handOffMetadata(r, issueWriteMetadataKey{}, workspace, userID, data)
}

func takeIssueWriteMetadata(r *http.Request, workspace, userID string) (domain.Bootstrap, bool) {
	return takeMetadata(r, issueWriteMetadataKey{}, workspace, userID)
}

func handOffMetadata(r *http.Request, key any, workspace, userID string, data domain.Bootstrap) {
	*r = *r.WithContext(context.WithValue(r.Context(), key, &pagedMetadataHandoff{workspace: workspace, userID: userID, data: data}))
}

// takeMetadata returns the projection the authorization built for this
// request's viewer and workspace, once.
func takeMetadata(r *http.Request, key any, workspace, userID string) (domain.Bootstrap, bool) {
	handoff, ok := r.Context().Value(key).(*pagedMetadataHandoff)
	if !ok || handoff.workspace != workspace || handoff.userID != userID || !handoff.taken.CompareAndSwap(false, true) {
		return domain.Bootstrap{}, false
	}
	data := handoff.data
	handoff.data = domain.Bootstrap{}
	return data, true
}

// issueWriteReadsMetadata reports the issue create and update routes whose
// handlers read the viewer's projection before they write.
func issueWriteReadsMetadata(r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" || parts[1] != "issue-records" {
		return false
	}
	return r.Method == http.MethodPost && len(parts) == 2 || r.Method == http.MethodPatch && len(parts) == 3 && parts[2] != "batch"
}
