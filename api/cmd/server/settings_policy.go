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
	if teamResourcePath(path) {
		// Linear "dangerousOperations": deleting, retiring/restoring a team and
		// changing its visibility are always team-owner operations.
		if r.Method == http.MethodDelete {
			return "owners"
		}
		var patch map[string]any
		if !peekRequestJSON(r, &patch) {
			return "owners"
		}
		for key := range patch {
			switch key {
			case "retired", "private", "subTeamAction", "parentTeamId":
				return "owners"
			}
		}
		return settings.SettingsPermission
	}
	switch {
	case strings.Contains(path, "/members/"):
		return settings.MemberPermission
	case strings.Contains(path, "/labels"), strings.Contains(path, "/label-groups"):
		return settings.LabelPermission
	case strings.Contains(path, "/templates"), strings.HasSuffix(path, "/recurring-issues"):
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

// teamSettingsWriteAllowed mirrors the team settings gate used by
// /api/teams/{id}/settings for team-scoped records stored outside that path
// (git automations and target branches). Workspace admins and team owners
// (including owners of a parent team) always pass; other team members pass
// when the team's "Settings management" permission allows all members.
// teamRoles comes from requestTeamRoles (team memberships live outside the
// workspace document, so they are resolved before the mutation).
func teamSettingsWriteAllowed(data *domain.Bootstrap, teamID string, teamRoles map[string]string, workspaceRole string) bool {
	if workspaceAdminRole(workspaceRole) {
		return true
	}
	if workspaceRole == "guest" || teamID == "" {
		return false
	}
	settings := data.TeamSettings[teamID]
	return teamOperationAllowed(settings, settings.SettingsPermission, effectiveTeamRole(data, teamID, teamRoles), workspaceRole)
}

// effectiveTeamRole applies store.TeamRole's inheritance to direct
// memberships: owners of a team own its descendants.
func effectiveTeamRole(data *domain.Bootstrap, teamID string, directRoles map[string]string) string {
	seen := map[string]bool{}
	for current := teamID; current != "" && !seen[current]; current = data.TeamSettings[current].ParentTeamID {
		seen[current] = true
		if directRoles[current] == "owner" {
			return "owner"
		}
	}
	return directRoles[teamID]
}

// requestWorkspaceRole resolves the caller's workspace role and their direct
// team memberships (team id → role; see effectiveTeamRole for inheritance) for
// handlers that enforce role checks inside a workspace mutation, where the
// store lock is already held. Local AUTH_DISABLED fixtures act as an admin.
func (s *server) requestWorkspaceRole(r *http.Request) (string, map[string]string) {
	teamRoles := map[string]string{}
	if s.authDisabled {
		return "admin", teamRoles
	}
	userID := authUser(r).ID
	data, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r))
	if !ok {
		return "", teamRoles
	}
	role, status, err := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, userID)
	if err != nil || status != "active" {
		return "", teamRoles
	}
	members, err := s.store.ListTeamMembers(r.Context(), data.Workspace.ID)
	if err != nil {
		return role, teamRoles
	}
	for _, member := range members {
		if member.UserID == userID {
			teamRoles[member.TeamID] = member.Role
		}
	}
	return role, teamRoles
}

// releasePipelineAdministrable mirrors Linear's canAdministerPipeline: workspace
// owners/admins, or members who own every team the pipeline belongs to.
// Pipelines without teams are administered by workspace admins only.
func releasePipelineAdministrable(data *domain.Bootstrap, teamIDs []string, teamRoles map[string]string, workspaceRole string) bool {
	if workspaceAdminRole(workspaceRole) {
		return true
	}
	if workspaceRole != "member" || len(teamIDs) == 0 {
		return false
	}
	for _, teamID := range teamIDs {
		if effectiveTeamRole(data, teamID, teamRoles) != "owner" {
			return false
		}
	}
	return true
}

// releasePipelineChangeAllowed applies releasePipelineAdministrable to the
// pipeline before a change and, when the change reassigns teams, to the team
// set it would end up with, so members cannot hand a pipeline to teams they
// do not own.
func releasePipelineChangeAllowed(data *domain.Bootstrap, current domain.ReleasePipeline, nextTeamIDs *[]string, teamRoles map[string]string, workspaceRole string) bool {
	if !releasePipelineAdministrable(data, current.TeamIDs, teamRoles, workspaceRole) {
		return false
	}
	return nextTeamIDs == nil || releasePipelineAdministrable(data, *nextTeamIDs, teamRoles, workspaceRole)
}

// teamResourcePath matches /api/workspaces/{workspace}/teams/{team} itself.
func teamResourcePath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 5 && parts[0] == "api" && parts[1] == "workspaces" && parts[3] == "teams"
}
