package store

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestWorkspaceRenameCoversEverySchemaTable fails when a table keyed by the
// workspace URL key is added without saying how a rename treats it: it must
// be moved (workspaceRenameTables, derived from workspacePurgeTables) or
// skipped with a reason. Tables keyed by the workspace ID need nothing: the
// ID does not change (TestWorkspacePurgeCoversEverySchemaTable lists them).
func TestWorkspaceRenameCoversEverySchemaTable(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	schema := sqliteSchema(t, repo)
	moved := map[string]bool{}
	for _, table := range workspaceRenameTables() {
		moved[table] = true
		if !hasColumn(schema[table], "workspace_key") {
			t.Errorf("workspaceRenameTables lists %s, which has no workspace_key column", table)
		}
	}
	for table, columns := range schema {
		if !hasColumn(columns, "workspace_key") {
			continue
		}
		if moved[table] == (workspaceRenameSkipped[table] != "") {
			t.Errorf("table %s has a workspace_key column: a rename must move it (add it to workspacePurgeTables) or skip it with a reason in workspaceRenameSkipped", table)
		}
	}
	for table := range workspaceRenameSkipped {
		if _, ok := schema[table]; !ok {
			t.Errorf("workspaceRenameSkipped lists %s, which the schema does not have", table)
		}
	}
	for table := range workspaceRenameContentTables {
		if !moved[table] {
			t.Errorf("workspaceRenameContentTables lists %s, which a rename does not move", table)
		}
	}
	for _, table := range workspaceKeyJSONTables {
		if _, ok := schema[table]; !ok || hasColumn(schema[table], "workspace_key") {
			t.Errorf("workspaceKeyJSONTables lists %s: it must exist and have no workspace_key column", table)
		}
	}
}

// The generic rename builders never write a metadata table: those writes
// must stay literal statements the metadata revision scan can check.
func TestWorkspaceRenameLeavesMetadataTablesToLiteralStatements(t *testing.T) {
	for _, table := range workspaceRenameTables() {
		if metadataTables[table] {
			t.Errorf("workspaceRenameTables includes the metadata table %s", table)
		}
	}
}

type recordingKeyObjects struct {
	mu      sync.Mutex
	copies  []WorkspaceKeyMove
	deletes []string
}

func (r *recordingKeyObjects) CopyWorkspaceKeyObjects(_ context.Context, move WorkspaceKeyMove) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.copies = append(r.copies, move)
	return nil
}

func (r *recordingKeyObjects) DeleteWorkspaceKeyObjects(_ context.Context, key string, sessions []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deletes = append(r.deletes, key+":"+strings.Join(sessions, ","))
	return nil
}

