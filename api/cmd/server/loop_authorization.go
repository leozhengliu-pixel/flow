package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Loop authorization. A loop runs as its owner, so every way of reaching a
// loop checks the team it belongs to: edit, publish, duplicate, restore and
// manual run need the actor to be a workspace admin or a member of the loop's
// team who passes the team's loop permission; reading a loop of a private team
// needs the team's visibility. At run time the owner is checked the same way,
// so a loop never executes with an identity that lost access.

var (
	// errLoopTeamForbidden rejects an actor who cannot act in the loop's team.
	errLoopTeamForbidden = errors.New("you don't have permission to manage loops of this team")
	// errLoopOwnerForbidden rejects handing a loop to someone else's identity.
	errLoopOwnerForbidden = errors.New("only a workspace admin can make someone else the owner of a loop")
)

// loopIdentityMayUseTeam reports whether a user may manage or execute loops of
// a team: workspace admins, and members of the team (or an ancestor) who pass
// the workspace's loop permission and the team's loop permission.
func loopIdentityMayUseTeam(data *domain.Bootstrap, teamID, userID, role string) bool {
	if role == "guest" || userID == "" {
		return false
	}
	if workspaceAdminRole(role) {
		return true
	}
	if !workspacePermissionAllows(data.WorkspaceSettings, "loop", role) {
		return false
	}
	teamRole := teamRoleForUser(data, teamID, userID)
	if teamRole == "" {
		return false
	}
	permission := strings.TrimSpace(data.TeamSettings[teamID].LoopPermission)
	if permission == "" {
		permission = "allMembers"
	}
	return teamOperationAllowed(data.TeamSettings[teamID], permission, teamRole, role)
}

// loopReadable reports whether the viewer may see a loop: workspace loops are
// every member's, a team's loop follows the team's visibility.
func (s *server) loopReadable(data *domain.Bootstrap, loop domain.Loop) bool {
	if s.authDisabled || loop.TeamID == "" || workspaceAdminRole(data.ViewerRole) {
		return true
	}
	return store.TeamContentVisible(*data, loop.TeamID, data.Viewer.ID, data.ViewerRole)
}

// loopReadableFilter is loopReadable with the visible teams computed once.
func (s *server) loopReadableFilter(data *domain.Bootstrap) func(domain.Loop) bool {
	if s.authDisabled || workspaceAdminRole(data.ViewerRole) {
		return func(domain.Loop) bool { return true }
	}
	known := map[string]bool{}
	return func(loop domain.Loop) bool {
		if loop.TeamID == "" {
			return true
		}
		visible, ok := known[loop.TeamID]
		if !ok {
			visible = store.TeamContentVisible(*data, loop.TeamID, data.Viewer.ID, data.ViewerRole)
			known[loop.TeamID] = visible
		}
		return visible
	}
}

// checkLoopTeamAccess is the write check on an existing loop: a loop hidden
// from the actor is not found, a visible one needs team access (403).
func (s *server) checkLoopTeamAccess(data *domain.Bootstrap, loop domain.Loop) error {
	if s.authDisabled || loop.TeamID == "" {
		return nil
	}
	if !s.loopReadable(data, loop) {
		return errNotFound
	}
	return s.checkLoopTeamTarget(data, loop)
}

// checkLoopTeamTarget is the same check for the team a loop is created in or
// moved to: the actor needs team access there (403).
func (s *server) checkLoopTeamTarget(data *domain.Bootstrap, loop domain.Loop) error {
	if s.authDisabled || loop.TeamID == "" {
		return nil
	}
	// A team outside the request's reach (a restricted key's other teams) is
	// not one the actor can act in, whatever their membership.
	if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == loop.TeamID }) {
		return errLoopTeamForbidden
	}
	if !loopIdentityMayUseTeam(data, loop.TeamID, data.Viewer.ID, data.ViewerRole) {
		return errLoopTeamForbidden
	}
	return nil
}

// checkLoopOwnerChange limits who may run a loop: the owner changes only by a
// workspace admin, and anyone else may only make themselves the owner.
func (s *server) checkLoopOwnerChange(data *domain.Bootstrap, previous, next string) error {
	if s.authDisabled || next == previous || workspaceAdminRole(data.ViewerRole) || next == data.Viewer.ID {
		return nil
	}
	return errLoopOwnerForbidden
}

// loopAccessData is the projection loop handlers check access on: the
// viewer, their workspace role and memberships, and the team graph with each
// team's loop permission. It is the issue routes' request access (one policy
// evaluation per request), not a workspace read.
func (s *server) loopAccessData(r *http.Request) (domain.Bootstrap, error) {
	data, _, err := s.requestIssueQueryAccess(r)
	if err != nil {
		return data, store.ErrAuthForbidden
	}
	if allowed, restricted := requestKeyTeams(r); restricted {
		// A team-restricted key reaches only its teams, whatever its owner's
		// role: the teams outside it are not there, and an admin's shortcut
		// is dropped so the key acts as a member inside them.
		data.Teams = slices.DeleteFunc(slices.Clone(data.Teams), func(team domain.Team) bool { return !slices.Contains(allowed, team.ID) })
		if workspaceAdminRole(data.ViewerRole) {
			data.ViewerRole = "member"
		}
	}
	return data, nil
}

