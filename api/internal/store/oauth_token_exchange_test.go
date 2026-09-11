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
			if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "replay-refresh", domain.APIKey{ID: "replay-access"}, nil, nil); !errors.Is(err, ErrAuthForbidden) {
				t.Fatalf("replayed refresh succeeded: %v", err)
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

func TestOAuthRefreshConcurrentExchangeOnlyOneSucceeds(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	results := make(chan error, 8)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := repo.ExchangeOAuthGrant(context.Background(), "refresh_token", "old-refresh", grant.ClientID, fmt.Sprintf("refresh-%d", i), domain.APIKey{ID: fmt.Sprintf("access-%d", i)}, nil, nil)
			results <- err
		}()
	}
	group.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrAuthForbidden) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("concurrent token exchange succeeded %d times", success)
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
