package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// useConfigFileDefaults installs the creation defaults of a config file for
// one test.
func useConfigFileDefaults(t *testing.T, content string) *appconfig.File {
	t.Helper()
	file, err := appconfig.ParseFile("config.yaml", []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	previous := domain.CurrentCreationDefaults()
	domain.SetCreationDefaults(file.Defaults)
	t.Cleanup(func() { domain.SetCreationDefaults(previous) })
	return file
}

const creationDefaultsFile = `
defaults:
  workspace:
    invitePermission: members
    sessionDurationDays: 14
    features: {initiatives: false}
  team:
    timezone: Asia/Shanghai
    defaultPriority: medium
    estimates: {type: fibonacci, allowZero: true}
    triage: {enabled: true}
    cycles: {enabled: true, durationWeeks: 1, upcomingCycles: 3}
  user:
    language: zh-CN
    theme: dark
    notifications: {email: false}
`

// New workspaces, teams and members' settings get the configured defaults;
// the workspace, team and members that already exist keep theirs.
func TestConfigFileCreationDefaults(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	before, _ := repository.BootstrapFor("test-workspace")
	existingTeam := before.Teams[0].ID
	if len(before.UserSettings) == 0 || before.UserSettings["usr_admin"].Language != "en-US" {
		t.Fatalf("fixture member settings = %+v", before.UserSettings)
	}
	file := useConfigFileDefaults(t, creationDefaultsFile)
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir(), defaultLanguage: file.DefaultLanguage})

	// A new team in the existing workspace.
	team := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": "Platform", "key": "PLT"}, http.StatusCreated)
	data, _ := repository.BootstrapFor("test-workspace")
	settings := data.TeamSettings[team.ID]
	if settings.EstimateType != "fibonacci" || !settings.EstimateAllowZero || !settings.TriageEnabled || settings.Timezone != "Asia/Shanghai" || settings.DefaultPriority != 3 {
		t.Fatalf("new team settings = %+v", settings)
	}
	if settings.StaleMonths != 6 || settings.Access != "public" || settings.ProgressOrder != "first" {
		t.Fatalf("new team lost built-in defaults: %+v", settings)
	}
	cycles := data.CycleSettings[team.ID]
	if !cycles.Enabled || cycles.DurationWeeks != 1 || cycles.UpcomingCount != 3 || !cycles.AutoCreate || cycles.StartsOn != 1 {
		t.Fatalf("new team cycle settings = %+v", cycles)
	}
	current, upcoming := 0, 0
	for _, cycle := range data.Cycles {
		if cycle.TeamID != team.ID {
			continue
		}
		switch cycle.Status {
		case "current":
			current++
		case "upcoming":
			upcoming++
		}
	}
	if current != 1 || upcoming != 3 {
		t.Fatalf("new team cycles: %d current, %d upcoming", current, upcoming)
	}
	// The team's settings and cycles are stored, not only cached.
	if err := repository.ReloadWorkspace(t.Context(), "test-workspace"); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := repository.BootstrapFor("test-workspace")
	stored := 0
	for _, cycle := range reloaded.Cycles {
		if cycle.TeamID == team.ID {
			stored++
		}
	}
	if reloaded.TeamSettings[team.ID].EstimateType != "fibonacci" || !reloaded.CycleSettings[team.ID].Enabled || stored != 4 {
		t.Fatalf("reloaded team settings %+v, %d cycles", reloaded.TeamSettings[team.ID], stored)
	}

	// The existing workspace, team and members are untouched.
	if existing := data.TeamSettings[existingTeam]; existing.EstimateType != before.TeamSettings[existingTeam].EstimateType || existing.TriageEnabled != before.TeamSettings[existingTeam].TriageEnabled || existing.Timezone != before.TeamSettings[existingTeam].Timezone {
		t.Fatalf("existing team settings changed: %+v", existing)
	}
	for userID, previous := range before.UserSettings {
		if data.UserSettings[userID].Language != previous.Language || data.UserSettings[userID].InterfaceTheme != previous.InterfaceTheme {
			t.Fatalf("existing member %s settings changed: %+v", userID, data.UserSettings[userID])
		}
	}
	if data.WorkspaceSettings.InvitePermission != before.WorkspaceSettings.InvitePermission || data.WorkspaceSettings.SessionDurationDays != before.WorkspaceSettings.SessionDurationDays {
		t.Fatalf("existing workspace settings changed: %+v", data.WorkspaceSettings)
	}

	// A team copying another team's settings keeps the copied ones.
	child := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": "Copy", "key": "CPY", "copyFromTeamId": existingTeam}, http.StatusCreated)
	data, _ = repository.BootstrapFor("test-workspace")
	if copied := data.TeamSettings[child.ID]; copied.EstimateType != data.TeamSettings[existingTeam].EstimateType {
		t.Fatalf("copied team settings = %+v", copied)
	}

	// A new workspace: its settings, first team and creator's settings.
	created := requestJSON[domain.Bootstrap](t, handler, "POST", "/api/workspaces", map[string]any{"name": "Defaults", "urlKey": "defaults-test", "region": "us", "tutorial": false}, http.StatusCreated)
	workspace, _ := repository.BootstrapFor(created.Workspace.URLKey)
	if workspace.WorkspaceSettings.InvitePermission != "members" || workspace.WorkspaceSettings.SessionDurationDays != 14 || workspace.WorkspaceSettings.FeatureFlags["initiatives"] || !workspace.WorkspaceSettings.FeatureFlags["releases"] {
		t.Fatalf("new workspace settings = %+v", workspace.WorkspaceSettings)
	}
	firstTeam := workspace.TeamSettings[workspace.Teams[0].ID]
	if firstTeam.EstimateType != "fibonacci" || !firstTeam.TriageEnabled || !workspace.CycleSettings[workspace.Teams[0].ID].Enabled {
		t.Fatalf("new workspace's first team = %+v, cycles %+v", firstTeam, workspace.CycleSettings[workspace.Teams[0].ID])
	}
	creator := workspace.UserSettings[workspace.Viewer.ID]
	if creator.Language != "zh-CN" || creator.InterfaceTheme != "Dark" || creator.HomeView != "Flow Agent (default)" {
		t.Fatalf("creator settings = %+v", creator)
	}
	if preferences := defaultPreferences("usr_new"); preferences.Email.Enabled || !preferences.Desktop.Enabled || preferences.EmailFormat != "digest" {
		t.Fatalf("notification defaults = %+v", preferences)
	}

	// The sign-in pages learn the default language.
	providers := requestJSON[map[string]any](t, handler, "GET", "/api/auth/providers", nil, http.StatusOK)
	if providers["defaultLanguage"] != "zh-CN" {
		t.Fatalf("providers = %v", providers)
	}
}

