package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A document mention queued while the recipient had access is not delivered
// by email or digest once the access is gone (and not retried either).
func TestDocumentNotificationDeliveryRechecksAccess(t *testing.T) {
	for _, bounded := range []string{"off", "on"} {
		t.Run("bounded metadata "+bounded, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
			repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "document-delivery.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			serving := repository
			if bounded == "on" {
				if serving, err = repository.BoundedMetadataViewForTesting(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			mailer, sink := newSinkMailer(t)
			service := &server{store: serving, uploadPath: t.TempDir(), mailer: mailer}
			host := httptest.NewServer(newHandler(service))
			defer host.Close()
			const workspace = "test-workspace"
			url := func(path string) string { return host.URL + path }
			admin := authClient(t)
			adminSession := authRequest[domain.AuthSession](t, admin, http.MethodPost, url("/api/auth/login"), map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
			join := func(name, email string) (*http.Client, domain.User) {
				client, user := verifiedAuthClient(t, host.URL, name, email)
				invitation := authRequest[[]domain.Invitation](t, admin, http.MethodPost, url("/api/workspaces/"+workspace+"/invitations"), map[string]any{"emails": []string{user.Email}, "role": "member"}, "", http.StatusCreated)
				authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, url("/api/invitations/accept"), map[string]string{"token": invitation[0].Token}, "", http.StatusOK)
				authRequest[domain.Bootstrap](t, client, http.MethodGet, url("/api/bootstrap"), nil, workspace, http.StatusOK)
				return client, user
			}
			_, aliceUser := join("Alice Delivery", "alice-delivery@example.com")
			_, bobUser := join("Bob Delivery", "bob-delivery@example.com")

			mention := func(title string) domain.Document {
				document := authRequest[domain.Document](t, admin, http.MethodPost, url("/api/documents"), map[string]any{"title": title, "content": "Draft"}, workspace, http.StatusCreated)
				authRequest[[]domain.DocumentPermission](t, admin, http.MethodPut, url("/api/documents/"+document.ID+"/permissions"), map[string]any{"permissions": []map[string]string{
					{"subjectType": "user", "subjectId": aliceUser.ID, "role": "editor"},
					{"subjectType": "user", "subjectId": bobUser.ID, "role": "editor"},
				}}, workspace, http.StatusOK)
				for _, user := range []domain.User{aliceUser, bobUser} {
					authRequest[domain.Document](t, admin, http.MethodPatch, url("/api/documents/"+document.ID), map[string]any{"content": "Hello @" + user.Name, "contentData": mentionDocument(user, "Hello")}, workspace, http.StatusOK)
				}
				return document
			}
			open := mention("Open plan")
			secret := mention("Secret plan")
			// The recipients lose the second document after the notices were queued.
			authRequest[[]domain.DocumentPermission](t, admin, http.MethodPut, url("/api/documents/"+secret.ID+"/permissions"), map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": adminSession.User.ID, "role": "owner"}}}, workspace, http.StatusOK)

			// Alice's notices are due as immediate mail now (a backlog the dispatcher
			// meets after the access is gone); Bob's wait in the digest.
			sink.take()
			all, err := repository.NotificationDeliveries(t.Context(), workspace, []string{"pending", "delivering", "delivered", "failed", "digest"}, time.Now().UTC().Add(72*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			for _, delivery := range all.NotificationDeliveries {
				if delivery.RecipientID != aliceUser.ID || delivery.Channel != "email" {
					continue
				}
				if err := repository.MutateNotificationDelivery(t.Context(), workspace, delivery.ID, func(item *domain.NotificationDelivery) error {
					item.Status, item.DeliveredAt, item.NextAttemptAt = "pending", nil, nil
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			service.dispatchNotificationEmails(t.Context(), workspace)
			service.dispatchNotificationDigests(t.Context(), workspace, time.Now().UTC().Add(72*time.Hour))
			var mails []parsedMail
			secretSeen := false
			for _, message := range sink.take() {
				to := message.header.Get("To")
				if !strings.Contains(to, "alice-delivery") && !strings.Contains(to, "bob-delivery") {
					continue
				}
				text := message.text + message.html
				if strings.Contains(text, "Secret plan") {
					secretSeen = true
				}
				if strings.Contains(text, "Open plan") {
					mails = append(mails, message)
				}
			}
			if secretSeen {
				t.Fatal("a recipient without access was sent the document title")
			}
			if len(mails) != 2 {
				t.Fatalf("recipients with access got %d mails about the open document, want 2 (immediate and digest)", len(mails))
			}
			deliveries, err := repository.NotificationDeliveries(t.Context(), workspace, []string{"pending", "delivering", "delivered", "failed", "digest"}, time.Now().UTC().Add(72*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			notifications := repository.Bootstrap().Notifications
			for _, delivery := range deliveries.NotificationDeliveries {
				for _, notification := range notifications {
					if notification.ID != delivery.NotificationID || delivery.Channel != "email" {
						continue
					}
					if notification.SourceID == secret.ID && (delivery.Status != "failed" || delivery.Error != errDocumentDeliveryNoAccess || delivery.NextAttemptAt != nil) {
						t.Errorf("secret delivery %s = %q (%s), retry %v", delivery.ID, delivery.Status, delivery.Error, delivery.NextAttemptAt)
					}
					if notification.SourceID == open.ID && delivery.Status != "delivered" {
						t.Errorf("open delivery %s = %q (%s)", delivery.ID, delivery.Status, delivery.Error)
					}
				}
			}
		})
	}
}

// An admin does not read a document that sits only in a private team they are
// not in, so a notification about it does not reach them either.
func TestDocumentRoleForHonoursAdminClosedTeams(t *testing.T) {
	admin := domain.User{ID: "admin", Active: true}
	data := domain.Bootstrap{
		Viewer: domain.User{ID: "actor"}, ViewerRole: "member",
		Teams: []domain.Team{{ID: "closed", Private: true}, {ID: "open"}, {ID: "retired", Private: true, ArchivedAt: ptr(time.Now())}},
	}
	directory := memberDirectory{users: map[string]domain.User{"admin": admin}, roles: map[string]string{"admin": "admin"}, teams: map[string][]string{}}
	service := &server{}
	role := func(teamIDs ...string) string {
		return documentRoleFor(service, &data, directory, domain.Document{ID: "doc", TeamIDs: teamIDs, Creator: domain.User{ID: "actor"}}, "admin")
	}
	if got := role("closed"); got != "none" {
		t.Errorf("admin outside the private team = %q, want none", got)
	}
	if got := role("open"); got == "none" {
		t.Errorf("admin on an open team's document = none")
	}
	if got := role(); got == "none" {
		t.Errorf("admin on an unscoped document = none")
	}
	if got := role("retired"); got == "none" {
		t.Errorf("admin on a retired team's document = none")
	}
	directory.teams["admin"] = []string{"closed"}
	if got := role("closed"); got == "none" {
		t.Errorf("admin who is on the private team = none")
	}
}
