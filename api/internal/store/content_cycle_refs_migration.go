package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// contentCycleRefsBatch bounds one backfill transaction.
const contentCycleRefsBatch = 500

// contentActivityCyclesBackgroundThreshold is the activity count above which
// the backfill runs after startup. A large production workspace can take over
// a minute; the cycle graph keeps using the JSON scan until its workspace's
// marker says the backfill is complete.
const contentActivityCyclesBackgroundThreshold = 100000

// ensureContentActivityCycles creates the marker rows synchronously, then
// either backfills inline (small or self-contained databases, tests) or in
// the background (a large external database).
func (s *SQLiteStore) ensureContentActivityCycles(ctx context.Context) error {
	if err := s.prepareContentActivityCycles(ctx); err != nil {
		return err
	}
	if s.dialect != "sqlite" {
		var activities int64
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_content_records WHERE kind='activity'`).Scan(&activities); err != nil {
			return err
		}
		if activities > contentActivityCyclesBackgroundThreshold {
			go s.runContentActivityCycleBackfill()
			return nil
		}
	}
	return s.backfillContentActivityCycles(ctx)
}

// prepareContentActivityCycles creates the table and one incomplete marker per
// workspace; it is cheap enough to run in the startup path.
func (s *SQLiteStore) prepareContentActivityCycles(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS issue_activity_cycles_migrations(workspace_key VARCHAR(191) PRIMARY KEY,last_resource VARCHAR(191) NOT NULL,last_id VARCHAR(191) NOT NULL,complete INTEGER NOT NULL)`); err != nil {
		return err
	}
	for _, workspace := range s.WorkspaceKeys() {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_activity_cycles_migrations(workspace_key,last_resource,last_id,complete) VALUES(?,'','',0) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
	}
	return nil
}

// migrateContentActivityCycles is the synchronous form used by tests and by
// small databases: prepare, then backfill.
func (s *SQLiteStore) migrateContentActivityCycles(ctx context.Context) error {
	if err := s.prepareContentActivityCycles(ctx); err != nil {
		return err
	}
	return s.backfillContentActivityCycles(ctx)
}

func (s *SQLiteStore) runContentActivityCycleBackfill() {
	ctx := s.WorkerContext()
	began := time.Now()
	if err := s.backfillContentActivityCycles(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Warn("activity cycle backfill stopped", "error", err)
		}
		return
	}
	slog.Info("activity cycle backfill finished", "ms", time.Since(began).Milliseconds())
}

// backfillContentActivityCycles indexes the cycle references of activities
// written before issue_activity_cycles existed. New writes maintain the table
// in syncActivityCycleRefs; this one pass fills it for existing workspaces.
func (s *SQLiteStore) backfillContentActivityCycles(ctx context.Context) error {
	for _, workspace := range s.WorkspaceKeys() {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			var lastID string
			var complete int
			if err := s.db.QueryRowContext(ctx, `SELECT last_id,complete FROM issue_activity_cycles_migrations WHERE workspace_key=?`, workspace).Scan(&lastID, &complete); err != nil {
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
				// content_identity_idx (workspace_key,kind,id) makes this a
				// short keyset scan; without the hint MySQL scans the
				// primary key and sorts the whole workspace per batch.
				from := "workspace_content_records"
				if s.dialect == "mysql" {
					from = "workspace_content_records FORCE INDEX (content_identity_idx)"
				}
				rows, err := tx.QueryContext(ctx, `SELECT resource_id,id,data FROM `+from+` WHERE workspace_key=? AND kind='activity' AND id>? ORDER BY id LIMIT ?`, workspace, lastID, contentCycleRefsBatch)
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
					lastID = id
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
				_, err = tx.ExecContext(ctx, `UPDATE issue_activity_cycles_migrations SET last_resource='',last_id=?,complete=? WHERE workspace_key=?`, lastID, complete, workspace)
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
