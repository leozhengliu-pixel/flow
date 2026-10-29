package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Initiative group bindings connect an initiative directly to a chat group,
// so its updates reach the group whether or not a team leads it. The
// initiative's owner or a workspace administrator connects it, with the same
// group checks as team bindings (an internal group of the application's
// tenant, with the bot in it) and an authorization record. The group gets the
// initiative's own updates only: never while a private team leads the
// initiative, and never a contributing team's content.

const (
	chatInitiativeBindingPermissionMessage    = "Initiative binding permission required"
	chatInitiativeBindingAuthorizationMessage = "Authorize sharing this initiative's updates with the group to save the binding"
	chatInitiativeBindingScopeMessage         = "Only initiatives without a lead team or led by a public team can be connected to a group"
	chatInitiativeBindingConflictMessage      = "This group already receives this initiative's updates"
	chatInitiativeNotFoundMessage             = "Initiative not found"
)

func init() {
	for message, code := range map[string]string{
		chatInitiativeBindingPermissionMessage:    "chat_initiative_binding_permission_required",
		chatInitiativeBindingAuthorizationMessage: "chat_initiative_binding_authorization_required",
		chatInitiativeBindingScopeMessage:         "chat_initiative_binding_scope",
		chatInitiativeBindingConflictMessage:      "chat_binding_conflict",
		chatInitiativeNotFoundMessage:             "chat_initiative_not_found",
	} {
		chatErrorCodes[message] = code
	}
	chatPermissionDomains["chat_initiative_binding_permission_required"] = "initiative"
	chatPermissionDomains["chat_initiative_binding_authorization_required"] = "initiative"
}

func (s *server) registerChatInitiativeBindingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/{provider}/chat/initiatives/{initiativeId}/bindings", s.listChatInitiativeBindings)
	mux.HandleFunc("POST /api/integrations/{provider}/chat/initiative-bindings", s.chatConfigurationMutation(s.saveChatInitiativeBinding))
	mux.HandleFunc("POST /api/integrations/{provider}/chat/initiative-bindings/{id}/revoke", s.chatConfigurationMutation(s.revokeChatInitiativeBinding))
	mux.HandleFunc("DELETE /api/integrations/{provider}/chat/initiative-bindings/{id}", s.chatConfigurationMutation(s.deleteChatInitiativeBinding))
}

func chatFindInitiative(data domain.Bootstrap, id string) (domain.Initiative, bool) {
	index := slices.IndexFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id })
	if index < 0 {
		return domain.Initiative{}, false
	}
	return data.Initiatives[index], true
}

// chatInitiativeShareable: the initiative's updates may be posted to a group.
// A private lead team owns the initiative's content, so it is never shared;
// contributing teams add nothing to the card, so a private one does not block
// it.
func chatInitiativeShareable(data domain.Bootstrap, initiative domain.Initiative) bool {
	if data.WorkspaceSettings.HIPAACompliance || initiative.ID == "" {
		return false
	}
	return initiative.LeadTeamID == "" || chatPublicTeam(data, initiative.LeadTeamID)
}

// chatInitiativeManager: the viewer may connect the initiative to groups, as
// its owner or a workspace administrator.
func (s *server) chatInitiativeManager(r *http.Request, data domain.Bootstrap, initiative domain.Initiative) bool {
	if s.authDisabled || s.applicationPolicyAdmin(r, data) {
		return true
	}
	return !strings.EqualFold(data.ViewerRole, "guest") && initiative.Owner != nil && initiative.Owner.ID != "" && initiative.Owner.ID == data.Viewer.ID
}

// chatInitiativeOwnerRequest lets an initiative's owner list the bot's groups
// (the group list takes ?initiativeId=).
func (s *server) chatInitiativeOwnerRequest(r *http.Request, data domain.Bootstrap) bool {
	id := r.URL.Query().Get("initiativeId")
	if id == "" || len(id) > 191 {
		return false
	}
	initiatives, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "initiatives")
	if !ok {
		return false
	}
	initiative, found := chatFindInitiative(initiatives, id)
	return found && s.chatInitiativeManager(r, data, initiative)
}

