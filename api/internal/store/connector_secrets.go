package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *SQLiteStore) createConnectorSecretsSchema(ctx context.Context) error {
	id, blob := "TEXT", "BLOB"
	if s.dialect == "mysql" {
		id, blob = "VARCHAR(191)", "LONGBLOB"
	}
	if s.dialect == "postgres" {
		blob = "BYTEA"
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS connector_secrets (id %s PRIMARY KEY, ciphertext %s NOT NULL, expires_at VARCHAR(40) NOT NULL)`, id, blob))
	return err
}

func ConnectorSecretsConfigured() bool { _, err := connectorCipher(); return err == nil }

// ConnectorSecretID is the storage key of a credential owned by owner (a
// member, or a fixed role such as "provider") in a workspace. Public
// identifiers are hashed into a stable key; it is not a password verifier.
func ConnectorSecretID(workspace, owner, id string) string {
	return fmt.Sprintf("credential_%x", sha256.Sum256([]byte(workspace+"\x00"+owner+"\x00"+id)))
}

func (s *SQLiteStore) PutConnectorSecret(ctx context.Context, id string, data []byte, expires time.Time) error {
	if len(id) > 191 || len(data) > 1<<20 {
		return errors.New("connector secret exceeds limit")
	}
	encrypted, err := sealSecret(data, []byte(id))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO connector_secrets(id,ciphertext,expires_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET ciphertext=excluded.ciphertext,expires_at=excluded.expires_at`, id, encrypted, expires.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ReadConnectorSecret(ctx context.Context, id string, consume bool) ([]byte, error) {
	if _, err := connectorCipher(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	lock := ""
	if consume && s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var encrypted []byte
	var expiry string
	if err = tx.QueryRowContext(ctx, `SELECT ciphertext,expires_at FROM connector_secrets WHERE id=?`+lock, id).Scan(&encrypted, &expiry); err != nil {
		return nil, err
	}
	end, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !end.After(time.Now()) {
		return nil, sql.ErrNoRows
	}
	plain, _, err := openSecret(encrypted, []byte(id))
	if err != nil {
		return nil, errors.New("connector credential decryption failed")
	}
	if consume {
		result, err := tx.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=? AND ciphertext=?`, id, encrypted)
		if err != nil {
			return nil, err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return nil, sql.ErrNoRows
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return plain, nil
}

// connectorSecretSwapAttempts bounds UpdateConnectorSecret's retries when the
// row changes between its read and its write.
const connectorSecretSwapAttempts = 5

// UpdateConnectorSecret replaces a stored secret with what update returns for
// its current value. The write is a compare-and-swap on the ciphertext that
// was read, so two updates never interleave (for example two replicas
// rotating one refresh token); after a concurrent change update runs again on
// the new value. An error from update leaves the secret unchanged. A missing
// or expired secret is sql.ErrNoRows.
func (s *SQLiteStore) UpdateConnectorSecret(ctx context.Context, id string, expires time.Time, update func(current []byte) ([]byte, error)) error {
	if _, err := connectorCipher(); err != nil {
		return err
	}
	for range connectorSecretSwapAttempts {
		var encrypted []byte
		var expiry string
		if err := s.db.QueryRowContext(ctx, `SELECT ciphertext,expires_at FROM connector_secrets WHERE id=?`, id).Scan(&encrypted, &expiry); err != nil {
			return err
		}
		if end, err := time.Parse(time.RFC3339Nano, expiry); err != nil || !end.After(time.Now()) {
			return sql.ErrNoRows
		}
		current, _, err := openSecret(encrypted, []byte(id))
		if err != nil {
			return errors.New("connector credential decryption failed")
		}
		next, err := update(current)
		if err != nil {
			return err
		}
		if len(next) > 1<<20 {
			return errors.New("connector secret exceeds limit")
		}
		sealed, err := sealSecret(next, []byte(id))
		if err != nil {
			return err
		}
		result, err := s.db.ExecContext(ctx, `UPDATE connector_secrets SET ciphertext=?,expires_at=? WHERE id=? AND ciphertext=?`, sealed, expires.UTC().Format(time.RFC3339Nano), id, encrypted)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count == 1 {
			return err
		}
	}
	return errors.New("connector secret changed concurrently")
}

func (s *SQLiteStore) DeleteConnectorSecret(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=?`, id)
	return err
}
