//go:build loadtest

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// The bounded-metadata load rig (scripts/loadrig, cmd/server/loadrig_*).
// Built only with -tags loadtest; production builds contain none of this.

func init() {
	// FLOW_LOADRIG_ALLOW_COLD_UNMIGRATED=1 lets a load-test build start
	// FLOW_BOUNDED_METADATA while COLD consumers are still registered as
	// unmigrated (default-COLD routing serves them). The HOT inventory must
	// be empty, and every other activation check (record hydration, journal,
	// CRDT runtime, peer shadow) still applies.
	if os.Getenv("FLOW_LOADRIG_ALLOW_COLD_UNMIGRATED") == "1" {
		loadRigAllowColdConsumers = true
	}
}

// LoadRigColdCounters reports the process-wide bounded-metadata counters
// the load rig samples: COLD slot use and waits, COLD facade and legacy-view
// decodes, COLD legacy write-lock holds and writer lock waits.
func LoadRigColdCounters() map[string]int64 {
	waiting := int64(0)
	queues := int64(0)
	coldSlotQueues.Range(func(_, value any) bool {
		waiting += value.(*coldSlotQueue).waiting.Load()
		queues++
		return true
	})
	facades, facadeTime, viewTime := ColdHydrationForTesting()
	holds, holdTotal, holdMax := ColdWriteLockHoldForTesting()
	return map[string]int64{
		"coldSlotAcquisitions":  coldSlotAcquisitions.Load(),
		"coldSlotRefusals":      coldSlotRefusals.Load(),
		"coldSlotsHeld":         coldSlotHolders.Load(),
		"coldSlotWaiting":       waiting,
		"coldSlotQueues":        queues,
		"coldSlotWaits":         coldSlotWaits.Load(),
		"coldSlotWaitNanos":     coldSlotWaitNanos.Load(),
		"coldSlotWaitMaxNanos":  coldSlotWaitMax.Load(),
		"coldFacadeDecodes":     facades,
		"coldFacadeDecodeNanos": int64(facadeTime),
		"coldViewReads":         coldAccessorReads.Load(),
		"coldViewDecodeNanos":   int64(viewTime),
		"coldViewFailures":      coldAccessorFailures.Load(),
		"coldWriteLockHolds":    holds,
		"coldWriteLockNanos":    int64(holdTotal),
		"coldWriteLockMaxNanos": int64(holdMax),
		"writerLockWaits":       writerLockWaits.waits.Load(),
		"writerLockWaitNanos":   writerLockWaits.nanos.Load(),
		"writerLockWaitMax":     writerLockWaits.longest.Load(),
		"coldWriterLockWaits":   coldWriterLockWaits.waits.Load(),
		"coldWriterWaitNanos":   coldWriterLockWaits.nanos.Load(),
		"coldWriterWaitMax":     coldWriterLockWaits.longest.Load(),
	}
}

// SetLoadRigSeedBatchBytes sets SeedSyntheticMetadata's per-transaction
// write budget (default 6 MiB) and returns the previous value.
func SetLoadRigSeedBatchBytes(bytes int) int {
	previous := syntheticSeedBatchBytes
	if bytes > 0 {
		syntheticSeedBatchBytes = bytes
	}
	return previous
}

// LoadRigContent is one content record (a comment, an activity event, field
// history) the seed writes for an issue.
type LoadRigContent struct {
	Kind, Resource string
	Item           any
}

