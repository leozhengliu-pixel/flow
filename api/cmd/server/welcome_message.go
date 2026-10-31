package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// appendWelcomeNotification puts the workspace welcome message in a member's
// inbox. It reports false when there is no message to send.
func appendWelcomeNotification(data *domain.Bootstrap, recipientID string, now time.Time) bool {
	settings := data.WorkspaceSettings
	if strings.TrimSpace(settings.WelcomeMessage) == "" || recipientID == "" {
		return false
	}
	actor := data.Viewer
	if author := userByID(data, settings.WelcomeMessageEditedByID); author != nil {
		actor = *author
	}
	data.Notifications = append(data.Notifications, domain.Notification{
		ID:              idgen.New("notification_welcome_"),
		RecipientID:     recipientID,
		Type:            "welcomeMessage",
		SourceType:      "workspace",
		SourceID:        data.Workspace.ID,
		Actor:           actor,
		Category:        "updates",
		GroupKey:        "welcomeMessage:" + recipientID,
		OccurrenceCount: 1,
		LatestActorIDs:  []string{actor.ID},
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	return true
}

// sendWelcomeMessage delivers the enabled welcome message to a member who just
// joined. Failures are logged; joining never fails because of it.
func (s *server) sendWelcomeMessage(ctx context.Context, workspaceKey, userID string) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey)
	if !ok {
		return
	}
	_, _, announcementEnabled := onboardingAnnouncementContent(&data)
	welcomeEnabled := data.WorkspaceSettings.WelcomeMessageEnabled && strings.TrimSpace(data.WorkspaceSettings.WelcomeMessage) != ""
	if !welcomeEnabled && !announcementEnabled {
		return
	}
	err := s.store.MutateWorkspace(ctx, workspaceKey, "notification.welcome_sent", userID, nil, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		if data.WorkspaceSettings.WelcomeMessageEnabled {
			appendWelcomeNotification(data, userID, now)
		}
		appendOnboardingAnnouncement(data, userID, now)
		return nil
	})
	if err != nil {
		log.Printf("Flow welcome message workspace=%s user=%s: %v", workspaceKey, userID, err)
	}
}

func appendOnboardingAnnouncement(data *domain.Bootstrap, recipientID string, now time.Time) bool {
	title, message, enabled := onboardingAnnouncementContent(data)
	if !enabled || recipientID == "" {
		return false
	}
	data.Notifications = append(data.Notifications, domain.Notification{
		ID: idgen.New("notification_announcement_"), RecipientID: recipientID,
		Type: "announcement", SourceType: "workspace", SourceID: data.Workspace.ID,
		Actor: data.Viewer, Category: "updates", Title: title, Text: message,
		GroupKey: "announcement:" + recipientID, OccurrenceCount: 1,
		CreatedAt: now, UpdatedAt: now,
	})
	return true
}

func onboardingAnnouncementContent(data *domain.Bootstrap) (string, string, bool) {
	announcement, _ := data.Settings["onboardingAnnouncement"].(map[string]any)
	enabled, _ := announcement["enabled"].(bool)
	title, _ := announcement["title"].(string)
	message, _ := announcement["message"].(string)
	title, message = strings.TrimSpace(title), strings.TrimSpace(message)
	return title, message, enabled && title != "" && message != ""
}

// testWelcomeMessage sends the current welcome message to the caller.
func (s *server) testWelcomeMessage(w http.ResponseWriter, r *http.Request) {
	sent := false
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "notification.welcome_test", "welcome", nil, func(data *domain.Bootstrap) error {
		sent = appendWelcomeNotification(data, data.Viewer.ID, time.Now().UTC())
		if !sent {
			return fmt.Errorf("%w: write a welcome message first", errInvalid)
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, map[string]bool{"sent": sent})
}
