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
// whether any of them is private: private or restricted access, or a
// membership limited to members or owners (such a team is not visible to
// non-members, as in issue visibility).
func chatTeamState(data domain.Bootstrap, teamID string) (active, private bool) {
	active, privacy := chatTeamPrivacy(data, teamID)
	return active, privacy != ""
}

// Why a team counts as private for group bindings.
const (
	chatTeamPrivateAccess = "private"
	chatTeamMembersOnly   = "members_only"
)

// chatTeamPrivacy is chatTeamState with the kind of privacy: private access
// on the team or a parent (chatTeamPrivateAccess) wins over a public team
// limited to its members or owners (chatTeamMembersOnly); "" for a public team.
func chatTeamPrivacy(data domain.Bootstrap, teamID string) (active bool, privacy string) {
	seen := map[string]bool{}
	for current := teamID; current != ""; current = data.TeamSettings[current].ParentTeamID {
		if seen[current] {
			return false, privacy
		}
		seen[current] = true
		index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == current })
		if index < 0 {
			return false, privacy
		}
		team := data.Teams[index]
		if team.ArchivedAt != nil || team.RetiredAt != nil {
			return false, privacy
		}
		settings := data.TeamSettings[current]
		access := strings.ToLower(strings.TrimSpace(settings.Access))
		membership := strings.ToLower(strings.TrimSpace(settings.MembershipRestriction))
		if team.Private || access == "private" || access == "restricted" {
			privacy = chatTeamPrivateAccess
		} else if (membership == "members" || membership == "owners") && privacy == "" {
			privacy = chatTeamMembersOnly
		}
	}
	return teamID != "", privacy
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
	return chatBindingScopeBlock(data, binding) == ""
}

// chatBindingScopeBlock is the scope rule with its reason: the first
// domain.ChatBindingBlocked code that stops the binding, or "" when its scope
// may be shared.
func chatBindingScopeBlock(data domain.Bootstrap, binding domain.ChatBinding) string {
	if reason := chatBindingTeamBlock(data, binding); reason != "" {
		return reason
	}
	if binding.TemplateID != "" && !chatBindingTemplatePermitted(data, binding, binding.TemplateID) {
		return domain.ChatBindingBlockedTemplate
	}
	return ""
}

// chatBindingTeamBlock is the scope rule without the binding's template:
// workspace, revocation, team and projects.
func chatBindingTeamBlock(data domain.Bootstrap, binding domain.ChatBinding) string {
	switch {
	case data.WorkspaceSettings.HIPAACompliance:
		return domain.ChatBindingBlockedHIPAA
	case binding.RevokedAt != nil:
		return domain.ChatBindingBlockedRevoked
	}
	if binding.TeamID == "" {
		return domain.ChatBindingBlockedTeamInactive
	}
	active, privacy := chatTeamPrivacy(data, binding.TeamID)
	switch {
	case !active:
		return domain.ChatBindingBlockedTeamInactive
	case privacy == chatTeamPrivateAccess && !chatPrivateBinding(binding):
		return domain.ChatBindingBlockedTeamPrivate
	case privacy == chatTeamMembersOnly && !chatPrivateBinding(binding):
		return domain.ChatBindingBlockedTeamMembersOnly
	case !chatBindingProjectsPermitted(data, binding):
		return domain.ChatBindingBlockedProjects
	}
	return ""
}

// chatBindingView is a binding as the settings list it: with BlockedReason,
// the domain.ChatBindingBlocked code that stops it now by the same rules
// queueChatDomainEvent applies (empty when it may deliver).
type chatBindingView struct {
	domain.ChatBinding
	BlockedReason string `json:"blockedReason,omitempty"`
}

