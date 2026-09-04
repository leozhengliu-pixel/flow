package main

import (
	"crypto/ed25519"
	"encoding/pem"
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"golang.org/x/crypto/ssh"
)

func TestCommitSigningKeyLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	_, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "flow-test")
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := string(encodePrivateKeyPEM(block))
	created := requestJSON[domain.CommitSigningKey](t, handler, http.MethodPost, "/api/account/signing-key", map[string]string{
		"name": "Laptop signing key", "privateKey": privatePEM,
	}, http.StatusCreated)
	if created.Name != "Laptop signing key" || created.Type != "ssh" || created.Fingerprint == "" {
		t.Fatalf("unexpected signing key metadata: %#v", created)
	}
	if created.Fingerprint == privatePEM {
		t.Fatal("private key was returned as fingerprint")
	}
	fetched := requestJSON[domain.CommitSigningKey](t, handler, http.MethodGet, "/api/account/signing-key", nil, http.StatusOK)
	if fetched.Fingerprint != created.Fingerprint {
		t.Fatalf("stored key mismatch: %#v vs %#v", fetched, created)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/account/settings", map[string]string{"language": "zh-CN"}, http.StatusOK)
	fetched = requestJSON[domain.CommitSigningKey](t, handler, http.MethodGet, "/api/account/signing-key", nil, http.StatusOK)
	if fetched.Fingerprint != created.Fingerprint {
		t.Fatal("unrelated settings update removed signing key metadata")
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	settings := bootstrap.UserSettings[bootstrap.Viewer.ID]
	if settings.CommitSigningKey == nil || settings.CommitSigningKey.Fingerprint != created.Fingerprint {
		t.Fatal("signing key metadata missing from account state")
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/account/signing-key", nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodGet, "/api/account/signing-key", nil, http.StatusOK)
}

func TestCommitSigningKeyRejectsInvalidAndEncryptedInput(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/account/signing-key", map[string]string{"name": "bad", "privateKey": "not-a-key"}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/account/signing-key", map[string]string{"name": "missing"}, http.StatusBadRequest)
	_, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(private, "flow-test", []byte("password"))
	if err != nil {
		t.Fatal(err)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/account/signing-key", map[string]string{"name": "encrypted", "privateKey": string(encodePrivateKeyPEM(block))}, http.StatusBadRequest)
}

// pemEncode keeps the test payload independent from the HTTP helpers.
func encodePrivateKeyPEM(block *pem.Block) []byte {
	return pem.EncodeToMemory(block)
}
