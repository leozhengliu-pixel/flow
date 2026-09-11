package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestOAuthAuthorizationGrantAtomicAndReusesExactScopes(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "authorize.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	grant := domain.OAuthAuthorizationCode{ClientID: "atomic-client", WorkspaceKey: data.Workspace.URLKey, UserID: data.Viewer.ID, Scopes: []string{"read"}, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	authorization := domain.OAuthAuthorization{ID: "atomic-authorization", ClientName: "Test client"}
	if _, err := repo.db.ExecContext(t.Context(), `CREATE TRIGGER fail_code BEFORE INSERT ON oauth_authorization_codes BEGIN SELECT RAISE(ABORT,'code failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateOAuthAuthorizationGrant(t.Context(), "first-code", grant, authorization, nil); err == nil {
		t.Fatal("code write failure ignored")
	}
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='oauthAuthorizations'`, grant.WorkspaceKey).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed code left authorization: count=%d err=%v", count, err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `DROP TRIGGER fail_code`); err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateOAuthAuthorizationGrant(t.Context(), "first-code", grant, authorization, nil)
	if err != nil {
		t.Fatal(err)
	}
	authorization.ID = "different-proposed-id"
	reused, err := repo.CreateOAuthAuthorizationGrant(t.Context(), "second-code", grant, authorization, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reused.AuthorizationID != created.AuthorizationID {
		t.Fatal("identical consent produced a second authorization")
	}
	grant.Scopes = []string{"read", "write"}
	expanded, err := repo.CreateOAuthAuthorizationGrant(t.Context(), "expanded-code", grant, authorization, nil)
	if err != nil {
		t.Fatal(err)
	}
	if expanded.AuthorizationID == created.AuthorizationID {
		t.Fatal("expanded consent silently widened existing authorization")
	}
	metadata, _ := repo.WorkspaceMetadata(grant.WorkspaceKey)
	for _, item := range metadata.OAuthAuthorizations {
		if item.ID == created.AuthorizationID && !slices.Equal(item.Scopes, []string{"read"}) {
			t.Fatal("existing consent scopes changed")
		}
	}
}

func TestOAuthAuthorizationGrantConcurrentConsentReusesOneRecord(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "concurrent-consent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	grant := domain.OAuthAuthorizationCode{ClientID: "concurrent-client", WorkspaceKey: data.Workspace.URLKey, UserID: data.Viewer.ID, Scopes: []string{"read"}, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 20)
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			out, err := repo.CreateOAuthAuthorizationGrant(context.Background(), fmt.Sprintf("code-%d", i), grant, domain.OAuthAuthorization{ID: fmt.Sprintf("auth-%d", i)}, nil)
			results <- result{out.AuthorizationID, err}
		}()
	}
	group.Wait()
	close(results)
	id := ""
	for item := range results {
		if item.err != nil {
			t.Fatal(item.err)
		}
		if id == "" {
			id = item.id
		}
		if id != item.id {
			t.Fatal("concurrent consent was duplicated")
		}
	}
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM oauth_authorization_codes`).Scan(&count); err != nil || count != 20 {
		t.Fatalf("missing per-request authorization codes: count=%d err=%v", count, err)
	}
}

func TestOAuthAuthorizationRevokeTargetsTokensAndStopsRefresh(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "current-refresh", domain.APIKey{ID: "access", SecretHash: "access-hash"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.RevokeOAuthAuthorizationRecords(t.Context(), grant.WorkspaceKey, grant.AuthorizationID, "wrong-user"); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("another user revoked consent: %v", err)
	}
	writes := auditWrites(t, repo)
	if err := repo.RevokeOAuthAuthorizationRecords(t.Context(), grant.WorkspaceKey, grant.AuthorizationID, grant.UserID); err != nil {
		t.Fatal(err)
	}
	changed := writes()
	if changed["workspace_metadata_records"] != 2 || changed["workspace_states"] != 0 || changed["issue_records"] != 0 || changed["workspace_content_records"] != 0 {
		t.Fatalf("revocation rewrote unrelated data: %v", changed)
	}
	if _, _, ok := repo.FindAPIKey("access-hash"); ok {
		t.Fatal("revoked access credential remained cached")
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "current-refresh", grant.ClientID, "next-refresh", domain.APIKey{ID: "next-access"}, nil, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("revoked refresh exchange succeeded: %v", err)
	}
	if err := repo.RevokeOAuthAuthorizationRecords(t.Context(), grant.WorkspaceKey, grant.AuthorizationID, grant.UserID); err != nil {
		t.Fatalf("repeat revoke failed: %v", err)
	}
}

func TestOAuthApprovalOnlyReadsPolicyAndRollsBackFailure(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	for _, query := range []string{`UPDATE issue_records SET data='broken unrelated issue'`, `UPDATE workspace_content_records SET data='broken unrelated comment'`} {
		if _, err := repo.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	mutate := func(data *domain.Bootstrap) error {
		if len(data.Issues)+len(data.Projects)+len(data.Users)+len(data.Comments)+len(data.Activities) > 0 {
			t.Fatal("approval loaded unrelated collections")
		}
		data.Settings["applicationPolicies"] = []any{map[string]any{"id": "pending", "status": "pending"}}
		return nil
	}
	if err := repo.RequestOAuthApplicationApproval(t.Context(), "test-workspace", "pending", mutate); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("validation failed")
	if err := repo.RequestOAuthApplicationApproval(t.Context(), "test-workspace", "pending", func(data *domain.Bootstrap) error { data.Settings["applicationPolicies"] = []any{}; return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	metadata, _ := repo.WorkspaceMetadata("test-workspace")
	if items, ok := metadata.Settings["applicationPolicies"].([]any); !ok || len(items) != 1 {
		t.Fatal("failed approval changed cache")
	}
}

func TestOAuthGrantChecksMembershipInsideTransaction(t *testing.T) {
	repo, refresh := oauthExchangeFixture(t)
	metadata, _ := repo.WorkspaceMetadata(refresh.WorkspaceKey)
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, metadata.Workspace.ID, refresh.UserID); err != nil {
		t.Fatal(err)
	}
	grant := domain.OAuthAuthorizationCode{ClientID: refresh.ClientID, WorkspaceKey: refresh.WorkspaceKey, UserID: refresh.UserID, Scopes: refresh.Scopes, ExpiresAt: refresh.ExpiresAt}
	if _, err := repo.CreateOAuthAuthorizationGrant(t.Context(), "suspended-code", grant, domain.OAuthAuthorization{ID: "suspended-consent"}, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("suspended member authorized: %v", err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", refresh.ClientID, "suspended-refresh", domain.APIKey{ID: "suspended-access"}, nil, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("suspended member refreshed: %v", err)
	}
}

func TestOAuthAuthorizationTargetedRecordsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { repo.Close() }()
	data := repo.Bootstrap()
	grant := domain.OAuthAuthorizationCode{ClientID: "restart-client", WorkspaceKey: data.Workspace.URLKey, UserID: data.Viewer.ID, Scopes: []string{"read"}, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	created, err := repo.CreateOAuthAuthorizationGrant(t.Context(), "restart-code", grant, domain.OAuthAuthorization{ID: "restart-consent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := repo.WorkspaceMetadata(grant.WorkspaceKey)
	if !slices.ContainsFunc(metadata.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool { return item.ID == created.AuthorizationID }) {
		t.Fatal("targeted consent lost after restart")
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "authorization_code", "restart-code", grant.ClientID, "restart-refresh", domain.APIKey{ID: "restart-access"}, func(domain.OAuthAuthorizationCode) bool { return true }, nil); err != nil {
		t.Fatalf("persisted code was not usable: %v", err)
	}
}

func TestOAuthAccessTokenTargetedRevocationAndRollback(t *testing.T) {
	repo, grant := oauthExchangeFixture(t)
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "refresh_token", "old-refresh", grant.ClientID, "access-refresh", domain.APIKey{ID: "revoke-access", SecretHash: "revoke-hash"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE issue_records SET data='broken unrelated issue'`, `UPDATE workspace_content_records SET data='broken unrelated history'`} {
		if _, err := repo.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	writes := auditWrites(t, repo)
	if err := repo.RevokeOAuthAccessToken(t.Context(), "unknown-secret"); err != nil {
		t.Fatal(err)
	}
	if changed := writes(); len(changed) != 0 {
		t.Fatalf("unknown revoke mutated rows: %v", changed)
	}
	if _, err := repo.db.ExecContext(t.Context(), `CREATE TRIGGER fail_revoke BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repo.RevokeOAuthAccessToken(t.Context(), "revoke-hash"); err == nil {
		t.Fatal("revocation failure was hidden")
	}
	if _, _, ok := repo.FindAPIKey("revoke-hash"); !ok {
		t.Fatal("failed revoke altered cache")
	}
	if changed := writes(); len(changed) != 0 {
		t.Fatalf("failed revoke committed writes: %v", changed)
	}
	if _, err := repo.db.ExecContext(t.Context(), `DROP TRIGGER fail_revoke`); err != nil {
		t.Fatal(err)
	}
	if err := repo.RevokeOAuthAccessToken(t.Context(), "revoke-hash"); err != nil {
		t.Fatal(err)
	}
	if changed := writes(); changed["workspace_metadata_records"] != 1 || len(changed) != 1 {
		t.Fatalf("revoke touched unrelated records: %v", changed)
	}
	if _, _, ok := repo.FindAPIKey("revoke-hash"); ok {
		t.Fatal("revoked token remained usable in cache")
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), grant.WorkspaceKey, "revoke-hash", nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("revoked token remained usable through SQL auth: %v", err)
	}
	if err := repo.RevokeOAuthAccessToken(t.Context(), "revoke-hash"); err != nil {
		t.Fatal(err)
	}
	if changed := writes(); len(changed) != 0 {
		t.Fatalf("repeat revoke wrote again: %v", changed)
	}
}
