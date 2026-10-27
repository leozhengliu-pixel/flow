package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"golang.org/x/oauth2"
)

type chatCredentials struct {
	AppSecret         string `json:"appSecret"`
	EncryptKey        string `json:"encryptKey"`
	VerificationToken string `json:"verificationToken"`
}

type chatPlatformError struct {
	Code       int
	Status     int
	RetryAfter time.Duration
	Retryable  bool
}

func (e *chatPlatformError) Error() string {
	return fmt.Sprintf("Chat platform request failed (HTTP %d, code %d)", e.Status, e.Code)
}

type chatCachedToken struct {
	Token     string
	ExpiresAt time.Time
}

var chatTokenCache = struct {
	sync.Mutex
	items map[string]chatCachedToken
}{items: map[string]chatCachedToken{}}
var chatTokenLocks [64]sync.Mutex
var chatConfigLocks [64]sync.Mutex

func chatProvider(provider string) bool { return provider == "feishu" || provider == "lark" }
func chatBaseURL(provider string) string {
	if provider == "feishu" {
		return "https://open.feishu.cn"
	}
	if provider == "lark" {
		return "https://open.larksuite.com"
	}
	return ""
}
func chatCredentialID(record domain.ChatIntegrationRecord) string {
	return store.ChatCredentialID(record.ConnectionID)
}
func (s *server) chatIntegrationForProvider(ctx context.Context, workspace, provider string) (domain.ChatIntegrationRecord, error) {
	return s.store.ChatIntegration(ctx, workspace, provider)
}
func (s *server) chatCredentials(ctx context.Context, record domain.ChatIntegrationRecord) (chatCredentials, error) {
	var credential chatCredentials
	raw, err := s.store.ReadConnectorSecret(ctx, chatCredentialID(record), false)
	if err == nil {
		err = json.Unmarshal(raw, &credential)
	}
	return credential, err
}
func chatHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, Transport: operatorCATransport(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Only fixed provider hosts are reachable; neither connection metadata nor
// callback payloads can choose an OAuth issuer or API origin.
func chatRequest(ctx context.Context, endpoint, method, token string, body any, result any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !slices.Contains([]string{"open.feishu.cn", "open.larksuite.com", "accounts.feishu.cn"}, u.Hostname()) {
		return errInvalid
	}
	var data []byte
	contentType := "application/json"
	if form, ok := body.(url.Values); ok {
		data = []byte(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	} else if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := chatHTTPClient().Do(req)
	if err != nil {
		return &chatPlatformError{Retryable: true}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return &chatPlatformError{Status: response.StatusCode, Retryable: true}
	}
	var envelope struct {
		Code  int    `json:"code"`
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return &chatPlatformError{Status: response.StatusCode, Retryable: response.StatusCode >= 500}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Code != 0 || envelope.Error != "" {
		retryAfter, _ := strconv.Atoi(response.Header.Get("Retry-After"))
		if retryAfter == 0 {
			if retryAt, e := http.ParseTime(response.Header.Get("Retry-After")); e == nil {
				retryAfter = int(time.Until(retryAt).Seconds())
			}
		}
		return &chatPlatformError{Code: envelope.Code, Status: response.StatusCode, RetryAfter: time.Duration(max(0, min(retryAfter, 3600))) * time.Second, Retryable: response.StatusCode == 429 || response.StatusCode >= 500 || envelope.Code == 99991400 || envelope.Code == 99991401}
	}
	if result != nil && json.Unmarshal(raw, result) != nil {
		return errors.New("Invalid chat platform response")
	}
	return nil
}

func obtainChatTenantToken(ctx context.Context, record domain.ChatIntegrationRecord, credential chatCredentials) (chatCachedToken, error) {
	var response struct {
		Token  string `json:"tenant_access_token"`
		Expire int    `json:"expire"`
	}
	err := chatRequest(ctx, chatBaseURL(record.Provider)+"/open-apis/auth/v3/tenant_access_token/internal", http.MethodPost, "", map[string]string{"app_id": record.AppID, "app_secret": credential.AppSecret}, &response)
	if err != nil {
		return chatCachedToken{}, err
	}
	if response.Token == "" || response.Expire < 1 {
		return chatCachedToken{}, errors.New("Chat application did not return a tenant token")
	}
	return chatCachedToken{Token: response.Token, ExpiresAt: time.Now().Add(time.Duration(min(response.Expire, 24*3600)) * time.Second)}, nil
}
func (s *server) chatTenantToken(ctx context.Context, record domain.ChatIntegrationRecord) (string, error) {
	credential, err := s.chatCredentials(ctx, record)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(record.Provider + "\x00" + record.AppID + "\x00" + credential.AppSecret))
	key := fmt.Sprintf("%x", digest)
	lock := &chatTokenLocks[int(digest[0])%len(chatTokenLocks)]
	lock.Lock()
	defer lock.Unlock()
	chatTokenCache.Lock()
	cached := chatTokenCache.items[key]
	chatTokenCache.Unlock()
	if cached.Token != "" && cached.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		return cached.Token, nil
	}
	cached, err = obtainChatTenantToken(ctx, record, credential)
	if err != nil {
		return "", err
	}
	chatTokenCache.Lock()
	if len(chatTokenCache.items) >= 256 {
		clear(chatTokenCache.items)
	}
	chatTokenCache.items[key] = cached
	chatTokenCache.Unlock()
	return cached.Token, nil
}
func (s *server) chatPlatformRequest(ctx context.Context, record domain.ChatIntegrationRecord, method, path string, body any, result any) error {
	if !strings.HasPrefix(path, "/open-apis/") || strings.Contains(path, "\\") || strings.Contains(path, "//") {
		return errInvalid
	}
	token, err := s.chatTenantToken(ctx, record)
	if err != nil {
		return err
	}
	if (method == http.MethodPost || method == http.MethodPatch) && strings.Contains(path, "/im/v1/messages") {
		scope := store.ChatRecordID("rate", record.Provider, record.AppID, record.TenantKey)
		at, err := s.store.ReserveChatRequest(ctx, scope, 250*time.Millisecond, time.Now())
		if err != nil {
			return &chatPlatformError{Retryable: true, RetryAfter: time.Second}
		}
		if wait := time.Until(at); wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	err = chatRequest(ctx, chatBaseURL(record.Provider)+path, method, token, body, result)
	var platformErr *chatPlatformError
	if errors.As(err, &platformErr) && (platformErr.Status == 401 || platformErr.Code == 99991663 || platformErr.Code == 99991668) {
		if credential, e := s.chatCredentials(ctx, record); e == nil {
			invalidateChatTenantToken(record, credential)
			token, e = s.chatTenantToken(ctx, record)
			if e != nil {
				return e
			}
			return chatRequest(ctx, chatBaseURL(record.Provider)+path, method, token, body, result)
		}
	}
	return err
}
func invalidateChatTenantToken(record domain.ChatIntegrationRecord, credential chatCredentials) {
	digest := sha256.Sum256([]byte(record.Provider + "\x00" + record.AppID + "\x00" + credential.AppSecret))
	chatTokenCache.Lock()
	delete(chatTokenCache.items, fmt.Sprintf("%x", digest))
	chatTokenCache.Unlock()
}

func chatProbe(ctx context.Context, record domain.ChatIntegrationRecord, credential chatCredentials) (string, string, error) {
	token, err := obtainChatTenantToken(ctx, record, credential)
	if err != nil {
		return "", "", err
	}
	var response struct {
		Bot struct {
			OpenID string `json:"open_id"`
			Name   string `json:"app_name"`
		} `json:"bot"`
	}
	err = chatRequest(ctx, chatBaseURL(record.Provider)+"/open-apis/bot/v3/info/", http.MethodGet, token.Token, nil, &response)
	if err != nil {
		return "", "", err
	}
	if response.Bot.OpenID == "" {
		return "", "", errors.New("Enable and publish the application bot before connecting")
	}
	return response.Bot.OpenID, response.Bot.Name, nil
}

func (s *server) registerChatIntegrationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/{provider}/chat", s.chatSettings)
	mux.HandleFunc("PUT /api/integrations/{provider}/chat", s.chatConfigurationMutation(s.saveChatIntegration))
	mux.HandleFunc("DELETE /api/integrations/{provider}/chat", s.chatConfigurationMutation(s.disconnectChatIntegration))
	mux.HandleFunc("POST /api/integrations/{provider}/chat/test", s.chatConfigurationMutation(s.testChatIntegration))
	mux.HandleFunc("GET /api/integrations/{provider}/chat/chats", s.listChatGroups)
	mux.HandleFunc("POST /api/integrations/{provider}/chat/bindings", s.chatConfigurationMutation(s.saveChatBinding))
	mux.HandleFunc("DELETE /api/integrations/{provider}/chat/bindings/{id}", s.chatConfigurationMutation(s.deleteChatBinding))
	mux.HandleFunc("POST /api/integrations/{provider}/chat/identity/start", s.chatConfigurationMutation(s.startChatIdentity))
	mux.HandleFunc("DELETE /api/integrations/{provider}/chat/identity", s.chatConfigurationMutation(s.deleteChatIdentity))
	mux.HandleFunc("GET /api/integrations/{provider}/chat/identity/oauth/callback", s.finishChatIdentity)
}
func (s *server) withChatConfigurationLock(ctx context.Context, workspace, provider string, action func() error) error {
	digest := sha256.Sum256([]byte(workspace + "\x00" + provider))
	lock := &chatConfigLocks[int(digest[0])%len(chatConfigLocks)]
	lock.Lock()
	defer lock.Unlock()
	if s.coordinator != nil {
		return s.coordinator.WithWorkspaceLock(ctx, "chat-configuration-"+fmt.Sprintf("%x", digest), action)
	}
	return action()
}
func (s *server) chatConfigurationMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := s.withChatConfigurationLock(r.Context(), workspaceKey(r), r.PathValue("provider"), func() error { next(w, r); return nil })
		if err != nil {
			writeError(w, 503, "Chat configuration is busy; retry shortly")
		}
	}
}
func (s *server) chatSettingsMetadata(r *http.Request) (domain.Bootstrap, error) {
	if s.authDisabled {
		data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
		if !ok {
			return data, errNotFound
		}
		data.ViewerRole = "admin"
		return data, nil
	}
	return s.store.PagedWorkspaceMetadata(r.Context(), workspaceKey(r), authUser(r).ID)
}
func (s *server) chatSession(w http.ResponseWriter, r *http.Request, admin bool) (domain.Bootstrap, bool) {
	if !chatProvider(r.PathValue("provider")) {
		writeError(w, 404, "Unsupported chat provider")
		return domain.Bootstrap{}, false
	}
	data, err := s.chatSettingsMetadata(r)
	if err != nil {
		writeError(w, 403, "Workspace access required")
		return data, false
	}
	if !s.authDisabled {
		if r.Header.Get("Authorization") != "" {
			writeError(w, 403, "Interactive sign-in required")
			return data, false
		}
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, 401, "Interactive sign-in required")
			return data, false
		}
		user, err := s.store.AuthenticateSession(r.Context(), cookie.Value)
		if err != nil || user.ID != data.Viewer.ID {
			writeError(w, 401, "Interactive sign-in required")
			return data, false
		}
		policy, exists := s.store.WorkspaceMetadataFields(workspaceKey(r), "identityProviders")
		if !exists {
			writeError(w, 403, "Workspace policy unavailable")
			return data, false
		}
		data.IdentityProviders = policy.IdentityProviders
		auth, err := s.store.SessionAuthentication(r.Context(), cookie.Value)
		if err != nil {
			writeError(w, 503, "Could not verify session authentication")
			return data, false
		}
		if !allowedAuthenticationMethod(data, data.ViewerRole, auth) || (data.WorkspaceSettings.RequireTwoFactor && auth.MFAVerifiedAt == nil) {
			writeError(w, 403, "Your session does not meet the workspace authentication policy")
			return data, false
		}
	}
	if admin && !s.applicationPolicyAdmin(r, data) {
		writeError(w, 403, "Workspace administrator required")
		return data, false
	}
	return data, true
}

