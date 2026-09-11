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
	clientID, err := randomSecret("flow_mcp_")
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
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	actor := s.oauthRequestUser(r)
	account, err := s.store.OAuthAccountForUser(r.Context(), actor.ID)
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
	workspace, ok := s.oauthWorkspaceForRequest(w, r, input.WorkspaceKey, actor)
	if !ok {
		return
	}
	authorizationID := fmt.Sprintf("oauth_authorization_%d", time.Now().UnixNano())
	if !applicationApproved(&workspace, client.ClientID, scopes) {
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
	_, err = s.store.CreateOAuthAuthorizationGrant(r.Context(), code, grant, domain.OAuthAuthorization{ID: authorizationID, ClientID: client.ClientID, ClientName: client.ClientName, UserID: actor.ID, Scopes: scopes, CreatedAt: time.Now().UTC()}, applicationApproved)
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
	case "authorization_code", "refresh_token":
		s.issueOAuthTokens(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "Supported grants: authorization_code, refresh_token")
	}
}

func (s *server) issueOAuthTokens(w http.ResponseWriter, r *http.Request) {
	accessToken, accessErr := randomSecret("flow_oauth_")
	refreshToken, refreshErr := randomSecret("flow_refresh_")
	if accessErr != nil || refreshErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not issue tokens")
		return
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	key := domain.APIKey{ID: fmt.Sprintf("oauth_token_%d", time.Now().UnixNano()), Name: "MCP OAuth token", Prefix: accessToken[:min(len(accessToken), 19)], SecretHash: secretHash(accessToken), TeamIDs: []string{}, CreatedAt: time.Now().UTC(), ExpiresAt: &expiresAt}
	token := r.Form.Get("refresh_token")
	if r.Form.Get("grant_type") == "authorization_code" {
		token = r.Form.Get("code")
	}
	grant, err := s.store.ExchangeOAuthGrant(r.Context(), r.Form.Get("grant_type"), token, r.Form.Get("client_id"), refreshToken, key, func(code domain.OAuthAuthorizationCode) bool {
		return code.RedirectURI == r.Form.Get("redirect_uri") && validPKCE(r.Form.Get("code_verifier"), code.CodeChallenge)
	}, applicationApproved)
	if err != nil {
		if errors.Is(err, store.ErrAuthForbidden) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Grant is invalid, expired, or revoked")
		} else {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not issue tokens")
		}
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
	if err := s.store.RevokeOAuthRefreshToken(r.Context(), token); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not revoke token")
		return
	}
	if err := s.store.RevokeOAuthAccessToken(r.Context(), secretHash(token)); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not revoke token")
		return
	}
	w.WriteHeader(http.StatusOK)
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
