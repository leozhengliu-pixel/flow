package domain

import "time"

type ProjectRelation struct {
	ID                        string    `json:"id"`
	ProjectID                 string    `json:"projectId"`
	RelatedProjectID          string    `json:"relatedProjectId"`
	Type                      string    `json:"type"`
	ProjectMilestoneID        string    `json:"projectMilestoneId,omitempty"`
	RelatedProjectMilestoneID string    `json:"relatedProjectMilestoneId,omitempty"`
	CreatedAt                 time.Time `json:"createdAt"`
	UpdatedAt                 time.Time `json:"updatedAt"`
}
type InitiativeRelation struct {
	ID                  string    `json:"id"`
	InitiativeID        string    `json:"initiativeId"`
	RelatedInitiativeID string    `json:"relatedInitiativeId"`
	Type                string    `json:"type"`
	SortOrder           float64   `json:"sortOrder"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}
type DocumentContentDraft struct {
	ID           string         `json:"id"`
	DocumentID   string         `json:"documentId"`
	UserID       string         `json:"userId"`
	Content      string         `json:"content"`
	ContentState string         `json:"contentState,omitempty"`
	ContentData  map[string]any `json:"contentData,omitempty"`
	Version      int64          `json:"version"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}
type CustomerStatus struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Color      string     `json:"color"`
	Position   float64    `json:"position"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}
type CustomerTier struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Color      string     `json:"color"`
	Position   float64    `json:"position"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}
type ReleaseNote struct {
	ID          string         `json:"id"`
	ReleaseID   string         `json:"releaseId"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	BodyData    map[string]any `json:"bodyData,omitempty"`
	Creator     User           `json:"creator"`
	PublishedAt *time.Time     `json:"publishedAt,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}
type ReleaseHistory struct {
	ID        string         `json:"id"`
	ReleaseID string         `json:"releaseId"`
	Actor     User           `json:"actor"`
	Action    string         `json:"action"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"createdAt"`
}
type TeamResourceSection struct {
	ID        string    `json:"id"`
	TeamID    string    `json:"teamId"`
	Name      string    `json:"name"`
	Position  float64   `json:"position"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type TeamPinnedResource struct {
	ID           string    `json:"id"`
	TeamID       string    `json:"teamId"`
	SectionID    string    `json:"sectionId,omitempty"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	URL          string    `json:"url,omitempty"`
	Title        string    `json:"title"`
	Position     float64   `json:"position"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
type AgentActivity struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionId"`
	IssueID   string         `json:"issueId,omitempty"`
	Type      string         `json:"type"`
	Status    string         `json:"status"`
	Body      string         `json:"body,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
}
type AIConversation struct {
	ID        string         `json:"id"`
	UserID    string         `json:"userId"`
	Title     string         `json:"title"`
	Status    string         `json:"status"`
	Context   map[string]any `json:"context,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
}
type AIPromptProgress struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	Phase          string    `json:"phase"`
	Progress       int       `json:"progress"`
	Message        string    `json:"message,omitempty"`
	Error          string    `json:"error,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}
type UsageAlert struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Threshold  int64          `json:"threshold"`
	Current    int64          `json:"current"`
	Status     string         `json:"status"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
	ResolvedAt *time.Time     `json:"resolvedAt,omitempty"`
	ArchivedAt *time.Time     `json:"archivedAt,omitempty"`
}
type PaidSubscription struct {
	ID                 string    `json:"id"`
	Plan               string    `json:"plan"`
	Status             string    `json:"status"`
	Seats              int       `json:"seats"`
	CurrentPeriodStart time.Time `json:"currentPeriodStart"`
	CurrentPeriodEnd   time.Time `json:"currentPeriodEnd"`
	CancelAtPeriodEnd  bool      `json:"cancelAtPeriodEnd"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}
