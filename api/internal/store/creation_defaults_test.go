package store

import (
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
