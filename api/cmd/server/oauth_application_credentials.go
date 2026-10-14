package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
	"flow/api/internal/store"
)

func validateOAuthInput(input *oauthInput) error {
	if input.Name != nil && (strings.TrimSpace(*input.Name) == "" || len(*input.Name) > 200) {
		return fmt.Errorf("application name must contain 1 to 200 characters")
	}
	if input.LogoURL != nil && *input.LogoURL != "" && !validOAuthMetadataURI(*input.LogoURL) && !strings.HasPrefix(*input.LogoURL, "/uploads/") {
		return fmt.Errorf("invalid application logo URL")
	}
	if input.DeveloperName != nil && len([]rune(strings.TrimSpace(*input.DeveloperName))) > 80 {
		return fmt.Errorf("developer name must be at most 80 characters")
	}
	if input.DeveloperURL != nil && *input.DeveloperURL != "" && !absoluteHTTPURL(*input.DeveloperURL, false) {
		return fmt.Errorf("developer URL must be an absolute HTTP or HTTPS URL")
	}
	if input.GitHubUsername != nil && strings.TrimSpace(*input.GitHubUsername) != "" && !validGitHubUsername(strings.TrimSpace(*input.GitHubUsername)) {
		return fmt.Errorf("Invalid GitHub username")
	}
	if input.RedirectURIs != nil && (len(*input.RedirectURIs) > 32 || slices.ContainsFunc(*input.RedirectURIs, func(uri string) bool { return !validOAuthRedirectURI(uri) })) {
		return fmt.Errorf("invalid OAuth redirect URI")
	}
	if input.Scopes != nil {
		scopes, err := normalizeOAuthApplicationScopes(*input.Scopes)
		if err != nil {
			return err
		}
		input.Scopes = &scopes
	}
	return nil
}

// normalizeOAuthApplicationScopes validates the scopes an application may
// request. Besides the application scopes it may allow the user-actor-only
// scopes admin and timeSchedule:write, which actor=app tokens never receive.
func normalizeOAuthApplicationScopes(values []string) ([]string, error) {
	userOnly := []string{}
	rest := []string{}
	for _, value := range values {
		value = canonicalOAuthScope(value)
		if domain.UserOnlyOAuthScope(value) {
			if !slices.Contains(userOnly, value) {
				userOnly = append(userOnly, value)
			}
			continue
		}
		rest = append(rest, value)
	}
	scopes, err := domain.NormalizeApplicationScopes(rest)
	if err != nil {
		return nil, err
	}
	return append(scopes, userOnly...), nil
}

func (s *server) issueClientCredentials(w http.ResponseWriter, r *http.Request, form url.Values) {
	if form.Get("grant_type") != "client_credentials" {
		writeOAuthError(w, 400, "unsupported_grant_type", "Use client_credentials")
		return
	}
	clientID, secret := form.Get("client_id"), form.Get("client_secret")
	if basicID, basicSecret, ok := r.BasicAuth(); ok {
		if clientID != "" || secret != "" {
			writeOAuthError(w, 400, "invalid_request", "Use only one client authentication method")
			return
		}
		clientID, secret = basicID, basicSecret
	}
	workspace, found := s.store.OAuthApplicationHome(clientID)
	if !found || workspaceKey(r) != "" && workspaceKey(r) != workspace {
		writeOAuthError(w, 401, "invalid_client", "Invalid client credentials")
		return
	}
	var scopes []string
	if form.Get("scope") != "" {
		scopes = strings.Fields(strings.ReplaceAll(form.Get("scope"), ",", " "))
		if slices.Contains(scopes, "admin") {
			writeOAuthError(w, 400, "invalid_scope", "the admin scope cannot be requested with actor=app")
			return
		}
		var err error
		scopes, err = domain.NormalizeApplicationScopes(scopes)
		if err != nil {
			writeOAuthError(w, 400, "invalid_scope", err.Error())
			return
		}
	}
	s.issueApplicationToken(w, r, workspace, clientID, secret, "", "app", scopes, "client_credentials")
}

func (s *server) issueApplicationToken(w http.ResponseWriter, r *http.Request, workspace, clientID, clientSecret, issuer, actor string, scopes []string, grantType string) {
	secret, err := randomSecret("flow_oauth_")
	if err != nil {
		writeOAuthError(w, 500, "server_error", "Could not generate token")
		return
	}
	duration := time.Hour
	if grantType == "client_credentials" {
		duration = 30 * 24 * time.Hour
	}
	now := time.Now().UTC()
	expires := now.Add(duration)
	key := domain.APIKey{ID: fmt.Sprintf("oauth_token_%d", now.UnixNano()), Name: "Application access token", Prefix: secret[:19], SecretHash: secretHash(secret), GrantType: grantType, CreatedAt: now, ExpiresAt: &expires}
	key, err = s.store.IssueApplicationCredential(r.Context(), workspace, clientID, clientSecret, issuer, actor, scopes, key, applicationApproved)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrOAuthGrantDisabled):
			writeOAuthError(w, 400, "unauthorized_client", "Enable client credentials for this application")
		case errors.Is(err, store.ErrOAuthTokenLimit):
			writeOAuthError(w, 429, "temporarily_unavailable", "Application token limit reached")
		case errors.Is(err, store.ErrAuthForbidden):
			writeOAuthError(w, 401, "invalid_client", "Client, installation, scopes or workspace approval is invalid")
		default:
			writeOAuthError(w, 500, "server_error", "Could not issue application credential")
		}
		return
	}
	if grantType == "developer_token" && key.Actor == "app" {
		// Remembered so "Create & copy token › App" copies this token again
		// instead of minting another one (one app token at a time).
		if err := s.store.SetApplicationDeveloperToken(r.Context(), workspace, clientID, key.ID, secret); err != nil {
			writeOAuthError(w, 500, "server_error", "Could not store developer token")
			return
		}
	}
	if grantType == "developer_token" {
		if app, ok := s.store.OAuthApplicationByClientID(clientID); ok {
			s.auditAPIKeyCredential(r, "developer_token_created", app)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, applicationTokenBody(secret, key, int(duration.Seconds())))
}

