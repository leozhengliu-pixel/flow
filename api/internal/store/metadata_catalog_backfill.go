package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

const MetadataCatalogBackfillBatch = 128

const metadataCatalogBatchBytes = 512 << 10

type MetadataCatalogProgress struct {
	Processed int
	Pruned    int
	Complete  bool
	Cursor    string
}

type metadataCatalogCheckpoint struct {
	last, digest, revision string
	started, phase         int
	omissions              int64
}

func readMetadataCatalogCheckpoint(ctx context.Context, tx *sqlTx, workspace, kind string) (metadataCatalogCheckpoint, error) {
	var point metadataCatalogCheckpoint
	err := tx.QueryRowContext(ctx, `SELECT last_record_key,started,phase,omissions,digest,revision FROM metadata_catalog_collections WHERE workspace_key=? AND field=?`, workspace, kind).Scan(&point.last, &point.started, &point.phase, &point.omissions, &point.digest, &point.revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return point, err
}

// BackfillMetadataCatalog prepares at most 128 records / 512 KiB of summaries
// in a read snapshot, then atomically installs them with a CAS checkpoint.
// Large-body hashing never holds SQLite's writer. Old canonical writes may
// race this shadow pass; finishing it still does not establish read coverage.
func (s *SQLiteStore) BackfillMetadataCatalog(ctx context.Context, workspace, kind string) (MetadataCatalogProgress, error) {
	progress := MetadataCatalogProgress{}
	if workspace == "" || !validMetadataKey(RecordKey{Kind: kind}) {
		return progress, ErrMetadataRecordQuery
	}
	read, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return progress, err
	}
	defer read.Rollback()
	var workspaceID string
	missingWorkspace := false
	if s.lifecycle != nil {
		err := read.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&workspaceID)
		if errors.Is(err, sql.ErrNoRows) {
			missingWorkspace, err = true, nil
		}
		if err != nil {
			return progress, err
		}
	}
	before, err := readMetadataCatalogCheckpoint(ctx, read, workspace, kind)
	if err != nil {
		return progress, err
	}
	if before.phase == 2 && !missingWorkspace {
		return MetadataCatalogProgress{Complete: true}, nil
	}
	position := before
	if missingWorkspace {
		// Old binaries do not know these tables. Reconcile their workspace
		// deletions too, removing orphan sidecars in bounded batches.
		position.phase, position.started, position.last = 1, 0, ""
	}
	after := position
	where, args := `workspace_key=? AND field=?`, []any{workspace, kind}
	if position.started != 0 {
		where += ` AND record_key>?`
		args = append(args, position.last)
	}
	table, columns := "workspace_metadata_records", "record_key,collection_order,LENGTH(data)"
	if position.phase == 1 {
		table, columns = "metadata_catalog", "record_key,collection_order,LENGTH(summary)"
	}
	rows, err := read.QueryContext(ctx, `SELECT `+columns+` FROM `+table+` WHERE `+where+` ORDER BY record_key LIMIT 128`, args...)
	if err != nil {
		return progress, err
	}
	batch := make([]metadataCatalogRow, 0, MetadataCatalogBackfillBatch)
	for rows.Next() {
		row := metadataCatalogRow{key: RecordKey{Kind: kind}}
		if err := rows.Scan(&row.key.ID, &row.order, &row.bodyBytes); err != nil {
			rows.Close()
			return progress, err
		}
		batch = append(batch, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return progress, err
	}
	prepared, removed := []metadataCatalogRow{}, []RecordKey{}
	preparedBytes, writeBytes := 0, 0
	for _, target := range batch {
		if position.phase == 0 {
			row, err := s.projectCanonicalMetadata(ctx, read, workspace, target.key, target.bodyBytes)
			if err != nil {
				return progress, err
			}
			row.order = target.order
			cost := metadataCatalogWriteBytes(workspace, row)
			if len(prepared) > 0 && (preparedBytes+len(row.summary) > metadataCatalogBatchBytes || writeBytes+cost > 4<<20) {
				break
			}
			prepared = append(prepared, row)
			preparedBytes += len(row.summary)
			writeBytes += cost
			if row.omitted {
				after.omissions++
			}
			stamp, _ := json.Marshal([]any{after.digest, target.key.ID, row.order, row.version})
			after.digest = metadataFingerprint(stamp)
			progress.Processed++
		} else {
			if preparedBytes > 0 && preparedBytes+int(target.bodyBytes) > metadataCatalogBatchBytes {
				break
			}
			preparedBytes += int(target.bodyBytes)
			var exists int
			if !missingWorkspace {
				if err := read.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, kind, target.key.ID).Scan(&exists); err != nil {
					return progress, err
				}
			}
			if exists == 0 {
				removed = append(removed, target.key)
				progress.Pruned++
			}
		}
		after.last, after.started = target.key.ID, 1
	}
	if len(batch) == 0 {
		after.phase++
		after.last, after.started = "", 0
	}
	if after.phase == 2 {
		// This digest describes the records observed by the shadow pass. It
		// is not a transactional revision or proof of old-writer coverage.
		after.revision, progress.Complete = metadataFingerprint([]byte(after.digest)), true
	}
	if err := read.Rollback(); err != nil {
		return progress, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return progress, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_catalog_collections(workspace_key,field,last_record_key,started,phase,omissions,digest,revision) VALUES(?,?,?,0,0,0,'','') ON CONFLICT(workspace_key,field) DO NOTHING`, workspace, kind, ""); err != nil {
		return progress, err
	}
	// Reserve the checkpoint (and SQLite's writer) only for bounded writes.
	if _, err := tx.ExecContext(ctx, `UPDATE metadata_catalog_collections SET phase=phase WHERE workspace_key=? AND field=?`, workspace, kind); err != nil {
		return progress, err
	}
	if s.lifecycle != nil {
		// A purge removes the workspace catalog row before deleting owned
		// tables, starting with the checkpoint this transaction has locked.
		// Recheck after that lock; do not lock the workspace row in reverse
		// order to canonical writers and workspace renames.
		var currentID string
		err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&currentID)
		if errors.Is(err, sql.ErrNoRows) {
			if !missingWorkspace {
				return MetadataCatalogProgress{Complete: true}, nil
			}
			err = nil
		}
		if err != nil {
			return progress, err
		}
		if currentID != workspaceID || missingWorkspace && currentID != "" {
			return MetadataCatalogProgress{}, ErrConcurrentWrite
		}
	}
	current, err := readMetadataCatalogCheckpoint(ctx, tx, workspace, kind)
	if err != nil {
		return progress, err
	}
	if current != before {
		return MetadataCatalogProgress{}, ErrConcurrentWrite
	}
	for _, row := range prepared {
		if err := writeMetadataCatalogRow(ctx, tx, workspace, row); err != nil {
			return progress, err
		}
	}
	for _, key := range removed {
		for _, table := range []string{"metadata_catalog_relations", "metadata_catalog"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_key=? AND field=? AND record_key=?`, workspace, kind, key.ID); err != nil {
				return progress, err
			}
		}
	}
	if missingWorkspace && progress.Complete {
		if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_catalog_collections WHERE workspace_key=? AND field=?`, workspace, kind); err != nil {
			return progress, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE metadata_catalog_collections SET last_record_key=?,started=?,phase=?,omissions=?,digest=?,revision=? WHERE workspace_key=? AND field=?`, after.last, after.started, after.phase, after.omissions, after.digest, after.revision, workspace, kind); err != nil {
			return progress, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MetadataCatalogProgress{}, err
	}
	progress.Cursor = after.last
	return progress, nil
}

