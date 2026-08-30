package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestSearchHistoryAndRecentResourcesLifecycle(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	ctx := context.Background()
	if err := repository.RecordSearch(ctx, data.Workspace.ID, data.Viewer.ID, "  roadmap  "); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordSearch(ctx, data.Workspace.ID, data.Viewer.ID, "roadmap"); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordSearch(ctx, data.Workspace.ID, data.Viewer.ID, " "); err != nil {
		t.Fatal(err)
	}
	history, err := repository.SearchHistory(ctx, data.Workspace.ID, data.Viewer.ID, 0)
	if err != nil || len(history) != 1 || history[0].Query != "roadmap" || history[0].UseCount != 2 {
		t.Fatalf("history=%#v err=%v", history, err)
	}
	if err := repository.RecordRecent(ctx, data.Workspace.ID, data.Viewer.ID, "issue", "issue_1"); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordRecent(ctx, data.Workspace.ID, data.Viewer.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	recent, err := repository.RecentResources(ctx, data.Workspace.ID, data.Viewer.ID, 500)
	if err != nil || len(recent) != 1 || recent[0].ResourceID != "issue_1" {
		t.Fatalf("recent=%#v err=%v", recent, err)
	}
	if err := repository.ClearSearchHistory(ctx, data.Workspace.ID, data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	history, err = repository.SearchHistory(ctx, data.Workspace.ID, data.Viewer.ID, 8)
	if err != nil || len(history) != 0 {
		t.Fatalf("cleared history=%#v err=%v", history, err)
	}
}

func TestDocumentCollaborationUpdateLifecycle(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	created := time.Now().UTC().Truncate(time.Microsecond)
	update := DocumentCollaborationUpdate{ID: "update-1", DocumentID: "document-1", ClientID: "client-1", Data: []byte("payload"), CreatedAt: created}
	inserted, err := repository.AppendDocumentCollaborationUpdate(ctx, "test-workspace", update)
	if err != nil || !inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}
	inserted, err = repository.AppendDocumentCollaborationUpdate(ctx, "test-workspace", update)
	if err != nil || inserted {
		t.Fatalf("duplicate inserted=%v err=%v", inserted, err)
	}
	updates, err := repository.DocumentCollaborationUpdates(ctx, "test-workspace", "document-1")
	if err != nil || len(updates) != 1 || string(updates[0].Data) != "payload" || updates[0].ClientID != "client-1" {
		t.Fatalf("updates=%#v err=%v", updates, err)
	}
	if err := repository.DeleteDocumentCollaborationUpdates(ctx, "test-workspace", "document-1", nil); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteDocumentCollaborationUpdates(ctx, "test-workspace", "document-1", []string{"update-1"}); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteDocumentCollaborationDocument(ctx, "test-workspace", "document-1"); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthStoreOneTimeGrantLifecycle(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	client := domain.OAuthClient{ClientID: "client-1", ClientName: "Test client", RedirectURIs: []string{"https://example.test/callback"}, GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"code"}, CreatedAt: now}
	if err := repository.RegisterOAuthClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	loadedClient, err := repository.OAuthClient(ctx, client.ClientID)
	if err != nil || loadedClient.ClientName != client.ClientName {
		t.Fatalf("client=%#v err=%v", loadedClient, err)
	}
	if _, err := repository.OAuthClient(ctx, "missing"); !IsOAuthNotFound(err) {
		t.Fatalf("missing client error=%v", err)
	}
	codeGrant := domain.OAuthAuthorizationCode{ClientID: client.ClientID, WorkspaceKey: "test-workspace", UserID: "usr_admin", RedirectURI: client.RedirectURIs[0], Scopes: []string{"read"}, ExpiresAt: now.Add(time.Minute)}
	if err := repository.CreateOAuthAuthorizationCode(ctx, "authorization-code", codeGrant); err != nil {
		t.Fatal(err)
	}
	consumedCode, err := repository.ConsumeOAuthAuthorizationCode(ctx, "authorization-code")
	if err != nil || consumedCode.ClientID != client.ClientID {
		t.Fatalf("code=%#v err=%v", consumedCode, err)
	}
	if _, err := repository.ConsumeOAuthAuthorizationCode(ctx, "authorization-code"); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("reused code error=%v", err)
	}
	refreshGrant := domain.OAuthRefreshGrant{ClientID: client.ClientID, WorkspaceKey: "test-workspace", UserID: "usr_admin", Scopes: []string{"read"}, ExpiresAt: now.Add(time.Hour)}
	if err := repository.CreateOAuthRefreshToken(ctx, "refresh-token", refreshGrant); err != nil {
		t.Fatal(err)
	}
	consumedRefresh, err := repository.ConsumeOAuthRefreshToken(ctx, "refresh-token")
	if err != nil || consumedRefresh.ClientID != client.ClientID {
		t.Fatalf("refresh=%#v err=%v", consumedRefresh, err)
	}
	if err := repository.RevokeOAuthRefreshToken(ctx, "refresh-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ConsumeOAuthRefreshToken(ctx, "refresh-token"); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("revoked refresh error=%v", err)
	}
}

func TestOAuthApplicationAndAPIKeyLookup(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	now := time.Now().UTC()
	if err := repository.MutateWorkspace(context.Background(), "test-workspace", "test.oauth", "workspace", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: "app-1", Name: "Workspace app", ClientID: "workspace-client", RedirectURIs: []string{"https://example.test/callback"}, CreatedAt: now})
		data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "key-1", Name: "Automation", SecretHash: "secret-hash", CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	client, ok := repository.OAuthApplicationClient("workspace-client")
	if !ok || client.ClientName != "Workspace app" {
		t.Fatalf("application client=%#v ok=%v", client, ok)
	}
	workspace, key, ok := repository.FindAPIKey("secret-hash")
	if !ok || workspace != "test-workspace" || key.ID != "key-1" {
		t.Fatalf("workspace=%q key=%#v ok=%v", workspace, key, ok)
	}
	if _, _, ok := repository.FindAPIKey("missing"); ok {
		t.Fatal("missing API key was accepted")
	}
}
