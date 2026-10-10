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

type documentTestMember struct {
	client *http.Client
	user   domain.User
}

// documentNotificationFixture signs in the workspace owner and two members
// (an editor and a commenter on a fresh document the owner created).
func documentNotificationFixture(t *testing.T) (*httptest.Server, *http.Client, domain.User, documentTestMember, documentTestMember, domain.Document) {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "document-notifications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	t.Cleanup(server.Close)
	owner := authClient(t)
	session := authRequest[domain.AuthSession](t, owner, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	members := make([]documentTestMember, 2)
	for index, name := range []string{"Alice Editor", "Bob Commenter"} {
		client, user := verifiedAuthClient(t, server.URL, name, []string{"alice-doc@example.com", "bob-doc@example.com"}[index])
		invitation := authRequest[[]domain.Invitation](t, owner, http.MethodPost, server.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{user.Email}, "role": "member"}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": invitation[0].Token}, "", http.StatusOK)
		// A member joins the workspace's user list on their first visit.
		authRequest[domain.Bootstrap](t, client, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
		members[index] = documentTestMember{client: client, user: user}
	}
	document := authRequest[domain.Document](t, owner, http.MethodPost, server.URL+"/api/documents", map[string]any{"title": "Launch plan", "content": "Draft"}, "test-workspace", http.StatusCreated)
	authRequest[[]domain.DocumentPermission](t, owner, http.MethodPut, server.URL+"/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": []map[string]string{
		{"subjectType": "user", "subjectId": members[0].user.ID, "role": "editor"},
		{"subjectType": "user", "subjectId": members[1].user.ID, "role": "commenter"},
	}}, "test-workspace", http.StatusOK)
	return server, owner, session.User, members[0], members[1], document
}

func documentNotifications(t *testing.T, client *http.Client, baseURL string) []domain.Notification {
	t.Helper()
	page := authRequest[store.NotificationPage](t, client, http.MethodGet, baseURL+"/api/notifications?limit=100", nil, "test-workspace", http.StatusOK)
	return slices.DeleteFunc(page.Notifications, func(item domain.Notification) bool { return item.SourceType != "document" })
}

func notificationsOfType(items []domain.Notification, kind string) []domain.Notification {
	return slices.DeleteFunc(slices.Clone(items), func(item domain.Notification) bool { return item.Type != kind })
}

func mentionDocument(user domain.User, text string) map[string]any {
	return map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{
		map[string]any{"type": "text", "text": text + " "},
		map[string]any{"type": "mention", "attrs": map[string]any{"id": user.ID, "type": "user", "label": user.Name}},
	}}}}
}