// requestKeyTeams returns the teams a team-restricted API key may reach.
func requestKeyTeams(r *http.Request) ([]string, bool) {
	key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
	if !ok || !apiKeyTeamRestrictionSelected(key) {
		return nil, false
	}
	return key.TeamIDs, true
}

// loopVisibleToRequest reports whether the request's viewer may see the loop;
// a loop of a team they cannot see is answered as not found.
func (s *server) loopVisibleToRequest(r *http.Request, loop domain.Loop) bool {
	if s.authDisabled || loop.TeamID == "" {
		return true
	}
	data, err := s.loopAccessData(r)
	return err == nil && s.loopReadable(&data, loop)
}

// loopEntityReadable reports whether the caller may read the entity a manual
// run is started on: a run reads and reports it, so it must be visible to the
// person who starts it.
func (s *server) loopEntityReadable(r *http.Request, data domain.Bootstrap, pipelines []domain.ReleasePipeline, entityType, id string, issue *domain.Issue) bool {
	if s.authDisabled {
		return true
	}
	_, access, err := s.requestIssueQueryAccess(r)
	if err != nil {
		return false
	}
	readable := func(teamID string) bool {
		return teamID == "" || access.Admin || slices.Contains(access.VisibleTeamIDs, teamID)
	}
	switch entityType {
	case "issue":
		return issue != nil && readable(issue.Team.ID) && !slices.Contains(access.ArchivedTeamIDs, issue.Team.ID)
	case "initiative":
		for _, item := range data.Initiatives {
			if item.ID != id {
				continue
			}
			teams := append([]string{item.LeadTeamID}, item.ContributingTeamIDs...)
			teams = slices.DeleteFunc(teams, func(team string) bool { return team == "" })
			return len(teams) == 0 || slices.ContainsFunc(teams, readable)
		}
		return false
	case "release":
		for _, item := range data.Releases {
			if item.ID != id {
				continue
			}
			if item.PipelineID == "" {
				return true
			}
			for _, pipeline := range pipelines {
				if pipeline.ID == item.PipelineID {
					return len(pipeline.TeamIDs) == 0 || slices.ContainsFunc(pipeline.TeamIDs, readable)
				}
			}
			return true
		}
		return false
	}
	teams := loopEntityTeams(data, entityType, id, issue)
	if len(teams) == 0 {
		return entityType == "project"
	}
	return slices.ContainsFunc(teams, readable)
}

// checkLoopOwnerForRun fails a run whose owner no longer fits the loop: not in
// the loop's team, or without the workspace's loop permission.
func (s *server) checkLoopOwnerForRun(data *domain.Bootstrap, loop domain.Loop, owner domain.User) error {
	if s.authDisabled {
		return nil
	}
	role := data.ViewerRole
	if loop.TeamID != "" {
		if !loopIdentityMayUseTeam(data, loop.TeamID, owner.ID, role) {
			return &loopRunError{reason: "unavailable", message: "the loop owner no longer has access to this loop's team"}
		}
		return nil
	}
	if role == "guest" || !workspacePermissionAllows(data.WorkspaceSettings, "loop", role) {
		return &loopRunError{reason: "unavailable", message: "the loop owner can no longer run loops in this workspace"}
	}
	return nil
}

// hideUnreadableLoops drops the loops of teams the bootstrap's viewer cannot
// see, so the workspace bootstrap lists the same loops as GET /api/loops.
func (s *server) hideUnreadableLoops(r *http.Request, data *domain.Bootstrap) {
	if s.authDisabled || len(data.Loops) == 0 {
		return
	}
	readable := s.loopReadableFilter(data)
	if allowed, restricted := requestKeyTeams(r); restricted {
		inner := readable
		readable = func(loop domain.Loop) bool {
			return inner(loop) && (loop.TeamID == "" || slices.Contains(allowed, loop.TeamID))
		}
	}
	data.Loops = slices.DeleteFunc(slices.Clone(data.Loops), func(loop domain.Loop) bool { return !readable(loop) })
}

// loopTeamReadableBy says which teams' loops an access scope may see: admins
// all, everyone else the teams whose content they can read.
func loopTeamReadableBy(access store.IssueRecordAccess) func(string) bool {
	return func(teamID string) bool {
		return access.Admin || access.WorkspaceAdmin || slices.Contains(access.VisibleTeamIDs, teamID)
	}
}

