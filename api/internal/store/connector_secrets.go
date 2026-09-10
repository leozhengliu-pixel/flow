package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
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

func connectorCipher() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("FLOW_CONNECTOR_SECRET_KEY"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("FLOW_CONNECTOR_SECRET_KEY must be a base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func ConnectorSecretsConfigured() bool { _, err := connectorCipher(); return err == nil }

func (s *SQLiteStore) PutConnectorSecret(ctx context.Context, id string, data []byte, expires time.Time) error {
	if len(id) > 191 || len(data) > 1<<20 {
		return errors.New("connector secret exceeds limit")
	}
	aead, err := connectorCipher()
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	encrypted := aead.Seal(nonce, nonce, data, []byte(id))
	_, err = s.db.ExecContext(ctx, `INSERT INTO connector_secrets(id,ciphertext,expires_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET ciphertext=excluded.ciphertext,expires_at=excluded.expires_at`, id, encrypted, expires.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ReadConnectorSecret(ctx context.Context, id string, consume bool) ([]byte, error) {
	aead, err := connectorCipher()
	if err != nil {
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
	if len(encrypted) < aead.NonceSize() {
		return nil, errors.New("invalid connector ciphertext")
	}
	plain, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], []byte(id))
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

func (s *SQLiteStore) DeleteConnectorSecret(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=?`, id)
	return err
}
