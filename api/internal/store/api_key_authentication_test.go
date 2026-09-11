package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func apiKeyRecordFixture(t testing.TB) (*SQLiteStore, domain.Bootstrap, domain.APIKey) {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	data := repo.Bootstrap()
	key := domain.APIKey{ID: "indexed-access-key", CreatorID: data.Viewer.ID, SecretHash: strings.Repeat("a", 64), Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{data.Teams[0].ID}, CreatedAt: time.Now().UTC()}
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "apiKeys", key.ID, key)
	return repo, data, key
}

func TestAPIKeyAuthenticationReadsLiveRecordsAndScopes(t *testing.T) {
	repo, data, key := apiKeyRecordFixture(t)
	ctx := context.Background()
	result, err := repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, nil)
	if err != nil || result.Workspace.ID != data.Workspace.ID || result.User.ID != key.CreatorID || len(result.Key.TeamIDs) != 1 || result.Key.TeamIDs[0] != key.TeamIDs[0] {
		t.Fatalf("live credential lookup failed: %+v %v", result, err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(ctx, "other-workspace", key.SecretHash, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("wrong workspace accepted", err)
	}
	key.Scopes = []string{}
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "apiKeys", key.ID, key)
	result, err = repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, nil)
	if err != nil || result.Key.Scopes == nil || len(result.Key.Scopes) != 0 {
		t.Fatal("empty scopes upgraded", err)
	}
	oldHash := key.SecretHash
	key.SecretHash = strings.Repeat("b", 64)
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "apiKeys", key.ID, key)
	if _, err := repo.AuthenticateAPIKeyRecord(ctx, "", oldHash, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("rotated hash still authorized", err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	key.RevokedAt = &now
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "apiKeys", key.ID, key)
	if _, err := repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("revoked credential accepted from cache", err)
	}
}

func TestAPIKeyAuthenticationChecksCurrentOAuthPolicyAndAuthorization(t *testing.T) {
	repo, data, key := apiKeyRecordFixture(t)
	ctx := context.Background()
	key.OAuthClientID = "client"
	key.AuthorizationID = "authorization"
	grant := domain.OAuthAuthorization{ID: key.AuthorizationID, ClientID: key.OAuthClientID, UserID: key.CreatorID, Scopes: []string{"read"}}
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "oauthAuthorizations", grant.ID, grant)
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "apiKeys", key.ID, key)
	approved := func(policy *domain.Bootstrap, client string, scopes []string) bool {
		if !policy.WorkspaceSettings.ReviewThirdPartyApplications {
			return true
		}
		raw, _ := json.Marshal(policy.Settings["applicationPolicies"])
		var policies []struct {
			ID, Status string
			Scopes     []string
		}
		_ = json.Unmarshal(raw, &policies)
		return slices.ContainsFunc(policies, func(p struct {
			ID, Status string
			Scopes     []string
		}) bool { return p.ID == client && p.Status == "approved" && !slices.ContainsFunc(scopes, func(scope string) bool { return !slices.Contains(p.Scopes, scope) }) })
	}
	if _, err := repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, approved); err != nil {
		t.Fatal(err)
	}
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "workspaceSettings", "reviewThirdPartyApplications", true)
	if _, err := repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, approved); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("new application review policy ignored", err)
	}
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "settings", "applicationPolicies", []map[string]any{{"id": "client", "status": "approved", "scopes": []string{"read"}}})
	if _, err := repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, approved); err != nil {
		t.Fatal(err)
	}
	grant.Scopes = []string{}
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "oauthAuthorizations", grant.ID, grant)
	if _, err := repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, approved); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("authorization scope removal ignored", err)
	}
	grant.Scopes = key.Scopes
	now := time.Now().UTC()
	grant.RevokedAt = &now
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "oauthAuthorizations", grant.ID, grant)
	if _, err := repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, approved); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("authorization revocation ignored", err)
	}
}

