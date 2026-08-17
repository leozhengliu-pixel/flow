package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

func TestSQLiteStorePersistsStateAndDomainEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}

	const issueID = "issue_test"
	err = store.MutateWithAggregate(context.Background(), "issue.created", map[string]string{"title": "Persist me"}, func(data *domain.Bootstrap) (string, error) {
		issue := data.Issues[0]
		issue.ID = issueID
		issue.Identifier = "CLE-999"
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

	reopened, err := OpenSQLite(path)
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

func TestSQLiteStoreWorkspaceLifecycleAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := OpenSQLite(path)
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
	err = repository.MutateWorkspace(context.Background(), "design-systems", "issue.created", "workspace_test_issue", nil, func(data *domain.Bootstrap) error {
		issue := domain.Issue{ID: "workspace_test_issue", Identifier: data.Teams[0].Key + "-1", Number: 1, Title: "Isolated issue", Team: data.Teams[0], State: data.States[1], Creator: data.Viewer}
		data.Issues = append(data.Issues, issue)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy, ok := repository.BootstrapFor("cleantrack")
	if !ok || slices.ContainsFunc(legacy.Issues, func(issue domain.Issue) bool { return issue.ID == "workspace_test_issue" }) {
		t.Fatalf("issue leaked into cleantrack: %#v", legacy.Issues)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(path)
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
	if err := reopened.DeleteWorkspace(context.Background(), "cleantrack"); err != nil {
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

func TestSQLiteStoreAddsAndPersistsCanonicalWorkflowStates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := Seed()
	legacy.States = legacy.States[:4]
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE workspace_state SET data = ? WHERE id = 1`, raw); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	states := reopened.Bootstrap().States
	if len(states) != 6 || states[4].ID != "state_canceled" || states[5].ID != "state_duplicate" {
		t.Fatalf("canonical states were not reconciled: %#v", states)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	persisted, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer persisted.Close()
	if got := persisted.Bootstrap().States; len(got) != 6 || got[5].Name != "Duplicate" {
		t.Fatalf("reconciled states did not survive reopen: %#v", got)
	}
}

func TestBootstrapForUserProjectsTeamStatesAndPrivateNotificationData(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	seed, ok := store.BootstrapFor("cleantrack")
	if !ok || len(seed.Teams) == 0 || len(seed.Users) < 2 {
		t.Fatalf("unexpected cleantrack seed: teams=%d users=%d", len(seed.Teams), len(seed.Users))
	}
	viewerID := seed.Viewer.ID
	otherUserID := seed.Users[1].ID
	teamID := seed.Teams[0].ID

	err = store.MutateWorkspace(context.Background(), "cleantrack", "test.bootstrap_projection", teamID, nil, func(data *domain.Bootstrap) error {
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

	projected, ok, err := store.BootstrapForUser(context.Background(), "cleantrack", viewerID)
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

func TestSQLiteStoreProjectsLegacyInboxNotifications(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := Seed()
	legacy.Notifications = nil
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE workspace_state SET data = ? WHERE id = 1`, raw); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	notifications := reopened.Bootstrap().Notifications
	if len(notifications) == 0 || notifications[0].ID == "" || notifications[0].IssueID == "" || notifications[0].SourceID == "" {
		t.Fatalf("legacy inbox projection = %#v", notifications)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	persisted, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer persisted.Close()
	if got := persisted.Bootstrap().Notifications; len(got) != len(notifications) {
		t.Fatalf("reconciled notifications did not survive reopen: %#v", got)
	}
}

func TestSQLiteStoreAddsCyclesToLegacyWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := Seed()
	legacy.Cycles = nil
	legacy.CycleSettings = nil
	for index := range legacy.Issues {
		legacy.Issues[index].CycleID = nil
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE workspace_state SET data = ? WHERE id = 1`, raw); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	data := reopened.Bootstrap()
	if len(data.Cycles) < 3 || !data.CycleSettings[data.Teams[0].ID].Enabled || data.Issues[0].CycleID == nil {
		t.Fatalf("cycles were not reconciled: cycles=%#v settings=%#v issue=%#v", data.Cycles, data.CycleSettings, data.Issues[0].CycleID)
	}
}
