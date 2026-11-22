package store

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func seedChatLifecycle(t *testing.T, repo *SQLiteStore, record domain.ChatIntegrationRecord) {
	t.Helper()
	ctx := t.Context()
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("r", 32))))
	if err := repo.PutConnectorSecret(ctx, ChatCredentialID(record.ConnectionID), []byte(`{"appSecret":"stable-secret"}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutChatBinding(ctx, record.WorkspaceKey, domain.ChatBinding{ID: "lifecycle_binding", ConnectionID: record.ConnectionID, ChatID: "oc_lifecycle", TeamID: "team_lifecycle", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutChatIdentity(ctx, record.WorkspaceKey, record.ConnectionID, domain.ChatIdentity{UserID: "flow_lifecycle_user", OpenID: "ou_lifecycle_user", TenantKey: record.TenantKey, AuthProvider: "password"}); err != nil {
		t.Fatal(err)
	}
	action := domain.ChatAction{ID: "lifecycle_action", WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, UserID: "flow_lifecycle_user", Payload: json.RawMessage(`{"title":"test-workspace should remain in user content"}`), ExpiresAt: time.Now().Add(time.Hour)}
	if err := repo.PutChatAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	action.ID = "chat_oauth_lifecycle"
	if err := repo.PutChatAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutConnectorSecret(ctx, action.ID, []byte(`{"workspaceKey":"test-workspace","verifier":"pending-secret"}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	message := domain.ChatMessage{ID: "lifecycle_mapping", WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, ChatID: "oc_lifecycle", MessageID: "om_lifecycle", IssueID: "issue_lifecycle", Version: "1"}
	if err := repo.PutChatMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnqueueChatJob(ctx, domain.ChatJob{ID: "lifecycle_job", WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "inbound", DedupKey: "lifecycle_source", Payload: json.RawMessage(`{"text":"test-workspace should remain unchanged"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.InitializeChatDomainCursor(ctx, record.WorkspaceKey); err != nil {
		t.Fatal(err)
	}
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeChatMutationReceipt(ctx, tx, record.WorkspaceKey, "flow_lifecycle_user", "lifecycle_receipt", "issue_lifecycle"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestChatWorkspaceRenameKeepsStableCredentialAndStructuredRouting(t *testing.T) {
	repo, record := chatStoreFixture(t)
	seedChatLifecycle(t, repo, record)
	data, _ := repo.WorkspaceMetadata(record.WorkspaceKey)
	workspace := data.Workspace
	workspace.URLKey = "renamed-chat-workspace"
	if _, err := repo.UpdateWorkspace(t.Context(), record.WorkspaceKey, workspace); err != nil {
		t.Fatal(err)
	}
	renamed, err := repo.ChatIntegrationByID(t.Context(), record.Provider, record.ConnectionID)
	if err != nil || renamed.WorkspaceKey != workspace.URLKey {
		t.Fatalf("callback route did not follow workspace rename: %#v %v", renamed, err)
	}
	if _, err = repo.ChatIntegration(t.Context(), record.WorkspaceKey, record.Provider); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old workspace retained routing: %v", err)
	}
	credential, err := repo.ReadConnectorSecret(t.Context(), ChatCredentialID(renamed.ConnectionID), false)
	if err != nil || !strings.Contains(string(credential), "stable-secret") {
		t.Fatalf("renaming invalidated encrypted credential: %q %v", credential, err)
	}
	bindings, err := repo.ChatBindings(t.Context(), workspace.URLKey, record.ConnectionID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("binding not migrated: %#v %v", bindings, err)
	}
	identity, err := repo.ChatIdentityForUser(t.Context(), workspace.URLKey, record.ConnectionID, "flow_lifecycle_user")
	if err != nil || identity.OpenID != "ou_lifecycle_user" || identity.AuthProvider != "password" {
		t.Fatalf("identity/evidence lost: %#v %v", identity, err)
	}
	action, err := repo.GetChatAction(t.Context(), workspace.URLKey, record.ConnectionID, "lifecycle_action")
	if err != nil || action.WorkspaceKey != workspace.URLKey || !strings.Contains(string(action.Payload), "test-workspace should remain") {
		t.Fatalf("action routing or user content rewritten incorrectly: %#v %v", action, err)
	}
	if _, err = repo.GetChatAction(t.Context(), workspace.URLKey, record.ConnectionID, "chat_oauth_lifecycle"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("pending OAuth action survived rename")
	}
	if _, err = repo.ReadConnectorSecret(t.Context(), "chat_oauth_lifecycle", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("pending OAuth encrypted state survived rename")
	}
	message, err := repo.ChatMessage(t.Context(), workspace.URLKey, record.ConnectionID, "om_lifecycle")
	if err != nil || message.WorkspaceKey != workspace.URLKey {
		t.Fatalf("card mapping routing not migrated: %#v %v", message, err)
	}
	jobs, err := repo.ClaimChatJobs(t.Context(), workspace.URLKey, "inbound", 1, time.Now().Add(time.Second))
	if err != nil || len(jobs) != 1 || jobs[0].WorkspaceKey != workspace.URLKey || !strings.Contains(string(jobs[0].Payload), "test-workspace should remain") {
		t.Fatalf("queued job routing/content not migrated: %#v %v", jobs, err)
	}
	if id, found, err := repo.ChatMutationReceipt(t.Context(), workspace.URLKey, "flow_lifecycle_user", "lifecycle_receipt"); err != nil || !found || id != "issue_lifecycle" {
		t.Fatalf("receipt scope not migrated: %q %v %v", id, found, err)
	}
	var cursor int
	if err = repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM chat_domain_cursors WHERE workspace_key=?`, workspace.URLKey).Scan(&cursor); err != nil || cursor != 1 {
		t.Fatal("notification cursor did not migrate")
	}
}

func TestChatWorkspaceDeletionPreventsURLReuseAndRemovesSecrets(t *testing.T) {
	repo, record := chatStoreFixture(t)
	seedChatLifecycle(t, repo, record)
	if err := repo.DeleteWorkspace(t.Context(), record.WorkspaceKey); err != nil {
		t.Fatal(err)
	}
	for _, table := range chatWorkspaceTables {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=?`, record.WorkspaceKey).Scan(&count); err != nil || count != 0 {
			t.Fatalf("workspace left rows in %s: %d %v", table, count, err)
		}
	}
	for _, id := range []string{ChatCredentialID(record.ConnectionID), "chat_oauth_lifecycle"} {
		if _, err := repo.ReadConnectorSecret(t.Context(), id, false); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("deleted workspace retained encrypted secret %s: %v", id, err)
		}
	}
	if _, err := repo.CreateWorkspace(t.Context(), "Replacement", record.WorkspaceKey, "us"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ChatIntegration(t.Context(), record.WorkspaceKey, record.Provider); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reused workspace inherited old application: %v", err)
	}
	if _, err := repo.ChatIdentityForOpenID(t.Context(), record.WorkspaceKey, record.ConnectionID, "ou_lifecycle_user"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reused workspace inherited old identity: %v", err)
	}
	if jobs, err := repo.ClaimChatJobs(t.Context(), record.WorkspaceKey, "inbound", 1, time.Now().Add(time.Second)); err != nil || len(jobs) != 0 {
		t.Fatalf("reused workspace inherited jobs: %#v %v", jobs, err)
	}
}
