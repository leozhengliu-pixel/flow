package store

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectorSecretsEncryptedBoundAndSingleUse(t *testing.T) {
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err = repo.PutConnectorSecret(t.Context(), "state", []byte("private-token"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = repo.db.QueryRowContext(t.Context(), `SELECT ciphertext FROM connector_secrets WHERE id='state'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-token") {
		t.Fatal("credential stored in plaintext")
	}
	if _, err = repo.db.ExecContext(t.Context(), `INSERT INTO connector_secrets(id,ciphertext,expires_at) SELECT 'copied',ciphertext,expires_at FROM connector_secrets WHERE id='state'`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReadConnectorSecret(t.Context(), "copied", false); err == nil {
		t.Fatal("ciphertext accepted with different ownership key")
	}
	value, err := repo.ReadConnectorSecret(t.Context(), "state", true)
	if err != nil || string(value) != "private-token" {
		t.Fatal("cannot consume state")
	}
	if _, err = repo.ReadConnectorSecret(t.Context(), "state", true); err == nil {
		t.Fatal("OAuth state replay succeeded")
	}
}
