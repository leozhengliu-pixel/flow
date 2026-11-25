package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// MetadataCatalogSkip is a collection one catalog pass could not finish,
// for example because a canonical row is not valid JSON. The collection
// stays incomplete (the SQL completeness check and a bounded start report
// it); later passes try it again.
type MetadataCatalogSkip struct {
	Workspace string `json:"workspace"`
	Field     string `json:"field"`
	Error     string `json:"error"`
}

// MetadataCatalogPassReport summarises one pass over every collection.
type MetadataCatalogPassReport struct {
	// Collections is the number of collections visited, Completed those
	// whose catalog is complete after the visit.
	Collections int `json:"collections"`
	Completed   int `json:"completed"`
	// Normalized counts canonical rows rewritten from TEXT to BLOB.
	Normalized int                   `json:"normalized"`
	Skipped    []MetadataCatalogSkip `json:"skipped"`
	Duration   time.Duration         `json:"-"`
}

// metadataCatalogRevisitPause is the pause between the bounded steps of a
// collection the catalog had already completed: the worker rescans those to
// catch changes made by writers without journal triggers, which is not
// urgent. It is the pace the worker always had.
const metadataCatalogRevisitPause = 25 * time.Millisecond

// metadataCatalogCASRetries bounds the retries of one collection's step that
// keeps losing its checkpoint to concurrent writers.
const metadataCatalogCASRetries = 1024

// metadataCatalogPass runs the catalog backfill over every collection of
// every workspace once. Every step is the same bounded, checkpointed
// transaction pair (BackfillMetadataCatalog), so live writers and other
// replicas' workers interleave with it as before. Collections that still need
// the catalog run back to back, yielding a quarter of each step's time
// between steps; with gentleRevisit, collections already complete are
// rescanned at the worker's original pace. A collection whose step fails is
// logged, reported and skipped; the pass goes on with the next collection.
// Only a cancelled context or a failure to find the next collection ends the
// pass early.
func (s *SQLiteStore) metadataCatalogPass(ctx context.Context, gentleRevisit bool) (report MetadataCatalogPassReport, err error) {
	report.Skipped = []MetadataCatalogSkip{}
	began := time.Now()
	defer func() { report.Duration = time.Since(began) }()
	cursor := metadataCollectionCursor{}
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		next, err := s.nextMetadataCatalogCollection(ctx, cursor)
		if errors.Is(err, sql.ErrNoRows) {
			return report, nil
		}
		if err != nil {
			return report, err
		}
		cursor = next
		workspace, kind := next.Workspace, next.Field
		report.Collections++
		normalized, complete, err := s.completeMetadataCatalogCollection(ctx, workspace, kind, gentleRevisit)
		report.Normalized += normalized
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		if err != nil {
			report.Skipped = append(report.Skipped, MetadataCatalogSkip{Workspace: workspace, Field: kind, Error: err.Error()})
			slog.Warn("metadata catalog: collection skipped", "workspace", workspace, "field", kind, "error", err.Error(),
				"hint", "the collection stays incomplete; fix or remove the row, the next pass retries it")
			continue
		}
		if complete {
			report.Completed++
		}
	}
}

func (s *SQLiteStore) completeMetadataCatalogCollection(ctx context.Context, workspace, kind string, gentleRevisit bool) (normalized int, complete bool, err error) {
	status, err := s.MetadataCatalogStatus(ctx, workspace, kind)
	if err != nil {
		return 0, false, err
	}
	gentle := gentleRevisit && status.Complete
	if status.Complete {
		// Revisit: writes after the previous pass may have changed it.
		if err := s.ResetMetadataCatalogBackfill(ctx, workspace, kind); err != nil {
			return 0, false, err
		}
	}
	normalized, err = s.normalizeMetadataRecordStorage(ctx, workspace, kind)
	if err != nil {
		return normalized, false, err
	}
	conflicts := 0
	for {
		began := time.Now()
		progress, err := s.BackfillMetadataCatalog(ctx, workspace, kind)
		if errors.Is(err, ErrConcurrentWrite) {
			if conflicts++; conflicts > metadataCatalogCASRetries {
				return normalized, false, fmt.Errorf("catalog checkpoint kept changing under concurrent writers: %w", err)
			}
		} else if err != nil {
			return normalized, false, err
		} else if progress.Complete {
			return normalized, true, nil
		}
		pause := time.Since(began) / 4
		if gentle {
			pause = metadataCatalogRevisitPause
		}
		if err := sleepContext(ctx, min(pause, metadataCatalogRevisitPause)); err != nil {
			return normalized, false, err
		}
	}
}