// A rename has the server copy the harness state of the workspace's coding
// sessions to the new key before it commits, and delete the old copies after.
func TestWorkspaceRenameMovesKeyNamedObjects(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	objects := &recordingKeyObjects{}
	repo.SetWorkspaceKeyObjects(objects)
	schema := sqliteSchema(t, repo)
	insertSyntheticRow(t, repo, "coding_sessions", schema["coding_sessions"], "workspace_key", "test-workspace", "", 1)
	sessions, err := repo.workspaceStrings(ctx, `SELECT id FROM coding_sessions WHERE workspace_key='test-workspace'`)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %v (%v)", sessions, err)
	}
	data, _ := repo.BootstrapFor("test-workspace")
	workspace := data.Workspace
	workspace.URLKey = "objects-moved"
	if _, err := repo.UpdateWorkspace(ctx, "test-workspace", workspace); err != nil {
		t.Fatal(err)
	}
	if len(objects.copies) != 1 || objects.copies[0].OldKey != "test-workspace" || objects.copies[0].NewKey != "objects-moved" || !slices.Equal(objects.copies[0].CodingSessionIDs, sessions) {
		t.Fatalf("copies = %#v", objects.copies)
	}
	if !slices.Equal(objects.deletes, []string{"test-workspace:" + sessions[0]}) {
		t.Fatalf("deletes = %v", objects.deletes)
	}
	var renames int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_key_renames WHERE workspace_id=? AND old_key='test-workspace' AND new_key='objects-moved'`, data.Workspace.ID).Scan(&renames); err != nil || renames != 1 {
		t.Fatalf("rename history rows = %d (%v)", renames, err)
	}
}

// Rows written under the previous key after a rename (by work that held the
// old key) are moved by AdoptRenamedWorkspaceRows; a row the current key
// already has wins, and a previous key another workspace also had is left.
func TestAdoptRenamedWorkspaceRows(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data, _ := repo.BootstrapFor("test-workspace")
	workspace := data.Workspace
	workspace.URLKey = "adopting"
	if _, err := repo.UpdateWorkspace(ctx, "test-workspace", workspace); err != nil {
		t.Fatal(err)
	}
	schema := sqliteSchema(t, repo)
	// Late writes under the old key; loop_run_migrations is keyed by the
	// workspace alone, so the current key's row conflicts.
	insertSyntheticRow(t, repo, "agent_run_activities", schema["agent_run_activities"], "workspace_key", "test-workspace", "", 1)
	insertSyntheticRow(t, repo, "loop_run_migrations", schema["loop_run_migrations"], "workspace_key", "adopting", "", 2)
	insertSyntheticRow(t, repo, "loop_run_migrations", schema["loop_run_migrations"], "workspace_key", "test-workspace", "", 3)
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO oauth_authorization_codes(code_hash,data,expires_at,created_at) VALUES('code_late',?,'2999-01-01','2026-01-01')`, []byte(`{"workspaceKey":"test-workspace"}`)); err != nil {
		t.Fatal(err)
	}
	report, err := repo.AdoptRenamedWorkspaceRows(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if report.Moved < 1 || report.Ambiguous != 0 {
		t.Fatalf("report = %+v", report)
	}
	if n := countWhere(t, repo, "agent_run_activities", "workspace_key", "adopting"); n != 1 {
		t.Fatalf("late activity under the current key: %d", n)
	}
	var migrated int
	if err := repo.db.QueryRowContext(ctx, `SELECT migrated_runs FROM loop_run_migrations WHERE workspace_key='adopting'`).Scan(&migrated); err != nil || migrated != 2 {
		t.Fatalf("the current key's row did not win: %d (%v)", migrated, err)
	}
	if table, err := repo.workspaceKeyInUse(ctx, "test-workspace"); err != nil || table != "" {
		t.Fatalf("old key still holds rows in %q (%v)", table, err)
	}
	var code int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM oauth_authorization_codes WHERE code_hash='code_late' AND instr(data,'"workspaceKey":"adopting"')>0`).Scan(&code); err != nil || code != 1 {
		t.Fatalf("late OAuth code not moved: %d (%v)", code, err)
	}

	// Another workspace had the key too: its rows are left alone.
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO workspace_key_renames(workspace_id,old_key,new_key,renamed_at) VALUES('ws_other','test-workspace','',?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	insertSyntheticRow(t, repo, "agent_run_activities", schema["agent_run_activities"], "workspace_key", "test-workspace", "", 4)
	report, err = repo.AdoptRenamedWorkspaceRows(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ambiguous != 2 || report.Moved != 0 {
		t.Fatalf("ambiguous report = %+v", report)
	}
	if n := countWhere(t, repo, "agent_run_activities", "workspace_key", "test-workspace"); n != 1 {
		t.Fatalf("an ambiguous key's row moved: %d left", n)
	}
}

// Migration 1040 repairs renames made before renames moved everything: it
// finds the previous key in the domain events, moves its rows, and copies
// back offloaded bodies the workspace refers to from a key no workspace has.
func TestWorkspaceKeyRepairMigration(t *testing.T) {
	ctx := context.Background()
	config := DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "flow.db"), FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1, AgentOffloadBytes: 4096}
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { repo.Close() }()
	data, _ := repo.BootstrapFor("test-workspace")
	now := time.Now().UTC().Truncate(time.Second)
	reply := strings.Repeat("Recovered after an old rename. ", 300)
	session := domain.AgentSession{ID: "agent_session_repair", SlugID: "repair", UserID: "usr_admin", Title: "Chat", Location: "agent", IssueIDs: []string{}, SkillIDs: []string{}, CreatedAt: now, UpdatedAt: now, Messages: []domain.AgentMessage{
		{ID: "m1", Role: "user", Content: "Hi", CreatedAt: now},
		{ID: "m2", Role: "assistant", Content: reply, CreatedAt: now},
	}}
	if err := repo.MutateWorkspace(ctx, "test-workspace", "agent.session_created", session.ID, nil, func(next *domain.Bootstrap) error {
		next.AgentSessions = append(next.AgentSessions, session)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// What an old rename from "legacy-key" left: rows under the old key,
	// the event that named it, and the offloaded body (moved to a key no
	// event names, as after two renames whose events were pruned).
	schema := sqliteSchema(t, repo)
	for index, table := range []string{"coding_sessions", "loop_run_records", "agent_session_states", "metadata_search_documents"} {
		insertSyntheticRow(t, repo, table, schema[table], "workspace_key", "legacy-key", "", 100+index)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at,workspace_id) VALUES('evt_legacy','workspace.created',?,?,?,?)`, []any{data.Workspace.ID, []byte(`{"urlKey":"legacy-key"}`), "2026-01-01T00:00:00Z", data.Workspace.ID}},
		{`UPDATE metadata_blobs SET workspace_key='unnamed-key' WHERE workspace_key='test-workspace'`, nil},
		{`DELETE FROM schema_migrations WHERE version=?`, []any{workspaceKeyRepairMigrationVersion}},
	} {
		if _, err := repo.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("%s: %v", statement.query, err)
		}
	}
	if err := repo.migrateRenamedWorkspaceKeys(ctx); err != nil {
		t.Fatal(err)
	}
	if table, err := repo.workspaceKeyInUse(ctx, "legacy-key"); err != nil || table != "" {
		t.Fatalf("legacy key still holds rows in %q (%v)", table, err)
	}
	for _, table := range []string{"coding_sessions", "loop_run_records", "agent_session_states", "metadata_search_documents"} {
		if countWhere(t, repo, table, "workspace_key", "test-workspace") == 0 {
			t.Errorf("%s row not moved to the current key", table)
		}
	}
	// Idempotent.
	if err := repo.migrateRenamedWorkspaceKeys(ctx); err != nil {
		t.Fatal(err)
	}
	// Reopened, the conversation holds a reference; its body loads again.
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if repo, err = OpenDatabase(config); err != nil {
		t.Fatal(err)
	}
	loaded, _ := repo.WorkspaceMetadataFields("test-workspace", "agentSessions")
	found := false
	for _, item := range loaded.AgentSessions {
		if item.ID != session.ID {
			continue
		}
		found = true
		if !AgentSessionHasOffloaded(item) {
			t.Fatal("the reply was not offloaded")
		}
		full, err := repo.LoadAgentSessionBodies(ctx, "test-workspace", item)
		if err != nil || full.Messages[1].Content != reply {
			t.Fatalf("body after the repair: %.60q (%v)", full.Messages[1].Content, err)
		}
	}
	if !found {
		t.Fatal("conversation missing")
	}
}

