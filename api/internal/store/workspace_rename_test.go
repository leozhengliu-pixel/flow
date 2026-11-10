package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// credentialIDForTest is the server's connector credential id formula,
// spelled out so the test does not depend on the store's copy of it.
func credentialIDForTest(workspace, owner, id string) string {
	return fmt.Sprintf("credential_%x", sha256.Sum256([]byte(workspace+"\x00"+owner+"\x00"+id)))
}

// renameFixture opens a store whose offloaded bodies live in object storage
// and gives the fixture workspace a long agent reply (an offloaded body), an
// integration connection with a stored credential, an app developer token
// and an OAuth refresh grant.
func renameFixture(t *testing.T) (*SQLiteStore, *memoryObjects, domain.AgentSession, string) {
	t.Helper()
	ctx := context.Background()
	objects := &memoryObjects{objects: map[string][]byte{}}
	config := DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "flow.db"), FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1, ContentStore: objects, AgentOffloadBytes: 4096}
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	reply := strings.Repeat("The rename keeps this answer. ", 400)
	session := domain.AgentSession{ID: "agent_session_rename", SlugID: "rename", UserID: "usr_admin", Title: "Chat", Location: "agent", IssueIDs: []string{}, SkillIDs: []string{}, CreatedAt: now, UpdatedAt: now, Messages: []domain.AgentMessage{
		{ID: "message_user", Role: "user", Content: "Summarise", CreatedAt: now},
		{ID: "message_reply", Role: "assistant", Content: reply, CreatedAt: now},
	}}
	if err := repo.MutateWorkspace(ctx, "test-workspace", "agent.session_created", session.ID, nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, session)
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "integration_rename", Provider: "gitlab", Name: "GitLab", Status: "connected"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(objects.objects) == 0 {
		t.Fatal("the reply was not offloaded to object storage")
	}
	// Credentials are sealed with their id as associated data.
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err := repo.PutConnectorSecret(ctx, credentialIDForTest("test-workspace", "provider", "integration_rename"), []byte(`{"token":"provider"}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO webhook_signing_secrets(ref,workspace_key,secret,created_at) VALUES(?,?,?,?)`, []any{"devtoken:test-workspace:client_rename", "test-workspace", "sealed", 1}},
		{`INSERT INTO oauth_refresh_tokens(token_hash,data,expires_at,created_at) VALUES(?,?,?,?)`, []any{"rt_rename", []byte(`{"workspaceKey":"test-workspace","userId":"usr_admin"}`), "2999-01-01", "2026-01-01"}},
		{`INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at) VALUES(?,?,?,?,?,1,?,?)`, []any{"task_rename", "test-workspace", "issue", "app", "pending", []byte(`{"id":"task_rename","workspaceKey":"test-workspace"}`), "2026-01-01"}},
	} {
		if _, err := repo.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("%s: %v", statement.query, err)
		}
	}
	// Reopen: a loaded workspace holds references, not the offloaded bodies.
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if repo, err = OpenDatabase(config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo, objects, session, reply
}