// chatBindingViews adds each binding's current blocked reason, read from the
// same metadata and rules as event delivery in both metadata modes.
func (s *server) chatBindingViews(ctx context.Context, workspace string, bindings []domain.ChatBinding) ([]chatBindingView, error) {
	views := make([]chatBindingView, len(bindings))
	for index, binding := range bindings {
		views[index].ChatBinding = binding
	}
	if len(bindings) == 0 {
		return views, nil
	}
	reasons := make([]string, len(bindings))
	if s.store.BoundedMetadataEnabled() {
		owner, found, err := s.store.BeginInstalledMetadataRead(ctx, workspace)
		if err != nil || !found {
			return nil, chatBindingViewsUnavailable(err)
		}
		defer owner.Close()
		bound, err := owner.Context(ctx)
		if err != nil {
			return nil, err
		}
		pulse, found, err := s.store.BeginPulseRead(bound, workspace, false)
		if err != nil || !found {
			return nil, chatBindingViewsUnavailable(err)
		}
		defer pulse.Close()
		policy, found, err := s.store.BeginChatDisplayRead(bound, workspace, store.ChatDisplayRequest{Policy: true})
		if err != nil || !found {
			return nil, chatBindingViewsUnavailable(err)
		}
		defer policy.Close()
		var ids []string
		for _, binding := range bindings {
			if binding.TemplateID != "" {
				ids = append(ids, binding.TemplateID)
			}
		}
		var templates []store.ChatEventTemplateHeader
		if len(ids) > 0 {
			read, found, err := s.store.BeginChatEventPolicyRead(bound, workspace, store.ChatEventPolicyRequest{TemplateIDs: ids})
			if err != nil || !found {
				return nil, chatBindingViewsUnavailable(err)
			}
			defer read.Close()
			templates = read.Templates
		}
		data := policy.Data
		data.Projects = pulse.Snapshot.Projects
		for index, binding := range bindings {
			reasons[index] = chatEventBindingScopeBlock(data, binding, templates)
		}
	} else {
		data, ok := s.store.WorkspaceMetadataFields(workspace, "teams", "teamSettings", "projects", "issueTemplates", "workspaceSettings")
		if !ok {
			return nil, chatBindingViewsUnavailable(nil)
		}
		for index, binding := range bindings {
			reasons[index] = chatBindingScopeBlock(data, binding)
		}
	}
	for index, binding := range bindings {
		if reasons[index] == "" && !s.chatBindingAuthorizerValid(ctx, workspace, binding) {
			reasons[index] = domain.ChatBindingBlockedAuthorizer
		}
		views[index].BlockedReason = reasons[index]
	}
	return views, nil
}

func chatBindingViewsUnavailable(err error) error {
	if err != nil {
		return err
	}
	return errors.New("chat binding policy metadata unavailable")
}

// chatBindingTemplatePermitted: a plain issue template of the binding's team
// (or of every team) that fits the binding's projects.
func chatBindingTemplatePermitted(data domain.Bootstrap, binding domain.ChatBinding, templateID string) bool {
	found := false
	for _, template := range data.IssueTemplates {
		if template.ID == templateID {
			if template.TemplateType == "form" || len(template.FormFields) > 0 || (template.TeamID != "" && template.TeamID != binding.TeamID) || (template.VisibilityTeamID != "" && template.VisibilityTeamID != binding.TeamID) || (template.ProjectID != "" && !chatBindingLimitedToProject(binding, template.ProjectID)) {
				return false
			}
			found = true
		}
	}
	return found
}

// chatValidRequestSettings checks a binding's request settings as saved:
// the reaction's emoji type, a priority from 0 to 4, and a template the
// binding may use.
func chatValidRequestSettings(data domain.Bootstrap, binding domain.ChatBinding) bool {
	requests := binding.Requests
	if requests == nil {
		return true
	}
	return (requests.Emoji == "" || chatRequestEmoji.MatchString(requests.Emoji)) && (requests.Priority == nil || *requests.Priority >= 0 && *requests.Priority <= 4) && (requests.TemplateID == "" || chatBindingTemplatePermitted(data, binding, requests.TemplateID))
}

