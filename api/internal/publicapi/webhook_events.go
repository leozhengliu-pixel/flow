package publicapi

import (
	"encoding/json"
	"time"

	"flow/api/internal/domain"
)

// Payloads of webhook deliveries (docs/webhooks.md). The server assembles
// them from domain events, so these types are the published description of
// the JSON a receiver gets, checked against the documented examples and the
// server's real deliveries by tests. Fields tagged partial:"true" hold the
// REST model of the entity as it was after the change; only its id is
// guaranteed because payloads of older webhooks and nested objects carry a
// subset.

// WebhookActor is who made the change.
type WebhookActor struct {
	ID    string `json:"id"`
	Type  string `json:"type" enum:"user,application"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	// URL is the actor's profile page in Flow.
	URL string `json:"url,omitempty"`
}

// WebhookEnvelope holds the fields every delivery carries.
type WebhookEnvelope struct {
	// CreatedAt is when the change happened (ISO 8601).
	CreatedAt time.Time `json:"createdAt"`
	// OrganizationID is the workspace id.
	OrganizationID string `json:"organizationId"`
	// WebhookID is the webhook this delivery belongs to.
	WebhookID string `json:"webhookId"`
	// WebhookTimestamp is the Unix time in milliseconds when the delivery was
	// sent. It is signed: use it for the replay check.
	WebhookTimestamp int64 `json:"webhookTimestamp"`
	// URL is the entity's page in Flow, when it has one.
	URL string `json:"url,omitempty"`
	// EventID is Flow's id of the change. Every webhook receiving the same
	// change gets the same value; use it to deduplicate.
	EventID string `json:"eventId,omitempty"`
	// EventType is Flow's detailed event, for example issue.updated or
	// comment.resolved.
	EventType string `json:"eventType,omitempty"`
	// Actor is who made the change; null for system changes.
	Actor *WebhookActor `json:"actor,omitempty"`
}

// DataChange is the part of a data change event that says what happened.
type DataChange struct {
	Action string `json:"action" enum:"create,update,remove"`
	// UpdatedFrom holds the previous values of the changed properties, on update.
	UpdatedFrom map[string]any `json:"updatedFrom,omitempty"`
}

// IssueWebhookEvent is sent when an issue is created, updated (including archive) or deleted.
type IssueWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string       `json:"type" const:"Issue"`
	Data domain.Issue `json:"data" partial:"true"`
}

// AttachmentWebhookEvent is sent when a file or link attachment is added to or removed from an issue.
type AttachmentWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string            `json:"type" const:"Attachment"`
	Data domain.Attachment `json:"data" partial:"true"`
}

// CommentWebhookData is a comment with the resource it belongs to.
type CommentWebhookData struct {
	domain.Comment
	// IssueID is the commented issue, or the id of the parent resource (project, initiative, document) for other comments.
	IssueID string `json:"issueId,omitempty"`
	UserID  string `json:"userId,omitempty"`
}

// CommentWebhookEvent is sent when a comment on an issue, project, project update, initiative, initiative update or document is created, edited, resolved or deleted.
type CommentWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string             `json:"type" const:"Comment"`
	Data CommentWebhookData `json:"data" partial:"true"`
}

// IssueLabelWebhookEvent is sent when a label is created, updated, merged or deleted.
type IssueLabelWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string            `json:"type" const:"IssueLabel"`
	Data domain.IssueLabel `json:"data" partial:"true"`
}

// ReactionWebhookData is an emoji reaction. Exactly the parent that was
// reacted to is set.
type ReactionWebhookData struct {
	ID                 string `json:"id"`
	Emoji              string `json:"emoji"`
	UserID             string `json:"userId"`
	IssueID            string `json:"issueId,omitempty"`
	CommentID          string `json:"commentId,omitempty"`
	ProjectID          string `json:"projectId,omitempty"`
	ProjectUpdateID    string `json:"projectUpdateId,omitempty"`
	InitiativeID       string `json:"initiativeId,omitempty"`
	InitiativeUpdateID string `json:"initiativeUpdateId,omitempty"`
	// ParentType and ParentID name other parents (documents, for example).
	ParentType string `json:"parentType,omitempty"`
	ParentID   string `json:"parentId,omitempty"`
}

// ReactionWebhookEvent is sent when an emoji reaction is added (create) or removed (remove).
type ReactionWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string              `json:"type" const:"Reaction"`
	Data ReactionWebhookData `json:"data"`
}

// IssueRelationWebhookEvent is sent when an issue relation is added or removed.
type IssueRelationWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string               `json:"type" const:"IssueRelation"`
	Data domain.IssueRelation `json:"data" partial:"true"`
}

// ProjectWebhookEvent is sent when a project is created, updated or deleted.
type ProjectWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string         `json:"type" const:"Project"`
	Data domain.Project `json:"data" partial:"true"`
}

// ProjectUpdateWebhookEvent is sent when a project update is posted, edited or deleted (also when an attachment is added to or removed from it). Its data is the update (with projectId); for remove, the update before it was deleted.
type ProjectUpdateWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string               `json:"type" const:"ProjectUpdate"`
	Data domain.ProjectUpdate `json:"data" partial:"true"`
}

// ProjectMilestoneWebhookData is a milestone with its position in the project.
type ProjectMilestoneWebhookData struct {
	domain.ProjectMilestone
	// SortOrder is the milestone's position in its project, starting at 0.
	SortOrder int `json:"sortOrder"`
}

// ProjectMilestoneWebhookEvent is sent when a project milestone is created, updated or deleted, and, as an update with updatedFrom {sortOrder}, for every milestone whose position changed when the milestones of a project are reordered (eventType project.milestone_reordered). Its data is the milestone (with projectId and sortOrder); for remove, the milestone before it was deleted.
type ProjectMilestoneWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string                      `json:"type" const:"ProjectMilestone"`
	Data ProjectMilestoneWebhookData `json:"data" partial:"true"`
}

// DocumentWebhookEvent is sent when a document is created, updated or deleted.
type DocumentWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string          `json:"type" const:"Document"`
	Data domain.Document `json:"data" partial:"true"`
}

// InitiativeWebhookEvent is sent when an initiative is created, updated or deleted.
type InitiativeWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string            `json:"type" const:"Initiative"`
	Data domain.Initiative `json:"data" partial:"true"`
}

// InitiativeUpdateWebhookEvent is sent when an initiative update is posted, edited or deleted (also when an attachment is added to or removed from it). Its data is the update (with initiativeId); for remove, the update before it was deleted.
type InitiativeUpdateWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string                  `json:"type" const:"InitiativeUpdate"`
	Data domain.InitiativeUpdate `json:"data" partial:"true"`
}

// CycleWebhookEvent is sent when a cycle is created, started, completed, updated or deleted.
type CycleWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string       `json:"type" const:"Cycle"`
	Data domain.Cycle `json:"data" partial:"true"`
}

// CustomerWebhookEvent is sent when a customer is created, updated, merged or deleted.
type CustomerWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string          `json:"type" const:"Customer"`
	Data domain.Customer `json:"data" partial:"true"`
}

// CustomerNeedWebhookEvent is sent when a customer request (Flow's name for a customer need) is created, updated or deleted.
type CustomerNeedWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string                 `json:"type" const:"CustomerNeed"`
	Data domain.CustomerRequest `json:"data" partial:"true"`
}

// UserWebhookEvent is sent when a workspace member changes.
type UserWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string      `json:"type" const:"User"`
	Data domain.User `json:"data" partial:"true"`
}

// ReleaseWebhookEvent is sent when a release is created, updated, completed or deleted.
type ReleaseWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string         `json:"type" const:"Release"`
	Data domain.Release `json:"data" partial:"true"`
}

// ReleaseNoteWebhookEvent is sent when a release note is created, updated or deleted.
type ReleaseNoteWebhookEvent struct {
	WebhookEnvelope
	DataChange
	Type string             `json:"type" const:"ReleaseNote"`
	Data domain.ReleaseNote `json:"data" partial:"true"`
}

// IssueSLAIssue identifies the issue an SLA event is about.
type IssueSLAIssue struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier,omitempty"`
	Title      string `json:"title,omitempty"`
}

