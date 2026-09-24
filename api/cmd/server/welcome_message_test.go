package main

import (
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestAppendWelcomeNotification(t *testing.T) {
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "workspace_1"},
		Viewer:    domain.User{ID: "user_admin"},
		Users:     []domain.User{{ID: "user_admin"}, {ID: "user_editor", DisplayName: "Editor"}},
	}
	if appendWelcomeNotification(&data, "user_new", time.Now()) {
		t.Fatal("an empty welcome message must not be sent")
	}
	data.WorkspaceSettings.WelcomeMessage = "Hello"
	data.WorkspaceSettings.WelcomeMessageEditedByID = "user_editor"
	if !appendWelcomeNotification(&data, "user_new", time.Now()) {
		t.Fatal("expected the welcome message to be sent")
	}
	if len(data.Notifications) != 1 {
		t.Fatalf("expected one notification, got %d", len(data.Notifications))
	}
	notification := data.Notifications[0]
	if notification.Type != "welcomeMessage" || notification.RecipientID != "user_new" || notification.SourceID != "workspace_1" || notification.Actor.ID != "user_editor" {
		t.Fatalf("unexpected notification: %+v", notification)
	}
}
