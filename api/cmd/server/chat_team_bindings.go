package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Team binding permission: a team's owners authorize sharing it with a chat
// group. Workspace administrators administer every team (store.TeamRole), so
// they hold this permission for all teams; no other member does. A binding
// never adds team memberships or widens who can read the team in Flow: its
// notifications are delivered to the group only, and group actions run with
// the acting member's own linked Flow identity and permissions.

// chatBindingEvents are the notification types a binding may share.
var chatBindingEvents = []string{"issue.created", "issue.status_changed", "issue.assignee_changed", "comment.created", "project.update_created", "initiative.update_created"}

type chatGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	External bool   `json:"external"`
}

// chatTeamState reports whether a team and its parent chain are active, and
// whether any of them is private or restricted.
func chatTeamState(data domain.Bootstrap, teamID string) (active, private bool) {
	seen := map[string]bool{}
	for current := teamID; current != ""; current = data.TeamSettings[current].ParentTeamID {
		if seen[current] {
			return false, private
		}
		seen[current] = true
		index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == current })
		if index < 0 {
			return false, private
		}
		team := data.Teams[index]
		if team.ArchivedAt != nil || team.RetiredAt != nil {
			return false, private
		}
		settings := data.TeamSettings[current]
		access := strings.ToLower(strings.TrimSpace(settings.Access))
		membership := strings.ToLower(strings.TrimSpace(settings.MembershipRestriction))
		if team.Private || access == "private" || access == "restricted" || membership == "members" || membership == "owners" {
			private = true
		}
	}
	return teamID != "", private
}

func chatPublicTeam(data domain.Bootstrap, teamID string) bool {
	active, private := chatTeamState(data, teamID)
	return active && !private
}

func chatPrivateBinding(binding domain.ChatBinding) bool {
	return binding.Authorization != nil && binding.Authorization.PrivateTeam
}

// chatTeamShareable: a public team, or the binding's own private team when the
// binding carries a private-team authorization.
func chatTeamShareable(data domain.Bootstrap, binding domain.ChatBinding, teamID string) bool {
	active, private := chatTeamState(data, teamID)
	return active && (!private || teamID == binding.TeamID && chatPrivateBinding(binding))
}