func writeMetadataCatalogRow(ctx context.Context, tx *sqlTx, workspace string, row metadataCatalogRow) error {
	omitted := 0
	if row.omitted {
		omitted = 1
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO metadata_catalog(workspace_key,field,record_key,collection_order,version,body_bytes,summary,omitted,created_at,updated_at,archived) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,version=excluded.version,body_bytes=excluded.body_bytes,summary=excluded.summary,omitted=excluded.omitted,created_at=excluded.created_at,updated_at=excluded.updated_at,archived=excluded.archived`, workspace, row.key.Kind, row.key.ID, row.order, row.version, row.bodyBytes, row.summary, omitted, row.created, row.updated, row.archived)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_catalog_relations WHERE workspace_key=? AND field=? AND record_key=?`, workspace, row.key.Kind, row.key.ID); err != nil {
		return err
	}
	for relation, values := range metadataCatalogRelations(row.summary) {
		seen := map[string]bool{}
		for _, value := range values {
			if seen[value] {
				continue
			}
			seen[value] = true
			if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_catalog_relations(workspace_key,field,record_key,relation,value_hash,value) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key,relation,value_hash) DO NOTHING`, workspace, row.key.Kind, row.key.ID, relation, metadataFingerprint([]byte(value)), value); err != nil {
				return err
			}
		}
	}
	return nil
}

func metadataCatalogWriteBytes(workspace string, row metadataCatalogRow) int {
	cost := len(row.summary) + 1024
	for relation, values := range metadataCatalogRelations(row.summary) {
		seen := map[string]bool{}
		for _, value := range values {
			if !seen[value] {
				cost += 512 + len(workspace) + len(row.key.Kind) + len(row.key.ID) + len(relation) + len(value)
				seen[value] = true
			}
		}
	}
	return cost
}

func (s *SQLiteStore) ResetMetadataCatalogBackfill(ctx context.Context, workspace, kind string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE metadata_catalog_collections SET last_record_key='',started=0,phase=0,omissions=0,digest='',revision='' WHERE workspace_key=? AND field=?`, workspace, kind)
	return err
}

