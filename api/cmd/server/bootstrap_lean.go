package main

import (
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// leanBootstrapRequested selects the list-record snapshot: issues are id
// references without bodies, and comments and activities are left to their
// per-resource endpoints. Callers without the header keep the full snapshot.
func leanBootstrapRequested(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Flow-Projection")), "lean")
}

// leanIssue is an issue as a list row: scalar fields plus ids that resolve
// against the team, state, label, user, project, cycle and milestone
// collections of the same response.
type leanIssue struct {
	ID                   string                 `json:"id"`
	Version              int64                  `json:"version"`
	Identifier           string                 `json:"identifier"`
	Number               int                    `json:"number"`
	Title                string                 `json:"title"`
	Icon                 string                 `json:"icon,omitempty"`
	HasContent           bool                   `json:"hasContent"`
	Priority             int                    `json:"priority"`
	SortOrder            float64                `json:"sortOrder"`
	Estimate             *float64               `json:"estimate,omitempty"`
	DueDate              *string                `json:"dueDate,omitempty"`
	SLABreachesAt        *time.Time             `json:"slaBreachesAt,omitempty"`
	SLAType              string                 `json:"slaType,omitempty"`
	CreatedAt            time.Time              `json:"createdAt"`
	UpdatedAt            time.Time              `json:"updatedAt"`
	CompletedAt          *time.Time             `json:"completedAt,omitempty"`
	StartedAt            *time.Time             `json:"startedAt,omitempty"`
	TriagedAt            *time.Time             `json:"triagedAt,omitempty"`
	StatusChangedAt      *time.Time             `json:"statusChangedAt,omitempty"`
	AutoClosedAt         *time.Time             `json:"autoClosedAt,omitempty"`
	CanceledAt           *time.Time             `json:"canceledAt,omitempty"`
	ArchivedAt           *time.Time             `json:"archivedAt,omitempty"`
	SharedAt             *time.Time             `json:"sharedAt,omitempty"`
	SnoozedUntil         *time.Time             `json:"snoozedUntil,omitempty"`
	TeamID               string                 `json:"teamId"`
	StateID              string                 `json:"stateId"`
	AssigneeID           *string                `json:"assigneeId,omitempty"`
	DelegateID           *string                `json:"delegateId,omitempty"`
	CreatorID            string                 `json:"creatorId"`
	LabelIDs             []string               `json:"labelIds"`
	ProjectID            *string                `json:"projectId,omitempty"`
	ProjectMilestoneID   *string                `json:"projectMilestoneId,omitempty"`
	CycleID              *string                `json:"cycleId,omitempty"`
	AddedToCycle         string                 `json:"addedToCycle,omitempty"`
	ParentID             *string                `json:"parentId,omitempty"`
	SubIssueIDs          []string               `json:"subIssueIds"`
	Relations            []domain.IssueRelation `json:"relations"`
	SubscriberIDs        []string               `json:"subscriberIds"`
	ReactionCounts       map[string]int         `json:"reactionCounts,omitempty"`
	AttachmentCount      int                    `json:"attachmentCount,omitempty"`
	AgentSessionID       string                 `json:"agentSessionId,omitempty"`
	AgentSessionState    string                 `json:"agentSessionState,omitempty"`
	SuggestedLabelIDs    []string               `json:"suggestedLabelIds,omitempty"`
	SuggestedAssigneeIDs []string               `json:"suggestedAssigneeIds,omitempty"`
	SuggestedProjectIDs  []string               `json:"suggestedProjectIds,omitempty"`
	SuggestedTeamIDs     []string               `json:"suggestedTeamIds,omitempty"`
	SuggestedDuplicateID []string               `json:"suggestedDuplicateIds,omitempty"`
	SuggestedRelatedIDs  []string               `json:"suggestedRelatedIds,omitempty"`
	ExternalSource       string                 `json:"externalSource,omitempty"`
	AutoClosed           bool                   `json:"autoClosed,omitempty"`
	TemplateID           string                 `json:"templateId,omitempty"`
	Recurrence           string                 `json:"recurrence,omitempty"`
	NextOccurrenceAt     *time.Time             `json:"nextOccurrenceAt,omitempty"`
	RecurrenceSeriesID   string                 `json:"recurrenceSeriesId,omitempty"`
	RecurrenceOccurrence string                 `json:"recurrenceOccurrence,omitempty"`
}

func leanIssueRecord(issue domain.Issue) leanIssue {
	row := leanIssue{
		ID: issue.ID, Version: issue.Version, Identifier: issue.Identifier, Number: issue.Number, Title: issue.Title, Icon: issue.Icon,
		HasContent: issue.HasContent, Priority: issue.Priority, SortOrder: issue.SortOrder, Estimate: issue.Estimate, DueDate: issue.DueDate,
		SLABreachesAt: issue.SLABreachesAt, SLAType: issue.SLAType, CreatedAt: issue.CreatedAt, UpdatedAt: issue.UpdatedAt,
		CompletedAt: issue.CompletedAt, StartedAt: issue.StartedAt, TriagedAt: issue.TriagedAt, StatusChangedAt: issue.StatusChangedAt,
		AutoClosedAt: issue.AutoClosedAt, CanceledAt: issue.CanceledAt, ArchivedAt: issue.ArchivedAt, SharedAt: issue.SharedAt, SnoozedUntil: issue.SnoozedUntil,
		TeamID: issue.Team.ID, StateID: issue.State.ID, CreatorID: issue.Creator.ID, LabelIDs: make([]string, 0, len(issue.Labels)),
		ProjectMilestoneID: issue.ProjectMilestoneID, CycleID: issue.CycleID, AddedToCycle: issue.AddedToCycle, ParentID: issue.ParentID,
		SubIssueIDs: nonNilStrings(issue.SubIssueIDs), Relations: issue.Relations, SubscriberIDs: nonNilStrings(issue.SubscriberIDs),
		ReactionCounts: issue.ReactionCounts, AttachmentCount: len(issue.Attachments), AgentSessionID: issue.AgentSessionID, AgentSessionState: issue.AgentSessionState,
		SuggestedLabelIDs: issue.SuggestedLabelIDs, SuggestedAssigneeIDs: issue.SuggestedAssigneeIDs, SuggestedProjectIDs: issue.SuggestedProjectIDs,
		SuggestedTeamIDs: issue.SuggestedTeamIDs, SuggestedDuplicateID: issue.SuggestedDuplicateIDs, SuggestedRelatedIDs: issue.SuggestedRelatedIDs,
		ExternalSource: issue.ExternalSource, AutoClosed: issue.AutoClosed, TemplateID: issue.TemplateID, Recurrence: issue.Recurrence,
		NextOccurrenceAt: issue.NextOccurrenceAt, RecurrenceSeriesID: issue.RecurrenceSeriesID, RecurrenceOccurrence: issue.RecurrenceOccurrence,
	}
	if row.Relations == nil {
		row.Relations = []domain.IssueRelation{}
	}
	for _, label := range issue.Labels {
		row.LabelIDs = append(row.LabelIDs, label.ID)
	}
	if issue.Assignee != nil {
		row.AssigneeID = &issue.Assignee.ID
	}
	if issue.Delegate != nil {
		row.DelegateID = &issue.Delegate.ID
	}
	if issue.Project != nil {
		row.ProjectID = &issue.Project.ID
	}
	return row
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