// IssueSLAData is the SLA of an issue.
type IssueSLAData struct {
	ID      string `json:"id"`
	IssueID string `json:"issueId"`
	RuleID  string `json:"ruleId,omitempty"`
	DueAt   string `json:"dueAt,omitempty"`
	Status  string `json:"status,omitempty"`
}

// IssueSLAWebhookEvent is sent when an SLA is set on an issue (set), is close to breaching (highRisk, once, when less than a quarter of the window of at most 24 hours remains) or breached (breached).
type IssueSLAWebhookEvent struct {
	WebhookEnvelope
	Type      string        `json:"type" const:"IssueSLA"`
	Action    string        `json:"action" enum:"set,highRisk,breached"`
	IssueData IssueSLAIssue `json:"issueData"`
	Data      IssueSLAData  `json:"data"`
}

// WebhookTestData is the body of a test event.
type WebhookTestData struct {
	WebhookID string `json:"webhookId"`
	Message   string `json:"message"`
}

// WebhookTestEvent is sent by "Send test event" and POST /api/webhooks/{id}/test.
type WebhookTestEvent struct {
	WebhookEnvelope
	Type   string          `json:"type" const:"WebhookTest"`
	Action string          `json:"action" const:"test"`
	Data   WebhookTestData `json:"data"`
}

