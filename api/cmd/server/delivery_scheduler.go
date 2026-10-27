package main

import (
	"context"
	"errors"
	"log"
	"slices"
	"sync"
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
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		workers.Wait()
		close(done)
	}()
	// Chat platform calls can be slow; they run in their own loop so they
	// never delay webhook, email or settings sweeps.
	go func() {
		defer workers.Done()
		s.runChatWorkerLoop(ctx, interval, s.processChatIntegrationTick)
	}()
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.store.WorkerContext().Done():
				return
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
	sweepSettings := s.settingsLastSweep.Swap(now.Unix()/60) != now.Unix()/60
	s.processDueWebhookDeliveries(ctx, now)
	if sweepSettings {
		s.reconcileAllApplicationWebhooks(ctx)
		s.sendDueOnboardingEmails(ctx, now)
		if err := s.store.PurgeChatHistory(ctx, now); err != nil {
			log.Printf("Chat history retention: %v", err)
		}
	}
	for _, key := range s.store.WorkspaceKeys() {
		s.sweepAppUserNotifications(ctx, key, now)
		if sweepSettings {
			s.sweepIssueSLAWebhooks(ctx, key, now)
			if err := s.maintainTeamSettings(ctx, key, now); err != nil {
				return err
			}
			if err := s.preparePulseSummaries(ctx, key, now); err != nil {
				return err
			}
			s.runDueLoops(key, now)
			s.rotateDueGitLabTokens(ctx, key, now)
			if err := s.purgeExpiredTeams(ctx, key, now); err != nil {
				log.Printf("Team purge workspace=%s: %v", key, err)
			}
		}
		if err := s.prepareDueNotificationDeliveries(ctx, key, now); err != nil {
			return err
		}
		s.streamAuditLog(ctx, key, now)
		s.dispatchNotificationEmails(ctx, key)
		s.dispatchNotificationDigests(ctx, key, now)
		data, ok := s.store.WorkspaceMetadataFields(key, "integrationDeliveries")
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

const (
	chatWorkerConcurrency = 4
	chatWorkspaceTimeout  = 15 * time.Second
)

func (s *server) runChatWorkerLoop(ctx context.Context, interval time.Duration, tick func(context.Context, string) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.store.WorkerContext().Done():
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepChatIntegrations(ctx, s.store.WorkspaceKeys(), tick)
		}
	}
}

// sweepChatIntegrations runs one bounded pass over the workspaces. The
// per-workspace database lease (ClaimChatWorker) keeps instances from
// processing the same workspace at once, so no leader lock is needed.
func (s *server) sweepChatIntegrations(ctx context.Context, keys []string, tick func(context.Context, string) error) {
	slots := make(chan struct{}, chatWorkerConcurrency)
	var running sync.WaitGroup
	for _, key := range keys {
		select {
		case <-ctx.Done():
			running.Wait()
			return
		case slots <- struct{}{}:
		}
		running.Add(1)
		go func() {
			defer running.Done()
			defer func() { <-slots }()
			workspaceCtx, cancel := context.WithTimeout(ctx, chatWorkspaceTimeout)
			defer cancel()
			if err := tick(workspaceCtx, key); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				log.Printf("Chat integration workspace=%s: %v", key, err)
			}
		}()
	}
	running.Wait()
}

func (s *server) prepareDueNotificationDeliveries(ctx context.Context, key string, now time.Time) error {
	statuses := []string{"failed"}
	if s.mailer != nil {
		statuses = append(statuses, "pending-disabled")
	}
	snapshot, err := s.store.NotificationDeliveries(ctx, key, statuses, now)
	if err != nil {
		return err
	}
	needsUpdate := slices.ContainsFunc(snapshot.NotificationDeliveries, func(delivery domain.NotificationDelivery) bool {
		return delivery.Channel == "email" && (delivery.Status == "failed" && delivery.NextAttemptAt != nil && !delivery.NextAttemptAt.After(now) || delivery.Status == "pending-disabled" && s.mailer != nil)
	})
	if !needsUpdate {
		return nil
	}
	for _, item := range snapshot.NotificationDeliveries {
		err := s.store.MutateNotificationDelivery(ctx, key, item.ID, func(delivery *domain.NotificationDelivery) error {
			if delivery.Channel != "email" {
				return nil
			}
			dueFailure := delivery.Status == "failed" && delivery.NextAttemptAt != nil && !delivery.NextAttemptAt.After(now)
			deliveryEnabled := delivery.Status == "pending-disabled" && s.mailer != nil
			if !dueFailure && !deliveryEnabled {
				return nil
			}
			delivery.Status, delivery.NextAttemptAt, delivery.UpdatedAt = "pending", nil, now
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
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
