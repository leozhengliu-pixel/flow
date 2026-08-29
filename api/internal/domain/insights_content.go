package domain

import (
	"encoding/json"
	"time"
)

// Dashboard is a persisted collection of analytical widgets. Visibility is
// evaluated for every read rather than relying on the bootstrap projection so
// shared links and private dashboards cannot leak through cached state.
type Dashboard struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	OwnerID       string            `json:"ownerId"`
	Visibility    string            `json:"visibility"`
	TeamIDs       []string          `json:"teamIds"`
	Widgets       []DashboardWidget `json:"widgets"`
	SubscriberIDs []string          `json:"subscriberIds"`
	ShareToken    string            `json:"shareToken,omitempty"`
	SharedAt      *time.Time        `json:"sharedAt,omitempty"`
	CreatedAt     time.Time         `json:"createdAt"`
	UpdatedAt     time.Time         `json:"updatedAt"`
}

type DashboardWidget struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Title    string          `json:"title"`
	Position int             `json:"position"`
	Width    int             `json:"width"`
	Config   json.RawMessage `json:"config"`
}

type DashboardWidgetResult struct {
	Widget DashboardWidget `json:"widget"`
	Value  any             `json:"value"`
}

type Post struct {
	ID            string     `json:"id"`
	Title         string     `json:"title,omitempty"`
	Body          string     `json:"body"`
	CreatorID     string     `json:"creatorId"`
	TeamIDs       []string   `json:"teamIds"`
	ProjectID     string     `json:"projectId,omitempty"`
	InitiativeID  string     `json:"initiativeId,omitempty"`
	SubscriberIDs []string   `json:"subscriberIds"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`
}

type FeedItem struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	ActorID      string            `json:"actorId"`
	ResourceType string            `json:"resourceType"`
	ResourceID   string            `json:"resourceId"`
	TeamIDs      []string          `json:"teamIds"`
	Title        string            `json:"title"`
	Body         string            `json:"body,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
}

type Meeting struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Description   string    `json:"description,omitempty"`
	OrganizerID   string    `json:"organizerId"`
	AttendeeIDs   []string  `json:"attendeeIds"`
	TeamIDs       []string  `json:"teamIds"`
	ProjectIDs    []string  `json:"projectIds"`
	IssueIDs      []string  `json:"issueIds"`
	StartsAt      time.Time `json:"startsAt"`
	DurationMins  int       `json:"durationMinutes"`
	URL           string    `json:"url,omitempty"`
	Notes         string    `json:"notes,omitempty"`
	Transcript    string    `json:"transcript,omitempty"`
	SubscriberIDs []string  `json:"subscriberIds"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type SemanticSearchFacet struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type FilterSuggestion struct {
	Field string `json:"field"`
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}
