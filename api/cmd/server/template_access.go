package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// templateAccess is the requesting viewer's reach into teams, read before a
// template is used or configured. Mutation callbacks may see only the records
// they write, so every team decision they make is answered from here.
type templateAccess struct {
	// all is development mode, which has no viewer projection.
	all bool
	// data holds the team graph, the viewer, their role and their own team
	// memberships.
	data   domain.Bootstrap
	access store.IssueRecordAccess
	// keyTeams are a team-restricted key's teams; nil for a session or an
	// unrestricted key.
	keyTeams map[string]bool
	// templatePermissions are the teams' "who may manage templates" settings.
	templatePermissions map[string]string
}

var (
	errTemplateTeamOutOfReach = fmt.Errorf("%w: the template names a team outside your teams", store.ErrAuthForbidden)
	errTemplateTeamMismatch   = fmt.Errorf("%w: the template belongs to another team", store.ErrAuthForbidden)
	errTeamTemplatePermission = fmt.Errorf("%w: your team role cannot manage this team's templates", store.ErrAuthForbidden)
)

func (s *server) requestTemplateAccess(r *http.Request) (templateAccess, error) {
	if s.authDisabled {
		return templateAccess{all: true}, nil
	}
	data, access, err := s.requestIssueQueryAccess(r)
	if err != nil {
		return templateAccess{}, err
	}
	result := templateAccess{data: data, access: access, templatePermissions: make(map[string]string, len(data.Teams))}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
		result.keyTeams = make(map[string]bool, len(key.TeamIDs))
		for _, id := range key.TeamIDs {
			result.keyTeams[id] = true
		}
	}
	for _, team := range data.Teams {
		if settings, found := s.store.TeamSettingsFor(data.Workspace.URLKey, team.ID); found {
			result.templatePermissions[team.ID] = settings.TemplatePermission
		}
	}
	return result, nil
}

func (a templateAccess) team(teamID string) bool {
	index := slices.IndexFunc(a.data.Teams, func(team domain.Team) bool { return team.ID == teamID })
	return index >= 0 && a.data.Teams[index].ArchivedAt == nil && (a.keyTeams == nil || a.keyTeams[teamID])
}

// teamRow reports whether the viewer sees the team's row and settings (a
// workspace admin sees every team's).
func (a templateAccess) teamRow(teamID string) bool {
	return a.all || a.team(teamID) && store.TeamRowVisible(a.data, teamID, a.data.Viewer.ID, a.data.ViewerRole)
}

// teamContent reports whether the viewer reads and adds the team's content;
// a workspace admin does so for a private team only as its member.
func (a templateAccess) teamContent(teamID string) bool {
	return a.all || a.team(teamID) && (a.access.Admin || slices.Contains(a.access.VisibleTeamIDs, teamID))
}

// teamTemplatesManageable applies the team's template permission, as the team
// template routes do (teamOperationPermission): workspace admins and team
// owners (directly or of a parent team) always, members when it allows.
func (a templateAccess) teamTemplatesManageable(teamID string) bool {
	if a.all || workspaceAdminRole(a.data.ViewerRole) {
		return true
	}
	role := ""
	seen := map[string]bool{}
	for current := teamID; current != "" && !seen[current]; current = a.data.TeamSettings[current].ParentTeamID {
		seen[current] = true
		for _, member := range a.data.TeamMembers {
			if member.TeamID != current || member.UserID != a.data.Viewer.ID {
				continue
			}
			if member.Role == "owner" {
				role = "owner"
			} else if current == teamID {
				role = member.Role
			}
		}
		if role == "owner" {
			break
		}
	}
	permission := teamTemplatePermission(domain.TeamSettings{TemplatePermission: a.templatePermissions[teamID]})
	return teamOperationAllowed(domain.TeamSettings{}, permission, role, a.data.ViewerRole)
}

// teamTemplateWritable reports whether the viewer may keep or put a template
// in teamID: they see the team and its template permission lets them manage
// its templates. An empty team is the workspace.
func (a templateAccess) teamTemplateWritable(teamID string) error {
	if teamID == "" {
		return nil
	}
	// A team the viewer cannot see answers as an unknown team does.
	if !a.teamRow(teamID) {
		return errInvalid
	}
	if !a.teamTemplatesManageable(teamID) {
		return errTeamTemplatePermission
	}
	return nil
}

