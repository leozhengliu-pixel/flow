package publicapi

// DocumentCreateInput creates a document. Content is Markdown; contentData
// and contentState carry the collaborative editor's document.
type DocumentCreateInput struct {
	// TemplateID starts from a document template.
	TemplateID *string `json:"templateId,omitempty"`
	Title      *string `json:"title,omitempty"`
	Icon       *string `json:"icon,omitempty"`
	Color      *string `json:"color,omitempty"`
	// Content is the document body as Markdown.
	Content *string `json:"content,omitempty"`
	// ContentState is the base64 Yjs state of the collaborative editor.
	ContentState *string `json:"contentState,omitempty"`
	// ContentData is the editor's JSON document (ProseMirror).
	ContentData map[string]any `json:"contentData,omitempty"`
	ProjectIDs  *[]string      `json:"projectIds,omitempty"`
	TeamIDs     *[]string      `json:"teamIds,omitempty"`
	// IssueID attaches the document to an issue.
	IssueID       *string   `json:"issueId,omitempty"`
	SubscriberIDs *[]string `json:"subscriberIds,omitempty"`
	Favorite      *bool     `json:"favorite,omitempty"`
	// CreateAsUser names the external person an actor=app token acts for
	// ("<name> via <App>", at most 100 characters). Rejected for user tokens.
	CreateAsUser *string `json:"createAsUser,omitempty"`
	// DisplayIconURL is that person's avatar; requires createAsUser.
	DisplayIconURL *string `json:"displayIconUrl,omitempty"`
}

// DocumentUpdateInput changes a document; omitted fields stay as they are.
type DocumentUpdateInput struct {
	Title       *string        `json:"title,omitempty"`
	Icon        *string        `json:"icon,omitempty"`
	Color       *string        `json:"color,omitempty"`
	Content     *string        `json:"content,omitempty"`
	ContentData map[string]any `json:"contentData,omitempty"`
	// ContentState replaces the collaborative base state; see expectedContentVersion.
	ContentState  *string   `json:"contentState,omitempty"`
	ProjectIDs    *[]string `json:"projectIds,omitempty"`
	TeamIDs       *[]string `json:"teamIds,omitempty"`
	IssueID       *string   `json:"issueId,omitempty"`
	SubscriberIDs *[]string `json:"subscriberIds,omitempty"`
	Favorite      *bool     `json:"favorite,omitempty"`
	// Archived archives or restores the document.
	Archived *bool `json:"archived,omitempty"`
	// ExpectedVersion rejects the write with 409 (and the current document)
	// when the document changed since the caller read it.
	ExpectedVersion *int64 `json:"expectedVersion,omitempty"`
	// ExpectedContentVersion guards replacing contentState; a stale value
	// keeps the content fields but skips contentState.
	ExpectedContentVersion *int64 `json:"expectedContentVersion,omitempty"`
	// DocumentUpdateIDs are the editor update ids the snapshot includes; the server prunes them after a successful write.
	DocumentUpdateIDs []string `json:"documentUpdateIds,omitempty"`
}

// ProjectMilestoneReorderInput orders the milestones of a project.
type ProjectMilestoneReorderInput struct {
	// IDs lists every milestone of the project once, in the new order.
	IDs []string `json:"ids"`
}

// IssueFilter is the filter tree of the issue query API: and, or, not, or a
// condition on one field. Fields are id, identifier, team, status (state id
// or type), statusType, project, assignee, creator, cycle, parent, priority,
// createdAt, updatedAt, title, sortOrder, labels, subscribers, myActivity,
// sharedWith, customerId, agentSessionState and the indexed attributes
// projectMilestoneId, estimate, dueDate, completedAt, canceledAt, startedAt,
// templateId, delegateId and others. Operators (case-insensitive) are is,
// in, isNot, notIn, includesAll, excludesAll, isEmpty, isNotEmpty, before,
// after, gt, gte, lt, lte, contains and doesNotContain.
type IssueFilter struct {
	And []IssueFilter `json:"and,omitempty"`
	Or  []IssueFilter `json:"or,omitempty"`
	Not *IssueFilter  `json:"not,omitempty"`
	// Field names the property to compare.
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator,omitempty"`
	// Value is a single comparison value; Values a list. Both are matched as text.
	Value  any   `json:"value,omitempty"`
	Values []any `json:"values,omitempty"`
}
