package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
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
	if missingWorkspace && s.db.journalReady.Load() {
		if err := read.Rollback(); err != nil {
			return progress, err
		}
		return s.pruneOrphanRecordCatalog(ctx, workspace)
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
	// Byte lengths: SQLite's LENGTH counts characters of a TEXT value, and
	// older writers stored some canonical rows as TEXT. The canonical reader
	// streams bytes, so a character count cut a non-ASCII row short.
	table, columns := "workspace_metadata_records", "record_key,collection_order,"+metadataByteLength(s.dialect, "data")
	if position.phase == 1 {
		table, columns = "metadata_catalog", "record_key,collection_order,"+metadataByteLength(s.dialect, "summary")
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
	if journalDatabaseReady(tx) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO record_versions(workspace_key,field,record_key,version,deleted) SELECT workspace_key,field,record_key,1,0 FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=? ON CONFLICT(workspace_key,field,record_key) DO NOTHING`, workspace, row.key.Kind, row.key.ID); err != nil {
			return err
		}
	}
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

// metadataCollectionCursor is a catalog pass's position in its walk over
// every collection: first those with canonical rows, then (orphans) the
// checkpoints whose canonical rows are all gone, which the pass visits to
// prune their catalog rows.
type metadataCollectionCursor struct {
	Workspace, Field string
	orphans          bool
}

// metadataCollectionOrphanPage bounds the checkpoints one orphan lookup reads.
const metadataCollectionOrphanPage = 64

// nextMetadataCatalogCollection returns the collection after the cursor, or
// sql.ErrNoRows at the end of the walk.
//
// Each half of the walk pages one table in that table's own SQL order, and
// the cursor of each half only ever holds values read from that table, so the
// walk never compares keys across collations. They differ: on MySQL the
// canonical workspace_key and field columns are utf8mb4_0900_ai_ci and the
// catalog's are VARBINARY; on PostgreSQL the canonical ones take the
// database collation and the catalog's are "C". A walk that merged the two
// tables' next keys with Go's byte order sorted "teams" before "teamSettings"
// in one table and after it in the other, and went back and forth between
// them forever. Comparing both tables as bytes in SQL instead (CAST AS
// BINARY on MySQL) cannot use the canonical table's index: every step was a
// scan and sort of the whole table. Here each step is an index range read.
// Collection names are the app's field names, distinct under any collation,
// so the first half meets no ties; a checkpoint is visited in the second half
// only when no canonical row has exactly its key, so no collection is
// visited twice.
func (s *SQLiteStore) nextMetadataCatalogCollection(ctx context.Context, after metadataCollectionCursor) (metadataCollectionCursor, error) {
	if !after.orphans {
		next := metadataCollectionCursor{}
		err := s.db.QueryRowContext(ctx, `SELECT workspace_key,field FROM workspace_metadata_records WHERE `+collectionKeysetAfter(s.dialect)+` ORDER BY workspace_key,field LIMIT 1`, collectionKeysetArgs(s.dialect, after)...).Scan(&next.Workspace, &next.Field)
		if !errors.Is(err, sql.ErrNoRows) {
			return next, err
		}
		after = metadataCollectionCursor{orphans: true}
	}
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,field FROM metadata_catalog_collections WHERE `+collectionKeysetAfter(s.dialect)+` ORDER BY workspace_key,field LIMIT `+fmt.Sprint(metadataCollectionOrphanPage), collectionKeysetArgs(s.dialect, after)...)
		if err != nil {
			return after, err
		}
		page := []metadataCollectionCursor{}
		for rows.Next() {
			next := metadataCollectionCursor{orphans: true}
			if err := rows.Scan(&next.Workspace, &next.Field); err != nil {
				rows.Close()
				return after, err
			}
			page = append(page, next)
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return after, err
		}
		for _, next := range page {
			// The equality finds the index range; on MySQL the binary
			// comparison then keeps only the checkpoint's exact bytes, which
			// utf8mb4_0900_ai_ci equates with other cases and accents.
			exact, args := "", []any{next.Workspace, next.Field}
			if s.dialect == "mysql" {
				exact, args = ` AND CAST(workspace_key AS BINARY)=CAST(? AS BINARY) AND CAST(field AS BINARY)=CAST(? AS BINARY)`, append(args, next.Workspace, next.Field)
			}
			var canonical int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field=?`+exact+` LIMIT 1) r`, args...).Scan(&canonical); err != nil {
				return after, err
			}
			if canonical == 0 {
				return next, nil
			}
		}
		if len(page) < metadataCollectionOrphanPage {
			return after, sql.ErrNoRows
		}
		after = page[len(page)-1]
	}
}

// collectionKeysetAfter is the (workspace_key,field) keyset predicate. MySQL
// evaluates a row-value comparison as a filter over an index scan from the
// first row; the expanded form is a range on the primary key.
func collectionKeysetAfter(dialect string) string {
	if dialect == "mysql" {
		return `(workspace_key>? OR workspace_key=? AND field>?)`
	}
	return `(workspace_key,field)>(?,?)`
}

func collectionKeysetArgs(dialect string, after metadataCollectionCursor) []any {
	if dialect == "mysql" {
		return []any{after.Workspace, after.Workspace, after.Field}
	}
	return []any{after.Workspace, after.Field}
}

// CompleteMetadataCatalogForTesting runs the catalog backfill over every
// collection of every workspace to completion, as an operator does before a
// bounded start (a bounded store refuses an incomplete catalog). Test
// binaries only.
func (s *SQLiteStore) CompleteMetadataCatalogForTesting(ctx context.Context) error {
	if !testing.Testing() {
		return ErrBoundedMetadataCoverage
	}
	cursor := metadataCollectionCursor{}
	for {
		var err error
		cursor, err = s.nextMetadataCatalogCollection(ctx, cursor)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		next, nextKind := cursor.Workspace, cursor.Field
		status, err := s.MetadataCatalogStatus(ctx, next, nextKind)
		if err != nil {
			return err
		}
		if status.Complete {
			if err := s.ResetMetadataCatalogBackfill(ctx, next, nextKind); err != nil {
				return err
			}
		}
		for {
			progress, err := s.BackfillMetadataCatalog(ctx, next, nextKind)
			if err != nil && !errors.Is(err, ErrConcurrentWrite) {
				return err
			}
			if err == nil && progress.Complete {
				break
			}
		}
	}
}
