package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func chatStoreFixture(t *testing.T) (*SQLiteStore, domain.ChatIntegrationRecord) {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	if err = repo.EnsureChatIntegrationsSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	record := domain.ChatIntegrationRecord{WorkspaceKey: "test-workspace", Provider: "feishu", ConnectionID: "chat_first", AppID: "cli_test", TenantKey: "tenant_a", BotOpenID: "ou_bot", Connected: true}
	if err = repo.PutChatIntegration(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	return repo, record
}

func TestChatIntegrationTenantIdentityAndDisclosureIsolation(t *testing.T) {
	repo, record := chatStoreFixture(t)
	other := record
	other.WorkspaceKey = "other"
	other.ConnectionID = "other_connection"
	if err := repo.PutChatIntegration(t.Context(), other); err == nil {
		t.Fatal("same application tenant was attached to two workspaces")
	}
	if _, err := repo.ChatIntegration(t.Context(), "other", "feishu"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unexpected cross-workspace record: %v", err)
	}
	mfa := time.Now().UTC()
	identity := domain.ChatIdentity{UserID: "flow_alice", OpenID: "ou_alice", TenantKey: record.TenantKey, Name: "Alice", AuthProvider: "oidc", AuthIssuer: "https://identity.example.com", MFAVerifiedAt: &mfa}
	if err := repo.PutChatIdentity(t.Context(), record.WorkspaceKey, record.ConnectionID, identity); err != nil {
		t.Fatal(err)
	}
	bound, err := repo.ChatIdentityForOpenID(t.Context(), record.WorkspaceKey, record.ConnectionID, identity.OpenID)
	if err != nil || bound.UserID != identity.UserID || bound.AuthProvider != "oidc" || bound.AuthIssuer != identity.AuthIssuer || bound.MFAVerifiedAt == nil {
		t.Fatalf("binding authentication not persisted: %#v %v", bound, err)
	}
	public, _ := json.Marshal(bound)
	var response map[string]any
	_ = json.Unmarshal(public, &response)
	if _, exists := response["authProvider"]; exists {
		t.Fatal("private session evidence exposed publicly")
	}
	stolen := identity
	stolen.UserID = "flow_bob"
	if err = repo.PutChatIdentity(t.Context(), record.WorkspaceKey, record.ConnectionID, stolen); err == nil {
		t.Fatal("platform identity rebound to another account")
	}
	wrongTenant := identity
	wrongTenant.TenantKey = "tenant_b"
	if err = repo.PutChatIdentity(t.Context(), record.WorkspaceKey, record.ConnectionID, wrongTenant); err == nil {
		t.Fatal("cross-tenant identity allowed")
	}
	if _, err = repo.ChatIdentityForUser(t.Context(), "other", record.ConnectionID, identity.UserID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cross-workspace identity read allowed")
	}
	binding := domain.ChatBinding{ID: "binding_a", ConnectionID: record.ConnectionID, ChatID: "oc_a", TeamID: "team_a", Enabled: true, CreatedAt: time.Now().UTC()}
	if err = repo.PutChatBinding(t.Context(), record.WorkspaceKey, binding); err != nil {
		t.Fatal(err)
	}
	if err = repo.PutChatBinding(t.Context(), "other", binding); err == nil {
		t.Fatal("cross-workspace binding overwrite allowed")
	}
	items, err := repo.ChatBindings(t.Context(), record.WorkspaceKey, record.ConnectionID)
	if err != nil || len(items) != 1 || items[0].ChatID != binding.ChatID {
		t.Fatalf("binding changed: %#v %v", items, err)
	}
}

func TestChatJobsDedupLeaseCrashRecoveryAndRevocation(t *testing.T) {
	repo, record := chatStoreFixture(t)
	now := time.Now().UTC()
	job := domain.ChatJob{ID: "job_a", WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "inbound", DedupKey: "message_a", Payload: json.RawMessage(`{"title":"Example"}`), RunAt: now}
	inserted, err := repo.EnqueueChatJob(t.Context(), job)
	if err != nil || !inserted {
		t.Fatalf("enqueue: %v %v", inserted, err)
	}
	duplicate := job
	duplicate.ID = "job_b"
	inserted, err = repo.EnqueueChatJob(t.Context(), duplicate)
	if err != nil || inserted {
		t.Fatalf("duplicate message accepted: %v %v", inserted, err)
	}
	claimed, err := repo.ClaimChatJobs(t.Context(), record.WorkspaceKey, job.Kind, 2, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	second, err := repo.ClaimChatJobs(t.Context(), record.WorkspaceKey, job.Kind, 2, now.Add(time.Second))
	if err != nil || len(second) != 0 {
		t.Fatalf("live lease stolen: %#v %v", second, err)
	}
	recovered, err := repo.ClaimChatJobs(t.Context(), record.WorkspaceKey, job.Kind, 2, now.Add(2*time.Minute))
	if err != nil || len(recovered) != 1 || recovered[0].Attempts != 2 {
		t.Fatalf("crashed worker not recovered: %#v %v", recovered, err)
	}
	if err = repo.FinishChatJob(t.Context(), claimed[0], "completed", now, ""); err == nil {
		t.Fatal("stale worker finished another worker's job")
	}
	if err = repo.FinishChatJob(t.Context(), recovered[0], "completed", now, ""); err != nil {
		t.Fatal(err)
	}
	if pending, err := repo.ClaimChatJobs(t.Context(), record.WorkspaceKey, job.Kind, 2, now.Add(5*time.Minute)); err != nil || len(pending) > 0 {
		t.Fatalf("completed job replayed: %#v %v", pending, err)
	}
	action := domain.ChatAction{ID: "action_a", WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, UserID: "alice", Payload: json.RawMessage(`{"kind":"create"}`), ExpiresAt: now.Add(time.Hour)}
	if err = repo.PutChatAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}
	action.Payload = json.RawMessage(`{"kind":"create","message":"om_1"}`)
	if err = repo.UpdateChatAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetChatAction(t.Context(), record.WorkspaceKey, record.ConnectionID, action.ID); err != nil || string(got.Payload) != string(action.Payload) {
		t.Fatalf("action mapping not persisted: %#v %v", got, err)
	}
	if err = repo.DeleteChatIdentity(t.Context(), record.WorkspaceKey, record.ConnectionID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetChatAction(t.Context(), record.WorkspaceKey, record.ConnectionID, action.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unlink did not revoke pending authorization/action")
	}
	if err = repo.DeleteChatIntegration(t.Context(), record.WorkspaceKey, record.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ChatIntegrationByID(t.Context(), record.Provider, record.ConnectionID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("disconnect retained application route")
	}
	if inserted, err = repo.EnqueueChatJob(t.Context(), job); err != nil || inserted {
		t.Fatalf("job accepted for disconnected app: %v %v", inserted, err)
	}
}

func TestChatMessageMappingAndDistributedRateReservation(t *testing.T) {
	repo, record := chatStoreFixture(t)
	message := domain.ChatMessage{ID: "mapping_a", WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, ChatID: "oc_a", MessageID: "om_a", IssueID: "issue_a", Version: "1"}
	if err := repo.PutChatMessage(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	firstMapping, err := repo.ChatMessage(t.Context(), record.WorkspaceKey, record.ConnectionID, message.MessageID)
	if err != nil || firstMapping.CreatedAt.IsZero() {
		t.Fatalf("message creation timestamp absent: %#v %v", firstMapping, err)
	}
	message.Version = "2"
	message.CreatedAt = time.Now().Add(time.Hour)
	if err := repo.PutChatMessage(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	messages, err := repo.ChatMessagesForIssue(t.Context(), record.WorkspaceKey, record.ConnectionID, "issue_a")
	if err != nil || len(messages) != 1 || messages[0].Version != "2" {
		t.Fatalf("card mapping failed: %#v %v", messages, err)
	}
	if !messages[0].CreatedAt.Equal(firstMapping.CreatedAt) {
		t.Fatal("card refresh reset platform message age")
	}
	now := time.Now().UTC()
	first, err := repo.ReserveChatRequest(t.Context(), "same_tenant", 250*time.Millisecond, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.ReserveChatRequest(t.Context(), "same_tenant", 250*time.Millisecond, now)
	if err != nil {
		t.Fatal(err)
	}
	if second.Sub(first) < 249*time.Millisecond {
		t.Fatalf("same tenant requests not serialized: %s %s", first, second)
	}
}

func TestChatRetentionPreservesRecentInboxDedup(t *testing.T) {
	repo, record := chatStoreFixture(t)
	now := time.Now().UTC()
	job := domain.ChatJob{ID: "retained_job", WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "inbound", DedupKey: "message_retained", Payload: json.RawMessage(`{}`), RunAt: now.Add(-40 * 24 * time.Hour)}
	if _, err := repo.EnqueueChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimChatJobs(t.Context(), record.WorkspaceKey, job.Kind, 1, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %#v", err, claimed)
	}
	if err = repo.FinishChatJob(t.Context(), claimed[0], "completed", time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	var completion int64
	if err = repo.db.QueryRowContext(t.Context(), `SELECT run_at FROM chat_jobs WHERE id=?`, job.ID).Scan(&completion); err != nil || completion < now.Add(-time.Minute).UnixMilli() {
		t.Fatal("terminal timestamp did not reflect completion")
	}
	if err = repo.PurgeChatHistory(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	duplicate := job
	duplicate.ID = "retained_duplicate"
	if inserted, err := repo.EnqueueChatJob(t.Context(), duplicate); err != nil || inserted {
		t.Fatal("recent completed inbox lost duplicate protection")
	}
	if _, err = repo.db.ExecContext(t.Context(), `UPDATE chat_jobs SET run_at=? WHERE id=?`, now.Add(-31*24*time.Hour).UnixMilli(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.PurgeChatHistory(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM chat_jobs WHERE id=?`, job.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("old terminal job was not purged")
	}
}

func TestChatIdentitiesEndWithRevokedSessionsAndPasswordReset(t *testing.T) {
	repo, record := chatStoreFixture(t)
	viewer := repo.Bootstrap().Viewer
	bind := func() {
		t.Helper()
		identity := domain.ChatIdentity{UserID: viewer.ID, OpenID: "ou_viewer", TenantKey: record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
		if err := repo.PutChatIdentity(t.Context(), record.WorkspaceKey, record.ConnectionID, identity); err != nil {
			t.Fatal(err)
		}
		bound, err := repo.ChatIdentityForUser(t.Context(), record.WorkspaceKey, record.ConnectionID, viewer.ID)
		if err != nil || bound.AuthenticatedAt.IsZero() {
			t.Fatalf("consent time not persisted: %#v %v", bound, err)
		}
	}
	unbound := func(reason string) {
		t.Helper()
		if _, err := repo.ChatIdentityForUser(t.Context(), record.WorkspaceKey, record.ConnectionID, viewer.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("chat identity survived %s: %v", reason, err)
		}
	}
	bind()
	if err := repo.RevokeOtherSessions(t.Context(), viewer.ID, "current-session"); err != nil {
		t.Fatal(err)
	}
	unbound("signing out other sessions")
	bind()
	token, err := repo.RequestPasswordReset(t.Context(), viewer.Email)
	if err != nil || token == "" {
		t.Fatalf("reset token: %v", err)
	}
	if err = repo.ResetPassword(t.Context(), token, "a-new-test-password"); err != nil {
		t.Fatal(err)
	}
	unbound("a password reset")
}