// The offloaded content sweep copies back bodies a workspace refers to that
// only a key no workspace has holds, before that key's bodies are swept.
func TestOffloadedContentSweepRecoversBodiesUnderAPreviousKey(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "flow.db"), FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1, AgentOffloadBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Now().UTC().Truncate(time.Second)
	session := domain.AgentSession{ID: "agent_session_sweep", SlugID: "sweep", UserID: "usr_admin", Title: "Chat", Location: "agent", IssueIDs: []string{}, SkillIDs: []string{}, CreatedAt: now, UpdatedAt: now, Messages: []domain.AgentMessage{
		{ID: "m1", Role: "assistant", Content: strings.Repeat("Swept but recovered. ", 400), CreatedAt: now},
	}}
	if err := repo.MutateWorkspace(ctx, "test-workspace", "agent.session_created", session.ID, nil, func(next *domain.Bootstrap) error {
		next.AgentSessions = append(next.AgentSessions, session)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// As a rename before renames moved bodies left them (the sweep looks at
	// renamed workspaces even when they hold no bodies).
	data, _ := repo.BootstrapFor("test-workspace")
	for _, statement := range []string{
		`UPDATE metadata_blobs SET workspace_key='renamed-away' WHERE workspace_key='test-workspace'`,
		`INSERT INTO workspace_key_renames(workspace_id,old_key,new_key,renamed_at) VALUES('` + data.Workspace.ID + `','renamed-away','test-workspace','2026-01-01T00:00:00Z')`,
	} {
		if _, err := repo.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if _, err := repo.SweepOffloadedContent(ctx, time.Now().Add(48*time.Hour), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if n := countWhere(t, repo, "metadata_blobs", "workspace_key", "test-workspace"); n == 0 {
		t.Fatal("the body was not recovered for the workspace that refers to it")
	}
}

// A workspace whose purge started keeps the key its receipt names.
func TestWorkspaceRenameRefusedOncePurgeStarted(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, err := repo.ScheduleWorkspaceDeletion(ctx, "test-workspace"); err != nil {
		t.Fatal(err)
	}
	if _, started, err := repo.BeginWorkspacePurge(ctx, "test-workspace", time.Now().Add(time.Hour)); err != nil || !started {
		t.Fatalf("begin purge: %v %v", started, err)
	}
	data, _ := repo.BootstrapFor("test-workspace")
	workspace := data.Workspace
	workspace.URLKey = "escaping-purge"
	if _, err := repo.UpdateWorkspace(ctx, "test-workspace", workspace); err != ErrWorkspacePurging {
		t.Fatalf("rename during a purge: %v, want ErrWorkspacePurging", err)
	}
}
