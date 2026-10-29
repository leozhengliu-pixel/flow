package main

import (
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// An initiative is workspace-level: it reaches teams through its lead team,
// its contributing teams and the projects linked to it. A caller restricted to
// some teams (a team-limited key, an application, a loop) can use it when:
//   - it has a lead team, and that team is one of the caller's; or
//   - it has no lead team, and it names no team and no project at all (a
//     workspace-wide initiative, like a workspace document); or
//   - it has no lead team, and one of its contributing teams is one of the
//     caller's, or one of its projects is visible to the caller.
//
// This is the rule the REST layer applies to the same key (initiativeAllowed).
func initiativeInTeams(initiative domain.Initiative, viewerRole string, teamAllowed func(string) bool, projectVisible func(string) bool) bool {
	if initiative.LeadTeamID != "" {
		return teamAllowed(initiative.LeadTeamID)
	}
	if len(initiative.ContributingTeamIDs) == 0 && len(initiative.ProjectIDs) == 0 {
		return viewerRole != "guest"
	}
	return slices.ContainsFunc(initiative.ContributingTeamIDs, teamAllowed) || slices.ContainsFunc(initiative.ProjectIDs, projectVisible)
}

// loopInitiativeRef is the initiative a loop's write tool call names, if it
// names one.
func loopInitiativeRef(name string, args map[string]any) string {
	switch name {
	case "save_status_update":
		if loopStringArg(args, "type") == "initiative" {
			return loopStringArg(args, "initiative")
		}
	case "save_initiative", "delete_initiative":
		return loopStringArg(args, "id", "initiative")
	case "save_comment":
		return loopStringArg(args, "initiativeId")
	}
	return ""
}

// initiativeOutsideTeams reports whether the initiative a write names exists
// but is outside the loop's team access.
func (scope *loopScope) initiativeOutsideTeams(ref string) bool {
	data, ok := scope.s.store.WorkspaceMetadataFields(scope.workspace, "initiatives", "projects")
	if !ok {
		return false
	}
	initiative, err := mcpFindInitiative(data, strings.TrimSpace(ref))
	if err != nil {
		return false
	}
	projectVisible := func(id string) bool {
		return slices.ContainsFunc(data.Projects, func(project domain.Project) bool {
			return project.ID == id && (len(project.TeamIDs) == 0 || slices.ContainsFunc(project.TeamIDs, func(team string) bool { return scope.teams[team] }))
		})
	}
	return !initiativeInTeams(initiative, "member", func(id string) bool { return scope.teams[id] }, projectVisible)
}
