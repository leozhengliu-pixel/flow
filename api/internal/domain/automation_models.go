package domain

import "time"

// TriageResponsibility owns an incoming issue. A round-robin responsibility
// advances Cursor atomically when a matching routing rule is applied.
type TriageResponsibility struct {
	ID        string    `json:"id"`
	TeamID    string    `json:"teamId"`
	Name      string    `json:"name"`
	Mode      string    `json:"mode"`
	UserIDs   []string  `json:"userIds"`
	Cursor    int       `json:"cursor"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type TriageRoutingRule struct {
	ID               string            `json:"id"`
	TeamID           string            `json:"teamId"`
	Name             string            `json:"name"`
	Position         int               `json:"position"`
	Enabled          bool              `json:"enabled"`
	Conditions       map[string]string `json:"conditions"`
	ResponsibilityID string            `json:"responsibilityId"`
	Priority         *int              `json:"priority,omitempty"`
	LabelIDs         []string          `json:"labelIds"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
}

type TriageAssignment struct {
	ID               string    `json:"id"`
	IssueID          string    `json:"issueId"`
	RuleID           string    `json:"ruleId,omitempty"`
	ResponsibilityID string    `json:"responsibilityId"`
	AssigneeID       string    `json:"assigneeId,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

type WorkflowAction struct {
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}

type WorkflowDefinition struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description,omitempty"`
	TeamID         string            `json:"teamId,omitempty"`
	Trigger        string            `json:"trigger"`
	Schedule       string            `json:"schedule,omitempty"`
	Conditions     map[string]string `json:"conditions"`
	Actions        []WorkflowAction  `json:"actions"`
	Enabled        bool              `json:"enabled"`
	MaxAttempts    int               `json:"maxAttempts"`
	NextRunAt      *time.Time        `json:"nextRunAt,omitempty"`
	LastRunAt      *time.Time        `json:"lastRunAt,omitempty"`
	LastRunStatus  string            `json:"lastRunStatus,omitempty"`
	ConsecutiveErr int               `json:"consecutiveErrors"`
	CreatorID      string            `json:"creatorId"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

type WorkflowRun struct {
	ID           string            `json:"id"`
	WorkflowID   string            `json:"workflowId"`
	Trigger      string            `json:"trigger"`
	ResourceType string            `json:"resourceType,omitempty"`
	ResourceID   string            `json:"resourceId,omitempty"`
	Status       string            `json:"status"`
	Attempt      int               `json:"attempt"`
	Error        string            `json:"error,omitempty"`
	Output       map[string]string `json:"output"`
	ScheduledAt  time.Time         `json:"scheduledAt"`
	StartedAt    time.Time         `json:"startedAt"`
	CompletedAt  *time.Time        `json:"completedAt,omitempty"`
	NextRetryAt  *time.Time        `json:"nextRetryAt,omitempty"`
}

type EmailIntakeAlias struct {
	Address   string    `json:"address"`
	TokenHash string    `json:"tokenHash,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type EmailIntakeAddress struct {
	ID                string             `json:"id"`
	TeamID            string             `json:"teamId"`
	LocalPart         string             `json:"localPart"`
	Domain            string             `json:"domain"`
	Address           string             `json:"address"`
	InboundTokenHash  string             `json:"inboundTokenHash,omitempty"`
	VerificationToken string             `json:"verificationToken,omitempty"`
	VerificationState string             `json:"verificationState"`
	VerifiedAt        *time.Time         `json:"verifiedAt,omitempty"`
	Aliases           []EmailIntakeAlias `json:"aliases"`
	Enabled           bool               `json:"enabled"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
}

type EmailIntakeMessage struct {
	ID          string     `json:"id"`
	AddressID   string     `json:"addressId"`
	MessageID   string     `json:"messageId"`
	From        string     `json:"from"`
	Subject     string     `json:"subject"`
	IssueID     string     `json:"issueId,omitempty"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	ReceivedAt  time.Time  `json:"receivedAt"`
	ProcessedAt *time.Time `json:"processedAt,omitempty"`
}

type PushSubscription struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	Endpoint  string    `json:"endpoint"`
	P256DH    string    `json:"p256dh"`
	Auth      string    `json:"auth"`
	UserAgent string    `json:"userAgent,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