func applicationTokenBody(secret string, key domain.APIKey, expiresIn int) publicapi.ApplicationToken {
	response := publicapi.ApplicationToken{AccessToken: secret, TokenType: "Bearer", ExpiresIn: expiresIn, Scope: publicOAuthScopes(key.Scopes), Actor: key.Actor, SubjectUserID: key.SubjectID()}
	if key.Actor == "app" {
		response.AppUserID = key.SubjectID()
	}
	return response
}

// developerTokenApplication finds the application of a developer token
// request; only workspace admins may create or copy developer tokens.
func (s *server) developerTokenApplication(w http.ResponseWriter, r *http.Request) (*domain.Bootstrap, domain.OAuthApplication, bool) {
	data := s.workspaceData(r)
	if !workspaceAdminRole(data.ViewerRole) {
		writeError(w, 403, "Workspace admin access required")
		return nil, domain.OAuthApplication{}, false
	}
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == r.PathValue("id") })
	if index < 0 {
		writeError(w, 404, "Application not found")
		return nil, domain.OAuthApplication{}, false
	}
	return &data, data.OAuthApplications[index], true
}

// existingAppDeveloperToken returns the application's app developer token
// while it is still active (not revoked, expired or replaced).
func (s *server) existingAppDeveloperToken(r *http.Request, data *domain.Bootstrap, app domain.OAuthApplication) (string, domain.APIKey, bool) {
	keyID, token, ok, err := s.store.ApplicationDeveloperToken(r.Context(), data.Workspace.URLKey, app.ClientID)
	if err != nil || !ok {
		return "", domain.APIKey{}, false
	}
	workspace, key, found := s.store.FindAPIKey(secretHash(token))
	if !found || workspace != data.Workspace.URLKey || key.ID != keyID || key.OAuthClientID != app.ClientID || key.Actor != "app" || key.RevokedAt != nil || key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now()) {
		return "", domain.APIKey{}, false
	}
	return token, key, true
}

func existingDeveloperTokenResponse(token string, key domain.APIKey) publicapi.ApplicationToken {
	expiresIn := 0
	if key.ExpiresAt != nil {
		expiresIn = int(time.Until(*key.ExpiresAt).Seconds())
	}
	response := applicationTokenBody(token, key, expiresIn)
	response.Existing = true
	return response
}

// GET /api/oauth-applications/{id}/developer-token returns the app user's
// active developer token (the reference app's "Existing app token copied to
// clipboard"), or 404 when there is none and scopes must be chosen.
func (s *server) getApplicationDeveloperToken(w http.ResponseWriter, r *http.Request) {
	data, app, ok := s.developerTokenApplication(w, r)
	if !ok {
		return
	}
	token, key, found := s.existingAppDeveloperToken(r, data, app)
	if !found {
		writeError(w, http.StatusNotFound, "No active app developer token")
		return
	}
	s.auditAPIKeyCredential(r, "developer_token_read", app)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, existingDeveloperTokenResponse(token, key))
}

// POST /api/oauth-applications/{id}/developer-token is the reference app's "Create &
// copy token": actor=user creates a read/write token for the admin, and
// actor=app creates a token for the application's app user with the chosen
// scopes, or returns the app user's active token (existing: true).
func (s *server) createApplicationDeveloperToken(w http.ResponseWriter, r *http.Request) {
	var input publicapi.DeveloperTokenInput
	if !decodeJSON(w, r, &input) {
		return
	}
	data, app, ok := s.developerTokenApplication(w, r)
	if !ok {
		return
	}
	if input.Actor == "" {
		input.Actor = "app"
	}
	if input.Actor == "app" {
		if token, key, found := s.existingAppDeveloperToken(r, data, app); found {
			s.auditAPIKeyCredential(r, "developer_token_read", app)
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, existingDeveloperTokenResponse(token, key))
			return
		}
	}
	if input.Actor == "user" && input.Scopes == nil {
		// The reference app's User token: read and write, as far as the app allows.
		input.Scopes = slices.DeleteFunc([]string{"read", "write"}, func(scope string) bool { return scope != "read" && !slices.Contains(app.Scopes, scope) })
	}
	for index, scope := range input.Scopes {
		input.Scopes[index] = canonicalOAuthScope(scope)
	}
	s.issueApplicationToken(w, r, data.Workspace.URLKey, app.ClientID, "", data.Viewer.ID, input.Actor, input.Scopes, "developer_token")
}

func (s *server) rotateOAuthApplicationSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := randomSecret("flow_secret_")
	if err != nil {
		writeError(w, 500, "Could not rotate secret")
		return
	}
	id := r.PathValue("id")
	var rotated domain.OAuthApplication
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_application.secret_rotated", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == id })
		if index < 0 {
			return errNotFound
		}
		data.OAuthApplications[index].ClientSecret = secret
		data.OAuthApplications[index].CredentialsAuthorizedBy = requestActor(s, r).ID
		data.OAuthApplications[index].UpdatedAt = time.Now().UTC()
		rotated = data.OAuthApplications[index]
		return nil
	})
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	s.auditAPIKeyCredential(r, "client_secret_rotated", rotated)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, publicapi.OAuthApplicationClientSecret{ClientSecret: secret})
}
