package store

import (
	"context"
	"encoding/json"
)

func (s *SQLiteStore) migrateContentIndexes(ctx context.Context) error {
	type record struct {
		workspace, kind, resource string
		raw                       json.RawMessage
	}
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,kind,resource_id,data FROM workspace_content_records WHERE record_version=0 LIMIT 250`)
		if err != nil {
			return err
		}
		batch := []record{}
		for rows.Next() {
			var item record
			if err := rows.Scan(&item.workspace, &item.kind, &item.resource, &item.raw); err != nil {
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
		for _, item := range batch {
			if err = writeContentRecord(ctx, tx, item.workspace, item.kind, item.resource, item.raw); err != nil {
				break
			}
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