func TestAPIKeyUsageCoalescesTwentyConcurrentRequestsWithoutWorkspaceWrites(t *testing.T) {
	repo, data, key := apiKeyRecordFixture(t)
	ctx := context.Background()
	writes := auditWrites(t, repo)
	var workers sync.WaitGroup
	failures := make(chan error, 20)
	for range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := repo.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, nil)
			if err == nil {
				err = repo.RecordAPIKeyUse(ctx, result.Workspace.URLKey, result.Key.ID, "")
			}
			if err != nil {
				failures <- err
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var used any
		err := repo.db.QueryRowContext(ctx, "SELECT "+repo.jsonText("data", "lastUsedAt")+" FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys' AND record_key=?", data.Workspace.URLKey, key.ID).Scan(&used)
		if err != nil {
			t.Fatal(err)
		}
		if used != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("usage worker did not record last use")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if changed := writes(); len(changed) != 1 || changed["workspace_metadata_records"] != 1 {
		t.Fatalf("usage wrote unrelated rows or failed to coalesce: %v", changed)
	}
	if err := repo.MutateWorkspace(ctx, data.Workspace.URLKey, "test.after_usage", data.Workspace.ID, nil, func(next *domain.Bootstrap) error { return nil }); err != nil {
		t.Fatalf("usage changed SQLite BLOB affinity: %v", err)
	}
}

func TestAPIKeyAuthenticationIgnoresUnrelatedMetadataBodies(t *testing.T) {
	repo, data, key := apiKeyRecordFixture(t)
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data='{"descriptionRevisions":"invalid-body"}' WHERE field='projects'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil); err != nil {
		t.Fatal("authentication decoded unrelated project", err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, data.Workspace.ID, key.CreatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("membership revocation ignored", err)
	}
}

func TestAPIKeyLookupMigrationBackfillsExistingRecords(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "lookup-backfill.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	dropAPIKeyLookupTriggers(t, repo)
	data := repo.Bootstrap()
	key := domain.APIKey{ID: "legacy-oauth-token", SecretHash: strings.Repeat("e", 64), CreatorID: data.Viewer.ID, Scopes: []string{"read"}, CreatedAt: time.Now().UTC()}
	raw, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'apiKeys',?,0,?)`, data.Workspace.URLKey, key.ID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("unindexed token authenticated", err)
	}
	if err := repo.migrateAPIKeyLookup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil); err != nil {
		t.Fatalf("startup backfill missed the token: %v", err)
	}
}

func TestPersonalAPIKeyAuthenticatesWithoutLookupTriggers(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "personal-key.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	dropAPIKeyLookupTriggers(t, repo)
	data := repo.Bootstrap()
	key := domain.APIKey{ID: "personal-cli-key", Name: "CLI", SecretHash: strings.Repeat("d", 64), CreatorID: data.Viewer.ID, Scopes: []string{"read", "write"}, CreatedAt: time.Now().UTC()}
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "api_key.created", key.ID, nil, func(next *domain.Bootstrap) error {
		next.APIKeys = append(next.APIKeys, key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil); err != nil {
		t.Fatalf("personal API key was not indexed: %v", err)
	}
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "api_key.revoked", key.ID, nil, func(next *domain.Bootstrap) error {
		next.APIKeys = slices.DeleteFunc(next.APIKeys, func(item domain.APIKey) bool { return item.ID == key.ID })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("removed API key remained in the hash index", err)
	}
}

func BenchmarkAPIKeyAuthenticationLargeDirectory(b *testing.B) {
	repo, data, key := apiKeyRecordFixture(b)
	repo.mu.Lock()
	snapshot := repo.workspaces[data.Workspace.URLKey]
	for i := 0; i < 10000; i++ {
		snapshot.Projects = append(snapshot.Projects, domain.Project{ID: fmt.Sprintf("project-%d", i), Description: strings.Repeat("large directory ", 100)})
	}
	repo.workspaces[data.Workspace.URLKey] = snapshot
	repo.mu.Unlock()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := repo.AuthenticateAPIKeyRecord(context.Background(), "", key.SecretHash, nil); err != nil {
			b.Fatal(err)
		}
		if err := repo.RecordAPIKeyUse(context.Background(), data.Workspace.URLKey, key.ID, ""); err != nil {
			b.Fatal(err)
		}
	}
}
