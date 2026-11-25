package main

import (
	"context"
	"net/http"
)

func scaleP21SW253CollectionAuthorizationRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch r.URL.Path {
	case "/api/projects", "/api/initiatives", "/api/project-milestones",
		"/api/comments", "/api/issue-labels", "/api/issue-suggestions":
		return true
	case "/api/documents":
		return connectionRequested(r)
	}
	return false
}

// These exact collection roots have no per-resource authorization predicate.
// Initiatives retain the literal root's independent guest refusal.
func (s *server) boundedHotCollectionAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() || !scaleP21SW253CollectionAuthorizationRequest(r) {
		return false, false
	}
	request := r
	if workspaceKey(r) != workspace || authUser(r).ID != userID {
		// The legacy wrapper binds its explicit arguments, even when its
		// containing request names a different workspace or viewer.
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
	data, _, err := s.requestIssueQueryAccess(request)
	return err == nil && (r.URL.Path != "/api/initiatives" || data.ViewerRole != "guest"), true
}
