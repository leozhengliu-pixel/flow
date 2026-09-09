package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *SQLiteStore) createSessionSecuritySchema(ctx context.Context) error {
	idType := "TEXT"
	if s.dialect == "mysql" {
		idType = "VARCHAR(191)"
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS auth_session_security (
		token_hash %s PRIMARY KEY, provider VARCHAR(191) NOT NULL, issuer VARCHAR(2048) NOT NULL,
		mfa_verified_at VARCHAR(40), FOREIGN KEY(token_hash) REFERENCES auth_sessions(token_hash) ON DELETE CASCADE
	)`, idType))
	return err
}

type SessionAuthentication struct {
	Provider      string
	Issuer        string
	MFAVerifiedAt *time.Time
}

func (s *SQLiteStore) SetSessionAuthentication(ctx context.Context, token, provider, issuer string, mfa bool) error {
	var verified any
	if mfa {
		verified = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_session_security(token_hash,provider,issuer,mfa_verified_at) VALUES(?,?,?,?) ON CONFLICT(token_hash) DO UPDATE SET provider=excluded.provider,issuer=excluded.issuer,mfa_verified_at=excluded.mfa_verified_at`, tokenHash(token), provider, issuer, verified)
	return err
}

func (s *SQLiteStore) SessionAuthentication(ctx context.Context, token string) (SessionAuthentication, error) {
	result := SessionAuthentication{Provider: "legacy"}
	var date sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT provider,issuer,mfa_verified_at FROM auth_session_security WHERE token_hash=?`, tokenHash(token)).Scan(&result.Provider, &result.Issuer, &date)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if date.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, date.String); err == nil {
			result.MFAVerifiedAt = &parsed
		}
	}
	return result, nil
}
