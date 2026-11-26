package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
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
		verified = formatStoredTime(time.Now())
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

// SessionCreatedAt is when the browser session was issued by a sign-in.
func (s *SQLiteStore) SessionCreatedAt(ctx context.Context, token string) (time.Time, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM auth_sessions WHERE token_hash=?`, tokenHash(token)).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, raw)
}

// BackdateSessionForTesting makes a session, and the second-factor
// verification it carries, older by age.
func (s *SQLiteStore) BackdateSessionForTesting(ctx context.Context, token string, age time.Duration) error {
	if !testing.Testing() {
		return sql.ErrConnDone
	}
	hash := tokenHash(token)
	var created string
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM auth_sessions WHERE token_hash=?`, hash).Scan(&created); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return err
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE auth_sessions SET created_at=? WHERE token_hash=?`, formatStoredTime(parsed.Add(-age)), hash); err != nil {
		return err
	}
	var verified sql.NullString
	if err = s.db.QueryRowContext(ctx, `SELECT mfa_verified_at FROM auth_session_security WHERE token_hash=?`, hash).Scan(&verified); err == sql.ErrNoRows || !verified.Valid {
		return nil
	} else if err != nil {
		return err
	}
	parsed, err = time.Parse(time.RFC3339Nano, verified.String)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE auth_session_security SET mfa_verified_at=? WHERE token_hash=?`, formatStoredTime(parsed.Add(-age)), hash)
	return err
}
