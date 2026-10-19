package store

// Users' commit-signing private keys for coding sessions (track E,
// docs/coding-sessions-design.md §6 coding_signing_keys, §19). The key is
// encrypted with AES-256-GCM under FLOW_CODING_SECRET_KEY (falling back to
// FLOW_CONNECTOR_SECRET_KEY, like the rest of the coding secrets); the
// additional data binds the ciphertext to its user and format, so a row
// copied to another user does not decrypt. Only the API process decrypts,
// to sign through POST /internal/coding-runtime/sign.

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
	"strings"
	"time"
)

// Signing key formats (mirrors coding.SigningFormatSSH / SigningFormatOpenPGP;
// store cannot import coding, which depends on config and so on store).
const (
	signingFormatSSH     = "ssh"
	signingFormatOpenPGP = "openpgp"
)

// SigningKeyError is returned by SigningKeyMaterial. The coding package
// recognises it by behaviour (NotFound / SigningKeyUnreadable), because store
// cannot import coding's sentinel errors.
type SigningKeyError struct {
	notFound bool
	cause    error
}

func (e *SigningKeyError) Error() string {
	if e.notFound {
		return "signing key: not found"
	}
	return "signing key: stored key cannot be decrypted"
}

// NotFound reports that the user has no stored key.
func (e *SigningKeyError) NotFound() bool { return e.notFound }

// SigningKeyUnreadable reports that the stored key cannot be decrypted (the
// secret key changed or the row is corrupt); the user must upload it again.
func (e *SigningKeyError) SigningKeyUnreadable() bool { return !e.notFound }

// Unwrap exposes the cause.
func (e *SigningKeyError) Unwrap() error { return e.cause }

// ErrSigningKeyNotFound is returned when the user has no stored key.
var ErrSigningKeyNotFound error = &SigningKeyError{notFound: true, cause: sql.ErrNoRows}

func unreadableSigningKey(cause error) error { return &SigningKeyError{cause: cause} }

// signingKeyEnvelopeV1 prefixes every ciphertext so the format can evolve
// (key rotation, another AEAD) without guessing.
const signingKeyEnvelopeV1 byte = 1

// maxSigningKeyMaterial matches the account API's upload limit.
const maxSigningKeyMaterial = 128 * 1024

// readSecretEnv reads NAME_FILE (trimmed file content) or NAME, like config.secret.
func readSecretEnv(name string) string {
	if path := strings.TrimSpace(os.Getenv(name + "_FILE")); path != "" {
		if content, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(content))
		}
	}
	return strings.TrimSpace(os.Getenv(name))
}

// signingKeyAEAD is AES-256-GCM under the coding secret key.
func signingKeyAEAD() (cipher.AEAD, error) {
	raw := readSecretEnv("FLOW_CODING_SECRET_KEY")
	if raw == "" {
		raw = readSecretEnv("FLOW_CONNECTOR_SECRET_KEY")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errors.New("FLOW_CODING_SECRET_KEY (or FLOW_CONNECTOR_SECRET_KEY) must be a base64-encoded 32-byte key to store commit signing keys")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SigningKeyStorageConfigured reports whether signing keys can be stored.
func SigningKeyStorageConfigured() bool {
	_, err := signingKeyAEAD()
	return err == nil
}

func signingKeyAAD(userID, format string) []byte {
	return []byte("coding_signing_keys\x00" + userID + "\x00" + format)
}

func validSigningFormat(format string) bool {
	return format == signingFormatSSH || format == signingFormatOpenPGP
}

// PutSigningKeyMaterial encrypts and stores the user's private key, replacing
// any previous one.
func (s *SQLiteStore) PutSigningKeyMaterial(ctx context.Context, userID string, format string, privateKey []byte) error {
	return s.PutCodingSigningKey(ctx, userID, format, "", privateKey)
}

// PutCodingSigningKey is PutSigningKeyMaterial plus the key's display
// fingerprint, kept in clear so support can match a row to account metadata
// without decrypting.
func (s *SQLiteStore) PutCodingSigningKey(ctx context.Context, userID, format, fingerprint string, privateKey []byte) error {
	if strings.TrimSpace(userID) == "" || len(userID) > 191 {
		return errors.New("signing key: invalid user id")
	}
	if !validSigningFormat(format) {
		return fmt.Errorf("signing key: unknown format %q", format)
	}
	if len(privateKey) == 0 || len(privateKey) > maxSigningKeyMaterial {
		return errors.New("signing key: key material is empty or too large")
	}
	if len(fingerprint) > 255 {
		fingerprint = fingerprint[:255]
	}
	aead, err := signingKeyAEAD()
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	envelope := append([]byte{signingKeyEnvelopeV1}, nonce...)
	envelope = aead.Seal(envelope, nonce, privateKey, signingKeyAAD(userID, format))
	now := millis(time.Now())
	_, err = s.db.ExecContext(ctx, `INSERT INTO coding_signing_keys(user_id,format,ciphertext,fingerprint,error_text,created_at,updated_at) VALUES(?,?,?,?,NULL,?,?) ON CONFLICT(user_id) DO UPDATE SET format=excluded.format,ciphertext=excluded.ciphertext,fingerprint=excluded.fingerprint,error_text=NULL,updated_at=excluded.updated_at`,
		userID, format, envelope, fingerprint, now, now)
	return err
}

// SigningKeyMaterial decrypts the user's private key. It returns
// ErrSigningKeyNotFound when none is stored and an unreadable *SigningKeyError
// when the row cannot be decrypted (the secret key changed).
func (s *SQLiteStore) SigningKeyMaterial(ctx context.Context, userID string) (string, []byte, error) {
	var format string
	var envelope []byte
	err := s.db.QueryRowContext(ctx, `SELECT format,ciphertext FROM coding_signing_keys WHERE user_id=?`, userID).Scan(&format, &envelope)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrSigningKeyNotFound
	}
	if err != nil {
		return "", nil, err
	}
	aead, err := signingKeyAEAD()
	if err != nil {
		return "", nil, unreadableSigningKey(err)
	}
	if len(envelope) < 1+aead.NonceSize() || envelope[0] != signingKeyEnvelopeV1 {
		return "", nil, unreadableSigningKey(errors.New("unknown ciphertext envelope"))
	}
	nonce, sealed := envelope[1:1+aead.NonceSize()], envelope[1+aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, sealed, signingKeyAAD(userID, format))
	if err != nil {
		return "", nil, unreadableSigningKey(errors.New("decryption failed"))
	}
	return format, plain, nil
}

// HasSigningKeyMaterial reports whether a key is stored, without decrypting it.
func (s *SQLiteStore) HasSigningKeyMaterial(ctx context.Context, userID string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coding_signing_keys WHERE user_id=?`, userID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// DeleteSigningKeyMaterial removes the user's key; deleting nothing succeeds.
func (s *SQLiteStore) DeleteSigningKeyMaterial(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM coding_signing_keys WHERE user_id=?`, userID)
	return err
}
