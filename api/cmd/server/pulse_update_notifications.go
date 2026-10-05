package main

import (
	"fmt"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Notification types for activity on a project or initiative update. They
// deliberately avoid the "projectUpdate" prefix that update reminders use.
const (
	notificationUpdateComment  = "updateComment"
	notificationUpdateReaction = "updateReaction"
)

// pulseUpdateScope scopes a mutation to the notifications owned by one
// update, so adding an author notification never loads or rewrites any
// other inbox records.
func pulseUpdateScope(updateID string) store.MutationScope {
	return store.MutationScope{Resources: []string{updateID}}
}

func canEditPulseUpdate(data *domain.Bootstrap, author domain.User) bool {
	return author.ID == data.Viewer.ID || workspaceAdminRole(data.ViewerRole)
}

// notifyUpdateAuthor tells an update's author about a comment or reaction
// (category "updates"); the actor's own activity is never notified.
func notifyUpdateAuthor(data *domain.Bootstrap, kind, sourceID, sourceName, updateID string, author domain.User, notificationType, commentID, emoji string, now time.Time) {
	actor := data.Viewer
	if author.ID == "" || author.ID == actor.ID || author.App {
		return
	}
	preferences, ok := data.NotificationPreferences[author.ID]
	if !ok {
		preferences = defaultPreferences(author.ID)
	}
	if !preferences.Inbox.Enabled || !categoryEnabled(preferences.Inbox, "updates") {
		return
	}
	actorName := firstNonEmpty(actor.DisplayName, actor.Name)
	text := actorName + " commented on your update"
	if notificationType == notificationUpdateReaction {
		text = actorName + " reacted " + emoji + " to your update"
	}
	groupKey := author.ID + ":" + updateID + ":" + notificationType
	if notificationType == notificationUpdateReaction {
		if existing := aggregatableNotification(data, groupKey, now); existing != nil {
			existing.OccurrenceCount++
			existing.Actor, existing.Text, existing.UpdatedAt = actor, text, now
			existing.LatestActorIDs = appendUnique(existing.LatestActorIDs, actor.ID)
			if len(existing.LatestActorIDs) > 3 {
				existing.LatestActorIDs = existing.LatestActorIDs[len(existing.LatestActorIDs)-3:]
			}
			enqueueNotificationDeliveries(data, *existing, preferences)
			return
		}
	}
	notification := domain.Notification{
		ID: fmt.Sprintf("notification_%s_%d_%s", notificationType, now.UnixNano(), author.ID), RecipientID: author.ID, Type: notificationType,
		SourceType: kind + "Update", SourceID: updateID, CommentID: commentID, Actor: actor, Category: "updates", GroupKey: groupKey,
		OccurrenceCount: 1, LatestActorIDs: []string{actor.ID}, Title: sourceName, Text: text, CreatedAt: now, UpdatedAt: now,
		Payload: &domain.NotificationPayload{Updates: []domain.PulseUpdateRef{{ID: updateID, Kind: kind, SourceID: sourceID, Source: sourceName}}, UpdateIDs: []string{updateID}},
	}
	if kind == "project" {
		notification.ProjectID = sourceID
	}
	data.Notifications = append(data.Notifications, notification)
	enqueueNotificationDeliveries(data, notification, preferences)
}
