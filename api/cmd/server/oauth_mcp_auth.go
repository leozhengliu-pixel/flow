package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

var personalOAuthScopes = []string{"read", "write", "openid", "email"}
var supportedOAuthScopes = append(slices.Clone(personalOAuthScopes), domain.ApplicationScopes()[2:]...)

type oauthAuthorizationRequest struct {
	Actor               string   `json:"actor,omitempty"`
	TeamIDs             []string `json:"teamIds,omitempty"`
	AllPublicTeams      bool     `json:"allPublicTeams,omitempty"`
	ClientID            string   `json:"clientId"`
	RedirectURI         string   `json:"redirectUri"`
	ResponseType        string   `json:"responseType"`
	Scope               string   `json:"scope"`
	State               string   `json:"state"`
	CodeChallenge       string   `json:"codeChallenge"`
	CodeChallengeMethod string   `json:"codeChallengeMethod"`
	Resource            string   `json:"resource,omitempty"`
	Prompt              string   `json:"prompt,omitempty"`
	WorkspaceKey        string   `json:"workspaceKey,omitempty"`
	Approve             bool     `json:"approve"`
}

func (s *server) oauthProtectedResource(w http.ResponseWriter, r *http.Request) {
	base := externalBaseURL(r)
	resource := base + "/mcp"
	scopes := personalOAuthScopes
	if r.URL.Query().Get("actor") == "app" {
		scopes = supportedOAuthScopes
	}
	if strings.HasSuffix(r.URL.Path, "/readonly") {
		resource = base + "/mcp/readonly"
		scopes = []string{"read", "openid", "email"}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         publicOAuthScopeList(scopes),
	})
}

func (s *server) oauthAuthorizationServer(w http.ResponseWriter, r *http.Request) {
	base := externalBaseURL(r)
	scopes := personalOAuthScopes
	if r.URL.Query().Get("actor") == "app" {
		scopes = supportedOAuthScopes
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                        base,
		"authorization_endpoint":                        base + "/oauth/authorize",
		"token_endpoint":                                base + "/oauth/token",
		"registration_endpoint":                         base + "/oauth/register",
		"revocation_endpoint":                           base + "/oauth/revoke",
		"response_types_supported":                      []string{"code"},
		"introspection_endpoint":                        base + "/oauth/introspect",
		"grant_types_supported":                         []string{"authorization_code", "refresh_token", "client_credentials"},
		"code_challenge_methods_supported":              []string{"S256"},
		"token_endpoint_auth_methods_supported":         []string{"none", "client_secret_basic", "client_secret_post"},
		"revocation_endpoint_auth_methods_supported":    []string{"none", "client_secret_basic", "client_secret_post"},
		"introspection_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"prompt_values_supported":                       []string{"consent"},
		"scopes_supported":                              publicOAuthScopeList(scopes),
	})
}

