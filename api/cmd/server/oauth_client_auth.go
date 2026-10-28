package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// userOAuthScopes are the scopes an actor=user authorization may request:
// The reference app's user scopes (read, write, issues:create, comments:create,
// timeSchedule:write, admin, release:*), Flow's identity scopes. app:*,
// customer:* and initiative:* scopes are only valid with actor=app:
// registered applications asking for them otherwise are rejected (as the reference app
// does) and public MCP clients have them dropped.
var userOAuthScopes = append(slices.DeleteFunc(slices.Clone(supportedOAuthScopes), appOnlyOAuthScope), "admin", "timeSchedule:write")

// oauthRequestError carries an RFC 6749 error code with its description.
type oauthRequestError struct {
	Code        string
	Description string
}

func (e oauthRequestError) Error() string { return e.Description }

func oauthInvalidRequest(format string, args ...any) error {
	return oauthRequestError{Code: "invalid_request", Description: fmt.Sprintf(format, args...)}
}

func oauthInvalidScope(format string, args ...any) error {
	return oauthRequestError{Code: "invalid_scope", Description: fmt.Sprintf(format, args...)}
}

func writeOAuthRequestError(w http.ResponseWriter, err error) {
	var typed oauthRequestError
	if errors.As(err, &typed) {
		writeOAuthError(w, http.StatusBadRequest, typed.Code, typed.Description)
		return
	}
	if store.IsDatabaseError(err) {
		requestID := w.Header().Get("X-Request-ID")
		slog.Error("oauth request failed", "request_id", requestID, "error", err.Error())
		writeOAuthError(w, http.StatusInternalServerError, "server_error", internalErrorMessage+" (request id "+requestID+")")
		return
	}
	writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
}

// canonicalOAuthScope maps the reference app's scope spellings onto Flow's stored names
// without lower-casing camel-case scopes such as timeSchedule:write.
func canonicalOAuthScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case "issues:create":
		return "create_issues"
	case "comments:create":
		return "create_comments"
	}
	return strings.TrimSpace(scope)
}

// publicOAuthScopeName is the name a scope is reported under in token
// responses, introspection and server metadata: the reference app's spelling
// (issues:create, comments:create) rather than Flow's stored name.
func publicOAuthScopeName(scope string) string {
	switch scope {
	case "create_issues":
		return "issues:create"
	case "create_comments":
		return "comments:create"
	}
	return scope
}

// publicOAuthScopes renders granted scopes in token
// responses: the reference app's names, de-duplicated, sorted and space-separated.
func publicOAuthScopes(scopes []string) string {
	names := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		names = append(names, publicOAuthScopeName(scope))
	}
	slices.Sort(names)
	return strings.Join(slices.Compact(names), " ")
}