func (s *server) chatAppOrigin() string {
	return strings.TrimRight(firstNonEmpty(s.allowedOrigin, os.Getenv("FLOW_APP_URL"), "http://localhost:5173"), "/")
}
func (s *server) chatCallbackURL(provider string) string {
	return s.chatAppOrigin() + "/api/integrations/" + provider + "/chat/identity/oauth/callback"
}
func (s *server) chatSettings(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, false)
	if !ok {
		return
	}
	s.writeChatSettings(w, r, data)
}
func (s *server) writeChatSettings(w http.ResponseWriter, r *http.Request, data domain.Bootstrap) {
	w.Header().Set("Cache-Control", "no-store")
	result := map[string]any{"connection": nil, "identity": nil, "bindings": []domain.ChatBinding{}, "credentialsConfigured": false, "webhookURL": "", "canManage": s.applicationPolicyAdmin(r, data)}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, "Could not read chat integration")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		for _, item := range data.IntegrationConnections {
			if item.Provider == r.PathValue("provider") {
				safe := redactIntegrationConnection(item)
				safe.Status = "error"
				safe.LastError = "Application setup is incomplete; disconnect and configure it again"
				result["connection"] = safe
				break
			}
		}
	}
	if err == nil {
		for _, item := range data.IntegrationConnections {
			if item.ID == record.ConnectionID {
				safe := redactIntegrationConnection(item)
				result["connection"] = safe
				break
			}
		}
		if result["connection"] == nil {
			status := "pending_authorization"
			if record.Connected {
				status = "connected"
			}
			result["connection"] = domain.IntegrationConnection{ID: record.ConnectionID, Provider: record.Provider, Name: record.Name, Status: status, UpdatedAt: record.UpdatedAt, Config: map[string]string{"appId": record.AppID, "tenantKey": record.TenantKey, "botOpenId": record.BotOpenID}, Scopes: []string{}, Channels: []string{}}
		}
		_, credentialErr := s.chatCredentials(r.Context(), record)
		result["credentialsConfigured"] = credentialErr == nil
		if credentialErr != nil {
			item := result["connection"].(domain.IntegrationConnection)
			item.Status = "error"
			item.LastError = "Application credentials are unavailable; restore the encryption key or reconnect the application"
			result["connection"] = item
		}
		if result["canManage"] == true {
			bindings, err := s.store.ChatBindings(r.Context(), record.WorkspaceKey, record.ConnectionID)
			if err != nil {
				writeError(w, 500, "Could not read group bindings")
				return
			}
			result["bindings"] = bindings
			result["webhookURL"] = s.chatAppOrigin() + "/api/integrations/" + record.Provider + "/" + record.ConnectionID + "/chat/webhook"
		}
		identity, err := s.store.ChatIdentityForUser(r.Context(), record.WorkspaceKey, record.ConnectionID, data.Viewer.ID)
		if err == nil {
			result["identity"] = identity
		}
	}
	writeJSON(w, 200, result)
}

