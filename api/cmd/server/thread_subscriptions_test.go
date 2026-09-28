package main

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func threadRecipients(data *domain.Bootstrap) map[string]bool {
	result := map[string]bool{}
	for _, notification := range data.Notifications {
		result[notification.RecipientID] = true
	}
	return result
}

func TestThreadReplyFanOut(t *testing.T) {
	users := []domain.User{{ID: "ana"}, {ID: "ben"}, {ID: "cal"}, {ID: "dee"}, {ID: "eve"}}
	rootID := "c_root"
	issue := domain.Issue{ID: "issue_1", SubscriberIDs: []string{"dee", "eve"}}
	data := &domain.Bootstrap{
		Users: users,
		Comments: map[string][]domain.Comment{"issue_1": {
			{ID: rootID, User: users[0]},
			{ID: "c_reply1", ParentID: &rootID, User: users[1]},
			{ID: "c_reply2", ParentID: &rootID, User: users[2]},
			{ID: "c_other", User: users[4]},
		}},
		NotificationPreferences: map[string]domain.NotificationPreferences{},
		ThreadSubscriptions: []domain.ThreadSubscription{
			{UserID: "dee", IssueID: "issue_1", CommentID: rootID, State: "muted"},
		},
	}
	reply := data.Comments["issue_1"][2]
	activity := domain.ActivityEvent{ID: "a1", Type: "comment.created", Actor: users[2], CreatedAt: time.Now()}
	appendIssueNotifications(data, issue, activity, &reply)
	got := threadRecipients(data)
	// ana and ben participate; eve follows the issue; dee muted the thread; cal is the actor.
	for _, want := range []string{"ana", "ben", "eve"} {
		if !got[want] {
			t.Fatalf("expected %s notified, got %v", want, got)
		}
	}
	if got["dee"] || got["cal"] {
		t.Fatalf("muted user or actor notified: %v", got)
	}

	// A root comment on another thread does not reach participants of this one.
	data.Notifications = nil
	other := data.Comments["issue_1"][3]
	appendIssueNotifications(data, issue, domain.ActivityEvent{ID: "a2", Type: "comment.created", Actor: users[4], CreatedAt: time.Now()}, &other)
	if got := threadRecipients(data); got["ana"] || got["ben"] || !got["dee"] {
		t.Fatalf("unexpected recipients for a new thread: %v", got)
	}
}

func TestThreadSubscriptionEndpoints(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Thread", "teamId": "team_test"}, http.StatusCreated)
	root := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]any{"body": "Root"}, http.StatusCreated)
	reply := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]any{"body": "Reply", "parentId": root.ID}, http.StatusCreated)

	saved := requestJSON[domain.ThreadSubscription](t, handler, http.MethodPut, "/api/issues/"+issue.ID+"/comments/"+reply.ID+"/subscription", map[string]any{"state": "muted"}, http.StatusOK)
	if saved.CommentID != root.ID || saved.State != "muted" {
		t.Fatalf("expected muted subscription on the root, got %#v", saved)
	}
	updated := requestJSON[domain.ThreadSubscription](t, handler, http.MethodPut, "/api/issue-records/"+issue.ID+"/comments/"+root.ID+"/subscription", map[string]any{"state": "subscribed"}, http.StatusOK)
	if updated.ID != saved.ID || updated.State != "subscribed" {
		t.Fatalf("expected upsert of the same record, got %#v", updated)
	}
	_ = requestJSON[map[string]any](t, handler, http.MethodPut, "/api/issues/"+issue.ID+"/comments/"+root.ID+"/subscription", map[string]any{"state": "loud"}, http.StatusBadRequest)
	_ = requestJSON[map[string]any](t, handler, http.MethodPut, "/api/issues/"+issue.ID+"/comments/missing/subscription", map[string]any{"state": "muted"}, http.StatusNotFound)

	boot := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(boot.ThreadSubscriptions) != 1 {
		t.Fatalf("expected one thread subscription in bootstrap, got %#v", boot.ThreadSubscriptions)
	}
	_ = requestJSON[struct{}](t, handler, http.MethodDelete, "/api/issues/"+issue.ID+"/comments/"+root.ID, nil, http.StatusNoContent)
	boot = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(boot.ThreadSubscriptions) != 0 {
		t.Fatalf("expected thread subscription removed with its thread, got %#v", boot.ThreadSubscriptions)
	}
}
