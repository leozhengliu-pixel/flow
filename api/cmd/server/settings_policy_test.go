package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestWorkspaceAgentAndFeaturePolicies(t *testing.T) {
	s := domain.WorkspaceSettings{FeatureFlags: map[string]bool{"ai": true, "ai-agent": false}, PreventGuestAgents: true}
	if workspaceFeatureEnabled(s, "ai-agent") || agentWorkspacePolicy(s, "member") == nil {
		t.Fatal("canonical Agent switch must override legacy ai key")
	}
	s.FeatureFlags["ai-agent"] = true
	if agentWorkspacePolicy(s, "guest") == nil || agentWorkspacePolicy(s, "member") != nil {
		t.Fatal("guest policy must not block members")
	}
	if featureForPath("/api/agent/sessions") != "ai-agent" || featureForPath("/api/loops/1") != "loops" {
		t.Fatal("missing API feature guards")
	}
	s.InitiativePermission = "admins"
	if workspacePermissionAllows(s, "initiative", "member") || !workspacePermissionAllows(s, "initiative", "admin") {
		t.Fatal("initiative permission ignored")
	}
}

func TestTeamPermissionsAllowConfiguredRolesWithoutPolicyEscalation(t *testing.T) {
	s := domain.TeamSettings{SettingsPermission: "allMembers", MemberPermission: "teamMembers", Access: "public"}
	r := httptest.NewRequest("PATCH", "/api/teams/team/settings", strings.NewReader(`{"description":"Edited"}`))
	r.Header.Set("Content-Type", "application/json")
	if !teamOperationAllowed(s, teamOperationPermission(s, r), "", "member") {
		t.Fatal("all workspace members should be allowed on public team general settings")
	}
	r = httptest.NewRequest("PATCH", "/api/teams/team/settings", strings.NewReader(`{"settingsPermission":"allMembers"}`))
	r.Header.Set("Content-Type", "application/json")
	if teamOperationAllowed(s, teamOperationPermission(s, r), "member", "member") {
		t.Fatal("members must not grant themselves permissions")
	}
	if !teamOperationAllowed(s, "teamMembers", "member", "member") || teamOperationAllowed(s, "teamMembers", "", "member") {
		t.Fatal("team member restriction ignored")
	}
	s.Access = "private"
	if teamOperationAllowed(s, "allMembers", "", "member") || teamOperationAllowed(s, "allMembers", "member", "guest") {
		t.Fatal("private teams and guests must remain restricted")
	}
}

func TestAgentGuidanceIncludesOnlyRelevantTeamSkills(t *testing.T) {
	d := domain.Bootstrap{Viewer: domain.User{ID: "u"}, UserSettings: map[string]domain.UserSettings{"u": {AgentInstructions: "Use Chinese"}}, TeamSettings: map[string]domain.TeamSettings{"a": {AgentSkills: []domain.TeamAgentSkill{{Name: "Context", Instructions: "Relevant instructions", Enabled: true}, {Name: "Off", Instructions: "Disabled instructions"}}}, "b": {AgentSkills: []domain.TeamAgentSkill{{Name: "Other", Instructions: "Unrelated instructions", Enabled: true}}}}}
	prompt := workspaceAgentSystemPrompt(d, []domain.Issue{{Team: domain.Team{ID: "a", Name: "A"}}}, nil)
	if !strings.Contains(prompt, "Use Chinese") || !strings.Contains(prompt, "Relevant instructions") || strings.Contains(prompt, "Unrelated instructions") || strings.Contains(prompt, "Disabled instructions") {
		t.Fatalf("incorrect guidance scope: %s", prompt)
	}
}