// normalizeMetadataRecordStorage rewrites the collection's canonical rows
// that an older SQLite writer stored as TEXT (or any type other than BLOB)
// as a BLOB of the same bytes, the form every current writer uses. Readers
// that measure a row with LENGTH before streaming it in byte ranges then see
// its byte length. The bytes, and so the record and its version hash, do not
// change. Each batch is an ordinary write transaction (journal capture and
// cache revisions as for any canonical write); a row a concurrent writer has
// replaced in the meantime is already a BLOB and is left alone. Bounded
// replicas leave rows to their own writers; the flag-off worker and
// `server metadata-prepare` normalise before activation.
func (s *SQLiteStore) normalizeMetadataRecordStorage(ctx context.Context, workspace, kind string) (int, error) {
	if s.dialect != "sqlite" || s.BoundedMetadataEnabled() {
		return 0, nil
	}
	normalized := 0
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT record_key,LENGTH(CAST(data AS BLOB)) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND typeof(data)<>'blob' ORDER BY record_key LIMIT 128`, workspace, kind)
		if err != nil {
			return normalized, err
		}
		keys, bytes := []string{}, int64(0)
		for rows.Next() {
			var key string
			var size int64
			if err := rows.Scan(&key, &size); err != nil {
				rows.Close()
				return normalized, err
			}
			if len(keys) > 0 && bytes+size > 4<<20 {
				break
			}
			keys, bytes = append(keys, key), bytes+size
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil || len(keys) == 0 {
			return normalized, err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return normalized, err
		}
		changed := 0
		for _, key := range keys {
			result, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=CAST(data AS BLOB) WHERE workspace_key=? AND field=? AND record_key=? AND typeof(data)<>'blob'`, workspace, kind, key)
			if err != nil {
				_ = tx.Rollback()
				return normalized, err
			}
			count, _ := result.RowsAffected()
			changed += int(count)
		}
		if err := tx.Commit(); err != nil {
			return normalized, err
		}
		normalized += changed
		slog.Info("metadata catalog: canonical rows stored as text rewritten as blobs", "workspace", workspace, "field", kind, "rows", changed)
	}
}

func metadataByteLength(dialect, column string) string {
	if dialect == "sqlite" {
		return "LENGTH(CAST(" + column + " AS BLOB))"
	}
	return "LENGTH(" + column + ")"
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RunMetadataCatalogBackfill is the FLOW_METADATA_CATALOG=shadow worker: a
// cancellable loop of passes, 30 seconds apart. Each pass first repairs the
// record journal's old-writer coverage (RepairRecordJournalPass) where the
// journal is enabled, then runs the catalog backfill over every collection
// (metadataCatalogPass), rescanning complete collections at the original
// gentle pace to repair changes made by old writers, including entirely
// deleted collections. It installs no canonical triggers. One failing
// collection or workspace is logged and skipped; it never stops the worker.
func (s *SQLiteStore) RunMetadataCatalogBackfill(ctx context.Context) error {
	for {
		if s.recordJournalRepairable() {
			repair, err := s.RepairRecordJournalPass(ctx)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				slog.Warn("record journal repair pass failed", "error", err.Error())
			} else if repair.Workspaces > 0 {
				slog.Info("record journal repair pass finished", "workspaces", repair.Workspaces, "repaired", repair.Repaired,
					"incomplete", len(repair.Incomplete), "duration", repair.Duration.Round(time.Millisecond).String())
			}
		}
		report, err := s.metadataCatalogPass(ctx, true)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			slog.Warn("metadata catalog pass failed", "error", err.Error(), "collections", report.Collections)
		} else {
			level := slog.LevelInfo
			if len(report.Skipped) > 0 {
				level = slog.LevelWarn
			}
			slog.Log(ctx, level, "metadata catalog pass finished", "collections", report.Collections, "complete", report.Completed,
				"skipped", len(report.Skipped), "normalized", report.Normalized, "duration", report.Duration.Round(time.Millisecond).String())
		}
		if err := sleepContext(ctx, 30*time.Second); err != nil {
			return err
		}
	}
}

func (s *SQLiteStore) startMetadataCatalog() {
	if metadataCatalogShadowEnabled() {
		go func() {
			if err := s.RunMetadataCatalogBackfill(s.WorkerContext()); err != nil && s.WorkerContext().Err() == nil {
				slog.Warn("metadata catalog shadow worker stopped", "error", fmt.Sprint(err))
			}
		}()
	}
}
