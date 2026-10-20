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

// The refresh lease is a compare-and-swap on the account row: one owner at a
// time, re-entrant for its owner, released only by its owner, and taken over
// once it expires.
func TestCodeHostAccountRefreshLease(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := t.Context()
	if _, err := repository.UpsertCodeHostAccount(ctx, domain.CodeHostAccount{ID: "cha_l", UserID: "alice", CodeHost: "github", HostName: "github.com", BaseURL: "https://github.com", ExternalUserID: "1", Login: "alice", AuthType: domain.CodeHostAuthGitHubApp}); err != nil {
		t.Fatal(err)
	}
	claim := func(owner string, lease time.Duration) bool {
		t.Helper()
		ok, err := repository.ClaimCodeHostAccountRefresh(ctx, "cha_l", owner, lease)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !claim("a", time.Minute) || claim("b", time.Minute) || !claim("a", time.Minute) {
		t.Fatal("the lease must be exclusive and re-entrant for its owner")
	}
	if err := repository.ReleaseCodeHostAccountRefresh(ctx, "cha_l", "b"); err != nil || claim("b", time.Minute) {
		t.Fatalf("only the owner releases the lease: %v", err)
	}
	if err := repository.ReleaseCodeHostAccountRefresh(ctx, "cha_l", "a"); err != nil || !claim("b", -time.Second) {
		t.Fatalf("a released lease can be claimed: %v", err)
	}
	if !claim("c", time.Minute) {
		t.Fatal("an expired lease must be taken over")
	}
	if ok, err := repository.ClaimCodeHostAccountRefresh(ctx, "cha_missing", "c", time.Minute); ok || err != nil {
		t.Fatalf("missing account: %v %v", ok, err)
	}
}
