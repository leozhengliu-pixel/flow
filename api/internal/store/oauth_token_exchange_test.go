package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func dropAPIKeyLookupTriggers(t *testing.T, repo *SQLiteStore) {
	t.Helper()
	for _, name := range []string{"api_key_lookup_insert", "api_key_lookup_update", "api_key_lookup_delete"} {
		if _, err := repo.db.ExecContext(t.Context(), "DROP TRIGGER IF EXISTS "+name); err != nil {
			t.Fatal(err)
		}
	}
}

func oauthExchangeFixture(t *testing.T) (*SQLiteStore, domain.OAuthRefreshGrant) {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "exchange.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	data := repo.Bootstrap()
	grant := domain.OAuthRefreshGrant{ClientID: "oauth-test", WorkspaceKey: data.Workspace.URLKey, UserID: data.Viewer.ID, AuthorizationID: "grant-test", Scopes: []string{"read"}, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := repo.MutateWorkspace(t.Context(), grant.WorkspaceKey, "oauth_authorization.created", grant.AuthorizationID, nil, func(next *domain.Bootstrap) error {
		next.OAuthAuthorizations = append(next.OAuthAuthorizations, domain.OAuthAuthorization{ID: grant.AuthorizationID, ClientID: grant.ClientID, UserID: grant.UserID, Scopes: grant.Scopes})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateOAuthRefreshToken(t.Context(), "old-refresh", grant); err != nil {
		t.Fatal(err)
	}
	return repo, grant
}

func TestOAuthRefreshExchangeRollsBackStorageFailures(t *testing.T) {
	for _, table := range []string{"workspace_metadata_records", "oauth_refresh_tokens", "domain_events"} {
		t.Run(table, func(t *testing.T) {
			repo, grant := oauthExchangeFixture(t)
			if _, err := repo.db.ExecContext(t.Context(), fmt.Sprintf(`CREATE TRIGGER fail_exchange BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT,'injected storage failure'); END`, table)); err != nil {
				t.Fatal(err)
			}
			key := domain.APIKey{ID: "new-access", SecretHash: "new-hash"}
			if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "new-refresh", key, nil, nil); err == nil || errors.Is(err, ErrAuthForbidden) {
				t.Fatalf("storage error must remain retriable: %v", err)
			}
			if _, _, ok := repo.FindAPIKey("new-hash"); ok {
				t.Fatal("failed transaction exposed access token in cache")
			}
			if _, err := repo.db.ExecContext(t.Context(), `DROP TRIGGER fail_exchange`); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "new-refresh", key, nil, nil); err != nil {
				t.Fatalf("failure consumed old refresh token: %v", err)
			}
			if _, _, ok := repo.FindAPIKey("new-hash"); !ok {
				t.Fatal("committed access token absent from cache")
			}
			// A retry inside the grace window returns the committed successor.
			replayed, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "replay-refresh", domain.APIKey{ID: "replay-access"}, nil, nil)
			if err != nil || replayed.ReplayedRefreshToken != "new-refresh" {
				t.Fatalf("replay = %q, %v", replayed.ReplayedRefreshToken, err)
			}
		})
	}
}

func TestOAuthExchangeValidatesBeforeConsumption(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", "wrong-client", "new-refresh", domain.APIKey{ID: "wrong-access"}, nil, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("wrong client accepted: %v", err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "new-refresh", domain.APIKey{ID: "right-access"}, nil, nil); err != nil {
		t.Fatalf("wrong client consumed legitimate refresh: %v", err)
	}
	code := domain.OAuthAuthorizationCode{ClientID: grant.ClientID, WorkspaceKey: grant.WorkspaceKey, UserID: grant.UserID, AuthorizationID: grant.AuthorizationID, Scopes: grant.Scopes, ExpiresAt: grant.ExpiresAt, CodeChallenge: "expected"}
	if err := repo.CreateOAuthAuthorizationCode(t.Context(), "code", code); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "authorization_code", "code", grant.ClientID, "code-refresh", domain.APIKey{ID: "code-access"}, func(domain.OAuthAuthorizationCode) bool { return false }, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("invalid PKCE accepted: %v", err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "authorization_code", "code", grant.ClientID, "code-refresh", domain.APIKey{ID: "code-access"}, func(value domain.OAuthAuthorizationCode) bool { return value.CodeChallenge == "expected" }, nil); err != nil {
		t.Fatalf("failed PKCE consumed legitimate code: %v", err)
	}
}

