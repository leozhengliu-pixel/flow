package domain

import "time"

// PulseSnapshot is the state of a project or initiative captured when an
// update is posted. Consecutive snapshots produce the update's PulseDiff.
type PulseSnapshot struct {
	CapturedAt time.Time `json:"capturedAt"`
	// Project fields.
	StatusID      string                   `json:"statusId,omitempty"`
	Status        string                   `json:"status,omitempty"`
	Priority      *int                     `json:"priority,omitempty"`
	PriorityLabel string                   `json:"priorityLabel,omitempty"`
	LeadID        string                   `json:"leadId,omitempty"`
	Lead          string                   `json:"lead,omitempty"`
	StartDate     string                   `json:"startDate,omitempty"`
	TargetDate    string                   `json:"targetDate,omitempty"`
	Progress      *float64                 `json:"progress,omitempty"`
	Milestones    []PulseMilestoneSnapshot `json:"milestones,omitempty"`
	// Initiative fields (status, owner and target date reuse the fields above).
	Projects []PulseRef `json:"projects,omitempty"`
	// Initiatives are an initiative's sub-initiatives. Nil means the snapshot
	// predates sub-initiative capture (no diff); an empty list is captured.
	Initiatives *[]PulseRef `json:"initiatives,omitempty"`
}

type PulseMilestoneSnapshot struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Progress   float64 `json:"progress"`
	Total      int64   `json:"total"`
	Completed  int64   `json:"completed"`
	TargetDate string  `json:"targetDate,omitempty"`
}

type PulseRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PulseValueChange is one property change between two snapshots. Empty
// strings mean "none" (no lead, no date).
type PulseValueChange struct {
	From   string `json:"from"`
	To     string `json:"to"`
	FromID string `json:"fromId,omitempty"`
	ToID   string `json:"toId,omitempty"`
}

type PulseProgressChange struct {
	Date time.Time `json:"date"`
	From float64   `json:"from"`
	To   float64   `json:"to"`
}

type PulseMilestoneChange struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// From is the previous progress; absent for a milestone added since the
	// previous update (Added).
	From *float64 `json:"from,omitempty"`
	To   float64  `json:"to"`
	// Completed is set when the milestone reached 100%.
	Completed  bool   `json:"completed,omitempty"`
	Added      bool   `json:"added,omitempty"`
	TargetDate string `json:"targetDate,omitempty"`
}

type PulseProjectsChange struct {
	Added   []PulseRef `json:"added"`
	Removed []PulseRef `json:"removed"`
}

// PulseDiff lists what changed since the previous update of the same project
// or initiative. For initiatives Lead holds the owner change.
type PulseDiff struct {
	Status        *PulseValueChange      `json:"status,omitempty"`
	Priority      *PulseValueChange      `json:"priority,omitempty"`
	Lead          *PulseValueChange      `json:"lead,omitempty"`
	StartDate     *PulseValueChange      `json:"startDate,omitempty"`
	TargetDate    *PulseValueChange      `json:"targetDate,omitempty"`
	ProgressSince *PulseProgressChange   `json:"progressSince,omitempty"`
	Milestones    []PulseMilestoneChange `json:"milestones,omitempty"`
	Projects      *PulseProjectsChange   `json:"projects,omitempty"`
	// Initiatives lists sub-initiatives added to or removed from an initiative.
	Initiatives *PulseProjectsChange `json:"initiatives,omitempty"`
}

// Empty reports whether nothing changed.
func (diff PulseDiff) Empty() bool {
	return diff.Status == nil && diff.Priority == nil && diff.Lead == nil && diff.StartDate == nil && diff.TargetDate == nil && diff.ProgressSince == nil && len(diff.Milestones) == 0 && diff.Projects == nil && diff.Initiatives == nil
}

// NotificationPayload carries structured data for notifications that are not
// tied to one issue. Pulse summaries list the updates they cover.
type NotificationPayload struct {
	Schedule    string           `json:"schedule,omitempty"`
	WindowStart *time.Time       `json:"windowStart,omitempty"`
	WindowEnd   *time.Time       `json:"windowEnd,omitempty"`
	UpdateIDs   []string         `json:"updateIds,omitempty"`
	Updates     []PulseUpdateRef `json:"updates,omitempty"`
	// Total counts every update in the window; Updates is capped.
	Total int `json:"total,omitempty"`
	// Document notifications (documentMention, documentNewComment, ...) carry
	// the document's current slug for links, a short excerpt (the comment
	// text, or the mention's surrounding text) and, for reactions, the emoji.
	DocumentSlugID string `json:"documentSlugId,omitempty"`
	Excerpt        string `json:"excerpt,omitempty"`
	QuotedText     string `json:"quotedText,omitempty"`
	Emoji          string `json:"emoji,omitempty"`
}

type PulseUpdateRef struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	SourceID string    `json:"sourceId"`
	Source   string    `json:"source,omitempty"`
	At       time.Time `json:"createdAt"`
}

type PulseSummaryItem struct {
	UpdateID   string `json:"updateId"`
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	Health     string `json:"health"`
	Summary    string `json:"summary"`
}

type PulseSummarySection struct {
	Kind  string             `json:"kind"`
	Items []PulseSummaryItem `json:"items"`
}

// PulseSummary is the readable summary of a Pulse summary notification.
type PulseSummary struct {
	Title       string                `json:"title"`
	GeneratedAt time.Time             `json:"generatedAt"`
	Sections    []PulseSummarySection `json:"sections"`
	Text        string                `json:"text"`
	AI          bool                  `json:"ai"`
	// AudioKey names the cached speech rendering in the object store.
	AudioKey string `json:"audioKey,omitempty"`
	// RetryAt is set on an extractive fallback produced because the AI
	// provider failed: the next open after it tries the provider again.
	// Attempts counts those failures (the backoff grows with it).
	RetryAt  *time.Time `json:"retryAt,omitempty"`
	Attempts int        `json:"attempts,omitempty"`
}

type PulseSummaryReport struct {
	UserID    string    `json:"userId"`
	UpdateID  string    `json:"updateId,omitempty"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"createdAt"`
}
