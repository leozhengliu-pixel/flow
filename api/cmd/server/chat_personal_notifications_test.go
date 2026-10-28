package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// notify records a notification for userID with the deliveries its
// preferences ask for, as notification creation does.
func chatPersonalNotify(t *testing.T, f *chatWorkflowFixture, userID, id, category, issueID string) {
	t.Helper()
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "test.chat_personal", id, nil, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		notification := domain.Notification{ID: id, RecipientID: userID, Category: category, Type: notificationType(category), IssueID: issueID, Actor: data.Viewer, CreatedAt: now, UpdatedAt: now}
		data.Notifications = append(data.Notifications, notification)
		enqueueNotificationDeliveries(data, notification, data.NotificationPreferences[userID])
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func chatPersonalPreferences(t *testing.T, f *chatWorkflowFixture, userID string, chat *domain.NotificationChannelPreferences) {
	t.Helper()
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "notification_preferences.updated", userID, nil, func(data *domain.Bootstrap) error {
		preferences := defaultPreferences(userID)
		preferences.Email.Enabled, preferences.Desktop.Enabled = false, false
		mergeNotificationPreferences(&preferences, domain.NotificationPreferences{Chat: chat})
		if data.NotificationPreferences == nil {
			data.NotificationPreferences = map[string]domain.NotificationPreferences{}
		}
		data.NotificationPreferences[userID] = preferences
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func chatPersonalDelivery(t *testing.T, f *chatWorkflowFixture, notificationID string) (domain.NotificationDelivery, bool) {
	t.Helper()
	data, ok := f.s.store.BootstrapFor(f.record.WorkspaceKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	for _, delivery := range data.NotificationDeliveries {
		if delivery.NotificationID == notificationID && delivery.Channel == "chat" {
			return delivery, true
		}
	}
	return domain.NotificationDelivery{}, false
}

func chatPersonalRun(t *testing.T, f *chatWorkflowFixture) {
	t.Helper()
	f.s.dispatchChatNotifications(t.Context(), f.record.WorkspaceKey)
	if err := f.s.processChatJobs(t.Context(), f.record.WorkspaceKey); err != nil {
		t.Fatal(err)
	}
}

func TestChatPersonalNotificationsAreBotDirectMessages(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	viewer, issue := data.Viewer.ID, data.Issues[0]
	// The app is not available to this account (its availability range).
	previous := http.DefaultTransport
	http.DefaultTransport = chatWorkflowTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/im/v1/messages") {
			raw, _ := io.ReadAll(r.Body)
			if r.URL.Query().Get("receive_id_type") != "open_id" && strings.Contains(string(raw), "ou_") {
				t.Errorf("personal message not addressed by open_id: %s", r.URL)
			}
			if strings.Contains(string(raw), `"receive_id":"ou_outside_range"`) {
				return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":230013,"msg":"Bot has NO availability to this user."}`)), Request: r}, nil
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })

	// Off by default: no chat delivery.
	chatPersonalNotify(t, f, viewer, "notification_off", "assignments", issue.ID)
	if _, ok := chatPersonalDelivery(t, f, "notification_off"); ok {
		t.Fatal("personal chat delivery without turning it on")
	}
	// On, with comments turned off.
	chatPersonalPreferences(t, f, viewer, &domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"comments": false}})
	chatPersonalNotify(t, f, viewer, "notification_assigned", "assignments", issue.ID)
	chatPersonalNotify(t, f, viewer, "notification_comment", "comments", issue.ID)
	if _, ok := chatPersonalDelivery(t, f, "notification_comment"); ok {
		t.Fatal("a type turned off was delivered")
	}
	chatPersonalRun(t, f)
	if len(f.sent) != 1 || f.sent[0]["receive_id"] != f.identity.OpenID || f.sent[0]["msg_type"] != "interactive" {
		delivery, _ := chatPersonalDelivery(t, f, "notification_assigned")
		t.Fatalf("personal notification not sent as a bot direct message: %#v %+v", f.sent, delivery)
	}
	content, _ := f.sent[0]["content"].(string)
	if !strings.Contains(content, "/issue/"+issue.Identifier) || !strings.Contains(content, "assigned the issue to you") || !strings.Contains(content, "Open in Flow") {
		t.Fatalf("personal notification content: %s", content)
	}
	if delivery, _ := chatPersonalDelivery(t, f, "notification_assigned"); delivery.Status != "delivered" || delivery.DeliveredAt == nil {
		t.Fatalf("delivery not recorded: %+v", delivery)
	}
	// Retried jobs do not send twice.
	chatPersonalRun(t, f)
	if len(f.sent) != 1 {
		t.Fatalf("delivered twice: %d", len(f.sent))
	}

	// The app is not available to the member's account: their own state.
	outside := f.identity
	outside.OpenID = "ou_outside_range"
	if err := f.s.store.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, outside); err != nil {
		t.Fatal(err)
	}
	chatPersonalNotify(t, f, viewer, "notification_outside", "assignments", issue.ID)
	chatPersonalRun(t, f)
	if delivery, _ := chatPersonalDelivery(t, f, "notification_outside"); delivery.Status != "failed" || delivery.Error != "chat_app_unavailable_to_user" {
		t.Fatalf("unavailable account delivery: %+v", delivery)
	}
	identity, err := f.s.store.ChatIdentityForUser(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, viewer)
	if err != nil || identity.PersonalDelivery != "unavailable" {
		t.Fatalf("unavailable state not recorded: %+v %v", identity, err)
	}
	connection, _ := f.s.store.WorkspaceMetadataFields(f.record.WorkspaceKey, "integrationConnections")
	for _, item := range connection.IntegrationConnections {
		if item.ID == f.record.ConnectionID && item.LastError != "" {
			t.Fatalf("a personal refusal marked the connection: %s", item.LastError)
		}
	}
	// Back in range: delivered again, and the state clears.
	if err := f.s.store.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, f.identity); err != nil {
		t.Fatal(err)
	}
	chatPersonalNotify(t, f, viewer, "notification_back", "assignments", issue.ID)
	chatPersonalRun(t, f)
	if identity, _ := f.s.store.ChatIdentityForUser(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, viewer); identity.PersonalDelivery != "" || len(f.sent) != 2 {
		t.Fatalf("delivery after returning to range: %+v %d", identity, len(f.sent))
	}

	// Unlinking stops delivery, also for one already queued.
	chatPersonalNotify(t, f, viewer, "notification_queued", "assignments", issue.ID)
	f.s.dispatchChatNotifications(t.Context(), f.record.WorkspaceKey)
	if err := f.s.store.DeleteChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, viewer); err != nil {
		t.Fatal(err)
	}
	if err := f.s.processChatJobs(t.Context(), f.record.WorkspaceKey); err != nil {
		t.Fatal(err)
	}
	chatPersonalNotify(t, f, viewer, "notification_unlinked", "assignments", issue.ID)
	chatPersonalRun(t, f)
	for _, id := range []string{"notification_queued", "notification_unlinked"} {
		if delivery, _ := chatPersonalDelivery(t, f, id); delivery.Status != "failed" || delivery.Error != "chat_not_linked" {
			t.Fatalf("%s after unlinking: %+v", id, delivery)
		}
	}
	if len(f.sent) != 2 {
		t.Fatalf("sent after unlinking: %d", len(f.sent))
	}
}

