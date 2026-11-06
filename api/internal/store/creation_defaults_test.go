package store

import (
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// A member who appears in a workspace gets the operator's personal defaults
// once; settings members already have are never rewritten.
func TestCreationDefaultsApplyToNewMembersOnly(t *testing.T) {
	previous := domain.CurrentCreationDefaults()
	t.Cleanup(func() { domain.SetCreationDefaults(previous) })
	language, theme, email := "zh-CN", "Dark", false
	domain.SetCreationDefaults(domain.CreationDefaults{User: domain.UserCreationDefaults{Language: &language, InterfaceTheme: &theme, EmailNotifications: &email}})

	existing := domain.User{ID: "usr_existing", Name: "Existing", Active: true}
	data := EmptyWorkspace("Acme", "acme", "us", existing)
	saved := data.UserSettings[existing.ID]
	saved.Language, saved.InterfaceTheme = "en-US", "Light"
	data.UserSettings[existing.ID] = saved
	savedPreferences := data.NotificationPreferences[existing.ID]
	savedPreferences.Email.Enabled = true
	data.NotificationPreferences[existing.ID] = savedPreferences

	joined := domain.User{ID: "usr_joined", Name: "Joined", Active: true}
	data.Users = append(data.Users, joined)
	normalize(&data)

	if got := data.UserSettings[joined.ID]; got.Language != "zh-CN" || got.InterfaceTheme != "Dark" || got.HomeView != "Flow Agent (default)" {
		t.Fatalf("new member settings = %+v", got)
	}
	if got := data.NotificationPreferences[joined.ID]; got.Email.Enabled || !got.Inbox.Enabled || !got.Desktop.Enabled {
		t.Fatalf("new member notifications = %+v", got)
	}
	if got := data.UserSettings[existing.ID]; got.Language != "en-US" || got.InterfaceTheme != "Light" {
		t.Fatalf("existing member settings rewritten: %+v", got)
	}
	if !data.NotificationPreferences[existing.ID].Email.Enabled {
		t.Fatal("existing member notifications rewritten")
	}
}

// Workspace defaults apply to a new workspace and leave a stored one alone.
func TestCreationDefaultsApplyToNewWorkspacesOnly(t *testing.T) {
	previous := domain.CurrentCreationDefaults()
	t.Cleanup(func() { domain.SetCreationDefaults(previous) })
	before := EmptyWorkspace("Before", "before", "us", domain.User{ID: "usr_a", Active: true})

	invite, estimate, cycles := "members", "tShirt", true
	domain.SetCreationDefaults(domain.CreationDefaults{
		Workspace: domain.WorkspaceCreationDefaults{InvitePermission: &invite, FeatureFlags: map[string]bool{"pulse": false}},
		Team:      domain.TeamCreationDefaults{EstimateType: &estimate, Cycles: domain.CycleCreationDefaults{Enabled: &cycles}},
	})
	after := EmptyWorkspace("After", "after", "us", domain.User{ID: "usr_b", Active: true})
	if after.WorkspaceSettings.InvitePermission != "members" || after.WorkspaceSettings.FeatureFlags["pulse"] || !after.WorkspaceSettings.FeatureFlags["releases"] {
		t.Fatalf("new workspace settings = %+v", after.WorkspaceSettings)
	}
	team := after.Teams[0].ID
	if after.TeamSettings[team].EstimateType != "tShirt" || !after.CycleSettings[team].Enabled || after.CycleSettings[team].DurationWeeks != 2 {
		t.Fatalf("new workspace team = %+v, cycles %+v", after.TeamSettings[team], after.CycleSettings[team])
	}

	normalize(&before)
	if before.WorkspaceSettings.InvitePermission != "admins" || !before.WorkspaceSettings.FeatureFlags["pulse"] || before.TeamSettings[before.Teams[0].ID].EstimateType != "notUsed" {
		t.Fatalf("stored workspace rewritten: %+v / %+v", before.WorkspaceSettings, before.TeamSettings[before.Teams[0].ID])
	}
}

// A new workspace gets the configured statuses and labels instead of the
// built-in ones; its first team starts in the configured default status. A
// stored workspace keeps its statuses and labels.
func TestCreationDefaultsSeedWorkflowStatesAndLabels(t *testing.T) {
	previous := domain.CurrentCreationDefaults()
	t.Cleanup(func() { domain.SetCreationDefaults(previous) })
	before := EmptyWorkspace("Before", "before", "us", domain.User{ID: "usr_a", Active: true})

	templates := []domain.WorkflowStateTemplate{
		{Name: "Inbox", Type: "backlog", Color: "#BEC2C8", Position: 0},
		{Name: "Ready", Type: "unstarted", Color: "#E2E2E2", Position: 1, Default: true},
		{Name: "Doing", Type: "started", Color: "#F2C94C", Position: 2},
		{Name: "Shipped", Type: "completed", Color: "#5E6AD2", Position: 3},
		{Name: "Dropped", Type: "canceled", Color: "#95A2B3", Position: 4},
		{Name: "Duplicate", Type: "canceled", Color: "#95A2B3", Position: 5, Reserved: true},
	}
	domain.SetCreationDefaults(domain.CreationDefaults{Workspace: domain.WorkspaceCreationDefaults{
		WorkflowStates: templates,
		CustomLabels:   true,
		Labels:         []domain.LabelTemplate{{Name: "Bug", Color: "#EB5757", ResourceType: "issue"}},
		LabelGroups:    []domain.LabelGroupTemplate{{Name: "Value", Color: "#18B99A", ResourceType: "project", Labels: []domain.LabelTemplate{{Name: "Revenue", Color: "#18B99A", ResourceType: "project"}}}},
	}})
	after := EmptyWorkspace("After", "after", "us", domain.User{ID: "usr_b", Active: true})
	if len(after.States) != len(templates) {
		t.Fatalf("states = %+v", after.States)
	}
	ids := map[string]bool{}
	for index, state := range after.States {
		template := templates[index]
		if state.Name != template.Name || state.Type != template.Type || state.Color != template.Color || state.Position != template.Position || state.Default != template.Default || state.Reserved != template.Reserved || state.TeamID != "" {
			t.Errorf("state %d = %+v, template %+v", index, state, template)
		}
		// Generated IDs: never a built-in status ID, whose colour normalize would migrate.
		if _, builtin := canonicalWorkflowStateColors[state.ID]; ids[state.ID] || !strings.HasPrefix(state.ID, "state_") || builtin {
			t.Errorf("state %d id %q", index, state.ID)
		}
		ids[state.ID] = true
	}
	if got := after.TeamSettings[after.Teams[0].ID].DefaultStateID; got != after.States[1].ID {
		t.Fatalf("first team default state = %q, want Ready (%q)", got, after.States[1].ID)
	}
	if len(after.LabelGroups) != 1 || len(after.Labels) != 2 {
		t.Fatalf("labels %+v groups %+v", after.Labels, after.LabelGroups)
	}
	group := after.LabelGroups[0]
	if group.Name != "Value" || group.ResourceType != "project" || group.Scope != "Workspace" || group.ID == "" {
		t.Fatalf("group = %+v", group)
	}
	if bug := after.Labels[0]; bug.Name != "Bug" || bug.Color != "#EB5757" || bug.ResourceType != "issue" || bug.Scope != "Workspace" || bug.GroupID != "" {
		t.Fatalf("label = %+v", bug)
	}
	if revenue := after.Labels[1]; revenue.Name != "Revenue" || revenue.GroupID != group.ID || revenue.ResourceType != "project" {
		t.Fatalf("grouped label = %+v", revenue)
	}

	// The workspace created before keeps Flow's statuses and labels.
	normalize(&before)
	if len(before.States) != len(canonicalWorkflowStates()) || before.States[0].ID != "state_backlog" || before.TeamSettings[before.Teams[0].ID].DefaultStateID != "state_backlog" {
		t.Fatalf("stored workspace states rewritten: %+v", before.States)
	}
	if len(before.Labels) != 3 || before.Labels[0].Name != "Bug" || len(before.LabelGroups) != 0 {
		t.Fatalf("stored workspace labels rewritten: %+v / %+v", before.Labels, before.LabelGroups)
	}

	// Labels configured as none keep the built-in statuses.
	domain.SetCreationDefaults(domain.CreationDefaults{Workspace: domain.WorkspaceCreationDefaults{CustomLabels: true}})
	bare := EmptyWorkspace("Bare", "bare", "us", domain.User{ID: "usr_c", Active: true})
	if len(bare.Labels) != 0 || len(bare.LabelGroups) != 0 || bare.States[0].ID != "state_backlog" {
		t.Fatalf("no labels: %+v / %+v / %+v", bare.Labels, bare.LabelGroups, bare.States)
	}
}