// publicOAuthScopeList is publicOAuthScopes for metadata lists, keeping order.
func publicOAuthScopeList(scopes []string) []string {
	names := []string{}
	for _, scope := range scopes {
		if name := publicOAuthScopeName(scope); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// appOnlyOAuthScope reports scopes the reference app accepts only with actor=app: the
// agent scopes and the customer / initiative resource scopes. A registered
// application requesting them for a user authorization gets invalid_scope.
func appOnlyOAuthScope(scope string) bool {
	return strings.HasPrefix(scope, "app:") || strings.HasPrefix(scope, "customer:") || strings.HasPrefix(scope, "initiative:")
}

// parseOAuthScopes accepts comma- or space-separated scopes (the reference app documents
// commas, RFC 6749 uses spaces) and always includes read, which every token
// carries.
func parseOAuthScopes(raw string) []string {
	scopes := []string{}
	for _, scope := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
		scopes = append(scopes, canonicalOAuthScope(scope))
	}
	scopes = normalizedStrings(scopes)
	if !slices.Contains(scopes, "read") {
		scopes = append([]string{"read"}, scopes...)
	}
	return scopes
}

func confidentialOAuthClient(client domain.OAuthClient) bool {
	return client.TokenEndpointAuthMethod != "" && client.TokenEndpointAuthMethod != "none"
}

func (s *server) validateOAuthAuthorizationRequest(r *http.Request, request oauthAuthorizationRequest) (domain.OAuthClient, []string, error) {
	if request.Actor != "" && request.Actor != "user" && request.Actor != "app" {
		return domain.OAuthClient{}, nil, oauthInvalidRequest("unsupported OAuth actor")
	}
	client, err := s.store.OAuthClient(r.Context(), request.ClientID)
	if err != nil {
		var ok bool
		if client, ok = s.store.OAuthApplicationClient(request.ClientID); !ok {
			// The reference app's wording for an unknown client_id.
			return client, nil, oauthRequestError{Code: "invalid_client", Description: fmt.Sprintf("Could not find OAuth client with clientId %s. Please check that you are logged into the correct workspace.", request.ClientID)}
		}
	}
	if strings.TrimSpace(request.RedirectURI) == "" {
		return client, nil, oauthInvalidRequest("Invalid parameters. Make sure to include `redirect_uri`")
	}
	if request.ResponseType != "code" {
		return client, nil, oauthInvalidRequest("Invalid or missing `response_type`. Expected `response_type=code`")
	}
	if !store.OAuthRedirectURIAllowed(client.RedirectURIs, request.RedirectURI) {
		return client, nil, oauthInvalidRequest("Invalid redirect_uri parameter for the application.")
	}
	switch request.Prompt {
	case "", "consent", "login", "select_account":
	default:
		return client, nil, oauthInvalidRequest("unsupported prompt value")
	}
	// PKCE (S256) is required for public clients and optional for
	// confidential clients, which authenticate with their secret instead.
	if request.CodeChallenge != "" || request.CodeChallengeMethod != "" {
		if request.CodeChallengeMethod != "S256" {
			return client, nil, oauthInvalidRequest("code_challenge_method must be S256")
		}
		if len(request.CodeChallenge) < 43 || len(request.CodeChallenge) > 128 {
			return client, nil, oauthInvalidRequest("code_challenge must be a base64url SHA-256 digest")
		}
	} else if !confidentialOAuthClient(client) {
		return client, nil, oauthInvalidRequest("S256 PKCE code challenge is required for public clients")
	}
	scopes := parseOAuthScopes(request.Scope)
	if request.Actor == "app" {
		if slices.Contains(scopes, "admin") {
			return client, nil, oauthInvalidScope("the admin scope cannot be requested with actor=app")
		}
		if slices.Contains(scopes, "email") || slices.Contains(scopes, "openid") || slices.Contains(scopes, "timeSchedule:write") {
			return client, nil, oauthInvalidScope("app authorization cannot request personal identity scopes")
		}
		if bad := slices.IndexFunc(scopes, func(scope string) bool { return !slices.Contains(domain.ApplicationScopes(), scope) }); bad >= 0 {
			return client, nil, oauthInvalidScope("Invalid scope: %s", scopes[bad])
		}
		scopes, err = domain.NormalizeApplicationScopes(scopes)
		if err != nil {
			return client, nil, oauthInvalidScope("%s", err.Error())
		}
	} else {
		// Like the reference app, a registered application asking for app:*,
		// customer:* or initiative:* scopes without actor=app is an error.
		// MCP clients (public, dynamically registered) request every
		// advertised scope, so theirs are dropped.
		if slices.ContainsFunc(scopes, appOnlyOAuthScope) {
			if confidentialOAuthClient(client) {
				return client, nil, oauthInvalidScope("The scopes requested are not valid for this actor mode.")
			}
			scopes = slices.DeleteFunc(scopes, appOnlyOAuthScope)
		}
		if bad := slices.IndexFunc(scopes, func(scope string) bool { return !slices.Contains(userOAuthScopes, scope) }); bad >= 0 {
			return client, nil, oauthInvalidScope("Invalid scope: %s", scopes[bad])
		}
		if slices.Contains(scopes, "admin") && (!confidentialOAuthClient(client) || !slices.Contains(client.AllowedScopes, "admin")) {
			return client, nil, oauthInvalidScope("the admin scope requires a registered application configured with admin")
		}
	}
	if strings.HasSuffix(request.Resource, "/mcp/readonly") && slices.Contains(scopes, "write") {
		return client, nil, oauthInvalidScope("the read-only MCP endpoint does not accept write scope")
	}
	if client.AllowedScopes != nil && slices.ContainsFunc(scopes, func(scope string) bool { return !slices.Contains(client.AllowedScopes, scope) }) {
		return client, nil, oauthInvalidScope("requested scopes exceed the application's configured permissions")
	}
	return client, scopes, nil
}

// previousOAuthConsent returns the workspace in which the user already
// authorized this confidential application for at least these scopes. Public
// (dynamically registered) clients and app installs always show consent, as
// does prompt=consent (which also lets the user pick another workspace).
func (s *server) previousOAuthConsent(client domain.OAuthClient, request oauthAuthorizationRequest, scopes []string, userID string, workspaces []domain.WorkspaceMembership) string {
	if request.Prompt != "" || request.Actor == "app" || !confidentialOAuthClient(client) {
		return ""
	}
	for _, membership := range workspaces {
		data, ok := s.store.WorkspaceMetadata(membership.Workspace.URLKey)
		if !ok || !applicationApproved(&data, client.ClientID, scopes) {
			continue
		}
		if slices.ContainsFunc(data.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool {
			return item.ClientID == client.ClientID && item.UserID == userID && item.RevokedAt == nil && item.Actor != "app" && !slices.ContainsFunc(scopes, func(scope string) bool { return !slices.Contains(item.Scopes, scope) })
		}) {
			return membership.Workspace.URLKey
		}
	}
	return ""
}

type oauthTokenClient struct {
	ID string
	// Authenticated is true when a confidential client proved its secret.
	Authenticated bool
}

func oauthBasicCredential(value string) string {
	// RFC 6749 §2.3.1: credentials are form-encoded before Basic encoding.
	if decoded, err := url.QueryUnescape(value); err == nil {
		return decoded
	}
	return value
}

// oauthClientCredentials reads client_secret_basic or client_secret_post
// credentials. Supplying both is an error.
func oauthClientCredentials(r *http.Request) (id, secret string, basic bool, err error) {
	id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
	if basicID, basicSecret, ok := r.BasicAuth(); ok {
		basicID = oauthBasicCredential(basicID)
		if secret != "" || id != "" && id != basicID {
			return "", "", true, oauthInvalidRequest("use only one client authentication method")
		}
		return basicID, oauthBasicCredential(basicSecret), true, nil
	}
	return id, secret, false, nil
}

func writeOAuthInvalidClient(w http.ResponseWriter, basic bool, description string) {
	if basic {
		w.Header().Set("WWW-Authenticate", `Basic realm="flow"`)
	}
	writeOAuthError(w, http.StatusUnauthorized, "invalid_client", description)
}

// oauthTokenClient authenticates the client of an authorization_code or
// refresh_token request. A secret, when supplied, must belong to a registered
// (confidential) application; public clients identify with client_id only.
func (s *server) oauthTokenClient(w http.ResponseWriter, r *http.Request) (oauthTokenClient, bool) {
	id, secret, basic, err := oauthClientCredentials(r)
	if err != nil {
		writeOAuthRequestError(w, err)
		return oauthTokenClient{}, false
	}
	if id == "" {
		writeOAuthInvalidClient(w, basic, "client_id is required")
		return oauthTokenClient{}, false
	}
	client := oauthTokenClient{ID: id}
	if secret != "" || basic {
		confidential, ok := s.store.OAuthClientSecret(id, secret)
		if !confidential || !ok {
			writeOAuthInvalidClient(w, basic, "Client authentication failed")
			return oauthTokenClient{}, false
		}
		client.Authenticated = true
	}
	return client, true
}

// revokeOAuthToken implements RFC 7009. The token goes in the `token` field
// with an optional token_type_hint; the legacy Authorization header and
// access_token / refresh_token fields are accepted for the reference app compatibility
// but must not be combined with `token`. Unknown or already revoked tokens
// return 200 as RFC 7009 requires. When client credentials are supplied they
// must be valid and the token must have been issued to that client.
func (s *server) revokeOAuthToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid form body")
		return
	}
	token, hint := strings.TrimSpace(r.Form.Get("token")), r.Form.Get("token_type_hint")
	legacyAccess, legacyRefresh := strings.TrimSpace(r.Form.Get("access_token")), strings.TrimSpace(r.Form.Get("refresh_token"))
	bearer := ""
	if header := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(header), "bearer ") {
		bearer = strings.TrimSpace(header[len("Bearer "):])
	}
	if token != "" && (legacyAccess != "" || legacyRefresh != "") {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "token cannot be combined with access_token or refresh_token")
		return
	}
	switch {
	case token != "":
	case legacyAccess != "":
		token, hint = legacyAccess, "access_token"
	case legacyRefresh != "":
		token, hint = legacyRefresh, "refresh_token"
	case bearer != "":
		token, hint = bearer, "access_token"
	default:
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	if hint != "" && hint != "access_token" && hint != "refresh_token" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_token_type", "token_type_hint must be access_token or refresh_token")
		return
	}
	clientID := ""
	if bearer == "" || token != bearer {
		id, secret, basic, err := oauthClientCredentials(r)
		if err != nil {
			writeOAuthRequestError(w, err)
			return
		}
		if secret != "" || basic {
			if confidential, ok := s.store.OAuthClientSecret(id, secret); !confidential || !ok {
				writeOAuthInvalidClient(w, basic, "Client authentication failed")
				return
			}
		}
		clientID = id
	}
	revokeRefresh := func() (bool, error) {
		state, found, err := s.store.OAuthRefreshToken(r.Context(), token)
		if err != nil || !found {
			return false, err
		}
		if clientID != "" && state.Grant.ClientID != clientID {
			return true, errOAuthWrongClient
		}
		return true, s.store.RevokeOAuthRefreshToken(r.Context(), token)
	}
	revokeAccess := func() (bool, error) {
		_, key, found := s.store.FindAPIKey(secretHash(token))
		if !found || key.OAuthClientID == "" {
			// Personal API keys are managed in settings, not by OAuth clients.
			return false, nil
		}
		if clientID != "" && key.OAuthClientID != clientID {
			return true, errOAuthWrongClient
		}
		return true, s.store.RevokeOAuthAccessToken(r.Context(), key.SecretHash)
	}
	order := []func() (bool, error){revokeAccess, revokeRefresh}
	if hint == "refresh_token" {
		order = []func() (bool, error){revokeRefresh, revokeAccess}
	}
	for _, revoke := range order {
		found, err := revoke()
		if errors.Is(err, errOAuthWrongClient) {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "The token was not issued to this client")
			return
		}
		if err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not revoke token")
			return
		}
		if found {
			break
		}
	}
	w.WriteHeader(http.StatusOK)
}