// A personal message never discloses an issue the member can no longer open,
// and stops while the tenant is not approved.
func TestChatPersonalNotificationsRespectAccessAndConnectionHealth(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	member, _, err := f.s.store.LoginExternal(t.Context(), "chat-personal@example.test", "Personal member", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.EnsureWorkspaceMembership(t.Context(), data.Workspace.ID, member.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, member.User.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, domain.ChatIdentity{UserID: member.User.ID, OpenID: "ou_member_personal", TenantKey: f.record.TenantKey, AuthenticatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	chatPersonalPreferences(t, f, member.User.ID, &domain.NotificationChannelPreferences{Enabled: true})
	if err = f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "team.updated", issue.Team.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			if data.Teams[index].ID == issue.Team.ID {
				data.Teams[index].Private = true
				settings := data.TeamSettings[issue.Team.ID]
				settings.Access = "private"
				data.TeamSettings[issue.Team.ID] = settings
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.SetTeamMembership(t.Context(), data.Workspace.ID, issue.Team.ID, member.User.ID, "", false); err != nil {
		t.Fatal(err)
	}
	chatPersonalNotify(t, f, member.User.ID, "notification_private", "subscriptions", issue.ID)
	chatPersonalRun(t, f)
	if delivery, _ := chatPersonalDelivery(t, f, "notification_private"); delivery.Status != "failed" || delivery.Error != "chat_recipient_no_access" || len(f.sent) != 0 {
		t.Fatalf("private issue disclosed: %+v %#v", delivery, f.sent)
	}

	// Leaving the workspace stops delivery, even of a public issue.
	if err = f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "team.updated", issue.Team.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			if data.Teams[index].ID == issue.Team.ID {
				data.Teams[index].Private = false
				settings := data.TeamSettings[issue.Team.ID]
				settings.Access = "public"
				data.TeamSettings[issue.Team.ID] = settings
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	chatPersonalNotify(t, f, member.User.ID, "notification_public", "subscriptions", issue.ID)
	if err = f.s.store.RemoveMember(t.Context(), data.Workspace.ID, member.User.ID); err != nil {
		t.Fatal(err)
	}
	chatPersonalRun(t, f)
	if delivery, _ := chatPersonalDelivery(t, f, "notification_public"); delivery.Status != "failed" || delivery.Error != "chat_recipient_no_access" || len(f.sent) != 0 {
		t.Fatalf("delivered after leaving the workspace: %+v %#v", delivery, f.sent)
	}

	// Not approved in the tenant: nothing is sent.
	record := f.record
	record.Connected = false
	if err = f.s.store.PutChatIntegration(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	chatPersonalPreferences(t, f, data.Viewer.ID, &domain.NotificationChannelPreferences{Enabled: true})
	chatPersonalNotify(t, f, data.Viewer.ID, "notification_unhealthy", "assignments", issue.ID)
	chatPersonalRun(t, f)
	if delivery, _ := chatPersonalDelivery(t, f, "notification_unhealthy"); delivery.Status != "failed" || delivery.Error != "chat_tenant_not_approved" || len(f.sent) != 0 {
		t.Fatalf("sent while not approved: %+v %#v", delivery, f.sent)
	}
}

func TestChatPersonalNotificationPreferencesKeepUnsentChannel(t *testing.T) {
	current := defaultPreferences("user")
	mergeNotificationPreferences(&current, domain.NotificationPreferences{Chat: &domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"comments": false, "bogus": true}}})
	if current.Chat == nil || !current.Chat.Enabled || current.Chat.Categories["comments"] || current.Chat.Categories["bogus"] {
		t.Fatalf("chat preferences: %+v", current.Chat)
	}
	// A client that does not know the chat channel leaves it alone.
	mergeNotificationPreferences(&current, domain.NotificationPreferences{Email: current.Email, Desktop: current.Desktop, Inbox: current.Inbox})
	if current.Chat == nil || !current.Chat.Enabled {
		t.Fatal("an update without the chat channel turned it off")
	}
}
