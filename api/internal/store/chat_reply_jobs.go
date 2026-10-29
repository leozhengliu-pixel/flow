package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"flow/api/internal/domain"
)

var ErrChatJobLeaseLost = errors.New("chat job claim no longer owned")

func (s *SQLiteStore) ChatReplyJobPayload(ctx context.Context, workspace, connection, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM chat_jobs WHERE workspace_key=? AND connection_id=? AND id=? AND kind='dm_reply'`, workspace, connection, id).Scan(&raw)
	return raw, err
}

func (s *SQLiteStore) createChatReplyLeaseSchema(ctx context.Context) error {
	prefix := "CREATE INDEX IF NOT EXISTS "
	if s.dialect == "mysql" {
		prefix = "CREATE INDEX "
	}
	return s.applyMigrationStatements(ctx, []string{
		`CREATE TABLE IF NOT EXISTS chat_reply_leases (thread_key VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,job_id VARCHAR(191) NOT NULL,attempt INTEGER NOT NULL,lease_until BIGINT NOT NULL)`,
		prefix + `chat_reply_leases_job ON chat_reply_leases(workspace_key,job_id)`,
		prefix + `chat_reply_leases_expiry ON chat_reply_leases(lease_until)`,
	})
}

// CheckpointChatReplyJob fences both reply delivery and thread ownership with
// the queue claim. A stale process cannot renew a successor's thread leases.
// The caller serializes checkpoints and delivery for this claim.
func (s *SQLiteStore) CheckpointChatReplyJob(ctx context.Context, job *domain.ChatJob, payload json.RawMessage, threadKey string, now time.Time) (bool, error) {
	if job.Kind != "dm_reply" || !json.Valid(payload) || len(payload) > 1<<20 {
		return false, ErrIssueQuery
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	until := max(now.Add(time.Minute).UnixMilli(), job.LeaseUntil.UnixMilli()+1)
	result, err := tx.ExecContext(ctx, `UPDATE chat_jobs SET payload=?,lease_until=? WHERE id=? AND workspace_key=? AND connection_id=? AND status='processing' AND attempts=? AND lease_until=? AND lease_until>?`, []byte(payload), until, job.ID, job.WorkspaceKey, job.ConnectionID, job.Attempts, job.LeaseUntil.UnixMilli(), now.UnixMilli())
	if err != nil {
		return false, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return false, ErrChatJobLeaseLost
	}
	if threadKey != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO chat_reply_leases(thread_key,workspace_key,job_id,attempt,lease_until) VALUES(?,?,?,?,0) ON CONFLICT DO NOTHING`, threadKey, job.WorkspaceKey, job.ID, job.Attempts); err != nil {
			return false, err
		}
		// An expired reply is reconciled by its queue successor before a
		// different message can answer. Its worker runtime may still be alive.
		result, err = tx.ExecContext(ctx, `UPDATE chat_reply_leases SET job_id=?,attempt=?,lease_until=? WHERE thread_key=? AND workspace_key=? AND (job_id=? OR (lease_until<=? AND NOT EXISTS(SELECT 1 FROM chat_jobs WHERE id=chat_reply_leases.job_id AND status IN ('pending','processing'))))`, job.ID, job.Attempts, until, threadKey, job.WorkspaceKey, job.ID, now.UnixMilli())
		if err != nil {
			return false, err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return false, nil
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE chat_reply_leases SET lease_until=?,attempt=? WHERE job_id=? AND workspace_key=?`, until, job.Attempts, job.ID, job.WorkspaceKey); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	job.LeaseUntil, job.Payload = time.UnixMilli(until), payload
	return true, nil
}

func (s *SQLiteStore) ReleaseChatReplyLeases(ctx context.Context, job domain.ChatJob) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_reply_leases WHERE workspace_key=? AND job_id=? AND attempt=?`, job.WorkspaceKey, job.ID, job.Attempts)
	return err
}
