package store

import (
	"bytes"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestCodeHostAccountsAreScopedToTheirUserAndSealTokens(t *testing.T) {
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := t.Context()
	alice, err := repository.UpsertCodeHostAccount(ctx, domain.CodeHostAccount{ID: "cha_a", UserID: "alice", CodeHost: "gitlab", HostName: "GitLab.Example.com", BaseURL: "https://gitlab.example.com", ExternalUserID: "5", Login: "Alice", AuthType: domain.CodeHostAuthPAT, Scopes: []string{"api", "read_user"}})
	if err != nil || alice.HostName != "gitlab.example.com" || alice.Status != domain.CodeHostAccountActive {
		t.Fatalf("upsert %+v %v", alice, err)
	}
	// Relinking keeps the id and replaces the identity.
	again, err := repository.UpsertCodeHostAccount(ctx, domain.CodeHostAccount{ID: "cha_new", UserID: "alice", CodeHost: "gitlab", HostName: "gitlab.example.com", BaseURL: "https://gitlab.example.com", ExternalUserID: "5", Login: "alice2", AuthType: domain.CodeHostAuthOAuth})
	if err != nil || again.ID != "cha_a" {
		t.Fatalf("relink %+v %v", again, err)
	}
	if err := repository.PutConnectorSecret(ctx, CodeHostAccountSecretID("cha_a"), []byte(`{"accessToken":"glpat-very-secret"}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err := repository.db.QueryRowContext(ctx, `SELECT ciphertext FROM connector_secrets WHERE id=?`, CodeHostAccountSecretID("cha_a")).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte("glpat-very-secret")) {
		t.Fatalf("token is not encrypted at rest: %v", err)
	}
	if _, err := repository.CodeHostAccount(ctx, "bob", "cha_a"); !errors.Is(err, ErrCodeHostAccountNotFound) {
		t.Fatalf("another user's account must not be found: %v", err)
	}
	if removed, _ := repository.DeleteCodeHostAccount(ctx, "bob", "cha_a"); removed {
		t.Fatal("another user removed the account")
	}
	byLogin, err := repository.CodeHostAccountsByLogin(ctx, "gitlab", "gitlab.example.com", []string{"@ALICE2", "nobody"})
	if err != nil || len(byLogin) != 1 || byLogin[0].UserID != "alice" {
		t.Fatalf("by login %+v %v", byLogin, err)
	}
	if err := repository.SetCodeHostAccountStatus(ctx, "cha_a", domain.CodeHostAccountReconnect, "revoked", nil); err != nil {
		t.Fatal(err)
	}
	listed, _ := repository.ListCodeHostAccounts(ctx, "alice")
	if len(listed) != 1 || listed[0].Status != domain.CodeHostAccountReconnect || listed[0].StatusReason != "revoked" {
		t.Fatalf("list %+v", listed)
	}
	if removed, err := repository.DeleteCodeHostAccount(ctx, "alice", "cha_a"); !removed || err != nil {
		t.Fatalf("unlink %v %v", removed, err)
	}
	if _, err := repository.ReadConnectorSecret(ctx, CodeHostAccountSecretID("cha_a"), false); err == nil {
		t.Fatal("unlink must delete the sealed tokens")
	}
}
