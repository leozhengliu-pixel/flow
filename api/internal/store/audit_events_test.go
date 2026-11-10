package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func openAuditTestStore(t *testing.T) (*SQLiteStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo, path
}

func auditTestWorkspaceID(t *testing.T, repo *SQLiteStore) string {
	t.Helper()
	data, ok := repo.storedWorkspace(accessWorkspace)
	if !ok {
		t.Fatal("test workspace missing")
	}
	return data.Workspace.ID
}

func stageAudit(data *domain.Bootstrap, action string, metadata map[string]any) {
	data.AuditLog = append([]domain.AuditLogEntry{{ID: idgen.New("audit_"), Actor: data.Viewer, Action: action, ResourceType: "test", ResourceID: "res", Metadata: metadata, CreatedAt: time.Now().UTC()}}, data.AuditLog...)
}

// Entries a mutation stages are committed with it to audit_events (in order,
// chained) and never to the workspace document; a failed or empty mutation
// records nothing.
func TestAuditEntriesCommitWithTheirMutation(t *testing.T) {
	ctx := context.Background()
	repo, _ := openAuditTestStore(t)
	workspaceID := auditTestWorkspaceID(t, repo)
	request := AuditRequest{IP: "203.0.113.0", UserAgent: "Chrome on macOS", UserID: "", ActorType: AuditActorAPIKey, ActorID: "api_key_1"}
	if err := repo.MutateWorkspace(WithAuditRequest(ctx, request), accessWorkspace, "test.audited", "res", nil, func(data *domain.Bootstrap) error {
		data.Workspace.Name = "Audited"
		stageAudit(data, "first", map[string]any{"password": "hunter2", "nested": map[string]any{"token": "x", "kept": 1}})
		stageAudit(data, "second", nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(ctx, accessWorkspace, "test.failed", "res", nil, func(data *domain.Bootstrap) error {
		stageAudit(data, "failed", nil)
		return errors.New("refused")
	}); err == nil {
		t.Fatal("expected the failing mutation to fail")
	}
	if err := repo.MutateWorkspace(ctx, accessWorkspace, "test.noop", "res", nil, func(data *domain.Bootstrap) error {
		stageAudit(data, "noop", nil)
		return ErrNoMutation
	}); err != nil {
		t.Fatal(err)
	}
	// An audit-only mutation (nothing else changes) still records its entry.
	if err := repo.MutateWorkspace(WithMetadataFields(ctx, "auditLog"), accessWorkspace, "audit.workspace_member", "res", nil, func(data *domain.Bootstrap) error {
		stageAudit(data, "third", nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stored, _ := repo.storedWorkspace(accessWorkspace); len(stored.AuditLog) != 0 {
		t.Fatalf("snapshot keeps %d audit entries", len(stored.AuditLog))
	}
	entries, err := repo.ListAuditEntries(ctx, workspaceID, AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	actions := []string{}
	for _, entry := range entries {
		actions = append(actions, entry.Action)
	}
	if len(entries) != 3 || entries[0].Action != "third" || entries[2].Action != "first" {
		t.Fatalf("entries = %v", actions)
	}
	first := entries[2]
	if first.Seq != 1 || entries[1].Seq != 2 || entries[0].Seq != 3 || first.Hash == "" {
		t.Fatalf("seq/hash = %d %d %d %q", first.Seq, entries[1].Seq, entries[0].Seq, first.Hash)
	}
	if _, ok := first.Metadata["password"]; ok {
		t.Fatalf("secret kept: %v", first.Metadata)
	}
	if nested, _ := first.Metadata["nested"].(map[string]any); nested["token"] != nil || nested["kept"] == nil {
		t.Fatalf("nested metadata = %v", first.Metadata["nested"])
	}
	if first.IP != "203.0.113.0" || first.UserAgent != "Chrome on macOS" || first.Result != AuditResultSuccess {
		t.Fatalf("request details = %+v", first)
	}
	if entries[0].IP != "" || entries[0].ActorType != AuditActorUser {
		t.Fatalf("entry outside the request = %+v", entries[0])
	}
	report, err := repo.VerifyAuditChain(ctx, workspaceID)
	if err != nil || !report.Valid || report.Entries != 3 || report.LastSeq != 3 {
		t.Fatalf("verify = %+v, %v", report, err)
	}
	// Reopening keeps nothing in the document.
	data, _ := repo.WorkspaceMetadata(accessWorkspace)
	if len(data.AuditLog) != 0 {
		t.Fatalf("workspace document holds %d audit entries", len(data.AuditLog))
	}
}

func appendTestAudit(t *testing.T, repo *SQLiteStore, workspaceID string, at time.Time, actions ...string) {
	t.Helper()
	for _, action := range actions {
		if err := repo.AppendAuditEntries(context.Background(), workspaceID, domain.AuditLogEntry{Action: action, ResourceType: "test", ResourceID: action, CreatedAt: at, Actor: domain.User{ID: "usr_1", Email: "ada@example.com"}}); err != nil {
			t.Fatal(err)
		}
	}
}

// Changing, removing or inserting a row breaks the chain at that row.
func TestAuditChainVerifyDetectsTampering(t *testing.T) {
	ctx := context.Background()
	repo, _ := openAuditTestStore(t)
	workspaceID := auditTestWorkspaceID(t, repo)
	appendTestAudit(t, repo, workspaceID, time.Now().UTC(), "a", "b", "c", "d")
	if report, err := repo.VerifyAuditChain(ctx, workspaceID); err != nil || !report.Valid || report.Entries != 4 {
		t.Fatalf("verify = %+v, %v", report, err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE audit_events SET metadata=? WHERE workspace_id=? AND seq=2`, `{"forged":true}`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if report, _ := repo.VerifyAuditChain(ctx, workspaceID); report.Valid || report.BrokenAt != 2 {
		t.Fatalf("edited row: %+v", report)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM audit_events WHERE workspace_id=? AND seq=2`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if report, _ := repo.VerifyAuditChain(ctx, workspaceID); report.Valid || report.BrokenAt != 2 {
		t.Fatalf("removed row: %+v", report)
	}
	other, _ := openAuditTestStore(t)
	otherID := auditTestWorkspaceID(t, other)
	appendTestAudit(t, other, otherID, time.Now().UTC(), "a", "b")
	if _, err := other.db.ExecContext(ctx, `DELETE FROM audit_events WHERE workspace_id=? AND seq=2`, otherID); err != nil {
		t.Fatal(err)
	}
	if report, _ := other.VerifyAuditChain(ctx, otherID); report.Valid || report.BrokenAt != 2 {
		t.Fatalf("truncated tail: %+v", report)
	}
}

// Retention removes only the oldest entries, a prefix of the chain, and the
// remaining chain still verifies.
func TestAuditPruneKeepsTheChainVerifiable(t *testing.T) {
	ctx := context.Background()
	repo, _ := openAuditTestStore(t)
	workspaceID := auditTestWorkspaceID(t, repo)
	old := time.Now().UTC().AddDate(0, 0, -200)
	appendTestAudit(t, repo, workspaceID, old, "old1", "old2")
	appendTestAudit(t, repo, workspaceID, time.Now().UTC(), "new1")
	// An old timestamp after a newer entry stays: only a prefix is removed.
	appendTestAudit(t, repo, workspaceID, old, "late")
	cutoff := time.Now().UTC().AddDate(0, 0, -180)
	deleted, err := repo.PruneAuditEvents(ctx, workspaceID, cutoff)
	if err != nil || deleted != 2 {
		t.Fatalf("pruned %d, %v", deleted, err)
	}
	if again, err := repo.PruneAuditEvents(ctx, workspaceID, cutoff); err != nil || again != 0 {
		t.Fatalf("second prune %d, %v", again, err)
	}
	entries, _ := repo.ListAuditEntries(ctx, workspaceID, AuditQuery{})
	if len(entries) != 2 {
		t.Fatalf("kept %d entries", len(entries))
	}
	report, err := repo.VerifyAuditChain(ctx, workspaceID)
	if err != nil || !report.Valid || report.PrunedThrough != 2 || report.FirstSeq != 3 || report.Entries != 2 {
		t.Fatalf("verify after prune = %+v, %v", report, err)
	}
	appendTestAudit(t, repo, workspaceID, time.Now().UTC(), "after")
	if report, _ := repo.VerifyAuditChain(ctx, workspaceID); !report.Valid || report.LastSeq != 5 {
		t.Fatalf("verify after append = %+v", report)
	}
}

// Entries the workspace document kept move into the table once, oldest
// first, with their client address; the document keeps none.
func TestLegacyAuditLogMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	legacy := []domain.AuditLogEntry{
		{ID: "audit_newer", Action: "updated", ResourceType: "project", ResourceID: "p1", CreatedAt: base.Add(time.Hour), Actor: domain.User{ID: "usr_a"}, Metadata: map[string]any{"ip": "198.51.100.7"}},
		{ID: "audit_older", Action: "created", ResourceType: "project", ResourceID: "p1", CreatedAt: base, Actor: domain.User{ID: "usr_a"}},
	}
	writeLegacy := func(repo *SQLiteStore, entries []domain.AuditLogEntry) {
		t.Helper()
		repo.writeMu.Lock()
		defer repo.writeMu.Unlock()
		before, _ := repo.storedWorkspace(accessWorkspace)
		after := before
		after.AuditLog = entries
		tx, err := repo.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := repo.persistMetadataTx(ctx, tx, accessWorkspace, before, &after); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		repo.installWorkspace(accessWorkspace, after, false)
	}
	writeLegacy(repo, legacy)
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, auditLegacyMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	workspaceID := auditTestWorkspaceID(t, repo)
	entries, err := repo.ListAuditEntries(ctx, workspaceID, AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].ID != "audit_older" || entries[1].Seq != 1 || entries[0].ID != "audit_newer" || entries[0].IP != "198.51.100.7" {
		t.Fatalf("migrated = %+v", entries)
	}
	if stored, _ := repo.storedWorkspace(accessWorkspace); len(stored.AuditLog) != 0 {
		t.Fatalf("document still holds %d entries", len(stored.AuditLog))
	}
	// An older server writing to the document again during an upgrade: the
	// next pass moves only what is new.
	writeLegacy(repo, append([]domain.AuditLogEntry{{ID: "audit_late", Action: "deleted", ResourceType: "project", ResourceID: "p1", CreatedAt: base.Add(2 * time.Hour)}}, legacy...))
	if err := repo.MigrateLegacyAuditLog(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.MigrateLegacyAuditLog(ctx); err != nil {
		t.Fatal(err)
	}
	entries, _ = repo.ListAuditEntries(ctx, workspaceID, AuditQuery{})
	if len(entries) != 3 || entries[0].ID != "audit_late" {
		t.Fatalf("after rerun = %+v", entries)
	}
	if report, _ := repo.VerifyAuditChain(ctx, workspaceID); !report.Valid {
		t.Fatalf("verify = %+v", report)
	}
	reopened, _ := repo.WorkspaceMetadata(accessWorkspace)
	if len(reopened.AuditLog) != 0 {
		t.Fatalf("document holds %d entries", len(reopened.AuditLog))
	}
}

// Account events go to every workspace of the account; an email no account
// has goes to the instance log.
func TestRecordAccountAudit(t *testing.T) {
	ctx := context.Background()
	repo, _ := openAuditTestStore(t)
	workspaceID := auditTestWorkspaceID(t, repo)
	data, _ := repo.storedWorkspace(accessWorkspace)
	user := data.Users[0]
	if err := repo.RecordAccountAudit(ctx, "", user.Email, domain.AuditLogEntry{Action: "sign_in_failed", ResourceType: "account", Result: AuditResultFailure}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordAccountAudit(ctx, "", "nobody@example.com", domain.AuditLogEntry{Action: "sign_in_failed", ResourceType: "account", Result: AuditResultFailure, Metadata: map[string]any{"email": "nobody@example.com"}}); err != nil {
		t.Fatal(err)
	}
	entries, _ := repo.ListAuditEntries(ctx, workspaceID, AuditQuery{})
	if len(entries) != 1 || entries[0].Actor.ID != user.ID || entries[0].ResourceID != user.ID || entries[0].Result != AuditResultFailure {
		t.Fatalf("workspace entries = %+v", entries)
	}
	instance, _ := repo.ListAuditEntries(ctx, InstanceAuditWorkspace, AuditQuery{})
	if len(instance) != 1 || instance[0].Metadata["email"] != "nobody@example.com" {
		t.Fatalf("instance entries = %+v", instance)
	}
	workspaces, _ := repo.AuditWorkspaces(ctx)
	if len(workspaces) != 2 || workspaces[0] != InstanceAuditWorkspace {
		t.Fatalf("audit workspaces = %v", workspaces)
	}
}

// The list filters and the stream cursor (seq, or the time/id cursor of a
// stream created before entries were numbered).
func TestAuditQueriesAndStreamCursor(t *testing.T) {
	ctx := context.Background()
	repo, _ := openAuditTestStore(t)
	workspaceID := auditTestWorkspaceID(t, repo)
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	appendTestAudit(t, repo, workspaceID, early, "a")
	appendTestAudit(t, repo, workspaceID, early.Add(time.Hour), "b", "c")
	if err := repo.AppendAuditEntries(ctx, workspaceID, domain.AuditLogEntry{Action: "keyed", ResourceType: "api_key", ResourceID: "k1", ActorType: AuditActorAPIKey, ActorID: "api_key_9", Actor: domain.User{ID: "usr_2"}, IP: "192.0.2.0", CreatedAt: early.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	check := func(name string, query AuditQuery, want int) {
		t.Helper()
		entries, err := repo.ListAuditEntries(ctx, workspaceID, query)
		if err != nil || len(entries) != want {
			t.Fatalf("%s: %d entries, %v", name, len(entries), err)
		}
	}
	check("all", AuditQuery{}, 4)
	check("since", AuditQuery{Since: early.Add(30 * time.Minute)}, 3)
	check("until", AuditQuery{Until: early.Add(30 * time.Minute)}, 1)
	check("resource type", AuditQuery{ResourceTypes: []string{"api_key"}}, 1)
	check("credential", AuditQuery{ActorIDs: []string{"api_key_9"}}, 1)
	check("user", AuditQuery{ActorIDs: []string{"usr_2"}}, 1)
	check("ip", AuditQuery{IP: "192.0.2.0"}, 1)
	check("limit", AuditQuery{Limit: 2}, 2)
	after, err := repo.AuditEntriesAfter(ctx, workspaceID, 2, time.Time{}, "", 10)
	if err != nil || len(after) != 2 || after[0].Seq != 3 {
		t.Fatalf("after seq = %+v, %v", after, err)
	}
	legacy, err := repo.AuditEntriesAfter(ctx, workspaceID, 0, early.Add(time.Hour), "~", 10)
	if err != nil || len(legacy) != 1 || legacy[0].Action != "keyed" {
		t.Fatalf("after legacy cursor = %+v, %v", legacy, err)
	}
	counts, err := repo.AuditEntryTypeCounts(ctx, workspaceID)
	if err != nil || counts[[2]string{"test", "b"}] != 1 || counts[[2]string{"api_key", "keyed"}] != 1 {
		t.Fatalf("counts = %v, %v", counts, err)
	}
}

// TestExternalDatabaseAuditEvents runs the audit log on PostgreSQL or MySQL
// (FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL): writes, the chain,
// retention and verification.
func TestExternalDatabaseAuditEvents(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	ctx := context.Background()
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	workspaceID := "audit_it_" + idgen.New("")
	old := time.Now().UTC().AddDate(0, 0, -400)
	appendTestAudit(t, repo, workspaceID, old, "old1", "old2")
	appendTestAudit(t, repo, workspaceID, time.Now().UTC(), "new1", "new2")
	if err := repo.AppendAuditEntries(ctx, workspaceID, domain.AuditLogEntry{Action: "unicode", ResourceType: "test", ResourceID: "r", Actor: domain.User{ID: "usr", DisplayName: "Zoë 张三"}, Metadata: map[string]any{"note": "nul\x00byte ✓"}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if report, err := repo.VerifyAuditChain(ctx, workspaceID); err != nil || !report.Valid || report.Entries != 5 {
		t.Fatalf("verify = %+v, %v", report, err)
	}
	if deleted, err := repo.PruneAuditEvents(ctx, workspaceID, time.Now().UTC().AddDate(0, 0, -180)); err != nil || deleted != 2 {
		t.Fatalf("prune = %d, %v", deleted, err)
	}
	if report, err := repo.VerifyAuditChain(ctx, workspaceID); err != nil || !report.Valid || report.Entries != 3 || report.PrunedThrough != 2 {
		t.Fatalf("verify after prune = %+v, %v", report, err)
	}
	entries, err := repo.ListAuditEntries(ctx, workspaceID, AuditQuery{Since: time.Now().UTC().Add(-time.Hour)})
	if err != nil || len(entries) != 3 || entries[0].Action != "unicode" || entries[0].Actor.DisplayName != "Zoë 张三" {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE audit_events SET action='forged' WHERE workspace_id=? AND seq=4`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if report, _ := repo.VerifyAuditChain(ctx, workspaceID); report.Valid || report.BrokenAt != 4 {
		t.Fatalf("tampered = %+v", report)
	}
	_, _ = repo.db.ExecContext(ctx, `DELETE FROM audit_events WHERE workspace_id=?`, workspaceID)
	_, _ = repo.db.ExecContext(ctx, `DELETE FROM audit_chain_heads WHERE workspace_id=?`, workspaceID)
}
