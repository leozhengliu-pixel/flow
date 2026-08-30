package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestDeliverySchedulerProcessesDueEmailAndHonorsBackoff(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	bootstrap := repository.Bootstrap()
	now := time.Now().UTC()
	due, future, pending := now.Add(-time.Minute), now.Add(time.Hour), "delivery_pending"
	err = repository.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "test.delivery_scheduler", "deliveries", nil, func(data *domain.Bootstrap) error {
		data.NotificationDeliveries = []domain.NotificationDelivery{
			{ID: "delivery_due", RecipientID: data.Viewer.ID, Channel: "email", Status: "failed", NextAttemptAt: &due, CreatedAt: now, UpdatedAt: now},
			{ID: "delivery_future", RecipientID: data.Viewer.ID, Channel: "email", Status: "failed", NextAttemptAt: &future, CreatedAt: now, UpdatedAt: now},
			{ID: pending, RecipientID: data.Viewer.ID, Channel: "email", Status: "pending", CreatedAt: now, UpdatedAt: now},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: repository, realtime: newRealtimeHub()}
	if err := s.processDueDeliveries(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	updated, _ := repository.BootstrapFor(bootstrap.Workspace.URLKey)
	status := map[string]domain.NotificationDelivery{}
	for _, delivery := range updated.NotificationDeliveries {
		status[delivery.ID] = delivery
	}
	if status["delivery_due"].Status != "pending-disabled" || status["delivery_due"].Attempts != 1 {
		t.Fatalf("due delivery = %#v", status["delivery_due"])
	}
	if status[pending].Status != "pending-disabled" || status[pending].Attempts != 1 {
		t.Fatalf("pending delivery = %#v", status[pending])
	}
	if status["delivery_future"].Status != "failed" || status["delivery_future"].Attempts != 0 {
		t.Fatalf("future delivery = %#v", status["delivery_future"])
	}
}

func TestDeliverySchedulerStopsGracefully(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, realtime: newRealtimeHub(), deliverySchedulerInterval: time.Hour}
	s.startDeliveryScheduler()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := s.stopDeliveryScheduler(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDueDeliveryIDsRespectsNextAttemptAndLimit(t *testing.T) {
	now := time.Now().UTC()
	due, future := now.Add(-time.Second), now.Add(time.Hour)
	ids := dueDeliveryIDs([]domain.IntegrationDelivery{{ID: "pending", Status: "pending"}, {ID: "due", Status: "failed", NextAttemptAt: &due}, {ID: "future", Status: "failed", NextAttemptAt: &future}, {ID: "limited", Status: "pending", Attempts: 8}}, now)
	if len(ids) != 2 || ids[0] != "due" || ids[1] != "pending" {
		t.Fatalf("due ids = %#v", ids)
	}
}