// chatInitiativeAuthorizerValid keeps a binding tied to its authorizer: it
// stops once they are neither the initiative's owner nor a workspace
// administrator (or left the workspace).
func (s *server) chatInitiativeAuthorizerValid(ctx context.Context, data domain.Bootstrap, binding domain.ChatInitiativeBinding, initiative domain.Initiative) bool {
	if binding.Authorization == nil || binding.Authorization.UserID == "" {
		return false
	}
	role, status, err := s.store.WorkspaceRole(ctx, data.Workspace.ID, binding.Authorization.UserID)
	if err != nil || status != "active" || strings.EqualFold(role, "guest") {
		return false
	}
	return workspaceAdminRole(role) || initiative.Owner != nil && initiative.Owner.ID == binding.Authorization.UserID
}

func chatInitiativeBindingActive(binding domain.ChatInitiativeBinding) bool {
	return binding.Enabled && binding.RevokedAt == nil
}

// chatInitiativeBindingSession resolves the caller, the tenant-authorized
// application and the workspace's initiatives.
func (s *server) chatInitiativeBindingSession(w http.ResponseWriter, r *http.Request) (domain.Bootstrap, domain.ChatIntegrationRecord, domain.Bootstrap, bool) {
	data, ok := s.chatSession(w, r, false)
	if !ok {
		return data, domain.ChatIntegrationRecord{}, domain.Bootstrap{}, false
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil {
		writeChatError(w, 404, "Chat application not configured")
		return data, record, domain.Bootstrap{}, false
	}
	metadata, found := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "workspace", "initiatives", "teams", "teamSettings", "workspaceSettings")
	if !found {
		writeChatError(w, 500, "Could not read chat integration")
		return data, record, metadata, false
	}
	return data, record, metadata, true
}

func (s *server) listChatInitiativeBindings(w http.ResponseWriter, r *http.Request) {
	data, record, metadata, ok := s.chatInitiativeBindingSession(w, r)
	if !ok {
		return
	}
	initiative, found := chatFindInitiative(metadata, r.PathValue("initiativeId"))
	if !found {
		writeChatError(w, 404, chatInitiativeNotFoundMessage)
		return
	}
	canManage := s.chatInitiativeManager(r, data, initiative)
	bindings := []domain.ChatInitiativeBinding{}
	// Who shares the initiative with which group is visible to the people who
	// may change it.
	if canManage {
		items, err := s.store.ChatInitiativeBindings(r.Context(), record.WorkspaceKey, record.ConnectionID, initiative.ID)
		if err != nil {
			writeChatError(w, 500, "Could not read group bindings")
			return
		}
		bindings = items
	}
	writeJSON(w, 200, map[string]any{
		"bindings":         bindings,
		"canManage":        canManage,
		"shareable":        chatInitiativeShareable(metadata, initiative),
		"tenantAuthorized": chatTenantAuthorized(record),
	})
}

func (s *server) chatInitiativeBindingForRequest(w http.ResponseWriter, r *http.Request, record domain.ChatIntegrationRecord, id string) (domain.ChatInitiativeBinding, bool) {
	binding, err := s.store.ChatInitiativeBinding(r.Context(), record.WorkspaceKey, record.ConnectionID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeChatError(w, 404, "Binding not found")
		return binding, false
	}
	if err != nil {
		writeChatError(w, 500, "Could not read group bindings")
		return binding, false
	}
	return binding, true
}

