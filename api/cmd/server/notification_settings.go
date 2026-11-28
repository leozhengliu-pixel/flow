package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func notificationUrgent(data *domain.Bootstrap, notification domain.Notification) bool {
	issue, err := issueByID(data, notification.IssueID)
	if err != nil || issue.Assignee == nil || issue.Assignee.ID != notification.RecipientID {
		return false
	}
	if issue.Priority == 1 {
		return true
	}
	for _, sla := range data.IssueSLAs {
		if sla.IssueID == issue.ID && sla.Status == "breached" {
			return true
		}
	}
	return false
}

func notificationDigestDue(data *domain.Bootstrap, notification domain.Notification, preferences domain.NotificationPreferences) time.Time {
	due := notification.UpdatedAt.Add(time.Hour)
	if !preferences.DelayLowPriority {
		return due
	}
	zone := time.UTC
	if issue, err := issueByID(data, notification.IssueID); err == nil {
		if location, err := time.LoadLocation(data.TeamSettings[issue.Team.ID].Timezone); err == nil {
			zone = location
		}
	}
	local := due.In(zone)
	if local.Hour() >= 18 {
		local = time.Date(local.Year(), local.Month(), local.Day()+1, 9, 0, 0, 0, zone)
	}
	if local.Hour() < 9 {
		local = time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, zone)
	}
	for local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		local = local.AddDate(0, 0, 1)
	}
	return local.UTC()
}

func (s *server) dispatchNotificationDigests(ctx context.Context, workspace string, now time.Time) {
	if s.mailer == nil {
		return
	}
	selection := store.NotificationDeliverySelection{Channel: "email", Statuses: []string{"digest"}}
	var data domain.Bootstrap
	var err error
	if s.store.BoundedMetadataEnabled() {
		read, readErr := s.store.BeginNotificationDeliveryContextRead(ctx, workspace, selection, now)
		if readErr != nil {
			return
		}
		defer read.Close()
		data = read.Data
	} else {
		data, err = s.store.NotificationDeliverySnapshot(ctx, workspace, selection, now)
		if err != nil {
			return
		}
	}
	byUser := map[string][]domain.NotificationDelivery{}
	for _, delivery := range data.NotificationDeliveries {
		if delivery.Channel == "email" && (delivery.NextAttemptAt == nil || !delivery.NextAttemptAt.After(now)) {
			byUser[delivery.RecipientID] = append(byUser[delivery.RecipientID], delivery)
		}
	}
	for userID, deliveries := range byUser {
		recipient := userByID(&data, userID)
		claimed := []domain.NotificationDelivery{}
		for _, delivery := range deliveries {
			ok := false
			err := s.store.MutateNotificationDelivery(ctx, workspace, delivery.ID, func(current *domain.NotificationDelivery) error {
				if current.Channel == "email" && current.Status == "digest" && (current.NextAttemptAt == nil || !current.NextAttemptAt.After(now)) {
					current.Status = "delivering"
					current.UpdatedAt = now
					ok = true
				}
				return nil
			})
			if err == nil && ok {
				claimed = append(claimed, delivery)
			}
		}
		if len(claimed) == 0 {
			continue
		}
		if recipient == nil {
			s.finishNotificationDigest(ctx, workspace, claimed, now, errors.New("notification recipient is unavailable"))
			continue
		}
		items := make([]notificationEmailItem, 0, len(claimed))
		sendable := make([]domain.NotificationDelivery, 0, len(claimed))
		for _, delivery := range claimed {
			index := slices.IndexFunc(data.Notifications, func(item domain.Notification) bool { return item.ID == delivery.NotificationID })
			if index < 0 {
				sendable = append(sendable, delivery)
				continue
			}
			notification := data.Notifications[index]
			// The recipient may have lost the document since it was queued.
			deliverable, accessErr := s.documentNotificationDeliverable(ctx, workspace, notification)
			if accessErr != nil {
				s.finishNotificationDigest(ctx, workspace, []domain.NotificationDelivery{delivery}, now, documentDeliveryError(accessErr))
				continue
			}
			if !deliverable {
				s.dropDocumentDelivery(ctx, workspace, delivery.ID)
				continue
			}
			sendable = append(sendable, delivery)
			item, itemErr := s.notificationEmailItem(&data, workspace, notification)
			if itemErr != nil {
				item = notificationEmailItem{Title: notification.Type, URL: s.mailer.appURL + "/" + url.PathEscape(workspace) + "/inbox", Icon: item.Icon, Line: inviterDisplayName(notification.Actor)}
			}
			items = append(items, item)
		}
		if len(sendable) == 0 {
			continue
		}
		err = s.mailer.sendNotificationEmail(recipient.Email, workspace, fmt.Sprintf("Flow: %d notifications", len(sendable)), items, data.WorkspaceSettings.HIPAACompliance)
		s.finishNotificationDigest(ctx, workspace, sendable, now, err)
	}
}

func (s *server) finishNotificationDigest(ctx context.Context, workspace string, deliveries []domain.NotificationDelivery, now time.Time, deliveryErr error) {
	for _, delivery := range deliveries {
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_ = s.store.MutateNotificationDelivery(persistCtx, workspace, delivery.ID, func(current *domain.NotificationDelivery) error {
			if current.Status != "delivering" {
				return nil
			}
			current.Attempts++
			current.UpdatedAt = now
			if deliveryErr == nil {
				current.Status = "delivered"
				current.DeliveredAt = &now
				current.NextAttemptAt = nil
				current.Error = ""
			} else {
				current.Status = "digest"
				retry := now.Add(time.Duration(1<<min(current.Attempts, 6)) * time.Minute)
				current.NextAttemptAt = &retry
				current.Error = deliveryErr.Error()
			}
			return nil
		})
		cancel()
	}
}
