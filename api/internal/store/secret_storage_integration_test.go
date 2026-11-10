//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestExternalDatabaseSecretStorage runs the secret storage paths (keyring
// reseal, compare-and-swap rotation, migration 1025 and the event scrub) on
// PostgreSQL or MySQL.
func TestExternalDatabaseSecretStorage(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	old, next := testSecretKey(11), testSecretKey(12)
	t.Setenv(connectorSecretKeyEnv, old)
	t.Setenv(connectorPreviousKeysEnv, "")
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	const workspace = "secrets-integration"
	_ = repo.DeleteWorkspace(ctx, workspace)
	for _, statement := range []string{
		`DELETE FROM domain_events WHERE id='evt_legacy_token'`,
		`DELETE FROM webhook_signing_secrets WHERE ref IN ('webhook:legacy-ext','webhook:sealed-ext')`,
		`DELETE FROM audit_stream_secrets WHERE webhook_id='stream-legacy-ext'`,
	} {
		if _, err := repo.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.CreateWorkspace(ctx, "Secrets Integration", workspace, "us"); err != nil {
		t.Fatal(err)
	}

	connectionID := legacyIntegrationFixture(t, repo, workspace)
	if copies := legacyTokenCopies(t, repo, workspace); copies != 2 {
		t.Fatalf("fixture stored %d legacy token copies, want 2", copies)
	}
	for range 2 {
		if err := repo.migrateIntegrationOAuthTokens(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if copies := legacyTokenCopies(t, repo, workspace); copies != 0 {
		t.Fatalf("%d plain-text token copies remain", copies)
	}
	if err := repo.RotateIntegrationOAuthTokens(ctx, workspace, connectionID, "stale", IntegrationOAuthTokens{AccessToken: "x"}); err != ErrIntegrationOAuthTokensChanged {
		t.Fatalf("stale rotation: %v", err)
	}
	if err := repo.RotateIntegrationOAuthTokens(ctx, workspace, connectionID, "legacy-refresh", IntegrationOAuthTokens{AccessToken: "a2", RefreshToken: "r2"}); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.db.ExecContext(ctx, `INSERT INTO webhook_signing_secrets(ref,workspace_key,secret,created_at) VALUES('webhook:legacy-ext',?,'whsec-legacy',1)`, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO audit_stream_secrets(webhook_id,workspace_key,secret) VALUES('stream-legacy-ext',?,'audit-legacy')`, workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetWebhookSigningSecret(ctx, "webhook:sealed-ext", workspace, "whsec-sealed"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResealSecrets(ctx); err != nil {
		t.Fatal(err)
	}

	t.Setenv(connectorSecretKeyEnv, next)
	t.Setenv(connectorPreviousKeysEnv, old)
	if _, err := repo.ResealSecrets(ctx); err != nil {
		t.Fatal(err)
	}
	t.Setenv(connectorPreviousKeysEnv, "")
	tokens, found, err := repo.IntegrationOAuthTokens(ctx, workspace, connectionID)
	if err != nil || !found || tokens.AccessToken != "a2" || tokens.RefreshToken != "r2" {
		t.Fatalf("tokens after rotation: %+v %v %v", tokens, found, err)
	}
	for ref, want := range map[string]string{"webhook:legacy-ext": "whsec-legacy", "webhook:sealed-ext": "whsec-sealed"} {
		if secret, ok, err := repo.WebhookSigningSecret(ctx, ref); err != nil || !ok || secret != want {
			t.Fatalf("%s after rotation: %q %v %v", ref, secret, ok, err)
		}
	}
	if secret, err := repo.AuditStreamSecret(ctx, "stream-legacy-ext"); err != nil || secret != "audit-legacy" {
		t.Fatalf("audit secret after rotation: %q %v", secret, err)
	}
	if err := repo.PutConnectorSecret(ctx, "expiring-ext", []byte("x"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_ = repo.DeleteConnectorSecret(ctx, "expiring-ext")
	_ = repo.DeleteIntegrationOAuthTokens(ctx, workspace, connectionID)
	_ = repo.DeleteWorkspace(ctx, workspace)
}