// Concurrent refreshes rotate once: one request mints the successor, the
// others are grace-window replays that return that same successor.
func TestOAuthRefreshConcurrentExchangeRotatesOnce(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	type outcome struct {
		issued, replayed string
		err              error
	}
	results := make(chan outcome, 8)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			issued := fmt.Sprintf("refresh-%d", i)
			result, err := repo.ExchangeOAuthGrant(context.Background(), "refresh_token", "old-refresh", grant.ClientID, issued, domain.APIKey{ID: fmt.Sprintf("access-%d", i), SecretHash: fmt.Sprintf("hash-%d", i)}, nil, nil)
			results <- outcome{issued, result.ReplayedRefreshToken, err}
		}()
	}
	group.Wait()
	close(results)
	winners, successor, replays := 0, "", []string{}
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.replayed == "" {
			winners++
			successor = result.issued
		} else {
			replays = append(replays, result.replayed)
		}
	}
	if winners != 1 || len(replays) != 7 {
		t.Fatalf("rotations = %d, replays = %d", winners, len(replays))
	}
	for _, replayed := range replays {
		if replayed != successor {
			t.Fatalf("replay returned %q, successor %q", replayed, successor)
		}
	}
	var rows int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM oauth_refresh_tokens WHERE revoked_at IS NULL`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("live refresh tokens = %d (%v)", rows, err)
	}
}

func TestOAuthRefreshReplayGraceWindowAndSuccessorUse(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "second", domain.APIKey{ID: "a1", SecretHash: "h1"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// The replay record is sealed: the successor is not stored in clear and
	// the record can never be redeemed as a refresh token.
	var sealed []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM oauth_refresh_tokens WHERE token_hash=?`, oauthReplayHash("old-refresh")).Scan(&sealed); err != nil || strings.Contains(string(sealed), "second") {
		t.Fatalf("replay record = %s (%v)", sealed, err)
	}
	if state, found, _ := repo.OAuthRefreshToken(t.Context(), "old-refresh"); !found || state.Active {
		t.Fatalf("consumed token state = %+v %v", state, found)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", "other-client", "x", domain.APIKey{ID: "a2"}, nil, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("other client replayed: %v", err)
	}
	// Grace window over: replay refused, successor still valid.
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE oauth_refresh_tokens SET expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), oauthReplayHash("old-refresh")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "x", domain.APIKey{ID: "a3"}, nil, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("replay after grace window: %v", err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "second", grant.ClientID, "third", domain.APIKey{ID: "a4", SecretHash: "h4"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Revoking a consumed token removes its replay record.
	if err := repo.RevokeOAuthRefreshToken(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "second", grant.ClientID, "x", domain.APIKey{ID: "a5"}, nil, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("revoked token replayed: %v", err)
	}
	// The forged replay key itself is not a refresh token.
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "flow-oauth-refresh-replay\x00third", grant.ClientID, "x", domain.APIKey{ID: "a6"}, nil, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("replay key redeemed: %v", err)
	}
}

