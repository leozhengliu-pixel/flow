package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
	"flow/api/internal/store"
)

// registerOAuthPlatformRoutes adds the OAuth developer-platform endpoints:
// token introspection, application manifests and the per-application
// install list. (GET /api/viewer lives with the public API resources.)
func (s *server) registerOAuthPlatformRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /oauth/introspect", s.introspectOAuthToken)
	mux.HandleFunc("GET /.well-known/oauth-app-manifest.schema.json", s.oauthAppManifestSchema)
	mux.HandleFunc("POST /api/oauth-applications/manifest", s.createOAuthApplicationFromManifest)
	mux.HandleFunc("POST /api/oauth-applications/manifest/validate", s.validateOAuthAppManifest)
	mux.HandleFunc("GET /api/oauth-applications/{id}/manifest", s.exportOAuthApplicationManifest)
	mux.HandleFunc("PUT /api/oauth-applications/{id}/manifest", s.updateOAuthApplicationFromManifest)
	mux.HandleFunc("GET /api/oauth-applications/{id}", s.getOAuthApplication)
	mux.HandleFunc("GET /api/oauth-applications/{id}/installs", s.listOAuthApplicationInstalls)
	mux.HandleFunc("GET /api/oauth-applications/{id}/client-secret", s.getOAuthApplicationClientSecret)
	mux.HandleFunc("GET /api/oauth-applications/{id}/developer-token", s.getApplicationDeveloperToken)
	mux.HandleFunc("GET /api/oauth-applications/{id}/webhook/secret", s.getApplicationWebhookSecret)
	mux.HandleFunc("GET /api/oauth/authorized-applications", s.listAuthorizedApplications)
	mux.HandleFunc("GET /api/oauth/installation", s.oauthConsentInstallation)
	mux.HandleFunc("POST /api/oauth-applications/icon", s.uploadOAuthApplicationIcon)
}

// GET /api/oauth-applications/{id}/client-secret returns the application's
// current client secret for the settings page's copy button (the reference app keeps the
// secret masked but copyable). Workspace admins: signed in, or with an admin
// API key (see apiKeyOAuthCredentialDenial), which is audited.
func (s *server) getOAuthApplicationClientSecret(w http.ResponseWriter, r *http.Request) {
	secret, ok := s.store.OAuthApplicationSecret(workspaceKey(r), r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Application not found")
		return
	}
	if app, found := s.workspaceOAuthApplication(r); found {
		s.auditAPIKeyCredential(r, "client_secret_read", app)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, publicapi.OAuthApplicationClientSecret{ClientSecret: secret})
}

// workspaceOAuthApplication is the request workspace's application {id}.
func (s *server) workspaceOAuthApplication(r *http.Request) (domain.OAuthApplication, bool) {
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == r.PathValue("id") })
	if index < 0 {
		return domain.OAuthApplication{}, false
	}
	return data.OAuthApplications[index], true
}

// oauthCredentialPath reports the routes that read, rotate or create an
// OAuth application's credentials: the client secret, developer tokens and
// the webhook signing secret.
func oauthCredentialPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "oauth-applications" || parts[2] == "" {
		return false
	}
	switch strings.Join(parts[3:], "/") {
	case "client-secret", "rotate-secret", "developer-token", "webhook/secret", "webhook/rotate-secret":
		return true
	}
	return false
}

// apiKeyOAuthCredentialDenial explains why an API key may not use an OAuth
// application credential route, or returns "" when it may: a personal API
// key (not an OAuth token) with the admin scope that is not restricted to
// selected teams. The owner must also be a workspace admin, which the
// admin-route check applies to every /api/oauth-applications request.
func apiKeyOAuthCredentialDenial(key domain.APIKey) string {
	switch {
	case key.OAuthClientID != "":
		return "OAuth tokens cannot manage application credentials; use an API key with the admin scope"
	case !apiKeyHasScope(key, "admin"):
		return "Application credentials require an API key with the admin scope"
	case apiKeyTeamRestrictionSelected(key):
		return "Application credentials require an API key with access to all teams"
	}
	return ""
}

// auditAPIKeyCredential writes an audit entry when an application credential
// is read, rotated or created with an API key: the key's owner (the entry's
// actor), the key, the application and the action. The credential itself is
// never recorded.
func (s *server) auditAPIKeyCredential(r *http.Request, action string, app domain.OAuthApplication) {
	key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
	if !ok {
		return
	}
	workspace := workspaceKey(r)
	ctx := store.WithMetadataFields(context.WithoutCancel(r.Context()), "auditLog")
	err := s.store.MutateWorkspace(ctx, workspace, "audit.oauth_application_credential", app.ID, map[string]any{"action": action}, func(data *domain.Bootstrap) error {
		appendAudit(data, action, "oauth_application", app.ID, map[string]any{"application": app.Name, "clientId": app.ClientID, "apiKeyId": key.ID, "apiKeyName": key.Name, "authentication": "apiKey"})
		return nil
	})
	if err != nil {
		log.Printf("oauth credential audit workspace=%s application=%s: %v", workspace, app.ID, err)
	}
}

