package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func signingKeyTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	t.Setenv("FLOW_CODING_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	return repository
}

func TestCodingSigningKeyRoundTrip(t *testing.T) {
	repository := signingKeyTestStore(t)
	ctx := context.Background()
	material := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----\n")

	_, _, err := repository.SigningKeyMaterial(ctx, "u1")
	var keyErr *SigningKeyError
	if !errors.As(err, &keyErr) || !keyErr.NotFound() || keyErr.SigningKeyUnreadable() {
		t.Fatalf("missing key: %v", err)
	}
	if err := repository.PutCodingSigningKey(ctx, "u1", "ssh", "SHA256:abc", material); err != nil {
		t.Fatal(err)
	}
	format, plain, err := repository.SigningKeyMaterial(ctx, "u1")
	if err != nil || format != "ssh" || !bytes.Equal(plain, material) {
		t.Fatalf("round trip: %q %q %v", format, plain, err)
	}
	var raw []byte
	var fingerprint string
	if err := repository.db.QueryRowContext(ctx, `SELECT ciphertext,fingerprint FROM coding_signing_keys WHERE user_id=?`, "u1").Scan(&raw, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("secret")) || fingerprint != "SHA256:abc" {
		t.Fatal("key material stored in clear")
	}
	// Replacing the key overwrites it.
	if err := repository.PutSigningKeyMaterial(ctx, "u1", "openpgp", []byte("pgp key")); err != nil {
		t.Fatal(err)
	}
	if format, plain, _ := repository.SigningKeyMaterial(ctx, "u1"); format != "openpgp" || string(plain) != "pgp key" {
		t.Fatalf("replace: %s %s", format, plain)
	}
	// A ciphertext copied to another user does not decrypt (AAD binds the user).
	if _, err := repository.db.ExecContext(ctx, `INSERT INTO coding_signing_keys(user_id,format,ciphertext,fingerprint,created_at,updated_at) SELECT 'u2',format,ciphertext,fingerprint,created_at,updated_at FROM coding_signing_keys WHERE user_id='u1'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.SigningKeyMaterial(ctx, "u2"); !errors.As(err, &keyErr) || !keyErr.SigningKeyUnreadable() {
		t.Fatalf("copied row: %v", err)
	}
	if has, err := repository.HasSigningKeyMaterial(ctx, "u1"); err != nil || !has {
		t.Fatalf("has: %v %v", has, err)
	}
	if err := repository.DeleteSigningKeyMaterial(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteSigningKeyMaterial(ctx, "u1"); err != nil {
		t.Fatal("delete must be idempotent")
	}
	if has, _ := repository.HasSigningKeyMaterial(ctx, "u1"); has {
		t.Fatal("key not deleted")
	}
}

func TestCodingSigningKeyRotatedSecretAndValidation(t *testing.T) {
	repository := signingKeyTestStore(t)
	ctx := context.Background()
	if err := repository.PutSigningKeyMaterial(ctx, "u1", "ssh", []byte("key")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_CODING_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	var keyErr *SigningKeyError
	if _, _, err := repository.SigningKeyMaterial(ctx, "u1"); !errors.As(err, &keyErr) || !keyErr.SigningKeyUnreadable() {
		t.Fatalf("rotated secret: %v", err)
	}
	for name, put := range map[string]func() error{
		"format": func() error { return repository.PutSigningKeyMaterial(ctx, "u1", "x509", []byte("k")) },
		"empty":  func() error { return repository.PutSigningKeyMaterial(ctx, "u1", "ssh", nil) },
		"user":   func() error { return repository.PutSigningKeyMaterial(ctx, "", "ssh", []byte("k")) },
		"too large": func() error {
			return repository.PutSigningKeyMaterial(ctx, "u1", "ssh", []byte(strings.Repeat("k", maxSigningKeyMaterial+1)))
		},
	} {
		if put() == nil {
			t.Errorf("%s: invalid input accepted", name)
		}
	}
	t.Setenv("FLOW_CODING_SECRET_KEY", "")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", "")
	if SigningKeyStorageConfigured() || repository.PutSigningKeyMaterial(ctx, "u1", "ssh", []byte("k")) == nil {
		t.Fatal("storing without a secret key must fail")
	}
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if !SigningKeyStorageConfigured() {
		t.Fatal("FLOW_CONNECTOR_SECRET_KEY is the fallback")
	}
}
