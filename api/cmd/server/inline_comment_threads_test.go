package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func inlineCommentTestHandler(t *testing.T) http.Handler {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	return newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
}

// TestProjectInlineCommentAnchors covers comments anchored to a selection of
// the project description ("Comment" on the description's selection toolbar).
func TestProjectInlineCommentAnchors(t *testing.T) {
	handler := inlineCommentTestHandler(t)
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Anchors", "teamIds": []string{"team_test"}}, http.StatusCreated)
	base := "/api/projects/" + project.ID + "/comments"

	root := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "About this", "anchorId": " cmt_anchor1 ", "quotedText": " charlie delta "}, http.StatusCreated)
	if root.AnchorID != "cmt_anchor1" || root.QuotedText != "charlie delta" || root.Version != 1 {
		t.Fatalf("anchored project comment = %#v", root)
	}
	reply := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Reply", "parentId": root.ID}, http.StatusCreated)
	if reply.AnchorID != "" || reply.ParentID == nil || *reply.ParentID != root.ID {
		t.Fatalf("reply = %#v", reply)
	}
	// The anchor belongs on the thread's first comment, is unique and bounded.
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"body": "Reply", "parentId": root.ID, "anchorId": "cmt_other"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"body": "Quote only", "quotedText": "charlie"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"body": "Long", "anchorId": strings.Repeat("a", 129)}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"body": "Huge quote", "anchorId": "cmt_huge", "quotedText": strings.Repeat("q", 4001)}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"body": "Again", "anchorId": "cmt_anchor1", "quotedText": "charlie"}, http.StatusConflict)
	// Page-level comments keep working without an anchor.
	plain := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Page comment"}, http.StatusCreated)
	if plain.AnchorID != "" || plain.QuotedText != "" {
		t.Fatalf("page-level comment = %#v", plain)
	}

	boot := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index := slices.IndexFunc(boot.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if index < 0 {
		t.Fatal("project missing from bootstrap")
	}
	stored := boot.Projects[index].Comments
	if len(stored) != 3 || stored[0].AnchorID != "cmt_anchor1" || stored[0].QuotedText != "charlie delta" {
		t.Fatalf("persisted project comments = %#v", stored)
	}

	// Resolving keeps the anchor; deleting the root removes its replies.
	resolved := requestJSON[domain.Comment](t, handler, http.MethodPatch, base+"/"+reply.ID, map[string]any{"resolved": true}, http.StatusOK)
	if resolved.ID != root.ID || !resolved.Resolved || resolved.AnchorID != "cmt_anchor1" {
		t.Fatalf("resolved thread = %#v", resolved)
	}
	requestJSON[any](t, handler, http.MethodDelete, base+"/"+root.ID, nil, http.StatusNoContent)
	boot = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index = slices.IndexFunc(boot.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if comments := boot.Projects[index].Comments; len(comments) != 1 || comments[0].ID != plain.ID {
		t.Fatalf("comments after deleting the thread = %#v", comments)
	}
}

// TestInitiativeInlineCommentThreads covers initiative comment threads: an
// anchored first comment, replies, resolve / reopen, thread subscriptions and
// deleting a thread.
func TestInitiativeInlineCommentThreads(t *testing.T) {
	handler := inlineCommentTestHandler(t)
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Threads"}, http.StatusCreated)
	base := "/api/initiatives/" + initiative.ID + "/comments"

	root := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "About this", "anchorId": "cmt_init1", "quotedText": "sierra tango"}, http.StatusCreated)
	if root.AnchorID != "cmt_init1" || root.QuotedText != "sierra tango" || root.Version != 1 || root.ParentID != nil {
		t.Fatalf("anchored initiative comment = %#v", root)
	}
	reply := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Reply", "parentId": root.ID}, http.StatusCreated)
	nested := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Reply to reply", "parentId": reply.ID}, http.StatusCreated)
	if reply.ParentID == nil || *reply.ParentID != root.ID || nested.ParentID == nil || *nested.ParentID != root.ID {
		t.Fatalf("replies should join the thread root: %#v %#v", reply, nested)
	}
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"body": "Orphan", "parentId": "missing"}, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"body": "Reply", "parentId": root.ID, "quotedText": "x", "anchorId": "cmt_x"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"body": "Again", "anchorId": "cmt_init1"}, http.StatusConflict)

	// Editing bumps the version; a stale version conflicts.
	edited := requestJSON[domain.Comment](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{"body": "About this (edited)", "expectedVersion": 1}, http.StatusOK)
	if edited.Version != 2 || edited.EditedAt == nil || edited.AnchorID != "cmt_init1" {
		t.Fatalf("edited comment = %#v", edited)
	}
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{"body": "Stale", "expectedVersion": 1}, http.StatusConflict)

	// Resolving from a reply resolves the root; reopening clears it.
	resolved := requestJSON[domain.Comment](t, handler, http.MethodPatch, base+"/"+nested.ID, map[string]any{"resolved": true}, http.StatusOK)
	if resolved.ID != root.ID || !resolved.Resolved || resolved.Body != "About this (edited)" {
		t.Fatalf("resolved thread = %#v", resolved)
	}
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{"resolved": false, "expectedVersion": 1}, http.StatusConflict)
	reopened := requestJSON[domain.Comment](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{"resolved": false}, http.StatusOK)
	if reopened.Resolved {
		t.Fatalf("reopened thread = %#v", reopened)
	}
	requestJSON[any](t, handler, http.MethodPatch, base+"/missing", map[string]any{"resolved": true}, http.StatusNotFound)

	// Thread subscriptions key on the root and upsert per viewer.
	muted := requestJSON[domain.ThreadSubscription](t, handler, http.MethodPut, base+"/"+reply.ID+"/subscription", map[string]any{"state": "muted"}, http.StatusOK)
	if muted.CommentID != root.ID || muted.InitiativeID != initiative.ID || muted.IssueID != "" || muted.ProjectID != "" || muted.State != "muted" {
		t.Fatalf("muted subscription = %#v", muted)
	}
	subscribed := requestJSON[domain.ThreadSubscription](t, handler, http.MethodPut, base+"/"+root.ID+"/subscription", map[string]any{"state": "subscribed"}, http.StatusOK)
	if subscribed.ID != muted.ID || subscribed.State != "subscribed" {
		t.Fatalf("expected an upsert of the same record, got %#v", subscribed)
	}
	requestJSON[any](t, handler, http.MethodPut, base+"/"+root.ID+"/subscription", map[string]any{"state": "loud"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPut, base+"/missing/subscription", map[string]any{"state": "muted"}, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodDelete, base+"/"+root.ID+"/subscription", nil, http.StatusNoContent)
	boot := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(boot.ThreadSubscriptions) != 0 {
		t.Fatalf("expected the subscription cleared, got %#v", boot.ThreadSubscriptions)
	}

	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+initiative.ID, nil, http.StatusOK)
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	for _, want := range []string{"initiative.commented", "initiative.comment_resolved", "initiative.comment_unresolved", "initiative.comment_thread_subscription"} {
		if !slices.Contains(types, want) {
			t.Fatalf("missing %s in events %v", want, types)
		}
	}

	// Deleting the first comment deletes the thread and its subscriptions.
	requestJSON[domain.ThreadSubscription](t, handler, http.MethodPut, base+"/"+root.ID+"/subscription", map[string]any{"state": "muted"}, http.StatusOK)
	plain := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Page comment"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, base+"/"+root.ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodDelete, base+"/"+root.ID, nil, http.StatusNotFound)
	boot = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index := slices.IndexFunc(boot.Initiatives, func(item domain.Initiative) bool { return item.ID == initiative.ID })
	if index < 0 {
		t.Fatal("initiative missing from bootstrap")
	}
	if comments := boot.Initiatives[index].Comments; len(comments) != 1 || comments[0].ID != plain.ID {
		t.Fatalf("comments after deleting the thread = %#v", comments)
	}
	if len(boot.ThreadSubscriptions) != 0 {
		t.Fatalf("expected the thread's subscriptions removed, got %#v", boot.ThreadSubscriptions)
	}
}