// applicationRealtime reports whether a registered application has a webhook,
// which the reference app presents as the permission "Receive realtime updates about
// your workspace" (not an OAuth scope: tokens never carry it).
func (s *server) applicationRealtime(clientID string) bool {
	app, ok := s.store.OAuthApplicationByClientID(clientID)
	return ok && app.WebhookEnabled && app.WebhookURL != ""
}

// activeApplicationInstallation is the workspace's active actor=app
// installation of a client, if any.
func (s *server) activeApplicationInstallation(ctx context.Context, workspace, clientID string) (domain.ApplicationInstallation, bool) {
	installations, err := s.store.ListApplications(ctx, workspace)
	if err != nil {
		return domain.ApplicationInstallation{}, false
	}
	index := slices.IndexFunc(installations, func(item domain.ApplicationInstallation) bool {
		return item.ClientID == clientID && item.Active && !item.Builtin
	})
	if index < 0 {
		return domain.ApplicationInstallation{}, false
	}
	return installations[index], true
}

// consentInstallation is what the consent screen shows for an application
// that is already installed in the workspace (the reference app's "already installed" and
// "Update access" states): who installed it and when, its scopes and whether
// it already receives realtime updates.
type consentInstallation struct {
	InstalledBy   string    `json:"installedBy"`
	InstallerName string    `json:"installerName"`
	CreatedAt     time.Time `json:"createdAt"`
	Scopes        []string  `json:"scopes"`
	Realtime      bool      `json:"realtime"`
}

// GET /api/oauth/installation?workspace=&client_id= returns the active
// actor=app installation of the client in the workspace (null when it is not
// installed). Workspace admins only, like installing.
func (s *server) oauthConsentInstallation(w http.ResponseWriter, r *http.Request) {
	access, ok := s.oauthWorkspaceForRequest(w, r, r.URL.Query().Get("workspace"), s.oauthRequestUser(r))
	if !ok {
		return
	}
	if !workspaceAdminRole(access.ViewerRole) {
		writeError(w, http.StatusForbidden, "Administrator required to install an application")
		return
	}
	installation, found := s.activeApplicationInstallation(r.Context(), access.Workspace.URLKey, r.URL.Query().Get("client_id"))
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"installation": nil})
		return
	}
	result := consentInstallation{InstalledBy: installation.InstalledBy, CreatedAt: installation.CreatedAt, Scopes: slices.Clone(installation.Scopes), Realtime: !installation.WithoutRealtime}
	if result.Scopes == nil {
		result.Scopes = []string{}
	}
	if metadata, ok := s.store.WorkspaceMetadataFields(access.Workspace.URLKey, "users"); ok {
		if index := slices.IndexFunc(metadata.Users, func(user domain.User) bool { return user.ID == installation.InstalledBy }); index >= 0 {
			result.InstallerName = cmp.Or(metadata.Users[index].DisplayName, metadata.Users[index].Name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"installation": result})
}

// GET /api/oauth-applications/{id}/webhook/secret returns the application's
// webhook signing secret for the settings page (the reference app shows it in the edit
// form and copies it from the application page). Workspace admins: signed
// in, or with an admin API key (audited).
func (s *server) getApplicationWebhookSecret(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == r.PathValue("id") })
	if index < 0 {
		writeError(w, http.StatusNotFound, "Application not found")
		return
	}
	secret, ok, err := s.store.WebhookSigningSecret(r.Context(), store.ApplicationWebhookSecretRef(data.OAuthApplications[index].ClientID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the webhook signing secret")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "The application has no webhook signing secret")
		return
	}
	s.auditAPIKeyCredential(r, "webhook_secret_read", data.OAuthApplications[index])
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, publicapi.ApplicationWebhookSecret{Secret: secret})
}

// GET /api/oauth-applications/{id}/installs lists this workspace's grants of
// an application: user authorizations and the actor=app installation.
func (s *server) listOAuthApplicationInstalls(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == r.PathValue("id") })
	if index < 0 {
		writeError(w, http.StatusNotFound, "Application not found")
		return
	}
	clientID := data.OAuthApplications[index].ClientID
	authorizations := []domain.OAuthAuthorization{}
	for _, item := range data.OAuthAuthorizations {
		if item.ClientID == clientID && item.RevokedAt == nil {
			authorizations = append(authorizations, item)
		}
	}
	installations, err := s.store.ListApplications(r.Context(), data.Workspace.URLKey)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	installations = slices.DeleteFunc(installations, func(item domain.ApplicationInstallation) bool { return item.ClientID != clientID })
	writeJSON(w, http.StatusOK, publicapi.OAuthApplicationInstalls{Authorizations: authorizations, Installations: installations})
}

