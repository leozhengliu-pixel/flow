package main

import (
	"context"
	"net/http"
	"strings"
)

func scaleP21SW306MembershipAuthorizationRequest(r *http.Request) bool {
	parts := strings.Split(r.URL.Path, "/")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if r.URL.Path == "/api/issues" {
			return true
		}
		if len(parts) != 3 || parts[0] != "" || parts[1] != "uploads" || parts[2] == "" {
			return false
		}
		return true
	}
	if r.Method != http.MethodPost {
		return false
	}
	if r.URL.Path == "/api/desktop-notifications/ack" || r.URL.Path == "/api/agent/sessions/stream" {
		return true
	}
	if len(parts) == 5 && parts[0] == "" && parts[1] == "api" && parts[2] == "workspaces" && parts[3] != "" && parts[4] == "teams" {
		return true
	}
	return len(parts) == 7 && parts[0] == "" && parts[1] == "api" && parts[2] == "agent" &&
		parts[3] == "sessions" && parts[4] != "" &&
		(parts[5] == "messages" && parts[6] == "stream" || parts[5] == "approvals" && parts[6] != "")
}

// These exact roots have no target predicate after successful projection.
// Three upload names retain the literal switch's independent guest refusal.
func (s *server) boundedMembershipAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() || !scaleP21SW306MembershipAuthorizationRequest(r) {
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
	data, _, err := s.requestIssueQueryAccess(request)
	if err != nil {
		return false, true
	}
	switch r.URL.Path {
	case "/uploads/initiatives", "/uploads/customers", "/uploads/customer-requests":
		return data.ViewerRole != "guest", true
	}
	return true, true
}
