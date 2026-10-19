package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// Coding sandboxes wiring (track A). The store adapter is
// internal/codingstore (the one adapter for every coding contract); this file
// only starts the sandbox loops.

// codingMeterUsage routes the sandbox reconciler's runtime usage through the
// session Meter, so runtime billed by either biller (both CAS the same
// coding_sandboxes.billed_seconds) re-checks spend limits.
type codingMeterUsage struct{ meter *coding.Meter }

func (u codingMeterUsage) RecordCodingUsage(ctx context.Context, record domain.CodingUsageRecord) error {
	return u.meter.Record(ctx, record)
}

// runCodingSandboxes starts the sandbox reconciler, the warm pool and the
// orphan GC until ctx ends (called from runCoding).
func (s *server) runCodingSandboxes(ctx context.Context) {
	if s.coding == nil || s.coding.sandbox == nil || s.coding.sandbox.Name() == "disabled" {
		return
	}
	adapter := s.codingStore()
	host, _ := os.Hostname()
	owner := "flow-" + host + "-" + sandbox.NewID(time.Now())
	logger := slog.Default().With("component", "coding-sandboxes")
	metrics := sandbox.DefaultMetrics()
	var leader sandbox.LeaderFunc
	if s.coordinator != nil {
		leader = s.coordinator.WithLeaderLock
	}
	reconciler := &sandbox.Reconciler{Store: adapter, Provider: s.coding.sandbox, Config: s.coding.config, Owner: owner, Logs: s.objectStore, Metrics: metrics, Logger: logger}
	reconciler.Usage = adapter
	if s.coding.meter != nil {
		reconciler.Usage = codingMeterUsage{meter: s.coding.meter}
	}
	s.coding.sandboxReconciler = reconciler
	pool := &sandbox.WarmPool{Store: adapter, Config: s.coding.config, Leader: leader, Metrics: metrics, Logger: logger, Wake: reconciler.Wake}
	gc := &sandbox.GC{Store: adapter, Provider: s.coding.sandbox, Config: s.coding.config, Leader: leader, Metrics: metrics, Logger: logger}
	s.coding.sandboxGC = gc
	// Track I: artifacts of deleted sessions (rows and objects).
	gc.AddSweeper(sandbox.Sweeper{Name: "coding-artifacts", Run: s.sweepOrphanCodingArtifacts})
	go reconciler.Run(ctx)
	go pool.Run(ctx)
	go gc.Run(ctx)
}

// sweepOrphanCodingArtifacts deletes the artifacts of sessions that no longer
// exist: the object first (a missing object is fine), then the rows.
func (s *server) sweepOrphanCodingArtifacts(ctx context.Context) error {
	owners, err := s.store.OrphanCodingArtifactSessions(ctx, 100)
	if err != nil {
		return err
	}
	for _, owner := range owners {
		keys, err := s.store.CodingArtifactObjectKeys(ctx, owner.WorkspaceKey, owner.SessionID)
		if err != nil {
			return err
		}
		if s.objectStore != nil {
			for _, key := range keys {
				if err := s.objectStore.Delete(ctx, key); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
		if _, err := s.store.DeleteCodingArtifacts(ctx, owner.WorkspaceKey, owner.SessionID); err != nil {
			return err
		}
	}
	return nil
}
