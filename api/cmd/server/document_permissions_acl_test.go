package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestDocumentPermissionRoles exercises the complete document ACL matrix
// through the authenticated HTTP boundary, including comment ownership and
// editor moderation rules.
func TestDocumentPermissionRoles(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "document-acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()

	owner := authClient(t)
	authRequest[domain.AuthSession](t, owner, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	users := make([]struct {
		client *http.Client
		user   domain.User
	}, 4)
	for index, role := range []string{"commenter", "editor", "viewer", "outsider"} {
		client, user := verifiedAuthClient(t, server.URL, "Document "+role, "document-"+role+"@example.com")
		users[index] = struct {
			client *http.Client
			user   domain.User
		}{client: client, user: user}
		invitation := authRequest[[]domain.Invitation](t, owner, http.MethodPost, server.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{user.Email}, "role": "member"}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": invitation[0].Token}, "", http.StatusOK)
	}
	workspaceAdmin, workspaceAdminUser := verifiedAuthClient(t, server.URL, "Document admin", "document-admin@example.com")
	adminInvitation := authRequest[[]domain.Invitation](t, owner, http.MethodPost, server.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{workspaceAdminUser.Email}, "role": "admin"}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, workspaceAdmin, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": adminInvitation[0].Token}, "", http.StatusOK)

	document := authRequest[domain.Document](t, owner, http.MethodPost, server.URL+"/api/documents", map[string]any{"title": "ACL document", "content": "initial"}, "test-workspace", http.StatusCreated)
	permissions := []map[string]string{
		{"subjectType": "user", "subjectId": users[0].user.ID, "role": "commenter"},
		{"subjectType": "user", "subjectId": users[1].user.ID, "role": "editor"},
		{"subjectType": "user", "subjectId": users[2].user.ID, "role": "viewer"},
	}
	grants := authRequest[[]domain.DocumentPermission](t, owner, http.MethodPut, server.URL+"/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": permissions}, "test-workspace", http.StatusOK)
	if len(grants) != 4 || !slices.ContainsFunc(grants, func(item domain.DocumentPermission) bool { return item.Role == "owner" }) {
		t.Fatalf("canonical owner grant missing: %#v", grants)
	}

	// The explicitly shared viewer can read the document and its thread but
	// cannot write comments, reactions, or document content.
	viewer := users[2].client
	viewerBootstrap := authRequest[domain.Bootstrap](t, viewer, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(viewerBootstrap.Documents, func(item domain.Document) bool { return item.ID == document.ID }) {
		t.Fatal("viewer did not receive the explicitly shared document")
	}
	authRequest[[]domain.Comment](t, viewer, http.MethodGet, server.URL+"/api/documents/"+document.ID+"/comments", nil, "test-workspace", http.StatusOK)
	authRequest[any](t, viewer, http.MethodPost, server.URL+"/api/documents/"+document.ID+"/comments", map[string]string{"body": "viewer comment"}, "test-workspace", http.StatusForbidden)
	authRequest[any](t, viewer, http.MethodPatch, server.URL+"/api/documents/"+document.ID, map[string]string{"content": "viewer edit"}, "test-workspace", http.StatusForbidden)

	commenter := users[0].client
	comment := authRequest[domain.Comment](t, commenter, http.MethodPost, server.URL+"/api/documents/"+document.ID+"/comments", map[string]string{"body": "commenter comment"}, "test-workspace", http.StatusCreated)
	authRequest[domain.Comment](t, commenter, http.MethodPatch, server.URL+"/api/documents/"+document.ID+"/comments/"+comment.ID, map[string]string{"body": "edited by author"}, "test-workspace", http.StatusOK)
	authRequest[domain.Comment](t, commenter, http.MethodPost, server.URL+"/api/documents/"+document.ID+"/comments/"+comment.ID+"/reactions", map[string]string{"emoji": "👍"}, "test-workspace", http.StatusOK)

	editor := users[1].client
	secondComment := authRequest[domain.Comment](t, editor, http.MethodPost, server.URL+"/api/documents/"+document.ID+"/comments", map[string]string{"body": "editor comment"}, "test-workspace", http.StatusCreated)
	authRequest[domain.Comment](t, editor, http.MethodPatch, server.URL+"/api/documents/"+document.ID+"/comments/"+comment.ID, map[string]string{"body": "moderated by editor"}, "test-workspace", http.StatusOK)
	authRequest[any](t, commenter, http.MethodPatch, server.URL+"/api/documents/"+document.ID+"/comments/"+secondComment.ID, map[string]string{"body": "commenter cannot edit editor"}, "test-workspace", http.StatusForbidden)
	authRequest[any](t, commenter, http.MethodDelete, server.URL+"/api/documents/"+document.ID+"/comments/"+secondComment.ID, nil, "test-workspace", http.StatusForbidden)
	authRequest[any](t, editor, http.MethodDelete, server.URL+"/api/documents/"+document.ID+"/comments/"+comment.ID, nil, "test-workspace", http.StatusNoContent)
	authRequest[domain.Document](t, editor, http.MethodPatch, server.URL+"/api/documents/"+document.ID, map[string]string{"content": "editor edit"}, "test-workspace", http.StatusOK)
	authRequest[domain.Document](t, workspaceAdmin, http.MethodPatch, server.URL+"/api/documents/"+document.ID, map[string]string{"content": "admin edit"}, "test-workspace", http.StatusOK)

	// A workspace member with no document grant cannot discover it or mutate
	// its permissions, even though the document itself is unscoped.
	outsider := users[3].client
	if visible := authRequest[domain.Bootstrap](t, outsider, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK); slices.ContainsFunc(visible.Documents, func(item domain.Document) bool { return item.ID == document.ID }) {
		t.Fatal("ungranted document leaked to workspace member")
	}
	authRequest[any](t, outsider, http.MethodGet, server.URL+"/api/documents/"+document.ID+"/comments", nil, "test-workspace", http.StatusForbidden)
	authRequest[any](t, commenter, http.MethodPut, server.URL+"/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": permissions}, "test-workspace", http.StatusForbidden)
	// Replacing the ACL with an empty list still leaves the canonical owner
	// entry, which must make the document owner-only rather than public.
	authRequest[[]domain.DocumentPermission](t, owner, http.MethodPut, server.URL+"/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": []map[string]string{}}, "test-workspace", http.StatusOK)
	if visible := authRequest[domain.Bootstrap](t, outsider, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK); slices.ContainsFunc(visible.Documents, func(item domain.Document) bool { return item.ID == document.ID }) {
		t.Fatal("owner-only document leaked after clearing explicit grants")
	}
}