// Without a config file nothing changes: Flow's built-in defaults apply and
// the sign-in pages follow the browser.
func TestNoConfigFileKeepsBuiltInDefaults(t *testing.T) {
	useConfigFileDefaults(t, "")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	team := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": "Platform", "key": "PLT"}, http.StatusCreated)
	data, _ := repository.BootstrapFor("test-workspace")
	if settings := data.TeamSettings[team.ID]; settings.EstimateType != "notUsed" || settings.TriageEnabled || settings.Timezone != "Etc/UTC" {
		t.Fatalf("team settings = %+v", settings)
	}
	if cycles := data.CycleSettings[team.ID]; cycles.Enabled || cycles.DurationWeeks != 2 || cycles.Capacity != 4 {
		t.Fatalf("cycle settings = %+v", cycles)
	}
	created := requestJSON[domain.Bootstrap](t, handler, "POST", "/api/workspaces", map[string]any{"name": "Plain", "urlKey": "plain-test", "region": "us", "tutorial": false}, http.StatusCreated)
	workspace, _ := repository.BootstrapFor(created.Workspace.URLKey)
	if workspace.UserSettings[workspace.Viewer.ID].Language != "en-US" || workspace.WorkspaceSettings.InvitePermission != "admins" {
		t.Fatalf("workspace = %+v / %+v", workspace.UserSettings[workspace.Viewer.ID], workspace.WorkspaceSettings)
	}
	providers := requestJSON[map[string]any](t, handler, "GET", "/api/auth/providers", nil, http.StatusOK)
	if _, ok := providers["defaultLanguage"]; ok {
		t.Fatalf("providers = %v", providers)
	}
}

const workflowDefaultsFile = `
defaults:
  workspace:
    workflowStates:
      - {name: Inbox, type: backlog, color: "#BEC2C8"}
      - {name: Ready, type: unstarted, color: "#E2E2E2", default: true}
      - {name: Doing, type: started, color: "#F2C94C"}
      - {name: Review, type: started, color: "#4EA7FC"}
      - {name: Shipped, type: completed, color: "#5E6AD2"}
      - {name: Dropped, type: canceled, color: "#95A2B3"}
    labels:
      - {name: Bug, color: "#EB5757"}
    labelGroups:
      - name: Area
        color: "#4EA7FC"
        labels:
          - {name: Backend}
          - {name: Frontend}
`

