package main

import (
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
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

type connectorCredential struct {
	URL      string            `json:"url"`
	Token    *oauth2.Token     `json:"token,omitempty"`
	Config   oauth2.Config     `json:"config"`
	Resource string            `json:"resource,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}
type connectorOAuthState struct {
	Workspace, UserID, ConnectorID, URL, Verifier, SessionHash string
	Config                                                     oauth2.Config
	Resource                                                   string
}

// Striped locks bound memory while serializing refresh-token rotation within
// this process. Cluster deployments additionally take the workspace lock.
var connectorRefreshLocks [64]sync.Mutex

func connectorSecretID(workspace, userID, id string) string {
	return fmt.Sprintf("credential_%x", sha256.Sum256([]byte(workspace+"\x00"+userID+"\x00"+id)))
}
func connectorOwner(item applicationPolicy, userID string) string {
	if item.Shared {
		return item.OwnerID
	}
	return userID
}
func (s *server) connectorHTTPClient() *http.Client {
	c := secureOutboundClient(20 * time.Second)
	if s.authDisabled {
		c = &http.Client{Timeout: 20 * time.Second, Transport: connectorTransport{base: http.DefaultTransport, allowLocal: true}}
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

func (s *server) connectorCredential(ctx context.Context, workspace, userID string, item applicationPolicy) (*connectorCredential, error) {
	if !store.ConnectorSecretsConfigured() {
		return nil, nil
	}
	id := connectorSecretID(workspace, connectorOwner(item, userID), item.ID)
	hash := sha256.Sum256([]byte(id))
	lock := &connectorRefreshLocks[int(hash[0])%len(connectorRefreshLocks)]
	lock.Lock()
	defer lock.Unlock()
	var credential *connectorCredential
	run := func() error {
		raw, err := s.store.ReadConnectorSecret(ctx, id, false)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var saved connectorCredential
		if json.Unmarshal(raw, &saved) != nil {
			return errInvalid
		}
		if saved.URL != item.URL {
			return nil
		}
		credential = &saved
		if saved.Token == nil || saved.Token.Valid() {
			return nil
		}
		if saved.Token.RefreshToken == "" {
			return fmt.Errorf("MCP authorization expired; reconnect %s", item.Name)
		}
		token, err := s.exchangeConnectorToken(ctx, saved.Config, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {saved.Token.RefreshToken}, "resource": {saved.Resource}})
		if err != nil {
			return fmt.Errorf("MCP authorization expired; reconnect %s", item.Name)
		}
		if token.RefreshToken == "" {
			token.RefreshToken = saved.Token.RefreshToken
		}
		credential.Token = token
		raw, _ = json.Marshal(credential)
		return s.store.PutConnectorSecret(ctx, id, raw, time.Now().AddDate(1, 0, 0))
	}
	var err error
	if s.coordinator != nil {
		err = s.coordinator.WithWorkspaceLock(ctx, "connector-"+id, run)
	} else {
		err = run()
	}
	return credential, err
}

func (s *server) connectorForRequest(r *http.Request) (domain.Bootstrap, applicationPolicy, error) {
	data := s.workspaceData(r)
	for _, item := range applicationPolicies(&data) {
		if item.Kind == "mcp" && item.ID == r.PathValue("id") {
			if item.OwnerID != data.Viewer.ID && !(item.Shared && s.applicationPolicyAdmin(r, data)) {
				return data, item, store.ErrAuthForbidden
			}
			if !connectorAllowed(data.WorkspaceSettings, item, data.Viewer.ID) {
				return data, item, store.ErrAuthForbidden
			}
			return data, item, nil
		}
	}
	return data, applicationPolicy{}, errNotFound
}

func (s *server) connectorAuthStatus(w http.ResponseWriter, r *http.Request) {
	data, item, err := s.connectorForRequest(r)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	result := map[string]any{"configured": store.ConnectorSecretsConfigured(), "authorized": false, "customHeaders": false}
	if store.ConnectorSecretsConfigured() {
		raw, err := s.store.ReadConnectorSecret(r.Context(), connectorSecretID(workspaceKey(r), connectorOwner(item, data.Viewer.ID), item.ID), false)
		if err == nil {
			var c connectorCredential
			_ = json.Unmarshal(raw, &c)
			result["authorized"] = c.URL == item.URL && c.Token != nil && (c.Token.Valid() || c.Token.RefreshToken != "")
			result["customHeaders"] = c.URL == item.URL && len(c.Headers) > 0
		}
	}
	writeJSON(w, 200, result)
}

func (s *server) saveConnectorHeaders(w http.ResponseWriter, r *http.Request) {
	data, item, err := s.connectorForRequest(r)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	var input struct {
		Headers map[string]string `json:"headers"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Headers) > 16 {
		writeError(w, 400, "Too many authentication headers")
		return
	}
	for k, v := range input.Headers {
		if len(v) > 8192 || strings.ContainsAny(k+v, "\r\n") || strings.TrimSpace(k) == "" {
			writeError(w, 400, "Invalid authentication header")
			return
		}
		for _, ch := range k {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				writeError(w, 400, "Invalid header name")
				return
			}
		}
		if slices.Contains([]string{"host", "cookie", "content-length", "content-type", "accept", "connection", "transfer-encoding", "mcp-session-id", "mcp-protocol-version"}, strings.ToLower(k)) {
			writeError(w, 400, "Reserved protocol header")
			return
		}
	}
	credential := connectorCredential{URL: item.URL, Headers: input.Headers}
	raw, _ := json.Marshal(credential)
	err = s.store.PutConnectorSecret(r.Context(), connectorSecretID(workspaceKey(r), connectorOwner(item, data.Viewer.ID), item.ID), raw, time.Now().AddDate(1, 0, 0))
	if err != nil {
		writeError(w, 503, "Connector credential encryption is not configured")
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}

