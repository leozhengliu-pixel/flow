package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
)

// Migration tests use a synthetic fixture that is never loaded by production.
func zentaoDemoSeed() domain.Bootstrap {
	data := localSQLiteFixture()
	now := time.Now().UTC()
	data.SavedViews = []domain.SavedView{
		{ID: "view_strategy", Name: "IT需求池", Resource: "issues", Scope: "workspace", OwnerID: data.Viewer.ID, View: "all", Filters: json.RawMessage(`[{"field":"labels","operator":"is","value":"label_type_requirement","valueLabel":"IT需求","values":[{"value":"label_type_requirement","valueLabel":"IT需求","color":"#5E6AD2"}]}]`), Display: json.RawMessage(`{"layout":"list"}`), CreatedAt: now, UpdatedAt: now},
		{ID: "view_custom", Name: "Custom view", Resource: "issues", Scope: "workspace", OwnerID: data.Viewer.ID, View: "all", Filters: json.RawMessage(`[]`), Display: json.RawMessage(`{"layout":"list"}`), CreatedAt: now, UpdatedAt: now},
	}
	data.Releases = []domain.Release{{ID: "release_car_phase2", Name: "Test release 3", PipelineID: "", Stage: "", Position: 0, ProjectIDs: []string{"project_aut"}, CreatedAt: now, UpdatedAt: now}}
	return data
}

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

func TestDatabaseWithEmptySeedStartsWithoutWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path, SeedProfile: "none", MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	account := repository.Account()
	if len(account.Workspaces) != 0 {
		t.Fatalf("empty seed created workspaces: %#v", account.Workspaces)
	}
	if account.Viewer.ID == "" {
		t.Fatal("empty seed did not provide a local viewer")
	}
	created, err := repository.CreateWorkspace(context.Background(), "First workspace", "first-workspace", "us")
	if err != nil {
		t.Fatal(err)
	}
	if created.Workspace.URLKey != "first-workspace" || created.Viewer.ID != account.Viewer.ID {
		t.Fatalf("workspace created from empty seed = %#v", created)
	}
}

func TestSQLitePerformancePragmasAndIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf.db")
	repository, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path, SeedProfile: "none", MaxOpenConns: 1})
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

func TestCoordinatedStoresReloadBeforeMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinated.db")
	first, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenSQLite(path)
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
	legacy := localSQLiteFixture()
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

func TestEnsureCanonicalLabelsMigratesLegacyDeliveryTaxonomy(t *testing.T) {
	data := zentaoDemoSeed()
	legacyBug := domain.IssueLabel{ID: "label_bug", Name: "Bug", Scope: "Workspace", GroupID: "label_group_quality"}
	obsoleteVersion := domain.IssueLabel{ID: "label_version_implementing", Name: "版本·实施", Scope: "Workspace", GroupID: "label_group_version"}
	custom := domain.IssueLabel{ID: "label_custom", Name: "Keep me", Scope: "Workspace"}
	data.Labels = append(data.Labels, legacyBug, obsoleteVersion, custom)
	data.LabelGroups = append(data.LabelGroups,
		domain.LabelGroup{ID: "label_group_quality", Name: "Quality", ResourceType: "issue"},
		domain.LabelGroup{ID: "label_group_version", Name: "Version", ResourceType: "issue"},
	)
	data.Issues[0].Description += " 来源于禅道 Bug #1。"
	data.Issues[0].Labels = []domain.IssueLabel{legacyBug, custom}
	data.Issues[1].Labels = []domain.IssueLabel{obsoleteVersion}

	if !ensureCanonicalLabelGroups(&data) || !ensureCanonicalLabels(&data) {
		t.Fatal("legacy taxonomy migration did not report a change")
	}
	if slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return obsoleteDeliveryLabelIDs[label.ID] }) {
		t.Fatalf("legacy labels remain after migration: %#v", data.Labels)
	}
	if slices.ContainsFunc(data.LabelGroups, func(group domain.LabelGroup) bool { return group.ID == "label_group_quality" }) {
		t.Fatalf("legacy group remains after migration: %#v", data.LabelGroups)
	}
	if !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == custom.ID }) {
		t.Fatal("custom label was removed by taxonomy migration")
	}
	if !slices.ContainsFunc(data.Issues[0].Labels, func(label domain.IssueLabel) bool { return label.ID == "label_type_defect" }) {
		t.Fatalf("issue was not reclassified as a defect: %#v", data.Issues[0].Labels)
	}
	if !slices.ContainsFunc(data.Issues[1].Labels, func(label domain.IssueLabel) bool { return label.ID == "label_type_development" }) {
		t.Fatalf("version-like issue was not reclassified as a development task: %#v", data.Issues[1].Labels)
	}
	issueGroups := slices.DeleteFunc(append([]domain.LabelGroup{}, data.LabelGroups...), func(group domain.LabelGroup) bool { return group.ResourceType != "issue" })
	if len(issueGroups) != 1 || issueGroups[0].ID != "label_group_work_item_type" {
		t.Fatalf("expected one work item type group, got %#v", issueGroups)
	}
}

