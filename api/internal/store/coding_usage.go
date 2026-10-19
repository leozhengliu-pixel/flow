package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"flow/api/internal/domain"
)

// Coding usage metering (track B, design §20): one coding_usage_records row
// per LLM request (track E) or runtime increment (sandbox reconciler). The
// session's cost_micros is the running sum, updated in the same transaction.
// Spend windows are summed by (workspace_key, user_id | loop_id, created_at).

// RecordCodingUsage stores a usage record; recording the same id twice is a no-op.
func (s *SQLiteStore) RecordCodingUsage(ctx context.Context, record domain.CodingUsageRecord) error {
	if record.ID == "" || record.WorkspaceKey == "" || (record.Kind != "llm" && record.Kind != "runtime") {
		return ErrCodingInput
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO coding_usage_records(id,workspace_key,session_id,sandbox_id,user_id,loop_id,kind,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,runtime_seconds,size,cost_micros,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		record.ID, record.WorkspaceKey, record.SessionID, record.SandboxID, record.UserID, record.LoopID, record.Kind, record.Model, record.InputTokens, record.OutputTokens, record.CacheReadTokens, record.CacheWriteTokens, record.RuntimeSeconds, record.Size, record.CostMicros, millis(record.CreatedAt))
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 1 && record.SessionID != "" && record.CostMicros != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE coding_sessions SET cost_micros=cost_micros+? WHERE workspace_key=? AND id=?`, record.CostMicros, record.WorkspaceKey, record.SessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CodingSpendMicros sums cost since a time, optionally for one user or loop.
func (s *SQLiteStore) CodingSpendMicros(ctx context.Context, workspaceKey, userID, loopID string, since time.Time) (int64, error) {
	where, args := "workspace_key=? AND created_at>=?", []any{workspaceKey, millis(since)}
	if userID != "" {
		where += " AND user_id=?"
		args = append(args, userID)
	}
	if loopID != "" {
		where += " AND loop_id=?"
		args = append(args, loopID)
	}
	var total sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT SUM(cost_micros) FROM coding_usage_records WHERE `+where, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// CodingUsage lists a workspace's usage records since a time, oldest first.
func (s *SQLiteStore) CodingUsage(ctx context.Context, workspaceKey string, since time.Time) ([]domain.CodingUsageRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,sandbox_id,user_id,loop_id,kind,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,runtime_seconds,size,cost_micros,created_at FROM coding_usage_records WHERE workspace_key=? AND created_at>=? ORDER BY created_at,id LIMIT 50000`, workspaceKey, millis(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CodingUsageRecord{}
	for rows.Next() {
		record := domain.CodingUsageRecord{WorkspaceKey: workspaceKey}
		var created int64
		if err := rows.Scan(&record.ID, &record.SessionID, &record.SandboxID, &record.UserID, &record.LoopID, &record.Kind, &record.Model, &record.InputTokens, &record.OutputTokens, &record.CacheReadTokens, &record.CacheWriteTokens, &record.RuntimeSeconds, &record.Size, &record.CostMicros, &created); err != nil {
			return nil, err
		}
		record.CreatedAt = codingTime(created)
		result = append(result, record)
	}
	return result, rows.Err()
}

// CodingSandboxBilledSeconds is how much of a sandbox's runtime was billed.
func (s *SQLiteStore) CodingSandboxBilledSeconds(ctx context.Context, sandboxID string) (int64, error) {
	var billed int64
	err := s.db.QueryRowContext(ctx, `SELECT billed_seconds FROM coding_sandboxes WHERE id=?`, sandboxID).Scan(&billed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrCodingNotFound
	}
	return billed, err
}

// AdvanceCodingSandboxBilled moves billed_seconds from → to (CAS), so each
// runtime second is billed once even with concurrent billers.
func (s *SQLiteStore) AdvanceCodingSandboxBilled(ctx context.Context, sandboxID string, from, to int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE coding_sandboxes SET billed_seconds=? WHERE id=? AND billed_seconds=?`, to, sandboxID, from)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
