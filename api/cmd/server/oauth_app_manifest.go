package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/publicapi"
	"flow/api/internal/store"
	"flow/api/internal/websearch"
)

// OAuth application manifests describe an OAuth application declaratively,
// with Flow extensions: oauth.scopes (the scopes the application may
// request), agent (shorthand for the app:assignable / app:mentionable
// scopes of an agent application), github.username (the form's "GitHub
// username") and the write-only webhook.secret the settings form generates.
const oauthAppManifestSchemaVersion = "1.0.0"

// The manifest types are part of the public contract (publicapi).
type (
	oauthAppManifest         = publicapi.OAuthAppManifest
	oauthAppManifestGitHub   = publicapi.OAuthAppManifestGitHub
	oauthAppManifestDisplay  = publicapi.OAuthAppManifestDisplay
	oauthAppManifestDev      = publicapi.OAuthAppManifestDev
	oauthAppManifestOAuth    = publicapi.OAuthAppManifestOAuth
	oauthAppManifestWebhook  = publicapi.OAuthAppManifestWebhook
	oauthAppManifestAgentCap = publicapi.OAuthAppManifestAgent
	oauthAppManifestIssue    = publicapi.OAuthAppManifestIssue
	// manifestApplicationResponse is the saved application plus, the first
	// time its webhook is enabled, the webhook signing secret.
	manifestApplicationResponse = publicapi.ManifestApplication
)

