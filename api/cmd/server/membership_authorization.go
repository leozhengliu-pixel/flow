package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

func scaleP21SW306MembershipAuthorizationRequest(r *http.Request) bool {
	parts := strings.Split(r.URL.Path, "/")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if r.URL.Path == "/api/issues" || r.URL.Path == "/api/files/sign" {
			return true
		}
		if len(parts) != 3 || parts[0] != "" || parts[1] != "uploads" || parts[2] == "" {
			decoded, matched := scaleP21EscapedRouteParts(r, "/uploads/{name}")
			// These producers emit raw URLs with a nonreserved literal prefix.
			// Encoded filename slashes reach the leaf's basename rejection.
			return matched && len(decoded) >= 2 &&
				(strings.HasPrefix(decoded[1], "attachment_") || strings.HasPrefix(decoded[1], "customer_request_attachment_") ||
					strings.HasPrefix(decoded[1], "document_attachment_") || strings.HasPrefix(decoded[1], "update_attachment_") ||
					strings.HasPrefix(decoded[1], "project_comment_attachment_") || strings.HasPrefix(decoded[1], "initiative_comment_attachment_") ||
					strings.HasPrefix(decoded[1], "loop_attachment_") || strings.HasPrefix(decoded[1], "agent_attachment_") ||
					strings.HasPrefix(decoded[1], "mcp_"))
		}
		return true
	}
	if r.Method != http.MethodPost {
		return false
	}
	if r.URL.Path == "/api/desktop-notifications/ack" || r.URL.Path == "/api/agent/sessions/stream" {
		return true
	}
	workflow := strings.Split(r.URL.EscapedPath(), "/")
	if len(workflow) == 5 && len(parts) >= 5 && parts[0] == "" && parts[1] == "api" && parts[2] == "workflows" && workflow[3] != "" {
		tail, err := url.PathUnescape(workflow[4])
		if err == nil && tail == "run" {
			return true
		}
	}
	if _, matched := scaleP21EscapedRouteParts(r, "/api/workspaces/{workspaceKey}/teams"); matched {
		return true
	}
	for _, pattern := range []string{
		"/api/agent/sessions/{id}/messages/stream",
		"/api/agent/sessions/{id}/approvals/{approvalId}",
	} {
		if _, matched := scaleP21EscapedRouteParts(r, pattern); matched {
			return true
		}
	}
	return false
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
	if parts, matched := scaleP21EscapedRouteParts(request, "/api/workspaces/{workspaceKey}/teams"); matched && len(parts) >= 5 && parts[3] == "teams" {
		return s.boundedTeamRowAuthorization(request, workspace, userID, parts[4]), true
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