func (s *server) registerOAuthClient(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input struct {
		ClientName              string   `json:"client_name"`
		ClientURI               string   `json:"client_uri"`
		LogoURI                 string   `json:"logo_uri"`
		RedirectURIs            []string `json:"redirect_uris"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.RedirectURIs) == 0 || len(input.RedirectURIs) > 20 || slices.ContainsFunc(input.RedirectURIs, func(item string) bool { return !validOAuthRedirectURI(item) }) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "At least one valid HTTPS or loopback redirect URI is required")
		return
	}
	suppliedName := strings.TrimSpace(input.ClientName)
	if suppliedName == "" {
		input.ClientName = "MCP client"
	} else {
		input.ClientName = suppliedName
	}
	if len(input.ClientName) > 200 || (input.ClientURI != "" && !validOAuthMetadataURI(input.ClientURI)) || (input.LogoURI != "" && !validApplicationIconURL(input.LogoURI, false)) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "Client metadata contains an invalid name or URL")
		return
	}
	if len(input.GrantTypes) == 0 {
		input.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	if len(input.ResponseTypes) == 0 {
		input.ResponseTypes = []string{"code"}
	}
	if input.TokenEndpointAuthMethod == "" {
		input.TokenEndpointAuthMethod = "none"
	}
	if input.TokenEndpointAuthMethod != "none" || !slices.Contains(input.GrantTypes, "authorization_code") || slices.ContainsFunc(input.GrantTypes, func(value string) bool { return value != "authorization_code" && value != "refresh_token" }) || len(input.ResponseTypes) != 1 || input.ResponseTypes[0] != "code" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "Flow supports public PKCE authorization-code clients")
		return
	}
	client := domain.OAuthClient{ClientName: strings.TrimSpace(input.ClientName), ClientURI: input.ClientURI, LogoURI: input.LogoURI, RedirectURIs: normalizedStrings(input.RedirectURIs), GrantTypes: normalizedStrings(input.GrantTypes), ResponseTypes: normalizedStrings(input.ResponseTypes), TokenEndpointAuthMethod: input.TokenEndpointAuthMethod, CreatedAt: time.Now().UTC()}
	if existing, found, findErr := s.store.FindOAuthClientByMetadata(r.Context(), client); findErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not look up client")
		return
	} else if found {
		existing.RedirectURIs = store.MergeOAuthRedirectURIs(existing.RedirectURIs, client.RedirectURIs)
		if err := s.store.UpdateOAuthClient(r.Context(), existing); err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not update client")
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}
	clientID, err := randomSecret(dynamicOAuthClientPrefix)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not register client")
		return
	}
	client.ClientID = clientID
	if err := s.store.RegisterOAuthClient(r.Context(), client); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not register client")
		return
	}
	writeJSON(w, http.StatusCreated, client)
}

func (s *server) getOAuthAuthorizationRequest(w http.ResponseWriter, r *http.Request) {
	request := oauthRequestFromQuery(r.URL.Query())
	client, scopes, err := s.validateOAuthAuthorizationRequest(r, request)
	if err != nil {
		writeOAuthRequestError(w, err)
		return
	}
	actor := s.oauthRequestUser(r)
	account, err := s.store.OAuthAccountForUser(r.Context(), actor.ID)
	if err != nil {
		writeOAuthError(w, http.StatusForbidden, "access_denied", "No accessible workspace")
		return
	}
	if client.HomeWorkspaceKey != "" && !client.PublicEnabled {
		account.Workspaces = slices.DeleteFunc(account.Workspaces, func(item domain.WorkspaceMembership) bool { return item.Workspace.URLKey != client.HomeWorkspaceKey })
		if len(account.Workspaces) == 0 {
			writeOAuthError(w, 403, "access_denied", "This application is private to its workspace")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"client":      client,
		"actor":       request.Actor,
		"redirectUri": request.RedirectURI,
		"scopes":      scopes,
		"scopeLabels": oauthScopeLabels(scopes),
		"workspaces":  account.Workspaces,
		"viewer":      account.Viewer,
		"prompt":      request.Prompt,
		// The application has a webhook: the reference app lists "Receive realtime
		// updates about your workspace" after the requested scopes.
		"realtime": s.applicationRealtime(client.ClientID),
		// Set when this user already granted these scopes to this
		// confidential application; the consent page completes the request
		// without asking again unless prompt=consent was supplied.
		"previouslyAuthorizedWorkspaceKey": s.previousOAuthConsent(client, request, scopes, actor.ID, account.Workspaces),
	})
}

func (s *server) decideOAuthAuthorization(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input oauthAuthorizationRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	client, scopes, err := s.validateOAuthAuthorizationRequest(r, input)
	if err != nil {
		writeOAuthRequestError(w, err)
		return
	}
	redirect, _ := url.Parse(input.RedirectURI)
	query := redirect.Query()
	if !input.Approve {
		// The reference app redirects a cancelled request with error=access_denied and
		// the state only (no error_description).
		query.Set("error", "access_denied")
		if input.State != "" {
			query.Set("state", input.State)
		}
		redirect.RawQuery = query.Encode()
		writeJSON(w, http.StatusOK, map[string]string{"redirect": redirect.String()})
		return
	}
	actor := s.oauthRequestUser(r)
	workspace, ok := s.oauthWorkspaceForRequest(w, r, input.WorkspaceKey, actor)
	if !ok {
		return
	}
	if client.HomeWorkspaceKey != "" && !client.PublicEnabled && client.HomeWorkspaceKey != workspace.Workspace.URLKey {
		writeOAuthError(w, 403, "access_denied", "This application is private to its workspace")
		return
	}
	newInstallation := false
	if input.Actor == "app" {
		// Authorizing an installed application again keeps its team access
		// (the consent screen asks for teams only on the first install).
		_, installed := s.activeApplicationInstallation(r.Context(), workspace.Workspace.URLKey, client.ClientID)
		newInstallation = !installed
		if !workspaceAdminRole(workspace.ViewerRole) || !installed && !input.AllPublicTeams && len(input.TeamIDs) == 0 {
			writeOAuthError(w, 403, "access_denied", "An administrator must select the teams this application can access")
			return
		}
	}
	authorizationID := fmt.Sprintf("oauth_authorization_%d", time.Now().UnixNano())
	ownWorkspace, ownApplication := s.store.OAuthApplicationHome(client.ClientID)
	if !(ownApplication && ownWorkspace == workspace.Workspace.URLKey) && !applicationApproved(&workspace, client.ClientID, scopes) {
		err := s.store.RequestOAuthApplicationApproval(r.Context(), workspace.Workspace.URLKey, client.ClientID, func(data *domain.Bootstrap) error {
			items := applicationPolicies(data)
			index := slices.IndexFunc(items, func(item applicationPolicy) bool { return item.ID == client.ClientID })
			if index < 0 && len(items) < 100 {
				items = append(items, applicationPolicy{ID: client.ClientID, Name: client.ClientName, Kind: "oauth", OwnerID: actor.ID, Shared: true, Status: "pending", Scopes: scopes, UpdatedAt: time.Now().UTC()})
			} else if index >= 0 && items[index].Status != "rejected" {
				items[index].Status = "pending"
				for _, scope := range scopes {
					if !slices.Contains(items[index].Scopes, scope) {
						items[index].Scopes = append(items[index].Scopes, scope)
					}
				}
			}
			setApplicationPolicies(data, items)
			return nil
		})
		if err != nil {
			writeOAuthError(w, 500, "server_error", "Could not request application approval")
			return
		}
		writeOAuthError(w, http.StatusForbidden, "access_denied", "Workspace administrator approval is required for this application and its requested scopes")
		return
	}
	code, err := randomSecret("flow_code_")
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not authorize client")
		return
	}
	grant := domain.OAuthAuthorizationCode{ClientID: client.ClientID, WorkspaceKey: workspace.Workspace.URLKey, UserID: actor.ID, RedirectURI: input.RedirectURI, Scopes: scopes, CodeChallenge: input.CodeChallenge, AuthorizationID: authorizationID, ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}
	grant.Actor = input.Actor
	grant.InstallerID = actor.ID
	grant.TeamIDs = input.TeamIDs
	grant.AllPublicTeams = input.AllPublicTeams
	grant.Realtime = s.applicationRealtime(client.ClientID)
	// Without PKCE only the client secret proves the redeemer is the client.
	grant.ClientAuthRequired = input.CodeChallenge == ""
	_, err = s.store.CreateOAuthAuthorizationGrant(r.Context(), code, grant, domain.OAuthAuthorization{ID: authorizationID, ClientID: client.ClientID, ClientName: client.ClientName, UserID: actor.ID, Scopes: scopes, CreatedAt: time.Now().UTC()}, applicationApproved)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not authorize client")
		return
	}
	if newInstallation {
		// Like the reference app, the installing administrator gets "The … application has been installed in your workspace".
		s.notifyApplicationInstalled(workspace, actor, client.ClientName, input.TeamIDs, input.AllPublicTeams)
	}
	query.Set("code", code)
	if input.State != "" {
		query.Set("state", input.State)
	}
	redirect.RawQuery = query.Encode()
	writeJSON(w, http.StatusOK, map[string]string{"redirect": redirect.String()})
}

func (s *server) oauthRequestUser(r *http.Request) domain.User {
	if s.authDisabled {
		return s.store.Account().Viewer
	}
	return authUser(r)
}

func (s *server) exchangeMCPToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid form body")
		return
	}
	switch r.Form.Get("grant_type") {
	case "client_credentials":
		s.issueClientCredentials(w, r, r.Form)
	case "authorization_code", "refresh_token":
		s.issueOAuthTokens(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "Supported grants: authorization_code, refresh_token, client_credentials")
	}
}

func (s *server) issueOAuthTokens(w http.ResponseWriter, r *http.Request) {
	client, ok := s.oauthTokenClient(w, r)
	if !ok {
		return
	}
	grantType := r.Form.Get("grant_type")
	accessToken, accessErr := randomSecret("flow_oauth_")
	refreshToken, refreshErr := randomSecret("flow_refresh_")
	if accessErr != nil || refreshErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not issue tokens")
		return
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	key := domain.APIKey{ID: fmt.Sprintf("oauth_token_%d", time.Now().UnixNano()), Name: "MCP OAuth token", Prefix: accessToken[:min(len(accessToken), 19)], SecretHash: secretHash(accessToken), TeamIDs: []string{}, CreatedAt: time.Now().UTC(), ExpiresAt: &expiresAt}
	token := r.Form.Get("refresh_token")
	if grantType == "authorization_code" {
		token = r.Form.Get("code")
	}
	if token == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code or refresh_token is required")
		return
	}
	grant, err := s.store.ExchangeOAuthGrant(r.Context(), grantType, token, client.ID, refreshToken, key, func(code domain.OAuthAuthorizationCode) bool {
		if grantType == "refresh_token" {
			// Tokens from a PKCE flow refresh with client_id alone; tokens
			// from a secret-only flow need the client secret again.
			return !code.ClientAuthRequired || client.Authenticated
		}
		if code.RedirectURI != r.Form.Get("redirect_uri") {
			return false
		}
		if code.CodeChallenge == "" {
			return client.Authenticated && r.Form.Get("code_verifier") == ""
		}
		return validPKCE(r.Form.Get("code_verifier"), code.CodeChallenge)
	}, applicationApproved)
	if err != nil {
		if errors.Is(err, store.ErrAuthForbidden) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Grant is invalid, expired, or revoked")
		} else {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not issue tokens")
		}
		return
	}
	if grant.ReplayedRefreshToken != "" {
		refreshToken = grant.ReplayedRefreshToken
	}
	actor := grant.Actor
	if actor == "" {
		actor = "user"
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": accessToken, "refresh_token": refreshToken, "token_type": "Bearer", "expires_in": 3600, "scope": publicOAuthScopes(grant.Scopes), "actor": actor})
}

func (s *server) revokeOAuthAuthorization(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := requestActor(s, r)
	workspace, ok := s.oauthWorkspaceForRequest(w, r, workspaceKey(r), actor)
	if !ok {
		return
	}
	err := s.store.RevokeOAuthAuthorizationRecords(r.Context(), workspace.Workspace.URLKey, id, actor.ID)
	if errors.Is(err, store.ErrAuthForbidden) || store.IsOAuthNotFound(err) {
		err = errNotFound
	}
	respondMutation(w, err, http.StatusNoContent, nil)
}

func oauthRequestFromQuery(query url.Values) oauthAuthorizationRequest {
	return oauthAuthorizationRequest{Actor: query.Get("actor"), ClientID: query.Get("client_id"), RedirectURI: query.Get("redirect_uri"), ResponseType: query.Get("response_type"), Scope: query.Get("scope"), State: query.Get("state"), CodeChallenge: query.Get("code_challenge"), CodeChallengeMethod: query.Get("code_challenge_method"), Resource: query.Get("resource"), Prompt: query.Get("prompt")}
}

// oauthScopeLabelText is the consent screen's wording for each scope, as
// The reference app words its permissions list (measured on the reference app's consent screen).
var oauthScopeLabelText = map[string]string{
	"read":                "Read access to your workspace",
	"write":               "Write access to your workspace",
	"admin":               "Admin access to manage settings and people",
	"create_issues":       "Create issues for your workspace",
	"create_comments":     "Create issue comments for your workspace",
	"timeSchedule:write":  "Create and modify time schedules for your workspace",
	"app:assignable":      "Assign issues and projects to the app in teams it can access",
	"app:mentionable":     "Mention app in issues, projects, and documents it can access",
	"initiative:read":     "Read data about initiatives in the workspace",
	"initiative:write":    "Read and write data about initiatives in the workspace",
	"customer:read":       "Read data about customers in the workspace",
	"customer:write":      "Read and write data about customers in the workspace",
	"release:read":        "Read releases in the workspace",
	"release:create":      "Create releases in the workspace",
	"releaseStage:update": "Update release stages in the workspace",
	"openid":              "Verify your identity",
	"email":               "Read your email address",
}

func oauthScopeLabels(scopes []string) []string {
	labels := []string{}
	for _, scope := range scopes {
		label := oauthScopeLabelText[scope]
		if label == "" {
			label = scope
		}
		labels = append(labels, label)
	}
	return labels
}

func validOAuthRedirectURI(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Fragment != "" || parsed.Host == "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := parsed.Hostname()
	return parsed.Scheme == "http" && (host == "127.0.0.1" || host == "::1" || host == "localhost")
}

func validOAuthMetadataURI(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func validPKCE(verifier, challenge string) bool {
	digest := sha256.Sum256([]byte(verifier))
	return verifier != "" && base64.RawURLEncoding.EncodeToString(digest[:]) == challenge
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
}
