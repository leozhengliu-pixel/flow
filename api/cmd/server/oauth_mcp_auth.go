package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

var supportedOAuthScopes = []string{"read", "write", "openid", "email"}

type oauthAuthorizationRequest struct {
	ClientID            string `json:"clientId"`
	RedirectURI         string `json:"redirectUri"`
	ResponseType        string `json:"responseType"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	CodeChallenge       string `json:"codeChallenge"`
	CodeChallengeMethod string `json:"codeChallengeMethod"`
	Resource            string `json:"resource,omitempty"`
	WorkspaceKey        string `json:"workspaceKey,omitempty"`
	Approve             bool   `json:"approve"`
}

func (s *server) oauthProtectedResource(w http.ResponseWriter, r *http.Request) {
	base := externalBaseURL(r)
	resource := base + "/mcp"
	scopes := supportedOAuthScopes
	if strings.HasSuffix(r.URL.Path, "/readonly") {
		resource = base + "/mcp/readonly"
		scopes = []string{"read", "openid", "email"}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         scopes,
	})
}

func (s *server) oauthAuthorizationServer(w http.ResponseWriter, r *http.Request) {
	base := externalBaseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"revocation_endpoint":                   base + "/oauth/revoke",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      supportedOAuthScopes,
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
	if input.ClientName == "" {
		input.ClientName = "MCP client"
	}
	input.ClientName = strings.TrimSpace(input.ClientName)
	if len(input.ClientName) > 200 || (input.ClientURI != "" && !validOAuthMetadataURI(input.ClientURI)) || (input.LogoURI != "" && !validOAuthMetadataURI(input.LogoURI)) {
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
	clientID, err := randomSecret("flow_mcp_")
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not register client")
		return
	}
	client := domain.OAuthClient{ClientID: clientID, ClientName: strings.TrimSpace(input.ClientName), ClientURI: input.ClientURI, LogoURI: input.LogoURI, RedirectURIs: normalizedStrings(input.RedirectURIs), GrantTypes: normalizedStrings(input.GrantTypes), ResponseTypes: normalizedStrings(input.ResponseTypes), TokenEndpointAuthMethod: input.TokenEndpointAuthMethod, CreatedAt: time.Now().UTC()}
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
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	actor := s.oauthRequestUser(r)
	account, err := s.store.AccountForUser(r.Context(), actor.ID)
	if err != nil {
		writeOAuthError(w, http.StatusForbidden, "access_denied", "No accessible workspace")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"client":      client,
		"redirectUri": request.RedirectURI,
		"scopes":      scopes,
		"scopeLabels": oauthScopeLabels(scopes),
		"workspaces":  account.Workspaces,
		"viewer":      account.Viewer,
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
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	redirect, _ := url.Parse(input.RedirectURI)
	query := redirect.Query()
	if !input.Approve {
		query.Set("error", "access_denied")
		query.Set("error_description", "The user denied the request")
		if input.State != "" {
			query.Set("state", input.State)
		}
		redirect.RawQuery = query.Encode()
		writeJSON(w, http.StatusOK, map[string]string{"redirect": redirect.String()})
		return
	}
	actor := s.oauthRequestUser(r)
	workspace, ok, err := s.store.BootstrapForUser(r.Context(), input.WorkspaceKey, actor.ID)
	if err != nil || !ok {
		writeOAuthError(w, http.StatusForbidden, "access_denied", "You do not have access to that workspace")
		return
	}
	authorizationID := fmt.Sprintf("oauth_authorization_%d", time.Now().UnixNano())
	code, err := randomSecret("flow_code_")
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not authorize client")
		return
	}
	grant := domain.OAuthAuthorizationCode{ClientID: client.ClientID, WorkspaceKey: workspace.Workspace.URLKey, UserID: actor.ID, RedirectURI: input.RedirectURI, Scopes: scopes, CodeChallenge: input.CodeChallenge, AuthorizationID: authorizationID, ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}
	if err := s.store.CreateOAuthAuthorizationCode(r.Context(), code, grant); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not authorize client")
		return
	}
	err = s.store.MutateWorkspace(r.Context(), workspace.Workspace.URLKey, "oauth_authorization.created", authorizationID, map[string]any{"clientId": client.ClientID, "scopes": scopes}, func(data *domain.Bootstrap) error {
		data.OAuthAuthorizations = append([]domain.OAuthAuthorization{{ID: authorizationID, ClientID: client.ClientID, ClientName: client.ClientName, UserID: actor.ID, Scopes: scopes, CreatedAt: time.Now().UTC()}}, data.OAuthAuthorizations...)
		return nil
	})
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not authorize client")
		return
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
	case "authorization_code":
		grant, err := s.store.ConsumeOAuthAuthorizationCode(r.Context(), r.Form.Get("code"))
		if err != nil || grant.ClientID != r.Form.Get("client_id") || grant.RedirectURI != r.Form.Get("redirect_uri") || !validPKCE(r.Form.Get("code_verifier"), grant.CodeChallenge) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Authorization code is invalid or expired")
			return
		}
		s.issueOAuthTokens(w, r, domain.OAuthRefreshGrant{ClientID: grant.ClientID, WorkspaceKey: grant.WorkspaceKey, UserID: grant.UserID, Scopes: grant.Scopes, AuthorizationID: grant.AuthorizationID, ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour)})
	case "refresh_token":
		grant, err := s.store.ConsumeOAuthRefreshToken(r.Context(), r.Form.Get("refresh_token"))
		if err != nil || grant.ClientID != r.Form.Get("client_id") {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Refresh token is invalid or expired")
			return
		}
		s.issueOAuthTokens(w, r, grant)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "Supported grants: authorization_code, refresh_token")
	}
}

func (s *server) issueOAuthTokens(w http.ResponseWriter, r *http.Request, grant domain.OAuthRefreshGrant) {
	accessToken, accessErr := randomSecret("flow_oauth_")
	refreshToken, refreshErr := randomSecret("flow_refresh_")
	if accessErr != nil || refreshErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not issue tokens")
		return
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	key := domain.APIKey{ID: fmt.Sprintf("oauth_token_%d", time.Now().UnixNano()), Name: "MCP OAuth token", Prefix: accessToken[:min(len(accessToken), 19)], SecretHash: secretHash(accessToken), CreatorID: grant.UserID, Scopes: grant.Scopes, TeamIDs: []string{}, CreatedAt: time.Now().UTC(), ExpiresAt: &expiresAt, OAuthClientID: grant.ClientID, AuthorizationID: grant.AuthorizationID}
	err := s.store.MutateWorkspace(r.Context(), grant.WorkspaceKey, "oauth_token.created", grant.AuthorizationID, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool {
			return item.ID == grant.AuthorizationID && item.RevokedAt == nil
		})
		if index < 0 {
			return errNotFound
		}
		data.APIKeys = append([]domain.APIKey{key}, data.APIKeys...)
		return nil
	})
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Authorization has been revoked")
		return
	}
	grant.ExpiresAt = time.Now().UTC().Add(30 * 24 * time.Hour)
	if err := s.store.CreateOAuthRefreshToken(r.Context(), refreshToken, grant); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not issue refresh token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": accessToken, "refresh_token": refreshToken, "token_type": "Bearer", "expires_in": 3600, "scope": strings.Join(grant.Scopes, " ")})
}

func (s *server) revokeOAuthToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	token := r.Form.Get("token")
	_ = s.store.RevokeOAuthRefreshToken(r.Context(), token)
	if workspace, key, ok := s.store.FindAPIKey(secretHash(token)); ok {
		_ = s.store.MutateWorkspace(r.Context(), workspace, "oauth_token.revoked", key.ID, nil, func(data *domain.Bootstrap) error {
			if index := slices.IndexFunc(data.APIKeys, func(item domain.APIKey) bool { return item.ID == key.ID }); index >= 0 {
				now := time.Now().UTC()
				data.APIKeys[index].RevokedAt = &now
			}
			return nil
		})
	}
	w.WriteHeader(http.StatusOK)
}

func (s *server) revokeOAuthAuthorization(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_authorization.revoked", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool { return item.ID == id && item.UserID == authUser(r).ID })
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		data.OAuthAuthorizations[index].RevokedAt = &now
		for keyIndex := range data.APIKeys {
			if data.APIKeys[keyIndex].AuthorizationID == id && data.APIKeys[keyIndex].RevokedAt == nil {
				data.APIKeys[keyIndex].RevokedAt = &now
			}
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) validateOAuthAuthorizationRequest(r *http.Request, request oauthAuthorizationRequest) (domain.OAuthClient, []string, error) {
	client, err := s.store.OAuthClient(r.Context(), request.ClientID)
	if err != nil {
		var ok bool
		if client, ok = s.store.OAuthApplicationClient(request.ClientID); !ok {
			return client, nil, fmt.Errorf("unknown OAuth client")
		}
	}
	if request.ResponseType != "code" || !slices.Contains(client.RedirectURIs, request.RedirectURI) {
		return client, nil, fmt.Errorf("response type or redirect URI is invalid")
	}
	if request.CodeChallenge == "" || request.CodeChallengeMethod != "S256" {
		return client, nil, fmt.Errorf("S256 PKCE code challenge is required")
	}
	scopes := normalizedStrings(strings.Fields(request.Scope))
	if len(scopes) == 0 {
		scopes = []string{"read"}
	}
	if slices.ContainsFunc(scopes, func(scope string) bool { return !slices.Contains(supportedOAuthScopes, scope) }) {
		return client, nil, fmt.Errorf("one or more requested scopes are unsupported")
	}
	if strings.HasSuffix(request.Resource, "/mcp/readonly") && slices.Contains(scopes, "write") {
		return client, nil, fmt.Errorf("the read-only MCP endpoint does not accept write scope")
	}
	return client, scopes, nil
}

func oauthRequestFromQuery(query url.Values) oauthAuthorizationRequest {
	return oauthAuthorizationRequest{ClientID: query.Get("client_id"), RedirectURI: query.Get("redirect_uri"), ResponseType: query.Get("response_type"), Scope: query.Get("scope"), State: query.Get("state"), CodeChallenge: query.Get("code_challenge"), CodeChallengeMethod: query.Get("code_challenge_method"), Resource: query.Get("resource")}
}

func oauthScopeLabels(scopes []string) []string {
	labels := []string{}
	for _, scope := range scopes {
		labels = append(labels, map[string]string{"read": "Read", "write": "Write", "openid": "Identity", "email": "Email address"}[scope])
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

func externalBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
}
