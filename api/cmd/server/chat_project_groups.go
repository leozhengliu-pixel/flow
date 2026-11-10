package main

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"

	"flow/api/internal/domain"
)

// A project's connected groups are part of the project as its viewers see it,
// like a Slack property's #channel: anyone who can open the project learns the
// names of the groups that receive its notifications. Only the display name
// is shared; the binding's configuration (events, authorizer, request
// settings) and the team-level binding list stay with those who manage it.

const chatProjectNotFoundMessage = "Project not found"

func init() {
	chatErrorCodes[chatProjectNotFoundMessage] = "chat_project_not_found"
}

// chatProjectGroup is the read-only view of a group binding that covers a
// project.
type chatProjectGroup struct {
	ID       string `json:"id"`
	ChatName string `json:"chatName"`
	Provider string `json:"provider"`
}

func (s *server) registerChatProjectGroupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/{provider}/chat/projects/{projectId}/groups", s.listChatProjectGroups)
}

// chatProjectGroupVisible: the binding names this project in its scope, is
// active, and belongs to one of the project's teams the viewer can see (a
// project shared with a private team never discloses that team's group).
func chatProjectGroupVisible(data domain.Bootstrap, project domain.Project, binding domain.ChatBinding) bool {
	if !binding.Enabled || binding.RevokedAt != nil || !chatBindingLimitedToProject(binding, project.ID) {
		return false
	}
	return slices.Contains(project.TeamIDs, binding.TeamID) && slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == binding.TeamID })
}

// listChatProjectGroups lists the groups a project is connected to for
// anyone who can see the project; others get 404, as for any project read.
// canManage tells the viewer whether they own one of the project's teams and
// may connect or change its groups.
func (s *server) listChatProjectGroups(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, false)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	index := domain.ProjectIndexBySlugRef(data.Projects, r.PathValue("projectId"))
	if index < 0 {
		writeChatError(w, 404, chatProjectNotFoundMessage)
		return
	}
	project := data.Projects[index]
	manageable := s.chatManageableTeamIDs(r, data)
	canManage := slices.ContainsFunc(project.TeamIDs, func(id string) bool { return slices.Contains(manageable, id) })
	groups := []chatProjectGroup{}
	provider := r.PathValue("provider")
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), provider)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeChatError(w, 500, "Could not read chat integration")
		return
	}
	if err == nil {
		bindings, err := s.store.ChatBindings(r.Context(), record.WorkspaceKey, record.ConnectionID)
		if err != nil {
			writeChatError(w, 500, "Could not read group bindings")
			return
		}
		for _, binding := range bindings {
			if chatProjectGroupVisible(data, project, binding) {
				groups = append(groups, chatProjectGroup{ID: binding.ID, ChatName: binding.ChatName, Provider: record.Provider})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"groups": groups, "canManage": canManage})
}
