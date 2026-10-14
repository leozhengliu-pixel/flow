package main

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"flow/api/internal/domain"
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
	data, err := s.store.NotificationDeliverySnapshot(ctx, workspace, []string{"digest"}, now)
	if err != nil {
		return
	}
	byUser := map[string][]domain.NotificationDelivery{}
	for _, delivery := range data.NotificationDeliveries {
		if delivery.Channel == "email" && (delivery.NextAttemptAt == nil || !delivery.NextAttemptAt.After(now)) {
			byUser[delivery.RecipientID] = append(byUser[delivery.RecipientID], delivery)
		}
	}
	for userID, deliveries := range byUser {
		recipient := userByID(&data, userID)
		if recipient == nil {
			continue
		}
		claimed := []domain.NotificationDelivery{}
		for _, delivery := range deliveries {
			ok := false
			err := s.store.MutateNotificationDelivery(ctx, workspace, delivery.ID, func(current *domain.NotificationDelivery) error {
				if current.Status == "digest" && (current.NextAttemptAt == nil || !current.NextAttemptAt.After(now)) {
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
		items := make([]notificationEmailItem, 0, len(claimed))
		for _, delivery := range claimed {
			for _, notification := range data.Notifications {
				if notification.ID == delivery.NotificationID {
					item, itemErr := s.notificationEmailItem(&data, workspace, notification)
					if itemErr != nil {
						item = notificationEmailItem{Title: notification.Type, URL: s.mailer.appURL + "/" + url.PathEscape(workspace) + "/inbox", Icon: item.Icon, Line: inviterDisplayName(notification.Actor)}
					}
					items = append(items, item)
					break
				}
			}
		}
		err = s.mailer.sendNotificationEmail(recipient.Email, workspace, fmt.Sprintf("Flow: %d notifications", len(claimed)), items, data.WorkspaceSettings.HIPAACompliance)
		for _, delivery := range claimed {
			_ = s.store.MutateNotificationDelivery(ctx, workspace, delivery.ID, func(current *domain.NotificationDelivery) error {
				if current.Status != "delivering" {
					return nil
				}
				current.Attempts++
				current.UpdatedAt = now
				if err == nil {
					current.Status = "delivered"
					current.DeliveredAt = &now
					current.NextAttemptAt = nil
					current.Error = ""
				} else {
					current.Status = "digest"
					retry := now.Add(time.Duration(1<<min(current.Attempts, 6)) * time.Minute)
					current.NextAttemptAt = &retry
					current.Error = err.Error()
				}
				return nil
			})
		}
	}
}