func TestDocumentCommentNotifications(t *testing.T) {
	server, owner, ownerUser, alice, bob, document := documentNotificationFixture(t)
	base := server.URL + "/api/documents/" + document.ID

	// Bob subscribes himself, then starts an inline thread: the owner
	// (subscribed as creator) hears about it, Bob does not.
	authRequest[[]domain.User](t, bob.client, http.MethodPost, base+"/subscribers", map[string]any{"userIds": []string{bob.user.ID}}, "test-workspace", http.StatusOK)
	thread := authRequest[domain.Comment](t, bob.client, http.MethodPost, base+"/comments", map[string]any{"body": "Is this date final?", "anchorId": "thread-anchor-1", "quotedText": "Draft"}, "test-workspace", http.StatusCreated)
	if thread.AnchorID != "thread-anchor-1" || thread.QuotedText != "Draft" {
		t.Fatalf("inline anchor was not stored: %#v", thread)
	}
	authRequest[any](t, alice.client, http.MethodPost, base+"/comments", map[string]any{"body": "Duplicate", "anchorId": "thread-anchor-1"}, "test-workspace", http.StatusConflict)
	authRequest[any](t, alice.client, http.MethodPost, base+"/comments", map[string]any{"body": "Reply", "parentId": thread.ID, "anchorId": "x"}, "test-workspace", http.StatusBadRequest)
	ownerItems := documentNotifications(t, owner, server.URL)
	newComments := notificationsOfType(ownerItems, notificationDocumentNewComment)
	if len(newComments) != 1 || newComments[0].CommentID != thread.ID || newComments[0].Payload == nil || newComments[0].Payload.QuotedText != "Draft" || newComments[0].Payload.DocumentSlugID != document.SlugID || newComments[0].Title != "Launch plan" || newComments[0].Category != "comments" {
		t.Fatalf("owner new-comment notification = %#v", newComments)
	}
	if len(documentNotifications(t, bob.client, server.URL)) != 0 {
		t.Fatal("the comment author was notified about their own comment")
	}

	// Alice replies mentioning the owner: the owner gets a mention (not a
	// second new-comment notice), Bob as a thread participant gets a reply.
	reply := authRequest[domain.Comment](t, alice.client, http.MethodPost, base+"/comments", map[string]any{"body": "@" + ownerUser.Name + " can you confirm?", "parentId": thread.ID}, "test-workspace", http.StatusCreated)
	ownerItems = documentNotifications(t, owner, server.URL)
	if mentions := notificationsOfType(ownerItems, notificationDocumentCommentMention); len(mentions) != 1 || mentions[0].CommentID != reply.ID || mentions[0].Category != "mentions" {
		t.Fatalf("owner mention notifications = %#v", mentions)
	}
	if got := notificationsOfType(ownerItems, notificationDocumentNewComment); len(got) != 1 || got[0].OccurrenceCount != 1 {
		t.Fatalf("a mentioned subscriber also got a new-comment notice: %#v", got)
	}
	bobItems := documentNotifications(t, bob.client, server.URL)
	if got := notificationsOfType(bobItems, notificationDocumentNewComment); len(got) != 1 || got[0].CommentID != reply.ID {
		t.Fatalf("thread participant reply notifications = %#v", got)
	}

	// Reactions notify the comment's author; resolving notifies participants.
	authRequest[domain.Comment](t, alice.client, http.MethodPost, base+"/comments/"+thread.ID+"/reactions", map[string]string{"emoji": "👍"}, "test-workspace", http.StatusOK)
	bobItems = documentNotifications(t, bob.client, server.URL)
	if got := notificationsOfType(bobItems, notificationDocumentCommentReaction); len(got) != 1 || got[0].Payload == nil || got[0].Payload.Emoji != "👍" || got[0].Category != "reactions" {
		t.Fatalf("reaction notifications = %#v", got)
	}
	// Removing the reaction does not notify again.
	authRequest[domain.Comment](t, alice.client, http.MethodPost, base+"/comments/"+thread.ID+"/reactions", map[string]string{"emoji": "👍"}, "test-workspace", http.StatusOK)
	if got := notificationsOfType(documentNotifications(t, bob.client, server.URL), notificationDocumentCommentReaction); len(got) != 1 || got[0].OccurrenceCount != 1 {
		t.Fatalf("un-reacting notified again: %#v", got)
	}
	authRequest[domain.Comment](t, owner, http.MethodPatch, base+"/comments/"+thread.ID, map[string]any{"resolved": true}, "test-workspace", http.StatusOK)
	for _, member := range []documentTestMember{alice, bob} {
		if got := notificationsOfType(documentNotifications(t, member.client, server.URL), notificationDocumentThreadResolved); len(got) != 1 || got[0].CommentID != thread.ID {
			t.Fatalf("%s resolved-thread notifications = %#v", member.user.Name, got)
		}
	}
}

