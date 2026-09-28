package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestProjectCommentThreadSubscriptionAndResolve covers Linear's "Subscribe to
// thread" / "Unsubscribe from thread" and "Resolve thread" actions on project
// comments.
func TestProjectCommentThreadSubscriptionAndResolve(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Threads", "teamIds": []string{"team_test"}}, http.StatusCreated)
	base := "/api/projects/" + project.ID + "/comments"
	root := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Root"}, http.StatusCreated)
	reply := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Reply", "parentId": root.ID}, http.StatusCreated)

	// Subscriptions always attach to the thread root and upsert per viewer.
	muted := requestJSON[domain.ThreadSubscription](t, handler, http.MethodPut, base+"/"+reply.ID+"/subscription", map[string]any{"state": "muted"}, http.StatusOK)
	if muted.CommentID != root.ID || muted.ProjectID != project.ID || muted.IssueID != "" || muted.State != "muted" || muted.UserID != "usr_admin" {
		t.Fatalf("muted subscription = %#v", muted)
	}
	subscribed := requestJSON[domain.ThreadSubscription](t, handler, http.MethodPut, base+"/"+root.ID+"/subscription", map[string]any{"state": "subscribed"}, http.StatusOK)
	if subscribed.ID != muted.ID || subscribed.State != "subscribed" {
		t.Fatalf("expected upsert of the same record, got %#v", subscribed)
	}
	requestJSON[any](t, handler, http.MethodPut, base+"/"+root.ID+"/subscription", map[string]any{"state": "loud"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPut, base+"/missing/subscription", map[string]any{"state": "muted"}, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodPut, "/api/projects/missing/comments/"+root.ID+"/subscription", map[string]any{"state": "muted"}, http.StatusNotFound)

	boot := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(boot.ThreadSubscriptions) != 1 || boot.ThreadSubscriptions[0].ProjectID != project.ID {
		t.Fatalf("bootstrap thread subscriptions = %#v", boot.ThreadSubscriptions)
	}
	requestJSON[any](t, handler, http.MethodDelete, base+"/"+root.ID+"/subscription", nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodDelete, base+"/missing/subscription", nil, http.StatusNotFound)
	boot = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(boot.ThreadSubscriptions) != 0 {
		t.Fatalf("expected cleared subscription, got %#v", boot.ThreadSubscriptions)
	}

	// Anyone may resolve a thread, even one another member started; resolving
	// from a reply resolves the thread root.
	foreign := "project_comment_foreign"
	if err := repository.MutateWorkspace(t.Context(), "", "project.commented", project.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Projects {
			if data.Projects[index].ID == project.ID {
				data.Projects[index].Comments = append(data.Projects[index].Comments, domain.Comment{ID: foreign, Version: 1, Body: "Theirs", Reactions: map[string][]string{}, User: domain.User{ID: "usr_other", Name: "Other"}})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resolved := requestJSON[domain.Comment](t, handler, http.MethodPatch, base+"/"+foreign, map[string]any{"resolved": true}, http.StatusOK)
	if !resolved.Resolved || resolved.Body != "Theirs" || resolved.EditedAt != nil || resolved.Version != 2 {
		t.Fatalf("resolved foreign comment = %#v", resolved)
	}
	resolved = requestJSON[domain.Comment](t, handler, http.MethodPatch, base+"/"+reply.ID, map[string]any{"resolved": true}, http.StatusOK)
	if resolved.ID != root.ID || !resolved.Resolved {
		t.Fatalf("resolving from a reply should resolve the root, got %#v", resolved)
	}
	stale := int64(1)
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{"resolved": false, "expectedVersion": stale}, http.StatusConflict)
	reopened := requestJSON[domain.Comment](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{"resolved": false}, http.StatusOK)
	if reopened.Resolved || reopened.ThreadSummary != nil {
		t.Fatalf("unresolved comment = %#v", reopened)
	}
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, base+"/missing", map[string]any{"resolved": true}, http.StatusNotFound)
	// Resolving does not grant edit rights over someone else's words.
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+foreign, map[string]any{"body": "Hijacked", "resolved": false}, http.StatusForbidden)

	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+project.ID, nil, http.StatusOK)
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	for _, want := range []string{"project.comment_thread_subscription", "project.comment_resolved", "project.comment_unresolved"} {
		if !slices.Contains(types, want) {
			t.Fatalf("missing %s in events %v", want, types)
		}
	}

	// Deleting the thread drops its subscriptions.
	requestJSON[domain.ThreadSubscription](t, handler, http.MethodPut, base+"/"+root.ID+"/subscription", map[string]any{"state": "muted"}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, base+"/"+root.ID, nil, http.StatusNoContent)
	boot = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(boot.ThreadSubscriptions) != 0 {
		t.Fatalf("expected thread subscription removed with its thread, got %#v", boot.ThreadSubscriptions)
	}
}
