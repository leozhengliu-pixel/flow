package domain

import (
	"encoding/json"
	"time"
)

// ChatIntegrationRecord contains public application routing information only.
// Credentials are stored separately using the encrypted connector secret store.
type ChatIntegrationRecord struct {
	WorkspaceKey string `json:"workspaceKey"`
	Provider     string `json:"provider"`
	ConnectionID string `json:"connectionId"`
	AppID        string `json:"appId"`
	TenantKey    string `json:"tenantKey"`
	BotOpenID    string `json:"botOpenId"`
	Name         string `json:"name"`
	// Connected means the enterprise tenant authorization is current: the
	// application's credentials last obtained a tenant token and read the
	// bot's info (the tenant administrator approved and published the app).
	Connected bool      `json:"connected"`
	UpdatedAt time.Time `json:"updatedAt"`
	// TenantAuthorization records the automatic tenant authorization.
	// Records stored while tenant authorization was a sign-in step carry
	// older fields, which are ignored; their next successful check rewrites
	// it.
	TenantAuthorization *ChatTenantAuthorization `json:"tenantAuthorization,omitempty"`
	// TenantCheck is the latest automatic tenant check.
	TenantCheck *ChatTenantCheck `json:"tenantCheck,omitempty"`
}

// ChatTenantAuthorization: the tenant was authorized automatically when the
// application's credentials obtained a tenant token and read the bot's info.
type ChatTenantAuthorization struct {
	// TenantKey is empty until the platform names the tenant (the bot's
	// internal groups or a member's linked account).
	TenantKey    string    `json:"tenantKey,omitempty"`
	AuthorizedAt time.Time `json:"authorizedAt"`
	// ConfiguredBy is the workspace administrator who configured or last
	// tested the connection, for the record.
	ConfiguredBy string `json:"configuredBy,omitempty"`
}

// ChatTenantCheck is the outcome of the latest automatic tenant check.
type ChatTenantCheck struct {
	CheckedAt time.Time `json:"checkedAt"`
	// ErrorCode is the stable code of a failed check, such as
	// chat_tenant_not_approved.
	ErrorCode string `json:"errorCode,omitempty"`
}

const (
	ChatBindingRoleWorkspaceAdmin = "workspace_admin"
	ChatBindingRoleTeamOwner      = "team_owner"
)

// ChatBindingAuthorization is set by the server: who authorized sharing the
// team with the group, in which capacity, and whether the team is private.
type ChatBindingAuthorization struct {
	UserID       string    `json:"userId"`
	Role         string    `json:"role" enum:"workspace_admin,team_owner"`
	AuthorizedAt time.Time `json:"authorizedAt"`
	// PrivateTeam is true when the team was private at authorization. A
	// private team binding delivers only while its authorizer can still
	// administer the team.
	PrivateTeam bool `json:"privateTeam"`
}

type ChatBinding struct {
	ID           string    `json:"id"`
	ConnectionID string    `json:"connectionId"`
	ChatID       string    `json:"chatId"`
	ChatName     string    `json:"chatName"`
	TeamID       string    `json:"teamId"`
	ProjectID    string    `json:"projectId,omitempty"`
	TemplateID   string    `json:"templateId,omitempty"`
	Events       []string  `json:"events"`
	Enabled      bool      `json:"enabled"`
	AllowCreate  bool      `json:"allowCreate"`
	AllowUpdate  bool      `json:"allowUpdate"`
	CreatedAt    time.Time `json:"createdAt"`
	// Authorization is recorded by the server whenever the binding is
	// created or its scope changes.
	Authorization *ChatBindingAuthorization `json:"authorization,omitempty"`
	// RevokedAt marks a revoked binding. It stays listed as a record and
	// never delivers or accepts group actions again.
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
	RevokedBy string     `json:"revokedBy,omitempty"`
}

type ChatIdentity struct {
	UserID    string `json:"userId"`
	OpenID    string `json:"openId"`
	TenantKey string `json:"tenantKey"`
	Name      string `json:"name,omitempty"`
	// PersonalDelivery is "unavailable" after the platform refused a
	// personal notification because the app is not available to this
	// account (its availability range); a later successful one clears it.
	PersonalDelivery string     `json:"personalDelivery,omitempty" enum:"unavailable"`
	AuthProvider     string     `json:"-"`
	AuthIssuer       string     `json:"-"`
	MFAVerifiedAt    *time.Time `json:"-"`
	// AuthenticatedAt is when the member consented from a signed-in session.
	// The workspace session duration is measured from it.
	AuthenticatedAt time.Time `json:"-"`
}

type ChatAction struct {
	ID           string          `json:"id"`
	WorkspaceKey string          `json:"workspaceKey"`
	ConnectionID string          `json:"connectionId"`
	UserID       string          `json:"userId"`
	Payload      json.RawMessage `json:"payload"`
	ExpiresAt    time.Time       `json:"expiresAt"`
}

type ChatMessage struct {
	ID              string    `json:"id"`
	WorkspaceKey    string    `json:"workspaceKey"`
	ConnectionID    string    `json:"connectionId"`
	ChatID          string    `json:"chatId"`
	MessageID       string    `json:"messageId"`
	IssueID         string    `json:"issueId"`
	SourceMessageID string    `json:"sourceMessageId,omitempty"`
	Version         string    `json:"version,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt"`
	CreatedAt       time.Time `json:"createdAt"`
}

// ChatJob is a durable inbox or outbox entry. LeaseUntil is also the claim
// token: an expired worker cannot finish a job claimed by another instance.
type ChatJob struct {
	ID           string          `json:"id"`
	WorkspaceKey string          `json:"workspaceKey"`
	ConnectionID string          `json:"connectionId"`
	Kind         string          `json:"kind"`
	DedupKey     string          `json:"dedupKey"`
	Payload      json.RawMessage `json:"payload"`
	Status       string          `json:"status"`
	Attempts     int             `json:"attempts"`
	RunAt        time.Time       `json:"runAt"`
	LeaseUntil   time.Time       `json:"leaseUntil"`
	LastError    string          `json:"lastError,omitempty"`
}
