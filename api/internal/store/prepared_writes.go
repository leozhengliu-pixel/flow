package store

import (
	"context"
	"database/sql"
)

// Reuse hot statements so SQLite compiles the installed capture triggers
// once per connection. The materializing writer also sets collection_order.
var recordJournalPreparedWrites = [...]string{
	`INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`,
	`INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at,workspace_id,seq,actor_id) VALUES(?,?,?,?,?,?,?,?,?)`,
	`INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`,
	`DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`,
}

func (s *SQLiteStore) ensureSQLiteRecordJournalWrites(ctx context.Context) error {
	if s.db.journalWrites != nil {
		return nil
	}
	queries := append(recordJournalPreparedWrites[:], `UPDATE record_journal_context SET tx_id=? WHERE id=1`, recordJournalAccountWrite)
	statements := make(map[string]*sql.Stmt, len(queries))
	for _, query := range queries {
		statement, err := s.db.DB.PrepareContext(ctx, query)
		if err != nil {
			for _, opened := range statements {
				_ = opened.Close()
			}
			return err
		}
		statements[query] = statement
	}
	s.db.journalWrites = statements
	return nil
}
