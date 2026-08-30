package main

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"flow/api/internal/domain"
)

const defaultDeliverySchedulerInterval = 5 * time.Second

func (s *server) startDeliveryScheduler() {
	if !s.deliverySchedulerStarted.CompareAndSwap(false, true) {
		return
	}
	interval := s.deliverySchedulerInterval
	if interval <= 0 {
		interval = defaultDeliverySchedulerInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.deliverySchedulerMu.Lock()
	s.deliverySchedulerCancel, s.deliverySchedulerDone = cancel, done
	s.deliverySchedulerMu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.runDeliverySchedulerTick(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("Delivery scheduler: %v", err)
				}
			}
		}
	}()
}

func (s *server) stopDeliveryScheduler(ctx context.Context) error {
	s.deliverySchedulerMu.Lock()
	cancel, done := s.deliverySchedulerCancel, s.deliverySchedulerDone
	s.deliverySchedulerMu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *server) runDeliverySchedulerTick(ctx context.Context) error {
	run := func() error { return s.processDueDeliveries(ctx, time.Now().UTC()) }
	if s.coordinator == nil {
		return run()
	}
	_, err := s.coordinator.WithLeaderLock(ctx, "delivery-scheduler", run)
	return err
}

func (s *server) processDueDeliveries(ctx context.Context, now time.Time) error {
	for _, key := range s.store.WorkspaceKeys() {
		if err := s.prepareDueNotificationDeliveries(ctx, key, now); err != nil {
			return err
		}
		s.dispatchNotificationEmails(ctx, key)
		data, ok := s.store.BootstrapFor(key)
		if !ok {
			continue
		}
		for _, delivery := range data.IntegrationDeliveries {
			due := delivery.Status == "pending" || delivery.Status == "failed" && delivery.NextAttemptAt != nil && !delivery.NextAttemptAt.After(now)
			if !due || delivery.Attempts >= 8 {
				continue
			}
			if _, err := s.processIntegrationDelivery(ctx, key, delivery.ID); err != nil && !errors.Is(err, errConflict) && !errors.Is(err, context.Canceled) {
				log.Printf("Integration delivery workspace=%s id=%s: %v", key, delivery.ID, err)
			}
		}
	}
	return nil
}

func (s *server) prepareDueNotificationDeliveries(ctx context.Context, key string, now time.Time) error {
	snapshot, ok := s.store.BootstrapFor(key)
	if !ok {
		return nil
	}
	needsUpdate := slices.ContainsFunc(snapshot.NotificationDeliveries, func(delivery domain.NotificationDelivery) bool {
		return delivery.Channel == "email" && (delivery.Status == "failed" && delivery.NextAttemptAt != nil && !delivery.NextAttemptAt.After(now) || delivery.Status == "pending-disabled" && s.mailer != nil)
	})
	if !needsUpdate {
		return nil
	}
	return s.store.MutateWorkspace(ctx, key, "notification.deliveries_scheduled", "scheduler", nil, func(data *domain.Bootstrap) error {
		for index := range data.NotificationDeliveries {
			delivery := &data.NotificationDeliveries[index]
			if delivery.Channel != "email" {
				continue
			}
			dueFailure := delivery.Status == "failed" && delivery.NextAttemptAt != nil && !delivery.NextAttemptAt.After(now)
			deliveryEnabled := delivery.Status == "pending-disabled" && s.mailer != nil
			if !dueFailure && !deliveryEnabled {
				continue
			}
			delivery.Status, delivery.NextAttemptAt, delivery.UpdatedAt = "pending", nil, now
		}
		return nil
	})
}

func dueDeliveryIDs(values []domain.IntegrationDelivery, now time.Time) []string {
	result := make([]string, 0)
	for _, delivery := range values {
		if delivery.Attempts >= 8 {
			continue
		}
		if delivery.Status == "pending" || delivery.Status == "failed" && delivery.NextAttemptAt != nil && !delivery.NextAttemptAt.After(now) {
			result = append(result, delivery.ID)
		}
	}
	slices.Sort(result)
	return result
}
