package main

import (
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func workspaceFeatureEnabled(settings domain.WorkspaceSettings, feature string) bool {
	if enabled, found := settings.FeatureFlags[feature]; found {
		return enabled
	}
	if feature == "ai-agent" {
		if enabled, found := settings.FeatureFlags["ai"]; found {
			return enabled
		}
	}
	return true
}

func agentWorkspacePolicy(settings domain.WorkspaceSettings, role string) error {
	if settings.HIPAACompliance {
		return store.ErrAuthForbidden
	}
	if !workspaceFeatureEnabled(settings, "ai-agent") {
		return store.ErrAuthForbidden
	}
	if settings.PreventGuestAgents && role == "guest" {
		return store.ErrAuthForbidden
	}
	return nil
}

func teamOperationPermission(settings domain.TeamSettings, r *http.Request) string {
	path := r.URL.Path
	switch {
	case strings.Contains(path, "/members/"):
		return settings.MemberPermission
	case strings.Contains(path, "/labels"), strings.Contains(path, "/label-groups"):
		return settings.LabelPermission
	case strings.Contains(path, "/templates"):
		return settings.TemplatePermission
	case strings.Contains(path, "/agent-skills"):
		return settings.AgentSkillPermission
	case strings.Contains(path, "/loops"):
		return settings.LoopPermission
	case strings.HasSuffix(path, "/settings"):
		var patch map[string]any
		if !peekRequestJSON(r, &patch) {
			return "owners"
		}
		for key := range patch {
			switch key {
			case "access", "membershipRestriction", "settingsPermission", "labelPermission", "templatePermission", "agentSkillPermission", "loopPermission", "memberPermission", "parentTeamId":
				return "owners"
			}
		}
		return settings.SettingsPermission
	default:
		return settings.SettingsPermission
	}
}

func teamOperationAllowed(settings domain.TeamSettings, permission, teamRole, workspaceRole string) bool {
	if workspaceRole == "guest" {
		return false
	}
	if teamRole == "owner" {
		return true
	}
	if permission == "teamMembers" {
		return teamRole == "member"
	}
	if permission == "allMembers" {
		return teamRole != "" || (settings.Access != "private" && settings.Access != "restricted")
	}
	return false
}
