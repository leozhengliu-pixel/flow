package main

import (
	"context"
	"net/http"
)

func scaleP21SW256IssueLabelPointRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	_, matched := scaleP21EscapedRouteParts(r, "/api/issue-labels/{id}")
	return matched
}

// The literal wrapper has no issue-label predicate. The handler's existing
// directory reader separately decides label existence and visibility.
func (s *server) boundedIssueLabelPointAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() || !scaleP21SW256IssueLabelPointRequest(r) {
		return false, false
	}
	request := r
	if workspaceKey(r) != workspace || authUser(r).ID != userID {
		viewer := authUser(r)
		viewer.ID = userID
		ctx := context.WithValue(r.Context(), workspaceKeyContextKey{}, workspace)
		ctx = context.WithValue(ctx, authUserContextKey{}, viewer)
		request = r.Clone(ctx)
		request.Header.Set("X-Workspace-Key", workspace)
		query := request.URL.Query()
		query.Del("workspace")
		request.URL.RawQuery = query.Encode()
	}
	_, _, err := s.requestIssueQueryAccess(request)
	return err == nil, true
}