func stateNames(states []domain.WorkflowState) []string {
	names := []string{}
	for _, state := range states {
		names = append(names, state.Name)
	}
	return names
}

// A new workspace (with its tutorial) gets the configured statuses and
// labels; its first team and teams created in it later share them and start
// new issues in the configured default status. The existing workspace and a
// team created in it keep Flow's built-in statuses and labels.
func TestConfigFileWorkflowStatesAndLabels(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	before, _ := repository.BootstrapFor("test-workspace")
	useConfigFileDefaults(t, workflowDefaultsFile)
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir(), workspaceTutorial: true})
	want := []string{"Inbox", "Ready", "Doing", "Review", "Shipped", "Dropped", "Duplicate"}

	created := requestJSON[domain.Bootstrap](t, handler, "POST", "/api/workspaces", map[string]any{"name": "Workflow", "urlKey": "workflow-test", "region": "us", "tutorial": true}, http.StatusCreated)
	key := created.Workspace.URLKey
	workspace, _ := repository.BootstrapFor(key)
	first := workspace.Teams[0].ID
	if got := stateNames(statesForTeam(&workspace, first)); !slices.Equal(got, want) {
		t.Fatalf("first team statuses = %v", got)
	}
	ready := stateForTeamByType(&workspace, first, "unstarted")
	if workspace.TeamSettings[first].DefaultStateID != ready.ID {
		t.Fatalf("first team default status = %q, want %q", workspace.TeamSettings[first].DefaultStateID, ready.ID)
	}
	labels := map[string]domain.IssueLabel{}
	for _, label := range workspace.Labels {
		labels[label.Name] = label
	}
	if len(workspace.LabelGroups) != 1 || workspace.LabelGroups[0].Name != "Area" || labels["Bug"].GroupID != "" || labels["Backend"].GroupID != workspace.LabelGroups[0].ID || labels["Frontend"].GroupID != workspace.LabelGroups[0].ID {
		t.Fatalf("labels %+v, groups %+v", workspace.Labels, workspace.LabelGroups)
	}
	if _, builtin := labels["Feature"]; builtin {
		t.Fatalf("built-in labels seeded too: %+v", workspace.Labels)
	}
	// The "Getting started" issues sit in the configured statuses.
	tutorial := 0
	for _, issue := range workspace.Issues {
		if issue.Team.ID == first {
			tutorial++
			if !slices.Contains(want, issue.State.Name) {
				t.Fatalf("tutorial issue in status %+v", issue.State)
			}
		}
	}
	if tutorial == 0 {
		t.Fatal("no tutorial issues")
	}

	// A team created later in the new workspace shares its statuses.
	team := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/"+key+"/teams", map[string]any{"name": "Platform", "key": "PLT"}, http.StatusCreated)
	states := requestJSON[[]domain.WorkflowState](t, handler, "GET", "/api/teams/"+team.ID+"/states?workspace="+key, nil, http.StatusOK)
	if got := stateNames(states); !slices.Equal(got, want) {
		t.Fatalf("new team statuses = %v", got)
	}
	workspace, _ = repository.BootstrapFor(key)
	if workspace.TeamSettings[team.ID].DefaultStateID != ready.ID {
		t.Fatalf("new team default status = %q, want %q", workspace.TeamSettings[team.ID].DefaultStateID, ready.ID)
	}
	// A new team starts with triage on, as in the reference.
	if !workspace.TeamSettings[team.ID].TriageEnabled {
		t.Fatalf("new team triage disabled: %+v", workspace.TeamSettings[team.ID])
	}
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues?workspace="+key, map[string]any{"title": "Configured workflow", "teamId": team.ID}, http.StatusCreated)
	if issue.State.ID != ready.ID {
		t.Fatalf("new issue status = %+v, want Ready", issue.State)
	}

	// The existing workspace keeps its statuses and labels, and so does a
	// team created in it.
	existing := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": "Ops", "key": "OPS"}, http.StatusCreated)
	data, _ := repository.BootstrapFor("test-workspace")
	if got, was := stateNames(statesForTeam(&data, existing.ID)), stateNames(statesForTeam(&before, before.Teams[0].ID)); !slices.Equal(got, was) {
		t.Fatalf("existing workspace statuses %v, were %v", got, was)
	}
	if data.TeamSettings[existing.ID].DefaultStateID != "state_backlog" || len(data.Labels) != len(before.Labels) || len(data.LabelGroups) != len(before.LabelGroups) {
		t.Fatalf("existing workspace changed: default %q, %d labels, %d groups", data.TeamSettings[existing.ID].DefaultStateID, len(data.Labels), len(data.LabelGroups))
	}
}