// TestWorkspaceRenameMovesEveryKeyedRowAndObject renames a workspace that
// has a row in every table keyed by the workspace URL key, an offloaded
// body, a credential, a developer token and an OAuth grant, and checks that
// all of it is found under the new key and nothing is left under the old.
func TestWorkspaceRenameMovesEveryKeyedRowAndObject(t *testing.T) {
	ctx := context.Background()
	repo, objects, session, reply := renameFixture(t)
	const oldKey, newKey, otherKey = "test-workspace", "renamed-workspace", "rename-other"
	schema := sqliteSchema(t, repo)
	var userID string
	if err := repo.db.QueryRowContext(ctx, `SELECT id FROM auth_users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	keyed := []string{}
	for index, table := range workspacePurgeTables {
		if table.byID || table.table == "workspace_states" {
			continue
		}
		keyed = append(keyed, table.table)
		for ownerIndex, owner := range []string{oldKey, otherKey} {
			insertSyntheticRow(t, repo, table.table, schema[table.table], "workspace_key", owner, userID, 7000+index*2+ownerIndex)
		}
	}
	// A chat job names its workspace in its payload.
	if _, err := repo.db.ExecContext(ctx, `UPDATE chat_jobs SET payload=? WHERE workspace_key=?`, []byte(`{"workspace":"`+oldKey+`","kind":"dm"}`), oldKey); err != nil {
		t.Fatal(err)
	}
	data, _ := repo.BootstrapFor(oldKey)
	workspace := data.Workspace
	workspace.URLKey = newKey
	if _, err := repo.UpdateWorkspace(ctx, oldKey, workspace); err != nil {
		t.Fatal(err)
	}
	for _, table := range keyed {
		if got := countWhere(t, repo, table, "workspace_key", oldKey); got != 0 {
			t.Errorf("%s keeps %d rows under the old key", table, got)
		}
		if got := countWhere(t, repo, table, "workspace_key", newKey); got == 0 {
			t.Errorf("%s has no row under the new key", table)
		}
		if got := countWhere(t, repo, table, "workspace_key", otherKey); got == 0 {
			t.Errorf("%s lost another workspace's row", table)
		}
	}
	// The offloaded reply reads under the new key, from objects named after it.
	loaded, _ := repo.WorkspaceMetadataFields(newKey, "agentSessions")
	var stored domain.AgentSession
	for _, item := range loaded.AgentSessions {
		if item.ID == session.ID {
			stored = item
		}
	}
	full, err := repo.LoadAgentSessionBodies(ctx, newKey, stored)
	if err != nil {
		t.Fatal(err)
	}
	if full.Messages[1].Content != reply {
		t.Fatalf("offloaded reply after the rename = %.80q", full.Messages[1].Content)
	}
	for key := range objects.objects {
		if strings.HasPrefix(key, oldKey+"/") {
			t.Errorf("object %s is still named after the old key", key)
		}
	}
	// Credentials, references and JSON naming the workspace.
	for name, check := range map[string][2]string{
		"developer token":       {`SELECT COUNT(*) FROM webhook_signing_secrets WHERE ref=?`, "devtoken:" + newKey + ":client_rename"},
		"oauth refresh grant":   {`SELECT COUNT(*) FROM oauth_refresh_tokens WHERE token_hash='rt_rename' AND instr(data,?)>0`, `"workspaceKey":"` + newKey + `"`},
		"application agent job": {`SELECT COUNT(*) FROM application_agent_tasks WHERE id='task_rename' AND instr(data,?)>0`, `"workspaceKey":"` + newKey + `"`},
		"account state":         {`SELECT COUNT(*) FROM account_state WHERE last_workspace_key=?`, newKey},
		"chat job payload":      {`SELECT COUNT(*) FROM chat_jobs WHERE instr(payload,?)>0`, `"workspace":"` + newKey + `"`},
	} {
		var count int
		if err := repo.db.QueryRowContext(ctx, check[0], check[1]).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("%s: %d rows under the new key, want 1", name, count)
		}
	}
	if secret, err := repo.ReadConnectorSecret(ctx, credentialIDForTest(newKey, "provider", "integration_rename"), false); err != nil || string(secret) != `{"token":"provider"}` {
		t.Errorf("provider credential under the new key: %q (%v)", secret, err)
	}
	var stale int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM connector_secrets WHERE id=?`, credentialIDForTest(oldKey, "provider", "integration_rename")).Scan(&stale); err != nil || stale != 0 {
		t.Errorf("credential under the old key: %d (%v)", stale, err)
	}
	// The old key is free: a new workspace can take it and starts empty.
	created, err := repo.CreateWorkspace(ctx, "Reused key", oldKey, "us")
	if err != nil {
		t.Fatalf("create a workspace with the previous key: %v", err)
	}
	if len(created.AgentSessions) != 0 {
		t.Fatalf("the new workspace shows %d conversations of the renamed one", len(created.AgentSessions))
	}
}

// A rename to a key that still holds rows (here: a deleted workspace's that
// were never purged) is refused, so they do not appear in this workspace.
func TestWorkspaceRenameRefusesAKeyThatStillHoldsRows(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	schema := sqliteSchema(t, repo)
	insertSyntheticRow(t, repo, "loop_run_records", schema["loop_run_records"], "workspace_key", "left-behind", "", 1)
	data, _ := repo.BootstrapFor("test-workspace")
	workspace := data.Workspace
	workspace.URLKey = "left-behind"
	if _, err := repo.UpdateWorkspace(ctx, "test-workspace", workspace); err != ErrWorkspaceKeyTaken {
		t.Fatalf("rename onto a key with rows: %v, want ErrWorkspaceKeyTaken", err)
	}
	if _, err := repo.CreateWorkspace(ctx, "Left behind", "left-behind", "us"); err != ErrWorkspaceKeyTaken {
		t.Fatalf("create on a key with rows: %v, want ErrWorkspaceKeyTaken", err)
	}
	if _, ok := repo.BootstrapFor("test-workspace"); !ok {
		t.Fatal("the refused rename changed the catalog")
	}
}

// Renaming back to the original key brings everything back with it.
func TestWorkspaceRenameRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _, session, reply := renameFixture(t)
	data, _ := repo.BootstrapFor("test-workspace")
	for _, step := range [][2]string{{"test-workspace", "round-trip"}, {"round-trip", "test-workspace"}} {
		workspace := data.Workspace
		workspace.URLKey = step[1]
		updated, err := repo.UpdateWorkspace(ctx, step[0], workspace)
		if err != nil {
			t.Fatalf("rename %s → %s: %v", step[0], step[1], err)
		}
		data = updated
	}
	loaded, _ := repo.WorkspaceMetadataFields("test-workspace", "agentSessions")
	for _, item := range loaded.AgentSessions {
		if item.ID != session.ID {
			continue
		}
		full, err := repo.LoadAgentSessionBodies(ctx, "test-workspace", item)
		if err != nil || full.Messages[1].Content != reply {
			t.Fatalf("reply after renaming back: %.60q (%v)", full.Messages[1].Content, err)
		}
	}
	var leftover int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_blobs WHERE workspace_key='round-trip'`).Scan(&leftover); err != nil || leftover != 0 {
		t.Fatalf("bodies left under the intermediate key: %d (%v)", leftover, err)
	}
	raw, _ := json.Marshal(data.Workspace)
	if !strings.Contains(string(raw), `"urlKey":"test-workspace"`) {
		t.Fatalf("workspace after the round trip: %s", raw)
	}
}
