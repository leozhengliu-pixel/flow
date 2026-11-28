package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestDeliverySchedulerIdleClonesAndEmailDigests(t *testing.T) {
	for _, mode := range []string{"legacy", "flags-off", "bounded"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			if mode == "flags-off" {
				t.Setenv("FLOW_RECORD_HYDRATION", "off")
				t.Setenv("FLOW_RECORD_JOURNAL", "off")
			}
			config := platformDatabaseFixtureScaleDatabaseConfig(t)
			config.MaxOpenConns = 1
			repo, err := store.OpenDatabase(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			fixture := resourceAccessScaleFixture(t, repo)
			key, user := fixture.data.Workspace.URLKey, fixture.data.Viewer
			now := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
			later := now.Add(time.Hour)
			err = repo.MutateWorkspace(t.Context(), key, "test.delivery_scan", "", nil, func(data *domain.Bootstrap) error {
				for i := range 100 {
					data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{
						ID: fmt.Sprintf("desktop-%03d", i), RecipientID: user.ID, Channel: "desktop", Status: "pending", CreatedAt: now, UpdatedAt: now,
					})
				}
				for _, id := range []string{"minute-digest-a", "minute-digest-b", "immediate-email"} {
					data.Notifications = append(data.Notifications, domain.Notification{
						ID: id, RecipientID: user.ID, Title: id, Text: "Scheduler fixture", CreatedAt: now, UpdatedAt: now,
					})
					status := "digest"
					if id == "immediate-email" {
						status = "failed"
					}
					data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{
						ID: id, NotificationID: id, RecipientID: user.ID, Channel: "email", Status: status, NextAttemptAt: &later, CreatedAt: now, UpdatedAt: now,
					})
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			source := repo
			if mode == "bounded" {
				source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), key)
				if err != nil {
					t.Fatal(err)
				}
			}
			mailer, sink := newSinkMailer(t)
			s := &server{store: source, mailer: mailer}
			tick := func(at time.Time) int64 {
				t.Helper()
				before := store.WorkspaceMetadataClonesForTesting()
				if err := s.processDueDeliveries(t.Context(), at); err != nil {
					t.Fatal(err)
				}
				return store.WorkspaceMetadataClonesForTesting() - before
			}
			for _, at := range []time.Time{now, now.Add(5 * time.Second)} {
				clones := tick(at)
				t.Logf("idle delivery tick cloned workspace metadata %d times", clones)
				if clones != 0 {
					t.Errorf("idle delivery tick cloned workspace metadata %d times, want 0", clones)
				}
			}
			if messages := sink.take(); len(messages) != 0 {
				t.Fatalf("idle tick sent %d messages", len(messages))
			}
			s.mailer = nil
			if clones := tick(later); clones != 0 {
				t.Fatalf("tick without mailer cloned workspace metadata %d times", clones)
			}
			effects := scaleHotDeliveryEffects(t, repo, key)
			for _, id := range []string{"minute-digest-a", "minute-digest-b"} {
				if row := effects[id].([]any); row[3] != "digest" || row[4] != 0 || row[8] != time.Hour {
					t.Fatalf("digest without a mailer changed: %v", row)
				}
			}
			s.mailer = mailer
			tick(later.Add(5 * time.Second))
			messages := sink.take()
			if len(messages) != 2 {
				t.Fatalf("tick with mailer sent %d messages, want one immediate and one digest", len(messages))
			}
			if mailSubject(t, messages[0]) != "immediate-email" || mailSubject(t, messages[1]) != "Flow: 2 notifications" ||
				!strings.Contains(messages[1].text, "minute-digest-a") || !strings.Contains(messages[1].text, "minute-digest-b") {
				t.Fatal("immediate email or grouped digest had unexpected content")
			}
			if clones := tick(later.Add(10 * time.Second)); clones != 0 {
				t.Errorf("after delivery cloned workspace metadata %d times", clones)
			}
			if messages := sink.take(); len(messages) != 0 {
				t.Fatalf("tick resent %d messages", len(messages))
			}
			effects = scaleHotDeliveryEffects(t, repo, key)
			if row := effects["immediate-email"].([]any); row[3] != "delivered" || row[4] != 2 || row[6] != true || row[7] != false {
				t.Fatalf("immediate retry not marked delivered: %v", row)
			}
			for _, id := range []string{"minute-digest-a", "minute-digest-b"} {
				if row := effects[id].([]any); row[3] != "delivered" || row[4] != 1 || row[6] != true || row[7] != false {
					t.Fatalf("digest not marked delivered: %v", row)
				}
			}
			for i := range 100 {
				if row := effects[fmt.Sprintf("desktop-%03d", i)].([]any); row[3] != "pending" || row[4] != 0 || row[7] != false {
					t.Fatalf("desktop delivery no longer waiting for client: %v", row)
				}
			}
		})
	}
}

func TestDeliverySchedulerProcessesDueEmailAndHonorsBackoff(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	bootstrap := repository.Bootstrap()
	now := time.Now().UTC()
	due, future, pending := now.Add(-time.Minute), now.Add(time.Hour), "delivery_pending"
	err = repository.MutateWorkspace(workspaceFixtureContext(t.Context()), bootstrap.Workspace.URLKey, "test.delivery_scheduler", "deliveries", nil, func(data *domain.Bootstrap) error {
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
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	s := &server{store: repository, realtime: newRealtimeHub(), deliverySchedulerInterval: time.Hour}
	cleanupTestServer(t, s)
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

func TestChatWorkerDoesNotBlockDeliverySweeps(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, realtime: newRealtimeHub()}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var mu sync.Mutex
	active, peak := 0, 0
	started := make(chan struct{}, 16)
	blocked := func(ctx context.Context, _ string) error {
		mu.Lock()
		active++
		peak = max(peak, active)
		mu.Unlock()
		started <- struct{}{}
		<-ctx.Done()
		mu.Lock()
		active--
		mu.Unlock()
		return ctx.Err()
	}
	keys := make([]string, chatWorkerConcurrency*2)
	for index := range keys {
		keys[index] = fmt.Sprintf("workspace-%d", index)
	}
	finished := make(chan struct{})
	go func() {
		s.sweepChatIntegrations(ctx, keys, blocked)
		close(finished)
	}()
	for range chatWorkerConcurrency {
		<-started
	}
	sweep := make(chan error, 1)
	go func() { sweep <- s.processDueDeliveries(t.Context(), time.Now().UTC()) }()
	select {
	case err := <-sweep:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delivery sweep waited for slow chat platform calls")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("chat sweep ignored shutdown")
	}
	if peak > chatWorkerConcurrency {
		t.Fatalf("chat sweep ran %d workspaces at once; limit is %d", peak, chatWorkerConcurrency)
	}
}
