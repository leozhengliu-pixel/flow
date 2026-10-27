package store

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
)

// peerStoreSets returns pairs of independent store instances that share one
// database and one Redis coordinator, like two API replicas: always a SQLite
// pair, plus a pair on FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL
// when those name a throwaway MySQL or PostgreSQL database.
func peerStoreSets(t *testing.T) map[string][2]*SQLiteStore {
	t.Helper()
	redisServer := miniredis.RunT(t)
	sets := map[string][2]*SQLiteStore{}
	path := filepath.Join(t.TempDir(), "peers.db")
	var sqlite [2]*SQLiteStore
	for index := range sqlite {
		repo, err := OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repo.Close() })
		sqlite[index] = repo
	}
	sets["sqlite"] = sqlite
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		var external [2]*SQLiteStore
		for index := range external {
			repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { repo.Close() })
			external[index] = repo
		}
		sets[driver] = external
	}
	// Both instances of a pair must agree on the coordinator prefix.
	for name, pair := range sets {
		prefix := fmt.Sprintf("peer-store-%s", name)
		for _, repo := range pair {
			coordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{redisServer.Addr()}, Prefix: prefix, ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { coordinator.Close() })
			repo.SetWorkspaceCoordinator(coordinator)
		}
	}
	return sets
}

