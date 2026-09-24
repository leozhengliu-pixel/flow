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
	if feature == "triage-intelligence" {
		return false
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
	case strings.Contains(path, "/resources"), strings.Contains(path, "/resource-sections"):
		return settings.PinnedViewPermission
	case strings.HasSuffix(path, "/default-favorites"):
		return "owners"
	case strings.HasSuffix(path, "/settings"):
		var patch map[string]any
		if !peekRequestJSON(r, &patch) {
			return "owners"
		}
		for key := range patch {
			switch key {
			case "access", "membershipRestriction", "settingsPermission", "labelPermission", "templatePermission", "agentSkillPermission", "loopPermission", "memberPermission", "pinnedViewPermission", "parentTeamId":
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
		// "allMembers" refers to every member of this team. Team visibility is
		// controlled separately and must never grant mutation rights to an
		// unrelated workspace member.
		return teamRole == "member"
	}
	return false
}

func teamOperationDeniedMessage(permission, workspaceRole string) string {
	if workspaceRole == "guest" {
		return "Guests cannot manage team settings"
	}
	if permission == "owners" {
		return "Team owner access required"
	}
	return "Team membership required"
}