// OAuthAppWebhookEvent is sent to application webhooks when the application was uninstalled or its last authorization revoked.
type OAuthAppWebhookEvent struct {
	WebhookEnvelope
	Type          string `json:"type" const:"OAuthApp"`
	Action        string `json:"action" const:"revoked"`
	OAuthClientID string `json:"oauthClientId"`
}

// PermissionChangeWebhookEvent is sent to application webhooks when the teams the application can access change.
type PermissionChangeWebhookEvent struct {
	WebhookEnvelope
	Type           string   `json:"type" const:"PermissionChange"`
	Action         string   `json:"action" const:"teamAccessChanged"`
	OAuthClientID  string   `json:"oauthClientId"`
	AppUserID      string   `json:"appUserId"`
	AddedTeamIDs   []string `json:"addedTeamIds"`
	RemovedTeamIDs []string `json:"removedTeamIds"`
}

// NotificationIssue is the issue of an inbox notification.
type NotificationIssue struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier,omitempty"`
	Title      string `json:"title,omitempty"`
	TeamID     string `json:"teamId,omitempty"`
	URL        string `json:"url,omitempty"`
}

// NotificationComment is the comment of an inbox notification.
type NotificationComment struct {
	ID     string `json:"id"`
	Body   string `json:"body,omitempty"`
	UserID string `json:"userId,omitempty"`
}

// AppUserNotificationData is one inbox notification of the application's user.
type AppUserNotificationData struct {
	ID        string               `json:"id"`
	Type      string               `json:"type"`
	IssueID   string               `json:"issueId,omitempty"`
	CommentID string               `json:"commentId,omitempty"`
	ActorID   string               `json:"actorId,omitempty"`
	Issue     *NotificationIssue   `json:"issue,omitempty"`
	Comment   *NotificationComment `json:"comment,omitempty"`
}

// AppUserNotificationWebhookEvent forwards the inbox of the application's user (assignment covers both assignee and delegate).
type AppUserNotificationWebhookEvent struct {
	WebhookEnvelope
	Type          string                  `json:"type" const:"AppUserNotification"`
	Action        string                  `json:"action" enum:"issueMention,issueCommentMention,issueEmojiReaction,issueCommentReaction,issueAssignedToYou,issueUnassignedFromYou,issueNewComment,issueStatusChanged"`
	OAuthClientID string                  `json:"oauthClientId"`
	AppUserID     string                  `json:"appUserId"`
	Notification  AppUserNotificationData `json:"notification"`
}

// AgentSessionIssue identifies the issue of an agent session.
type AgentSessionIssue struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier,omitempty"`
	Title      string `json:"title,omitempty"`
}

// AgentSessionCreator is the person who started the session.
type AgentSessionCreator struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// AgentSessionData is the session an agent is asked to work on.
type AgentSessionData struct {
	ID        string               `json:"id"`
	Status    string               `json:"status"`
	IssueID   string               `json:"issueId,omitempty"`
	CommentID string               `json:"commentId,omitempty"`
	Issue     *AgentSessionIssue   `json:"issue,omitempty"`
	Comment   *NotificationComment `json:"comment,omitempty"`
	Creator   *AgentSessionCreator `json:"creator,omitempty"`
}

