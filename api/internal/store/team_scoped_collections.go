package store

import (
	"flow/api/internal/domain"
)

// IssueTemplateTeamsVisible reports whether every team an issue template
// names is visible: its team (for a workspace template, the default team of
// the issues it creates), the team its visibility is narrowed to and each
// sub-issue's team. Templates naming no team are workspace-wide.
func IssueTemplateTeamsVisible(template domain.IssueTemplate, visible func(teamID string) bool) bool {
	if template.TeamID != "" && !visible(template.TeamID) || template.VisibilityTeamID != "" && !visible(template.VisibilityTeamID) {
		return false
	}
	for _, child := range template.SubIssues {
		if child.TeamID != "" && !visible(child.TeamID) {
			return false
		}
	}
	return true
}

// FilterTeamScopedCollections projects the team-scoped settings and team page
// collections of a bootstrap. rows reports the teams whose settings the viewer
// sees and manages (templates, email intake, triage, workflows); content the
// teams whose content they read (a team page's pinned resources). Records
// without a team are workspace-wide and stay. Release notes and history follow
// the releases left in data, so the releases must be projected first.
//
// The filtered collections are rebuilt rather than compacted in place: a
// projection may share its backing arrays with a stored snapshot.
func FilterTeamScopedCollections(data *domain.Bootstrap, rows, content func(teamID string) bool) {
	inRows := func(teamID string) bool { return teamID == "" || rows(teamID) }
	data.IssueTemplates = keepTeamScoped(data.IssueTemplates, func(item domain.IssueTemplate) bool {
		return IssueTemplateTeamsVisible(item, rows)
	})
	data.DocumentTemplates = keepTeamScoped(data.DocumentTemplates, func(item domain.DocumentTemplate) bool { return inRows(item.TeamID) })
	data.EmailIntakeAddresses = keepTeamScoped(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return inRows(item.TeamID) })
	data.TriageResponsibilities = keepTeamScoped(data.TriageResponsibilities, func(item domain.TriageResponsibility) bool { return inRows(item.TeamID) })
	data.TriageRoutingRules = keepTeamScoped(data.TriageRoutingRules, func(item domain.TriageRoutingRule) bool { return inRows(item.TeamID) })
	data.WorkflowDefinitions = keepTeamScoped(data.WorkflowDefinitions, func(item domain.WorkflowDefinition) bool { return inRows(item.TeamID) })
	data.TeamResourceSections = keepTeamScoped(data.TeamResourceSections, func(item domain.TeamResourceSection) bool { return content(item.TeamID) })
	data.TeamPinnedResources = keepTeamScoped(data.TeamPinnedResources, func(item domain.TeamPinnedResource) bool { return content(item.TeamID) })
	if len(data.ReleaseNotes) == 0 && len(data.ReleaseHistory) == 0 {
		return
	}
	releases := make(map[string]bool, len(data.Releases))
	for _, release := range data.Releases {
		releases[release.ID] = true
	}
	data.ReleaseNotes = keepTeamScoped(data.ReleaseNotes, func(item domain.ReleaseNote) bool { return releases[item.ReleaseID] })
	data.ReleaseHistory = keepTeamScoped(data.ReleaseHistory, func(item domain.ReleaseHistory) bool { return releases[item.ReleaseID] })
}

// keepTeamScoped returns items unchanged when every item is kept, else a new
// slice of the kept items.
func keepTeamScoped[T any](items []T, keep func(T) bool) []T {
	for index, item := range items {
		if keep(item) {
			continue
		}
		result := make([]T, index, len(items)-1)
		copy(result, items[:index])
		for _, rest := range items[index+1:] {
			if keep(rest) {
				result = append(result, rest)
			}
		}
		return result
	}
	return items
}

// TeamClosed reports whether a team keeps its content from non-members: a
// private or members-only team, or a team inside a private boundary.
func (scope *DocumentLinkScope) TeamClosed(teamID string) bool {
	if scope.byID == nil {
		scope.byID = make(map[string]domain.Team, len(scope.teams))
		for _, team := range scope.teams {
			scope.byID[team.ID] = team
		}
	}
	return scope.teamClosed(teamID)
}

// BlankHiddenTemplateDefaults clears the default templates a projected team's
// settings name (for members, for non-members, for projects) when the
// projection left that template out: the viewer would otherwise learn the id
// of a template they may not see. A template collection that is absent (nil:
// a partial read that did not load it) leaves its defaults alone.
func BlankHiddenTemplateDefaults(data *domain.Bootstrap) {
	if len(data.TeamSettings) == 0 || data.IssueTemplates == nil && data.ProjectTemplates == nil {
		return
	}
	issueTemplates := make(map[string]bool, len(data.IssueTemplates))
	for _, template := range data.IssueTemplates {
		issueTemplates[template.ID] = true
	}
	projectTemplates := make(map[string]bool, len(data.ProjectTemplates))
	for _, template := range data.ProjectTemplates {
		projectTemplates[template.ID] = true
	}
	hiddenIssue := func(id string) bool { return id != "" && data.IssueTemplates != nil && !issueTemplates[id] }
	for teamID, settings := range data.TeamSettings {
		changed := false
		if hiddenIssue(settings.DefaultIssueTemplateForMembersID) {
			settings.DefaultIssueTemplateForMembersID, changed = "", true
		}
		if hiddenIssue(settings.DefaultIssueTemplateForNonMembersID) {
			settings.DefaultIssueTemplateForNonMembersID, changed = "", true
		}
		if settings.DefaultProjectTemplateID != "" && data.ProjectTemplates != nil && !projectTemplates[settings.DefaultProjectTemplateID] {
			settings.DefaultProjectTemplateID, changed = "", true
		}
		if changed {
			data.TeamSettings[teamID] = settings
		}
	}
}