// chatBindingAuthorizerValid keeps a private team binding tied to the person
// who authorized it: once they can no longer administer the team (they left
// it, lost the owner role or the workspace), the binding stops.
func (s *server) chatBindingAuthorizerValid(ctx context.Context, workspace string, binding domain.ChatBinding) bool {
	if !chatPrivateBinding(binding) {
		return true
	}
	data, ok, err := s.store.WorkspaceMetadataFieldsScoped(ctx, workspace, store.MetadataRecordScope{}, "workspace")
	if err != nil {
		return false
	}
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
	if s.store.BoundedMetadataEnabled() {
		owner, found, err := s.store.BeginInstalledMetadataRead(ctx, record.WorkspaceKey)
		if err != nil {
			return err
		}
		if !found {
			return store.ErrAuthForbidden
		}
		defer owner.Close()
		bound, err := owner.Context(ctx)
		if err != nil {
			return err
		}
		pulse, found, err := s.store.BeginPulseRead(bound, record.WorkspaceKey, false)
		if err != nil {
			return err
		}
		if !found {
			return store.ErrAuthForbidden
		}
		defer pulse.Close()
		policy, found, err := s.store.BeginChatDisplayRead(bound, record.WorkspaceKey, store.ChatDisplayRequest{Policy: true})
		if err != nil {
			return err
		}
		if !found {
			return store.ErrAuthForbidden
		}
		defer policy.Close()
		var headers []store.ChatEventTemplateHeader
		if binding.TemplateID != "" {
			templates, found, err := s.store.BeginChatEventPolicyRead(bound, record.WorkspaceKey, store.ChatEventPolicyRequest{TemplateIDs: []string{binding.TemplateID}})
			if err != nil {
				return err
			}
			if !found {
				return store.ErrAuthForbidden
			}
			defer templates.Close()
			headers = templates.Templates
		}
		data := policy.Data
		data.Projects = pulse.Snapshot.Projects
		if !chatEventBindingScopePermitted(data, binding, headers) || !s.chatBindingAuthorizerValid(ctx, record.WorkspaceKey, binding) {
			return store.ErrAuthForbidden
		}
		return s.chatGroupPermitted(ctx, record, binding.ChatID, len(checkMembership) > 0 && checkMembership[0])
	}
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "teams", "teamSettings", "projects", "issueTemplates", "workspaceSettings")
	if !ok || !chatBindingScopePermitted(data, binding) || !s.chatBindingAuthorizerValid(ctx, record.WorkspaceKey, binding) {
		return store.ErrAuthForbidden
	}
	return s.chatGroupPermitted(ctx, record, binding.ChatID, len(checkMembership) > 0 && checkMembership[0])
}

// chatGroupPermitted asks the chat platform whether the group is an internal
// group of the application's tenant and, when checkMembership is set, whether
// the bot is in it. Team and initiative bindings share it.
func (s *server) chatGroupPermitted(ctx context.Context, record domain.ChatIntegrationRecord, chatID string, checkMembership bool) error {
	_, err := s.chatGroupDetails(ctx, record, chatID, checkMembership)
	return err
}

