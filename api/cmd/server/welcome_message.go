package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
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
		ID:              fmt.Sprintf("notification_welcome_%d", now.UnixNano()),
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
	if !ok || !data.WorkspaceSettings.WelcomeMessageEnabled || strings.TrimSpace(data.WorkspaceSettings.WelcomeMessage) == "" {
		return
	}
	err := s.store.MutateWorkspace(ctx, workspaceKey, "notification.welcome_sent", userID, nil, func(data *domain.Bootstrap) error {
		appendWelcomeNotification(data, userID, time.Now().UTC())
		return nil
	})
	if err != nil {
		log.Printf("Flow welcome message workspace=%s user=%s: %v", workspaceKey, userID, err)
	}
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
