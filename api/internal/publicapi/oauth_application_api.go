package publicapi

import "flow/api/internal/domain"

// OAuth application management (docs/oauth.md, docs/api.md). Workspace
// administrators, or API keys with the admin scope owned by one.

// OAuthApplicationInput creates (name required) or updates an OAuth
// application. Omitted fields are left unchanged.
type OAuthApplicationInput struct {
	// Name is shown on the consent screen and in Settings › Applications.
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	// LogoURL is an absolute HTTP(S) URL or an uploaded application icon.
	LogoURL *string `json:"logoUrl,omitempty"`
	// RedirectURIs are the allowed OAuth callbacks (HTTPS, or loopback HTTP).
	RedirectURIs *[]string `json:"redirectUris,omitempty"`
	// Scopes are the scopes the application may request.
	Scopes *[]string `json:"scopes,omitempty"`
	// SupportsClientCredentials enables the client_credentials grant.
	SupportsClientCredentials *bool `json:"supportsClientCredentials,omitempty"`
	// PublicEnabled lets other workspaces install the application.
	PublicEnabled  *bool   `json:"publicEnabled,omitempty"`
	DeveloperName  *string `json:"developerName,omitempty"`
	DeveloperURL   *string `json:"developerUrl,omitempty"`
	GitHubUsername *string `json:"githubUsername,omitempty"`
}

// OAuthApplicationInstalls lists the workspace's grants of an application:
// members' active user authorizations and the actor=app installation.
type OAuthApplicationInstalls struct {
	Authorizations []domain.OAuthAuthorization      `json:"authorizations"`
	Installations  []domain.ApplicationInstallation `json:"installations"`
}

// OAuthApplicationClientSecret carries the application's client secret
// (reading it, or the new one after a rotation). Sent with Cache-Control:
// no-store.
type OAuthApplicationClientSecret struct {
	ClientSecret string `json:"clientSecret"`
}

// ApplicationWebhookSecret carries the signing secret of an application
// webhook. Sent with Cache-Control: no-store.
type ApplicationWebhookSecret struct {
	Secret string `json:"secret"`
}

// DeveloperTokenInput creates a developer token for an application.
type DeveloperTokenInput struct {
	// Actor is app (default: a token of the application's app user, or its
	// active one) or user (a token of the caller through the application).
	Actor string `json:"actor,omitempty" enum:"app,user"`
	// Scopes are the token's scopes (for user tokens, read and write as far
	// as the application allows when omitted).
	Scopes []string `json:"scopes,omitempty"`
}

// ApplicationToken is an access token issued to an application (developer
// tokens and the client_credentials grant).
type ApplicationToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	// ExpiresIn is the lifetime in seconds.
	ExpiresIn int `json:"expires_in"`
	// Scope is the space-separated scopes.
	Scope string `json:"scope"`
	// Actor is app (the application's app user) or user.
	Actor         string `json:"actor" enum:"app,user"`
	SubjectUserID string `json:"subject_user_id"`
	AppUserID     string `json:"app_user_id,omitempty"`
	// Existing is true when the app user's active developer token was
	// returned instead of a new one.
	Existing bool `json:"existing,omitempty"`
}

// OAuthAppManifest describes an OAuth application declaratively (see
// /.well-known/oauth-app-manifest.schema.json).
type OAuthAppManifest struct {
	Schema        string `json:"$schema,omitempty"`
	SchemaVersion string `json:"schemaVersion"`
	// Distribution is private (default) or public.
	Distribution string                   `json:"distribution,omitempty" enum:"private,public"`
	Display      *OAuthAppManifestDisplay `json:"display,omitempty"`
	Developer    *OAuthAppManifestDev     `json:"developer,omitempty"`
	OAuth        *OAuthAppManifestOAuth   `json:"oauth,omitempty"`
	Webhook      *OAuthAppManifestWebhook `json:"webhook,omitempty"`
	Agent        *OAuthAppManifestAgent   `json:"agent,omitempty"`
	GitHub       *OAuthAppManifestGitHub  `json:"github,omitempty"`
}

// OAuthAppManifestGitHub is the "GitHub username": for actor=app, commits
// and comments by this GitHub account belong to the app user.
type OAuthAppManifestGitHub struct {
	Username string `json:"username"`
}

type OAuthAppManifestDisplay struct {
	Description string `json:"description,omitempty"`
	IconURL     string `json:"iconUrl,omitempty"`
}

type OAuthAppManifestDev struct {
	Name string `json:"name"`
}

type OAuthAppManifestOAuth struct {
	ClientName   string   `json:"client_name"`
	ClientURI    string   `json:"client_uri"`
	RedirectURIs []string `json:"redirect_uris"`
	GrantTypes   []string `json:"grant_types,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

type OAuthAppManifestWebhook struct {
	Enabled       *bool    `json:"enabled,omitempty"`
	URL           string   `json:"url"`
	ResourceTypes []string `json:"resourceTypes"`
	// Secret is the signing secret shown in the settings form before the
	// webhook is first saved. Write-only: it is used only while the
	// application has no signing secret yet and is never exported.
	Secret string `json:"secret,omitempty"`
}

// OAuthAppManifestAgent is shorthand for the app:assignable and
// app:mentionable scopes of an agent application.
type OAuthAppManifestAgent struct {
	Assignable  bool `json:"assignable,omitempty"`
	Mentionable bool `json:"mentionable,omitempty"`
}

// OAuthAppManifestIssue is one validation problem, at a JSON path.
type OAuthAppManifestIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// OAuthAppManifestValidation is the result of validating a manifest.
type OAuthAppManifestValidation struct {
	Valid  bool                    `json:"valid"`
	Issues []OAuthAppManifestIssue `json:"issues"`
	// Application previews the application the manifest describes (when valid).
	Application *domain.OAuthApplication `json:"application,omitempty"`
}

// ManifestApplication is an application saved from a manifest, with the
// webhook signing secret the first time its webhook is enabled.
type ManifestApplication struct {
	domain.OAuthApplication
	WebhookSecret string `json:"webhookSecret,omitempty"`
}
