package domain

import "time"

// ChatBindingRequests configures request intake for a group binding: a
// member turns a message of the group into a request in the bound team's
// triage by mentioning the bot, adding the configured reaction, or using the
// message shortcut.
type ChatBindingRequests struct {
	Enabled bool `json:"enabled"`
	// Emoji is the reaction (the platform's emoji type, such as "OK") that
	// turns a message into a request; empty leaves reactions alone.
	Emoji string `json:"emoji,omitempty"`
	// TemplateID is the issue template requests are created with.
	TemplateID string `json:"templateId,omitempty"`
	// Priority (0 to 4) of new requests; absent uses the template's or the
	// team's default.
	Priority *int `json:"priority,omitempty"`
}

// RequestsEnabled reports whether the binding takes requests.
func (binding ChatBinding) RequestsEnabled() bool {
	return binding.Requests != nil && binding.Requests.Enabled
}

// Chat request sources and statuses.
const (
	ChatRequestSourceMention  = "mention"
	ChatRequestSourceReaction = "reaction"
	ChatRequestSourceShortcut = "shortcut"

	// ChatRequestPending: the message is reserved by its requester and the
	// issue is being created.
	ChatRequestPending = "pending"
	// ChatRequestCreated: the request's issue exists.
	ChatRequestCreated = "created"
)

// ChatIntakeRequest records the request made from one chat message: the
// message can carry one request only, whoever asks and however often. The
// record also routes the request's status changes and its outcome back to
// the message's thread.
type ChatIntakeRequest struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connectionId"`
	ChatID       string `json:"chatId"`
	// MessageID is the message the request was made from.
	MessageID string `json:"messageId"`
	BindingID string `json:"bindingId"`
	// IssueID is the request's issue in the team's triage, and AskID its
	// request record.
	IssueID         string `json:"issueId"`
	AskID           string `json:"askId,omitempty"`
	RequesterID     string `json:"requesterId"`
	RequesterOpenID string `json:"requesterOpenId"`
	Source          string `json:"source" enum:"mention,reaction,shortcut"`
	Status          string `json:"status" enum:"pending,created"`
	// Outcome is the closed state type (completed or canceled) the thread
	// was last told about; empty while the request is open.
	Outcome   string    `json:"outcome,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
