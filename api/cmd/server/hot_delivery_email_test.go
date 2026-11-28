package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleHotDeliveryEmailDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	config := platformDatabaseFixtureScaleDatabaseConfig(t)
	config.MaxOpenConns = 1
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := resourceAccessScaleFixture(t, repo)
	workspace := f.data.Workspace.URLKey
	issueID := idgen.New("issue_")
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	future := time.Now().UTC().Add(time.Hour)
	for _, mode := range []string{"immediate", "digest"} {
		phases := []string{"sent", "hipaa", "transport-error", "no-mailer"}
		if mode == "digest" {
			phases = append(phases, "cancel-after-send")
		}
		for _, phase := range phases {
			var wantMail, wantEffects any
			for _, bounded := range []bool{false, true} {
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_delivery_email_seed", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.HIPAACompliance = phase == "hipaa"
					data.Projects = []domain.Project{{ID: "delivery-unrelated-project", Description: strings.Repeat("unrelated body ", 100000)}}
					data.Reviews = []domain.CodeReview{
						{ID: "first-review", SlugID: "matched-review", Title: "First installed review", Description: strings.Repeat("unused review body ", 50000)},
						{ID: "matched-review", SlugID: "later-review", Title: "Later ID match"},
					}
					data.Issues = []domain.Issue{{ID: issueID, Identifier: "MAIL-1", Team: data.Teams[0], State: domain.WorkflowState{ID: "delivery-state", Type: "unstarted"}, Creator: data.Viewer, Title: "Email issue", Description: strings.Repeat("unused issue body ", 50000), CreatedAt: now, UpdatedAt: now}}
					actor := domain.User{DisplayName: "Notification actor", AvatarURL: "https://example.test/actor.png", BuiltinAgent: true}
					data.Notifications = []domain.Notification{
						{ID: "delivery-email-title", RecipientID: data.Viewer.ID, Type: "pulse", Title: "Self-contained notification", Text: "Its own body", Actor: actor, CreatedAt: now, UpdatedAt: now},
						{ID: "delivery-email-issue", RecipientID: data.Viewer.ID, Type: "assignment", IssueID: issueID, Actor: actor, CreatedAt: now, UpdatedAt: now},
						{ID: "delivery-email-review", RecipientID: data.Viewer.ID, ReviewID: "matched-review", IssueID: issueID, Title: "Ignored self title", Actor: actor, CreatedAt: now, UpdatedAt: now},
						{ID: "delivery-email-missing-review", RecipientID: data.Viewer.ID, ReviewID: "missing-review", Actor: actor, CreatedAt: now, UpdatedAt: now},
						{ID: "delivery-email-missing-issue", RecipientID: data.Viewer.ID, IssueID: "missing-issue", Actor: actor, CreatedAt: now, UpdatedAt: now},
					}
					data.NotificationDeliveries = nil
					status := "pending"
					if mode == "digest" {
						status = "digest"
					}
					for index, notification := range data.Notifications {
						data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: fmt.Sprintf("delivery-email-%d", index), NotificationID: notification.ID, RecipientID: data.Viewer.ID, Channel: "email", Status: status, CreatedAt: now, UpdatedAt: now})
					}
					data.NotificationDeliveries = append(data.NotificationDeliveries,
						domain.NotificationDelivery{ID: "delivery-email-missing", NotificationID: "missing", RecipientID: data.Viewer.ID, Channel: "email", Status: status, CreatedAt: now, UpdatedAt: now},
						domain.NotificationDelivery{ID: "delivery-email-chat", Channel: "chat", Status: "pending", CreatedAt: now, UpdatedAt: now},
						domain.NotificationDelivery{ID: "delivery-email-future", Channel: "email", Status: "failed", NextAttemptAt: &future, CreatedAt: now, UpdatedAt: now},
					)
					if phase != "cancel-after-send" {
						data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{
							ID: "delivery-email-recipient", NotificationID: data.Notifications[0].ID, RecipientID: "missing", Channel: "email", Status: status, CreatedAt: now, UpdatedAt: now,
						})
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				source := repo
				if bounded {
					source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
				}
				mailer, sink := newSinkMailer(t)
				ctx, cancel := context.WithCancel(t.Context())
				switch phase {
				case "transport-error":
					send := mailer.transport
					mailer.transport = func(envelope string, recipients []string, raw []byte) error {
						if err := send(envelope, recipients, raw); err != nil {
							return err
						}
						return errors.New("delivery transport failed")
					}
				case "no-mailer":
					mailer = nil
				case "cancel-after-send":
					send := mailer.transport
					mailer.transport = func(envelope string, recipients []string, raw []byte) error {
						defer cancel()
						return send(envelope, recipients, raw)
					}
				}
				service := &server{store: source, mailer: mailer}
				if mode == "digest" {
					service.dispatchNotificationDigests(ctx, workspace, now)
				} else {
					service.dispatchNotificationEmails(ctx, workspace)
				}
				cancel()
				messages := []any{}
				for _, message := range sink.take() {
					messages = append(messages, []any{message.header.Get("To"), mailSubject(t, message), message.text, message.html})
				}
				effects := scaleHotDeliveryEffects(t, repo, workspace)
				if mode == "digest" && phase != "no-mailer" {
					if phase == "cancel-after-send" {
						for _, id := range []string{"delivery-email-0", "delivery-email-1", "delivery-email-2", "delivery-email-3", "delivery-email-4", "delivery-email-missing"} {
							if row := effects[id].([]any); row[3] != "delivered" || row[4] != 1 || row[7] != false {
								t.Fatalf("scheduler cancellation lost digest delivery result: %v", row)
							}
						}
					} else {
						missing := effects["delivery-email-recipient"].([]any)
						if missing[3] != "digest" || missing[4] != 1 || missing[5] == "" || missing[8] != 2*time.Minute {
							t.Fatalf("digest with no recipient did not back off: %v", missing)
						}
					}
					if phase == "transport-error" {
						retry := effects["delivery-email-0"].([]any)
						if retry[3] != "digest" || retry[4] != 1 || retry[5] != "delivery transport failed" || retry[8] != 2*time.Minute {
							t.Fatalf("digest transport backoff changed: %v", retry)
						}
					}
					clones := store.WorkspaceMetadataClonesForTesting()
					service.dispatchNotificationDigests(t.Context(), workspace, now.Add(5*time.Second))
					if store.WorkspaceMetadataClonesForTesting() != clones || len(sink.take()) != 0 || !reflect.DeepEqual(effects, scaleHotDeliveryEffects(t, repo, workspace)) {
						t.Fatal("delivered or backed-off digests were reconsidered on the next tick")
					}
				}
				if !bounded {
					wantMail, wantEffects = messages, effects
				} else if !reflect.DeepEqual(messages, wantMail) || !reflect.DeepEqual(effects, wantEffects) {
					t.Fatalf("mode=%s phase=%s email output/effects differ:\nmail=%#v\nwant=%#v\neffects=%#v\nwant=%#v", mode, phase, messages, wantMail, effects, wantEffects)
				}
				expected := 3
				if mode == "digest" {
					expected = 1
				}
				if phase != "no-mailer" && len(messages) != expected {
					t.Fatalf("mode=%s phase=%s delivered %d messages, want %d", mode, phase, len(messages), expected)
				}
				if bounded && (source.WorkspaceMetadataDecodes() != 0 || source.MetadataWorkUsedForTesting() != 0) {
					t.Fatal("email dispatch decoded full metadata or leaked held work")
				}
			}
		}
	}
}

func scaleHotDeliveryEffects(t *testing.T, repo *store.SQLiteStore, workspace string) map[string]any {
	t.Helper()
	data, err := repo.NotificationDeliveries(t.Context(), workspace, store.NotificationDeliverySelection{Statuses: []string{"pending", "pending-disabled", "delivering", "delivered", "failed", "digest"}}, time.Now().UTC().Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]any{}
	for _, delivery := range data.NotificationDeliveries {
		delay := time.Duration(0)
		if delivery.NextAttemptAt != nil {
			delay = delivery.NextAttemptAt.Sub(delivery.UpdatedAt)
		}
		result[delivery.ID] = []any{delivery.RecipientID, delivery.NotificationID, delivery.Channel, delivery.Status, delivery.Attempts, delivery.Error, delivery.DeliveredAt != nil, delivery.NextAttemptAt != nil, delay}
	}
	return result
}