// githubUsernamePattern accepts GitHub user names (alphanumerics and single
// inner hyphens) and GitHub App bot names with the "[bot]" suffix.
var githubUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9]|-[A-Za-z0-9])*(\[bot\])?$`)

func validGitHubUsername(name string) bool {
	return githubUsernamePattern.MatchString(name) && len(strings.TrimSuffix(name, "[bot]")) <= 39
}

// githubUsernameTakenError is the reference app's error when another user (here:
// another application's app user) is already associated with the account.
type githubUsernameTakenError struct{}

func (githubUsernameTakenError) Error() string {
	return "There is already a user in Flow associated with this GitHub account."
}
func (githubUsernameTakenError) Unwrap() error { return errInvalid }

// checkGitHubUsernameAvailable rejects a GitHub username already associated
// with another application of the workspace.
func checkGitHubUsernameAvailable(data *domain.Bootstrap, applicationID, username string) error {
	if username == "" {
		return nil
	}
	if slices.ContainsFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool {
		return app.ID != applicationID && strings.EqualFold(app.GitHubUsername, username)
	}) {
		return githubUsernameTakenError{}
	}
	return nil
}

// webhookSecretPattern is the format of webhook signing secrets
// (randomWebhookSecret); the settings form generates one in the same shape.
var webhookSecretPattern = regexp.MustCompile(`^flow_wh_[A-Za-z0-9_-]{32,64}$`)

var oauthManifestReservedName = regexp.MustCompile(`(?i)\bflow\b`)

func decodeOAuthAppManifest(raw []byte) (oauthAppManifest, []oauthAppManifestIssue) {
	var manifest oauthAppManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, []oauthAppManifestIssue{{Path: "", Message: "manifest must be a JSON object matching the schema: " + err.Error()}}
	}
	return manifest, nil
}

func absoluteHTTPURL(raw string, httpsOnly bool) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme == "https" || !httpsOnly && parsed.Scheme == "http"
}

// publicWebhookHost rejects loopback, private-network and internal hosts. It
// is a syntactic check; deliveries re-check resolved addresses.
func publicWebhookHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return websearch.PublicAddr(addr)
	}
	return true
}

func manifestValuesUnique(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

// validate checks the manifest against the published schema and returns the
// normalized application scopes and webhook resource types.
//
// Webhook URLs go through the same check as every Flow webhook (webhookURL,
// the server's validWebhookURL); resource types are canonicalised by the
// webhook delivery code and stored under their payload type names, exactly
// as PUT /api/oauth-applications/{id}/webhook stores them.
func validateOAuthAppManifestContent(m *oauthAppManifest, webhookURL func(string) (string, error)) ([]string, []string, []oauthAppManifestIssue) {
	issues := []oauthAppManifestIssue{}
	add := func(path, format string, args ...any) {
		issues = append(issues, oauthAppManifestIssue{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if m.SchemaVersion != oauthAppManifestSchemaVersion {
		add("schemaVersion", "schemaVersion must be %q", oauthAppManifestSchemaVersion)
	}
	if m.Distribution != "" && m.Distribution != "private" && m.Distribution != "public" {
		add("distribution", "distribution must be private or public")
	}
	if m.Display != nil {
		if utf8.RuneCountInString(m.Display.Description) > 1000 {
			add("display.description", "description must be at most 1000 characters")
		}
		// An icon uploaded on the settings page is served by Flow itself
		// (/uploads/oauth_app_icon_…); any other icon must be HTTPS.
		if icon := strings.TrimSpace(m.Display.IconURL); icon != "" && !uploadedOAuthAppIcon(icon) && !validApplicationIconURL(icon, false) {
			add("display.iconUrl", "iconUrl must be an absolute HTTPS URL")
		}
	}
	if m.Developer == nil {
		add("developer.name", "developer.name is required")
	} else if count := utf8.RuneCountInString(strings.TrimSpace(m.Developer.Name)); count < 2 || count > 80 {
		add("developer.name", "developer.name must contain 2 to 80 characters")
	}
	var scopes []string
	if m.OAuth == nil {
		add("oauth", "oauth is required")
	} else {
		name := strings.TrimSpace(m.OAuth.ClientName)
		if count := utf8.RuneCountInString(name); count < 2 || count > 80 {
			add("oauth.client_name", "client_name must contain 2 to 80 characters")
		}
		if oauthManifestReservedName.MatchString(name) {
			add("oauth.client_name", "client_name must not contain Flow")
		}
		if strings.Contains(strings.ToLower(name), "http://") || strings.Contains(strings.ToLower(name), "https://") {
			add("oauth.client_name", "client_name must not contain a URL")
		}
		// Like the reference app's "Developer URL", the homepage is required only for
		// public applications; when given it must be an HTTP(S) URL.
		if strings.TrimSpace(m.OAuth.ClientURI) == "" {
			if m.Distribution == "public" {
				add("oauth.client_uri", "client_uri is required for public applications")
			}
		} else if !absoluteHTTPURL(m.OAuth.ClientURI, false) {
			add("oauth.client_uri", "client_uri must be an absolute HTTP or HTTPS URL")
		}
		if len(m.OAuth.RedirectURIs) < 1 || len(m.OAuth.RedirectURIs) > 32 {
			add("oauth.redirect_uris", "redirect_uris must contain 1 to 32 URIs")
		}
		if !manifestValuesUnique(m.OAuth.RedirectURIs) {
			add("oauth.redirect_uris", "redirect_uris must be unique")
		}
		for index, uri := range m.OAuth.RedirectURIs {
			if !validOAuthRedirectURI(uri) {
				add(fmt.Sprintf("oauth.redirect_uris[%d]", index), "redirect URI must be HTTPS (or HTTP on a loopback host) without a fragment")
			}
		}
		if len(m.OAuth.GrantTypes) == 0 {
			m.OAuth.GrantTypes = []string{"authorization_code"}
		}
		if !manifestValuesUnique(m.OAuth.GrantTypes) {
			add("oauth.grant_types", "grant_types must be unique")
		}
		if !slices.Contains(m.OAuth.GrantTypes, "authorization_code") {
			add("oauth.grant_types", "grant_types must include authorization_code")
		}
		for _, grant := range m.OAuth.GrantTypes {
			if grant != "authorization_code" && grant != "client_credentials" {
				add("oauth.grant_types", "unsupported grant type %q", grant)
			}
		}
		requested := slices.Clone(m.OAuth.Scopes)
		if len(requested) == 0 {
			requested = []string{"read"}
		}
		if m.Agent != nil && m.Agent.Assignable {
			requested = append(requested, "app:assignable")
		}
		if m.Agent != nil && m.Agent.Mentionable {
			requested = append(requested, "app:mentionable")
		}
		if !slices.ContainsFunc(requested, func(scope string) bool { return canonicalOAuthScope(scope) == "read" }) {
			requested = append([]string{"read"}, requested...)
		}
		var err error
		scopes, err = normalizeOAuthApplicationScopes(requested)
		if err != nil {
			add("oauth.scopes", "%s", err.Error())
		}
	}
	var resourceTypes []string
	if m.Webhook != nil {
		parsed, err := url.Parse(strings.TrimSpace(m.Webhook.URL))
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			add("webhook.url", "webhook.url must be an absolute HTTPS URL")
		} else if !publicWebhookHost(parsed.Hostname()) {
			add("webhook.url", "webhook.url must not point at a loopback, private-network or internal host")
		} else if webhookURL != nil {
			if _, err := webhookURL(m.Webhook.URL); err != nil {
				add("webhook.url", "%s", strings.TrimPrefix(err.Error(), errInvalid.Error()+": "))
			}
		}
		if m.Webhook.Secret != "" && !webhookSecretPattern.MatchString(m.Webhook.Secret) {
			add("webhook.secret", "webhook.secret must be a flow_wh_ signing secret")
		}
		if len(m.Webhook.ResourceTypes) == 0 {
			add("webhook.resourceTypes", "webhook.resourceTypes must contain at least one resource type")
		}
		for _, value := range m.Webhook.ResourceTypes {
			id, ok := canonicalWebhookResourceType(value)
			if !ok {
				add("webhook.resourceTypes", "unsupported resource type %q", value)
				continue
			}
			name := webhookTypeName(id)
			if slices.Contains(resourceTypes, name) {
				add("webhook.resourceTypes", "webhook.resourceTypes must be unique")
				continue
			}
			resourceTypes = append(resourceTypes, name)
		}
	}
	if m.GitHub != nil {
		if name := strings.TrimSpace(m.GitHub.Username); name != "" && !validGitHubUsername(name) {
			add("github.username", "Invalid GitHub username")
		}
	}
	return scopes, resourceTypes, issues
}

// applyTo writes the manifest into an application. Absent optional blocks
// clear the corresponding settings: a manifest is the whole configuration.
func applyOAuthAppManifest(m oauthAppManifest, app *domain.OAuthApplication, scopes, resourceTypes []string) {
	app.Name = strings.TrimSpace(m.OAuth.ClientName)
	app.DeveloperName = strings.TrimSpace(m.Developer.Name)
	app.DeveloperURL = strings.TrimSpace(m.OAuth.ClientURI)
	app.Description, app.LogoURL = "", ""
	if m.Display != nil {
		app.Description = strings.TrimSpace(m.Display.Description)
		app.LogoURL = strings.TrimSpace(m.Display.IconURL)
	}
	app.RedirectURIs = normalizedStrings(m.OAuth.RedirectURIs)
	app.GitHubUsername = ""
	if m.GitHub != nil {
		app.GitHubUsername = strings.TrimSpace(m.GitHub.Username)
	}
	app.SupportsClientCredentials = slices.Contains(m.OAuth.GrantTypes, "client_credentials")
	app.PublicEnabled = m.Distribution == "public"
	app.Scopes = scopes
	app.WebhookEnabled, app.WebhookURL, app.WebhookResourceTypes = false, "", nil
	if m.Webhook != nil {
		app.WebhookEnabled = m.Webhook.Enabled == nil || *m.Webhook.Enabled
		app.WebhookURL = strings.TrimSpace(m.Webhook.URL)
		app.WebhookResourceTypes = resourceTypes
	}
	app.UpdatedAt = time.Now().UTC()
}

func oauthAppManifestSchemaURL(r *http.Request) string {
	return externalBaseURL(r) + "/.well-known/oauth-app-manifest.schema.json"
}

// exportOAuthAppManifest renders an application as a manifest.
func exportOAuthAppManifest(r *http.Request, app domain.OAuthApplication) oauthAppManifest {
	manifest := oauthAppManifest{Schema: oauthAppManifestSchemaURL(r), SchemaVersion: oauthAppManifestSchemaVersion, Distribution: "private"}
	if app.PublicEnabled {
		manifest.Distribution = "public"
	}
	if app.Description != "" || app.LogoURL != "" {
		manifest.Display = &oauthAppManifestDisplay{Description: app.Description, IconURL: app.LogoURL}
	}
	manifest.Developer = &oauthAppManifestDev{Name: app.DeveloperName}
	grants := []string{"authorization_code"}
	if app.SupportsClientCredentials {
		grants = append(grants, "client_credentials")
	}
	scopes := slices.DeleteFunc(slices.Clone(app.Scopes), func(scope string) bool { return scope == "app:assignable" || scope == "app:mentionable" })
	manifest.OAuth = &oauthAppManifestOAuth{ClientName: app.Name, ClientURI: app.DeveloperURL, RedirectURIs: slices.Clone(app.RedirectURIs), GrantTypes: grants, Scopes: scopes}
	if manifest.OAuth.RedirectURIs == nil {
		manifest.OAuth.RedirectURIs = []string{}
	}
	if slices.Contains(app.Scopes, "app:assignable") || slices.Contains(app.Scopes, "app:mentionable") {
		manifest.Agent = &oauthAppManifestAgentCap{Assignable: slices.Contains(app.Scopes, "app:assignable"), Mentionable: slices.Contains(app.Scopes, "app:mentionable")}
	}
	if app.WebhookURL != "" {
		enabled := app.WebhookEnabled
		manifest.Webhook = &oauthAppManifestWebhook{Enabled: &enabled, URL: app.WebhookURL, ResourceTypes: slices.Clone(app.WebhookResourceTypes)}
	}
	if app.GitHubUsername != "" {
		manifest.GitHub = &oauthAppManifestGitHub{Username: app.GitHubUsername}
	}
	return manifest
}

func (s *server) readOAuthAppManifest(w http.ResponseWriter, r *http.Request) (oauthAppManifest, []string, []string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(r.Body); err != nil {
		writeError(w, http.StatusBadRequest, "manifest body is too large or unreadable")
		return oauthAppManifest{}, nil, nil, false
	}
	manifest, issues := decodeOAuthAppManifest(buffer.Bytes())
	if issues != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid OAuth application manifest", "issues": issues})
		return manifest, nil, nil, false
	}
	scopes, resourceTypes, issues := validateOAuthAppManifestContent(&manifest, s.validWebhookURL)
	if len(issues) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid OAuth application manifest", "issues": issues})
		return manifest, nil, nil, false
	}
	return manifest, scopes, resourceTypes, true
}

// POST /api/oauth-applications/manifest/validate
func (s *server) validateOAuthAppManifest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(r.Body); err != nil {
		writeError(w, http.StatusBadRequest, "manifest body is too large or unreadable")
		return
	}
	manifest, issues := decodeOAuthAppManifest(buffer.Bytes())
	if issues == nil {
		var scopes, resourceTypes []string
		scopes, resourceTypes, issues = validateOAuthAppManifestContent(&manifest, s.validWebhookURL)
		if len(issues) == 0 {
			preview := domain.OAuthApplication{}
			applyOAuthAppManifest(manifest, &preview, scopes, resourceTypes)
			preview.UpdatedAt = time.Time{}
			writeJSON(w, http.StatusOK, publicapi.OAuthAppManifestValidation{Valid: true, Issues: []oauthAppManifestIssue{}, Application: &preview})
			return
		}
	}
	writeJSON(w, http.StatusOK, publicapi.OAuthAppManifestValidation{Valid: false, Issues: issues})
}

// POST /api/oauth-applications/manifest creates an application from a manifest.
func (s *server) createOAuthApplicationFromManifest(w http.ResponseWriter, r *http.Request) {
	manifest, scopes, resourceTypes, ok := s.readOAuthAppManifest(w, r)
	if !ok {
		return
	}
	clientID, idErr := randomSecret("flow_client_")
	secret, secretErr := randomSecret("flow_secret_")
	if idErr != nil || secretErr != nil {
		writeError(w, http.StatusInternalServerError, "Could not generate client credentials")
		return
	}
	actor := requestActor(s, r)
	var created domain.OAuthApplication
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "oauth_application.created", map[string]string{"source": "manifest"}, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.OAuthApplication{ID: idgen.New("oauth_app_"), ClientID: clientID, ClientSecret: secret, CreatorID: actor.ID, CredentialsAuthorizedBy: actor.ID, CreatedAt: now}
		applyOAuthAppManifest(manifest, &created, scopes, resourceTypes)
		if err := checkGitHubUsernameAvailable(data, created.ID, created.GitHubUsername); err != nil {
			return "", err
		}
		data.OAuthApplications = append([]domain.OAuthApplication{created}, data.OAuthApplications...)
		return created.ID, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	s.auditAPIKeyCredential(r, "client_secret_issued", created)
	s.respondManifestApplication(w, r, http.StatusCreated, created, manifestWebhookSecret(manifest))
}

// manifestWebhookSecret is the signing secret the settings form generated, if any.
func manifestWebhookSecret(m oauthAppManifest) string {
	if m.Webhook == nil {
		return ""
	}
	return m.Webhook.Secret
}

// respondManifestApplication finishes a manifest save the way the webhook
// settings endpoint does: an enabled webhook gets a signing secret (the one
// the settings form generated and showed, when given), and every
// workspace's managed application webhooks are reconciled (created, updated
// or removed) against the new configuration.
func (s *server) respondManifestApplication(w http.ResponseWriter, r *http.Request, status int, app domain.OAuthApplication, formSecret string) {
	response := manifestApplicationResponse{OAuthApplication: app}
	if app.WebhookEnabled {
		if _, signing, _ := s.store.WebhookSigningSecret(r.Context(), store.ApplicationWebhookSecretRef(app.ClientID)); !signing {
			secret, err := s.storeApplicationWebhookSecret(r, app, formSecret)
			if err != nil {
				respondMutation(w, err, status, nil)
				return
			}
			response.WebhookSecret = secret
			response.WebhookSecretPrefix = secret[:min(len(secret), 17)]
			s.auditAPIKeyCredential(r, "webhook_secret_issued", app)
		}
	}
	go s.reconcileAllApplicationWebhooks(context.Background())
	if response.ClientSecret != "" || response.WebhookSecret != "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	writeJSON(w, status, response)
}

// PUT /api/oauth-applications/{id}/manifest replaces an application's
// configuration. Client ID and secret are kept.
func (s *server) updateOAuthApplicationFromManifest(w http.ResponseWriter, r *http.Request) {
	manifest, scopes, resourceTypes, ok := s.readOAuthAppManifest(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var updated domain.OAuthApplication
	changed := false
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_application.updated", id, map[string]string{"source": "manifest"}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == id })
		if index < 0 {
			return errNotFound
		}
		// Manifest lists are applied in place, so compare against a snapshot.
		original := data.OAuthApplications[index]
		before := jsonSnapshot(original)
		applyOAuthAppManifest(manifest, &data.OAuthApplications[index], scopes, resourceTypes)
		if err := checkGitHubUsernameAvailable(data, id, data.OAuthApplications[index].GitHubUsername); err != nil {
			return err
		}
		data.OAuthApplications[index].CredentialsAuthorizedBy = requestActor(s, r).ID
		updated = data.OAuthApplications[index]
		unstamped := updated
		unstamped.UpdatedAt = original.UpdatedAt
		changed = !unchangedJSON(before, unstamped)
		updated.ClientSecret = ""
		if !changed {
			return store.ErrNoMutation
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	if changed {
		s.syncApplicationIdentity(r.Context(), updated.ClientID)
	}
	s.respondManifestApplication(w, r, http.StatusOK, updated, manifestWebhookSecret(manifest))
}

// GET /api/oauth-applications/{id}/manifest
func (s *server) exportOAuthApplicationManifest(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == r.PathValue("id") })
	if index < 0 {
		writeError(w, http.StatusNotFound, "Application not found")
		return
	}
	writeJSON(w, http.StatusOK, exportOAuthAppManifest(r, data.OAuthApplications[index]))
}

// GET /.well-known/oauth-app-manifest.schema.json
func (s *server) oauthAppManifestSchema(w http.ResponseWriter, r *http.Request) {
	urlProperty := func(description string, httpsOnly bool) map[string]any {
		pattern := "^https?://"
		if httpsOnly {
			pattern = "^https://"
		}
		return map[string]any{"type": "string", "format": "uri", "pattern": pattern, "description": description}
	}
	unique := func(items map[string]any, min, max int, description string) map[string]any {
		value := map[string]any{"type": "array", "items": items, "uniqueItems": true, "minItems": min, "description": description}
		if max > 0 {
			value["maxItems"] = max
		}
		return value
	}
	scopes := append(slices.Clone(domain.ApplicationScopes()), "issues:create", "comments:create", "admin", "timeSchedule:write")
	resourceTypes := []string{"OAuthAuthorization", "ProjectMilestone"}
	for _, item := range webhookResourceTypes {
		resourceTypes = append(resourceTypes, item.ID, item.Type)
	}
	slices.Sort(resourceTypes)
	resourceTypes = slices.Compact(resourceTypes)
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  oauthAppManifestSchemaURL(r),
		"title":                "Flow OAuth application manifest",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"schemaVersion", "developer", "oauth"},
		"properties": map[string]any{
			"$schema":       map[string]any{"type": "string", "description": "Schema URL for editor and CI validation"},
			"schemaVersion": map[string]any{"const": oauthAppManifestSchemaVersion},
			"distribution":  map[string]any{"enum": []string{"private", "public"}, "default": "private", "description": "private: only the creating workspace; public: installable by other workspaces"},
			"display": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
				"description": map[string]any{"type": "string", "maxLength": 1000},
				"iconUrl":     urlProperty("Application icon (at least 256x256)", true),
			}},
			"developer": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"name"}, "properties": map[string]any{
				"name": map[string]any{"type": "string", "minLength": 2, "maxLength": 80},
			}},
			"oauth": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"client_name", "redirect_uris"}, "properties": map[string]any{
				"client_name":   map[string]any{"type": "string", "minLength": 2, "maxLength": 80, "not": map[string]any{"pattern": "(?i)(\\bflow\\b|https?://)"}},
				"client_uri":    urlProperty("Homepage or documentation URL (required for public applications)", false),
				"redirect_uris": unique(map[string]any{"type": "string", "format": "uri"}, 1, 32, "HTTPS redirect URIs (HTTP only on loopback hosts)"),
				"grant_types":   map[string]any{"type": "array", "uniqueItems": true, "minItems": 1, "items": map[string]any{"enum": []string{"authorization_code", "client_credentials"}}, "contains": map[string]any{"const": "authorization_code"}, "default": []string{"authorization_code"}},
				"scopes":        map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"enum": scopes}, "description": "Flow extension: scopes the application may request (read is always included)"},
			}},
			"webhook": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"url", "resourceTypes"}, "properties": map[string]any{
				"enabled":       map[string]any{"type": "boolean", "default": true},
				"url":           urlProperty("Public HTTPS endpoint (no loopback or private-network hosts)", true),
				"resourceTypes": unique(map[string]any{"enum": resourceTypes}, 1, 0, "Resource types delivered to the application webhook"),
				"secret":        map[string]any{"type": "string", "pattern": webhookSecretPattern.String(), "writeOnly": true, "description": "Flow extension: signing secret to use when the webhook is first enabled (the settings form generates it); ignored once the application has a secret, never exported"},
			}},
			"github": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"username"}, "description": "Flow extension: for actor=app, GitHub activity of this account is attributed to the app user", "properties": map[string]any{
				"username": map[string]any{"type": "string", "pattern": githubUsernamePattern.String(), "maxLength": 44, "description": "GitHub username (use the [bot] suffix for GitHub Apps, like my-app[bot])"},
			}},
			"agent": map[string]any{"type": "object", "additionalProperties": false, "description": "Flow extension: agent capabilities", "properties": map[string]any{
				"assignable":  map[string]any{"type": "boolean", "description": "Adds app:assignable (issues can be delegated to the app)"},
				"mentionable": map[string]any{"type": "boolean", "description": "Adds app:mentionable (the app can be @-mentioned)"},
			}},
		},
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, schema)
}
