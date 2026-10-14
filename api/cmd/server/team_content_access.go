package main

import (
	"slices"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// teamContentVisible reports whether the viewer of a projected bootstrap may
// read a team's content (issues, projects, documents, views). Every visible
// team row qualifies, except that a workspace admin sees and manages private
// teams they have not joined while their content stays closed.
func teamContentVisible(data domain.Bootstrap, teamID string) bool {
	if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }) {
		return false
	}
	return !workspaceAdminRole(data.ViewerRole) || store.TeamContentVisible(data, teamID, data.Viewer.ID, data.ViewerRole)
}