func chatBindingScopePermitted(data domain.Bootstrap, binding domain.ChatBinding) bool {
	if data.WorkspaceSettings.HIPAACompliance || binding.RevokedAt != nil || binding.TeamID == "" || !chatTeamShareable(data, binding, binding.TeamID) {
		return false
	}
	if binding.ProjectID != "" {
		found := false
		for _, project := range data.Projects {
			if project.ID != binding.ProjectID {
				continue
			}
			if project.ArchivedAt != nil || !slices.Contains(project.TeamIDs, binding.TeamID) {
				return false
			}
			// A project shared with another private team would disclose
			// that team's work; only public teams may join the bound one.
			for _, teamID := range project.TeamIDs {
				if !chatTeamShareable(data, binding, teamID) {
					return false
				}
			}
			found = true
		}
		if !found {
			return false
		}
	}
	if binding.TemplateID != "" {
		found := false
		for _, template := range data.IssueTemplates {
			if template.ID == binding.TemplateID {
				if template.TemplateType == "form" || len(template.FormFields) > 0 || (template.TeamID != "" && template.TeamID != binding.TeamID) || (template.VisibilityTeamID != "" && template.VisibilityTeamID != binding.TeamID) || (template.ProjectID != "" && template.ProjectID != binding.ProjectID) {
					return false
				}
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// chatBindingAuthorizerValid keeps a private team binding tied to the person
// who authorized it: once they can no longer administer the team (they left
// it, lost the owner role or the workspace), the binding stops.
func (s *server) chatBindingAuthorizerValid(ctx context.Context, workspace string, binding domain.ChatBinding) bool {
	if !chatPrivateBinding(binding) {
		return true
	}
	data, ok := s.store.WorkspaceMetadataFields(workspace, "workspace")
	if !ok || binding.Authorization.UserID == "" {
		return false
	}
	role, err := s.store.TeamRole(ctx, data.Workspace.ID, binding.TeamID, binding.Authorization.UserID)
	return err == nil && role == "owner"
}

type chatGroupRefusal struct{ message string }

func (e chatGroupRefusal) Error() string { return e.message }

// chatBindingPermitted re-checks a binding against the current workspace and
// the chat platform: the scope rule, the private-team authorizer, and that the
// group is an internal group of the application's tenant. checkMembership also
// asks the platform whether the bot is in the group (when saving).
func (s *server) chatBindingPermitted(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, checkMembership ...bool) error {
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "teams", "teamSettings", "projects", "issueTemplates", "workspaceSettings")
	if !ok || !chatBindingScopePermitted(data, binding) || !s.chatBindingAuthorizerValid(ctx, record.WorkspaceKey, binding) {
		return store.ErrAuthForbidden
	}
	var response struct {
		Data struct {
			Name      string `json:"name"`
			External  *bool  `json:"external"`
			TenantKey string `json:"tenant_key"`
			ChatMode  string `json:"chat_mode"`
		} `json:"data"`
	}
	if err := s.chatPlatformRequest(ctx, record, http.MethodGet, "/open-apis/im/v1/chats/"+url.PathEscape(binding.ChatID), nil, &response); err != nil {
		return err
	}
	if response.Data.TenantKey != record.TenantKey {
		return chatGroupRefusal{"This group belongs to another tenant"}
	}
	if response.Data.External == nil || *response.Data.External || (response.Data.ChatMode != "group" && response.Data.ChatMode != "topic") {
		return chatGroupRefusal{"Only internal groups of the application's tenant can be bound"}
	}
	if len(checkMembership) > 0 && checkMembership[0] {
		var member struct {
			Data struct {
				IsInChat bool `json:"is_in_chat"`
			} `json:"data"`
		}
		if err := s.chatPlatformRequest(ctx, record, http.MethodGet, "/open-apis/im/v1/chats/"+url.PathEscape(binding.ChatID)+"/members/is_in_chat", nil, &member); err != nil || !member.Data.IsInChat {
			return chatGroupRefusal{"Add the bot to this group before binding it"}
		}
	}
	return nil
}

// chatTeamOwner reports whether the viewer administers the team: a team
// owner (inherited from parent teams) or a workspace administrator.
func (s *server) chatTeamOwner(r *http.Request, data domain.Bootstrap, teamID string) bool {
	if s.authDisabled {
		return true
	}
	if strings.EqualFold(data.ViewerRole, "guest") || teamID == "" {
		return false
	}
	role, err := s.store.TeamRole(r.Context(), data.Workspace.ID, teamID, data.Viewer.ID)
	return err == nil && role == "owner"
}

// chatManageableTeamIDs lists the teams whose bindings the viewer may see and
// manage (team binding permission).
func (s *server) chatManageableTeamIDs(r *http.Request, data domain.Bootstrap) []string {
	ids := []string{}
	if !s.authDisabled && strings.EqualFold(data.ViewerRole, "guest") {
		return ids
	}
	teams, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "teams")
	if !ok {
		return ids
	}
	admin := s.applicationPolicyAdmin(r, data)
	for _, team := range teams.Teams {
		if admin || s.chatTeamOwner(r, data, team.ID) {
			ids = append(ids, team.ID)
		}
	}
	return ids
}

// chatTenantAuthorized: the automatic tenant check last succeeded.
func chatTenantAuthorized(record domain.ChatIntegrationRecord) bool {
	return record.Connected
}

// chatPermissions describes the three permission domains for the viewer.
func (s *server) chatPermissions(r *http.Request, data domain.Bootstrap, record domain.ChatIntegrationRecord, exists, canManage bool, manageable []string) map[string]any {
	connectedBy := ""
	for _, item := range data.IntegrationConnections {
		if item.ID == record.ConnectionID {
			connectedBy = item.ConnectedBy
		}
	}
	tenant := chatTenantPermission(record, exists, connectedBy)
	privateTeams := []string{}
	if len(manageable) > 0 {
		if teams, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "teams", "teamSettings"); ok {
			for _, id := range manageable {
				if _, private := chatTeamState(teams, id); private {
					privateTeams = append(privateTeams, id)
				}
			}
		}
	}
	return map[string]any{
		"tenant":    tenant,
		"workspace": map[string]any{"canManage": canManage},
		"teams":     map[string]any{"manageableTeamIds": manageable, "privateTeamIds": privateTeams},
	}
}

// chatBindingSession resolves the caller and the tenant-authorized
// application for a binding endpoint. Workspace administrators and team owners
// may continue; each handler then checks the specific team.
func (s *server) chatBindingSession(w http.ResponseWriter, r *http.Request) (domain.Bootstrap, domain.ChatIntegrationRecord, bool) {
	data, ok := s.chatSession(w, r, false)
	if !ok {
		return data, domain.ChatIntegrationRecord{}, false
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil {
		writeChatError(w, 404, "Chat application not configured")
		return data, record, false
	}
	if !s.applicationPolicyAdmin(r, data) && len(s.chatManageableTeamIDs(r, data)) == 0 {
		writeChatError(w, 403, chatTeamBindingPermissionMessage)
		return data, record, false
	}
	if !chatTenantAuthorized(record) {
		writeChatError(w, 409, chatTenantRefusal(record))
		return data, record, false
	}
	return data, record, true
}

func (s *server) listChatGroups(w http.ResponseWriter, r *http.Request) {
	_, record, ok := s.chatBindingSession(w, r)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > 2048 {
		writeChatError(w, 400, "Invalid cursor")
		return
	}
	query := url.Values{"page_size": {"50"}}
	if cursor != "" {
		query.Set("page_token", cursor)
	}
	var response chatPlatformGroupPage
	// The list uses the application's tenant token: nobody signs in for it.
	if err := s.chatPlatformRequest(r.Context(), record, http.MethodGet, "/open-apis/im/v1/chats?"+query.Encode(), nil, &response); err != nil {
		writeChatError(w, 502, "The chat platform request failed; check the bot's permissions and retry")
		return
	}
	// Groups are filtered by the application's tenant: read it now if the
	// last check could not, or, while the app lacks the tenant information
	// permission, take it from the bot's internal groups.
	if record.TenantKey == "" {
		if err := s.withChatConfigurationLock(r.Context(), record.WorkspaceKey, record.Provider, func() error {
			if !chatTenantKeyFallback(record) {
				s.readChatTenantKey(r.Context(), &record)
			}
			return s.adoptChatTenantKey(r.Context(), &record, chatInternalTenantKey(response.Data.Items))
		}); err != nil {
			writeChatError(w, 500, "Could not read chat integration")
			return
		}
	}
	items := []chatGroup{}
	for _, item := range response.Data.Items {
		if record.TenantKey != "" && item.TenantKey == record.TenantKey && item.External != nil && !*item.External {
			items = append(items, chatGroup{ID: item.ChatID, Name: item.Name})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "pageToken": response.Data.PageToken, "hasMore": response.Data.HasMore})
}

func (s *server) chatBindingByIDForRequest(w http.ResponseWriter, r *http.Request, record domain.ChatIntegrationRecord, id string) (domain.ChatBinding, bool) {
	items, err := s.store.ChatBindings(r.Context(), record.WorkspaceKey, record.ConnectionID)
	if err != nil {
		writeChatError(w, 500, "Could not read group bindings")
		return domain.ChatBinding{}, false
	}
	index := slices.IndexFunc(items, func(item domain.ChatBinding) bool { return item.ID == id })
	if index < 0 {
		writeChatError(w, 404, "Binding not found")
		return domain.ChatBinding{}, false
	}
	return items[index], true
}

func (s *server) saveChatBinding(w http.ResponseWriter, r *http.Request) {
	data, record, ok := s.chatBindingSession(w, r)
	if !ok {
		return
	}
	var input struct {
		domain.ChatBinding
		// Authorize is the caller's explicit consent to share the team's
		// selected updates and actions with everyone in the group.
		Authorize bool `json:"authorize"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	binding := input.ChatBinding
	if binding.ChatID == "" || len(binding.ChatID) > 191 || len(binding.ChatName) > 200 || len(binding.Events) > len(chatBindingEvents) || binding.TeamID == "" {
		writeChatError(w, 400, "Invalid group binding")
		return
	}
	for _, event := range binding.Events {
		if !slices.Contains(chatBindingEvents, event) {
			writeChatError(w, 400, "Unsupported notification event")
			return
		}
	}
	if !s.chatTeamOwner(r, data, binding.TeamID) {
		writeChatError(w, 403, chatTeamBindingPermissionMessage)
		return
	}
	binding.ConnectionID = record.ConnectionID
	binding.CreatedAt = time.Now().UTC()
	binding.RevokedAt, binding.RevokedBy = nil, ""
	var previous *domain.ChatBinding
	if binding.ID == "" {
		id, err := randomSecret("chat_binding_")
		if err != nil {
			writeChatError(w, 500, "Could not create binding")
			return
		}
		binding.ID = id
	} else {
		existing, found := s.chatBindingByIDForRequest(w, r, record, binding.ID)
		if !found {
			return
		}
		// Moving a binding to another team needs permission on both teams.
		if !s.chatTeamOwner(r, data, existing.TeamID) {
			writeChatError(w, 403, chatTeamBindingPermissionMessage)
			return
		}
		if existing.RevokedAt != nil {
			writeChatError(w, 409, "This binding was revoked; create a new binding instead")
			return
		}
		binding.CreatedAt = existing.CreatedAt
		previous = &existing
	}
	if !input.Authorize {
		writeChatError(w, 400, "Authorize sharing this team with the group to save the binding")
		return
	}
	teams, _ := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "teams", "teamSettings")
	_, private := chatTeamState(teams, binding.TeamID)
	role := domain.ChatBindingRoleTeamOwner
	if s.applicationPolicyAdmin(r, data) {
		role = domain.ChatBindingRoleWorkspaceAdmin
	}
	binding.Authorization = &domain.ChatBindingAuthorization{UserID: data.Viewer.ID, Role: role, AuthorizedAt: time.Now().UTC(), PrivateTeam: private}
	// Groups are compared with the application's tenant, so it must be known.
	s.learnChatTenantKey(r.Context(), &record)
	if err := s.chatBindingPermitted(r.Context(), record, binding, true); err != nil {
		var refusal chatGroupRefusal
		var platform *chatPlatformError
		switch {
		case errors.As(err, &refusal):
			writeChatError(w, 400, refusal.message)
		case errors.As(err, &platform):
			writeChatError(w, 502, "Could not verify the group on the chat platform; check the bot's permissions and retry")
		default:
			writeChatError(w, 400, "Select an active team and a compatible project and template")
		}
		return
	}
	// A revoked binding of the same group is replaced; its record stays in
	// the audit log.
	if previous == nil {
		items, err := s.store.ChatBindings(r.Context(), record.WorkspaceKey, record.ConnectionID)
		if err == nil {
			for _, item := range items {
				if item.ChatID == binding.ChatID && item.RevokedAt != nil {
					_ = s.store.DeleteChatBinding(r.Context(), record.WorkspaceKey, record.ConnectionID, item.ID)
				}
			}
		}
	}
	err := s.store.InitializeChatDomainCursor(r.Context(), record.WorkspaceKey)
	if err == nil {
		err = s.store.PutChatBinding(r.Context(), record.WorkspaceKey, binding)
	}
	if err != nil {
		writeChatError(w, 409, "Could not save binding; the group may already be bound")
		return
	}
	action := "chat_binding.authorized"
	if previous != nil {
		action = "chat_binding.updated"
	}
	s.auditChatBinding(r, record, action, binding)
	writeJSON(w, 200, binding)
}

func (s *server) revokeChatBinding(w http.ResponseWriter, r *http.Request) {
	data, record, ok := s.chatBindingSession(w, r)
	if !ok {
		return
	}
	binding, found := s.chatBindingByIDForRequest(w, r, record, r.PathValue("id"))
	if !found {
		return
	}
	if !s.chatTeamOwner(r, data, binding.TeamID) {
		writeChatError(w, 403, chatTeamBindingPermissionMessage)
		return
	}
	if binding.RevokedAt == nil {
		now := time.Now().UTC()
		binding.RevokedAt, binding.RevokedBy, binding.Enabled = &now, data.Viewer.ID, false
		if err := s.store.PutChatBinding(r.Context(), record.WorkspaceKey, binding); err != nil {
			writeChatError(w, 500, "Could not save changes")
			return
		}
		s.auditChatBinding(r, record, "chat_binding.revoked", binding)
	}
	writeJSON(w, 200, binding)
}

func (s *server) deleteChatBinding(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, false)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil {
		writeChatError(w, 404, "Connection not found")
		return
	}
	items, err := s.store.ChatBindings(r.Context(), record.WorkspaceKey, record.ConnectionID)
	if err != nil {
		writeChatError(w, 500, "Could not read group bindings")
		return
	}
	index := slices.IndexFunc(items, func(item domain.ChatBinding) bool { return item.ID == r.PathValue("id") })
	if index < 0 {
		w.WriteHeader(204)
		return
	}
	if !s.chatTeamOwner(r, data, items[index].TeamID) {
		writeChatError(w, 403, chatTeamBindingPermissionMessage)
		return
	}
	err = s.store.DeleteChatBinding(r.Context(), record.WorkspaceKey, record.ConnectionID, items[index].ID)
	if err == nil {
		s.auditChatBinding(r, record, "chat_binding.deleted", items[index])
	}
	respondMutation(w, err, 204, nil)
}

func (s *server) auditChatBinding(r *http.Request, record domain.ChatIntegrationRecord, action string, binding domain.ChatBinding) {
	metadata := map[string]any{"provider": record.Provider, "connectionId": record.ConnectionID, "chatId": binding.ChatID, "chatName": binding.ChatName, "teamId": binding.TeamID, "events": binding.Events, "allowCreate": binding.AllowCreate, "allowUpdate": binding.AllowUpdate, "enabled": binding.Enabled}
	if binding.ProjectID != "" {
		metadata["projectId"] = binding.ProjectID
	}
	if binding.Authorization != nil {
		metadata["authorizedBy"] = binding.Authorization.UserID
		metadata["authorizedAs"] = binding.Authorization.Role
		metadata["authorizedAt"] = binding.Authorization.AuthorizedAt
		metadata["privateTeam"] = binding.Authorization.PrivateTeam
	}
	if binding.RevokedAt != nil {
		metadata["revokedBy"] = binding.RevokedBy
		metadata["revokedAt"] = *binding.RevokedAt
	}
	s.auditChatIntegration(r, record.WorkspaceKey, action, "chat_binding", binding.ID, metadata)
}

func (s *server) auditChatIntegration(r *http.Request, workspace, action, resourceType, resourceID string, metadata map[string]any) {
	s.auditChatIntegrationContext(r.Context(), workspace, action, resourceType, resourceID, metadata)
}

func (s *server) auditChatIntegrationContext(ctx context.Context, workspace, action, resourceType, resourceID string, metadata map[string]any) {
	ctx = store.WithMetadataFields(context.WithoutCancel(ctx), "auditLog")
	if err := s.store.MutateWorkspace(ctx, workspace, "audit.chat_integration", resourceID, map[string]any{"action": action}, func(data *domain.Bootstrap) error {
		appendAudit(data, action, resourceType, resourceID, metadata)
		return nil
	}); err != nil {
		log.Printf("chat integration audit workspace=%s action=%s: %v", workspace, action, err)
	}
}
