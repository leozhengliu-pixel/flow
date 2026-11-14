package main

import (
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW15NotificationReadCallerEffects(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		view, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		past, future := now.Add(-time.Hour), now.Add(24*time.Hour)
		var expected any
		for _, source := range []*store.SQLiteStore{repo, view} {
			for iteration := range 5 {
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw15.caller_reset", "", nil, func(data *domain.Bootstrap) error {
					data.NotificationDeliveries = []domain.NotificationDelivery{
						{ID: "sw15-failed-email", Channel: "email", Status: "failed", NextAttemptAt: &past, CreatedAt: now, UpdatedAt: now},
						{ID: "sw15-pending-email", Channel: "email", Status: "pending", CreatedAt: now, UpdatedAt: now},
						{ID: "sw15-disabled-email", Channel: "email", Status: "pending-disabled", CreatedAt: now, UpdatedAt: now},
						{ID: "sw15-pending-chat", Channel: "chat", Status: "pending", RecipientID: "sw15-unlinked", CreatedAt: now, UpdatedAt: now},
						{ID: "sw15-future-email", Channel: "email", Status: "failed", NextAttemptAt: &future, CreatedAt: now, UpdatedAt: now},
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				service := &server{store: source}
				if err := service.prepareDueNotificationDeliveries(t.Context(), workspace, now); err != nil {
					t.Fatal("due retry preparation failed", iteration, err)
				}
				service.dispatchNotificationEmails(t.Context(), workspace)
				service.dispatchChatNotifications(t.Context(), workspace)
				result, err := repo.NotificationDeliveries(t.Context(), workspace, []string{"pending", "failed", "pending-disabled"}, future.Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				effects := map[string]any{}
				for _, delivery := range result.NotificationDeliveries {
					effects[delivery.ID] = []any{delivery.Channel, delivery.Status, delivery.Attempts, delivery.Error, delivery.NextAttemptAt != nil}
				}
				if len(effects) != 5 ||
					!reflect.DeepEqual(effects["sw15-failed-email"], []any{"email", "pending-disabled", 1, "", false}) ||
					!reflect.DeepEqual(effects["sw15-pending-chat"], []any{"chat", "failed", 1, chatPersonalNotLinked, false}) {
					t.Fatal("normal scheduling side effects changed", iteration, effects)
				}
				if expected == nil {
					expected = effects
				} else if !reflect.DeepEqual(expected, effects) {
					t.Fatal("off/bounded scheduling state changed or retained allowance leaked", iteration, effects, expected)
				}
			}
		}
		if view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("native scheduler read decoded full metadata")
		}
	}
}
