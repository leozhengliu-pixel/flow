package store

import (
	"context"
	"slices"

	"flow/api/internal/domain"
)

// LegacyIssueViewerPolicy projects only viewer membership, team policy, and
// the settings used by comment-action reads. It does not load issue records
// or the catalogs used to validate issue edits.
func (s *SQLiteStore) LegacyIssueViewerPolicy(ctx context.Context, workspace, userID string) (domain.Bootstrap, error) {
	data, err := s.commentActionPolicySnapshot(ctx, workspace, userID)
	if err != nil {
		return data, err
	}
	role, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, userID)
	if err != nil {
		return data, err
	}
	if status != "active" {
		return data, ErrAuthForbidden
	}
	user, err := s.authUserByID(ctx, userID)
	if err != nil {
		return data, err
	}
	membership, err := s.readIssueMemberships(ctx, data.Workspace.ID, userID, role)
	if err != nil {
		return data, err
	}
	data.Viewer, data.ViewerRole, data.TeamMembers = user, role, membership.Teams
	visible := make(map[string]bool, len(data.Teams))
	for _, team := range data.Teams {
		if teamVisibleToUser(data, team.ID, userID, role) {
			visible[team.ID] = true
		}
	}
	data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return !visible[team.ID] })
	data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(member domain.TeamMember) bool { return !visible[member.TeamID] })
	for id := range data.TeamSettings {
		if !visible[id] {
			delete(data.TeamSettings, id)
		}
	}
	return data, nil
}

func (s *SQLiteStore) commentActionPolicySnapshot(ctx context.Context, workspace, userID string) (domain.Bootstrap, error) {
	if s.BoundedMetadataEnabled() {
		return domain.Bootstrap{}, ErrBoundedMetadataCoverage
	}
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db && (workspace == "" || workspace == snapshot.workspace) {
		return commentActionPolicy(snapshot.metadata, userID), nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	installed, ok := s.workspaces[workspace]
	if !ok {
		return domain.Bootstrap{}, ErrAuthForbidden
	}
	return commentActionPolicy(installed, userID), nil
}

func commentActionPolicy(installed domain.Bootstrap, userID string) domain.Bootstrap {
	data := domain.Bootstrap{
		Workspace: installed.Workspace,
		Teams:     installed.Teams,
		WorkspaceSettings: domain.WorkspaceSettings{
			HIPAACompliance:    installed.WorkspaceSettings.HIPAACompliance,
			PreventGuestAgents: installed.WorkspaceSettings.PreventGuestAgents,
		},
	}
	if settings, ok := installed.UserSettings[userID]; ok {
		data.UserSettings = map[string]domain.UserSettings{userID: {
			UserID: settings.UserID, AgentEnabled: settings.AgentEnabled, SuggestedActionsOnComments: settings.SuggestedActionsOnComments,
		}}
	}
	data = cloneBootstrap(data)
	data.TeamSettings = make(map[string]domain.TeamSettings, len(installed.TeamSettings))
	for id, team := range installed.TeamSettings {
		data.TeamSettings[id] = domain.TeamSettings{
			Access: team.Access, ParentTeamID: team.ParentTeamID, MembershipRestriction: team.MembershipRestriction,
		}
	}
	for _, flag := range []string{"ai-agent", "ai"} {
		if enabled, ok := installed.WorkspaceSettings.FeatureFlags[flag]; ok {
			if data.WorkspaceSettings.FeatureFlags == nil {
				data.WorkspaceSettings.FeatureFlags = map[string]bool{}
			}
			data.WorkspaceSettings.FeatureFlags[flag] = enabled
		}
	}
	return data
}

// CommentActionReadMetadata selects actions and their raw validation inputs
// from the same installed generation, in source order. It deliberately
// preserves duplicate proposals and uses the raw assignee directory.
func (s *SQLiteStore) CommentActionReadMetadata(workspace, issueID, teamID string) domain.Bootstrap {
	if s.BoundedMetadataEnabled() {
		return domain.Bootstrap{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	installed := s.workspaces[workspace]
	var data domain.Bootstrap
	assignees, labels, states := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, action := range installed.CommentActions {
		if action.IssueID != issueID || action.Status == "dismissed" {
			continue
		}
		data.CommentActions = append(data.CommentActions, action)
		if action.Status == "accepted" {
			continue
		}
		switch args := action.Arguments; action.Action {
		case "assignee":
			if args.AssigneeID != nil && *args.AssigneeID != "" {
				assignees[*args.AssigneeID] = true
			}
		case "labels":
			if args.LabelIDs != nil && len(*args.LabelIDs) <= 50 {
				for _, id := range *args.LabelIDs {
					labels[id] = true
				}
			}
		case "status":
			if args.StateID != nil {
				states[*args.StateID] = true
			}
		}
	}
	if len(data.CommentActions) == 0 {
		return data
	}
	if len(assignees) != 0 {
		for _, user := range installed.Users {
			if assignees[user.ID] {
				data.Users = append(data.Users, domain.User{ID: user.ID, Active: user.Active, App: user.App})
			}
		}
	}
	if len(labels) != 0 {
		for _, label := range installed.Labels {
			if labels[label.ID] {
				data.Labels = append(data.Labels, domain.IssueLabel{ID: label.ID, Scope: label.Scope, ArchivedAt: label.ArchivedAt})
			}
		}
	}
	if len(labels) != 0 || len(states) != 0 {
		workflowTeam := teamID
		data.TeamSettings = map[string]domain.TeamSettings{}
		for current := teamID; ; {
			if _, seen := data.TeamSettings[current]; seen {
				break
			}
			settings := installed.TeamSettings[current]
			data.TeamSettings[current] = domain.TeamSettings{
				ParentTeamID: settings.ParentTeamID, InheritWorkflowStatuses: settings.InheritWorkflowStatuses,
			}
			if settings.ParentTeamID == "" {
				break
			}
			if current == workflowTeam && settings.InheritWorkflowStatuses {
				workflowTeam = settings.ParentTeamID
			}
			current = settings.ParentTeamID
		}
		if len(states) != 0 {
			// An unreferenced team state still disables the global fallback.
			specific := slices.ContainsFunc(installed.States, func(state domain.WorkflowState) bool { return state.TeamID == workflowTeam })
			for _, state := range installed.States {
				if states[state.ID] && (state.TeamID == workflowTeam || !specific && state.TeamID == "") {
					data.States = append(data.States, domain.WorkflowState{ID: state.ID, TeamID: state.TeamID})
				}
			}
		}
	}
	return cloneBootstrap(data)
}
