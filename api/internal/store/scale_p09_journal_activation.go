package store

import (
	"context"
	"database/sql"
	"errors"
)

func (tx *sqlTx) recordJournalRepairBudget() int64 {
	if tx.maxTransactionBytes <= 0 {
		return 1 << 62
	}
	return int64(max(tx.maxTransactionBytes-tx.writtenBytes, 2048)) / 2
}

func recordJournalRepairKeyCost(size int64) int64 {
	// Relationship inserts can cost much more than their compact JSON. Keep
	// room for them and the deferred audit/recipient participants. Always
	// allow one key so a large body cannot stall the repair cursor.
	return min(size, metadataSummaryMaxBytes)*512 + 2048
}

// Each restart from off loses knowledge of which records changed. Walk the
// union of current keys and retained versions, including disappeared records,
// in batches; never claim a pre-disable cursor covers this interval.
func (tx *sqlTx) repairRecordJournalActivation(ctx context.Context, workspace string) (bool, error) {
	var phase int
	var field, id string
	err := tx.QueryRowContext(ctx, `SELECT phase,last_field,last_key FROM record_journal_coverage WHERE workspace_key=?`, workspace).Scan(&phase, &field, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	contentID := recordJournalContentKeySQL(tx.dialect, "resource_id", "id")
	query := `WITH canonical AS (
		SELECT field,record_key,'workspace_metadata_records' source FROM workspace_metadata_records WHERE workspace_key=?
		UNION ALL SELECT '@core','root','workspace_states' FROM workspace_states WHERE workspace_key=?
		UNION ALL SELECT 'issues',id,'issue_records' FROM issue_records WHERE workspace_key=?
		UNION ALL SELECT CASE kind WHEN 'comment' THEN 'comments' WHEN 'activity' THEN 'activities' WHEN 'notification' THEN 'notifications' WHEN 'delivery' THEN 'notificationDeliveries' ELSE kind END,` + contentID + `,'workspace_content_records' FROM workspace_content_records WHERE workspace_key=?
	), candidates AS (
		SELECT field,record_key,source,0 deleted FROM canonical
		UNION ALL SELECT v.field,v.record_key,CASE WHEN v.field='@core' THEN 'workspace_states' WHEN v.field='issues' THEN 'issue_records' WHEN v.field IN ('comments','activities','notifications','notificationDeliveries') THEN 'workspace_content_records' ELSE 'workspace_metadata_records' END,1
		FROM record_versions v WHERE workspace_key=? AND NOT EXISTS(SELECT 1 FROM canonical c WHERE c.field=v.field AND c.record_key=v.record_key)
	) SELECT c.field,c.record_key,c.source,c.deleted,COALESCE((SELECT LENGTH(m.data) FROM workspace_metadata_records m WHERE m.workspace_key=? AND m.field=c.field AND m.record_key=c.record_key),0) FROM candidates c WHERE c.field>? OR (c.field=? AND c.record_key>?) ORDER BY c.field,c.record_key LIMIT 128`
	rows, err := tx.QueryContext(ctx, query, workspace, workspace, workspace, workspace, workspace, workspace, field, field, id)
	if err != nil {
		return true, err
	}
	type candidate struct {
		field, id, source string
		deleted           int
	}
	batch := []candidate{}
	cost, budget, more := int64(0), tx.recordJournalRepairBudget(), false
	for rows.Next() {
		var key candidate
		var size int64
		if err := rows.Scan(&key.field, &key.id, &key.source, &key.deleted, &size); err != nil {
			rows.Close()
			return true, err
		}
		cost += recordJournalRepairKeyCost(size)
		if len(batch) > 0 && cost > budget {
			more = true
			break
		}
		batch = append(batch, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return true, err
	}
	for _, key := range batch {
		if _, err := tx.ExecContext(ctx, `INSERT INTO record_versions(workspace_key,field,record_key,version,deleted) VALUES(?,?,?,1,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET version=record_versions.version+1,deleted=excluded.deleted`, workspace, key.field, key.id, key.deleted); err != nil {
			return true, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO record_journal_dirty(tx_id,workspace_key,field,record_key,source) VALUES(?,?,?,?,?) ON CONFLICT(tx_id,workspace_key,field,record_key) DO UPDATE SET source=excluded.source`, tx.journalToken, workspace, key.field, key.id, key.source); err != nil {
			return true, err
		}
	}
	if len(batch) == 128 || more {
		last := batch[len(batch)-1]
		_, err = tx.ExecContext(ctx, `UPDATE record_journal_coverage SET last_field=?,last_key=? WHERE workspace_key=?`, last.field, last.id, workspace)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM record_journal_coverage WHERE workspace_key=?`, workspace)
	}
	return true, err
}