// issueTemplateOwnerTeamID is the team a template belongs to: the team its
// visibility is narrowed to, else a team template's team. A workspace
// template's team is only the default team of the issues it creates.
func issueTemplateOwnerTeamID(template domain.IssueTemplate) string {
	if template.VisibilityTeamID != "" {
		return template.VisibilityTeamID
	}
	if template.Scope == "workspace" {
		return ""
	}
	return template.TeamID
}

// issueTemplateUsableBy reports whether the viewer may create an issue in
// targetTeamID from template: they see the teams the template names, the
// template is the workspace's or belongs to the target team or one of its
// parent teams (sub-teams inherit their parents' templates), and every
// sub-issue it creates lands in a team whose content they reach. data is the
// workspace the issue is created in.
func issueTemplateUsableBy(data *domain.Bootstrap, viewer templateAccess, template domain.IssueTemplate, targetTeamID string) error {
	if template.TeamID != "" && !viewer.teamRow(template.TeamID) || template.VisibilityTeamID != "" && !viewer.teamRow(template.VisibilityTeamID) {
		return errNotFound
	}
	if owner := issueTemplateOwnerTeamID(template); owner != "" && owner != targetTeamID && !slices.Contains(teamAncestorIDs(data, targetTeamID), owner) {
		return errTemplateTeamMismatch
	}
	for _, child := range template.SubIssues {
		if child.TeamID != "" && !viewer.teamContent(child.TeamID) {
			return errTemplateTeamOutOfReach
		}
	}
	return nil
}

// issueCreateTemplateUsable applies issueTemplateUsableBy to an issue created
// from input's template, in the team createIssueInWorkspace will choose. A
// missing template is left to createIssueInWorkspace.
func issueCreateTemplateUsable(data *domain.Bootstrap, viewer templateAccess, input domain.IssueCreateInput) error {
	if input.TemplateID == "" {
		return nil
	}
	index := slices.IndexFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool { return template.ID == input.TemplateID })
	if index < 0 {
		return nil
	}
	template := data.IssueTemplates[index]
	target := input.TeamID
	if target == "" {
		target = template.TeamID
	}
	if target == "" && len(data.Teams) > 0 {
		target = data.Teams[0].ID
	}
	return issueTemplateUsableBy(data, viewer, template, target)
}

// askTemplateAccess reads the viewer's template access for an ask route that
// sets, changes or applies a template (needed); otherwise it reads nothing.
// On failure it has written the refusal.
func (s *server) askTemplateAccess(w http.ResponseWriter, r *http.Request, needed bool) (templateAccess, bool) {
	if !needed {
		return templateAccess{}, true
	}
	viewer, err := s.requestTemplateAccess(r)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return templateAccess{}, false
	}
	return viewer, true
}

// askTemplateUsable applies issueTemplateUsableBy to the issue an ask creates
// from its template (createIssueFromAsk): in the ask's team, else the
// template's, else the first team.
func askTemplateUsable(data *domain.Bootstrap, viewer templateAccess, ask domain.Ask) error {
	if ask.TemplateID == "" {
		return nil
	}
	index := slices.IndexFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool { return template.ID == ask.TemplateID })
	if index < 0 {
		return nil
	}
	template := data.IssueTemplates[index]
	target := ask.TeamID
	if target == "" {
		target = template.TeamID
	}
	if target == "" && len(data.Teams) > 0 {
		target = data.Teams[0].ID
	}
	return issueTemplateUsableBy(data, viewer, template, target)
}

// issueTemplateReachable reports whether the viewer sees every team a stored
// template names; a template they cannot see is not found for them.
func issueTemplateReachable(viewer templateAccess, template domain.IssueTemplate) bool {
	return viewer.all || store.IssueTemplateTeamsVisible(template, viewer.teamRow)
}

// issueTemplateInputReachable refuses a template edit that would name a team
// outside the viewer's teams: its default team, the team its visibility is
// narrowed to or a sub-issue's team.
func issueTemplateInputReachable(viewer templateAccess, input domain.IssueTemplateMutationInput) error {
	teams := []string{}
	if input.TeamID != nil {
		teams = append(teams, *input.TeamID)
	}
	if input.VisibilityTeamID != nil {
		teams = append(teams, *input.VisibilityTeamID)
	}
	if input.SubIssues != nil {
		for _, child := range *input.SubIssues {
			teams = append(teams, child.TeamID)
		}
	}
	for _, id := range teams {
		// A team the viewer cannot see answers as an unknown team does
		// (applyIssueTemplate).
		if id != "" && !viewer.teamRow(id) {
			return errInvalid
		}
	}
	return nil
}