// githubAppUsers maps lower-cased GitHub usernames to the app users of the
// workspace's active application installations whose application set that
// username (the reference app's "GitHub username": commits and comments in GitHub by
// that account belong to the app user).
func (s *server) githubAppUsers(ctx context.Context, workspace string) map[string]domain.User {
	installations, err := s.store.ListApplications(ctx, workspace)
	if err != nil {
		return nil
	}
	users := map[string]domain.User{}
	for _, installation := range installations {
		if !installation.Active || installation.UserID == "" {
			continue
		}
		app, ok := s.store.OAuthApplicationByClientID(installation.ClientID)
		if !ok || app.GitHubUsername == "" {
			continue
		}
		users[strings.ToLower(app.GitHubUsername)] = installation.User()
	}
	return users
}

// githubActor is the app user associated with a GitHub login, or fallback.
func githubActor(users map[string]domain.User, login string, fallback domain.User) domain.User {
	if user, ok := users[strings.ToLower(strings.TrimSpace(login))]; ok && login != "" {
		return user
	}
	return fallback
}

// oauthAttributionInput is the optional attribution on
// create requests made with an actor=app token.
type oauthAttributionInput struct {
	CreateAsUser   *string `json:"createAsUser"`
	DisplayIconURL *string `json:"displayIconUrl"`
}

// oauthAttributionRequest reports create requests whose body may carry
// createAsUser / displayIconUrl: issues, comments, attachments and documents.
func oauthAttributionRequest(r *http.Request) bool {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" {
		return false
	}
	switch {
	case len(parts) == 2:
		return parts[1] == "issues" || parts[1] == "issue-records" || parts[1] == "documents"
	case len(parts) == 4:
		return (parts[1] == "issues" || parts[1] == "issue-records" || parts[1] == "documents") && (parts[3] == "comments" || parts[3] == "attachments" || parts[3] == "links" || parts[3] == "link-attachments")
	case len(parts) == 5:
		return (parts[1] == "issues" || parts[1] == "issue-records") && parts[3] == "attachments" && parts[4] == "from-url"
	}
	return false
}

// oauthAttributionError is a client error in createAsUser / displayIconUrl.
type oauthAttributionError string

func (e oauthAttributionError) Error() string { return string(e) }

// oauthRequestAttribution builds the "via <App>" attribution for a request
// authenticated with an OAuth token. User-actor tokens are attributed to the
// person "via" the application; actor=app tokens carry attribution only when
// the request names the external user with createAsUser.
func (s *server) oauthRequestAttribution(r *http.Request, key domain.APIKey) (*domain.ActorBot, error) {
	if key.OAuthClientID == "" || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		// Reads create nothing to attribute.
		return nil, nil
	}
	var input oauthAttributionInput
	if oauthAttributionRequest(r) && r.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxJSONRequestBytes+1))
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		_ = json.Unmarshal(raw, &input)
	}
	name, icon := s.oauthClientDisplay(r, key.OAuthClientID)
	bot := &domain.ActorBot{ID: key.OAuthClientID, Type: domain.ActorBotTypeOAuthApplication, Name: name, AvatarURL: icon}
	createAs := ""
	if input.CreateAsUser != nil {
		createAs = strings.TrimSpace(*input.CreateAsUser)
	}
	iconURL := ""
	if input.DisplayIconURL != nil {
		iconURL = strings.TrimSpace(*input.DisplayIconURL)
	}
	if key.Actor != "app" {
		if createAs != "" || iconURL != "" {
			return nil, oauthAttributionError("createAsUser and displayIconUrl are only available to actor=app tokens")
		}
		return bot, nil
	}
	if createAs == "" {
		if iconURL != "" {
			return nil, oauthAttributionError("displayIconUrl can only be used with createAsUser")
		}
		return nil, nil
	}
	if utf8.RuneCountInString(createAs) > 100 {
		return nil, oauthAttributionError("createAsUser must be at most 100 characters")
	}
	if iconURL != "" && !absoluteHTTPURL(iconURL, false) {
		return nil, oauthAttributionError("displayIconUrl must be an absolute HTTP or HTTPS URL")
	}
	bot.UserDisplayName, bot.UserAvatarURL = createAs, iconURL
	return bot, nil
}

// viewerBotActor returns the request's OAuth attribution for a record made by
// actor (the mutation's viewer). Other users' records are never attributed.
func viewerBotActor(data *domain.Bootstrap, actor domain.User) *domain.ActorBot {
	if data == nil || data.Viewer.Via == nil || actor.ID == "" || actor.ID != data.Viewer.ID {
		return nil
	}
	copy := *data.Viewer.Via
	return &copy
}
