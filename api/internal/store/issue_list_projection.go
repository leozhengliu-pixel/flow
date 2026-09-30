package store

import (
	"context"
	"encoding/json"
	"flow/api/internal/domain"
	"strings"
)

func issueListProjection(issue domain.Issue) domain.Issue {
	issue.Description = ""
	issue.DescriptionState = ""
	issue.DocumentContent = nil
	issue.Reactions = map[string][]string{}
	issue.SubscriberIDs = []string{}
	issue.IsSummary = true
	return issue
}

func (s *SQLiteStore) ensureIssueListProjection(ctx context.Context) error {
	blob := "BLOB"
	if s.dialect == "mysql" {
		blob = "LONGBLOB"
	}
	if s.dialect == "postgres" {
		blob = "BYTEA"
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE issue_records ADD COLUMN list_data "+blob); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return err
	}
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS issue_list_migrations(workspace_key VARCHAR(191) PRIMARY KEY,last_id VARCHAR(191) NOT NULL,complete INTEGER NOT NULL)`)
	return err
}

func (s *SQLiteStore) migrateIssueListProjection(ctx context.Context) error {
	for workspace := range s.workspaces {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_list_migrations(workspace_key,last_id,complete) VALUES(?,'',0) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
		for {
			var last string
			var done int
			if err := s.db.QueryRowContext(ctx, `SELECT last_id,complete FROM issue_list_migrations WHERE workspace_key=?`, workspace).Scan(&last, &done); err != nil {
				return err
			}
			if done != 0 {
				break
			}
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			err = func() error {
				rows, err := tx.QueryContext(ctx, `SELECT id,data,list_data FROM issue_records WHERE workspace_key=? AND id>? ORDER BY id LIMIT 256`, workspace, last)
				if err != nil {
					return err
				}
				type row struct {
					id        string
					raw, list []byte
				}
				batch := []row{}
				for rows.Next() {
					var item row
					if err := rows.Scan(&item.id, &item.raw, &item.list); err != nil {
						rows.Close()
						return err
					}
					batch = append(batch, item)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				// One CASE update per batch instead of a round trip per record.
				ids := []string{}
				cases := []string{}
				args := []any{}
				for _, item := range batch {
					last = item.id
					if len(item.list) > 0 {
						continue
					}
					var issue domain.Issue
					if err := json.Unmarshal(item.raw, &issue); err != nil {
						return err
					}
					raw, err := json.Marshal(issueListProjection(issue))
					if err != nil {
						return err
					}
					ids = append(ids, item.id)
					if s.dialect == "postgres" {
						cases = append(cases, "WHEN ? THEN CAST(? AS BYTEA)")
					} else {
						cases = append(cases, "WHEN ? THEN ?")
					}
					args = append(args, item.id, raw)
				}
				if len(ids) > 0 {
					clause, idArgs := bindList("id", ids)
					args = append(append(args, workspace), idArgs...)
					if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET list_data=CASE id `+strings.Join(cases, " ")+` END WHERE workspace_key=? AND `+clause, args...); err != nil {
						return err
					}
				}
				if len(batch) < 256 {
					done = 1
				}
				_, err = tx.ExecContext(ctx, `UPDATE issue_list_migrations SET last_id=?,complete=? WHERE workspace_key=?`, last, done, workspace)
				return err
			}()
			if err != nil {
				tx.Rollback()
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
	}
	return nil
}
