package store

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"modernc.org/sqlite"
)

const recordJournalSmallFallback = "record journal requires bounded streaming"
const recordJournalSmallWriteBudget = 1 << 20

func init() {
	sqlite.MustRegisterFunction("flow_rj_small", &sqlite.FunctionImpl{NArgs: 1, Deterministic: true, Scalar: func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if n, ok := args[0].(int64); !ok || n != 0 {
			return nil, errors.New(recordJournalSmallFallback)
		}
		return int64(1), nil
	}})
	sqlite.MustRegisterFunction("flow_rj_project", &sqlite.FunctionImpl{NArgs: 7, Deterministic: true, VolatileArgs: true, Scalar: func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		raw, ok := args[0].([]byte)
		if !ok {
			return nil, errors.New(recordJournalSmallFallback)
		}
		row := recordJournalProjection{Kind: args[1].(string), ID: args[2].(string), Order: args[3].(int64), OldOmitted: int(args[4].(int64)), Bytes: int64(len(raw)), Relations: []recordJournalRelation{}}
		if err := projectRecordJournalSummary(raw, &row); err != nil {
			return nil, err
		}
		oldSummary, exists := args[5].([]byte)
		oldOrder, ordered := args[6].(int64)
		row.Rewrite = !exists || !ordered || oldOrder != row.Order || row.OldOmitted != row.Omitted || !bytes.Equal(oldSummary, row.Summary)
		if !row.Rewrite {
			row.Relations = []recordJournalRelation{}
		}
		cost := len(row.Summary) + 2048
		for _, relation := range row.Relations {
			cost += 512 + len(row.Kind) + len(row.ID) + len(relation.Name) + len(relation.Value) + 191
		}
		return json.Marshal(struct {
			recordJournalProjection
			Cost int
		}{row, cost})
	}})
}

// Functions occur only in application SQL against the new input view. Old
// replicas never prepare them, including when they execute canonical triggers.
const recordJournalSQLCommit = `INSERT INTO record_journal_commit_v3(tx_id,data)
	WITH guard AS MATERIALIZED (SELECT flow_rj_small((SELECT COUNT(*) FROM record_journal_dirty WHERE tx_id=(SELECT tx_id FROM record_journal_context WHERE id=1))+(SELECT COUNT(*) FROM record_journal_event_pending WHERE tx_id=(SELECT tx_id FROM record_journal_context WHERE id=1))+` + recordJournalInlineCount + `>16) ok), dirty AS MATERIALIZED (
		SELECT workspace_key,field,record_key,source FROM record_journal_dirty WHERE tx_id=(SELECT tx_id FROM record_journal_context WHERE id=1) AND (SELECT ok FROM guard)=1
		UNION SELECT json_extract(k.value,'$.workspace'),json_extract(k.value,'$.field'),json_extract(k.value,'$.id'),json_extract(k.value,'$.source') FROM record_journal_context i,json_each(i.keys) k WHERE i.id=1 AND (SELECT ok FROM guard)=1
	), events AS MATERIALIZED (
		SELECT event_id,workspace_id FROM record_journal_event_pending WHERE tx_id=(SELECT tx_id FROM record_journal_context WHERE id=1) AND (SELECT ok FROM guard)=1
		UNION SELECT json_extract(e.value,'$.id'),json_extract(e.value,'$.workspaceId') FROM record_journal_context i,json_each(i.events) e WHERE i.id=1 AND (SELECT ok FROM guard)=1
	), affected AS MATERIALIZED (
		SELECT w.workspace_key,w.workspace_id FROM dirty d JOIN workspace_states w ON w.workspace_key=d.workspace_key
		UNION SELECT w.workspace_key,w.workspace_id FROM events p JOIN workspace_states w ON w.workspace_id=p.workspace_id
	), records AS MATERIALIZED (
		SELECT d.workspace_key,d.field,d.record_key,d.source,v.version,v.deleted,
			CASE WHEN d.source='workspace_metadata_records' AND v.deleted=0 THEN jsonb(CAST(flow_rj_project(CASE WHEN LENGTH(CAST(m.data AS BLOB))<=8192 THEN CAST(m.data AS BLOB) ELSE NULL END,d.field,d.record_key,m.collection_order,COALESCE(c.omitted,0),CAST(c.summary AS BLOB),c.collection_order) AS TEXT))
			WHEN d.source='workspace_metadata_records' THEN jsonb_object('Kind',d.field,'ID',d.record_key,'Deleted',json('true'),'Omitted',0,'OldOmitted',COALESCE(c.omitted,0),'Relations',json('[]')) ELSE NULL END projection
		FROM dirty d
		LEFT JOIN record_versions v ON v.workspace_key=d.workspace_key AND v.field=d.field AND v.record_key=d.record_key
		LEFT JOIN workspace_metadata_records m ON d.source='workspace_metadata_records' AND m.workspace_key=d.workspace_key AND m.field=d.field AND m.record_key=d.record_key
		LEFT JOIN metadata_catalog c ON d.source='workspace_metadata_records' AND c.workspace_key=d.workspace_key AND c.field=d.field AND c.record_key=d.record_key
		ORDER BY d.workspace_key,d.field,d.record_key LIMIT 17
	), work AS MATERIALIZED (
		SELECT w.workspace_key,w.workspace_id,
			CASE WHEN (SELECT COUNT(*) FROM events p WHERE p.workspace_id=w.workspace_id)=1
				AND LENGTH(CAST((SELECT event_id FROM events p WHERE p.workspace_id=w.workspace_id LIMIT 1) AS BLOB))<=60
				THEN 'rje:'||(SELECT event_id FROM events p WHERE p.workspace_id=w.workspace_id LIMIT 1) ELSE 'rj'||lower(hex(randomblob(16))) END journal_id,
			m.generation||':'||COALESCE(a.epoch,0) epoch,
			(SELECT json_group_array(json_object('key',json_object('Kind',r.field,'ID',r.record_key),'version',CAST(r.version AS TEXT),'deleted',json(CASE WHEN r.deleted<>0 THEN 'true' ELSE 'false' END))) FROM records r WHERE r.workspace_key=w.workspace_key AND r.field<>'@authz') deltas,
			(SELECT json_group_array(json(r.projection)) FROM records r WHERE r.workspace_key=w.workspace_key AND r.projection IS NOT NULL) catalog,
			(SELECT json_group_array(p.event_id) FROM events p WHERE p.workspace_id=w.workspace_id) events,
			EXISTS(SELECT 1 FROM records r WHERE r.workspace_key=w.workspace_key AND r.field='@authz') authz
		FROM affected w CROSS JOIN record_journal_mode m LEFT JOIN workspace_authz_epochs a ON a.workspace_id=w.workspace_id WHERE m.id=1 AND m.enabled=1
	)
	SELECT ?1,jsonb_object('Created',?2,'CacheRevisions',1,'QueuedRevisions',json(?3),'Workspaces',json_group_array(json_object(
		'Key',workspace_key,'ID',workspace_id,'Catalog',json(catalog),'Events',json(events),
		'Change',json_object('txId',journal_id,'seq','','authzEpoch',epoch,
			'records',json(CASE WHEN LENGTH(CAST(deltas AS BLOB))>64512 THEN '[]' ELSE deltas END),
			'invalidations',json(CASE WHEN LENGTH(CAST(deltas AS BLOB))>64512 OR (deltas='[]' AND authz=0) THEN '["workspace"]' WHEN authz<>0 THEN '["authz"]' ELSE '[]' END)))))
	FROM work
	WHERE (SELECT ok FROM guard)=1
	AND flow_rj_small(COALESCE((SELECT SUM(json_extract(projection,'$.Cost')) FROM records),0)>262144)=1
	AND flow_rj_small(EXISTS(SELECT 1 FROM records WHERE field<>'@authz' AND version IS NULL))=1`