// AgentSessionPreviousComment is an earlier comment of the triggering thread.
type AgentSessionPreviousComment struct {
	ID        string `json:"id"`
	Body      string `json:"body,omitempty"`
	UserID    string `json:"userId,omitempty"`
	UserName  string `json:"userName,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// AgentGuidance is one rule of the agent guidance that applies to a session.
type AgentGuidance struct {
	Body string `json:"body"`
	// Origin is workspace or team.
	Origin   string `json:"origin" enum:"workspace,team"`
	TeamID   string `json:"teamId,omitempty"`
	TeamKey  string `json:"teamKey,omitempty"`
	TeamName string `json:"teamName,omitempty"`
}

// AgentActivityPrompt is the person's prompt, on prompted events.
type AgentActivityPrompt struct {
	Type string `json:"type"`
	Body string `json:"body,omitempty"`
}

// AgentSessionWebhookEvent tells an agent about work: created for a new session (the agent was delegated an issue or mentioned) and prompted when a person replies. Answer within 5 seconds and process the work asynchronously.
type AgentSessionWebhookEvent struct {
	WebhookEnvelope
	Type             string                        `json:"type" const:"AgentSessionEvent"`
	Action           string                        `json:"action" enum:"created,prompted"`
	OAuthClientID    string                        `json:"oauthClientId"`
	AppUserID        string                        `json:"appUserId"`
	AgentSession     AgentSessionData              `json:"agentSession"`
	Issue            *AgentSessionIssue            `json:"issue,omitempty"`
	PreviousComments []AgentSessionPreviousComment `json:"previousComments,omitempty"`
	Guidance         []AgentGuidance               `json:"guidance,omitempty"`
	AgentActivity    *AgentActivityPrompt          `json:"agentActivity,omitempty"`
	// PromptContext is a ready-to-use prompt: the issue, the triggering thread
	// and the guidance rules, XML-escaped.
	PromptContext string `json:"promptContext,omitempty"`
}

// LegacyWebhookEvent is the version 1 body that webhooks created before
// signing keep until an administrator switches them
// (PATCH /api/webhooks/{id} with payloadVersion 2).
type LegacyWebhookEvent struct {
	// ID is the event id.
	ID string `json:"id"`
	// Type is the Flow resource name, for example issues.
	Type string `json:"type"`
	// Action is the raw event verb, for example created or status_changed.
	Action string `json:"action"`
	// Data is the mutation input.
	Data           json.RawMessage `json:"data"`
	PreviousValues json.RawMessage `json:"previousValues,omitempty"`
	CreatedAt      string          `json:"createdAt"`
	Actor          *WebhookActor   `json:"actor,omitempty"`
	URL            string          `json:"url,omitempty"`
	UpdatedFrom    json.RawMessage `json:"updatedFrom,omitempty"`
	OrganizationID string          `json:"organizationId"`
	WebhookID      string          `json:"webhookId"`
	EventType      string          `json:"eventType"`
	// WebhookTimestamp is the signed Unix time in milliseconds of the attempt.
	WebhookTimestamp int64 `json:"webhookTimestamp"`
}

// WebhookEvent describes one payload type for the OpenAPI webhooks section.
type WebhookEvent struct {
	// Type is the payload type (the Flow-Event header).
	Type string
	// Resource is the resourceTypes value that subscribes to it.
	Resource string
	// ApplicationOnly marks events only application webhooks receive.
	ApplicationOnly bool
	Payload         any
}

// WebhookEvents lists every payload type of docs/webhooks.md.
var WebhookEvents = []WebhookEvent{
	{Type: "Issue", Resource: "issues", Payload: IssueWebhookEvent{}},
	{Type: "Attachment", Resource: "attachments", Payload: AttachmentWebhookEvent{}},
	{Type: "Comment", Resource: "comments", Payload: CommentWebhookEvent{}},
	{Type: "IssueLabel", Resource: "labels", Payload: IssueLabelWebhookEvent{}},
	{Type: "Reaction", Resource: "reactions", Payload: ReactionWebhookEvent{}},
	{Type: "IssueRelation", Resource: "relations", Payload: IssueRelationWebhookEvent{}},
	{Type: "Project", Resource: "projects", Payload: ProjectWebhookEvent{}},
	{Type: "ProjectUpdate", Resource: "project_updates", Payload: ProjectUpdateWebhookEvent{}},
	{Type: "ProjectMilestone", Resource: "milestones", Payload: ProjectMilestoneWebhookEvent{}},
	{Type: "Document", Resource: "documents", Payload: DocumentWebhookEvent{}},
	{Type: "Initiative", Resource: "initiatives", Payload: InitiativeWebhookEvent{}},
	{Type: "InitiativeUpdate", Resource: "initiative_updates", Payload: InitiativeUpdateWebhookEvent{}},
	{Type: "Cycle", Resource: "cycles", Payload: CycleWebhookEvent{}},
	{Type: "Customer", Resource: "customers", Payload: CustomerWebhookEvent{}},
	{Type: "CustomerNeed", Resource: "customer_requests", Payload: CustomerNeedWebhookEvent{}},
	{Type: "User", Resource: "users", Payload: UserWebhookEvent{}},
	{Type: "Release", Resource: "releases", Payload: ReleaseWebhookEvent{}},
	{Type: "ReleaseNote", Resource: "release_notes", Payload: ReleaseNoteWebhookEvent{}},
	{Type: "IssueSLA", Resource: "issue_sla", Payload: IssueSLAWebhookEvent{}},
	{Type: "WebhookTest", Resource: "", Payload: WebhookTestEvent{}},
	{Type: "OAuthApp", Resource: "oauth_app", ApplicationOnly: true, Payload: OAuthAppWebhookEvent{}},
	{Type: "PermissionChange", Resource: "permission_changes", ApplicationOnly: true, Payload: PermissionChangeWebhookEvent{}},
	{Type: "AppUserNotification", Resource: "app_user_notifications", ApplicationOnly: true, Payload: AppUserNotificationWebhookEvent{}},
	{Type: "AgentSessionEvent", Resource: "agent_session_events", ApplicationOnly: true, Payload: AgentSessionWebhookEvent{}},
}
