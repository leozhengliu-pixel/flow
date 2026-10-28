package main

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Enterprise tenant authorization is its own permission domain. It is done
// once, on the chat platform, by an account that can authorize the
// application for the whole tenant: Flow verifies that the account is a
// tenant administrator of the tenant the application is installed in. A Flow
// workspace role never stands in for it. Once it is complete, workspace
// administrators manage the integration (group list, bindings) with the
// application's own tenant token; nobody is asked to confirm the tenant again.

type chatPlatformProfile struct {
	OpenID, TenantKey, Name string
}

// startChatTenantAuthorization begins the tenant authorization sign-in. A
// workspace administrator or the member they designated may start it; the
// platform account that signs in must be a tenant administrator.
func (s *server) startChatTenantAuthorization(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, false)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil {
		writeChatError(w, 400, "Configure the application first")
		return
	}
	if !s.applicationPolicyAdmin(r, data) && (record.TenantAuthorizerID == "" || record.TenantAuthorizerID != data.Viewer.ID) {
		writeChatError(w, 403, "Only a workspace administrator or the designated tenant authorizer can start tenant authorization")
		return
	}
	if _, err := s.chatCredentials(r.Context(), record); err != nil {
		writeChatError(w, 500, "Application credentials unavailable")
		return
	}
	s.startChatOAuth(w, r, data, record, "tenant")
}

// designateChatTenantAuthorizer lets a workspace administrator name the member
// who holds tenant authorization capability on the chat platform. An empty
// userId clears the designation.
func (s *server) designateChatTenantAuthorizer(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, true)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil {
		writeChatError(w, 400, "Configure the application first")
		return
	}
	var input struct {
		UserID string `json:"userId"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.UserID != "" && !s.authDisabled && !slices.ContainsFunc(data.Members, func(member domain.WorkspaceMember) bool {
		return member.User.ID == input.UserID && member.Status == "active" && !strings.EqualFold(member.Role, "guest")
	}) {
		writeChatError(w, 400, "Choose an active member of this workspace")
		return
	}
	record.TenantAuthorizerID = input.UserID
	if err = s.store.PutChatIntegration(r.Context(), record); err != nil {
		writeChatError(w, 500, "Could not save changes")
		return
	}
	s.auditChatIntegration(r, record.WorkspaceKey, "chat_tenant.authorizer_designated", "chat_integration", record.ConnectionID, map[string]any{"provider": record.Provider, "userId": input.UserID})
	updated, _ := s.chatSettingsMetadata(r)
	s.writeChatSettings(w, r, updated)
}

// finishChatTenantAuthorization completes the tenant sign-in after the
// platform identity was verified. The account must be a tenant administrator
// of the application's own tenant.
func (s *server) finishChatTenantAuthorization(w http.ResponseWriter, r *http.Request, record domain.ChatIntegrationRecord, pending chatOAuthState, profile chatPlatformProfile, workspaceAdmin bool, fail func(string)) {
	// The starter's capacity is checked again at completion.
	if !workspaceAdmin && (record.TenantAuthorizerID == "" || record.TenantAuthorizerID != pending.UserID) {
		fail("Only a workspace administrator or the designated tenant authorizer can start tenant authorization")
		return
	}
	var tenant struct {
		Data struct {
			Tenant struct {
				TenantKey string `json:"tenant_key"`
			} `json:"tenant"`
		} `json:"data"`
	}
	if err := s.chatPlatformRequest(r.Context(), record, http.MethodGet, "/open-apis/tenant/v2/tenant/query", nil, &tenant); err != nil || tenant.Data.Tenant.TenantKey == "" {
		fail("Could not verify the application's tenant; check the application's contact and tenant permissions")
		return
	}
	if tenant.Data.Tenant.TenantKey != profile.TenantKey {
		fail("This account belongs to another tenant")
		return
	}
	var user struct {
		Data struct {
			User struct {
				IsTenantManager bool `json:"is_tenant_manager"`
			} `json:"user"`
		} `json:"data"`
	}
	err := s.chatPlatformRequest(r.Context(), record, http.MethodGet, "/open-apis/contact/v3/users/"+url.PathEscape(profile.OpenID)+"?user_id_type=open_id", nil, &user)
	if err != nil {
		fail("Could not verify the application's tenant; check the application's contact and tenant permissions")
		return
	}
	if !user.Data.User.IsTenantManager {
		fail("This chat account is not a tenant administrator of the application's tenant")
		return
	}
	record.TenantKey = profile.TenantKey
	record.Connected = true
	record.TenantAuthorization = &domain.ChatTenantAuthorization{UserID: pending.UserID, OpenID: profile.OpenID, Name: profile.Name, TenantKey: profile.TenantKey, AuthorizedAt: time.Now().UTC(), Method: domain.ChatTenantMethodTenantManager}
	if err = s.store.PutChatIntegration(r.Context(), record); err != nil {
		fail("Application tenant is already connected to another workspace")
		return
	}
	if err = s.syncChatConnection(r.Context(), record, pending.UserID, "connected", "success", ""); err != nil {
		fail("Could not save connection metadata")
		return
	}
	s.auditChatIntegration(r, record.WorkspaceKey, "chat_tenant.authorized", "chat_integration", record.ConnectionID, map[string]any{"provider": record.Provider, "tenantKey": record.TenantKey, "authorizedBy": pending.UserID, "platformOpenId": profile.OpenID, "platformName": profile.Name})
	http.Redirect(w, r, s.chatOAuthLanding(pending)+"?chat_auth=success&chat_provider="+record.Provider+"&chat_tenant=authorized", http.StatusSeeOther)
}