// createPeerIssue creates an issue (with an activity) through the standalone
// issue-record transaction, as POST /api/issues does.
func createPeerIssue(t *testing.T, repo *SQLiteStore, workspace, id string, template domain.Issue) domain.Issue {
	t.Helper()
	err := repo.MutateWorkspaceWithAggregate(WithIssueRecordMutations(t.Context()), workspace, "issue.created", map[string]any{}, func(data *domain.Bootstrap) (string, error) {
		issue := template
		issue.ID, issue.Identifier, issue.Number = id, "PEER-"+id, data.NextIssueNumber
		issue.Version, issue.ParentID, issue.SubIssueIDs, issue.ArchivedAt = 1, nil, []string{}, nil
		issue.CreatedAt = time.Now().UTC()
		issue.UpdatedAt = issue.CreatedAt
		data.Issues = append(data.Issues, issue)
		data.Activities[id] = []domain.ActivityEvent{{ID: "created-" + id, Type: "issue.created", Actor: data.Viewer, CreatedAt: issue.CreatedAt}}
		return id, nil
	})
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	created, err := repo.IssueRecord(t.Context(), workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

// peerWorkspace is a workspace with issues (the test fixture's; a shared
// external database also holds workspaces other tests created) and one of
// its issues as a template.
func peerWorkspace(t *testing.T, repo *SQLiteStore) (string, domain.Issue) {
	t.Helper()
	for _, workspace := range repo.WorkspaceKeys() {
		page, err := repo.readIssueRecords(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > 0 {
			return workspace, page[0]
		}
	}
	t.Fatal("no workspace with issues")
	return "", domain.Issue{}
}

func countIssueRows(t *testing.T, repo *SQLiteStore, workspace string) int {
	t.Helper()
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM issue_records WHERE workspace_key=?`, workspace).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// pausedMutation starts mutate on repo and returns once its callback runs
// (the snapshot is loaded); the callback then waits for release. The
// returned wait reports the mutation's error and how often the callback ran.
func pausedMutation(t *testing.T, run func(callback func(*domain.Bootstrap) error) error, apply func(*domain.Bootstrap) error) (release func(), wait func() (int, error)) {
	t.Helper()
	loaded, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var calls atomic.Int32
	go func() {
		done <- run(func(data *domain.Bootstrap) error {
			if calls.Add(1) == 1 {
				close(loaded)
				<-resume
			}
			return apply(data)
		})
	}()
	select {
	case <-loaded:
	case err := <-done:
		t.Fatalf("mutation finished before its callback paused: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("mutation callback never ran")
	}
	return func() { close(resume) }, func() (int, error) {
		select {
		case err := <-done:
			return int(calls.Load()), err
		case <-time.After(20 * time.Second):
			t.Fatal("mutation did not finish")
			return 0, nil
		}
	}
}

// The agent session archive audit only appends an audit entry. While it
// runs on one server another server commits a new issue; the audit must not
// delete it (it did when the audit loaded and replaced the issue collection).
func TestArchiveAuditKeepsAnIssueCreatedOnAnotherServer(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second := pair[0], pair[1]
			workspace, template := peerWorkspace(t, second)
			before := countIssueRows(t, second, workspace)
			taskID := fmt.Sprintf("mention_audit_%d", time.Now().UnixNano())
			ctx := WithMetadataFields(t.Context(), "auditLog")
			release, wait := pausedMutation(t, func(callback func(*domain.Bootstrap) error) error {
				return first.MutateWorkspace(ctx, workspace, "audit.agent_session", taskID, map[string]any{"action": "agent_session.archived"}, callback)
			}, func(data *domain.Bootstrap) error {
				if data.Issues != nil || data.Comments != nil || data.Activities != nil {
					t.Errorf("archive audit loaded issue content: issues=%d comments=%d", len(data.Issues), len(data.Comments))
				}
				data.AuditLog = append([]domain.AuditLogEntry{{ID: "audit_" + taskID, Actor: data.Viewer, Action: "agent_session.archived", ResourceType: "agent_session", ResourceID: taskID, CreatedAt: time.Now().UTC()}}, data.AuditLog...)
				return nil
			})
			created := createPeerIssue(t, second, workspace, fmt.Sprintf("peer-audit-%d", time.Now().UnixNano()), template)
			release()
			if _, err := wait(); err != nil {
				t.Fatal(err)
			}
			if after := countIssueRows(t, second, workspace); after != before+1 {
				t.Fatalf("issue rows %d -> %d, want %d", before, after, before+1)
			}
			kept, err := first.IssueRecord(t.Context(), workspace, created.ID)
			if err != nil || kept.Version != created.Version || kept.Title != created.Title {
				t.Fatalf("issue created on the other server: %+v %v", kept, err)
			}
			if err := first.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			audit, _ := first.WorkspaceMetadataFields(workspace, "auditLog")
			if !slices.ContainsFunc(audit.AuditLog, func(entry domain.AuditLogEntry) bool { return entry.ResourceID == taskID }) {
				t.Fatal("audit entry was not written")
			}
		})
	}
}

// The archive audit writes one audit entry however large the workspace is:
// no issue, comment, activity or attachment row, and the same rows with 5 or
// 205 issues.
func TestArchiveAuditWriteVolumeDoesNotGrowWithWorkspace(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := t.Context()
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	writes := auditWrites(t, repo)
	audit := func(id string) map[string]int {
		t.Helper()
		writes()
		if err := repo.MutateWorkspace(WithMetadataFields(ctx, "auditLog"), workspace, "audit.agent_session", id, map[string]any{"action": "agent_session.archived"}, func(next *domain.Bootstrap) error {
			if next.Issues != nil || next.Comments != nil || next.Activities != nil || next.Notifications != nil {
				t.Error("archive audit loaded issue content")
			}
			next.AuditLog = append([]domain.AuditLogEntry{{ID: "audit_" + id, Actor: next.Viewer, Action: "agent_session.archived", ResourceType: "agent_session", ResourceID: id, CreatedAt: time.Now().UTC()}}, next.AuditLog...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return writes()
	}
	small := audit("mention_small")
	issues := make([]domain.Issue, 200)
	for index := range issues {
		issue := data.Issues[0]
		issue.ID, issue.Identifier, issue.Number = fmt.Sprintf("volume-%03d", index), fmt.Sprintf("VOL-%d", index), 1000+index
		issues[index] = issue
	}
	if err := repo.ImportIssues(ctx, workspace, issues); err != nil {
		t.Fatal(err)
	}
	large := audit("mention_large")
	for _, table := range []string{"issue_records", "issue_label_records", "issue_permission_records", "issue_subscriber_records", "issue_attribute_records", "workspace_content_records", "workspace_states"} {
		if small[table] != 0 || large[table] != 0 {
			t.Errorf("archive audit wrote %s: %d then %d rows", table, small[table], large[table])
		}
	}
	if fmt.Sprint(small) != fmt.Sprint(large) || small["workspace_metadata_records"] == 0 || small["workspace_metadata_records"] > 2 {
		t.Fatalf("archive audit writes grew with the workspace or are not one entry: %v then %v", small, large)
	}
}
