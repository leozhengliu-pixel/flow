package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// The egress proxy authenticates a sandbox by (sandbox id, token) through
// whichever Flow replica answers. A token minted on one replica must
// authenticate on another, always as its own sandbox; a restart mints a new
// token while the old one stays valid until its own expiry; revocation and
// expiry are seen by every replica.
func TestSandboxTokenIdentityAcrossReplicasAndRotation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coding.db")
	replicaA, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replicaA.Close() })
	replicaB, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replicaB.Close() })

	now := time.Now()
	for _, id := range []string{"sa", "sb"} {
		if err := replicaA.CreateWarmSandbox(ctx, CodingSandbox{ID: id, Size: "medium", Region: "eu", Desired: "running", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	tokenA, err := replicaA.MintSandboxToken(ctx, CodingSandbox{ID: "sa"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenB, err := replicaB.MintSandboxToken(ctx, CodingSandbox{ID: "sb"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for name, repo := range map[string]*SQLiteStore{"A": replicaA, "B": replicaB} {
		if grant, err := repo.AuthenticateSandboxToken(ctx, tokenA); err != nil || grant.SandboxID != "sa" {
			t.Fatalf("replica %s: token of sa authenticates as %q (%v)", name, grant.SandboxID, err)
		}
		if grant, err := repo.AuthenticateSandboxToken(ctx, tokenB); err != nil || grant.SandboxID != "sb" {
			t.Fatalf("replica %s: token of sb authenticates as %q (%v)", name, grant.SandboxID, err)
		}
	}

	// Rotation: a restart mints a fresh token; the previous one keeps its
	// own expiry (processes started before the rewrite still hold it).
	rotated, err := replicaB.MintSandboxToken(ctx, CodingSandbox{ID: "sa"}, time.Hour)
	if err != nil || rotated == tokenA {
		t.Fatalf("rotation must mint a new token (%v)", err)
	}
	for _, token := range []string{tokenA, rotated} {
		if grant, err := replicaA.AuthenticateSandboxToken(ctx, token); err != nil || grant.SandboxID != "sa" {
			t.Fatalf("token before expiry: %q (%v)", grant.SandboxID, err)
		}
	}
	if _, err := replicaA.db.ExecContext(ctx, `UPDATE coding_sandbox_tokens SET expires_at=? WHERE token_hash=?`, millis(now.Add(-time.Second)), tokenHash(tokenA)); err != nil {
		t.Fatal(err)
	}
	if _, err := replicaB.AuthenticateSandboxToken(ctx, tokenA); !errors.Is(err, ErrCodingTokenInvalid) {
		t.Fatalf("expired token on the other replica: %v", err)
	}
	if grant, err := replicaB.AuthenticateSandboxToken(ctx, rotated); err != nil || grant.SandboxID != "sa" {
		t.Fatalf("current token: %q (%v)", grant.SandboxID, err)
	}

	// Stop revokes every token of the sandbox, on every replica, and leaves
	// other sandboxes alone.
	if err := replicaA.RevokeSandboxTokens(ctx, "sa"); err != nil {
		t.Fatal(err)
	}
	if _, err := replicaB.AuthenticateSandboxToken(ctx, rotated); !errors.Is(err, ErrCodingTokenInvalid) {
		t.Fatalf("revoked token: %v", err)
	}
	if grant, err := replicaB.AuthenticateSandboxToken(ctx, tokenB); err != nil || grant.SandboxID != "sb" {
		t.Fatalf("other sandbox after revoke: %q (%v)", grant.SandboxID, err)
	}
}