func TestEnsureCanonicalLabelsRenamesRequirementLabelReferences(t *testing.T) {
	data := zentaoDemoSeed()
	for index := range data.Labels {
		if data.Labels[index].ID == "label_type_requirement" {
			data.Labels[index].Name = "原始需求"
		}
	}
	for issueIndex := range data.Issues {
		for labelIndex := range data.Issues[issueIndex].Labels {
			if data.Issues[issueIndex].Labels[labelIndex].ID == "label_type_requirement" {
				data.Issues[issueIndex].Labels[labelIndex].Name = "原始需求"
			}
		}
	}
	data.SavedViews[0].Filters = json.RawMessage(`[{
		"field":"labels","operator":"is","value":"label_type_requirement","valueLabel":"原始需求",
		"values":[{"value":"label_type_requirement","valueLabel":"原始需求","color":"#5E6AD2"}]
	}]`)

	if !ensureCanonicalLabels(&data) {
		t.Fatal("requirement label rename did not report a change")
	}
	if !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool {
		return label.ID == "label_type_requirement" && label.Name == "IT需求"
	}) {
		t.Fatalf("canonical requirement label was not renamed: %#v", data.Labels)
	}
	for _, issue := range data.Issues {
		for _, label := range issue.Labels {
			if label.ID == "label_type_requirement" && label.Name != "IT需求" {
				t.Fatalf("issue label reference was not renamed: %#v", label)
			}
		}
	}
	var filters []map[string]any
	if err := json.Unmarshal(data.SavedViews[0].Filters, &filters); err != nil {
		t.Fatal(err)
	}
	if filters[0]["valueLabel"] != "IT需求" {
		t.Fatalf("saved view filter was not renamed: %#v", filters[0])
	}
	values := filters[0]["values"].([]any)
	if values[0].(map[string]any)["valueLabel"] != "IT需求" {
		t.Fatalf("saved view filter value was not renamed: %#v", values[0])
	}
}

func TestEnsureCanonicalSavedViewDisplaysOnlyMigratesLegacyBuiltIns(t *testing.T) {
	data := zentaoDemoSeed()
	legacy := json.RawMessage(`{"layout":"list","ordering":"updatedAt","direction":"desc"}`)
	customized := json.RawMessage(`{"layout":"board","grouping":"assignee"}`)
	data.SavedViews[0].Display = slices.Clone(legacy)
	data.SavedViews[1].Display = slices.Clone(customized)
	data.SavedViews = append(data.SavedViews, domain.SavedView{
		ID:      "view_custom",
		Name:    "Custom",
		Display: slices.Clone(legacy),
	})

	if !ensureCanonicalSavedViewDisplays(&data) {
		t.Fatal("legacy built-in display migration did not report a change")
	}
	if string(data.SavedViews[0].Display) == string(legacy) {
		t.Fatal("legacy built-in display was not migrated")
	}
	if string(data.SavedViews[1].Display) != string(customized) {
		t.Fatalf("customized built-in display was overwritten: %s", data.SavedViews[1].Display)
	}
	if string(data.SavedViews[len(data.SavedViews)-1].Display) != string(legacy) {
		t.Fatal("custom view display was migrated")
	}
	if ensureCanonicalSavedViewDisplays(&data) {
		t.Fatal("canonical migration was not idempotent")
	}
}

func TestEnsureCanonicalSavedViewNamesRenamesRequirementView(t *testing.T) {
	data := zentaoDemoSeed()
	data.SavedViews[0].Name = "原始需求"
	data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "view_unrelated", Name: "原始需求", Filters: json.RawMessage(`[]`)})

	if !ensureCanonicalSavedViewNames(&data) {
		t.Fatal("requirement view rename did not report a change")
	}
	if data.SavedViews[0].Name != "IT需求池" {
		t.Fatalf("requirement view name = %q", data.SavedViews[0].Name)
	}
	if data.SavedViews[len(data.SavedViews)-1].Name != "原始需求" {
		t.Fatal("unrelated view with the same name was renamed")
	}
	if ensureCanonicalSavedViewNames(&data) {
		t.Fatal("requirement view rename was not idempotent")
	}
}

