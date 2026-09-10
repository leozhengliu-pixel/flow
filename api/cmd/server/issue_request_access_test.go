package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/store"
)

func TestIssueQueryAccessIsReusedOnlyWithinRequest(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	viewer, _, err := repository.Register(context.Background(), "Query member", "query-member@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.EnsureWorkspaceMembership(context.Background(), data.Workspace.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	s := &server{store: repository}
	r := httptest.NewRequest("GET", "/api/issue-records?workspace="+data.Workspace.URLKey, nil)
	r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, viewer))
	first, _, err := s.requestIssueQueryAccess(r)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.requestIssueQueryAccess(r)
	if err != nil || len(first.Teams) == 0 || &first.Teams[0] != &second.Teams[0] {
		t.Fatal("middleware and query copied policy twice")
	}
	if err := repository.SuspendMember(context.Background(), data.Workspace.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	next := httptest.NewRequest("GET", r.URL.String(), nil)
	next = next.WithContext(context.WithValue(next.Context(), authUserContextKey{}, viewer))
	if _, _, err := s.requestIssueQueryAccess(next); err == nil {
		t.Fatal("request cache hid a membership revocation")
	}
}

func TestRealtimeIssueQueryAccessIsRecheckedOnTheSameConnection(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	viewer, _, err := repository.Register(context.Background(), "Realtime member", "realtime-member@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.EnsureWorkspaceMembership(context.Background(), data.Workspace.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	s := &server{store: repository}
	r := httptest.NewRequest("GET", "/api/realtime/events?workspace="+data.Workspace.URLKey, nil)
	r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, viewer))
	if _, _, err := s.requestIssueQueryAccess(r); err != nil {
		t.Fatal(err)
	}
	if err := repository.SuspendMember(context.Background(), data.Workspace.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.requestIssueQueryAccess(r); err == nil {
		t.Fatal("long-lived realtime connection retained revoked workspace access")
	}
}

func TestRecentProjectUsesItsAuthorizedDirectoryRatherThanIssueQueryMetadata(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	project := data.Projects[0]
	body, _ := json.Marshal(map[string]string{"type": "project", "id": project.ID})
	r := httptest.NewRequest("POST", "/api/recent?workspace="+data.Workspace.URLKey, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, data.Viewer))
	s := &server{store: repository}
	w := httptest.NewRecorder()
	s.recordRecentResource(w, r)
	if w.Code != 204 {
		t.Fatalf("recent project rejected after policy-only query projection: %d %s", w.Code, w.Body.String())
	}
	recent, err := repository.RecentResources(r.Context(), data.Workspace.ID, data.Viewer.ID, 10)
	if err != nil || len(recent) != 1 || recent[0].ResourceID != project.ID {
		t.Fatalf("recent project missing: %+v %v", recent, err)
	}
}

func TestProjectIssueSummaryAuthorizesItsProjectWithoutTheFullDirectory(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	s := &server{store: repository}
	for _, projectID := range []string{data.Projects[0].ID, "not-a-project"} {
		r := httptest.NewRequest("GET", "/api/issue-records/project-summary?workspace="+data.Workspace.URLKey+"&projectId="+projectID, nil)
		r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, data.Viewer))
		w := httptest.NewRecorder()
		s.issueRecordProjectSummary(w, r)
		want := 200
		if projectID == "not-a-project" {
			want = 404
		}
		if w.Code != want {
			t.Fatalf("project summary status=%d want %d: %s", w.Code, want, w.Body.String())
		}
	}
}
