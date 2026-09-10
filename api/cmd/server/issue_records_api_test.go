package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestDevelopmentDraftRetainsClientIDWithoutIssueHydration(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, authDisabled: true, uploadPath: t.TempDir()}
	handler := newHandler(s)
	var event domain.RealtimeEvent
	repository.SetRealtimeSink(func(_ string, received domain.RealtimeEvent) { event = received })
	r := httptest.NewRequest("POST", "/api/drafts", strings.NewReader(`{"type":"issue","title":"Unsent"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Client-ID", "draft-owner-client")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 201 || event.ClientID != "draft-owner-client" {
		t.Fatalf("draft echo would remount the editor: status=%d event=%+v", w.Code, event)
	}
}

func TestIssueRecordSnapshotCompactsOnlyAcknowledgedCollaborationUpdates(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issue-records", map[string]any{"title": "Collaboration snapshot"}, 201)
	documentID := "document_content_" + issue.ID
	for _, id := range []string{"collab-included", "collab-concurrent"} {
		_, err := repository.AppendDocumentCollaborationUpdate(context.Background(), "test-workspace", store.DocumentCollaborationUpdate{ID: id, DocumentID: documentID, ClientID: "client", Data: []byte{1, 2, 3}, CreatedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
	}
	requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+issue.ID, map[string]any{
		"description": "Saved", "descriptionData": map[string]any{"type": "doc"}, "contentState": "AQID", "expectedDocumentVersion": 0, "documentUpdateIds": []string{"collab-included"},
	}, 200)
	updates, err := repository.DocumentCollaborationUpdates(context.Background(), "test-workspace", documentID)
	if err != nil || len(updates) != 1 || updates[0].ID != "collab-concurrent" {
		t.Fatalf("snapshot lost concurrent updates or retained acknowledged updates: %+v %v", updates, err)
	}
	requestJSON[any](t, handler, "PATCH", "/api/issue-records/"+issue.ID, map[string]any{
		"descriptionData": map[string]any{"type": "doc"}, "expectedDocumentVersion": 0, "documentUpdateIds": []string{"collab-concurrent"},
	}, 409)
	updates, err = repository.DocumentCollaborationUpdates(context.Background(), "test-workspace", documentID)
	if err != nil || len(updates) != 1 {
		t.Fatal("conflicting snapshot pruned uncommitted updates")
	}
}

func TestIssueRecordsCreateUpdateAndContext(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	metadata := requestJSON[domain.Bootstrap](t, handler, "GET", "/api/issue-records/bootstrap", nil, 200)
	if !metadata.IssueCollectionPaged || len(metadata.Issues) != 0 {
		t.Fatal("paged bootstrap materialized issue collection")
	}
	created := requestJSON[domain.Issue](t, handler, "POST", "/api/issue-records", map[string]any{"title": "Created through row storage", "teamId": metadata.Teams[0].ID}, 201)
	updated := requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+created.ID, map[string]any{"title": "Updated through row storage", "priority": 1, "expectedVersion": created.Version}, 200)
	if updated.Version != created.Version+1 || updated.Priority != 1 {
		t.Fatalf("update %#v", updated)
	}
	requestJSON[any](t, handler, "PATCH", "/api/issue-records/"+created.ID, map[string]any{"title": "Stale write", "expectedVersion": created.Version}, 409)
	child := requestJSON[domain.Issue](t, handler, "POST", "/api/issue-records", map[string]any{"title": "Row child", "teamId": metadata.Teams[0].ID, "parentId": created.ID}, 201)
	if child.Number <= created.Number {
		t.Fatal("issue numbers were reused")
	}
	context := requestJSON[struct {
		Issue      domain.Issue           `json:"issue"`
		Related    []domain.Issue         `json:"relatedIssues"`
		Activities []domain.ActivityEvent `json:"activities"`
	}](t, handler, "GET", "/api/issue-records/"+created.Identifier+"/context", nil, 200)
	if context.Issue.Title != updated.Title || !slices.ContainsFunc(context.Related, func(issue domain.Issue) bool { return issue.ID == child.ID }) || len(context.Activities) != 2 {
		t.Fatalf("context lost state: %#v", context)
	}
	legacy := requestJSON[domain.Bootstrap](t, handler, "GET", "/api/bootstrap", nil, 200)
	if !slices.ContainsFunc(legacy.Issues, func(issue domain.Issue) bool { return issue.ID == created.ID && issue.Title == updated.Title }) {
		t.Fatal("compatibility bootstrap lost row mutation")
	}
	comment := requestJSON[domain.Comment](t, handler, "POST", "/api/issue-records/"+created.ID+"/comments", map[string]string{"body": "Native comment"}, 201)
	requestJSON[domain.Comment](t, handler, "PATCH", "/api/issue-records/"+created.ID+"/comments/"+comment.ID, map[string]any{"body": "Edited native comment", "expectedVersion": comment.Version}, 200)
	comments := requestJSON[struct {
		Comments []domain.Comment `json:"comments"`
	}](t, handler, "GET", "/api/issue-records/"+created.ID+"/context", nil, 200)
	if len(comments.Comments) != 1 || comments.Comments[0].Body != "Edited native comment" {
		t.Fatalf("comment persistence: %#v", comments)
	}
	requestJSON[any](t, handler, "DELETE", "/api/issue-records/"+created.ID+"/comments/"+comment.ID, nil, 204)
	requestJSON[any](t, handler, "DELETE", "/api/issue-records/"+created.ID, nil, 204)
	requestJSON[any](t, handler, "GET", "/api/issue-records/"+created.ID, nil, 404)
	child = requestJSON[domain.Issue](t, handler, "GET", "/api/issue-records/"+child.ID, nil, 200)
	if child.ParentID != nil {
		t.Fatal("deleted parent left a dangling child reference")
	}
}

func TestIssueRecordsRespectPrivateTeamsAndSharedPermissions(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	member, memberUser := verifiedAuthClient(t, host.URL, "Record reader", "record-reader@example.test")
	invite := authRequest[[]domain.Invitation](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{memberUser.Email}, "role": "member"}, "", 201)
	authRequest[domain.WorkspaceMembership](t, member, "POST", host.URL+"/api/invitations/accept", map[string]string{"token": invite[0].Token}, "", 200)
	team := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Private record team", "key": "REC", "private": true}, "", 201)
	issue := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issue-records", map[string]any{"title": "Private record", "teamId": team.ID}, "test-workspace", 201)
	authRequest[any](t, member, "GET", host.URL+"/api/issue-records/"+issue.ID, nil, "test-workspace", 404)
	authRequest[any](t, member, "POST", host.URL+"/api/issue-records", map[string]any{"title": "Forbidden", "teamId": team.ID}, "test-workspace", 403)
	permissions := authRequest[[]domain.IssuePermission](t, admin, "PUT", host.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": memberUser.ID, "role": "viewer"}}}, "test-workspace", 200)
	_ = permissions
	authRequest[domain.Issue](t, member, "GET", host.URL+"/api/issue-records/"+issue.ID, nil, "test-workspace", 200)
	authRequest[any](t, member, "PATCH", host.URL+"/api/issue-records/"+issue.ID, map[string]string{"title": "Not allowed"}, "test-workspace", 403)
}