func (s *server) disconnectConnectorOAuth(w http.ResponseWriter, r *http.Request) {
	data, item, err := s.connectorForRequest(r)
	if err != nil {
		respondMutation(w, err, 204, nil)
		return
	}
	err = s.store.DeleteConnectorSecret(r.Context(), connectorSecretID(workspaceKey(r), connectorOwner(item, data.Viewer.ID), item.ID))
	respondMutation(w, err, 204, nil)
}

func (s *server) connectorCallbackURL() string {
	return strings.TrimRight(firstNonEmpty(s.allowedOrigin, os.Getenv("FLOW_APP_URL"), "http://localhost:5173"), "/") + "/api/connector-oauth/callback"
}

func (s *server) startConnectorOAuth(w http.ResponseWriter, r *http.Request) {
	data, item, err := s.connectorForRequest(r)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	if !store.ConnectorSecretsConfigured() {
		writeError(w, 503, "Configure FLOW_CONNECTOR_SECRET_KEY before authorizing connectors")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	config, resource, err := s.discoverConnectorOAuth(ctx, item.URL)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	config.RedirectURL = s.connectorCallbackURL()
	state, err := randomSecret("mcp_oauth_")
	if err != nil {
		writeError(w, 500, "Could not begin authorization")
		return
	}
	verifier := oauth2.GenerateVerifier()
	sessionHash := ""
	if !s.authDisabled {
		cookie, e := r.Cookie(sessionCookieName)
		if e != nil {
			writeError(w, 401, "Interactive sign-in required")
			return
		}
		sessionHash = secretHash(cookie.Value)
	}
	pending := connectorOAuthState{Workspace: workspaceKey(r), UserID: data.Viewer.ID, ConnectorID: item.ID, URL: item.URL, Verifier: verifier, Config: config, Resource: resource, SessionHash: sessionHash}
	raw, _ := json.Marshal(pending)
	if err = s.store.PutConnectorSecret(ctx, state, raw, time.Now().Add(10*time.Minute)); err != nil {
		writeError(w, 500, "Could not save authorization state")
		return
	}
	writeJSON(w, 200, map[string]string{"authorizationURL": config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", resource))})
}

func (s *server) discoverConnectorOAuth(ctx context.Context, endpoint string) (oauth2.Config, string, error) {
	client := s.connectorHTTPClient()
	defer client.CloseIdleConnections()
	resourceURL, err := url.Parse(endpoint)
	if err != nil {
		return oauth2.Config{}, "", errInvalid
	}
	if !integrationEndpointSafe(ctx, endpoint, s.authDisabled) {
		return oauth2.Config{}, "", errInvalid
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"Flow","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	response, err := client.Do(req)
	if err != nil {
		return oauth2.Config{}, "", fmt.Errorf("Could not discover MCP authentication")
	}
	challenges, _ := oauthex.ParseWWWAuthenticate(response.Header.Values("WWW-Authenticate"))
	response.Body.Close()
	candidates := []string{}
	scope := ""
	for _, challenge := range challenges {
		if challenge.Scheme == "bearer" {
			if raw := challenge.Params["resource_metadata"]; raw != "" {
				candidates = append(candidates, raw)
			}
			scope = challenge.Params["scope"]
			break
		}
	}
	origin := resourceURL.Scheme + "://" + resourceURL.Host
	if len(candidates) == 0 {
		if resourceURL.Path != "" && resourceURL.Path != "/" {
			candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+resourceURL.EscapedPath())
		}
		candidates = append(candidates, origin+"/.well-known/oauth-protected-resource")
	}
	var resource *oauthex.ProtectedResourceMetadata
	for _, candidate := range candidates {
		if !integrationEndpointSafe(ctx, candidate, s.authDisabled) {
			continue
		}
		resource, err = oauthex.GetProtectedResourceMetadata(ctx, candidate, endpoint, client)
		if err == nil {
			break
		}
	}
	if resource == nil || err != nil || len(resource.AuthorizationServers) == 0 {
		return oauth2.Config{}, "", fmt.Errorf("MCP server did not publish valid protected-resource metadata")
	}
	issuer, err := url.Parse(resource.AuthorizationServers[0])
	if err != nil || !integrationEndpointSafe(ctx, issuer.String(), s.authDisabled) {
		return oauth2.Config{}, "", fmt.Errorf("Invalid authorization issuer")
	}
	origin = issuer.Scheme + "://" + issuer.Host
	candidates = []string{origin + "/.well-known/oauth-authorization-server" + strings.TrimRight(issuer.EscapedPath(), "/"), origin + "/.well-known/openid-configuration" + strings.TrimRight(issuer.EscapedPath(), "/"), strings.TrimRight(issuer.String(), "/") + "/.well-known/openid-configuration"}
	var metadata *oauthex.AuthServerMeta
	for _, candidate := range candidates {
		metadata, err = oauthex.GetAuthServerMeta(ctx, candidate, issuer.String(), client)
		if err == nil {
			break
		}
	}
	if metadata == nil || err != nil || !slices.Contains(metadata.CodeChallengeMethodsSupported, "S256") {
		return oauth2.Config{}, "", fmt.Errorf("Authorization server must support OAuth PKCE S256")
	}
	if !integrationEndpointSafe(ctx, metadata.AuthorizationEndpoint, s.authDisabled) || !integrationEndpointSafe(ctx, metadata.TokenEndpoint, s.authDisabled) {
		return oauth2.Config{}, "", fmt.Errorf("Invalid OAuth endpoints")
	}
	config := oauth2.Config{RedirectURL: s.connectorCallbackURL(), Endpoint: oauth2.Endpoint{AuthURL: metadata.AuthorizationEndpoint, TokenURL: metadata.TokenEndpoint, AuthStyle: oauth2.AuthStyleInParams}}
	if scope == "" {
		scope = strings.Join(resource.ScopesSupported, " ")
	}
	config.Scopes = strings.Fields(scope)
	var clients map[string]struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	_ = json.Unmarshal([]byte(os.Getenv("FLOW_MCP_OAUTH_CLIENTS")), &clients)
	if registered, ok := clients[issuer.String()]; ok && registered.ClientID != "" {
		config.ClientID, config.ClientSecret = registered.ClientID, registered.ClientSecret
		if config.ClientSecret != "" && slices.Contains(metadata.TokenEndpointAuthMethodsSupported, "client_secret_basic") {
			config.Endpoint.AuthStyle = oauth2.AuthStyleInHeader
		}
	} else if metadata.ClientIDMetadataDocumentSupported && strings.HasPrefix(config.RedirectURL, "https://") {
		config.ClientID = strings.TrimSuffix(config.RedirectURL, "/callback") + "/client-metadata"
	} else if metadata.RegistrationEndpoint != "" && integrationEndpointSafe(ctx, metadata.RegistrationEndpoint, s.authDisabled) {
		registered, err := oauthex.RegisterClient(ctx, metadata.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{ClientName: "Flow", RedirectURIs: []string{config.RedirectURL}, TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}, Scope: scope}, client)
		if err != nil {
			return config, "", fmt.Errorf("OAuth client registration failed")
		}
		config.ClientID = registered.ClientID
	} else {
		return config, "", fmt.Errorf("Register this authorization server in FLOW_MCP_OAUTH_CLIENTS")
	}
	return config, resource.Resource, nil
}

func (s *server) connectorClientMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"client_id": strings.TrimSuffix(s.connectorCallbackURL(), "/callback") + "/client-metadata", "client_name": "Flow", "redirect_uris": []string{s.connectorCallbackURL()}, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
}

func (s *server) exchangeConnectorToken(ctx context.Context, config oauth2.Config, values url.Values) (*oauth2.Token, error) {
	if !integrationEndpointSafe(ctx, config.Endpoint.TokenURL, s.authDisabled) {
		return nil, errInvalid
	}
	values.Set("client_id", config.ClientID)
	if config.ClientSecret != "" && config.Endpoint.AuthStyle != oauth2.AuthStyleInHeader {
		values.Set("client_secret", config.ClientSecret)
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", config.Endpoint.TokenURL, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if config.Endpoint.AuthStyle == oauth2.AuthStyleInHeader {
		req.SetBasicAuth(url.QueryEscape(config.ClientID), url.QueryEscape(config.ClientSecret))
	}
	client := s.connectorHTTPClient()
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OAuth token exchange unavailable")
	}
	defer response.Body.Close()
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token) != nil || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		return nil, fmt.Errorf("OAuth token exchange failed")
	}
	result := &oauth2.Token{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, TokenType: token.TokenType}
	if token.ExpiresIn > 0 {
		result.Expiry = time.Now().Add(time.Duration(min(token.ExpiresIn, int64(365*24*3600))) * time.Second)
	}
	return result, nil
}

