package domain

import "slices"

// InheritedDocumentTeams derives Document.InheritedTeamIDs: the team of the
// issue the document is attached to (issueTeamID), then the teams of its
// projects in order, each once. Teams that are no longer in the workspace
// scope nothing and are left out. The server's link handlers and the startup
// migration both derive the field with it, so they always agree.
func InheritedDocumentTeams(issueTeamID string, projectIDs []string, projects []Project, teams []Team) []string {
	var result []string
	add := func(teamID string) {
		if teamID != "" && !slices.Contains(result, teamID) && slices.ContainsFunc(teams, func(team Team) bool { return team.ID == teamID }) {
			result = append(result, teamID)
		}
	}
	add(issueTeamID)
	for _, projectID := range projectIDs {
		if index := slices.IndexFunc(projects, func(project Project) bool { return project.ID == projectID }); index >= 0 {
			for _, teamID := range projects[index].TeamIDs {
				add(teamID)
			}
		}
	}
	return result
}