// SeedLoadRigContent writes content records through the production content
// row writer (the same rows, actor index and activity cycle references the
// server writes), in one transaction per call. Callers pass bounded batches.
func (s *SQLiteStore) SeedLoadRigContent(ctx context.Context, workspace string, items []LoadRigContent) error {
	if len(items) == 0 {
		return nil
	}
	rows := make([]contentRecordRow, 0, len(items))
	for _, item := range items {
		raw, err := json.Marshal(item.Item)
		if err != nil {
			return err
		}
		row, err := buildContentRecordRow(workspace, item.Kind, item.Resource, raw)
		if err != nil {
			return fmt.Errorf("load rig content %s/%s: %w", item.Kind, item.Resource, err)
		}
		rows = append(rows, row)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeContentRecordRows(ctx, tx, rows); err != nil {
		return err
	}
	return tx.Commit()
}

// ExtendLoadRigSessions keeps the seeded sessions valid for ttl, so a seeded
// database can be driven on later days. It touches only sessions of the
// given users.
func (s *SQLiteStore) ExtendLoadRigSessions(ctx context.Context, users []string, ttl time.Duration) error {
	expires := time.Now().UTC().Add(ttl).Format(time.RFC3339Nano)
	for start := 0; start < len(users); start += 200 {
		clause, args := bindList("user_id", users[start:min(start+200, len(users))])
		if _, err := s.db.ExecContext(ctx, `UPDATE auth_sessions SET expires_at=? WHERE `+clause, append([]any{expires}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

// LoadRigTableRows counts the rows of the tables the seed fills, for the
// seed report.
func (s *SQLiteStore) LoadRigTableRows(ctx context.Context, workspace string) map[string]int64 {
	counts := map[string]int64{}
	for _, table := range []string{"issue_records", "workspace_content_records", "workspace_metadata_records", "domain_events", "record_journal"} {
		var count int64
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE workspace_key=?`
		args := []any{workspace}
		if table == "record_journal" {
			query, args = `SELECT COUNT(*) FROM `+table, nil
		}
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
			slog.Warn("load rig: row count", "table", table, "error", err)
			continue
		}
		counts[table] = count
	}
	return counts
}

// LoadRigDatabaseBytes reports the database size the driver sees: the
// SQLite page total, or pg_database_size / the MySQL schema total.
func (s *SQLiteStore) LoadRigDatabaseBytes(ctx context.Context) int64 {
	var size int64
	var err error
	switch s.dialect {
	case "postgres":
		err = s.db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&size)
	case "mysql":
		err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(data_length+index_length),0) FROM information_schema.tables WHERE table_schema=DATABASE()`).Scan(&size)
	default:
		err = s.db.QueryRowContext(ctx, `SELECT page_count*page_size FROM pragma_page_count(), pragma_page_size()`).Scan(&size)
	}
	if err != nil {
		slog.Warn("load rig: database size", "error", err)
	}
	return size
}

// CompleteLoadRigMetadataCatalog runs the metadata catalog backfill (the
// FLOW_METADATA_CATALOG=shadow worker) once over every collection of every
// workspace, to completion. A bounded store refuses to start until the
// catalog covers every canonical record (validateResidentBackfill).
func (s *SQLiteStore) CompleteLoadRigMetadataCatalog(ctx context.Context) (int, error) {
	workspace, kind, collections := "", "", 0
	for {
		next, nextKind, err := s.nextMetadataCatalogCollection(ctx, workspace, kind)
		if errors.Is(err, sql.ErrNoRows) {
			return collections, nil
		}
		if err != nil {
			return collections, err
		}
		status, err := s.MetadataCatalogStatus(ctx, next, nextKind)
		if err != nil {
			return collections, err
		}
		if status.Complete {
			// Revisit, as the shadow worker does: writes after its pass
			// may have changed the collection.
			if err := s.ResetMetadataCatalogBackfill(ctx, next, nextKind); err != nil {
				return collections, err
			}
		}
		for {
			progress, err := s.BackfillMetadataCatalog(ctx, next, nextKind)
			if err != nil && !errors.Is(err, ErrConcurrentWrite) {
				return collections, fmt.Errorf("catalog %s/%s: %w", next, nextKind, err)
			}
			if err == nil && progress.Complete {
				break
			}
		}
		collections++
		workspace, kind = next, nextKind
	}
}