func TestEnsureCanonicalSavedViewFiltersMigratesCompactValues(t *testing.T) {
	data := zentaoDemoSeed()
	data.SavedViews = []domain.SavedView{{
		ID: "view_compact", Name: "Compact", Filters: json.RawMessage(`[
			{"field":"project","operator":"is","values":["project_aut"]},
			{"field":"labels","operator":"is","values":["label_type_requirement"]}
		]`),
	}}

	if !ensureCanonicalSavedViewFilters(&data) {
		t.Fatal("compact saved view filters were not migrated")
	}
	var filters []map[string]any
	if err := json.Unmarshal(data.SavedViews[0].Filters, &filters); err != nil {
		t.Fatal(err)
	}
	if got := filters[0]["valueLabel"]; got != "Test project" {
		t.Fatalf("unexpected project label: %v", got)
	}
	values, ok := filters[1]["values"].([]any)
	if !ok || len(values) != 1 {
		t.Fatalf("unexpected migrated values: %#v", filters[1]["values"])
	}
	labelValue, ok := values[0].(map[string]any)
	if !ok || labelValue["value"] != "label_type_requirement" || labelValue["valueLabel"] != "IT需求" {
		t.Fatalf("unexpected migrated label value: %#v", values[0])
	}
	if ensureCanonicalSavedViewFilters(&data) {
		t.Fatal("saved view filter migration was not idempotent")
	}
}

func TestEnsureCarMallReleaseManagementMigratesVersionMilestones(t *testing.T) {
	data := zentaoDemoSeed()
	data.ReleasePipelines = slices.DeleteFunc(data.ReleasePipelines, func(pipeline domain.ReleasePipeline) bool {
		return pipeline.ID == "release_pipeline_car_mall"
	})
	data.Releases = slices.DeleteFunc(data.Releases, func(release domain.Release) bool {
		return release.ID == "release_car_phase1" || release.ID == "release_car_316"
	})
	for releaseIndex := range data.Releases {
		if data.Releases[releaseIndex].ID == "release_car_phase2" {
			data.Releases[releaseIndex].PipelineID = ""
			data.Releases[releaseIndex].Stage = ""
			data.Releases[releaseIndex].Position = 0
		}
	}
	projectIndex := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == "project_aut" })
	if projectIndex < 0 {
		t.Fatal("car mall project missing from seed")
	}
	data.Projects[projectIndex].Milestones = []domain.ProjectMilestone{
		{ID: "milestone_car_phase1", ProjectID: "project_aut", Name: "车商城一期迭代完成"},
		{ID: "milestone_car_316", ProjectID: "project_aut", Name: "车商城316迭代完成"},
	}
	assignments := map[string]string{"issue_53156": "milestone_car_phase1", "issue_105130": "milestone_car_316"}
	for issueIndex := range data.Issues {
		if milestoneID := assignments[data.Issues[issueIndex].ID]; milestoneID != "" {
			data.Issues[issueIndex].ProjectMilestoneID = stringPointer(milestoneID)
		}
	}

	if !ensureCarMallReleaseManagement(&data) {
		t.Fatal("car mall release migration did not report a change")
	}
	if slices.ContainsFunc(data.Projects[projectIndex].Milestones, func(milestone domain.ProjectMilestone) bool {
		return milestone.ID == "milestone_car_phase1" || milestone.ID == "milestone_car_316"
	}) {
		t.Fatalf("version milestones remain after migration: %#v", data.Projects[projectIndex].Milestones)
	}
	for _, issue := range data.Issues {
		if assignments[issue.ID] != "" && issue.ProjectMilestoneID != nil {
			t.Fatalf("issue %s still references milestone %q", issue.ID, *issue.ProjectMilestoneID)
		}
	}
	pipelineIndex := slices.IndexFunc(data.ReleasePipelines, func(pipeline domain.ReleasePipeline) bool {
		return pipeline.ID == "release_pipeline_car_mall"
	})
	if pipelineIndex < 0 || data.ReleasePipelines[pipelineIndex].Name != "车商城交付发布管线" || !slices.Equal(data.ReleasePipelines[pipelineIndex].TeamIDs, data.Projects[projectIndex].TeamIDs) {
		t.Fatalf("car mall release pipeline = %#v", data.ReleasePipelines)
	}
	for releaseID, issueID := range map[string]string{"release_car_phase1": "issue_53156", "release_car_316": "issue_105130"} {
		releaseIndex := slices.IndexFunc(data.Releases, func(release domain.Release) bool { return release.ID == releaseID })
		if releaseIndex < 0 || data.Releases[releaseIndex].PipelineID != "release_pipeline_car_mall" || data.Releases[releaseIndex].Stage != "已发布" || !slices.Contains(data.Releases[releaseIndex].IssueIDs, issueID) {
			t.Fatalf("migrated release %s = %#v", releaseID, data.Releases)
		}
	}
	phaseTwoIndex := slices.IndexFunc(data.Releases, func(release domain.Release) bool { return release.ID == "release_car_phase2" })
	if phaseTwoIndex < 0 || data.Releases[phaseTwoIndex].PipelineID != "release_pipeline_car_mall" || data.Releases[phaseTwoIndex].Stage != "待规划" {
		t.Fatalf("phase two release was not attached to pipeline: %#v", data.Releases)
	}
	if ensureCarMallReleaseManagement(&data) {
		t.Fatal("car mall release migration was not idempotent")
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
	legacy := localSQLiteFixture()
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
	legacy := localSQLiteFixture()
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
