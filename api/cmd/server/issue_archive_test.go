package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestOpenIssueDescriptionFollowsArchiveAndRestore(t *testing.T) {
	for _, path := range []string{"/api/issues/", "/api/issue-records/"} {
		t.Run(path, func(t *testing.T) {
			server, _ := newSingleAuthServerWithStore(t)
			fixture := newIssueAccessFixture(t, server.URL, server.URL, issueAccessSetup{role: "member", inTeam: true})
			for _, archived := range []bool{true, false} {
				authRequest[domain.Issue](t, fixture.admin, http.MethodPatch, server.URL+path+fixture.issue.ID, map[string]any{"archived": archived}, "test-workspace", http.StatusOK)
				notice := expectSocketNoticeWithin(t, fixture.socket, "document.access", fixture.documentID, 3*time.Second)
				if notice["readOnly"] != archived {
					t.Fatalf("archived=%v: access notice=%v", archived, notice)
				}
			}
		})
	}
}

func TestArchivedIssueContentRequiresSeparateRestoration(t *testing.T) {
	for _, path := range []string{"/api/issues", "/api/issue-records"} {
		t.Run(path, func(t *testing.T) {
			repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			s := &server{store: repo, authDisabled: true, uploadPath: t.TempDir()}
			handler := newHandler(s)
			defer s.stopDeliveryScheduler(context.Background())
			issue := requestJSON[domain.Issue](t, handler, http.MethodPost, path, map[string]any{"title": "Archive boundary", "description": "Original description"}, http.StatusCreated)
			archived := requestJSON[domain.Issue](t, handler, http.MethodPatch, path+"/"+issue.ID, map[string]any{"archived": true}, http.StatusOK)
			for _, input := range []map[string]any{
				{"title": "Changed"},
				{"description": "Changed"},
				{"priority": 1},
				{"archived": false, "title": "Changed"},
				{"archived": false, "priority": 1},
			} {
				requestJSON[any](t, handler, http.MethodPatch, path+"/"+issue.ID, input, http.StatusBadRequest)
			}
			other := requestJSON[domain.Issue](t, handler, http.MethodPost, path, map[string]any{"title": "Active batch sibling"}, http.StatusCreated)
			requestJSON[any](t, handler, http.MethodPost, path+"/batch", map[string]any{"issueIds": []string{other.ID, issue.ID}, "update": map[string]any{"priority": 1}}, http.StatusBadRequest)
			unchanged := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+other.ID, nil, http.StatusOK)
			if unchanged.Priority != other.Priority || unchanged.Version != other.Version {
				t.Fatal("rejected batch partially changed an active sibling")
			}
			saved := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID, nil, http.StatusOK)
			if saved.Title != issue.Title || saved.Description != issue.Description || saved.Priority != issue.Priority || saved.Version != archived.Version || saved.ArchivedAt == nil {
				t.Fatalf("rejected edit changed the archive: %+v", saved)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"?workspace=test-workspace", nil)
			if _, visible, editable, err := s.socketIssue(request, issue.ID); err != nil || !visible || editable {
				t.Fatalf("archived description socket: visible=%v editable=%v err=%v", visible, editable, err)
			}
			restored := requestJSON[domain.Issue](t, handler, http.MethodPatch, path+"/"+issue.ID, map[string]any{"archived": false, "expectedVersion": saved.Version}, http.StatusOK)
			if restored.ArchivedAt != nil {
				t.Fatal("restore left the issue archived")
			}
			requestJSON[domain.Issue](t, handler, http.MethodPatch, path+"/"+issue.ID, map[string]any{"title": "Editable again", "description": "Restored description", "priority": 1}, http.StatusOK)
			if _, _, editable, err := s.socketIssue(request, issue.ID); err != nil || !editable {
				t.Fatalf("restored description socket: editable=%v err=%v", editable, err)
			}
		})
	}
}