func (s *server) syncChatConnection(ctx context.Context, record domain.ChatIntegrationRecord, userID, status, testStatus, lastError string) error {
	return s.store.MutateWorkspace(store.WithMetadataFields(ctx, "integrationConnections"), record.WorkspaceKey, "integration.updated", record.ConnectionID, nil, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		index := slices.IndexFunc(data.IntegrationConnections, func(c domain.IntegrationConnection) bool { return c.ID == record.ConnectionID })
		if index < 0 {
			data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: record.ConnectionID, Provider: record.Provider, ConnectedBy: userID, CreatedAt: now, Scopes: []string{"im:message:send_as_bot", "im:message.group_at_msg:readonly", "im:chat:read"}, Channels: []string{}})
			index = len(data.IntegrationConnections) - 1
		}
		item := &data.IntegrationConnections[index]
		item.Name = record.Name
		item.Status = status
		item.UpdatedAt = now
		item.Config = map[string]string{"appId": record.AppID, "tenantKey": record.TenantKey, "botOpenId": record.BotOpenID}
		item.OAuthAccessToken = ""
		item.OAuthRefreshToken = ""
		item.OAuthState = ""
		item.LastError = lastError
		if testStatus != "" {
			item.LastTestAt = &now
			item.LastTestStatus = testStatus
			if testStatus == "success" {
				item.LastTestStatus = "ready"
			}
			if testStatus == "failed" {
				item.LastTestStatus = "error"
			}
		}
		return nil
	})
}

