package store

import (
	"context"
	"encoding/json"
)

// RepairRecordJournal repairs at most 128 old-writer keys and 128 events in
// one transaction. Their original transaction boundaries are unknowable, so
// the repair publishes a workspace invalidation, never a partial upsert set.
// Continue while remaining is true. Normal serving still rejects gaps until
// every batch has committed. P05's bounded catalog pass seeds untouched legacy
// metadata versions; this method reconciles writes made during that pass.
func (s *SQLiteStore) RepairRecordJournal(ctx context.Context, workspaceID string) (remaining bool, err error) {
	if !s.db.journalReady.Load() || recordJournalMode() == "off" {
		return true, ErrRecordJournalUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return true, err
	}
	defer tx.Rollback()
	if err := tx.beginRecordJournal(ctx); err != nil {
		return true, err
	}
	tx.journalRepair = true
	var workspace string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_key FROM workspace_states WHERE workspace_id=?`, workspaceID).Scan(&workspace); err != nil {
		return true, err
	}
	if _, err := tx.repairRecordJournalActivation(ctx, workspace); err != nil {
		return true, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.tx_id,d.field,d.record_key,COALESCE(LENGTH(m.data),0) FROM record_journal_dirty d LEFT JOIN workspace_metadata_records m ON d.source='workspace_metadata_records' AND m.workspace_key=d.workspace_key AND m.field=d.field AND m.record_key=d.record_key WHERE d.workspace_key=? AND d.tx_id<>? ORDER BY d.tx_id,d.field,d.record_key LIMIT 128`, workspace, tx.journalToken)
	if err != nil {
		return true, err
	}
	type dirtyKey struct{ token, field, id string }
	keys := []dirtyKey{}
	cost, budget := int64(0), tx.recordJournalRepairBudget()
	for rows.Next() {
		var key dirtyKey
		var size int64
		if err := rows.Scan(&key.token, &key.field, &key.id, &size); err != nil {
			rows.Close()
			return true, err
		}
		cost += recordJournalRepairKeyCost(size)
		if len(keys) > 0 && cost > budget {
			break
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return true, err
	}
	for _, key := range keys {
		// Multiple old tokens can name the same key. Collapse them without
		// overwriting its current version, tombstone or canonical value.
		if _, err := tx.ExecContext(ctx, `INSERT INTO record_journal_dirty(tx_id,workspace_key,field,record_key,source) SELECT ?,workspace_key,field,record_key,source FROM record_journal_dirty WHERE tx_id=? AND workspace_key=? AND field=? AND record_key=? ON CONFLICT(tx_id,workspace_key,field,record_key) DO NOTHING`, tx.journalToken, key.token, workspace, key.field, key.id); err != nil {
			return true, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM record_journal_dirty WHERE tx_id=? AND workspace_key=? AND field=? AND record_key=?`, key.token, workspace, key.field, key.id); err != nil {
			return true, err
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT event_id FROM record_journal_event_pending WHERE workspace_id=? ORDER BY event_id LIMIT 128`, workspaceID)
	if err != nil {
		return true, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return true, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return true, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE record_journal_event_pending SET tx_id=? WHERE event_id=?`, tx.journalToken, id); err != nil {
			return true, err
		}
	}
	// An old sequencer may have removed a journal marker as an unknown
	// event. Requeue a bounded invalidation in place of its stale upserts.
	rows, err = tx.QueryContext(ctx, `SELECT j.id FROM record_journal j WHERE j.workspace_id=? AND j.seq IS NULL AND `+recordJournalMissingSequenceSQL(s.dialect)+` ORDER BY j.created_at,j.id LIMIT 128`, workspaceID)
	if err != nil {
		return true, err
	}
	stranded := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return true, err
		}
		stranded = append(stranded, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return true, err
	}
	epoch, err := readAuthzEpoch(ctx, tx, workspaceID)
	if err != nil {
		return true, err
	}
	for _, id := range stranded {
		raw, _ := json.Marshal(CommittedChange{TxID: id, AuthzEpoch: epoch, Records: []RecordDelta{}, Invalidations: []string{"workspace"}})
		if _, err := tx.ExecContext(ctx, `UPDATE record_journal SET payload=?,authz_epoch=? WHERE id=?`, raw, epoch, id); err != nil {
			return true, err
		}
		if tx.dialect != "sqlite" {
			tx.sequencing = append(tx.sequencing, pendingEventSequence{workspaceID: workspaceID, eventID: id})
		}
	}
	if tx.dialect == "sqlite" {
		if err := tx.numberRecordJournal(ctx, workspaceID, stranded); err != nil {
			return true, err
		}
	}
	if err := tx.Commit(); err != nil {
		return true, err
	}
	var gaps int
	query := `SELECT (SELECT COUNT(*) FROM record_journal_dirty WHERE workspace_key=?)+(SELECT COUNT(*) FROM record_journal_coverage WHERE workspace_key=?)+(SELECT COUNT(*) FROM record_journal_event_pending WHERE workspace_id=?)+(SELECT COUNT(*) FROM record_journal j WHERE j.workspace_id=? AND j.seq IS NULL AND ` + recordJournalMissingSequenceSQL(s.dialect) + `)`
	args := []any{workspace, workspace, workspaceID, workspaceID}
	if s.dialect == "sqlite" {
		query += `+(SELECT COUNT(*) FROM record_journal_context i,json_each(i.keys) k WHERE i.id=1 AND json_extract(k.value,'$.workspace')=?)
			+(SELECT COUNT(*) FROM record_journal_context i,json_each(i.events) e WHERE i.id=1 AND json_extract(e.value,'$.workspaceId')=?)`
		args = append(args, workspace, workspaceID)
	}
	err = s.db.QueryRowContext(ctx, query, args...).Scan(&gaps)
	return gaps != 0, err
}

// A dual-writing server can have catalog rows for every collection. Once an
// old replica removes the workspace, repair all its orphan summaries in
// bounded batches, even when only one collection's backfill was requested.
func (s *SQLiteStore) pruneOrphanRecordCatalog(ctx context.Context, workspace string) (MetadataCatalogProgress, error) {
	progress := MetadataCatalogProgress{}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return progress, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE metadata_catalog_collections SET revision=revision WHERE workspace_key=?`, workspace); err != nil {
		return progress, err
	}
	var present int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&present); err != nil {
		return progress, err
	}
	if present != 0 {
		return MetadataCatalogProgress{}, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT field,record_key FROM metadata_catalog WHERE workspace_key=? ORDER BY field,record_key LIMIT 128`, workspace)
	if err != nil {
		return progress, err
	}
	keys := []RecordKey{}
	for rows.Next() {
		var key RecordKey
		if err := rows.Scan(&key.Kind, &key.ID); err != nil {
			rows.Close()
			return progress, err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return progress, err
	}
	for _, key := range keys {
		for _, table := range []string{"metadata_catalog_relations", "metadata_catalog"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.Kind, key.ID); err != nil {
				return progress, err
			}
		}
	}
	progress.Pruned = len(keys)
	complete := len(keys) < 128
	for _, table := range []string{"record_versions", "record_journal_dirty"} {
		rows, err := tx.QueryContext(ctx, `SELECT field,record_key FROM `+table+` WHERE workspace_key=? ORDER BY field,record_key LIMIT 128`, workspace)
		if err != nil {
			return progress, err
		}
		batch := []RecordKey{}
		for rows.Next() {
			var key RecordKey
			if err := rows.Scan(&key.Kind, &key.ID); err != nil {
				rows.Close()
				return progress, err
			}
			batch = append(batch, key)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return progress, err
		}
		for _, key := range batch {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.Kind, key.ID); err != nil {
				return progress, err
			}
		}
		complete = complete && len(batch) < 128
	}
	if complete {
		if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_catalog_collections WHERE workspace_key=?`, workspace); err != nil {
			return progress, err
		}
		progress.Complete = true
	}
	return progress, tx.Commit()
}
