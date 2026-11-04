package domain

import "time"

func (s UserSettings) CommentActionsEnabled() bool {
	return s.SuggestedActionsOnComments == nil || *s.SuggestedActionsOnComments
}

// CommentAction is an inert proposal. Resolution and its mutation commit together.
type CommentAction struct {
	ID             string                 `json:"id"`
	IssueID        string                 `json:"issueId"`
	CommentID      string                 `json:"commentId"`
	CommentVersion int64                  `json:"commentVersion"`
	TargetUserID   string                 `json:"targetUserId"`
	Status         string                 `json:"status" enum:"pending,accepted,dismissed"`
	Action         string                 `json:"action" enum:"status,assignee,priority,labels,subIssue,link"`
	Arguments      CommentActionArguments `json:"arguments"`
	CreatedAt      time.Time              `json:"createdAt"`
	ResolvedAt     *time.Time             `json:"resolvedAt,omitempty"`
	ResolvedBy     string                 `json:"resolvedBy,omitempty"`
	ResultIssueID  string                 `json:"resultIssueId,omitempty"`
}

type CommentActionArguments struct {
	StateID     *string   `json:"stateId,omitempty"`
	AssigneeID  *string   `json:"assigneeId,omitempty"`
	Priority    *int      `json:"priority,omitempty"`
	LabelIDs    *[]string `json:"labelIds,omitempty"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	URL         string    `json:"url,omitempty"`
}

type CommentActionResolveInput struct {
	Status string `json:"status" enum:"accepted,dismissed"`
}

type CommentActionResult struct {
	Suggestion CommentAction `json:"suggestion"`
	Issue      Issue         `json:"issue"`
}
