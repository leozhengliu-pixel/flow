package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestCommentResolveAndThreadSummary(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title":  "Resolve thread",
		"teamId": "team_test",
	}, http.StatusCreated)
	root := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]any{
		"body": "Root decision needed",
	}, http.StatusCreated)
	_ = requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]any{
		"body":     "Ship it",
		"parentId": root.ID,
	}, http.StatusCreated)

	_ = requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+issue.Team.ID+"/settings", map[string]any{
		"resolvedThreadSummaries": true,
	}, http.StatusOK)

	resolved := requestJSON[domain.Comment](t, handler, http.MethodPatch, "/api/issues/"+issue.ID+"/comments/"+root.ID, map[string]any{
		"resolved": true,
	}, http.StatusOK)
	if !resolved.Resolved {
		t.Fatalf("expected resolved comment, got %#v", resolved)
	}
	if resolved.ThreadSummary == nil || resolved.ThreadSummary.Content == "" {
		t.Fatalf("expected auto thread summary, got %#v", resolved.ThreadSummary)
	}

	reopened := requestJSON[domain.Comment](t, handler, http.MethodPatch, "/api/issues/"+issue.ID+"/comments/"+root.ID, map[string]any{
		"resolved": false,
	}, http.StatusOK)
	if reopened.Resolved {
		t.Fatalf("expected unresolved comment")
	}
	if reopened.ThreadSummary != nil {
		t.Fatalf("expected summary cleared on reopen, got %#v", reopened.ThreadSummary)
	}
}