// chatGroupDetails is chatGroupPermitted with the group's name.
func (s *server) chatGroupDetails(ctx context.Context, record domain.ChatIntegrationRecord, chatID string, checkMembership bool) (string, error) {
	var response struct {
		Data struct {
			Name      string `json:"name"`
			External  *bool  `json:"external"`
			TenantKey string `json:"tenant_key"`
			ChatMode  string `json:"chat_mode"`
		} `json:"data"`
	}
	if err := s.chatPlatformRequest(ctx, record, http.MethodGet, "/open-apis/im/v1/chats/"+url.PathEscape(chatID), nil, &response); err != nil {
		return "", err
	}
	if response.Data.TenantKey != record.TenantKey {
		return "", chatGroupRefusal{"This group belongs to another tenant"}
	}
	if response.Data.External == nil || *response.Data.External || (response.Data.ChatMode != "group" && response.Data.ChatMode != "topic") {
		return "", chatGroupRefusal{"Only internal groups of the application's tenant can be bound"}
	}
	if checkMembership {
		var member struct {
			Data struct {
				IsInChat bool `json:"is_in_chat"`
			} `json:"data"`
		}
		if err := s.chatPlatformRequest(ctx, record, http.MethodGet, "/open-apis/im/v1/chats/"+url.PathEscape(chatID)+"/members/is_in_chat", nil, &member); err != nil || !member.Data.IsInChat {
			return "", chatGroupRefusal{"Add the bot to this group before binding it"}
		}
	}
	return response.Data.Name, nil
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
	// membersOnlyTeamIds: the private teams that are private only because
	// their membership (or a parent's) is limited to members or owners.
	privateTeams, membersOnlyTeams := []string{}, []string{}
	if len(manageable) > 0 {
		if teams, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "teams", "teamSettings"); ok {
			for _, id := range manageable {
				_, privacy := chatTeamPrivacy(teams, id)
				if privacy != "" {
					privateTeams = append(privateTeams, id)
				}
				if privacy == chatTeamMembersOnly {
					membersOnlyTeams = append(membersOnlyTeams, id)
				}
			}
		}
	}
	return map[string]any{
		"tenant":    tenant,
		"workspace": map[string]any{"canManage": canManage},
		"teams":     map[string]any{"manageableTeamIds": manageable, "privateTeamIds": privateTeams, "membersOnlyTeamIds": membersOnlyTeams},
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
	if !s.applicationPolicyAdmin(r, data) && len(s.chatManageableTeamIDs(r, data)) == 0 && !s.chatInitiativeOwnerRequest(r, data) {
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
		// Language is read apart so that a save without it keeps the
		// binding's language.
		Language *string `json:"language"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	binding := input.ChatBinding
	if input.Language != nil {
		binding.Language = *input.Language
	}
	binding.NormalizeProjects()
	if binding.ChatID == "" || len(binding.ChatID) > 191 || len(binding.ChatName) > 200 || len(binding.Events) > len(chatBindingEvents) || binding.TeamID == "" || len(binding.ProjectIDs) > chatBindingMaxProjects {
		writeChatError(w, 400, "Invalid group binding")
		return
	}
	if !slices.Contains(chatBindingLanguages, binding.Language) {
		writeChatError(w, 400, "Unsupported group language")
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
	if requests := binding.Requests; requests != nil {
		if templates, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "issueTemplates", "projects"); !ok || !chatValidRequestSettings(templates, binding) {
			writeChatError(w, 400, "Choose a supported reaction, a priority and a template of the binding's team for requests")
			return
		}
		if !requests.Enabled && !requests.EveryMention && requests.Emoji == "" && requests.TemplateID == "" && requests.Priority == nil {
			binding.Requests = nil
		}
	}
	binding.ConnectionID = record.ConnectionID
	binding.CreatedAt = time.Now().UTC()
	binding.RevokedAt, binding.RevokedBy = nil, ""
	// A saved binding passed the sharing rules again; earlier skips are
	// history (the audit log and server log keep them).
	binding.LastSkipped = nil
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
		if input.Language == nil {
			binding.Language = existing.Language
		}
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
	if len(binding.ProjectIDs) > 0 {
		metadata["projectIds"] = binding.ProjectIDs
	}
	if requests := binding.Requests; requests != nil {
		settings := map[string]any{"enabled": requests.Enabled, "everyMention": requests.EveryMention, "emoji": requests.Emoji, "templateId": requests.TemplateID}
		if requests.Priority != nil {
			settings["priority"] = *requests.Priority
		}
		metadata["requests"] = settings
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
	if err := s.store.MutateWorkspaceScoped(ctx, workspace, "audit.chat_integration", resourceID, map[string]any{"action": action}, store.MetadataRecordScope{}, func(data *domain.Bootstrap) error {
		appendAudit(data, action, resourceType, resourceID, metadata)
		return nil
	}); err != nil {
		log.Printf("chat integration audit workspace=%s action=%s: %v", workspace, action, err)
	}
}
