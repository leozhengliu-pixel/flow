package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func openCodingTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "coding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

func insertCodingSession(t *testing.T, repo *SQLiteStore, workspace, id string) {
	t.Helper()
	data, _ := json.Marshal(domain.CodingSession{ID: id, Summary: "from json", State: "pending"})
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO coding_sessions(id,workspace_key,agent_session_id,creator_id,source,state,phase,phase_started_at,data,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, workspace, "anchor-"+id, "u1", "issue", "complete", "idle", 1, data, 1, 1); err != nil {
		t.Fatal(err)
	}
}

func TestSessionPullRequestsUpsertOrderingAndRouting(t *testing.T) {
	repo := openCodingTestStore(t)
	ctx := t.Context()
	insertCodingSession(t, repo, "ws", "s1")
	insertCodingSession(t, repo, "other", "s2")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pr := domain.SessionPullRequest{SessionID: "s1", CodeHost: "github", HostName: "GitHub.com", Repository: "Acme/App", Number: 7, URL: "https://github.com/Acme/App/pull/7", Title: "Fix", State: "open", Draft: true, HeadBranch: "flow/fix", BaseBranch: "main", UpdatedAt: base}
	if err := repo.UpsertSessionPullRequest(ctx, "ws", pr); err != nil {
		t.Fatal(err)
	}
	// Idempotent replay.
	if err := repo.UpsertSessionPullRequest(ctx, "ws", pr); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertSessionPullRequest(ctx, "other", domain.SessionPullRequest{SessionID: "s2", CodeHost: "github", HostName: "github.com", Repository: "acme/app", Number: 7, UpdatedAt: base}); err != nil {
		t.Fatal(err)
	}
	// Merged, then a stale "open" and a later "closed" arrive.
	if err := repo.UpsertSessionPullRequest(ctx, "ws", domain.SessionPullRequest{SessionID: "s1", CodeHost: "github", HostName: "github.com", Repository: "acme/app", Number: 7, State: "merged", UpdatedAt: base.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertSessionPullRequest(ctx, "ws", domain.SessionPullRequest{SessionID: "s1", CodeHost: "github", HostName: "github.com", Repository: "acme/app", Number: 7, State: "open", UpdatedAt: base.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertSessionPullRequest(ctx, "ws", domain.SessionPullRequest{SessionID: "s1", CodeHost: "github", HostName: "github.com", Repository: "acme/app", Number: 7, State: "closed", UpdatedAt: base.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.SessionPullRequests(ctx, "ws", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want one PR, got %+v", list)
	}
	got := list[0]
	if got.State != "merged" || got.Title != "Fix" || got.URL == "" || got.HeadBranch != "flow/fix" || !got.CreatedAt.Equal(base) || !got.UpdatedAt.Equal(base.Add(3*time.Minute)) {
		t.Fatalf("merged state, preserved fields and created time expected: %+v", got)
	}
	sessions, err := repo.SessionsForChangeRequest(ctx, "github", "GITHUB.COM", "ACME/app", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != "s1" || sessions[0].WorkspaceKey != "ws" || sessions[0].Phase != domain.CodingPhaseIdle || sessions[0].State != "complete" || sessions[0].Summary != "from json" || sessions[1].WorkspaceKey != "other" {
		t.Fatalf("routing: %+v", sessions)
	}
	if none, err := repo.SessionsForChangeRequest(ctx, "github", "github.com", "acme/app", 8); err != nil || len(none) != 0 {
		t.Fatalf("unexpected sessions %v %v", none, err)
	}
	if err := repo.UpsertSessionPullRequest(ctx, "ws", domain.SessionPullRequest{SessionID: "s1"}); err == nil {
		t.Fatal("incomplete link must be rejected")
	}
}

func TestCodeHostInstallations(t *testing.T) {
	repo := openCodingTestStore(t)
	ctx := t.Context()
	installation := domain.CodeHostInstallation{WorkspaceKey: "ws", CodeHost: "github", HostName: "github.com", InstallationID: "42", AccountLogin: "acme", AccountType: "Organization", RepositorySelection: "selected"}
	for range 2 {
		if err := repo.UpsertCodeHostInstallation(ctx, installation); err != nil {
			t.Fatal(err)
		}
	}
	other := installation
	other.WorkspaceKey = "ws2"
	if err := repo.UpsertCodeHostInstallation(ctx, other); err != nil {
		t.Fatal(err)
	}
	list, err := repo.CodeHostInstallations(ctx, "ws", "github")
	if err != nil || len(list) != 1 || list[0].ID == "" || list[0].WorkspaceKey != "ws" || list[0].AccountLogin != "acme" {
		t.Fatalf("installations: %+v %v", list, err)
	}
	firstCreated := list[0].CreatedAt
	workspaces, err := repo.WorkspacesForInstallation(ctx, "github", "GitHub.com", "42")
	if err != nil || len(workspaces) != 2 || workspaces[0] != "ws" || workspaces[1] != "ws2" {
		t.Fatalf("workspaces: %v %v", workspaces, err)
	}
	suspended := time.Now().UTC().Truncate(time.Second)
	if err := repo.RefreshCodeHostInstallation(ctx, domain.CodeHostInstallation{CodeHost: "github", HostName: "github.com", InstallationID: "42", AccountLogin: "acme", RepositorySelection: "all", SuspendedAt: &suspended}); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.CodeHostInstallations(ctx, "ws2", "github")
	if len(list) != 1 || list[0].SuspendedAt == nil || list[0].RepositorySelection != "all" {
		t.Fatalf("refresh: %+v", list)
	}
	list, _ = repo.CodeHostInstallations(ctx, "ws", "github")
	if !list[0].CreatedAt.Equal(firstCreated) {
		t.Fatalf("created time must be kept: %v vs %v", list[0].CreatedAt, firstCreated)
	}
	if removed, err := repo.UnlinkCodeHostInstallation(ctx, "ws", "github", list[0].ID); err != nil || !removed {
		t.Fatalf("unlink: %v %v", removed, err)
	}
	if removed, _ := repo.UnlinkCodeHostInstallation(ctx, "ws", "github", "42"); removed {
		t.Fatal("second unlink must report nothing removed")
	}
	if err := repo.DeleteCodeHostInstallation(ctx, "github", "github.com", "42"); err != nil {
		t.Fatal(err)
	}
	if workspaces, _ := repo.WorkspacesForInstallation(ctx, "github", "github.com", "42"); len(workspaces) != 0 {
		t.Fatalf("delete must remove every workspace link: %v", workspaces)
	}
}

func TestClaimCodeHostWebhookDelivery(t *testing.T) {
	repo := openCodingTestStore(t)
	ctx := t.Context()
	first, err := repo.ClaimCodeHostWebhookDelivery(ctx, "github", "d1", "ws")
	if err != nil || !first {
		t.Fatalf("first claim: %v %v", first, err)
	}
	again, err := repo.ClaimCodeHostWebhookDelivery(ctx, "github", "d1", "ws")
	if err != nil || again {
		t.Fatalf("duplicate claim must be refused: %v %v", again, err)
	}
	if other, _ := repo.ClaimCodeHostWebhookDelivery(ctx, "github", "d1", "ws2"); !other {
		t.Fatal("another workspace gets its own claim")
	}
	if err := repo.ReleaseCodeHostWebhookDelivery(ctx, "github", "d1", "ws"); err != nil {
		t.Fatal(err)
	}
	if retry, _ := repo.ClaimCodeHostWebhookDelivery(ctx, "github", "d1", "ws"); !retry {
		t.Fatal("released claim must be claimable again")
	}
}
