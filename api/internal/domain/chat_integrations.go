package domain

import (
	"encoding/json"
	"time"
)

// ChatIntegrationRecord contains public application routing information only.
// Credentials are stored separately using the encrypted connector secret store.
type ChatIntegrationRecord struct {
	WorkspaceKey string    `json:"workspaceKey"`
	Provider     string    `json:"provider"`
	ConnectionID string    `json:"connectionId"`
	AppID        string    `json:"appId"`
	TenantKey    string    `json:"tenantKey"`
	BotOpenID    string    `json:"botOpenId"`
	Name         string    `json:"name"`
	Connected    bool      `json:"connected"`
	UpdatedAt    time.Time `json:"updatedAt"`
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
}

type ChatIdentity struct {
	UserID        string     `json:"userId"`
	OpenID        string     `json:"openId"`
	TenantKey     string     `json:"tenantKey"`
	Name          string     `json:"name,omitempty"`
	AuthProvider  string     `json:"-"`
	AuthIssuer    string     `json:"-"`
	MFAVerifiedAt *time.Time `json:"-"`
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
