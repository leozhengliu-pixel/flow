package store

import (
	"context"
	"encoding/json"
)

func (s *SQLiteStore) migrateContentIndexes(ctx context.Context) error {
	type record struct {
		workspace, kind, resource, id string
		raw                           json.RawMessage
	}
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,kind,resource_id,id,data FROM workspace_content_records WHERE record_version=0 LIMIT 1000`)
		if err != nil {
			return err
		}
		batch := []record{}
		for rows.Next() {
			var item record
			if err := rows.Scan(&item.workspace, &item.kind, &item.resource, &item.id, &item.raw); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(batch) == 0 {
			return nil
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		// Unversioned rows always need rewriting: build them in memory and
		// upsert them with multi-row statements (a round trip per record made
		// this migration take minutes at ~100k records on a networked database).
		records := make([]contentRecordRow, 0, len(batch))
		for _, item := range batch {
			var raw []byte
			if raw, err = json.Marshal(item.raw); err != nil {
				break
			}
			var row contentRecordRow
			if row, err = buildContentRecordRow(item.workspace, item.kind, item.resource, raw); err != nil {
				break
			}
			// Rewrite the selected row under its stored key. The id inside the
			// JSON can be missing or differ (legacy and hand-written rows); keyed
			// on it, the upsert added another row and left this one at version 0,
			// so the next query returned it again and startup never finished.
			row.id = item.id
			records = append(records, row)
		}
		if err == nil {
			err = writeContentRecordRows(ctx, tx, records)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
}
