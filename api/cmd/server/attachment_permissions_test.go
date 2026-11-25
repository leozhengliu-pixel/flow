package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A viewer of an issue reads its attachments and links but cannot add, edit or remove them; an editor can.
func TestAttachmentAndLinkWritesNeedEditAccess(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "attachment-permissions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer api.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	member, user := verifiedAuthClient(t, api.URL, "Viewer", "viewer@example.com")
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, api.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"viewer@example.com"}, "role": "member", "teamIds": []string{bootstrap.Teams[0].ID}}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, api.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)

	issue := authRequest[domain.Issue](t, admin, http.MethodPost, api.URL+"/api/issues", map[string]any{"title": "Attachment permissions", "teamId": bootstrap.Teams[0].ID}, "test-workspace", http.StatusCreated)
	link := authRequest[domain.Attachment](t, admin, http.MethodPost, api.URL+"/api/issues/"+issue.ID+"/links", map[string]string{"url": "https://example.com/spec", "title": "Spec"}, "test-workspace", http.StatusCreated)
	file := uploadAuthAttachment(t, admin, api.URL, issue.ID, "test-workspace", http.StatusCreated)
	authRequest[[]domain.IssuePermission](t, admin, http.MethodPut, api.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{
		"permissions": []map[string]string{{"subjectType": "user", "subjectId": user.ID, "role": "viewer"}},
	}, "test-workspace", http.StatusOK)

	base := api.URL + "/api/issues/" + issue.ID
	denied := func(label string, status int) {
		t.Helper()
		if status != http.StatusForbidden {
			t.Errorf("%s as a viewer = %d, want 403", label, status)
		}
	}
	status := func(method, path string, body any) int {
		t.Helper()
		request := authRequestStatus(t, member, method, api.URL+path, body, "test-workspace")
		return request
	}
	denied("add link", status(http.MethodPost, "/api/issues/"+issue.ID+"/links", map[string]string{"url": "https://example.com/other"}))
	denied("save link attachment", status(http.MethodPost, "/api/issues/"+issue.ID+"/link-attachments", map[string]string{"url": "https://example.com/other"}))
	denied("edit link", status(http.MethodPatch, "/api/issues/"+issue.ID+"/attachments/"+link.ID, map[string]string{"title": "Changed", "url": "https://example.com/changed"}))
	denied("delete link", status(http.MethodDelete, "/api/issues/"+issue.ID+"/attachments/"+link.ID, nil))
	denied("delete file", status(http.MethodDelete, "/api/issues/"+issue.ID+"/attachments/"+file.ID, nil))
	uploadAuthAttachment(t, member, api.URL, issue.ID, "test-workspace", http.StatusForbidden)
	read := authRequest[connection[domain.Attachment]](t, member, http.MethodGet, api.URL+"/api/attachments?issueId="+issue.ID, nil, "test-workspace", http.StatusOK)
	if len(read.Nodes) != 2 {
		t.Errorf("a viewer sees %d attachments, want 2", len(read.Nodes))
	}

	authRequest[[]domain.IssuePermission](t, admin, http.MethodPut, base+"/permissions", map[string]any{
		"permissions": []map[string]string{{"subjectType": "user", "subjectId": user.ID, "role": "editor"}},
	}, "test-workspace", http.StatusOK)
	edited := authRequest[domain.Attachment](t, member, http.MethodPatch, base+"/attachments/"+link.ID, map[string]string{"title": "Changed", "url": "https://example.com/changed"}, "test-workspace", http.StatusOK)
	if edited.URL != "https://example.com/changed" || edited.Title != "Changed" {
		t.Errorf("an editor's change = %#v", edited)
	}
	authRequest[any](t, member, http.MethodDelete, base+"/attachments/"+link.ID, nil, "test-workspace", http.StatusNoContent)
}
