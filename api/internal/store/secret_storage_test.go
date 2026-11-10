package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func testSecretKey(fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

func openSecretTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

func TestSecretKeyRotationResealsUnderCurrentKey(t *testing.T) {
	old, next := testSecretKey(1), testSecretKey(2)
	t.Setenv(connectorSecretKeyEnv, old)
	t.Setenv(connectorPreviousKeysEnv, "")
	repo := openSecretTestStore(t)
	ctx := t.Context()
	if err := repo.PutConnectorSecret(ctx, "credential", []byte("private-token"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetWebhookSigningSecret(ctx, "webhook:w1", "test-workspace", "whsec-value"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetAuditStreamSecret(ctx, "test-workspace", "stream-1", "audit-value"); err != nil {
		t.Fatal(err)
	}

	// Rotate: the old key is only listed as previous; everything still opens.
	t.Setenv(connectorSecretKeyEnv, next)
	t.Setenv(connectorPreviousKeysEnv, old)
	if err := ValidateSecretKeys(); err != nil {
		t.Fatal(err)
	}
	if value, err := repo.ReadConnectorSecret(ctx, "credential", false); err != nil || string(value) != "private-token" {
		t.Fatalf("read with previous key: %q %v", value, err)
	}
	report, err := repo.ResealSecrets(ctx)
	if err != nil || report.Resealed != 3 || report.Unreadable != 0 {
		t.Fatalf("reseal after rotation: %+v %v", report, err)
	}
	if report, err := repo.ResealSecrets(ctx); err != nil || report != (SecretSealReport{}) {
		t.Fatalf("second reseal is not a no-op: %+v %v", report, err)
	}

	// The old key can now be dropped.
	t.Setenv(connectorPreviousKeysEnv, "")
	if value, err := repo.ReadConnectorSecret(ctx, "credential", false); err != nil || string(value) != "private-token" {
		t.Fatalf("read after reseal: %q %v", value, err)
	}
	if secret, ok, err := repo.WebhookSigningSecret(ctx, "webhook:w1"); err != nil || !ok || secret != "whsec-value" {
		t.Fatalf("webhook secret after reseal: %q %v %v", secret, ok, err)
	}
	if secret, err := repo.AuditStreamSecret(ctx, "stream-1"); err != nil || secret != "audit-value" {
		t.Fatalf("audit secret after reseal: %q %v", secret, err)
	}

	// A secret no configured key opens is reported, never overwritten.
	t.Setenv(connectorSecretKeyEnv, testSecretKey(3))
	report, err = repo.ResealSecrets(ctx)
	if err != nil || report.Unreadable != 3 || report.Resealed != 0 {
		t.Fatalf("unknown key: %+v %v", report, err)
	}
}

func TestValidateSecretKeys(t *testing.T) {
	for _, test := range []struct {
		current, previous string
		valid             bool
	}{
		{"", "", true},
		{testSecretKey(1), "", true},
		{testSecretKey(1), testSecretKey(2) + ", " + testSecretKey(3), true},
		{"not-a-key", "", false},
		{testSecretKey(1), "short", false},
		{"", testSecretKey(2), false},
	} {
		t.Setenv(connectorSecretKeyEnv, test.current)
		t.Setenv(connectorPreviousKeysEnv, test.previous)
		if err := ValidateSecretKeys(); (err == nil) != test.valid {
			t.Fatalf("current=%q previous=%q: %v", test.current, test.previous, err)
		}
	}
}

func TestSigningSecretsNeedKeyAndLegacyPlainValuesAreSealed(t *testing.T) {
	t.Setenv(connectorSecretKeyEnv, "")
	t.Setenv(connectorPreviousKeysEnv, "")
	repo := openSecretTestStore(t)
	ctx := t.Context()
	if err := repo.SetWebhookSigningSecret(ctx, "webhook:new", "test-workspace", "whsec-new"); !errors.Is(err, ErrSecretKeyMissing) {
		t.Fatalf("webhook secret saved without a key: %v", err)
	}
	if err := repo.SetAuditStreamSecret(ctx, "test-workspace", "stream-new", "audit-new"); !errors.Is(err, ErrSecretKeyMissing) {
		t.Fatalf("audit stream secret saved without a key: %v", err)
	}
	var count int
	if err := repo.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM webhook_signing_secrets)+(SELECT COUNT(*) FROM audit_stream_secrets)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rows stored without a key: %d %v", count, err)
	}

	// Rows an older release wrote in plain text keep working without a key.
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO webhook_signing_secrets(ref,workspace_key,secret,created_at) VALUES('webhook:legacy','test-workspace','whsec-legacy',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO audit_stream_secrets(webhook_id,workspace_key,secret) VALUES('stream-legacy','test-workspace','audit-legacy')`); err != nil {
		t.Fatal(err)
	}
	if secret, ok, err := repo.WebhookSigningSecret(ctx, "webhook:legacy"); err != nil || !ok || secret != "whsec-legacy" {
		t.Fatalf("legacy webhook secret: %q %v %v", secret, ok, err)
	}
	report, err := repo.ResealSecrets(ctx)
	if err != nil || report.Plaintext != 2 || report.Resealed != 0 {
		t.Fatalf("reseal without key: %+v %v", report, err)
	}

	// With a key the plain values are sealed in place.
	t.Setenv(connectorSecretKeyEnv, testSecretKey(4))
	report, err = repo.ResealSecrets(ctx)
	if err != nil || report.Resealed != 2 || report.Plaintext != 0 {
		t.Fatalf("reseal with key: %+v %v", report, err)
	}
	var webhookStored, auditStored string
	if err := repo.db.QueryRowContext(ctx, `SELECT secret FROM webhook_signing_secrets WHERE ref='webhook:legacy'`).Scan(&webhookStored); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(ctx, `SELECT secret FROM audit_stream_secrets WHERE webhook_id='stream-legacy'`).Scan(&auditStored); err != nil {
		t.Fatal(err)
	}
	for stored, plain := range map[string]string{webhookStored: "whsec-legacy", auditStored: "audit-legacy"} {
		if !strings.HasPrefix(stored, sealedTextPrefix) || strings.Contains(stored, plain) {
			t.Fatalf("secret not sealed: %q", stored)
		}
	}
	if secret, ok, err := repo.WebhookSigningSecret(ctx, "webhook:legacy"); err != nil || !ok || secret != "whsec-legacy" {
		t.Fatalf("sealed webhook secret: %q %v %v", secret, ok, err)
	}
	if secret, err := repo.AuditStreamSecret(ctx, "stream-legacy"); err != nil || secret != "audit-legacy" {
		t.Fatalf("sealed audit secret: %q %v", secret, err)
	}
	// A sealed value is bound to its row.
	if _, err := repo.db.ExecContext(ctx, `UPDATE audit_stream_secrets SET webhook_id='stream-moved' WHERE webhook_id='stream-legacy'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuditStreamSecret(ctx, "stream-moved"); err == nil {
		t.Fatal("audit stream secret opened under another stream")
	}
}

func TestIntegrationOAuthTokensRotateWithCompareAndSwap(t *testing.T) {
	t.Setenv(connectorSecretKeyEnv, testSecretKey(5))
	repo := openSecretTestStore(t)
	ctx := t.Context()
	if _, found, err := repo.IntegrationOAuthTokens(ctx, "test-workspace", "c1"); err != nil || found {
		t.Fatalf("tokens before connect: %v %v", found, err)
	}
	if err := repo.PutIntegrationOAuthTokens(ctx, "test-workspace", "c1", IntegrationOAuthTokens{AccessToken: "a1", RefreshToken: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RotateIntegrationOAuthTokens(ctx, "test-workspace", "c1", "stale", IntegrationOAuthTokens{AccessToken: "a2"}); !errors.Is(err, ErrIntegrationOAuthTokensChanged) {
		t.Fatalf("rotation with a stale refresh token: %v", err)
	}
	if err := repo.RotateIntegrationOAuthTokens(ctx, "test-workspace", "c1", "r1", IntegrationOAuthTokens{AccessToken: "a2"}); err != nil {
		t.Fatal(err)
	}
	tokens, found, err := repo.IntegrationOAuthTokens(ctx, "test-workspace", "c1")
	if err != nil || !found || tokens.AccessToken != "a2" || tokens.RefreshToken != "r1" {
		t.Fatalf("rotated tokens (refresh kept): %+v %v %v", tokens, found, err)
	}
	if removed, err := repo.RemoveIntegrationOAuthTokens(ctx, "test-workspace", "c1", "a1"); err != nil || removed {
		t.Fatalf("removed tokens a reconnect replaced: %v %v", removed, err)
	}
	if removed, err := repo.RemoveIntegrationOAuthTokens(ctx, "test-workspace", "c1", "a2"); err != nil || !removed {
		t.Fatalf("remove revoked tokens: %v %v", removed, err)
	}
	if _, found, _ := repo.IntegrationOAuthTokens(ctx, "test-workspace", "c1"); found {
		t.Fatal("tokens survived removal")
	}
}

// legacyIntegrationFixture stores a connection the way older releases did:
// tokens inside its workspace metadata record, and a domain event whose
// previous values copied them.
func legacyIntegrationFixture(t *testing.T, repo *SQLiteStore, workspace string) (connectionID string) {
	t.Helper()
	ctx := t.Context()
	connectionID = "integration_slack_legacy"
	if err := repo.MutateWorkspace(ctx, workspace, "integration.connected", connectionID, nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: connectionID, Provider: "slack", Name: "Slack", Status: "connected", Config: map[string]string{}, Scopes: []string{}, Channels: []string{}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := repo.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='integrationConnections' AND record_key=?`, workspace, connectionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record["oauthAccessToken"], record["oauthRefreshToken"] = "xoxb-legacy-access", "legacy-refresh"
	raw, _ = json.Marshal(record)
	if _, err := repo.db.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='integrationConnections' AND record_key=?`, raw, workspace, connectionID); err != nil {
		t.Fatal(err)
	}
	previous := `{"oauthAccessToken":"xoxb-old-access","oauthRefreshToken":"old-refresh","lastError":"timeout","updatedAt":"2026-01-02T03:04:05Z"}`
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, "evt_legacy_token", "integration.oauth_refreshed", connectionID, []byte(`{}`), previous, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return connectionID
}

// legacyTokenCopies counts stored values of the workspace that still hold a
// legacy token field (read in Go: BLOB/BYTEA columns do not compare as text
// on every dialect).
func legacyTokenCopies(t *testing.T, repo *SQLiteStore, workspace string) int {
	t.Helper()
	count := 0
	for _, query := range []string{
		`SELECT data FROM workspace_metadata_records WHERE workspace_key=?`,
		`SELECT data FROM workspace_states WHERE workspace_key=?`,
		`SELECT COALESCE(previous_values,'') FROM domain_events WHERE aggregate_id LIKE 'integration_%' AND ? <> ''`,
	} {
		rows, err := repo.db.QueryContext(t.Context(), query, workspace)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte("oauthAccessToken")) || bytes.Contains(raw, []byte("oauthRefreshToken")) {
				count++
			}
		}
		rows.Close()
	}
	return count
}

func TestIntegrationOAuthTokenMigrationEncryptsAndScrubs(t *testing.T) {
	t.Setenv(connectorSecretKeyEnv, testSecretKey(6))
	repo := openSecretTestStore(t)
	ctx := t.Context()
	workspace := repo.Bootstrap().Workspace.URLKey
	connectionID := legacyIntegrationFixture(t, repo, workspace)
	if copies := legacyTokenCopies(t, repo, workspace); copies != 2 {
		t.Fatalf("fixture stored %d legacy token copies, want 2", copies)
	}
	for range 2 { // idempotent
		if err := repo.migrateIntegrationOAuthTokens(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if copies := legacyTokenCopies(t, repo, workspace); copies != 0 {
		t.Fatalf("%d plain-text token copies remain", copies)
	}
	tokens, found, err := repo.IntegrationOAuthTokens(ctx, workspace, connectionID)
	if err != nil || !found || tokens.AccessToken != "xoxb-legacy-access" || tokens.RefreshToken != "legacy-refresh" {
		t.Fatalf("migrated tokens: %+v %v %v", tokens, found, err)
	}
	var sealed []byte
	if err := repo.db.QueryRowContext(ctx, `SELECT ciphertext FROM connector_secrets WHERE id=?`, IntegrationOAuthSecretID(workspace, connectionID)).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte("xoxb")) {
		t.Fatalf("tokens not encrypted: %v", err)
	}
	data, _ := repo.WorkspaceMetadata(workspace)
	for _, connection := range data.IntegrationConnections {
		if connection.ID == connectionID && (!connection.OAuthAuthorized || connection.Status != "connected") {
			t.Fatalf("migrated connection: %+v", connection)
		}
	}
	// The scrubbed event keeps its other previous values.
	var previous string
	if err := repo.db.QueryRowContext(ctx, `SELECT previous_values FROM domain_events WHERE id='evt_legacy_token'`).Scan(&previous); err != nil || !strings.Contains(previous, `"lastError":"timeout"`) || strings.Contains(previous, "xoxb") {
		t.Fatalf("scrubbed previous values: %s %v", previous, err)
	}
	// After a reload the connection still reads its tokens.
	reloaded, err := repo.loadWorkspaceState(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range reloaded.IntegrationConnections {
		if connection.ID == connectionID && !connection.OAuthAuthorized {
			t.Fatalf("reloaded connection lost its authorization: %+v", connection)
		}
	}
}

func TestIntegrationOAuthTokenMigrationWithoutKeyDropsTokens(t *testing.T) {
	t.Setenv(connectorSecretKeyEnv, "")
	repo := openSecretTestStore(t)
	ctx := t.Context()
	workspace := repo.Bootstrap().Workspace.URLKey
	connectionID := legacyIntegrationFixture(t, repo, workspace)
	if err := repo.migrateIntegrationOAuthTokens(ctx); err != nil {
		t.Fatal(err)
	}
	if copies := legacyTokenCopies(t, repo, workspace); copies != 0 {
		t.Fatalf("%d plain-text token copies remain", copies)
	}
	var secrets int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM connector_secrets`).Scan(&secrets); err != nil || secrets != 0 {
		t.Fatalf("secrets stored without a key: %d %v", secrets, err)
	}
	data, _ := repo.WorkspaceMetadata(workspace)
	for _, connection := range data.IntegrationConnections {
		if connection.ID == connectionID && (connection.OAuthAuthorized || connection.Status != "error" || connection.LastError != IntegrationOAuthKeyMissingError) {
			t.Fatalf("connection without key: %+v", connection)
		}
	}
}