func metadataCatalogShadowEnabled() bool {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_METADATA_CATALOG")))
	return mode == "shadow"
}

func (s *SQLiteStore) nextMetadataCatalogCollection(ctx context.Context, workspace, kind string) (string, string, error) {
	nextWorkspace, nextKind := "", ""
	for _, table := range []string{"workspace_metadata_records", "metadata_catalog_collections"} {
		var candidateWorkspace, candidateKind string
		err := s.db.QueryRowContext(ctx, `SELECT workspace_key,field FROM `+table+` WHERE (workspace_key,field)>(?,?) ORDER BY workspace_key,field LIMIT 1`, workspace, kind).Scan(&candidateWorkspace, &candidateKind)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", "", err
		}
		if nextWorkspace == "" || candidateWorkspace < nextWorkspace || candidateWorkspace == nextWorkspace && candidateKind < nextKind {
			nextWorkspace, nextKind = candidateWorkspace, candidateKind
		}
	}
	if nextWorkspace == "" {
		return "", "", sql.ErrNoRows
	}
	return nextWorkspace, nextKind, nil
}

// RunMetadataCatalogBackfill is a cancellable, single-worker shadow scan. It
// revisits completed collections to repair changes made by old writers,
// including entirely deleted collections. It installs no canonical triggers.
func (s *SQLiteStore) RunMetadataCatalogBackfill(ctx context.Context) error {
	workspace, kind := "", ""
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
		nextWorkspace, nextKind, err := s.nextMetadataCatalogCollection(ctx, workspace, kind)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			workspace, kind = "", ""
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(30 * time.Second):
				continue
			}
		}
		status, err := s.MetadataCatalogStatus(ctx, nextWorkspace, nextKind)
		if err != nil {
			return err
		}
		if status.Complete {
			if err := s.ResetMetadataCatalogBackfill(ctx, nextWorkspace, nextKind); err != nil {
				return err
			}
		}
		for {
			progress, err := s.BackfillMetadataCatalog(ctx, nextWorkspace, nextKind)
			if err != nil && !errors.Is(err, ErrConcurrentWrite) {
				return err
			}
			if progress.Complete {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
		}
		workspace, kind = nextWorkspace, nextKind
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
