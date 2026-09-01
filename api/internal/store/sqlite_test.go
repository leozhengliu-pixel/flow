package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
)

func TestSQLiteStorePersistsStateAndDomainEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	store, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}

	const issueID = "issue_test"
	err = store.MutateWithAggregate(context.Background(), "issue.created", map[string]string{"title": "Persist me"}, func(data *domain.Bootstrap) (string, error) {
		issue := data.Issues[0]
		issue.ID = issueID
		issue.Identifier = "TST-999"
		issue.Title = "Persist me"
		data.Issues = append([]domain.Issue{issue}, data.Issues...)
		return issueID, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Bootstrap().Issues[0].Title; got != "Persist me" {
		t.Fatalf("persisted title = %q", got)
	}
	events, err := reopened.Events(context.Background(), issueID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "issue.created" || events[0].AggregateID != issueID {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestProjectProgressHistoriesPersistAndRefreshOnIssueMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	store, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}

	seed, ok := store.BootstrapFor("test-workspace")
	if !ok {
		t.Fatal("test workspace is missing")
	}
	projectIndex := slices.IndexFunc(seed.Projects, func(project domain.Project) bool { return project.ID == "project_aut" })
	if projectIndex < 0 || len(seed.Projects[projectIndex].IssueCountHistory) == 0 {
		t.Fatalf("project progress history was not generated: %#v", seed.Projects)
	}
	issueID := seed.Issues[0].ID
	err = store.MutateWorkspace(context.Background(), "test-workspace", "issue.updated", issueID, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == issueID })
		if index < 0 {
			return errors.New("issue not found")
		}
		completedAt := time.Now().UTC()
		data.Issues[index].State.Type = "completed"
		data.Issues[index].CompletedAt = &completedAt
		data.Issues[index].UpdatedAt = completedAt
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := store.BootstrapFor("test-workspace")
	if !ok || len(updated.Projects[projectIndex].CompletedScopeHistory) == 0 {
		t.Fatalf("project progress history was not refreshed: %#v", updated.Projects)
	}
	if got := updated.Projects[projectIndex].CompletedScopeHistory[len(updated.Projects[projectIndex].CompletedScopeHistory)-1].Value; got < 1 {
		t.Fatalf("completed history value = %v, want at least 1", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, ok := reopened.BootstrapFor("test-workspace")
	if !ok || len(persisted.Projects[projectIndex].ProgressHistory) == 0 {
		t.Fatalf("project progress history was not persisted: %#v", persisted.Projects)
	}
}

func TestOpenSQLiteStartsWithoutFixtureWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	account := repository.Account()
	if len(account.Workspaces) != 0 {
		t.Fatalf("new database created fixture workspaces: %#v", account.Workspaces)
	}
	if account.Viewer.ID == "" {
		t.Fatal("new database did not provide an onboarding viewer")
	}
	created, err := repository.CreateWorkspace(context.Background(), "First workspace", "first-workspace", "us")
	if err != nil {
		t.Fatal(err)
	}
	if created.Workspace.URLKey != "first-workspace" || created.Viewer.ID != account.Viewer.ID {
		t.Fatalf("workspace created from onboarding state = %#v", created)
	}
}

func TestWorkspaceStateSizeLimitRejectsOversizedMutation(t *testing.T) {
	repository, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "flow.db"), FixtureProfile: "none", MaxOpenConns: 1, MaxStateBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if _, err := repository.CreateWorkspace(context.Background(), "Oversized Workspace", "oversized", "us"); err == nil || !strings.Contains(err.Error(), "workspace state exceeds") {
		t.Fatalf("workspace state limit error=%v", err)
	}
}

func TestSQLitePerformancePragmasAndIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf.db")
	repository, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path, FixtureProfile: "none", MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	var journalMode string
	if err := repository.db.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode = %q, want wal", journalMode)
	}
	var synchronous int
	if err := repository.db.QueryRowContext(t.Context(), `PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 1 { // NORMAL is SQLite value 1.
		t.Fatalf("synchronous = %d, want NORMAL (1)", synchronous)
	}
	for _, name := range []string{
		"workspace_states_updated_idx",
		"domain_events_created_idx",
		"workspace_invitations_workspace_created_idx",
	} {
		var count int
		if err := repository.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s missing", name)
		}
	}
}

func TestSchemaMigrationsAreVersionedAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("schema migration count = %d, want 2", count)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("schema migration count after reopen = %d, want 2", count)
	}
}

func TestCoordinatedStoresReloadBeforeMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinated.db")
	first, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	redisServer := miniredis.RunT(t)
	firstCoordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{redisServer.Addr()}, Prefix: "store-test", ConnectTimeout: time.Second, LockTTL: time.Second, LockWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer firstCoordinator.Close()
	secondCoordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{redisServer.Addr()}, Prefix: "store-test", ConnectTimeout: time.Second, LockTTL: time.Second, LockWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer secondCoordinator.Close()
	first.SetWorkspaceCoordinator(firstCoordinator)
	second.SetWorkspaceCoordinator(secondCoordinator)

	data := first.Bootstrap()
	issueID := data.Issues[0].ID
	if err := first.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.title", issueID, nil, func(next *domain.Bootstrap) error {
		next.Issues[0].Title = "coordinated title"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.priority", issueID, nil, func(next *domain.Bootstrap) error {
		next.Issues[0].Priority = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := first.ReloadWorkspace(t.Context(), data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	updated, _ := first.BootstrapFor(data.Workspace.URLKey)
	if updated.Issues[0].Title != "coordinated title" || updated.Issues[0].Priority != 1 {
		t.Fatalf("coordinated mutations lost data: %#v", updated.Issues[0])
	}

	created, err := first.CreateWorkspace(t.Context(), "Coordinated workspace", "coordinated-workspace", "us")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.CreateWorkspace(t.Context(), "Duplicate", "coordinated-workspace", "us"); err == nil {
		t.Fatal("catalog lock allowed a duplicate workspace key")
	}
	workspace := created.Workspace
	workspace.Name = "Renamed workspace"
	if _, err := second.UpdateWorkspace(t.Context(), created.Workspace.URLKey, workspace); err != nil {
		t.Fatal(err)
	}
	if err := first.ReloadAllWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reloaded, ok := first.BootstrapFor(created.Workspace.URLKey); !ok || reloaded.Workspace.Name != "Renamed workspace" {
		t.Fatalf("coordinated workspace update = %#v, %v", reloaded.Workspace, ok)
	}
	if err := first.DeleteWorkspace(t.Context(), created.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	if err := second.ReloadAllWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := second.BootstrapFor(created.Workspace.URLKey); ok {
		t.Fatal("deleted workspace remained in a remote cache")
	}
}

func TestSQLiteStoreWorkspaceLifecycleAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateWorkspace(context.Background(), "Design Systems", "design-systems", "eu")
	if err != nil {
		t.Fatal(err)
	}
	if created.Workspace.URLKey != "design-systems" || created.Workspace.Region != "eu" || len(created.Issues) != 0 || len(created.Teams) != 1 {
		t.Fatalf("new workspace bootstrap = %#v", created)
	}
	if created.UserSettings == nil || created.NotificationPreferences == nil || created.Reviews == nil || created.Drafts == nil || created.AgentSkills == nil || created.CustomEmojis == nil {
		t.Fatalf("new workspace returned nullable frontend collections: %#v", created)
	}
	err = repository.MutateWorkspace(context.Background(), "design-systems", "issue.created", "workspace_test_issue", nil, func(data *domain.Bootstrap) error {
		issue := domain.Issue{ID: "workspace_test_issue", Identifier: data.Teams[0].Key + "-1", Number: 1, Title: "Isolated issue", Team: data.Teams[0], State: data.States[1], Creator: data.Viewer}
		data.Issues = append(data.Issues, issue)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy, ok := repository.BootstrapFor("test-workspace")
	if !ok || slices.ContainsFunc(legacy.Issues, func(issue domain.Issue) bool { return issue.ID == "workspace_test_issue" }) {
		t.Fatalf("issue leaked into test-workspace: %#v", legacy.Issues)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	isolated, ok := reopened.BootstrapFor("design-systems")
	if !ok || len(isolated.Issues) != 1 || isolated.Issues[0].Title != "Isolated issue" {
		t.Fatalf("isolated workspace did not persist: %#v", isolated.Issues)
	}
	if got := len(reopened.Account().Workspaces); got != 2 {
		t.Fatalf("workspace count = %d", got)
	}
	if err := reopened.DeleteWorkspace(context.Background(), "design-systems"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.BootstrapFor("design-systems"); ok {
		t.Fatal("deleted workspace still exists")
	}
	if err := reopened.DeleteWorkspace(context.Background(), "test-workspace"); err != nil {
		t.Fatal(err)
	}
	account := reopened.Account()
	if len(account.Workspaces) != 0 || account.Viewer.ID == "" {
		t.Fatalf("zero-workspace account = %#v", account)
	}
	if _, err := reopened.CreateWorkspace(context.Background(), "Fresh Start", "fresh-start", "us"); err != nil {
		t.Fatalf("create after zero-workspace state: %v", err)
	}
}

func TestBootstrapForUserProjectsTeamStatesAndPrivateNotificationData(t *testing.T) {
	store, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	seed, ok := store.BootstrapFor("test-workspace")
	if !ok || len(seed.Teams) == 0 || len(seed.Users) < 2 {
		t.Fatalf("unexpected test-workspace seed: teams=%d users=%d", len(seed.Teams), len(seed.Users))
	}
	viewerID := seed.Viewer.ID
	otherUserID := seed.Users[1].ID
	teamID := seed.Teams[0].ID

	err = store.MutateWorkspace(context.Background(), "test-workspace", "test.bootstrap_projection", teamID, nil, func(data *domain.Bootstrap) error {
		teamStates := make([]domain.WorkflowState, 0, len(data.States))
		for index, state := range data.States {
			state.ID = "team_state_" + state.ID
			state.TeamID = teamID
			state.Position = float64(index)
			teamStates = append(teamStates, state)
		}
		data.States = append(data.States, teamStates...)
		data.NotificationPreferences = map[string]domain.NotificationPreferences{
			viewerID:    {UserID: viewerID},
			otherUserID: {UserID: otherUserID},
		}
		data.NotificationDeliveries = []domain.NotificationDelivery{
			{ID: "delivery_viewer", RecipientID: viewerID},
			{ID: "delivery_other", RecipientID: otherUserID},
		}
		data.Notifications = []domain.Notification{
			{ID: "notification_viewer", RecipientID: viewerID},
			{ID: "notification_other", RecipientID: otherUserID},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	projected, ok, err := store.BootstrapForUser(context.Background(), "test-workspace", viewerID)
	if err != nil || !ok {
		t.Fatalf("bootstrap projection failed: ok=%v err=%v", ok, err)
	}
	if len(projected.States) != len(seed.States) {
		t.Fatalf("projected states=%d, want %d team states", len(projected.States), len(seed.States))
	}
	if slices.ContainsFunc(projected.States, func(state domain.WorkflowState) bool { return state.TeamID == "" || state.TeamID != teamID }) {
		t.Fatalf("projected states contain canonical or foreign states: %#v", projected.States)
	}
	if len(projected.NotificationPreferences) != 1 || projected.NotificationPreferences[viewerID].UserID != viewerID {
		t.Fatalf("private preferences leaked: %#v", projected.NotificationPreferences)
	}
	if len(projected.NotificationDeliveries) != 1 || projected.NotificationDeliveries[0].RecipientID != viewerID {
		t.Fatalf("private deliveries leaked: %#v", projected.NotificationDeliveries)
	}
	if len(projected.Notifications) != 1 || projected.Notifications[0].RecipientID != viewerID {
		t.Fatalf("private notifications leaked: %#v", projected.Notifications)
	}
}
