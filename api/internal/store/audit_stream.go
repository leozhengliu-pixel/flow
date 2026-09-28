package store

import (
	"context"
	"database/sql"
	"errors"
)

// Audit stream signing secrets are needed in plain form to sign deliveries, so
// they live in their own table instead of the workspace document.
func (s *SQLiteStore) ensureAuditStreamSecrets(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS audit_stream_secrets (webhook_id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,secret TEXT NOT NULL)`)
	return err
}

func (s *SQLiteStore) SetAuditStreamSecret(ctx context.Context, workspace, webhookID, secret string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audit_stream_secrets WHERE webhook_id=?`, webhookID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_stream_secrets (webhook_id,workspace_key,secret) VALUES (?,?,?)`, webhookID, workspace, secret)
	return err
}

func (s *SQLiteStore) AuditStreamSecret(ctx context.Context, webhookID string) (string, error) {
	var secret string
	err := s.db.QueryRowContext(ctx, `SELECT secret FROM audit_stream_secrets WHERE webhook_id=?`, webhookID).Scan(&secret)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return secret, err
}

func (s *SQLiteStore) DeleteAuditStreamSecret(ctx context.Context, webhookID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM audit_stream_secrets WHERE webhook_id=?`, webhookID)
	return err
}
