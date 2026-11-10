package store

import (
	"context"
	"database/sql"
	"errors"
)

// Audit stream signing secrets are needed in plain form to sign deliveries, so
// they live in their own table instead of the workspace document, sealed
// under FLOW_CONNECTOR_SECRET_KEY like webhook signing secrets.
func (s *SQLiteStore) ensureAuditStreamSecrets(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS audit_stream_secrets (webhook_id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,secret TEXT NOT NULL)`)
	return err
}

// auditStreamSecretAAD binds a sealed audit stream secret to its stream.
func auditStreamSecretAAD(webhookID string) string { return "audit-stream:" + webhookID }

// SetAuditStreamSecret seals the secret. Without FLOW_CONNECTOR_SECRET_KEY it
// returns ErrSecretKeyMissing and stores nothing.
func (s *SQLiteStore) SetAuditStreamSecret(ctx context.Context, workspace, webhookID, secret string) error {
	sealed, err := sealSecretText(secret, auditStreamSecretAAD(webhookID))
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audit_stream_secrets WHERE webhook_id=?`, webhookID); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_stream_secrets (webhook_id,workspace_key,secret) VALUES (?,?,?)`, webhookID, workspace, sealed)
	return err
}

// AuditStreamSecret returns the stream's secret, or "" when none is stored. A
// secret an older release stored in plain text keeps signing until
// ResealSecrets seals it.
func (s *SQLiteStore) AuditStreamSecret(ctx context.Context, webhookID string) (string, error) {
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT secret FROM audit_stream_secrets WHERE webhook_id=?`, webhookID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	secret, _, err := openSecretText(stored, auditStreamSecretAAD(webhookID))
	return secret, err
}

func (s *SQLiteStore) DeleteAuditStreamSecret(ctx context.Context, webhookID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM audit_stream_secrets WHERE webhook_id=?`, webhookID)
	return err
}