// loopBehaviorChanged reports whether next does something different from
// before: its instructions, trigger, tools, entity scope, team or level.
func loopBehaviorChanged(before, next domain.Loop) bool {
	return before.Instructions != next.Instructions ||
		!reflect.DeepEqual(before.InstructionsData, next.InstructionsData) ||
		before.TriggerType != next.TriggerType ||
		!reflect.DeepEqual(before.TriggerConfig, next.TriggerConfig) ||
		!slices.Equal(before.ConnectorIDs, next.ConnectorIDs) ||
		before.TeamAccess != next.TeamAccess ||
		before.AllowChangesOutsideTrigger != next.AllowChangesOutsideTrigger ||
		before.AllowExternalSync != next.AllowExternalSync ||
		before.WebSearch != next.WebSearch ||
		before.CodeAccess != next.CodeAccess ||
		before.Level != next.Level ||
		before.TeamID != next.TeamID ||
		!slices.Equal(before.TrustedSourceKeys, next.TrustedSourceKeys) ||
		!reflect.DeepEqual(before.OutputContract, next.OutputContract)
}

// trackLoopBehaviorEditor records who last changed a loop's behaviour when
// they are neither its owner nor an admin (the loop runs with the owner's
// access, so the editor's reach bounds the run). A save by the owner or an
// admin clears it.
func (s *server) trackLoopBehaviorEditor(data *domain.Bootstrap, before *domain.Loop, next *domain.Loop) {
	editor := data.Viewer.ID
	if s.authDisabled || editor == "" || editor == next.OwnerID || workspaceAdminRole(data.ViewerRole) {
		next.BehaviorEditorID = ""
		return
	}
	next.BehaviorEditorID = before.BehaviorEditorID
	if loopBehaviorChanged(*before, *next) {
		next.BehaviorEditorID = editor
	}
}

// restrictToBehaviorEditor narrows a run's workspace view to the teams both
// the owner and the loop's last behaviour editor can read, so a member's edit
// of an admin's loop cannot reach what only the admin sees.
func (s *server) restrictToBehaviorEditor(ctx context.Context, workspace string, data *domain.Bootstrap, loop domain.Loop, owner domain.User) error {
	if s.authDisabled || loop.BehaviorEditorID == "" || loop.BehaviorEditorID == owner.ID {
		return nil
	}
	_, editor, err := s.store.IssueQueryAccess(ctx, workspace, loop.BehaviorEditorID)
	if err != nil {
		return &loopRunError{reason: "unavailable", message: "the person who last changed this loop no longer has access to this workspace; an admin or the owner must save it again"}
	}
	allowed := map[string]bool{}
	for _, team := range data.Teams {
		if store.TeamContentVisible(*data, team.ID, owner.ID, data.ViewerRole) && (editor.Admin || slices.Contains(editor.VisibleTeamIDs, team.ID)) {
			allowed[team.ID] = true
		}
	}
	if loop.TeamID != "" && !allowed[loop.TeamID] {
		return &loopRunError{reason: "unavailable", message: "the person who last changed this loop cannot access its team; an admin or the owner must save it again"}
	}
	inScope := func(ids ...string) bool {
		ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id == "" })
		return len(ids) == 0 || slices.ContainsFunc(ids, func(id string) bool { return allowed[id] })
	}
	data.Teams = slices.DeleteFunc(slices.Clone(data.Teams), func(team domain.Team) bool { return !allowed[team.ID] })
	data.Projects = slices.DeleteFunc(slices.Clone(data.Projects), func(project domain.Project) bool { return !inScope(project.TeamIDs...) })
	data.Cycles = slices.DeleteFunc(slices.Clone(data.Cycles), func(cycle domain.Cycle) bool { return !inScope(cycle.TeamID) })
	data.Initiatives = slices.DeleteFunc(slices.Clone(data.Initiatives), func(item domain.Initiative) bool {
		return !inScope(append([]string{item.LeadTeamID}, item.ContributingTeamIDs...)...)
	})
	// An admin owner would otherwise read every team's issues through the
	// entity context; the narrowed team list is the limit.
	if workspaceAdminRole(data.ViewerRole) {
		data.ViewerRole = "member"
	}
	return nil
}

// loopMutationView is the workspace as a loop write sees it: the mutation's
// data with the caller's role and team memberships, which a write's data may
// not carry (access is read before the write, by loopAccessData).
func (s *server) loopMutationView(data *domain.Bootstrap, access domain.Bootstrap) *domain.Bootstrap {
	if s.authDisabled {
		return data
	}
	view := *data
	view.Viewer, view.ViewerRole, view.TeamMembers, view.Teams = access.Viewer, access.ViewerRole, access.TeamMembers, access.Teams
	return &view
}

// loopWriteAccess reads the caller's role and memberships before a loop write.
func (s *server) loopWriteAccess(w http.ResponseWriter, r *http.Request) (domain.Bootstrap, bool) {
	access, err := s.loopAccessData(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "workspace access denied")
		return access, false
	}
	return access, true
}
