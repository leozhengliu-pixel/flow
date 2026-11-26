//go:build loadtest

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// The bounded-metadata load rig (scripts/loadrig, cmd/server/loadrig_*).
// Built only with -tags loadtest; production builds contain none of this.

// LoadRigColdCounters reports the process-wide bounded-metadata counters
// the load rig samples: COLD slot use and waits, COLD facade and legacy-view
// decodes, COLD legacy write-lock holds and writer lock waits.
func LoadRigColdCounters() map[string]int64 {
	counters := ColdMetricsSnapshot()
	for key, value := range ResidentWriteStatsForTesting() {
		counters["residentWrite."+key] = value
	}
	counters["copyRuns"], counters["copyRunFrames"] = metadataLogicalCopyRuns.Load(), metadataLogicalCopyRunFrames.Load()
	counters["imageElementSeeded"] = coldDecodeElementSeeded.Load()
	return counters
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
	expires := formatStoredTime(time.Now().Add(ttl))
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

// CompleteLoadRigMetadataCatalog runs the production catalog pass (the one
// `server metadata-prepare` runs) once over every collection of every
// workspace. A bounded store refuses to start until the catalog covers every
// canonical record (validateResidentBackfill), so a skipped collection fails
// the rig here.
func (s *SQLiteStore) CompleteLoadRigMetadataCatalog(ctx context.Context) (int, error) {
	report, err := s.metadataCatalogPass(ctx, catalogRevisit)
	if err != nil {
		return report.Collections, err
	}
	if len(report.Skipped) > 0 {
		skip := report.Skipped[0]
		return report.Collections, fmt.Errorf("catalog %s/%s: %s (%d collections skipped)", skip.Workspace, skip.Field, skip.Error, len(report.Skipped))
	}
	return report.Collections, nil
}
