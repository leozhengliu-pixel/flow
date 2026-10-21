package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/codingenv"
	"flow/api/internal/domain"
)

// openCodingEnvironmentStores opens the SQLite fixture and, when
// FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL name a throwaway MySQL or
// PostgreSQL database, that database too (as the infrastructure CI job does).
func openCodingEnvironmentStores(t *testing.T) map[string]*SQLiteStore {
	t.Helper()
	stores := map[string]*SQLiteStore{}
	sqlite, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlite.Close() })
	stores["sqlite"] = sqlite
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		external, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { external.Close() })
		stores[driver] = external
	}
	return stores
}

func withCodingSecretKey(t *testing.T) {
	t.Helper()
	if err := SetCodingSecretKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		codingSecretKey.Lock()
		codingSecretKey.key = nil
		codingSecretKey.Unlock()
	})
}

func uniqueWorkspace(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func TestCodingEnvironmentCRUD(t *testing.T) {
	for name, repository := range openCodingEnvironmentStores(t) {
		t.Run(name, func(t *testing.T) { testCodingEnvironmentCRUD(t, repository) })
	}
}

func testCodingEnvironmentCRUD(t *testing.T, repository *SQLiteStore) {
	ctx := context.Background()
	workspace := uniqueWorkspace("env-crud")
	created, err := repository.CreateCodingEnvironment(ctx, domain.CodingEnvironment{
		WorkspaceKey: workspace, Repository: "https://github.com/Acme/Web.git", OwnerIDs: []string{"u1"}, Harness: "claudeCode", Model: "claude-x",
		Network: domain.CodingNetworkExtended, AllowedDomains: []string{"api.example.com"}, SandboxSize: domain.CodingSizeLarge,
		Mise:          domain.CodingMiseConfig{Tools: map[string]string{"node": "22"}, Files: []domain.CodingMiseFile{{Path: ".npmrc", Content: "x"}}, EnvironmentVariables: []domain.CodingEnvVar{{Name: "NODE_ENV", Value: "test"}}},
		PrepareScript: "npm ci", CustomGuidance: "Run npm test", DependencyManagement: domain.CodingDependenciesCustomOnly, CreatedBy: "u1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.ID) != 26 || created.Version != 1 || created.Repository != "Acme/Web" || created.HostName != "github.com" || created.Name != "Web" || created.CreatedBy != "u1" {
		t.Fatalf("created: %+v", created)
	}
	got, err := repository.CodingEnvironment(ctx, workspace, created.ID)
	if err != nil || got.Mise.Tools["node"] != "22" || got.Mise.Files[0].Path != ".npmrc" || got.PrepareScript != "npm ci" || got.Network != "extended" || got.SandboxSize != "large" || got.DependencyManagement != "custom-only" || got.Harness != "claudeCode" || got.OwnerIDs[0] != "u1" || got.WorkspaceKey != workspace {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := repository.CodingEnvironment(ctx, "other-workspace", created.ID); !errors.Is(err, domain.ErrCodingNotFound) {
		t.Fatalf("other workspace must not see it: %v", err)
	}
	if _, err := repository.CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: workspace, Repository: "acme/web"}); !errors.Is(err, domain.ErrCodingRepoInUse) {
		t.Fatalf("duplicate repo: %v", err)
	}
	if _, err := repository.CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: workspace + "-b", Repository: "acme/web"}); err != nil {
		t.Fatalf("other workspace same repo: %v", err)
	}
	byRepo, err := repository.CodingEnvironmentForRepository(ctx, workspace, "github", "", "ACME/web")
	if err != nil || byRepo.ID != created.ID {
		t.Fatalf("for repository: %+v %v", byRepo, err)
	}
	if _, err := repository.CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: workspace, Repository: "a/b//c"}); !errors.Is(err, codingenv.ErrInvalidEnvironment) {
		t.Fatalf("invalid: %v", err)
	}
	second, err := repository.CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: workspace, Name: "API", CodeHost: "gitlab", HostName: "gitlab.example.com", Repository: "g/sub/api"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repository.UpdateCodingEnvironment(ctx, workspace, created.ID, 1, func(environment *domain.CodingEnvironment) error {
		environment.PrepareScript = "pnpm i"
		environment.ID, environment.CreatedBy = "hijack", "someone"
		return nil
	})
	if err != nil || updated.Version != 2 || updated.PrepareScript != "pnpm i" || updated.ID != created.ID || updated.CreatedBy != "u1" {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if _, err := repository.UpdateCodingEnvironment(ctx, workspace, created.ID, 1, func(*domain.CodingEnvironment) error { return nil }); !errors.Is(err, domain.ErrCodingVersionConflict) {
		t.Fatalf("stale version: %v", err)
	}
	if _, err := repository.UpdateCodingEnvironment(ctx, workspace, second.ID, 0, func(environment *domain.CodingEnvironment) error {
		environment.CodeHost, environment.HostName, environment.Repository = "github", "github.com", "acme/WEB"
		return nil
	}); !errors.Is(err, domain.ErrCodingRepoInUse) {
		t.Fatalf("moving onto a used repository: %v", err)
	}
	if _, err := repository.UpdateCodingEnvironment(ctx, workspace, created.ID, 0, func(environment *domain.CodingEnvironment) error {
		environment.SandboxSize = "huge"
		return nil
	}); !errors.Is(err, codingenv.ErrInvalidEnvironment) {
		t.Fatalf("invalid update: %v", err)
	}
	archived, err := repository.SetCodingEnvironmentArchived(ctx, workspace, created.ID, true)
	if err != nil || archived.ArchivedAt == nil || archived.Version != 3 {
		t.Fatalf("archive: %+v %v", archived, err)
	}
	if again, err := repository.SetCodingEnvironmentArchived(ctx, workspace, created.ID, true); err != nil || again.Version != 3 {
		t.Fatalf("archive is idempotent: %+v %v", again, err)
	}
	replacement, err := repository.CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: workspace, Repository: "acme/web"})
	if err != nil {
		t.Fatalf("replacement after archive: %v", err)
	}
	if _, err := repository.SetCodingEnvironmentArchived(ctx, workspace, created.ID, false); !errors.Is(err, domain.ErrCodingRepoInUse) {
		t.Fatalf("unarchive conflict: %v", err)
	}
	if active, err := repository.ListCodingEnvironments(ctx, workspace, false); err != nil || len(active) != 2 {
		t.Fatalf("active list: %d %v", len(active), err)
	}
	if all, err := repository.ListCodingEnvironments(ctx, workspace, true); err != nil || len(all) != 3 {
		t.Fatalf("all list: %d %v", len(all), err)
	}
	if _, err := repository.SetCodingEnvironmentArchived(ctx, workspace, replacement.ID, true); err != nil {
		t.Fatal(err)
	}
	restored, err := repository.SetCodingEnvironmentArchived(ctx, workspace, created.ID, false)
	if err != nil || restored.ArchivedAt != nil {
		t.Fatalf("unarchive: %+v %v", restored, err)
	}
	script := "x"
	withProposal, err := repository.SetCodingEnvironmentProposal(ctx, workspace, created.ID, &domain.CodingEnvironmentProposal{SessionID: "s1", PrepareScript: &script, Explanation: "because"})
	if err != nil || withProposal.PendingProposal == nil || withProposal.PendingProposal.SessionID != "s1" || withProposal.Version != restored.Version {
		t.Fatalf("proposal: %+v %v", withProposal, err)
	}
	if cleared, err := repository.SetCodingEnvironmentProposal(ctx, workspace, created.ID, nil); err != nil || cleared.PendingProposal != nil {
		t.Fatalf("clear proposal: %+v %v", cleared, err)
	}
}