func TestOAuthRefreshDoesNotHydrateIssuesOrDiscussions(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	for _, query := range []string{`UPDATE issue_records SET data='invalid JSON'`, `UPDATE workspace_content_records SET data='invalid JSON'`} {
		if _, err := repo.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	writes := auditWrites(t, repo)
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "new-refresh", domain.APIKey{ID: "new-access"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	changes := writes()
	if changes["issue_records"] != 0 || changes["workspace_content_records"] != 0 || changes["workspace_metadata_records"] != 1 {
		t.Fatalf("unbounded token exchange writes: %v", changes)
	}
}

func TestOAuthExchangeChecksCurrentAuthorizationAndApplicationPolicy(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	key := domain.APIKey{ID: "policy-access"}
	if err := repo.MutateWorkspace(t.Context(), grant.WorkspaceKey, "application_policy.updated", "policy", nil, func(next *domain.Bootstrap) error {
		next.WorkspaceSettings.ReviewThirdPartyApplications = true
		if next.Settings == nil {
			next.Settings = map[string]any{}
		}
		next.Settings["applicationPolicies"] = []any{map[string]any{"id": grant.ClientID, "status": "approved"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	approved := func(data *domain.Bootstrap, client string, scopes []string) bool {
		if !data.WorkspaceSettings.ReviewThirdPartyApplications || data.Settings["applicationPolicies"] == nil {
			t.Fatal("exchange did not load current policy rows")
		}
		return false
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "policy-refresh", key, nil, approved); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("policy denied exchange succeeded: %v", err)
	}
	if err := repo.MutateWorkspace(t.Context(), grant.WorkspaceKey, "oauth_authorization.revoked", grant.AuthorizationID, nil, func(next *domain.Bootstrap) error {
		for i := range next.OAuthAuthorizations {
			if next.OAuthAuthorizations[i].ID == grant.AuthorizationID {
				now := time.Now().UTC()
				next.OAuthAuthorizations[i].RevokedAt = &now
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "policy-refresh", key, nil, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("revoked authorization refreshed: %v", err)
	}
}

func TestOAuthExchangeAuthenticatesWithoutLookupTriggers(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	dropAPIKeyLookupTriggers(t, repo)
	key := domain.APIKey{ID: "lookup-access", SecretHash: strings.Repeat("c", 64), Scopes: []string{"read"}}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "new-refresh", key, nil, nil); err != nil {
		t.Fatal(err)
	}
	result, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil)
	if err != nil {
		t.Fatalf("issued OAuth token was not indexed: %v", err)
	}
	if result.Key.ID != key.ID || result.Key.AuthorizationID != grant.AuthorizationID {
		t.Fatalf("authenticated the wrong credential: %+v", result.Key)
	}
}

// Each exchange issues a fresh access token; expired OAuth token rows are
// dropped instead of accumulating as one "MCP OAuth token" per refresh, while
// live tokens (other sessions of the same authorization) are kept.
func TestOAuthExchangePrunesExpiredAccessTokens(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	past, future := time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Hour)
	if err := repo.MutateWorkspace(t.Context(), grant.WorkspaceKey, "api_key.created", "seed", nil, func(next *domain.Bootstrap) error {
		next.APIKeys = append(next.APIKeys,
			domain.APIKey{ID: "expired-oauth", SecretHash: "expired-hash", OAuthClientID: grant.ClientID, AuthorizationID: grant.AuthorizationID, ExpiresAt: &past},
			domain.APIKey{ID: "personal-expired", SecretHash: "personal-hash", ExpiresAt: &past},
			domain.APIKey{ID: "live-oauth", SecretHash: "live-hash", OAuthClientID: grant.ClientID, AuthorizationID: grant.AuthorizationID, ExpiresAt: &future},
			domain.APIKey{ID: "older-live-oauth", SecretHash: "older-hash", OAuthClientID: grant.ClientID, AuthorizationID: grant.AuthorizationID, ExpiresAt: &future},
			domain.APIKey{ID: "other-app-oauth", SecretHash: "other-hash", OAuthClientID: "other-client", AuthorizationID: "other-grant", ExpiresAt: &future},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "new-refresh", domain.APIKey{ID: "new-access", SecretHash: "new-hash", ExpiresAt: &future}, nil, nil); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, key := range repo.Bootstrap().APIKeys {
		ids[key.ID] = true
	}
	if ids["expired-oauth"] || !ids["personal-expired"] || !ids["live-oauth"] || !ids["older-live-oauth"] || !ids["other-app-oauth"] || !ids["new-access"] {
		t.Fatalf("api keys after exchange = %v", ids)
	}
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE field='apiKeys' AND record_key='expired-oauth'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired token row still stored: count=%d err=%v", count, err)
	}
}

func TestOAuthRefreshRevocationRevokesItsAccessTokens(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	if err := repo.CreateOAuthRefreshToken(t.Context(), "other-session", grant); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(time.Hour)
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "chain-2", domain.APIKey{ID: "chain-access-1", SecretHash: "chain-hash-1", ExpiresAt: &future}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "chain-2", grant.ClientID, "chain-3", domain.APIKey{ID: "chain-access-2", SecretHash: "chain-hash-2", ExpiresAt: &future}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "other-session", grant.ClientID, "other-2", domain.APIKey{ID: "other-access", SecretHash: "other-hash", ExpiresAt: &future}, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, first, _ := repo.FindAPIKey("chain-hash-1")
	_, second, _ := repo.FindAPIKey("chain-hash-2")
	_, other, _ := repo.FindAPIKey("other-hash")
	if first.RefreshFamilyID == "" || first.RefreshFamilyID != second.RefreshFamilyID || other.RefreshFamilyID == first.RefreshFamilyID {
		t.Fatalf("families = %q %q %q", first.RefreshFamilyID, second.RefreshFamilyID, other.RefreshFamilyID)
	}
	// Revoking an already rotated token ends the whole chain.
	if err := repo.RevokeOAuthRefreshToken(t.Context(), "chain-2"); err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"chain-hash-1", "chain-hash-2"} {
		if _, _, ok := repo.FindAPIKey(hash); ok {
			t.Fatalf("access token %s survived refresh revocation", hash)
		}
	}
	if _, _, ok := repo.FindAPIKey("other-hash"); !ok {
		t.Fatal("another session's access token was revoked")
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "chain-3", grant.ClientID, "x", domain.APIKey{ID: "x"}, nil, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("revoked refresh token exchanged: %v", err)
	}
}
