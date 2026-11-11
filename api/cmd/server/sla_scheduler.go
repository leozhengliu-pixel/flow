package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) runSLAScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	s.runSLATick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.store.WorkerContext().Done():
			return
		case <-ticker.C:
			s.runSLATick(ctx)
		}
	}
}

func (s *server) runSLATick(ctx context.Context) {
	run := func() error {
		for _, key := range s.store.WorkspaceKeys() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := s.maintainSLADeadlines(ctx, key, time.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	if s.coordinator == nil {
		err = run()
	} else {
		_, err = s.coordinator.WithLeaderLock(ctx, "sla-deadlines", run)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("SLA deadlines: %v", err)
	}
}

func (s *server) maintainSLADeadlines(ctx context.Context, key string, now time.Time) error {
	data, ok := s.store.WorkspaceMetadataFields(key, "settings", "issueSlas", "slaEvents")
	if !ok || !slaEnabled(&data) {
		return nil
	}
	ids := []string{}
	for _, sla := range data.IssueSLAs {
		if sla.Status != "active" {
			continue
		}
		highRisk := sla.DueAt.Add(-24 * time.Hour)
		if sla.HighRiskAt != nil {
			highRisk = *sla.HighRiskAt
		}
		if sla.HighRiskAt == nil || sla.MediumRiskAt == nil || sla.TargetMinutes == 0 || !now.Before(sla.DueAt) || !now.Before(highRisk) && !slaEventRecorded(&data, sla.ID, "high_risk") {
			ids = append(ids, sla.IssueID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	scope := store.WithMutationScope(ctx, store.MutationScope{IssueIDs: ids})
	return s.store.MutateWorkspace(scope, key, "sla.maintained", "workspace", nil, func(current *domain.Bootstrap) error {
		before, _ := json.Marshal([]any{current.IssueSLAs, current.SLAEvents})
		for index := range current.Issues {
			applySLARules(current, &current.Issues[index], now)
		}
		after, _ := json.Marshal([]any{current.IssueSLAs, current.SLAEvents})
		if bytes.Equal(before, after) {
			return store.ErrNoMutation
		}
		return nil
	})
}
