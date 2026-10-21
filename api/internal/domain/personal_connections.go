package domain

import "time"

// Personal connections: a member's own link to a service that is not a code
// host (Settings › Account › Connected accounts). Google Calendar keeps sealed
// OAuth tokens and the member's next out-of-office window; Notion holds no
// token, only the member's choice to let Notion preview their Flow links.
const (
	PersonalConnectionGoogleCalendar = "google-calendar"
	PersonalConnectionNotion         = "notion"

	PersonalConnectionActive    = "active"
	PersonalConnectionReconnect = "reconnect"
)

// PersonalConnection is one member's connection to a service. Tokens are never
// part of it: they are sealed in the connector secret store.
type PersonalConnection struct {
	ID       string `json:"id"`
	UserID   string `json:"-"`
	Provider string `json:"provider"`
	// Account is the provider's name for the member (the Google account's email).
	Account string   `json:"account,omitempty"`
	Scopes  []string `json:"scopes"`
	Status  string   `json:"status"` // active | reconnect
	// StatusReason explains a "reconnect" status.
	StatusReason   string     `json:"statusReason,omitempty"`
	TokenExpiresAt *time.Time `json:"-"`
	// AwayFrom/AwayUntil: the member's next out-of-office window read from the
	// calendar (Google Calendar only).
	AwayFrom  *time.Time `json:"awayFrom,omitempty"`
	AwayUntil *time.Time `json:"awayUntil,omitempty"`
	LinkedAt  time.Time  `json:"linkedAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}