func (s *server) finishConnectorOAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	callbackWorkspace := ""
	fail := func(status int, message string) {
		if callbackWorkspace != "" && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, strings.TrimSuffix(s.connectorCallbackURL(), "/api/connector-oauth/callback")+"/"+url.PathEscape(callbackWorkspace)+"/settings/account/agents?connector_error=authorization_failed", http.StatusSeeOther)
			return
		}
		writeError(w, status, message)
	}
	state := r.URL.Query().Get("state")
	if !strings.HasPrefix(state, "mcp_oauth_") || len(state) > 150 {
		fail(400, "Invalid OAuth state")
		return
	}
	raw, err := s.store.ReadConnectorSecret(r.Context(), state, false)
	var pending connectorOAuthState
	if err != nil || json.Unmarshal(raw, &pending) != nil {
		fail(400, "Authorization expired; reconnect the connector")
		return
	}
	if !s.authDisabled {
		cookie, e := r.Cookie(sessionCookieName)
		if e != nil || secretHash(cookie.Value) != pending.SessionHash {
			fail(403, "Complete authorization in the same signed-in browser")
			return
		}
		user, e := s.store.AuthenticateSession(r.Context(), cookie.Value)
		if e != nil || user.ID != pending.UserID {
			fail(403, "Sign in again to reconnect")
			return
		}
	}
	callbackWorkspace = pending.Workspace
	raw, err = s.store.ReadConnectorSecret(r.Context(), state, true)
	if err != nil {
		fail(400, "Authorization already used")
		return
	}
	if r.URL.Query().Get("error") != "" {
		fail(400, "Connector authorization was declined")
		return
	}
	data, err := s.store.PagedWorkspaceMetadata(r.Context(), pending.Workspace, pending.UserID)
	if err != nil {
		fail(403, "Workspace access revoked")
		return
	}
	if !s.authDisabled {
		r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, data.Viewer))
		if !s.authorizeAuthenticationPolicy(w, r, data, data.ViewerRole) {
			return
		}
	}
	var selected *applicationPolicy
	for _, item := range applicationPolicies(&data) {
		if item.ID == pending.ConnectorID && item.URL == pending.URL && connectorAllowed(data.WorkspaceSettings, item, pending.UserID) && (item.OwnerID == pending.UserID || item.Shared && workspaceAdminRole(data.ViewerRole)) {
			selected = &item
			break
		}
	}
	if selected == nil {
		fail(403, "Connector access revoked")
		return
	}
	token, err := s.exchangeConnectorToken(r.Context(), pending.Config, url.Values{"grant_type": {"authorization_code"}, "code": {r.URL.Query().Get("code")}, "redirect_uri": {pending.Config.RedirectURL}, "code_verifier": {pending.Verifier}, "resource": {pending.Resource}})
	if err != nil {
		fail(502, err.Error())
		return
	}
	credential := connectorCredential{URL: pending.URL, Config: pending.Config, Resource: pending.Resource, Token: token}
	raw, _ = json.Marshal(credential)
	err = s.store.PutConnectorSecret(r.Context(), connectorSecretID(pending.Workspace, connectorOwner(*selected, pending.UserID), pending.ConnectorID), raw, time.Now().AddDate(1, 0, 0))
	if err != nil {
		fail(500, "Could not save authorization")
		return
	}
	http.Redirect(w, r, strings.TrimSuffix(s.connectorCallbackURL(), "/api/connector-oauth/callback")+"/"+url.PathEscape(pending.Workspace)+"/settings/account/agents?connector=authorized", http.StatusSeeOther)
}