func TestCodingEnvironmentSecrets(t *testing.T) {
	withCodingSecretKey(t)
	for name, repository := range openCodingEnvironmentStores(t) {
		t.Run(name, func(t *testing.T) {
			// The test rotates and restores the key and its cleanup clears it,
			// so every store needs its own key.
			withCodingSecretKey(t)
			testCodingEnvironmentSecrets(t, repository)
		})
	}
}

func testCodingEnvironmentSecrets(t *testing.T, repository *SQLiteStore) {
	ctx := context.Background()
	workspace := uniqueWorkspace("env-secrets")
	environment, err := repository.CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: workspace, Repository: "acme/secrets"})
	if err != nil {
		t.Fatal(err)
	}
	const value = "sk_live_super_secret_value"
	if err := repository.PutCodingEnvironmentSecret(ctx, workspace, environment.ID, "STRIPE_KEY", value, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := repository.PutCodingEnvironmentSecret(ctx, workspace, environment.ID, "STRIPE_KEY", "other", "u2"); !errors.Is(err, domain.ErrCodingSecretExists) {
		t.Fatalf("secrets are immutable: %v", err)
	}
	if err := repository.PutCodingEnvironmentSecret(ctx, workspace, environment.ID, "FLOW_TOKEN", "x", "u1"); !errors.Is(err, codingenv.ErrInvalidEnvironment) {
		t.Fatalf("reserved name: %v", err)
	}
	if err := repository.PutCodingEnvironmentSecret(ctx, workspace, environment.ID, "EMPTY", "", "u1"); !errors.Is(err, codingenv.ErrInvalidEnvironment) {
		t.Fatalf("empty value: %v", err)
	}
	if err := repository.PutCodingEnvironmentSecret(ctx, workspace, "missing", "A", "x", "u1"); !errors.Is(err, domain.ErrCodingNotFound) {
		t.Fatalf("missing environment: %v", err)
	}
	if err := repository.PutCodingEnvironmentSecret(ctx, workspace, environment.ID, "DB_PASSWORD", "hunter2", "u1"); err != nil {
		t.Fatal(err)
	}
	got, err := repository.CodingEnvironment(ctx, workspace, environment.ID)
	if err != nil || len(got.Secrets) != 2 || got.Secrets[0].Name != "DB_PASSWORD" || got.Secrets[1].CreatedBy != "u1" {
		t.Fatalf("masked secrets: %+v %v", got.Secrets, err)
	}
	if encoded, _ := encodeCodingEnvironment(got); bytes.Contains(encoded, []byte(value)) {
		t.Fatal("secret value leaked into the environment document")
	}
	var stored []byte
	if err := repository.db.QueryRowContext(ctx, `SELECT ciphertext FROM coding_environment_secrets WHERE environment_id=? AND name=?`, environment.ID, "STRIPE_KEY").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(value)) {
		t.Fatal("secret stored in plaintext")
	}
	values, err := repository.CodingEnvironmentSecretValues(ctx, workspace, environment.ID)
	if err != nil || len(values) != 2 || values[1].Name != "STRIPE_KEY" || values[1].Value != value {
		t.Fatalf("decrypt: %+v %v", values, err)
	}
	// The AAD binds ciphertext to its environment and name.
	if _, err := repository.db.ExecContext(ctx, `UPDATE coding_environment_secrets SET ciphertext=? WHERE environment_id=? AND name=?`, stored, environment.ID, "DB_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CodingEnvironmentSecretValues(ctx, workspace, environment.ID); !errors.Is(err, ErrCodingSecretUnreadable) {
		t.Fatalf("swapped ciphertext must not decrypt: %v", err)
	}
	if err := repository.DeleteCodingEnvironmentSecret(ctx, workspace, environment.ID, "DB_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteCodingEnvironmentSecret(ctx, workspace, environment.ID, "DB_PASSWORD"); !errors.Is(err, domain.ErrCodingNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if err := repository.DeleteCodingEnvironmentSecret(ctx, workspace, environment.ID, "STRIPE_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := repository.PutCodingEnvironmentSecret(ctx, workspace, environment.ID, "STRIPE_KEY", "rotated", "u2"); err != nil {
		t.Fatal(err)
	}
	if err := SetCodingSecretKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CodingEnvironmentSecretValues(ctx, workspace, environment.ID); !errors.Is(err, ErrCodingSecretUnreadable) {
		t.Fatalf("rotated key: %v", err)
	}
	withCodingSecretKey(t)
	names, err := repository.CodingEnvironmentSecretNames(ctx, workspace, environment.ID)
	if err != nil || strings.Join(names, ",") != "STRIPE_KEY" {
		t.Fatalf("names: %v %v", names, err)
	}
}

