package store

import (
	"context"
	"fmt"
)

// contentCycleRefsBatch bounds one backfill transaction.
const contentCycleRefsBatch = 500

// migrateContentActivityCycles indexes the cycle references of activities
// written before issue_activity_cycles existed. New writes maintain the table
// in syncActivityCycleRefs; this one pass fills it for existing workspaces.
func (s *SQLiteStore) migrateContentActivityCycles(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS issue_activity_cycles_migrations(workspace_key VARCHAR(191) PRIMARY KEY,last_resource VARCHAR(191) NOT NULL,last_id VARCHAR(191) NOT NULL,complete INTEGER NOT NULL)`); err != nil {
		return err
	}
	for workspace := range s.workspaces {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_activity_cycles_migrations(workspace_key,last_resource,last_id,complete) VALUES(?,'','',0) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
		for {
			var lastResource, lastID string
			var complete int
			if err := s.db.QueryRowContext(ctx, `SELECT last_resource,last_id,complete FROM issue_activity_cycles_migrations WHERE workspace_key=?`, workspace).Scan(&lastResource, &lastID, &complete); err != nil {
				return err
			}
			if complete != 0 {
				break
			}
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			err = func() error {
				rows, err := tx.QueryContext(ctx, `SELECT resource_id,id,data FROM workspace_content_records WHERE workspace_key=? AND kind='activity' AND (resource_id>? OR (resource_id=? AND id>?)) ORDER BY resource_id,id LIMIT ?`, workspace, lastResource, lastResource, lastID, contentCycleRefsBatch)
				if err != nil {
					return err
				}
				batch := []contentRecordRow{}
				count := 0
				for rows.Next() {
					var resource, id string
					var raw []byte
					if err := rows.Scan(&resource, &id, &raw); err != nil {
						rows.Close()
						return err
					}
					row, err := buildContentRecordRow(workspace, "activity", resource, raw)
					if err != nil {
						rows.Close()
						return err
					}
					batch = append(batch, row)
					lastResource, lastID = resource, id
					count++
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if err := syncActivityCycleRefRows(ctx, tx, batch); err != nil {
					return err
				}
				if count < contentCycleRefsBatch {
					complete = 1
				}
				_, err = tx.ExecContext(ctx, `UPDATE issue_activity_cycles_migrations SET last_resource=?,last_id=?,complete=? WHERE workspace_key=?`, lastResource, lastID, complete, workspace)
				return err
			}()
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("index activity cycle references: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
	}
	return nil
}