func TestDocumentContentAndLifecycleNotifications(t *testing.T) {
	server, owner, _, alice, bob, document := documentNotificationFixture(t)
	base := server.URL + "/api/documents/" + document.ID

	// The owner subscribes Bob: Bob is told, and sees edits from then on.
	subscribers := authRequest[[]domain.User](t, owner, http.MethodPost, base+"/subscribers", map[string]any{"userIds": []string{bob.user.ID}}, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(subscribers, func(user domain.User) bool { return user.ID == bob.user.ID }) {
		t.Fatalf("subscriber list = %#v", subscribers)
	}
	if got := notificationsOfType(documentNotifications(t, bob.client, server.URL), notificationDocumentSubscribed); len(got) != 1 || got[0].Category != "subscriptions" {
		t.Fatalf("subscribed notifications = %#v", got)
	}
	listed := authRequest[[]domain.User](t, bob.client, http.MethodGet, base+"/subscribers", nil, "test-workspace", http.StatusOK)
	if len(listed) != 2 {
		t.Fatalf("listed subscribers = %#v", listed)
	}
	// A commenter cannot be made to subscribe others without comment access
	// checks failing, and nobody can subscribe a user who cannot open it.
	authRequest[any](t, bob.client, http.MethodPost, base+"/subscribers", map[string]any{"userIds": []string{"usr_missing"}}, "test-workspace", http.StatusBadRequest)

	// Alice's edit mentions Bob: one mention, and one "edited" notice per
	// editing burst for subscribers.
	authRequest[domain.Document](t, alice.client, http.MethodPatch, base, map[string]any{"content": "Hello @Bob", "contentData": mentionDocument(bob.user, "Hello")}, "test-workspace", http.StatusOK)
	authRequest[domain.Document](t, alice.client, http.MethodPatch, base, map[string]any{"content": "Hello @Bob again", "contentData": mentionDocument(bob.user, "Hello again")}, "test-workspace", http.StatusOK)
	bobItems := documentNotifications(t, bob.client, server.URL)
	if got := notificationsOfType(bobItems, notificationDocumentMention); len(got) != 1 || got[0].OccurrenceCount != 1 || got[0].Category != "mentions" {
		t.Fatalf("mention notifications = %#v", got)
	}
	if got := notificationsOfType(bobItems, notificationDocumentChanges); len(got) != 1 || got[0].OccurrenceCount != 1 {
		t.Fatalf("edited notifications = %#v", got)
	}
	if got := notificationsOfType(documentNotifications(t, owner, server.URL), notificationDocumentChanges); len(got) != 1 {
		t.Fatalf("creator edited notifications = %#v", got)
	}

	// Moving the document to a team notifies subscribers.
	bootstrap := authRequest[domain.Bootstrap](t, owner, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	authRequest[domain.Document](t, owner, http.MethodPatch, base, map[string]any{"teamIds": []string{bootstrap.Teams[0].ID}}, "test-workspace", http.StatusOK)
	// Bob only has an explicit grant, so he still sees the moved document.
	if got := notificationsOfType(documentNotifications(t, bob.client, server.URL), notificationDocumentMoved); len(got) != 1 || got[0].Text == "" {
		t.Fatalf("moved notifications = %#v", got)
	}

	// Ownership changes reach the user who gained or lost it.
	authRequest[[]domain.DocumentPermission](t, owner, http.MethodPut, base+"/permissions", map[string]any{"permissions": []map[string]string{
		{"subjectType": "user", "subjectId": alice.user.ID, "role": "owner"},
		{"subjectType": "user", "subjectId": bob.user.ID, "role": "commenter"},
	}}, "test-workspace", http.StatusOK)
	if got := notificationsOfType(documentNotifications(t, alice.client, server.URL), notificationDocumentAddedAsOwner); len(got) != 1 {
		t.Fatalf("added-as-owner notifications = %#v", got)
	}
	authRequest[[]domain.DocumentPermission](t, owner, http.MethodPut, base+"/permissions", map[string]any{"permissions": []map[string]string{
		{"subjectType": "user", "subjectId": alice.user.ID, "role": "editor"},
		{"subjectType": "user", "subjectId": bob.user.ID, "role": "commenter"},
	}}, "test-workspace", http.StatusOK)
	if got := notificationsOfType(documentNotifications(t, alice.client, server.URL), notificationDocumentRemovedAsOwner); len(got) != 1 {
		t.Fatalf("removed-as-owner notifications = %#v", got)
	}

	// Unsubscribing someone else tells them; deleting and restoring tell the
	// remaining subscribers.
	authRequest[[]domain.User](t, owner, http.MethodDelete, base+"/subscribers/"+bob.user.ID, nil, "test-workspace", http.StatusOK)
	if got := notificationsOfType(documentNotifications(t, bob.client, server.URL), notificationDocumentUnsubscribed); len(got) != 1 {
		t.Fatalf("unsubscribed notifications = %#v", got)
	}
	authRequest[[]domain.User](t, alice.client, http.MethodPost, base+"/subscribers", map[string]any{"userIds": []string{alice.user.ID}}, "test-workspace", http.StatusOK)
	authRequest[any](t, owner, http.MethodDelete, base, nil, "test-workspace", http.StatusNoContent)
	if got := notificationsOfType(documentNotifications(t, alice.client, server.URL), notificationDocumentDeleted); len(got) != 1 {
		t.Fatalf("deleted notifications = %#v", got)
	}
	if got := notificationsOfType(documentNotifications(t, bob.client, server.URL), notificationDocumentDeleted); len(got) != 0 {
		t.Fatalf("an unsubscribed member heard about the deletion: %#v", got)
	}
	authRequest[domain.Document](t, owner, http.MethodPost, base+"/restore", nil, "test-workspace", http.StatusOK)
	if got := notificationsOfType(documentNotifications(t, alice.client, server.URL), notificationDocumentRestored); len(got) != 1 {
		t.Fatalf("restored notifications = %#v", got)
	}
}

func TestDocumentNotificationsRespectSettings(t *testing.T) {
	server, owner, ownerUser, _, bob, document := documentNotificationFixture(t)
	preferences := defaultPreferences(ownerUser.ID)
	preferences.Inbox.Categories["comments"] = false
	authRequest[domain.NotificationPreferences](t, owner, http.MethodPatch, server.URL+"/api/notification-preferences", preferences, "test-workspace", http.StatusOK)
	authRequest[domain.Comment](t, bob.client, http.MethodPost, server.URL+"/api/documents/"+document.ID+"/comments", map[string]any{"body": "Muted for the owner"}, "test-workspace", http.StatusCreated)
	if got := notificationsOfType(documentNotifications(t, owner, server.URL), notificationDocumentNewComment); len(got) != 0 {
		t.Fatalf("a disabled category still notified: %#v", got)
	}
	// Mentions are a separate category and still arrive.
	authRequest[domain.Comment](t, bob.client, http.MethodPost, server.URL+"/api/documents/"+document.ID+"/comments", map[string]any{"body": "@" + ownerUser.Name + " please look"}, "test-workspace", http.StatusCreated)
	if got := notificationsOfType(documentNotifications(t, owner, server.URL), notificationDocumentCommentMention); len(got) != 1 {
		t.Fatalf("mention notifications = %#v", got)
	}
}

func TestDocumentThreadSubscriptionMutesReplies(t *testing.T) {
	server, owner, _, alice, bob, document := documentNotificationFixture(t)
	base := server.URL + "/api/documents/" + document.ID
	thread := authRequest[domain.Comment](t, bob.client, http.MethodPost, base+"/comments", map[string]any{"body": "Thread"}, "test-workspace", http.StatusCreated)
	// The owner (a document subscriber) unsubscribes from this thread only.
	authRequest[domain.ThreadSubscription](t, owner, http.MethodPut, base+"/comments/"+thread.ID+"/subscription", map[string]string{"state": "muted"}, "test-workspace", http.StatusOK)
	authRequest[any](t, owner, http.MethodPut, base+"/comments/"+thread.ID+"/subscription", map[string]string{"state": "loud"}, "test-workspace", http.StatusBadRequest)
	before := len(notificationsOfType(documentNotifications(t, owner, server.URL), notificationDocumentNewComment))
	authRequest[domain.Comment](t, alice.client, http.MethodPost, base+"/comments", map[string]any{"body": "Reply", "parentId": thread.ID}, "test-workspace", http.StatusCreated)
	if got := notificationsOfType(documentNotifications(t, owner, server.URL), notificationDocumentNewComment); len(got) != before || got[0].OccurrenceCount != 1 {
		t.Fatalf("a muted thread still notified: %#v", got)
	}
	// Back to the default: replies reach document subscribers again.
	authRequest[any](t, owner, http.MethodDelete, base+"/comments/"+thread.ID+"/subscription", nil, "test-workspace", http.StatusNoContent)
	authRequest[domain.Comment](t, alice.client, http.MethodPost, base+"/comments", map[string]any{"body": "Another reply", "parentId": thread.ID}, "test-workspace", http.StatusCreated)
	if got := notificationsOfType(documentNotifications(t, owner, server.URL), notificationDocumentNewComment); len(got) != 1 || got[0].OccurrenceCount != 2 {
		t.Fatalf("unmuted thread notifications = %#v", got)
	}
}