var errOAuthWrongClient = errors.New("token belongs to another client")

// introspectOAuthToken implements RFC 7662 for confidential clients. A client
// may only introspect tokens issued to it; anything else is inactive.
func (s *server) introspectOAuthToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid form body")
		return
	}
	id, secret, basic, err := oauthClientCredentials(r)
	if err != nil {
		writeOAuthRequestError(w, err)
		return
	}
	if confidential, ok := s.store.OAuthClientSecret(id, secret); !confidential || !ok {
		writeOAuthInvalidClient(w, basic, "Introspection requires a confidential client")
		return
	}
	token := strings.TrimSpace(r.Form.Get("token"))
	if token == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	inactive := map[string]any{"active": false}
	if r.Form.Get("token_type_hint") != "refresh_token" {
		if workspace, key, found := s.store.FindAPIKey(secretHash(token)); found && key.OAuthClientID == id {
			actor := key.Actor
			if actor == "" {
				actor = "user"
			}
			result := map[string]any{"active": true, "scope": publicOAuthScopes(key.Scopes), "client_id": id, "token_type": "Bearer", "iat": key.CreatedAt.Unix(), "sub": key.SubjectID(), "actor": actor, "workspace": workspace, "grant_type": key.GrantType}
			if key.ExpiresAt != nil {
				result["exp"] = key.ExpiresAt.Unix()
			}
			if actor == "app" {
				result["app_user_id"] = key.SubjectID()
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
	}
	state, found, err := s.store.OAuthRefreshToken(r.Context(), token)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Could not introspect token")
		return
	}
	if !found || !state.Active || state.Grant.ClientID != id {
		writeJSON(w, http.StatusOK, inactive)
		return
	}
	actor := state.Grant.Actor
	if actor == "" {
		actor = "user"
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "scope": publicOAuthScopes(state.Grant.Scopes), "client_id": id, "token_type": "refresh_token", "iat": state.CreatedAt.Unix(), "exp": state.ExpiresAt.Unix(), "sub": state.Grant.UserID, "actor": actor, "workspace": state.Grant.WorkspaceKey})
}

// oauthClientDisplay names an OAuth client for "via <App>" attribution.
func (s *server) oauthClientDisplay(r *http.Request, clientID string) (string, string) {
	if client, ok := s.store.OAuthApplicationClient(clientID); ok {
		return client.ClientName, client.LogoURI
	}
	if client, err := s.store.OAuthClient(r.Context(), clientID); err == nil {
		return client.ClientName, client.LogoURI
	}
	return "", ""
}