// issueTemplateEditAccess reads the viewer's template access for an issue
// template create, edit or delete and refuses input naming a team outside
// their teams. On failure it has written the refusal.
func (s *server) issueTemplateEditAccess(w http.ResponseWriter, r *http.Request, input domain.IssueTemplateMutationInput) (templateAccess, bool) {
	viewer, err := s.requestTemplateAccess(r)
	if err == nil {
		err = issueTemplateInputReachable(viewer, input)
	}
	// Narrowing a template to a team makes it that team's template: the
	// team's template permission applies, whichever route narrows it.
	if err == nil && input.VisibilityTeamID != nil {
		err = viewer.teamTemplateWritable(*input.VisibilityTeamID)
	}
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return templateAccess{}, false
	}
	return viewer, true
}

// documentTemplateWritable reports whether the viewer may change or delete a
// stored document template. A team's template answers to that team, apart
// from the workspace-wide template permission the route already checked: a
// template of a team they do not see is not found, and the team's template
// permission must let them manage it.
func (a templateAccess) documentTemplateWritable(template domain.DocumentTemplate) error {
	if template.TeamID != "" && !a.teamRow(template.TeamID) {
		return errNotFound
	}
	return a.teamTemplateWritable(template.TeamID)
}

// documentTemplateEditAccess reads the viewer's template access for a
// document template create, edit or delete and refuses moving a template
// into a team they cannot manage templates of. On failure it has written the
// refusal.
func (s *server) documentTemplateEditAccess(w http.ResponseWriter, r *http.Request, input documentTemplateInput) (templateAccess, bool) {
	viewer, err := s.requestTemplateAccess(r)
	if err == nil && input.TeamID != nil {
		err = viewer.teamTemplateWritable(strings.TrimSpace(*input.TeamID))
	}
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return templateAccess{}, false
	}
	return viewer, true
}

// documentTemplateUsableBy reports whether the viewer may create a document
// from template: they see its team, and when the template puts the document
// in that team (no team chosen), they add content to it.
func documentTemplateUsableBy(viewer templateAccess, template domain.DocumentTemplate, teamsChosen bool) error {
	if template.TeamID == "" {
		return nil
	}
	if !viewer.teamRow(template.TeamID) {
		return errInvalid
	}
	if !teamsChosen && !viewer.teamContent(template.TeamID) {
		return errTemplateTeamOutOfReach
	}
	return nil
}

// workspaceIssueTemplate reports whether the workspace template routes
// (/api/issue-templates) manage template: a template no team owns. Team
// templates change only through their team's routes, which check the team.
func workspaceIssueTemplate(template domain.IssueTemplate) bool {
	return issueTemplateOwnerTeamID(template) == ""
}

// chatBindingTemplatesUsable applies issueTemplateUsableBy, for the person
// saving a chat binding, to the templates the binding creates issues from
// (its create form's and its requests'), in the binding's team. data holds
// the workspace's issue templates, teams and team settings. A template that
// does not exist is left to the binding's own validation.
func (s *server) chatBindingTemplatesUsable(r *http.Request, data domain.Bootstrap, binding domain.ChatBinding) bool {
	ids := []string{}
	if binding.TemplateID != "" {
		ids = append(ids, binding.TemplateID)
	}
	if binding.Requests != nil && binding.Requests.TemplateID != "" {
		ids = append(ids, binding.Requests.TemplateID)
	}
	if len(ids) == 0 {
		return true
	}
	viewer, err := s.requestTemplateAccess(r)
	if err != nil {
		return false
	}
	for _, id := range ids {
		index := slices.IndexFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool { return template.ID == id })
		if index < 0 {
			continue
		}
		template := data.IssueTemplates[index]
		if !issueTemplateReachable(viewer, template) || issueTemplateUsableBy(&data, viewer, template, binding.TeamID) != nil {
			return false
		}
	}
	return true
}