func (s *SQLiteStore) ensureSQLiteRecordJournalCommit(ctx context.Context) error {
	var err error
	if s.db.journalSQLFlush == nil {
		s.db.journalSQLFlush, err = s.db.DB.PrepareContext(ctx, recordJournalSQLCommit)
	}
	if err == nil {
		err = s.ensureSQLiteRecordJournalWrites(ctx)
	}
	return err
}

func (tx *sqlTx) flushSQLRecordJournal() (bool, error) {
	if tx.dialect != "sqlite" || tx.journalRepair || tx.database.journalCommitHook.Load() != nil {
		return false, nil
	}
	if tx.maxTransactionBytes > 0 && tx.maxTransactionBytes-tx.writtenBytes < recordJournalSmallWriteBudget+65536 {
		return false, nil
	}
	ctx := tx.context()
	if tx.maxTransactionBytes > 0 && !tx.journalFinal {
		if _, err := tx.Tx.ExecContext(ctx, `SAVEPOINT flow_record_journal`); err != nil {
			return true, err
		}
		tx.journalBudget = &recordJournalBudgetState{tx.writtenBytes, len(tx.sequencing), len(tx.signals), tx.issueCounts}
	}
	writer := tx.Tx.StmtContext(ctx, tx.database.journalSQLFlush)
	observeSQLStatement(ctx, recordJournalSQLCommit)
	type revision struct {
		Workspace, Kind string
		Flush           bool
	}
	queued := make([]revision, 0, len(tx.cacheRevisions))
	for key := range tx.cacheRevisions {
		queued = append(queued, revision{key.workspace, key.kind, tx.journalFinal})
	}
	if tx.issueCounts != nil {
		for workspace := range tx.issueCounts.revisions {
			queued = append(queued, revision{workspace, cacheKindIssues, false})
		}
	}
	for workspace := range tx.journalCounts {
		queued = append(queued, revision{workspace, cacheKindIssues, false})
	}
	queuedJSON, _ := json.Marshal(queued)
	_, err := writer.ExecContext(ctx, tx.journalToken, time.Now().UTC().Format(time.RFC3339Nano), queuedJSON)
	writer.Close()
	if err != nil && strings.Contains(err.Error(), recordJournalSmallFallback) {
		if tx.journalBudget != nil {
			if _, err := tx.Tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT flow_record_journal`); err != nil {
				return true, err
			}
			if err := tx.finishRecordJournalSavepoint(); err != nil {
				return true, err
			}
		}
		return false, nil
	}
	if err == nil {
		tx.writtenBytes += recordJournalSmallWriteBudget
		if tx.journalFinal {
			tx.cacheRevisions = nil
		}
	}
	return true, err
}