func (s *server) saveChatInitiativeBinding(w http.ResponseWriter, r *http.Request) {
	data, record, metadata, ok := s.chatInitiativeBindingSession(w, r)
	if !ok {
		return
	}
	var input struct {
		ID           string `json:"id,omitempty"`
		InitiativeID string `json:"initiativeId"`
		ChatID       string `json:"chatId"`
		ChatName     string `json:"chatName"`
		Enabled      bool   `json:"enabled"`
		// Authorize is the caller's explicit consent to share the
		// initiative's updates with everyone in the group.
		Authorize bool `json:"authorize"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ChatID == "" || len(input.ChatID) > 191 || len(input.ChatName) > 200 || input.InitiativeID == "" || len(input.InitiativeID) > 191 || len(input.ID) > 191 {
		writeChatError(w, 400, "Invalid group binding")
		return
	}
	initiative, found := chatFindInitiative(metadata, input.InitiativeID)
	if !found {
		writeChatError(w, 404, chatInitiativeNotFoundMessage)
		return
	}
	if !s.chatInitiativeManager(r, data, initiative) {
		writeChatError(w, 403, chatInitiativeBindingPermissionMessage)
		return
	}
	binding := domain.ChatInitiativeBinding{ID: input.ID, ConnectionID: record.ConnectionID, ChatID: input.ChatID, ChatName: input.ChatName, InitiativeID: initiative.ID, Enabled: input.Enabled, CreatedAt: time.Now().UTC()}
	var previous *domain.ChatInitiativeBinding
	if binding.ID != "" {
		existing, exists := s.chatInitiativeBindingForRequest(w, r, record, binding.ID)
		if !exists {
			return
		}
		// Moving a binding to another initiative needs permission on both.
		if current, ok := chatFindInitiative(metadata, existing.InitiativeID); ok && !s.chatInitiativeManager(r, data, current) || !ok && !s.applicationPolicyAdmin(r, data) {
			writeChatError(w, 403, chatInitiativeBindingPermissionMessage)
			return
		}
		if existing.RevokedAt != nil {
			writeChatError(w, 409, "This binding was revoked; create a new binding instead")
			return
		}
		binding.CreatedAt = existing.CreatedAt
		previous = &existing
	} else {
		id, err := randomSecret("chat_initiative_binding_")
		if err != nil {
			writeChatError(w, 500, "Could not create binding")
			return
		}
		binding.ID = id
	}
	if !input.Authorize {
		writeChatError(w, 400, chatInitiativeBindingAuthorizationMessage)
		return
	}
	if !chatInitiativeShareable(metadata, initiative) {
		writeChatError(w, 400, chatInitiativeBindingScopeMessage)
		return
	}
	if !chatTenantAuthorized(record) {
		writeChatError(w, 409, chatTenantRefusal(record))
		return
	}
	role := domain.ChatInitiativeBindingRoleOwner
	if s.applicationPolicyAdmin(r, data) && (initiative.Owner == nil || initiative.Owner.ID != data.Viewer.ID) {
		role = domain.ChatInitiativeBindingRoleWorkspaceAdmin
	}
	binding.Authorization = &domain.ChatInitiativeBindingAuthorization{UserID: data.Viewer.ID, Role: role, AuthorizedAt: time.Now().UTC()}
	s.learnChatTenantKey(r.Context(), &record)
	if err := s.chatGroupPermitted(r.Context(), record, binding.ChatID, true); err != nil {
		var refusal chatGroupRefusal
		if errors.As(err, &refusal) {
			writeChatError(w, 400, refusal.message)
		} else {
			writeChatError(w, 502, "Could not verify the group on the chat platform; check the bot's permissions and retry")
		}
		return
	}
	existing, err := s.store.ChatInitiativeBindings(r.Context(), record.WorkspaceKey, record.ConnectionID, initiative.ID)
	if err != nil {
		writeChatError(w, 500, "Could not read group bindings")
		return
	}
	for _, item := range existing {
		if item.ChatID != binding.ChatID || item.ID == binding.ID {
			continue
		}
		if item.RevokedAt == nil {
			writeChatError(w, 409, chatInitiativeBindingConflictMessage)
			return
		}
		// A revoked binding of the same group is replaced; its record
		// stays in the audit log.
		if err := s.store.DeleteChatInitiativeBinding(r.Context(), record.WorkspaceKey, record.ConnectionID, item.ID); err != nil {
			writeChatError(w, 500, "Could not save changes")
			return
		}
	}
	err = s.store.InitializeChatDomainCursor(r.Context(), record.WorkspaceKey)
	if err == nil {
		err = s.store.PutChatInitiativeBinding(r.Context(), record.WorkspaceKey, binding)
	}
	if err != nil {
		writeChatError(w, 409, chatInitiativeBindingConflictMessage)
		return
	}
	action := "chat_initiative_binding.authorized"
	if previous != nil {
		action = "chat_initiative_binding.updated"
	}
	s.auditChatInitiativeBinding(r, record, action, binding)
	writeJSON(w, 200, binding)
}

// chatInitiativeBindingManaged resolves a binding the caller may change: its
// initiative's owner or a workspace administrator (also for a binding whose
// initiative was deleted).
func (s *server) chatInitiativeBindingManaged(w http.ResponseWriter, r *http.Request) (domain.Bootstrap, domain.ChatIntegrationRecord, domain.ChatInitiativeBinding, bool) {
	data, record, metadata, ok := s.chatInitiativeBindingSession(w, r)
	if !ok {
		return data, record, domain.ChatInitiativeBinding{}, false
	}
	binding, found := s.chatInitiativeBindingForRequest(w, r, record, r.PathValue("id"))
	if !found {
		return data, record, binding, false
	}
	initiative, exists := chatFindInitiative(metadata, binding.InitiativeID)
	if exists && !s.chatInitiativeManager(r, data, initiative) || !exists && !s.applicationPolicyAdmin(r, data) {
		writeChatError(w, 403, chatInitiativeBindingPermissionMessage)
		return data, record, binding, false
	}
	return data, record, binding, true
}

func (s *server) revokeChatInitiativeBinding(w http.ResponseWriter, r *http.Request) {
	data, record, binding, ok := s.chatInitiativeBindingManaged(w, r)
	if !ok {
		return
	}
	if binding.RevokedAt == nil {
		now := time.Now().UTC()
		binding.RevokedAt, binding.RevokedBy, binding.Enabled = &now, data.Viewer.ID, false
		if err := s.store.PutChatInitiativeBinding(r.Context(), record.WorkspaceKey, binding); err != nil {
			writeChatError(w, 500, "Could not save changes")
			return
		}
		s.auditChatInitiativeBinding(r, record, "chat_initiative_binding.revoked", binding)
	}
	writeJSON(w, 200, binding)
}

func (s *server) deleteChatInitiativeBinding(w http.ResponseWriter, r *http.Request) {
	_, record, binding, ok := s.chatInitiativeBindingManaged(w, r)
	if !ok {
		return
	}
	err := s.store.DeleteChatInitiativeBinding(r.Context(), record.WorkspaceKey, record.ConnectionID, binding.ID)
	if err == nil {
		s.auditChatInitiativeBinding(r, record, "chat_initiative_binding.deleted", binding)
	}
	respondMutation(w, err, 204, nil)
}

func (s *server) auditChatInitiativeBinding(r *http.Request, record domain.ChatIntegrationRecord, action string, binding domain.ChatInitiativeBinding) {
	metadata := map[string]any{"provider": record.Provider, "connectionId": record.ConnectionID, "chatId": binding.ChatID, "chatName": binding.ChatName, "initiativeId": binding.InitiativeID, "enabled": binding.Enabled}
	if binding.Authorization != nil {
		metadata["authorizedBy"] = binding.Authorization.UserID
		metadata["authorizedAs"] = binding.Authorization.Role
		metadata["authorizedAt"] = binding.Authorization.AuthorizedAt
	}
	if binding.RevokedAt != nil {
		metadata["revokedBy"] = binding.RevokedBy
		metadata["revokedAt"] = *binding.RevokedAt
	}
	s.auditChatIntegration(r, record.WorkspaceKey, action, "chat_initiative_binding", binding.ID, metadata)
}

// queueChatInitiativeBindingEvent queues an initiative update for the groups
// the initiative is connected to directly.
func (s *server) queueChatInitiativeBindingEvent(ctx context.Context, workspace string, record domain.ChatIntegrationRecord, event domain.DomainEvent) error {
	if event.Type != "initiative.update_created" {
		return nil
	}
	bindings, err := s.store.ChatInitiativeBindings(ctx, workspace, record.ConnectionID, event.AggregateID)
	if err != nil || len(bindings) == 0 {
		return err
	}
	data, ok := s.store.WorkspaceMetadataFields(workspace, "workspace", "initiatives", "teams", "teamSettings", "workspaceSettings")
	if !ok {
		return chatInvalid("chat notification metadata unavailable")
	}
	initiative, found := chatFindInitiative(data, event.AggregateID)
	if !found || !chatInitiativeShareable(data, initiative) {
		return nil
	}
	for _, binding := range bindings {
		if !chatInitiativeBindingActive(binding) || !binding.CreatedAt.IsZero() && event.CreatedAt.Before(binding.CreatedAt) || !s.chatInitiativeAuthorizerValid(ctx, data, binding, initiative) {
			continue
		}
		key := chatKey(record.ConnectionID, "initiative-binding", binding.ID, event.ID)
		raw, _ := json.Marshal(chatNotificationJob{InitiativeBindingID: binding.ID, InitiativeID: initiative.ID, Kind: "initiative.update_created"})
		if _, err := s.store.EnqueueChatJob(ctx, domain.ChatJob{ID: "chat_job_" + key, WorkspaceKey: workspace, ConnectionID: record.ConnectionID, Kind: "notification", DedupKey: key, Payload: raw, RunAt: time.Now().UTC()}); err != nil {
			return err
		}
	}
	return nil
}

// chatSendInitiativeBindingUpdate delivers a queued initiative update after
// checking the binding, its authorizer and the group again.
func (s *server) chatSendInitiativeBindingUpdate(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, payload chatNotificationJob) error {
	binding, err := s.store.ChatInitiativeBinding(ctx, record.WorkspaceKey, record.ConnectionID, payload.InitiativeBindingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !chatInitiativeBindingActive(binding) || binding.InitiativeID != payload.InitiativeID {
		return nil
	}
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "workspace", "initiatives", "teams", "teamSettings", "workspaceSettings")
	if !ok {
		return chatInvalid("initiative metadata unavailable")
	}
	initiative, found := chatFindInitiative(data, binding.InitiativeID)
	if !found || !chatInitiativeShareable(data, initiative) || !s.chatInitiativeAuthorizerValid(ctx, data, binding, initiative) {
		return nil
	}
	if err := s.chatGroupPermitted(ctx, record, binding.ChatID, false); err != nil {
		var refusal chatGroupRefusal
		if errors.As(err, &refusal) {
			return chatInvalid("initiative group no longer permitted")
		}
		return err
	}
	slug := initiative.SlugID
	if slug == "" {
		slug = initiative.ID
	}
	link := fmt.Sprintf("%s/%s/initiative/%s/overview", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(slug))
	elements := []map[string]any{chatCardText(initiative.Name)}
	if health := chatInitiativeHealthLabel(initiative.Health); health != "" {
		elements = append(elements, chatCardText("Health: "+health))
	}
	elements = append(elements, map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatPlain("Open in Flow"), "url": link}}})
	content, _ := json.Marshal(chatCard("Initiative update", elements...))
	return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=chat_id", map[string]any{"receive_id": binding.ChatID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "initiative-binding")[:32]}, nil)
}

func chatInitiativeHealthLabel(health string) string {
	switch health {
	case "onTrack":
		return "On track"
	case "atRisk":
		return "At risk"
	case "offTrack":
		return "Off track"
	}
	return ""
}
