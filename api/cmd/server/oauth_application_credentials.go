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
	"flow/api/internal/store"
)

func validateOAuthInput(input *oauthInput) error {
	if input.Name != nil && (strings.TrimSpace(*input.Name) == "" || len(*input.Name) > 200) {
		return fmt.Errorf("application name must contain 1 to 200 characters")
	}
	if input.LogoURL != nil && *input.LogoURL != "" && !validOAuthMetadataURI(*input.LogoURL) && !strings.HasPrefix(*input.LogoURL, "/uploads/") {
		return fmt.Errorf("invalid application logo URL")
	}
	if input.RedirectURIs != nil && (len(*input.RedirectURIs) > 20 || slices.ContainsFunc(*input.RedirectURIs, func(uri string) bool { return !validOAuthRedirectURI(uri) })) {
		return fmt.Errorf("invalid OAuth redirect URI")
	}
	if input.Scopes != nil {
		scopes, err := domain.NormalizeApplicationScopes(*input.Scopes)
		if err != nil {
			return err
		}
		input.Scopes = &scopes
	}
	return nil
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
	response := map[string]any{"access_token": secret, "token_type": "Bearer", "expires_in": int(duration.Seconds()), "scope": strings.Join(key.Scopes, " "), "actor": key.Actor, "subject_user_id": key.SubjectID()}
	if key.Actor == "app" {
		response["app_user_id"] = key.SubjectID()
	}
	writeJSON(w, 200, response)
}

func (s *server) createApplicationDeveloperToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Actor  string   `json:"actor"`
		Scopes []string `json:"scopes"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	data := s.workspaceData(r)
	if !workspaceAdminRole(data.ViewerRole) {
		writeError(w, 403, "Workspace admin access required")
		return
	}
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == r.PathValue("id") })
	if index < 0 {
		writeError(w, 404, "Application not found")
		return
	}
	if input.Actor == "" {
		input.Actor = "app"
	}
	s.issueApplicationToken(w, r, data.Workspace.URLKey, data.OAuthApplications[index].ClientID, "", data.Viewer.ID, input.Actor, input.Scopes, "developer_token")
}

func (s *server) rotateOAuthApplicationSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := randomSecret("flow_secret_")
	if err != nil {
		writeError(w, 500, "Could not rotate secret")
		return
	}
	id := r.PathValue("id")
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_application.secret_rotated", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == id })
		if index < 0 {
			return errNotFound
		}
		data.OAuthApplications[index].ClientSecret = secret
		data.OAuthApplications[index].CredentialsAuthorizedBy = requestActor(s, r).ID
		data.OAuthApplications[index].UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	writeJSON(w, 200, map[string]string{"clientSecret": secret})
}
