package domain

import (
	"encoding/json"
	"time"
)

// Webhook delivery statuses.
const (
	WebhookDeliveryPending    = "pending"
	WebhookDeliveryDelivering = "delivering"
	WebhookDeliveryRetrying   = "retrying"
	WebhookDeliverySucceeded  = "succeeded"
	WebhookDeliveryFailed     = "failed"
	WebhookDeliveryCanceled   = "canceled"
)

// Webhook delivery kinds.
const (
	WebhookDeliveryKindEvent     = "event"
	WebhookDeliveryKindTest      = "test"
	WebhookDeliveryKindLifecycle = "lifecycle"
)

// WebhookDeliveryBodyLimit caps the stored response body of an attempt.
const WebhookDeliveryBodyLimit = 2048

// WebhookPayloadVersion is the current delivery envelope. Webhooks created
// before it existed keep version 1 (resource names such as "issues" and raw
// actions such as "created") until an admin switches them.
const WebhookPayloadVersion = 2

// WebhookDelivery is one queued or completed delivery of an event to a
// webhook endpoint. Payload is the request body without webhookTimestamp,
// which is added (and signed) when each attempt is sent.
type WebhookDelivery struct {
	ID             string                   `json:"id"`
	WorkspaceKey   string                   `json:"-"`
	WebhookID      string                   `json:"webhookId"`
	SecretRef      string                   `json:"-"`
	Kind           string                   `json:"kind" enum:"event,test,lifecycle"`
	EventID        string                   `json:"eventId"`
	EventType      string                   `json:"eventType"`
	Action         string                   `json:"action"`
	URL            string                   `json:"url"`
	Payload        json.RawMessage          `json:"payload,omitempty"`
	Status         string                   `json:"status" enum:"pending,delivering,retrying,succeeded,failed,canceled"`
	Attempt        int                      `json:"attempt"`
	MaxAttempts    int                      `json:"maxAttempts"`
	NextAttemptAt  *time.Time               `json:"nextAttemptAt,omitempty"`
	ResponseStatus int                      `json:"responseStatus,omitempty"`
	ResponseBody   string                   `json:"responseBody,omitempty"`
	Error          string                   `json:"error,omitempty"`
	DurationMS     int64                    `json:"durationMs"`
	Attempts       []WebhookDeliveryAttempt `json:"attempts"`
	RedeliveryOf   string                   `json:"redeliveryOf,omitempty"`
	CreatedAt      time.Time                `json:"createdAt"`
	UpdatedAt      time.Time                `json:"updatedAt"`
	CompletedAt    *time.Time               `json:"completedAt,omitempty"`
}

// WebhookDeliveryAttempt summarises one HTTP attempt of a delivery.
type WebhookDeliveryAttempt struct {
	Attempt        int       `json:"attempt"`
	At             time.Time `json:"at"`
	ResponseStatus int       `json:"responseStatus,omitempty"`
	DurationMS     int64     `json:"durationMs"`
	Error          string    `json:"error,omitempty"`
	Succeeded      bool      `json:"succeeded"`
}

// AgentExternalURL is a link an agent attaches to its session (a pull
// request, a run in its own dashboard).
type AgentExternalURL struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// AgentPlanStep is one step of an agent session's plan. Status is pending,
// inProgress, completed or canceled.
type AgentPlanStep struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}