func (s *server) recordChatDeliveryResult(ctx context.Context, record domain.ChatIntegrationRecord, deliveryErr error) error {
	return s.withChatConfigurationLock(ctx, record.WorkspaceKey, record.Provider, func() error {
		current, err := s.store.ChatIntegrationByID(ctx, record.Provider, record.ConnectionID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.WorkspaceKey != record.WorkspaceKey || !current.Connected {
			return nil
		}
		return s.store.MutateWorkspace(store.WithMetadataFields(ctx, "integrationConnections"), record.WorkspaceKey, "integration.updated", record.ConnectionID, nil, func(data *domain.Bootstrap) error {
			for index := range data.IntegrationConnections {
				item := &data.IntegrationConnections[index]
				if item.ID != record.ConnectionID {
					continue
				}
				now := time.Now().UTC()
				if deliveryErr == nil {
					item.LastDeliveryAt = &now
					if strings.HasPrefix(item.LastError, "Chat delivery: ") {
						item.LastError = ""
					}
				} else {
					message := "Chat delivery: request failed. Check bot permissions, group access and application credentials."
					var platformErr *chatPlatformError
					if errors.As(deliveryErr, &platformErr) {
						message = "Chat delivery: " + platformErr.Error()
					}
					if item.LastError == message {
						return store.ErrNoMutation
					}
					item.LastError = message
				}
				item.UpdatedAt = now
				break
			}
			return nil
		})
	})
}

func (s *server) saveChatIntegration(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, true)
	if !ok {
		return
	}
	if !store.ConnectorSecretsConfigured() {
		writeError(w, 503, "Configure FLOW_CONNECTOR_SECRET_KEY before connecting chat applications")
		return
	}
	var input struct {
		Name              string `json:"name"`
		AppID             string `json:"appId"`
		AppSecret         string `json:"appSecret"`
		EncryptKey        string `json:"encryptKey"`
		VerificationToken string `json:"verificationToken"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.AppID = strings.TrimSpace(input.AppID)
	input.Name = strings.TrimSpace(input.Name)
	if input.AppID == "" || len(input.AppID) > 191 || len(input.Name) > 200 || len(input.AppSecret) > 4096 || len(input.EncryptKey) > 4096 || len(input.VerificationToken) > 4096 {
		writeError(w, 400, "Invalid application configuration")
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, "Could not read chat integration")
		return
	}
	credential := chatCredentials{}
	if err == nil {
		credential, err = s.chatCredentials(r.Context(), record)
		if err != nil {
			writeError(w, 500, "Could not read encrypted application credentials")
			return
		}
	}
	if record.ConnectionID != "" && record.AppID != input.AppID {
		writeError(w, 400, "Disconnect the current application before changing App ID")
		return
	}
	if record.ConnectionID == "" {
		id, err := randomSecret("chat_")
		if err != nil {
			writeError(w, 500, "Could not create connection")
			return
		}
		record = domain.ChatIntegrationRecord{WorkspaceKey: workspaceKey(r), Provider: r.PathValue("provider"), ConnectionID: id, AppID: input.AppID}
	}
	if input.AppSecret != "" {
		credential.AppSecret = input.AppSecret
	}
	if input.EncryptKey != "" {
		credential.EncryptKey = input.EncryptKey
	}
	if input.VerificationToken != "" {
		credential.VerificationToken = input.VerificationToken
	}
	if credential.AppSecret == "" || credential.EncryptKey == "" || credential.VerificationToken == "" {
		writeError(w, 400, "App Secret, Encrypt Key and Verification Token are required")
		return
	}
	botID, botName, err := chatProbe(r.Context(), record, credential)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	record.BotOpenID = botID
	record.Name = firstNonEmpty(input.Name, botName, record.Provider)
	if err = s.store.InitializeChatDomainCursor(r.Context(), record.WorkspaceKey); err != nil {
		writeError(w, 500, "Could not initialize notification cursor")
		return
	}
	raw, _ := json.Marshal(credential)
	if err = s.store.PutChatIntegration(r.Context(), record); err != nil {
		writeError(w, 409, "Application is already connected to another workspace")
		return
	}
	if err = s.store.PutConnectorSecret(r.Context(), chatCredentialID(record), raw, time.Now().AddDate(10, 0, 0)); err != nil {
		writeError(w, 500, "Could not save encrypted application credentials")
		return
	}
	status := "pending_authorization"
	if record.Connected {
		status = "connected"
	}
	if err = s.syncChatConnection(r.Context(), record, data.Viewer.ID, status, "success", ""); err != nil {
		writeError(w, 500, "Could not save connection metadata")
		return
	}
	updated, _ := s.chatSettingsMetadata(r)
	s.writeChatSettings(w, r, updated)
}

func (s *server) disconnectChatIntegration(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, true)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, "Could not read integration")
		return
	}
	ids := []string{}
	if record.ConnectionID != "" {
		ids = append(ids, record.ConnectionID)
	}
	for _, item := range data.IntegrationConnections {
		if item.Provider == r.PathValue("provider") && !slices.Contains(ids, item.ID) {
			ids = append(ids, item.ID)
		}
	}
	err = nil
	for _, id := range ids {
		candidate := domain.ChatIntegrationRecord{WorkspaceKey: workspaceKey(r), Provider: r.PathValue("provider"), ConnectionID: id}
		if credential, e := s.chatCredentials(r.Context(), candidate); e == nil {
			candidate.AppID = record.AppID
			invalidateChatTenantToken(candidate, credential)
		}
		if err = s.store.DeleteChatIntegration(r.Context(), candidate.WorkspaceKey, id); err != nil {
			break
		}
		if err = s.store.DeleteConnectorSecret(r.Context(), chatCredentialID(candidate)); err != nil {
			break
		}
	}
	if err == nil {
		err = s.store.MutateWorkspace(store.WithMetadataFields(r.Context(), "integrationConnections"), workspaceKey(r), "integration.disconnected", record.ConnectionID, nil, func(data *domain.Bootstrap) error {
			data.IntegrationConnections = slices.DeleteFunc(data.IntegrationConnections, func(c domain.IntegrationConnection) bool { return c.Provider == r.PathValue("provider") })
			return nil
		})
	}
	respondMutation(w, err, 204, nil)
}
func (s *server) testChatIntegration(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, true)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil {
		writeError(w, 404, "Chat application not configured")
		return
	}
	credential, err := s.chatCredentials(r.Context(), record)
	if err == nil {
		record.BotOpenID, _, err = chatProbe(r.Context(), record, credential)
	}
	if err != nil {
		_ = s.syncChatConnection(r.Context(), record, data.Viewer.ID, "error", "failed", err.Error())
		writeError(w, 502, "Chat application check failed; verify app credentials and bot publication")
		return
	}
	if err = s.store.PutChatIntegration(r.Context(), record); err != nil {
		writeError(w, 500, "Could not save application health")
		return
	}
	status := "pending_authorization"
	if record.Connected {
		status = "connected"
	}
	if err = s.syncChatConnection(r.Context(), record, data.Viewer.ID, status, "success", ""); err != nil {
		writeError(w, 500, "Could not save application health")
		return
	}
	updated, _ := s.chatSettingsMetadata(r)
	s.writeChatSettings(w, r, updated)
}

type chatGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	External bool   `json:"external"`
}

func (s *server) listChatGroups(w http.ResponseWriter, r *http.Request) {
	_, ok := s.chatSession(w, r, true)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil || !record.Connected {
		writeError(w, 400, "Authorize an application administrator to verify the tenant first")
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > 2048 {
		writeError(w, 400, "Invalid cursor")
		return
	}
	query := url.Values{"page_size": {"50"}}
	if cursor != "" {
		query.Set("page_token", cursor)
	}
	var response struct {
		Data struct {
			Items []struct {
				ChatID    string `json:"chat_id"`
				Name      string `json:"name"`
				TenantKey string `json:"tenant_key"`
				External  *bool  `json:"external"`
			} `json:"items"`
			PageToken string `json:"page_token"`
			HasMore   bool   `json:"has_more"`
		} `json:"data"`
	}
	if err = s.chatPlatformRequest(r.Context(), record, http.MethodGet, "/open-apis/im/v1/chats?"+query.Encode(), nil, &response); err != nil {
		writeError(w, 502, err.Error())
		return
	}
	items := []chatGroup{}
	for _, item := range response.Data.Items {
		if item.TenantKey == record.TenantKey && item.External != nil && !*item.External {
			items = append(items, chatGroup{ID: item.ChatID, Name: item.Name})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "pageToken": response.Data.PageToken, "hasMore": response.Data.HasMore})
}

func chatPublicTeam(data domain.Bootstrap, teamID string) bool {
	seen := map[string]bool{}
	for current := teamID; current != ""; current = data.TeamSettings[current].ParentTeamID {
		if seen[current] {
			return false
		}
		seen[current] = true
		index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == current })
		if index < 0 {
			return false
		}
		team := data.Teams[index]
		if team.Private || team.ArchivedAt != nil || team.RetiredAt != nil {
			return false
		}
		settings := data.TeamSettings[current]
		access := strings.ToLower(strings.TrimSpace(settings.Access))
		membership := strings.ToLower(strings.TrimSpace(settings.MembershipRestriction))
		if access == "private" || access == "restricted" || membership == "members" || membership == "owners" {
			return false
		}
	}
	return teamID != ""
}
func chatBindingScopePermitted(data domain.Bootstrap, binding domain.ChatBinding) bool {
	if data.WorkspaceSettings.HIPAACompliance || !chatPublicTeam(data, binding.TeamID) {
		return false
	}
	if binding.ProjectID != "" {
		found := false
		for _, project := range data.Projects {
			if project.ID != binding.ProjectID {
				continue
			}
			if project.ArchivedAt != nil {
				return false
			}
			if !slices.Contains(project.TeamIDs, binding.TeamID) {
				return false
			}
			for _, teamID := range project.TeamIDs {
				if !chatPublicTeam(data, teamID) {
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
func (s *server) chatBindingPermitted(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding) error {
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "teams", "teamSettings", "projects", "issueTemplates", "workspaceSettings")
	if !ok || !chatBindingScopePermitted(data, binding) {
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
	if response.Data.External == nil || *response.Data.External || response.Data.TenantKey != record.TenantKey || (response.Data.ChatMode != "group" && response.Data.ChatMode != "topic") {
		return store.ErrAuthForbidden
	}
	return nil
}
func (s *server) saveChatBinding(w http.ResponseWriter, r *http.Request) {
	_, ok := s.chatSession(w, r, true)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil || !record.Connected {
		writeError(w, 400, "Connect and authorize the application first")
		return
	}
	var input domain.ChatBinding
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ChatID == "" || len(input.ChatID) > 191 || len(input.ChatName) > 200 || len(input.Events) > 5 {
		writeError(w, 400, "Invalid group binding")
		return
	}
	for _, event := range input.Events {
		if !slices.Contains([]string{"issue.created", "issue.status_changed", "issue.assignee_changed", "comment.created", "project.update_created"}, event) {
			writeError(w, 400, "Unsupported notification event")
			return
		}
	}
	input.ConnectionID = record.ConnectionID
	input.CreatedAt = time.Now().UTC()
	if input.ID == "" {
		input.ID, err = randomSecret("chat_binding_")
	} else {
		items, e := s.store.ChatBindings(r.Context(), record.WorkspaceKey, record.ConnectionID)
		if e != nil {
			writeError(w, 500, "Could not read group bindings")
			return
		}
		index := slices.IndexFunc(items, func(item domain.ChatBinding) bool { return item.ID == input.ID })
		if index < 0 {
			writeError(w, 404, "Binding not found")
			return
		}
		input.CreatedAt = items[index].CreatedAt
	}
	if err != nil {
		writeError(w, 500, "Could not create binding")
		return
	}
	if err = s.chatBindingPermitted(r.Context(), record, input); err != nil {
		writeError(w, 400, "Select a same-tenant internal group, an active public team and compatible public project/template")
		return
	}
	if err = s.store.InitializeChatDomainCursor(r.Context(), record.WorkspaceKey); err == nil {
		err = s.store.PutChatBinding(r.Context(), record.WorkspaceKey, input)
	}
	if err != nil {
		writeError(w, 409, "Could not save binding; the group may already be bound")
		return
	}
	writeJSON(w, 200, input)
}
func (s *server) deleteChatBinding(w http.ResponseWriter, r *http.Request) {
	_, ok := s.chatSession(w, r, true)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil {
		writeError(w, 404, "Connection not found")
		return
	}
	err = s.store.DeleteChatBinding(r.Context(), record.WorkspaceKey, record.ConnectionID, r.PathValue("id"))
	respondMutation(w, err, 204, nil)
}

type chatOAuthState struct {
	WorkspaceKey, ConnectionID, Provider, AppID, UserID, Verifier, SessionHash, RedirectURI string
	Authentication                                                                          store.SessionAuthentication
	CanEstablishTenant                                                                      bool
}

func (s *server) startChatIdentity(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, false)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if err != nil {
		writeError(w, 400, "Configure the application first")
		return
	}
	admin := s.applicationPolicyAdmin(r, data)
	if !record.Connected && !admin {
		writeError(w, 403, "An administrator must verify the application tenant first")
		return
	}
	state, err := randomSecret("chat_oauth_")
	if err != nil {
		writeError(w, 500, "Could not begin authorization")
		return
	}
	pending := chatOAuthState{WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Provider: record.Provider, AppID: record.AppID, UserID: data.Viewer.ID, Verifier: oauth2.GenerateVerifier(), RedirectURI: s.chatCallbackURL(record.Provider), CanEstablishTenant: admin}
	if !s.authDisabled {
		cookie, _ := r.Cookie(sessionCookieName)
		pending.SessionHash = secretHash(cookie.Value)
		pending.Authentication, err = s.store.SessionAuthentication(r.Context(), cookie.Value)
		if err != nil {
			writeError(w, 500, "Could not verify session authentication")
			return
		}
	} else {
		pending.Authentication.Provider = "development"
	}
	raw, _ := json.Marshal(pending)
	if err = s.store.PutConnectorSecret(r.Context(), state, raw, time.Now().Add(10*time.Minute)); err != nil {
		writeError(w, 500, "Could not save authorization state")
		return
	}
	// The action row makes pending OAuth states revocable by disconnect.
	if err = s.store.PutChatAction(r.Context(), domain.ChatAction{ID: state, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, UserID: data.Viewer.ID, Payload: json.RawMessage(`{"kind":"oauth"}`), ExpiresAt: time.Now().Add(10 * time.Minute)}); err != nil {
		_ = s.store.DeleteConnectorSecret(r.Context(), state)
		writeError(w, 500, "Could not save authorization state")
		return
	}
	host := "https://accounts.feishu.cn"
	if record.Provider == "lark" {
		host = "https://accounts.larksuite.com"
	}
	config := oauth2.Config{ClientID: record.AppID, RedirectURL: pending.RedirectURI, Endpoint: oauth2.Endpoint{AuthURL: host + "/open-apis/authen/v1/authorize"}}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"authorizeUrl": config.AuthCodeURL(state, oauth2.S256ChallengeOption(pending.Verifier))})
}

func (s *server) finishChatIdentity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	state := r.URL.Query().Get("state")
	if !chatProvider(r.PathValue("provider")) || !strings.HasPrefix(state, "chat_oauth_") || len(state) > 150 {
		writeError(w, 400, "Invalid authorization state")
		return
	}
	raw, err := s.store.ReadConnectorSecret(r.Context(), state, false)
	var pending chatOAuthState
	if err != nil || json.Unmarshal(raw, &pending) != nil || pending.Provider != r.PathValue("provider") {
		writeError(w, 400, "Authorization expired")
		return
	}
	err = s.withChatConfigurationLock(r.Context(), pending.WorkspaceKey, pending.Provider, func() error { s.finishChatIdentityLocked(w, r, state, pending); return nil })
	if err != nil {
		writeError(w, 503, "Chat configuration is busy; restart authorization")
	}
}

func (s *server) finishChatIdentityLocked(w http.ResponseWriter, r *http.Request, state string, pending chatOAuthState) {
	fail := func(message string) {
		if wantsHTMLRedirect(r) {
			http.Redirect(w, r, s.chatAppOrigin()+"/"+url.PathEscape(pending.WorkspaceKey)+"/settings/account/connections?chat_auth=error&chat_provider="+pending.Provider+"&chat_auth_error="+url.QueryEscape(message), http.StatusSeeOther)
		} else {
			writeError(w, 400, message)
		}
	}
	if !s.authDisabled {
		cookie, e := r.Cookie(sessionCookieName)
		if e != nil || secretHash(cookie.Value) != pending.SessionHash {
			writeError(w, 403, "Complete authorization in the same signed-in browser")
			return
		}
		user, e := s.store.AuthenticateSession(r.Context(), cookie.Value)
		if e != nil || user.ID != pending.UserID {
			writeError(w, 403, "Sign in again to bind your account")
			return
		}
		access, e := s.store.OAuthWorkspaceAccess(r.Context(), pending.WorkspaceKey, user.ID)
		if e == nil {
			pending.Authentication, e = s.store.SessionAuthentication(r.Context(), cookie.Value)
		}
		if e != nil || !allowedAuthenticationMethod(access, access.ViewerRole, pending.Authentication) {
			writeError(w, 403, "Your workspace authentication policy no longer permits this binding")
			return
		}
		if access.WorkspaceSettings.RequireTwoFactor && pending.Authentication.MFAVerifiedAt == nil {
			writeError(w, 403, "Workspace multi-factor authentication required")
			return
		}
		pending.CanEstablishTenant = pending.CanEstablishTenant && workspaceAdminRole(access.ViewerRole)
	}
	record, err := s.store.ChatIntegrationByID(r.Context(), pending.Provider, pending.ConnectionID)
	if err != nil || record.WorkspaceKey != pending.WorkspaceKey || record.AppID != pending.AppID {
		fail("Application connection changed; restart authorization")
		return
	}
	if _, err = s.store.GetChatAction(r.Context(), record.WorkspaceKey, record.ConnectionID, state); err != nil {
		fail("Authorization revoked or expired")
		return
	}
	_, err = s.store.ReadConnectorSecret(r.Context(), state, true)
	if err != nil {
		fail("Authorization already used")
		return
	}
	_, _ = s.store.ConsumeChatAction(r.Context(), record.WorkspaceKey, record.ConnectionID, state)
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		fail("Authorization declined")
		return
	}
	credential, err := s.chatCredentials(r.Context(), record)
	if err != nil {
		fail("Application credentials unavailable")
		return
	}
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {record.AppID}, "client_secret": {credential.AppSecret}, "code": {r.URL.Query().Get("code")}, "redirect_uri": {pending.RedirectURI}, "code_verifier": {pending.Verifier}}
	endpoint := "https://accounts.feishu.cn/oauth/v3/token"
	var tokenBody any = values
	if record.Provider == "lark" {
		endpoint = "https://open.larksuite.com/open-apis/authen/v2/oauth/token"
		body := map[string]string{}
		for key := range values {
			body[key] = values.Get(key)
		}
		tokenBody = body
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err = chatRequest(r.Context(), endpoint, http.MethodPost, "", tokenBody, &token); err != nil || token.AccessToken == "" {
		fail("Platform authorization failed; verify application OAuth configuration")
		return
	}
	var profile struct {
		Data struct {
			OpenID    string `json:"open_id"`
			TenantKey string `json:"tenant_key"`
			Name      string `json:"name"`
		} `json:"data"`
	}
	if err = chatRequest(r.Context(), chatBaseURL(record.Provider)+"/open-apis/authen/v1/user_info", http.MethodGet, token.AccessToken, nil, &profile); err != nil || profile.Data.OpenID == "" || profile.Data.TenantKey == "" {
		fail("Could not verify platform identity")
		return
	}
	if record.TenantKey != "" && record.TenantKey != profile.Data.TenantKey {
		fail("This account belongs to another tenant")
		return
	}
	if record.TenantKey == "" {
		if !pending.CanEstablishTenant {
			fail("Administrator authorization required")
			return
		}
		record.TenantKey = profile.Data.TenantKey
	}
	record.Connected = true
	if err = s.store.PutChatIntegration(r.Context(), record); err != nil {
		fail("Application tenant is already connected to another workspace")
		return
	}
	identity := domain.ChatIdentity{UserID: pending.UserID, OpenID: profile.Data.OpenID, TenantKey: record.TenantKey, Name: profile.Data.Name, AuthProvider: pending.Authentication.Provider, AuthIssuer: pending.Authentication.Issuer, MFAVerifiedAt: pending.Authentication.MFAVerifiedAt}
	if err = s.store.PutChatIdentity(r.Context(), record.WorkspaceKey, record.ConnectionID, identity); err != nil {
		fail("This platform identity is already bound to another Flow account")
		return
	}
	if err = s.syncChatConnection(r.Context(), record, pending.UserID, "connected", "success", ""); err != nil {
		fail("Could not save connection metadata")
		return
	}
	http.Redirect(w, r, s.chatAppOrigin()+"/"+url.PathEscape(record.WorkspaceKey)+"/settings/account/connections?chat_auth=success&chat_provider="+record.Provider, http.StatusSeeOther)
}
func (s *server) deleteChatIdentity(w http.ResponseWriter, r *http.Request) {
	data, ok := s.chatSession(w, r, false)
	if !ok {
		return
	}
	record, err := s.store.ChatIntegration(r.Context(), workspaceKey(r), r.PathValue("provider"))
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(204)
		return
	}
	if err == nil {
		err = s.store.DeleteChatIdentity(r.Context(), record.WorkspaceKey, record.ConnectionID, data.Viewer.ID)
	}
	respondMutation(w, err, 204, nil)
}
