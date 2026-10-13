package publicapi

import "flow/api/internal/domain"

// Webhook management (docs/webhooks.md#api-reference).

// Webhook is a webhook as the API returns it: the stored webhook with its
// effective payload version and signing state. The signing secret is never
// part of it (only WebhookWithSecret on creation and rotation).
type Webhook struct {
	domain.Webhook
	// PayloadVersion is the delivery envelope: 2 is the current one, 1 the
	// legacy envelope of webhooks created before signing.
	PayloadVersion int `json:"payloadVersion" enum:"1,2"`
	// SigningStatus is active, rotationRequired (created before signing: rotate
	// the secret to sign deliveries) or revoked (deliveries go out unsigned).
	SigningStatus string `json:"signingStatus" enum:"active,rotationRequired,revoked"`
}

// WebhookWithSecret is returned when a webhook is created or its secret
// rotated: the only time the signing secret is shown.
type WebhookWithSecret struct {
	Webhook
	// Secret signs every delivery (Flow-Signature). Store it now.
	Secret string `json:"secret"`
}

// WebhookCreateInput creates a webhook.
type WebhookCreateInput struct {
	// Name is shown in the webhook list; the URL when omitted.
	Name *string `json:"name,omitempty"`
	// Label is an alias of name.
	Label *string `json:"label,omitempty"`
	// URL is a public HTTPS endpoint that answers 2xx within 5 seconds.
	URL string `json:"url"`
	// ResourceTypes are payload type names (Issue, Comment) or Flow ids (issues, comments).
	ResourceTypes []string `json:"resourceTypes"`
	// TeamID restricts the webhook to one team.
	TeamID *string `json:"teamId,omitempty"`
	// TeamIDs restricts the webhook to several teams. Omit both for every team you can access.
	TeamIDs *[]string `json:"teamIds,omitempty"`
	// TeamRestriction is all or selected; it follows teamIds when omitted.
	TeamRestriction *string `json:"teamRestriction,omitempty" enum:"all,selected"`
	// AllPublicTeams selects every team.
	AllPublicTeams *bool `json:"allPublicTeams,omitempty"`
	Enabled        *bool `json:"enabled,omitempty"`
	// PayloadVersion selects the delivery envelope; new webhooks use 2.
	PayloadVersion *int `json:"payloadVersion,omitempty" enum:"1,2"`
}

// WebhookUpdateInput changes a webhook. Application-managed webhooks only
// accept enabled.
type WebhookUpdateInput struct {
	Name            *string   `json:"name,omitempty"`
	Label           *string   `json:"label,omitempty"`
	URL             *string   `json:"url,omitempty"`
	ResourceTypes   *[]string `json:"resourceTypes,omitempty"`
	TeamID          *string   `json:"teamId,omitempty"`
	TeamIDs         *[]string `json:"teamIds,omitempty"`
	TeamRestriction *string   `json:"teamRestriction,omitempty" enum:"all,selected"`
	AllPublicTeams  *bool     `json:"allPublicTeams,omitempty"`
	// Enabled false pauses the webhook and cancels its queued retries; true
	// resumes it and resets the failure window.
	Enabled *bool `json:"enabled,omitempty"`
	// PayloadVersion 2 switches a legacy webhook to the current envelope.
	PayloadVersion *int `json:"payloadVersion,omitempty" enum:"1,2"`
}

// ApplicationWebhookInput configures the webhook of an OAuth application.
type ApplicationWebhookInput struct {
	Enabled *bool `json:"enabled,omitempty"`
	// URL is a public HTTPS endpoint.
	URL *string `json:"url,omitempty"`
	// ResourceTypes may include the application-only types agent_session_events, app_user_notifications, permission_changes and oauth_app.
	ResourceTypes *[]string `json:"resourceTypes,omitempty"`
}

// ApplicationWebhookSettings is the webhook configuration of an OAuth application.
type ApplicationWebhookSettings struct {
	ApplicationID  string   `json:"applicationId"`
	ClientID       string   `json:"clientId"`
	Enabled        bool     `json:"enabled"`
	URL            string   `json:"url"`
	ResourceTypes  []string `json:"resourceTypes"`
	SecretPrefix   string   `json:"secretPrefix,omitempty"`
	SigningEnabled bool     `json:"signingEnabled"`
	// Secret is the signing secret, present only when it was just created or rotated.
	Secret string `json:"secret,omitempty"`
	// Installations counts the workspaces with a managed webhook.
	Installations int `json:"installations"`
}
