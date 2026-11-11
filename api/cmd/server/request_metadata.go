package main

import (
	"context"
	"net/http"
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

func handOffPagedMetadata(r *http.Request, workspace, userID string, data domain.Bootstrap) {
	*r = *r.WithContext(context.WithValue(r.Context(), pagedMetadataHandoffKey{}, &pagedMetadataHandoff{workspace: workspace, userID: userID, data: data}))
}

// takePagedMetadata returns the projection the authorization built for this
// request's viewer and workspace, once.
func takePagedMetadata(r *http.Request, workspace, userID string) (domain.Bootstrap, bool) {
	handoff, ok := r.Context().Value(pagedMetadataHandoffKey{}).(*pagedMetadataHandoff)
	if !ok || handoff.workspace != workspace || handoff.userID != userID || !handoff.taken.CompareAndSwap(false, true) {
		return domain.Bootstrap{}, false
	}
	data := handoff.data
	handoff.data = domain.Bootstrap{}
	return data, true
}
