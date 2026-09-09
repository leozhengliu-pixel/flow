package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"flow/api/internal/domain"
)

func attachmentKey(url string) string {
	hash := sha256.Sum256([]byte(url))
	return hex.EncodeToString(hash[:])
}

func (s *SQLiteStore) ensureAttachmentIndex(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS issue_attachment_records(workspace_key VARCHAR(191) NOT NULL,issue_id VARCHAR(191) NOT NULL,url_hash VARCHAR(64) NOT NULL,PRIMARY KEY(workspace_key,issue_id,url_hash))`,
		`CREATE TABLE IF NOT EXISTS issue_attachment_migrations(workspace_key VARCHAR(191) PRIMARY KEY,last_id VARCHAR(191) NOT NULL,complete INTEGER NOT NULL)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	statement := "CREATE INDEX IF NOT EXISTS issue_attachment_url_idx ON issue_attachment_records(workspace_key,url_hash,issue_id)"
	if s.dialect == "mysql" {
		statement = "CREATE INDEX issue_attachment_url_idx ON issue_attachment_records(workspace_key,url_hash,issue_id)"
	}
	if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
		return err
	}
	return nil
}

func writeAttachmentIndex(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue) error {
	ids := []string{}
	rows := [][]string{}
	for _, issue := range issues {
		ids = append(ids, issue.ID)
		for _, attachment := range issue.Attachments {
			rows = append(rows, []string{issue.ID, attachmentKey(attachment.URL)})
		}
	}
	return syncIssueIndexRows(ctx, tx, workspace, "issue_attachment_records", []string{"issue_id", "url_hash"}, 2, ids, rows)
}

func (s *SQLiteStore) migrateAttachmentIndex(ctx context.Context) error {
	for workspace := range s.workspaces {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_attachment_migrations(workspace_key,last_id,complete) VALUES(?,'',0) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
		for {
			var last string
			var done int
			if err := s.db.QueryRowContext(ctx, `SELECT last_id,complete FROM issue_attachment_migrations WHERE workspace_key=?`, workspace).Scan(&last, &done); err != nil {
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
				rows, err := tx.QueryContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id>? ORDER BY id LIMIT 64`, workspace, last)
				if err != nil {
					return err
				}
				batch := []domain.Issue{}
				for rows.Next() {
					var raw []byte
					var issue domain.Issue
					if err := rows.Scan(&raw); err != nil {
						rows.Close()
						return err
					}
					if err := json.Unmarshal(raw, &issue); err != nil {
						rows.Close()
						return err
					}
					batch = append(batch, issue)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if err := writeAttachmentIndex(ctx, tx, workspace, batch); err != nil {
					return err
				}
				if len(batch) > 0 {
					last = batch[len(batch)-1].ID
				}
				if len(batch) < 64 {
					done = 1
				}
				_, err = tx.ExecContext(ctx, `UPDATE issue_attachment_migrations SET last_id=?,complete=? WHERE workspace_key=?`, last, done, workspace)
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

func (s *SQLiteStore) IssueAttachmentVisible(ctx context.Context, q IssueRecordQuery, url string) (bool, error) {
	q.Filter = IssueFilter{}
	q.Archived = "all"
	where, args, err := issueRecordWhere(q)
	if err != nil {
		return false, err
	}
	prefix, prefixArgs := issueAccessCTE(q)
	var count int
	err = s.db.QueryRowContext(ctx, prefix+`SELECT COUNT(*) FROM (SELECT i.id FROM issue_attachment_records a JOIN issue_records i ON i.workspace_key=a.workspace_key AND i.id=a.issue_id WHERE a.url_hash=? AND `+where+` LIMIT 1) visible_attachment`, append(append(prefixArgs, attachmentKey(url)), args...)...).Scan(&count)
	return count > 0, err
}