func TestCodingSecretKeyRequired(t *testing.T) {
	t.Setenv("FLOW_CODING_SECRET_KEY", "")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", "")
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	environment, err := repository.CreateCodingEnvironment(context.Background(), domain.CodingEnvironment{WorkspaceKey: "w", Repository: "a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if CodingSecretsConfigured() {
		t.Fatal("no key configured")
	}
	if err := repository.PutCodingEnvironmentSecret(context.Background(), "w", environment.ID, "A_KEY", "v", "u"); err == nil {
		t.Fatal("secrets need a key")
	}
	if err := SetCodingSecretKey("not-base64!"); err == nil {
		t.Fatal("bad key accepted")
	}
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if !CodingSecretsConfigured() {
		t.Fatal("connector key fallback")
	}
}

func TestCodingSnapshotStore(t *testing.T) {
	for name, repository := range openCodingEnvironmentStores(t) {
		t.Run(name, func(t *testing.T) { testCodingSnapshotStore(t, repository) })
	}
}

func testCodingSnapshotStore(t *testing.T, repository *SQLiteStore) {
	ctx := context.Background()
	workspace := uniqueWorkspace("env-snapshots")
	environment, err := repository.CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: workspace, Repository: "acme/snap"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	prefix := workspace + "-"
	id := func(name string) string { return prefix + name }
	snapshot := func(name string, offset time.Duration, status string) domain.CodingEnvironmentSnapshot {
		return domain.CodingEnvironmentSnapshot{ID: id(name), EnvironmentID: environment.ID, Status: status, Strategy: "archive", ConfigHash: "h1", Image: "img", BaseRef: "HEAD", CreatedAt: base.Add(offset)}
	}
	if err := repository.CreateCodingSnapshot(ctx, workspace, snapshot("s1", 0, "")); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateCodingSnapshot(ctx, workspace, snapshot("s1", 0, "")); !errors.Is(err, domain.ErrCodingVersionConflict) {
		t.Fatalf("duplicate id: %v", err)
	}
	if err := repository.CreateCodingSnapshot(ctx, workspace, snapshot("s2", time.Minute, "")); err != nil {
		t.Fatal(err)
	}
	if superseded, err := repository.SupersedeQueuedCodingSnapshots(ctx, workspace, environment.ID, id("s2"), time.Now()); err != nil || superseded != 1 {
		t.Fatalf("supersede: %d %v", superseded, err)
	}
	if got, _ := repository.CodingSnapshot(ctx, workspace, id("s1")); got.Status != domain.CodingSnapshotSuperseded || got.FinishedAt == nil {
		t.Fatalf("superseded: %+v", got)
	}
	if active, err := repository.ActiveCodingSnapshots(ctx, workspace, environment.ID); err != nil || len(active) != 1 || active[0].ID != id("s2") {
		t.Fatalf("active: %+v %v", active, err)
	}

	now := time.Now()
	jobs, err := repository.LeaseCodingSnapshotJobs(ctx, "owner-a", now, 30*time.Second, 50)
	if err != nil {
		t.Fatal(err)
	}
	job := findSnapshotJob(t, jobs, id("s2"))
	if job.WorkspaceKey != workspace {
		t.Fatalf("lease: %+v", job)
	}
	if again, _ := repository.LeaseCodingSnapshotJobs(ctx, "owner-b", now, 30*time.Second, 50); containsSnapshot(again, id("s2")) {
		t.Fatal("a leased row must not be leased twice")
	}
	job.Snapshot.Status, job.Snapshot.SandboxID, job.Attempts, job.Plan = domain.CodingSnapshotBuilding, "sbx1", 1, []byte(`{"UploadURL":"u"}`)
	started := now
	job.Snapshot.StartedAt = &started
	if err := repository.SaveCodingSnapshotJob(ctx, "owner-b", job, now); !errors.Is(err, domain.ErrCodingLeaseLost) {
		t.Fatalf("save by a non-owner: %v", err)
	}
	if err := repository.SaveCodingSnapshotJob(ctx, "owner-a", job, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordCodingSnapshotResult(ctx, workspace, id("s2"), "old-sandbox", codingenv.SnapshotResult{Status: "ready"}); !errors.Is(err, domain.ErrCodingVersionConflict) {
		t.Fatalf("stale sandbox result: %v", err)
	}
	jobs, _ = repository.LeaseCodingSnapshotJobs(ctx, "owner-a", time.Now(), 30*time.Second, 50)
	job = findSnapshotJob(t, jobs, id("s2"))
	if job.Result != nil || string(job.Plan) != `{"UploadURL":"u"}` || job.Attempts != 1 || job.Snapshot.SandboxID != "sbx1" {
		t.Fatalf("leased job: %+v", job)
	}
	// A result arriving while a step runs survives that step's save.
	if err := repository.RecordCodingSnapshotResult(ctx, workspace, id("s2"), "sbx1", codingenv.SnapshotResult{Status: "ready", BaseSHA: "abc", SizeBytes: 42}); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordCodingSnapshotResult(ctx, workspace, id("s2"), "sbx1", codingenv.SnapshotResult{Status: "ready"}); err != nil {
		t.Fatalf("repeated result: %v", err)
	}
	if err := repository.SaveCodingSnapshotJob(ctx, "owner-a", job, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	jobs, _ = repository.LeaseCodingSnapshotJobs(ctx, "owner-a", time.Now(), 30*time.Second, 50)
	job = findSnapshotJob(t, jobs, id("s2"))
	if job.Result == nil || job.Result.BaseSHA != "abc" {
		t.Fatalf("result lost: %+v", job.Result)
	}
	finished := time.Now()
	job.Result = nil
	job.Snapshot.Status, job.Snapshot.StorageRef, job.Snapshot.SizeBytes, job.Snapshot.FinishedAt = domain.CodingSnapshotReady, "ref-s2", 42, &finished
	expires := finished.Add(-time.Minute)
	job.Snapshot.ExpiresAt = &expires
	if err := repository.SaveCodingSnapshotJob(ctx, "owner-a", job, time.Now()); err != nil {
		t.Fatal(err)
	}
	ready, err := repository.CodingSnapshot(ctx, workspace, id("s2"))
	if err != nil || ready.Status != domain.CodingSnapshotReady || ready.StorageRef != "ref-s2" || ready.SizeBytes != 42 {
		t.Fatalf("ready: %+v %v", ready, err)
	}
	if jobs, _ := repository.LeaseCodingSnapshotJobs(ctx, "owner-a", time.Now().Add(time.Hour), time.Second, 50); containsSnapshot(jobs, id("s2")) {
		t.Fatal("terminal rows are not leased")
	}
	if _, err := repository.LatestReadyCodingSnapshot(ctx, workspace, environment.ID, "h1"); !errors.Is(err, domain.ErrCodingNotFound) {
		t.Fatalf("expired snapshot must not be restorable: %v", err)
	}
	prunable, err := repository.PrunableCodingSnapshots(ctx, time.Now(), 2, 100)
	if err != nil || !containsSnapshot(prunable, id("s2")) {
		t.Fatalf("expired is prunable: %+v %v", prunable, err)
	}
	if err := repository.MarkCodingSnapshotPruned(ctx, workspace, id("s2"), domain.CodingSnapshotExpired, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := repository.CodingSnapshot(ctx, workspace, id("s2")); got.Status != domain.CodingSnapshotExpired || got.StorageRef != "" {
		t.Fatalf("pruned: %+v", got)
	}
	for index := 0; index < 3; index++ {
		item := snapshot(fmt.Sprintf("r%d", index), time.Duration(10+index)*time.Minute, domain.CodingSnapshotReady)
		item.StorageRef = "ref"
		if err := repository.CreateCodingSnapshot(ctx, workspace, item); err != nil {
			t.Fatal(err)
		}
	}
	prunable, _ = repository.PrunableCodingSnapshots(ctx, time.Now(), 2, 100)
	if !containsSnapshot(prunable, id("r0")) || containsSnapshot(prunable, id("r1")) || containsSnapshot(prunable, id("r2")) {
		t.Fatalf("retention: %+v", prunable)
	}
	if latest, err := repository.LatestReadyCodingSnapshot(ctx, workspace, environment.ID, "h1"); err != nil || latest.ID != id("r2") {
		t.Fatalf("latest ready: %+v %v", latest, err)
	}
	if usable, err := repository.LatestUsableCodingSnapshot(ctx, workspace, environment.ID); err != nil || usable.ID != id("r2") {
		t.Fatalf("latest usable: %+v %v", usable, err)
	}
	stale, err := repository.StaleCodingEnvironments(ctx, time.Now(), 100)
	found := false
	for _, item := range stale {
		if item.Environment.ID == environment.ID && item.Latest.ID == id("r2") {
			found = true
		}
	}
	if err != nil || !found {
		t.Fatalf("stale: %+v %v", stale, err)
	}
	for _, item := range func() []codingenv.StaleEnvironment {
		s, _ := repository.StaleCodingEnvironments(ctx, base, 100)
		return s
	}() {
		if item.Environment.ID == environment.ID {
			t.Fatalf("not stale yet: %+v", item)
		}
	}
	if withLatest, _ := repository.CodingEnvironment(ctx, workspace, environment.ID); withLatest.LatestSnapshot == nil || withLatest.LatestSnapshot.ID != id("r2") {
		t.Fatalf("latest snapshot on environment: %+v", withLatest.LatestSnapshot)
	}
	if err := repository.CreateCodingSnapshot(ctx, workspace, snapshot("c1", 20*time.Minute, "")); err != nil {
		t.Fatal(err)
	}
	if canceled, err := repository.RequestCodingSnapshotCancel(ctx, workspace, id("c1")); err != nil || canceled.Status != domain.CodingSnapshotQueued {
		t.Fatalf("cancel: %+v %v", canceled, err)
	}
	jobs, _ = repository.LeaseCodingSnapshotJobs(ctx, "owner-a", time.Now(), time.Second, 50)
	if job := findSnapshotJob(t, jobs, id("c1")); !job.CancelRequested {
		t.Fatal("cancel flag")
	}
	if err := repository.AppendCodingSnapshotLog(ctx, workspace, id("c1"), []byte("line 1\n")); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendCodingSnapshotLog(ctx, workspace, id("c1"), []byte("line 2\n")); err != nil {
		t.Fatal(err)
	}
	logs, last, err := repository.CodingSnapshotLogs(ctx, workspace, id("c1"), 0, 0)
	if err != nil || string(logs) != "line 1\nline 2\n" || last != 2 {
		t.Fatalf("logs: %q %d %v", logs, last, err)
	}
	if more, last2, _ := repository.CodingSnapshotLogs(ctx, workspace, id("c1"), last, 0); len(more) != 0 || last2 != 2 {
		t.Fatalf("follow: %q %d", more, last2)
	}
	if other, _, _ := repository.CodingSnapshotLogs(ctx, "other", id("c1"), 0, 0); len(other) != 0 {
		t.Fatal("logs are workspace scoped")
	}
	if deleted, err := repository.TrimCodingSnapshotHistory(ctx, 2); err != nil || deleted < 1 {
		t.Fatalf("trim: %d %v", deleted, err)
	}
	if _, err := repository.CodingSnapshot(ctx, workspace, id("s1")); !errors.Is(err, domain.ErrCodingNotFound) {
		t.Fatalf("old superseded row trimmed: %v", err)
	}
	if _, err := repository.CodingSnapshot(ctx, workspace, id("r0")); err != nil {
		t.Fatalf("rows with storage are kept: %v", err)
	}
}

func containsSnapshot(jobs []codingenv.SnapshotJob, id string) bool {
	for _, job := range jobs {
		if job.Snapshot.ID == id {
			return true
		}
	}
	return false
}

func findSnapshotJob(t *testing.T, jobs []codingenv.SnapshotJob, id string) codingenv.SnapshotJob {
	t.Helper()
	for _, job := range jobs {
		if job.Snapshot.ID == id {
			return job
		}
	}
	t.Fatalf("snapshot %s not leased: %d jobs", id, len(jobs))
	return codingenv.SnapshotJob{}
}

func TestCodingSnapshotLogCap(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	chunk := bytes.Repeat([]byte("x"), codingSnapshotLogChunkBytes)
	for index := 0; index < codingSnapshotLogMaxBytes/codingSnapshotLogChunkBytes+3; index++ {
		if err := repository.AppendCodingSnapshotLog(ctx, "w", "s", chunk); err != nil {
			t.Fatal(err)
		}
	}
	logs, _, err := repository.CodingSnapshotLogs(ctx, "w", "s", 0, 0)
	if err != nil || len(logs) > codingSnapshotLogMaxBytes {
		t.Fatalf("cap: %d %v", len(logs), err)
	}
}

func TestMigrateLegacyCodingEnvironments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	keys := repository.WorkspaceKeys()
	if len(keys) == 0 {
		t.Fatal("fixture has no workspace")
	}
	workspace := keys[0]
	legacy := []domain.CodingAgentEnvironment{
		{ID: "env_web", Name: "Web", Repository: "https://github.com/acme/web", SetupCommand: "npm ci"},
		{ID: "env_dup", Name: "Web again", Repository: "acme/web"},
		{ID: "env_lab", Name: "Lab", Repository: "git@gitlab.example.com:group/sub/lab.git", Archived: true},
		{ID: "env_bad", Name: "Broken", Repository: "not a repo"},
		{ID: "", Name: "No id", Repository: "acme/api"},
	}
	if err := repository.MutateWorkspace(ctx, workspace, "test.legacy_coding_environments", "x", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.CodingAgentSettings.Environments = legacy
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		if err := repository.migrateLegacyCodingEnvironments(ctx); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	environments, err := repository.ListCodingEnvironments(ctx, workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]domain.CodingEnvironment{}
	for _, environment := range environments {
		byID[environment.ID] = environment
	}
	if len(environments) != 4 {
		t.Fatalf("migrated %d: %+v", len(environments), environments)
	}
	if web := byID["env_web"]; web.CodeHost != "github" || web.HostName != "github.com" || web.Repository != "acme/web" || web.PrepareScript != "npm ci" || web.ArchivedAt != nil {
		t.Fatalf("web: %+v", web)
	}
	if byID["env_dup"].ArchivedAt == nil {
		t.Fatal("a second environment for the same repository is archived")
	}
	if lab := byID["env_lab"]; lab.CodeHost != "gitlab" || lab.HostName != "gitlab.example.com" || lab.Repository != "group/sub/lab" || lab.ArchivedAt == nil {
		t.Fatalf("lab: %+v", lab)
	}
	if byID["legacy-acme-api-4"].Repository != "acme/api" {
		t.Fatalf("generated id: %+v", byID)
	}
	data, _ := repository.WorkspaceMetadata(workspace)
	if len(data.WorkspaceSettings.CodingAgentSettings.Environments) != 0 {
		t.Fatal("legacy list must be cleared")
	}
	repository.Close()
	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if applied, err := reopened.migrationApplied(ctx, codingEnvironmentsMigrationVersion); err != nil || !applied {
		t.Fatalf("migration recorded: %t %v", applied, err)
	}
	if again, err := reopened.ListCodingEnvironments(ctx, workspace, true); err != nil || len(again) != 4 {
		t.Fatalf("after reopen: %d %v", len(again), err)
	}
	if reopenedData, _ := reopened.WorkspaceMetadata(workspace); len(reopenedData.WorkspaceSettings.CodingAgentSettings.Environments) != 0 {
		t.Fatal("cleared list persisted")
	}
}
