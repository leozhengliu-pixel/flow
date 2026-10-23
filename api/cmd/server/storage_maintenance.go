package main

import (
	"context"
	"log"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const (
	defaultTrashRetention = 30 * 24 * time.Hour
	// trashPurgeBatch bounds how many expired trash entries one write removes.
	trashPurgeBatch = 200
	// storageMaintenanceInterval is how often old events and expired trash are
	// removed; offloaded content is swept every offloadSweepInterval and kept
	// offloadSweepGrace after nothing refers to it.
	storageMaintenanceInterval = time.Hour
	offloadSweepInterval       = 6 * time.Hour
	offloadSweepGrace          = 24 * time.Hour
	// maintenancePause spaces out batches so they do not crowd out requests.
	maintenancePause = 100 * time.Millisecond
)

// trashRetention is how long deleted items stay restorable
// (FLOW_TRASH_RETENTION_DAYS, set at startup).
var trashRetention = defaultTrashRetention

// trashExpired reports whether a trash entry is past its retention: the expiry
// it was deleted with, or the configured window when that is shorter.
func trashExpired(entry domain.TrashEntry, now time.Time) bool {
	return now.After(entry.ExpiresAt) || (!entry.DeletedAt.IsZero() && now.After(entry.DeletedAt.Add(trashRetention)))
}

// trashRetentionDays is the retention the app shows ("deleted permanently
// after N days").
func trashRetentionDays() int {
	return max(1, int(trashRetention/(24*time.Hour)))
}

// runStorageMaintenance keeps the database from growing without bound. Each
// pass prunes domain events older than eventRetention (0 keeps them), purges
// expired trash in every workspace, moves offloadable bodies out of metadata
// rows written by earlier versions and, less often, deletes offloaded bodies
// nothing refers to any more. All of it runs in small batches.
func (s *server) runStorageMaintenance(ctx context.Context, eventRetention time.Duration) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	var lastSweep time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		now := time.Now().UTC()
		sweep := now.Sub(lastSweep) >= offloadSweepInterval
		// With Redis coordination one replica runs the pass; the others
		// would only repeat the same deletes in parallel.
		ran := true
		run := func() error { s.maintainStorage(ctx, now, eventRetention, sweep); return nil }
		if s.coordinator == nil {
			_ = run()
		} else if elected, err := s.coordinator.WithLeaderLock(ctx, "storage-maintenance", run); err != nil || !elected {
			ran = elected
			if err != nil && ctx.Err() == nil {
				log.Printf("Storage maintenance: %v", err)
			}
		}
		if ran && sweep {
			lastSweep = now
		}
		timer.Reset(storageMaintenanceInterval)
	}
}

func (s *server) maintainStorage(ctx context.Context, now time.Time, eventRetention time.Duration, sweep bool) {
	if eventRetention > 0 {
		if deleted, err := s.pruneDomainEvents(ctx, now.Add(-eventRetention)); err != nil {
			if ctx.Err() == nil {
				log.Printf("Domain event pruning: %v", err)
			}
		} else if deleted > 0 {
			log.Printf("Pruned %d domain events older than %s", deleted, now.Add(-eventRetention).Format(time.DateOnly))
		}
	}
	for _, key := range s.store.WorkspaceKeys() {
		if _, err := s.purgeExpiredTrash(ctx, key, now); err != nil && ctx.Err() == nil {
			log.Printf("Trash purge workspace=%s: %v", key, err)
		}
	}
	for _, key := range s.store.WorkspaceKeys() {
		if _, err := s.purgeDeletedAgentSessions(ctx, key, now.Add(-trashRetention)); err != nil && ctx.Err() == nil {
			log.Printf("Agent chat purge workspace=%s: %v", key, err)
		}
	}
	if err := s.migrateOffloadedContent(ctx); err != nil && ctx.Err() == nil {
		log.Printf("Content offload migration: %v", err)
	}
	if sweep {
		if deleted, err := s.store.SweepOffloadedContent(ctx, now, offloadSweepGrace); err != nil && ctx.Err() == nil {
			log.Printf("Offloaded content sweep: %v", err)
		} else if deleted > 0 {
			log.Printf("Deleted %d unreferenced offloaded bodies", deleted)
		}
	}
}

// pruneDomainEvents deletes events created before cutoff, one batch at a time.
func (s *server) pruneDomainEvents(ctx context.Context, cutoff time.Time) (int, error) {
	total := 0
	for {
		deleted, err := s.store.PruneDomainEvents(ctx, cutoff)
		total += deleted
		if err != nil || deleted < store.DomainEventPruneBatch {
			return total, err
		}
		if !maintenanceSleep(ctx) {
			return total, ctx.Err()
		}
	}
}

func (s *server) migrateOffloadedContent(ctx context.Context) error {
	for {
		_, done, err := s.store.MigrateOffloadedContent(ctx, 16)
		if err != nil || done {
			return err
		}
		if !maintenanceSleep(ctx) {
			return ctx.Err()
		}
	}
}

func maintenanceSleep(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(maintenancePause):
		return true
	}
}

// purgeExpiredTrash permanently deletes a workspace's expired trash entries,
// at most trashPurgeBatch per write, with what they keep alive elsewhere
// (deleted documents' comment threads and collaboration logs, deleted loops'
// run history). It returns how many entries it purged.
func (s *server) purgeExpiredTrash(ctx context.Context, key string, now time.Time) (int, error) {
	purged := 0
	for {
		data, ok := s.store.WorkspaceMetadataFields(key, "trash")
		if !ok {
			return purged, nil
		}
		batch := map[string]bool{}
		for _, entry := range data.Trash {
			if trashExpired(entry, now) {
				batch[entry.ID] = true
				if len(batch) == trashPurgeBatch {
					break
				}
			}
		}
		if len(batch) == 0 {
			return purged, nil
		}
		inBatch := func(entry domain.TrashEntry) bool { return batch[entry.ID] }
		scope := store.WithMutationScope(ctx, store.MutationScope{Resolve: func(current domain.Bootstrap) store.MutationScope {
			documentIDs, _ := trashedDocumentOwners(current.Trash, inBatch)
			return store.MutationScope{Resources: documentIDs}
		}})
		var collaborationIDs, loopIDs []string
		removed := 0
		err := s.store.MutateWorkspace(scope, key, "schedules.maintained", "advanced_schedules", nil, func(next *domain.Bootstrap) error {
			documentIDs, documentCollaborationIDs := trashedDocumentOwners(next.Trash, inBatch)
			for _, documentID := range documentIDs {
				delete(next.Comments, documentID)
			}
			loops := trashedLoopIDs(next.Trash, inBatch)
			before := len(next.Trash)
			next.Trash = slices.DeleteFunc(next.Trash, inBatch)
			if removed = before - len(next.Trash); removed == 0 {
				return store.ErrNoMutation
			}
			collaborationIDs, loopIDs = documentCollaborationIDs, loops
			return nil
		})
		if err != nil {
			return purged, err
		}
		if removed == 0 {
			return purged, nil
		}
		s.purgeDocumentContent(ctx, key, collaborationIDs)
		s.purgeLoopRuns(ctx, key, loopIDs)
		purged += removed
		if len(batch) < trashPurgeBatch {
			return purged, nil
		}
		if !maintenanceSleep(ctx) {
			return purged, ctx.Err()
		}
	}
}
