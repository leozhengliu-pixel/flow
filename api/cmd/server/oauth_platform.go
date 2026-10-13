package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
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
	mux.HandleFunc("GET /api/oauth-applications/{id}/installs", s.listOAuthApplicationInstalls)
	mux.HandleFunc("GET /api/oauth-applications/{id}/client-secret", s.getOAuthApplicationClientSecret)
	mux.HandleFunc("GET /api/oauth/authorized-applications", s.listAuthorizedApplications)
	mux.HandleFunc("POST /api/oauth-applications/icon", s.uploadOAuthApplicationIcon)
}

// GET /api/oauth-applications/{id}/client-secret returns the application's
// current client secret for the settings page's copy button (the reference app keeps the
// secret masked but copyable). Workspace admins only, never with an API key.
func (s *server) getOAuthApplicationClientSecret(w http.ResponseWriter, r *http.Request) {
	secret, ok := s.store.OAuthApplicationSecret(workspaceKey(r), r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Application not found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"clientSecret": secret})
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
	writeJSON(w, http.StatusOK, map[string]any{"authorizations": authorizations, "installations": installations})
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
