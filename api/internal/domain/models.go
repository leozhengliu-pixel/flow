package domain

import (
	"encoding/json"
	"time"
)

type User struct {
	App               bool       `json:"app,omitempty"`
	BuiltinAgent      bool       `json:"builtinAgent,omitempty"`
	OAuthClientID     string     `json:"oauthClientId,omitempty"`
	AppScopes         []string   `json:"appScopes,omitempty"`
	AppTeamIDs        []string   `json:"appTeamIds,omitempty"`
	AppAllPublicTeams bool       `json:"appAllPublicTeams,omitempty"`
	OutOfOfficeUntil  *time.Time `json:"outOfOfficeUntil,omitempty"`
	ID                string     `json:"id"`
	UserID            string     `json:"userId,omitempty"`
	Name              string     `json:"name"`
	DisplayName       string     `json:"displayName"`
	// Username is the workspace-unique handle (shown as "username"), derived
	// per request from account settings or the email local part.
	Username      string `json:"username,omitempty"`
	JobTitle      string `json:"jobTitle,omitempty"`
	Email         string `json:"email"`
	AvatarURL     string `json:"avatarUrl,omitempty"`
	Active        bool   `json:"active"`
	EmailVerified bool   `json:"emailVerified"`
	// Via is request-scoped OAuth attribution ("<User> via <App>"). It is never
	// serialized on the user; records copy it into their BotActor field.
	Via *ActorBot `json:"-"`
}

// AuthIdentity binds a user to a stable identifier issued by an external provider.
// The provider and issuer are part of the key because OIDC subject values are
// only guaranteed to be unique within an issuer.
type AuthIdentity struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userId"`
	Provider    string    `json:"provider"`
	Issuer      string    `json:"issuer"`
	Subject     string    `json:"subject"`
	Username    string    `json:"username,omitempty"`
	ClaimsJSON  string    `json:"claimsJson,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	LastLoginAt time.Time `json:"lastLoginAt"`
}

// IdentityProvider is a workspace-owned enterprise login configuration. Secrets
// are referenced by environment variable rather than stored in workspace JSON.
type IdentityProvider struct {
	ID              string   `json:"id"`
	WorkspaceID     string   `json:"workspaceId"`
	Type            string   `json:"type"`
	Name            string   `json:"name"`
	Issuer          string   `json:"issuer"`
	ClientID        string   `json:"clientId,omitempty"`
	ClientSecretEnv string   `json:"clientSecretEnv,omitempty"`
	Scopes          []string `json:"scopes"`
	Domains         []string `json:"domains"`
	Enabled         bool     `json:"enabled"`
	Enforced        bool     `json:"enforced"`
	// RoleClaim optionally names an IdP claim containing a workspace role.
	RoleClaim       string            `json:"roleClaim,omitempty"`
	RoleMapping     map[string]string `json:"roleMapping,omitempty"`
	DefaultRole     string            `json:"defaultRole,omitempty"`
	DiscoveryStatus string            `json:"discoveryStatus"`
	LastVerifiedAt  *time.Time        `json:"lastVerifiedAt,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}

type WorkspaceMember struct {
	User       User       `json:"user"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	JoinedAt   time.Time  `json:"joinedAt"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
	// ManagedBy is "scim" while the workspace's SCIM provisioning manages
	// the member: their role, name, email and status are changed in the
	// identity provider, not in Flow.
	ManagedBy string `json:"managedBy,omitempty"`
}

// MemberManagedBySCIM marks a member provisioned by an active SCIM connection.
const MemberManagedBySCIM = "scim"

// IssuePermission grants access to an issue (and, for sub-issues, may be
// inherited from an ancestor) to an individual, team, or the workspace.
// Private-team issues use these grants to support explicit sharing without
// making the whole team visible.
type IssuePermission struct {
	ID          string    `json:"id"`
	IssueID     string    `json:"issueId"`
	SubjectType string    `json:"subjectType"`
	SubjectID   string    `json:"subjectId"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type TeamMember struct {
	TeamID        string    `json:"teamId"`
	UserID        string    `json:"userId"`
	Role          string    `json:"role"`
	JoinedAt      time.Time `json:"joinedAt"`
	Managed       bool      `json:"managed,omitempty"`
	ManagedSource string    `json:"managedSource,omitempty"`
	// Origin records what created the membership: manual, invitation, api,
	// sync or scim (see team_membership.go). Rows from before origins were
	// tracked read as manual.
	Origin string `json:"origin,omitempty" enum:"manual,invitation,api,sync,scim"`
}

type Invitation struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	TeamIDs     []string   `json:"teamIds"`
	Status      string     `json:"status"`
	InviterID   string     `json:"inviterId"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	AcceptedAt  *time.Time `json:"acceptedAt,omitempty"`
	Token       string     `json:"token,omitempty"`
}

// WorkspaceInviteLink is the shareable workspace join link (distinct from
// per-email Invitation tokens accepted at /invite/:token).
type WorkspaceInviteLink struct {
	Token     string    `json:"token"`
	Enabled   bool      `json:"enabled"`
	CreatedBy string    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// OAuthSyncGroupRequest is an app-user request to sync IdP/directory groups
// into workspace teams. Approve/deny is available when this model is present.
type OAuthSyncGroupRequest struct {
	ID            string     `json:"id"`
	ApplicationID string     `json:"applicationId"`
	GroupName     string     `json:"groupName"`
	TeamID        string     `json:"teamId,omitempty"`
	Status        string     `json:"status"` // pending | approved | denied
	RequestedBy   string     `json:"requestedBy,omitempty"`
	ReviewedBy    string     `json:"reviewedBy,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	ReviewedAt    *time.Time `json:"reviewedAt,omitempty"`
}

type AuthSession struct {
	User        User                  `json:"user"`
	Memberships []WorkspaceMembership `json:"memberships"`
	ExpiresAt   time.Time             `json:"expiresAt"`
}

type AgentMessage struct {
	ID      string             `json:"id"`
	Role    string             `json:"role"`
	Content string             `json:"content"`
	Parts   []AgentMessagePart `json:"parts,omitempty"`
	// Mentions are the resources @-mentioned in a user message, in order.
	Mentions []AgentMention `json:"mentions,omitempty"`
	// Attachments are the files sent with a user message; their content is in object storage at URL.
	Attachments []AgentMessageAttachment `json:"attachments,omitempty"`
	DurationMS  int64                    `json:"durationMs,omitempty"`
	// AnswerTo is the question part a user message answered by picking one of its options (shown as the chosen
	// option on the question, not as a separate message).
	AnswerTo string `json:"answerTo,omitempty"`
	// Feedback is the owner's rating of an assistant reply (thumbs up / down with an optional note).
	Feedback  *AgentFeedback `json:"feedback,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
	// Status is "streaming" while the reply is still being written (its parts are what it has done so far, including
	// approvals and questions waiting for the owner); empty once it finished, failed or was stopped.
	Status string `json:"status,omitempty"`
	// UpdatedAt is when a streaming reply was last saved; the running server refreshes it while the reply waits.
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	// WorkerCursor is how far a streaming reply written by an agent worker run has applied the run's activities
	// (set only while it streams).
	WorkerCursor *AgentWorkerCursor `json:"workerCursor,omitempty"`
	// Offloaded names fields stored outside the workspace record and not
	// loaded yet (see OffloadedContent).
	Offloaded OffloadedContent `json:"_offloaded,omitempty"`
}

// AgentWorkerCursor names the last activity of an agent worker run a reply holds: the run, its attempt and the
// activity's sequence number.
type AgentWorkerCursor struct {
	Run     string `json:"run"`
	Attempt int    `json:"attempt"`
	Seq     int64  `json:"seq"`
}

// AgentMessageAttachment is a file sent with an agent chat message. URL is empty for a file sent by name only (a type
// the agent cannot read).
type AgentMessageAttachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	URL         string `json:"url,omitempty"`
}

// OffloadedContent names, by field ("content", "text", "result", or "body"
// for a document version's content), bodies kept outside the workspace
// record. They are loaded on demand; the field is empty until then.
type OffloadedContent map[string]ContentRef

// ContentRef locates an offloaded body.
type ContentRef struct {
	Key  string `json:"key"`
	Size int    `json:"size"`
	// Summary is the start of an offloaded transcript text.
	Summary string `json:"summary,omitempty"`
}

// AgentFeedback is a rating ("positive" or "negative") and/or a written note about agent output.
type AgentFeedback struct {
	Rating    string    `json:"rating,omitempty"`
	Comment   string    `json:"comment,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// AgentMention is an @-mentioned issue, project, document or user.
type AgentMention struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

type AgentMessagePart struct {
	Elicitation *AgentElicitation `json:"elicitation,omitempty"`
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Title       string            `json:"title,omitempty"`
	Text        string            `json:"text,omitempty"`
	Status      string            `json:"status,omitempty"`
	ToolCall    *AgentToolCall    `json:"toolCall,omitempty"`
	// Question is a choice the agent asked the user for (the ask_user tool; the reference app's select elicitation).
	Question *AgentQuestion `json:"question,omitempty"`
	// Offloaded locates Text while it is not loaded.
	Offloaded OffloadedContent `json:"_offloaded,omitempty"`
}

// AgentQuestion is a question with options the agent paused on. It is answered by the user's next message:
// Selected holds the chosen option's value, Answer free text typed instead.
type AgentQuestion struct {
	Options       []AgentQuestionOption `json:"options"`
	AllowFreeText bool                  `json:"allowFreeText"`
	Selected      string                `json:"selected,omitempty"`
	Answer        string                `json:"answer,omitempty"`
	// Approval is set when the question asks to approve actions the agent held back (saved by earlier versions for
	// bulk deletes or an update it wrote): the app shows a readable preview, and the held calls run once the user
	// approves.
	Approval *AgentApproval `json:"approval,omitempty"`
}

// AgentApproval is a set of tool calls waiting for the user's approval, with what they would change.
type AgentApproval struct {
	// Kind is "connector" (tools of a connector set to always ask), or, in approvals saved by earlier versions,
	// "delete" (several items to the trash) or "publish" (a project or initiative update).
	Kind    string                `json:"kind"`
	Targets []AgentApprovalTarget `json:"targets,omitempty"`
	// Body and Health are the update to publish; Parent names its project or initiative.
	Body   string               `json:"body,omitempty"`
	Health string               `json:"health,omitempty"`
	Parent *AgentApprovalTarget `json:"parent,omitempty"`
	// Calls are the held tool calls; Decision is "approved" or "rejected" once handled.
	Calls    []AgentToolCall `json:"calls,omitempty"`
	Decision string          `json:"decision,omitempty"`
}

// AgentApprovalTarget is one item an approval names, shown as a chip.
type AgentApprovalTarget struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Title string `json:"title,omitempty"`
	Href  string `json:"href,omitempty"`
	// SubIssues counts an issue's sub-issues, which deleting it affects.
	SubIssues int `json:"subIssues,omitempty"`
	// Affected counts other linked items the delete touches, by kind ("issues", "milestones", …).
	Affected map[string]int `json:"affected,omitempty"`
	// Permanent marks a delete with no trash (customers, loops, views…): it cannot be undone.
	Permanent bool `json:"permanent,omitempty"`
}

type AgentQuestionOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type AgentElicitation struct {
	ID            string          `json:"id"`
	SessionID     string          `json:"sessionId"`
	ConnectorName string          `json:"connectorName"`
	ConnectorURL  string          `json:"connectorUrl"`
	Mode          string          `json:"mode"`
	Message       string          `json:"message"`
	Schema        json.RawMessage `json:"schema,omitempty"`
	URL           string          `json:"url,omitempty"`
	Action        string          `json:"action,omitempty"`
}

type AgentToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Status    string          `json:"status"`
	Error     string          `json:"error,omitempty"`
	// Title overrides the tool row label, e.g. "Updated workflow definition draft".
	Title string `json:"title,omitempty"`
	// Connector and RemoteTool name an external connector tool call, whose Name is only an opaque id
	// ("external_<hash>"): the connector's display name and the tool's own name on that connector.
	Connector  string `json:"connector,omitempty"`
	RemoteTool string `json:"remoteTool,omitempty"`
	// ApprovalID is set while a write tool is waiting for the user decision.
	// It is intentionally transient and omitted from normal tool calls.
	ApprovalID string `json:"approvalId,omitempty"`
	// Change is what a successful Flow write did (the chat's "Created …" / "Updated …" result card and its Undo).
	Change *AgentChange `json:"change,omitempty"`
	// Offloaded locates Result while it is not loaded.
	Offloaded OffloadedContent `json:"_offloaded,omitempty"`
}

// AgentChange records one agent write for the result card and Undo / Restore: the entity it touched and, for an
// update, the previous and new values of the fields it changed (in the write tool's argument shape, capped in size).
type AgentChange struct {
	// Action is "created", "updated" or "deleted".
	Action     string `json:"action"`
	EntityType string `json:"entityType"`
	EntityID   string `json:"entityId,omitempty"`
	// ParentID is the project or initiative a milestone or status update belongs to.
	ParentID string `json:"parentId,omitempty"`
	// Label names the entity as it was written (issue identifier, project name, document title…).
	Label string `json:"label,omitempty"`
	// URL is the entity's app path, so the card links even before the client has loaded the new record.
	URL    string          `json:"url,omitempty"`
	Fields []string        `json:"fields,omitempty"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
	// Undoable is false when the change cannot be reverted (unsupported entity or snapshots over the size cap).
	Undoable bool `json:"undoable"`
	// State is "" while the change is applied and "undone" after Undo (the card then offers Restore).
	State string `json:"state,omitempty"`
	// Note explains a partial undo or restore, e.g. fields that changed since and were left alone.
	Note string `json:"note,omitempty"`
	// Skipped lists the fields the last undo or restore left alone because they changed after the agent's edit.
	Skipped   []string   `json:"skipped,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

type AgentSession struct {
	ID       string   `json:"id"`
	SlugID   string   `json:"slugId"`
	UserID   string   `json:"userId"`
	Title    string   `json:"title"`
	Favorite bool     `json:"favorite"`
	Location string   `json:"location"`
	IssueIDs []string `json:"issueIds"`
	// ProjectIDs and DocumentIDs are resources @-mentioned in the conversation.
	ProjectIDs  []string `json:"projectIds,omitempty"`
	DocumentIDs []string `json:"documentIds,omitempty"`
	// InitiativeIDs are initiatives in context: @-mentioned, or the initiative page the chat was opened on.
	InitiativeIDs []string       `json:"initiativeIds,omitempty"`
	UserIDs       []string       `json:"userIds,omitempty"`
	LoopIDs       []string       `json:"loopIds,omitempty"` // loops (usually drafts) the conversation is building
	SkillIDs      []string       `json:"skillIds"`
	Messages      []AgentMessage `json:"messages"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	// LastReadAt is when the owner last saw the conversation. It is unread while
	// UpdatedAt is later (a reply landed after they looked); conversations from
	// before read tracking have no LastReadAt and count as read.
	LastReadAt *time.Time `json:"lastReadAt,omitempty"`
	// Feedback holds notes the owner left about the whole conversation ("Leave feedback…" in the chat menu).
	Feedback []AgentFeedback `json:"feedback,omitempty"`
	// DeletedAt marks a chat the owner deleted: hidden everywhere, restorable until the retention window passes.
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
	// Origin names the chat platform of a conversation started in a direct message with the workspace's Lark or
	// Feishu bot; empty for chats started in Flow.
	Origin string `json:"origin,omitempty" enum:"lark,feishu"`
}

type PersonalAgentSkill struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId"`
	Name         string    `json:"name"`
	Instructions string    `json:"instructions"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type AccountSession struct {
	ID      string `json:"id"`
	Current bool   `json:"current"`
	// Device metadata is intentionally optional for legacy sessions. New
	// sessions are decorated by the HTTP layer from the request user-agent.
	Name            string    `json:"name,omitempty"`
	OperatingSystem string    `json:"operatingSystem,omitempty"`
	BrowserType     string    `json:"browserType,omitempty"`
	Location        string    `json:"location,omitempty"`
	IP              string    `json:"ip,omitempty"`
	CountryCodes    []string  `json:"countryCodes,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	LastSeenAt      time.Time `json:"lastSeenAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type Workspace struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	URLKey              string     `json:"urlKey"`
	Icon                string     `json:"icon,omitempty"`
	LogoURL             string     `json:"logoUrl,omitempty"`
	Color               string     `json:"color,omitempty"`
	Region              string     `json:"region,omitempty"`
	CreatedAt           time.Time  `json:"createdAt,omitempty"`
	DeletionRequestedAt *time.Time `json:"deletionRequestedAt,omitempty"`
}

type WorkspaceMembership struct {
	Workspace  Workspace `json:"workspace"`
	Role       string    `json:"role"`
	JoinedAt   time.Time `json:"joinedAt"`
	IssueCount int       `json:"issueCount"`
}

type AccountBootstrap struct {
	Viewer                         User                  `json:"viewer"`
	Workspaces                     []WorkspaceMembership `json:"workspaces"`
	LastWorkspaceKey               string                `json:"lastWorkspaceKey,omitempty"`
	WorkspaceRegionSelectorEnabled bool                  `json:"workspaceRegionSelectorEnabled"`
	WorkspaceDefaultRegion         string                `json:"workspaceDefaultRegion"`
	// ChatPlatform is the deployment's first-class chat platform (FLOW_CHAT_PLATFORM).
	ChatPlatform string `json:"chatPlatform,omitempty" enum:"slack,lark,feishu"`
}

type Team struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Key            string     `json:"key"`
	Color          string     `json:"color"`
	Icon           string     `json:"icon,omitempty"`
	Private        bool       `json:"private,omitempty"`
	ExternalSource string     `json:"externalSource,omitempty"`
	RetiredAt      *time.Time `json:"retiredAt,omitempty"`
	// ArchivedAt marks a deleted team inside its restoration window. Archived
	// teams and their issues are hidden from every viewer until restored or purged.
	ArchivedAt   *time.Time `json:"archivedAt,omitempty"`
	ArchivedByID string     `json:"archivedById,omitempty"`
	CreatedAt    *time.Time `json:"createdAt,omitempty"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

type Customer struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	LogoURL       string   `json:"logoUrl,omitempty"`
	OwnerID       string   `json:"ownerId,omitempty"`
	Status        string   `json:"status"`
	Tier          string   `json:"tier,omitempty"`
	AnnualRevenue float64  `json:"annualRevenue,omitempty"`
	Size          int      `json:"size,omitempty"`
	Domains       []string `json:"domains"`
	// ExternalIDs identify the customer in other systems (CRM, support desk);
	// each is unique within the workspace and usable for upserts.
	ExternalIDs []string  `json:"externalIds,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type WorkflowState struct {
	ID          string  `json:"id"`
	TeamID      string  `json:"teamId,omitempty"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Color       string  `json:"color"`
	Type        string  `json:"type"`
	Position    float64 `json:"position"`
	Default     bool    `json:"default,omitempty"`
	Reserved    bool    `json:"reserved,omitempty"`
}

type IssueLabel struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Color         string     `json:"color"`
	Description   string     `json:"description,omitempty"`
	IssueCount    int        `json:"issueCount,omitempty"`
	Scope         string     `json:"scope,omitempty"`
	ResourceType  string     `json:"resourceType,omitempty"`
	GroupID       string     `json:"groupId,omitempty"`
	CreatorID     string     `json:"creatorId,omitempty"`
	CreatedAt     time.Time  `json:"createdAt,omitempty"`
	LastAppliedAt *time.Time `json:"lastAppliedAt,omitempty"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`
}

type LabelGroup struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Color        string     `json:"color"`
	Description  string     `json:"description,omitempty"`
	Scope        string     `json:"scope,omitempty"`
	ResourceType string     `json:"resourceType"`
	CreatedAt    time.Time  `json:"createdAt"`
	ArchivedAt   *time.Time `json:"archivedAt,omitempty"`
}

type ProjectSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color"`
}

type DocumentContent struct {
	ID           string         `json:"id"`
	Version      int64          `json:"version"`
	Content      string         `json:"content"`
	ContentState string         `json:"contentState"`
	ContentData  map[string]any `json:"contentData"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

type Document struct {
	ID           string         `json:"id"`
	SlugID       string         `json:"slugId"`
	Title        string         `json:"title"`
	Icon         string         `json:"icon,omitempty"`
	Color        string         `json:"color,omitempty"`
	Content      string         `json:"content"`
	ContentState string         `json:"contentState,omitempty"`
	ContentData  map[string]any `json:"contentData,omitempty"`
	Creator      User           `json:"creator"`
	BotActor     *ActorBot      `json:"botActor,omitempty"`
	ProjectIDs   []string       `json:"projectIds"`
	TeamIDs      []string       `json:"teamIds"`
	IssueID      string         `json:"issueId,omitempty"`
	// InheritedTeamIDs are the teams of the issue and projects the document
	// is attached to. Read-only: Flow updates them when the document is
	// attached or detached, the issue moves to another team, or a project's
	// teams change. When one of them is private, workspace-wide access to
	// the document is limited to people who can read that team; people and
	// teams the document is shared with directly keep their access. Public
	// teams do not limit access.
	InheritedTeamIDs []string `json:"inheritedTeamIds,omitempty"`
	// IssueTeamID is the team of the issue the document is attached to.
	// Read-only.
	IssueTeamID   string               `json:"issueTeamId,omitempty"`
	SubscriberIDs []string             `json:"subscriberIds"`
	Favorite      bool                 `json:"favorite"`
	ArchivedAt    *time.Time           `json:"archivedAt,omitempty"`
	CreatedAt     time.Time            `json:"createdAt"`
	UpdatedAt     time.Time            `json:"updatedAt"`
	Revisions     []DocumentRevision   `json:"revisions"`
	Attachments   []Attachment         `json:"attachments,omitempty"`
	Permissions   []DocumentPermission `json:"permissions,omitempty"`
	// Version increases with every write that changes the document (a PATCH
	// that only repeats current values keeps it); PATCH callers may send
	// expectedVersion to detect a concurrent change (409 with the current
	// document). ContentVersion increases only when the collaborative base
	// state (contentState) is replaced, guarding update-log compaction.
	Version        int64 `json:"version"`
	ContentVersion int64 `json:"contentVersion"`
	// PreviousSlugIDs keeps the slugs a renamed document used before, so old
	// links keep resolving (and the web app redirects to slugId).
	PreviousSlugIDs []string `json:"previousSlugIds,omitempty"`
	// CollaborationID names the realtime (Yjs) document the editor joins; ""
	// means the document id. Restoring a version starts a new generation so
	// editors holding the replaced state reload instead of merging it back.
	CollaborationID string `json:"collaborationId,omitempty"`
	// DeletedAt is only set on a document read back from "Recently deleted".
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
}

// DocumentPermission grants a role to a workspace member, team, or the whole
// workspace. Roles are owner, editor, commenter, or viewer.
type DocumentPermission struct {
	ID          string    `json:"id"`
	DocumentID  string    `json:"documentId"`
	SubjectType string    `json:"subjectType"`
	SubjectID   string    `json:"subjectId"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type DocumentRevision struct {
	ID           string         `json:"id"`
	DocumentID   string         `json:"documentId"`
	Title        string         `json:"title"`
	Content      string         `json:"content"`
	ContentState string         `json:"contentState,omitempty"`
	ContentData  map[string]any `json:"contentData,omitempty"`
	Author       User           `json:"author"`
	// BotActor attributes the latest edit to an application or to Flow's agent.
	BotActor *ActorBot `json:"botActor,omitempty"`
	// AuthorIDs lists everyone who edited during the burst this version
	// covers (Author is the latest). CreatedAt is when the burst ended.
	AuthorIDs []string  `json:"authorIds,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// StartedAt is when the editing burst this version covers began.
	StartedAt *time.Time `json:"startedAt,omitempty"`
	// Offloaded["body"] locates Content, ContentState and ContentData while
	// they are not loaded (document history loads them on demand).
	Offloaded OffloadedContent `json:"_offloaded,omitempty"`
}

type DocumentTemplate struct {
	ID           string         `json:"id"`
	TeamID       string         `json:"teamId"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	Title        string         `json:"title,omitempty"`
	Icon         string         `json:"icon,omitempty"`
	Color        string         `json:"color,omitempty"`
	Content      string         `json:"content,omitempty"`
	ContentState string         `json:"contentState,omitempty"`
	ContentData  map[string]any `json:"contentData,omitempty"`
	Creator      User           `json:"creator"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// Issue follows Flow's public GraphQL entity naming and relationship shape.
type Issue struct {
	IsSummary              bool             `json:"isSummary,omitempty"`
	ID                     string           `json:"id"`
	Version                int64            `json:"version"`
	Identifier             string           `json:"identifier"`
	Number                 int              `json:"number"`
	PreviousIdentifiers    []string         `json:"previousIdentifiers,omitempty"` // identifiers the issue had before it moved to other teams, oldest first; they still resolve
	Title                  string           `json:"title"`
	Icon                   string           `json:"icon,omitempty"` // recurring issue icon (issue template icon format)
	Description            string           `json:"description"`
	DescriptionState       string           `json:"descriptionState,omitempty"`
	DocumentContent        *DocumentContent `json:"documentContent,omitempty"`
	Priority               int              `json:"priority"`
	PriorityLabel          string           `json:"priorityLabel"`
	SortOrder              float64          `json:"sortOrder"`
	Estimate               *float64         `json:"estimate,omitempty"`
	DueDate                *string          `json:"dueDate,omitempty"`
	SLABreachesAt          *time.Time       `json:"slaBreachesAt,omitempty"`
	SLAType                string           `json:"slaType,omitempty"`
	CreatedAt              time.Time        `json:"createdAt"`
	UpdatedAt              time.Time        `json:"updatedAt"`
	CompletedAt            *time.Time       `json:"completedAt,omitempty"`
	StartedAt              *time.Time       `json:"startedAt,omitempty"`
	TriagedAt              *time.Time       `json:"triagedAt,omitempty"`
	SuggestionsGeneratedAt *time.Time       `json:"suggestionsGeneratedAt,omitempty"`
	SuggestionsSource      string           `json:"suggestionsSource,omitempty"`
	SuggestionsThinking    string           `json:"suggestionsThinking,omitempty"`
	SuggestionsInputSketch string           `json:"suggestionsInputSketch,omitempty"` // title/description fingerprint of the last generation
	StatusChangedAt        *time.Time       `json:"statusChangedAt,omitempty"`
	AutoClosedAt           *time.Time       `json:"autoClosedAt,omitempty"`
	CanceledAt             *time.Time       `json:"canceledAt,omitempty"`
	ArchivedAt             *time.Time       `json:"archivedAt,omitempty"`
	ShareToken             string           `json:"shareToken,omitempty"`
	SharedAt               *time.Time       `json:"sharedAt,omitempty"`
	Team                   Team             `json:"team"`
	State                  WorkflowState    `json:"state"`
	Assignee               *User            `json:"assignee,omitempty"`
	Delegate               *User            `json:"delegate,omitempty"`
	Creator                User             `json:"creator"`
	BotActor               *ActorBot        `json:"botActor,omitempty"`
	Labels                 []IssueLabel     `json:"labels"`
	Project                *ProjectSummary  `json:"project,omitempty"`
	ProjectMilestoneID     *string          `json:"projectMilestoneId,omitempty"`
	CycleID                *string          `json:"cycleId,omitempty"`
	AddedToCycle           string           `json:"addedToCycle,omitempty"`
	AgentSessionID         string           `json:"agentSessionId,omitempty"`
	SuggestedLabelIDs      []string         `json:"suggestedLabelIds,omitempty"`
	// Active Triage Intelligence suggestion targets (the "Suggested
	// assignee/project/team" and "Suggested duplicates/relations" filters). They
	// mirror the active IssueSuggestions so filters stay indexed lookups.
	SuggestedAssigneeIDs  []string `json:"suggestedAssigneeIds,omitempty"`
	SuggestedProjectIDs   []string `json:"suggestedProjectIds,omitempty"`
	SuggestedTeamIDs      []string `json:"suggestedTeamIds,omitempty"`
	SuggestedDuplicateIDs []string `json:"suggestedDuplicateIds,omitempty"`
	SuggestedRelatedIDs   []string `json:"suggestedRelatedIds,omitempty"`
	// AgentSessionState is the delegated agent task's status, decorated on
	// read (never persisted): pending, active, awaitingInput, complete, error, canceled,
	// or merged once a pull request linked to the issue merged during the session.
	AgentSessionState    string              `json:"agentSessionState,omitempty"`
	ExternalSource       string              `json:"externalSource,omitempty"`
	AutoClosed           bool                `json:"autoClosed,omitempty"`
	TemplateID           string              `json:"templateId,omitempty"`
	ParentID             *string             `json:"parentId,omitempty"`
	Recurrence           string              `json:"recurrence,omitempty"`
	NextOccurrenceAt     *time.Time          `json:"nextOccurrenceAt,omitempty"`
	RecurrenceSeriesID   string              `json:"recurrenceSeriesId,omitempty"`
	RecurrenceOccurrence string              `json:"recurrenceOccurrence,omitempty"`
	SnoozedUntil         *time.Time          `json:"snoozedUntil,omitempty"`
	SubscriberIDs        []string            `json:"subscriberIds"`
	Reactions            map[string][]string `json:"reactions"`
	SubIssueIDs          []string            `json:"subIssueIds"`
	Relations            []IssueRelation     `json:"relations"`
	Attachments          []Attachment        `json:"attachments"`
	Permissions          []IssuePermission   `json:"permissions,omitempty"`
	// Computed by the list projection only; never persisted on a full record.
	HasContent     bool           `json:"hasContent,omitempty"`
	ReactionCounts map[string]int `json:"reactionCounts,omitempty"`
	// AffectedIssues is set on the response of an issue update only (never
	// persisted): the other issues the write changed as a side effect and the
	// version each one now has, so a client can keep editing them without
	// waiting for the realtime update.
	AffectedIssues []IssueVersion `json:"affectedIssues,omitempty"`
}

// IssueVersion is an issue's id with its current version.
type IssueVersion struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type Cycle struct {
	ID          string    `json:"id"`
	Number      int       `json:"number"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	TeamID      string    `json:"teamId"`
	StartsAt    time.Time `json:"startsAt"`
	EndsAt      time.Time `json:"endsAt"`
	Status      string    `json:"status"`
	// Capacity is the cycle's planning capacity, in the unit of its scope
	// (estimate points, or issues when the team does not use estimates):
	// capacityOverride when one is set, otherwise capacityAutomatic. It is
	// computed when the cycle is read.
	Capacity float64 `json:"capacity"`
	// CapacityOverride is the capacity typed in Edit cycle. Absent means the
	// capacity is automatic; PATCH /api/cycles/{id} with "capacity": null
	// (or 0) clears it.
	CapacityOverride *int `json:"capacityOverride,omitempty"`
	// CapacityAutomatic is the computed capacity: the team's velocity (the
	// average completed scope of its last three completed cycles, fewer when
	// fewer exist) or, without history, a default of 30 per active team
	// member. Computed when the cycle is read.
	CapacityAutomatic float64 `json:"capacityAutomatic"`
	// CapacitySource tells where capacity comes from: override, velocity or
	// default. Computed when the cycle is read.
	CapacitySource   string                    `json:"capacitySource,omitempty" enum:"override,velocity,default"`
	CapacityByMember map[string]map[string]int `json:"capacityByMember,omitempty"`
	Favorite         bool                      `json:"favorite"`
	Resources        []CycleResource           `json:"resources"`
	CalendarToken    string                    `json:"calendarToken,omitempty"`
	Insight          map[string]string         `json:"insight,omitempty"`
	CreatedAt        time.Time                 `json:"createdAt"`
	UpdatedAt        time.Time                 `json:"updatedAt"`
}

// OptionalInt is a request number that tells an absent key from null: Set
// reports that the key was sent, and Value is nil when it was null.
type OptionalInt struct {
	Set   bool
	Value *int
}

func (value *OptionalInt) UnmarshalJSON(raw []byte) error {
	value.Set = true
	value.Value = nil
	if string(raw) == "null" {
		return nil
	}
	var number int
	if err := json.Unmarshal(raw, &number); err != nil {
		return err
	}
	value.Value = &number
	return nil
}

func (value OptionalInt) MarshalJSON() ([]byte, error) {
	if value.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*value.Value)
}

func (value OptionalInt) IsZero() bool { return !value.Set }

type CycleResource struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	DocumentID string    `json:"documentId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type CycleSettings struct {
	Enabled          bool `json:"enabled"`
	DurationWeeks    int  `json:"durationWeeks"`
	CooldownWeeks    int  `json:"cooldownWeeks"`
	StartsOn         int  `json:"startsOn"`
	UpcomingCount    int  `json:"upcomingCount"`
	Capacity         int  `json:"capacity"`
	AutoCreate       bool `json:"autoCreate"`
	AutoAddActive    bool `json:"autoAddActive"`
	AutoAddDueDate   bool `json:"autoAddDueDate"`
	AutoAddStarted   bool `json:"autoAddStarted"`
	AutoAddCompleted bool `json:"autoAddCompleted"`
	AutoMigrate      bool `json:"autoMigrate"`
	FavoriteView     bool `json:"favoriteView"`
}

type TeamSettings struct {
	TeamID                string `json:"teamId"`
	Description           string `json:"description,omitempty"`
	Timezone              string `json:"timezone"`
	EstimateType          string `json:"estimateType"`
	DefaultStateID        string `json:"defaultStateId"`
	DefaultPriority       int    `json:"defaultPriority"`
	IssueEmailEnabled     bool   `json:"issueEmailEnabled"`
	DetailedHistory       bool   `json:"detailedHistory"`
	Access                string `json:"access"`
	MembershipRestriction string `json:"membershipRestriction"`
	SettingsPermission    string `json:"settingsPermission"`
	LabelPermission       string `json:"labelPermission"`
	TemplatePermission    string `json:"templatePermission"`
	AgentSkillPermission  string `json:"agentSkillPermission"`
	LoopPermission        string `json:"loopPermission"`
	MemberPermission      string `json:"memberPermission"`
	PinnedViewPermission  string `json:"pinnedViewPermission"`
	// Templates preselected when creating issues/projects for this team.
	DefaultIssueTemplateForMembersID    string `json:"defaultIssueTemplateForMembersId,omitempty"`
	DefaultIssueTemplateForNonMembersID string `json:"defaultIssueTemplateForNonMembersId,omitempty"`
	DefaultProjectTemplateID            string `json:"defaultProjectTemplateId,omitempty"`
	// Estimate scale options (see the web estimates module).
	EstimateAllowZero bool `json:"estimateAllowZero"`
	EstimateExtended  bool `json:"estimateExtended"`
	// EstimateCountUnestimated is nil for teams created before the option,
	// which keeps the historical behavior of counting them as 1 point.
	EstimateCountUnestimated *bool                `json:"estimateCountUnestimated,omitempty"`
	IssueSharingEnabled      bool                 `json:"issueSharingEnabled"`
	IssueSharingPermission   string               `json:"issueSharingPermission,omitempty"`
	SlackChannelID           string               `json:"slackChannelId,omitempty"`
	SlackChannelName         string               `json:"slackChannelName,omitempty"`
	SlackNotifications       map[string]bool      `json:"slackNotifications"`
	PRAutomations            map[string]string    `json:"prAutomations"`
	AutoCloseParents         bool                 `json:"autoCloseParents"`
	AutoCloseSubIssues       bool                 `json:"autoCloseSubIssues"`
	AutoCloseStale           bool                 `json:"autoCloseStale"`
	StaleMonths              int                  `json:"staleMonths"`
	StaleStatusID            string               `json:"staleStatusId,omitempty"`
	AutoArchiveMonths        int                  `json:"autoArchiveMonths"`
	ProgressOrder            string               `json:"progressOrder"`
	ReleaseAutomations       []TeamAutomationRule `json:"releaseAutomations"`
	TriageEnabled            bool                 `json:"triageEnabled"`
	TriageRequirePriority    bool                 `json:"triageRequirePriority"`
	TriageAction             string               `json:"triageAction"`
	TriageRules              []TeamAutomationRule `json:"triageRules"`
	AgentSkills              []TeamAgentSkill     `json:"agentSkills"`
	AgentConnectors          []TeamAgentConnector `json:"agentConnectors,omitempty"`
	ProjectUpdatePrompt      string               `json:"projectUpdatePrompt"`
	AgentGuidance            string               `json:"agentGuidance,omitempty"` // Team settings › Agents; wins over workspace and parent team guidance
	ResolvedSummaries        bool                 `json:"resolvedThreadSummaries"`
	ShowInitiatives          bool                 `json:"showInitiatives"`
	InheritIssueEstimation   bool                 `json:"inheritIssueEstimation"`
	InheritWorkflowStatuses  bool                 `json:"inheritWorkflowStatuses"`
	InheritProjectStatuses   bool                 `json:"inheritProjectStatuses"`
	InheritCycles            bool                 `json:"inheritCycles"`
	ParentTeamID             string               `json:"parentTeamId,omitempty"`
	// TriageActionUserIDs are the members notified or assigned by the triage
	// responsibility action ("notify" / "assign").
	TriageActionUserIDs []string `json:"triageActionUserIds"`
	// IssueViewDefaults holds team default display options per issue view
	// ("all", "active", "backlog", "board"); opaque to the server.
	IssueViewDefaults map[string]json.RawMessage `json:"issueViewDefaults,omitempty"`
	// IssueViewInsights holds the team's shared Insights configuration per
	// issue view ("Set default for everyone"); validated by the server.
	IssueViewInsights map[string]json.RawMessage `json:"issueViewInsights,omitempty"`
}

type TeamAutomationRule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Trigger string `json:"trigger"`
	Action  string `json:"action"`
	Enabled bool   `json:"enabled"`
}

type TeamAgentSkill struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Enabled      bool   `json:"enabled"`
}

type IssueTemplate struct {
	ID               string              `json:"id"`
	TeamID           string              `json:"teamId"`
	Icon             string              `json:"icon,omitempty"`
	Color            string              `json:"color,omitempty"`
	Name             string              `json:"name"`
	Title            string              `json:"title,omitempty"`
	Description      string              `json:"description,omitempty"`
	Body             string              `json:"body,omitempty"`
	StateID          string              `json:"stateId,omitempty"`
	Priority         int                 `json:"priority"`
	AssigneeID       string              `json:"assigneeId,omitempty"`
	ProjectID        string              `json:"projectId,omitempty"`
	LabelIDs         []string            `json:"labelIds"`
	Scope            string              `json:"scope,omitempty"`
	VisibilityTeamID string              `json:"visibilityTeamId,omitempty"`
	TemplateType     string              `json:"templateType,omitempty"`
	FormFields       []TemplateFormField `json:"formFields,omitempty"`
	SubIssues        []TemplateSubIssue  `json:"subIssues,omitempty"`
	Creator          User                `json:"creator"`
	CreatedAt        time.Time           `json:"createdAt"`
	UpdatedAt        time.Time           `json:"updatedAt"`
}

type TemplateFormField struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Options     []string `json:"options,omitempty"`
}

type TemplateSubIssue struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	TeamID      string   `json:"teamId,omitempty"`
	Priority    int      `json:"priority"`
	AssigneeID  string   `json:"assigneeId,omitempty"`
	LabelIDs    []string `json:"labelIds,omitempty"`
}

type IssueRelation struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	IssueID        string `json:"issueId"`
	RelatedIssueID string `json:"relatedIssueId"`
}

type Attachment struct {
	ID          string    `json:"id"`
	IssueID     string    `json:"issueId"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	ContentType string    `json:"contentType"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"createdAt"`
	Creator     User      `json:"creator"`
	BotActor    *ActorBot `json:"botActor,omitempty"`
	Provider    string    `json:"provider,omitempty"`
	ProviderID  string    `json:"providerId,omitempty"`
	ProviderURL string    `json:"providerUrl,omitempty"`
	LinkbackURL string    `json:"linkbackUrl,omitempty"`
	SyncStatus  string    `json:"syncStatus,omitempty"`
	// Link attachments: a subtitle, an icon and
	// free-form key/value metadata shown with the link. Re-creating an
	// attachment with the same URL on the same issue updates it.
	Subtitle  string         `json:"subtitle,omitempty"`
	IconURL   string         `json:"iconUrl,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	UpdatedAt *time.Time     `json:"updatedAt,omitempty"`
}

type ProjectStatus struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Color       string  `json:"color"`
	Type        string  `json:"type"`
	Position    float64 `json:"position"`
}

type ProjectProgressHistoryPoint struct {
	Date                     time.Time `json:"date"`
	Value                    float64   `json:"value"`
	BacklogEstimate          float64   `json:"backlogEstimate,omitempty"`
	UnstartedEstimate        float64   `json:"unstartedEstimate,omitempty"`
	StartedEstimate          float64   `json:"startedEstimate,omitempty"`
	CompletedEstimate        float64   `json:"completedEstimate,omitempty"`
	ScopeEstimate            float64   `json:"scopeEstimate,omitempty"`
	ScopeCount               int       `json:"scopeCount,omitempty"`
	CompletedIssueCount      int       `json:"completedIssueCount,omitempty"`
	StartedIssueCount        int       `json:"startedIssueCount,omitempty"`
	CompletedEstimateToday   float64   `json:"completedEstimateToday,omitempty"`
	CompletedIssueCountToday int       `json:"completedIssueCountToday,omitempty"`
	AddedEstimateToday       float64   `json:"addedEstimateToday,omitempty"`
	AddedIssueCountToday     int       `json:"addedIssueCountToday,omitempty"`
}

type Project struct {
	ID                   string                       `json:"id"`
	Name                 string                       `json:"name"`
	SlugID               string                       `json:"slugId"`                    // URL slug `<name-slug>-<12 hex>`; the hex suffix stays when the name changes
	PreviousSlugIDs      []string                     `json:"previousSlugIds,omitempty"` // earlier slugs (before a rename, or the legacy name-only slug); links using them still resolve
	Summary              string                       `json:"summary"`
	Description          string                       `json:"description"`
	Icon                 string                       `json:"icon,omitempty"`
	Color                string                       `json:"color"`
	Priority             int                          `json:"priority"`
	Position             float64                      `json:"position"`
	PriorityLabel        string                       `json:"priorityLabel"`
	Progress             float64                      `json:"progress"`
	Health               string                       `json:"health"`
	Status               ProjectStatus                `json:"status"`
	Lead                 *User                        `json:"lead,omitempty"`
	CreatorID            string                       `json:"creatorId,omitempty"`
	Creator              *User                        `json:"creator,omitempty"`
	BotActor             *ActorBot                    `json:"botActor,omitempty"` // creation through an application or Flow's agent
	MemberIDs            []string                     `json:"memberIds"`
	LabelIDs             []string                     `json:"labelIds"`
	TeamIDs              []string                     `json:"teamIds"`
	DependencyIDs        []string                     `json:"dependencyIds"`
	Initiatives          []string                     `json:"initiatives"`
	Customers            []string                     `json:"customers"`
	Resources            []ProjectResource            `json:"resources"`
	Milestones           []ProjectMilestone           `json:"milestones"`
	Comments             []Comment                    `json:"comments"`
	CommentAttachments   []Attachment                 `json:"commentAttachments,omitempty"`
	DescriptionRevisions []ProjectDescriptionRevision `json:"descriptionRevisions"`
	UpdateCadence        string                       `json:"updateCadence"`
	UpdateSchedule       *ProjectUpdateSchedule       `json:"updateSchedule,omitempty"`
	StartDate            *string                      `json:"startDate,omitempty"`
	StartDateResolution  string                       `json:"startDateResolution,omitempty"`
	TargetDate           *string                      `json:"targetDate,omitempty"`
	TargetDateResolution string                       `json:"targetDateResolution,omitempty"`
	SlackChannelID       string                       `json:"slackChannelId,omitempty"`
	SlackChannelName     string                       `json:"slackChannelName,omitempty"`
	IssueCount           int                          `json:"issueCount"`
	ArchivedAt           *time.Time                   `json:"archivedAt,omitempty"`
	CreatedAt            time.Time                    `json:"createdAt"`
	UpdatedAt            time.Time                    `json:"updatedAt"`

	IssueCountHistory      []ProjectProgressHistoryPoint `json:"issueCountHistory,omitempty"`
	ScopeHistory           []ProjectProgressHistoryPoint `json:"scopeHistory,omitempty"`
	CompletedScopeHistory  []ProjectProgressHistoryPoint `json:"completedScopeHistory,omitempty"`
	InProgressScopeHistory []ProjectProgressHistoryPoint `json:"inProgressScopeHistory,omitempty"`
	ProgressHistory        []ProjectProgressHistoryPoint `json:"progressHistory,omitempty"`
}

type ProjectDescriptionRevision struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	Description string    `json:"description"`
	Author      User      `json:"author"`
	CreatedAt   time.Time `json:"createdAt"`
}

type ProjectResource struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"projectId"`
	Type          string    `json:"type"`
	Title         string    `json:"title"`
	URL           string    `json:"url"`
	PinnedTeamIDs []string  `json:"pinnedTeamIds"`
	CreatorID     string    `json:"creatorId,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type ProjectMilestone struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"projectId"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	TargetDate  *string `json:"targetDate,omitempty"`
	// DescriptionRevisions holds earlier descriptions, newest first, for the
	// milestone's "Show description history" dialog.
	DescriptionRevisions []ProjectDescriptionRevision `json:"descriptionRevisions,omitempty"`
	CreatedAt            time.Time                    `json:"createdAt"`
	UpdatedAt            time.Time                    `json:"updatedAt"`
}

// ProjectUpdate mirrors the update stream shown on a Flow project. Updates
// belong to the project aggregate so they persist alongside project metadata.
type ProjectUpdate struct {
	ID          string              `json:"id"`
	ProjectID   string              `json:"projectId"`
	Body        string              `json:"body"`
	BodyData    map[string]any      `json:"bodyData,omitempty"`
	Health      string              `json:"health"`
	CreatedAt   time.Time           `json:"createdAt"`
	EditedAt    *time.Time          `json:"editedAt,omitempty"`
	User        User                `json:"user"`
	Comments    []Comment           `json:"comments"`
	Reactions   map[string][]string `json:"reactions"`
	Attachments []Attachment        `json:"attachments"`
	DueAt       *time.Time          `json:"dueAt,omitempty"`
	Missing     bool                `json:"missing,omitempty"`
	// Snapshot records the project's status, priority, lead, dates and
	// milestone progress when the update was posted; Diff compares it with
	// the previous update's snapshot. Updates posted before snapshots were
	// recorded have neither.
	Snapshot *PulseSnapshot `json:"snapshot,omitempty"`
	Diff     *PulseDiff     `json:"diff,omitempty"`
}

type CustomerRequest struct {
	ID          string       `json:"id"`
	CustomerID  string       `json:"customerId"`
	Body        string       `json:"body"`
	Source      string       `json:"source"`
	SourceURL   string       `json:"sourceUrl,omitempty"`
	Creator     User         `json:"creator"`
	IssueID     string       `json:"issueId,omitempty"`
	ProjectID   string       `json:"projectId,omitempty"`
	Attachments []Attachment `json:"attachments"`
	Priority    float64      `json:"priority,omitempty"`
	ArchivedAt  *time.Time   `json:"archivedAt,omitempty"`
	// ArchivedByExclusion is the excluded domain/email entry that archived
	// this request; removing that entry from the exclusion list restores it.
	ArchivedByExclusion string    `json:"archivedByExclusion,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type Release struct {
	ID             string            `json:"id"`
	SlugID         string            `json:"slugId"`
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	Description    string            `json:"description"`
	Status         string            `json:"status"`
	PipelineID     string            `json:"pipelineId,omitempty"`
	Stage          string            `json:"stage,omitempty"`
	CommitSHA      string            `json:"commitSha,omitempty"`
	ReleaseNotes   string            `json:"releaseNotes,omitempty"`
	Position       float64           `json:"position"`
	StartDate      *string           `json:"startDate,omitempty"`
	TargetDate     *string           `json:"targetDate,omitempty"`
	ProjectIDs     []string          `json:"projectIds"`
	IssueIDs       []string          `json:"issueIds"`
	IssueCount     int               `json:"issueCount,omitempty"`
	CompletedCount int               `json:"completedCount,omitempty"`
	SubscriberIDs  []string          `json:"subscriberIds"`
	Resources      []ReleaseResource `json:"resources"`
	Creator        User              `json:"creator"`
	StartedAt      *time.Time        `json:"startedAt,omitempty"`
	ReleasedAt     *time.Time        `json:"releasedAt,omitempty"`
	StageFrozenAt  *time.Time        `json:"stageFrozenAt,omitempty"`
	ArchivedAt     *time.Time        `json:"archivedAt,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

type ReleaseResource struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	URL        string `json:"url,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	// Content holds markdown for documents attached by CI (the --document option).
	Content string `json:"content,omitempty"`
	// Source names the integration that owns a synced link ("gitlab" for a
	// GitLab Release Hook). Empty for links added by hand or by the CI API;
	// only owned links are reconciled on later deliveries.
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type Ask struct {
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Body       string        `json:"body"`
	Source     string        `json:"source"`
	Requester  User          `json:"requester"`
	TeamID     string        `json:"teamId,omitempty"`
	TemplateID string        `json:"templateId,omitempty"`
	Status     string        `json:"status"`
	IssueID    string        `json:"issueId,omitempty"`
	Approvals  []AskApproval `json:"approvals"`
	CreatedAt  time.Time     `json:"createdAt"`
	UpdatedAt  time.Time     `json:"updatedAt"`
}

type AskApproval struct {
	ID        string     `json:"id"`
	AskID     string     `json:"askId"`
	Approver  User       `json:"approver"`
	Decision  string     `json:"decision"`
	Note      string     `json:"note,omitempty"`
	DecidedAt *time.Time `json:"decidedAt,omitempty"`
}

type Loop struct {
	ID string `json:"id"`
	// SlugID is the loop's URL slug, `<name-slug>-<12 hex>` (see LoopSlugID);
	// it is derived whenever a loop is served, so it always follows the name.
	SlugID                     string           `json:"slugId,omitempty"`
	Name                       string           `json:"name"`
	Description                string           `json:"description"`
	Status                     string           `json:"status"` // draft | published; loops stored before drafts are published
	TemplateID                 string           `json:"templateId,omitempty"`
	SourcePrompt               string           `json:"sourcePrompt,omitempty"`
	TeamID                     string           `json:"teamId,omitempty"`
	Icon                       string           `json:"icon,omitempty"`
	Color                      string           `json:"color,omitempty"`
	Level                      string           `json:"level"`
	TriggerType                string           `json:"triggerType"`
	TriggerConfig              map[string]any   `json:"triggerConfig,omitempty"`
	Instructions               string           `json:"instructions"`
	InstructionsData           map[string]any   `json:"instructionsData,omitempty"`
	ConnectorIDs               []string         `json:"connectorIds"`
	TeamAccess                 string           `json:"teamAccess"`
	AllowChangesOutsideTrigger bool             `json:"allowChangesOutsideTrigger"`
	AllowExternalSync          bool             `json:"allowExternalSync"`
	WebSearch                  bool             `json:"webSearch"`
	CodeAccess                 string           `json:"codeAccess"` // disabled | read | readWrite
	Enabled                    bool             `json:"enabled"`
	OwnerID                    string           `json:"ownerId,omitempty"`
	EditPolicy                 string           `json:"editPolicy,omitempty"` // "Who can edit": all (default) | teamOwners | owner
	TrustedSourceKeys          []string         `json:"trustedSourceKeys,omitempty"`
	Creator                    User             `json:"creator"`
	LastRunAt                  *time.Time       `json:"lastRunAt,omitempty"`
	NextRunAt                  *time.Time       `json:"nextRunAt,omitempty"`
	RunCount30d                int              `json:"runCount30d"`
	CreatedAt                  time.Time        `json:"createdAt"`
	UpdatedAt                  time.Time        `json:"updatedAt"`
	PublishedAt                *time.Time       `json:"publishedAt,omitempty"`
	Attachments                []LoopAttachment `json:"attachments,omitempty"`
	Version                    int              `json:"version,omitempty"`
	VersionID                  string           `json:"versionId,omitempty"`
	// ExpectedOutputs are what a successful run must produce: statusUpdate,
	// issue, comment, document or change (any Flow change); ["none"] when a
	// run may legitimately change nothing. Empty means inferred from the
	// instructions (see EffectiveOutputs on the run).
	ExpectedOutputs []string `json:"expectedOutputs,omitempty"`
	// OutputContract is what the loop's runs produce: read from the name and
	// instructions by the model (source model), set by a person in the
	// editor (source person, authoritative), or the conservative report-only
	// contract used when it could not be determined (source fallback).
	OutputContract *LoopOutputContract `json:"outputContract,omitempty"`
}

// LoopOutputContract says, for each output kind, what a loop's runs make:
// the operation, its target, how many, when, and the instruction text it
// was read from. Runs are prompted from it, their write tools are checked
// against it, and acceptance verifies its required outputs exist.
type LoopOutputContract struct {
	// Source: model | person | fallback.
	Source string `json:"source"`
	// InstructionsHash identifies the name, instructions and extraction
	// prompt a model contract was read from; a different hash is stale.
	InstructionsHash string `json:"instructionsHash,omitempty"`
	// ReadOnly: the run's reply (finish_run summary) is its only output;
	// every Flow and connector write is refused.
	ReadOnly         bool   `json:"readOnly"`
	ReadOnlyEvidence string `json:"readOnlyEvidence,omitempty"`
	// Outputs are the outputs the instructions name, one entry per kind
	// and operation.
	Outputs []LoopContractOutput `json:"outputs"`
	// Always: required outputs are due even when nothing changed.
	Always         bool   `json:"always,omitempty"`
	AlwaysEvidence string `json:"alwaysEvidence,omitempty"`
	// SkipConditions are the instructions' conditions for skipping a run.
	SkipConditions []LoopContractCondition `json:"skipConditions,omitempty"`
	// Model and ExtractedAt describe a model extraction; Error says why a
	// fallback contract was used.
	Model       string     `json:"model,omitempty"`
	ExtractedAt *time.Time `json:"extractedAt,omitempty"`
	Error       string     `json:"error,omitempty"`
	// UpdatedBy is the person who set a person contract.
	UpdatedBy string `json:"updatedBy,omitempty"`
	// Stale is computed when a loop is served: the name or instructions
	// changed since the contract was read or set.
	Stale bool `json:"stale,omitempty"`
}

// LoopContractOutput is one output of a loop's contract.
type LoopContractOutput struct {
	// Kind: document | issue | comment | statusUpdate | change (other Flow
	// changes such as issue properties).
	Kind string `json:"kind"`
	// Operation: create | update | append | write (create or update) |
	// read (only read) | forbidden.
	Operation string `json:"operation"`
	// Required: a run must produce it (create, update, append, write).
	Required bool               `json:"required"`
	Target   LoopContractTarget `json:"target"`
	// Cardinality: onePerRun | perItem | any.
	Cardinality string `json:"cardinality,omitempty"`
	// Conditions under which the output is made ("only for bugs without
	// repro steps"); an output with conditions is usually not required.
	Conditions []string `json:"conditions,omitempty"`
	// Group: outputs sharing a group are alternatives; one is enough.
	Group string `json:"group,omitempty"`
	// Evidence quotes the instruction text; Reasoning says why.
	Evidence  string `json:"evidence,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

// LoopContractTarget is where an output goes.
type LoopContractTarget struct {
	// Mode: new (a new one each time) | existing (an existing one, named by
	// title, document, project or initiative) | triggering (the run's
	// triggering entity) | matching (issues in a set the instructions
	// describe, Match) | any.
	Mode string `json:"mode,omitempty"`
	// TitleTemplate is a new document's or issue's title, with {date},
	// {week}, {month}, {year}, {team} or {project} filled by the runtime and
	// the Placeholders the run fills.
	TitleTemplate string `json:"titleTemplate,omitempty"`
	// Placeholders are the title template's placeholders the run's model
	// fills ("{topic}"), each with what it is filled from.
	Placeholders []LoopContractPlaceholder `json:"placeholders,omitempty"`
	// Title and DocumentID name an existing document; a person picks the
	// document by id in the editor.
	Title      string `json:"title,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	// Ambiguous: the instructions refer to an existing document without
	// saying which ("the team doc"); until a person picks it (DocumentID),
	// runs write to no existing document for the output.
	Ambiguous bool `json:"ambiguous,omitempty"`
	// Team is the team named in the instructions; TeamID the resolved one.
	Team   string `json:"team,omitempty"`
	TeamID string `json:"teamId,omitempty"`
	// Project and Initiative name the project or initiative an output is
	// on (a comment or status update) or in (a new issue); ProjectID and
	// InitiativeID are the resolved or picked ones.
	Project      string `json:"project,omitempty"`
	ProjectID    string `json:"projectId,omitempty"`
	Initiative   string `json:"initiative,omitempty"`
	InitiativeID string `json:"initiativeId,omitempty"`
	// Match describes the set of issues a matching target covers ("bugs
	// without repro steps").
	Match string `json:"match,omitempty"`
	// Description says the target in words.
	Description string `json:"description,omitempty"`
	// RenderedTitle is the title template for a run's period (on a run's
	// contract only).
	RenderedTitle string `json:"renderedTitle,omitempty"`
}

// LoopContractPlaceholder is a title placeholder the run's model fills.
type LoopContractPlaceholder struct {
	// Name is the placeholder without braces ("topic").
	Name string `json:"name"`
	// Source says what the run fills it from ("the error's subject").
	Source string `json:"source,omitempty"`
}

// LoopContractCondition is a condition quoted from the instructions.
type LoopContractCondition struct {
	Condition string `json:"condition"`
	Evidence  string `json:"evidence,omitempty"`
}

// LoopDefinition is the part of a loop a published version snapshots.
type LoopDefinition struct {
	Name                       string         `json:"name"`
	Description                string         `json:"description"`
	Icon                       string         `json:"icon,omitempty"`
	Color                      string         `json:"color,omitempty"`
	Level                      string         `json:"level"`
	TeamID                     string         `json:"teamId,omitempty"`
	TriggerType                string         `json:"triggerType"`
	TriggerConfig              map[string]any `json:"triggerConfig"`
	Instructions               string         `json:"instructions"`
	InstructionsData           map[string]any `json:"instructionsData,omitempty"`
	ConnectorIDs               []string       `json:"connectorIds"`
	TeamAccess                 string         `json:"teamAccess"`
	AllowChangesOutsideTrigger bool           `json:"allowChangesOutsideTrigger"`
	AllowExternalSync          bool           `json:"allowExternalSync"`
	WebSearch                  bool           `json:"webSearch"`
	CodeAccess                 string         `json:"codeAccess"`
}

// LoopVersion is an immutable snapshot of a published loop definition.
type LoopVersion struct {
	ID                  string         `json:"id"`
	LoopID              string         `json:"loopId"`
	Version             int            `json:"version"`
	PublishedAt         time.Time      `json:"publishedAt"`
	PublishedBy         User           `json:"publishedBy"`
	ChangeSummary       []string       `json:"changeSummary"`
	RestoredFromVersion int            `json:"restoredFromVersion,omitempty"`
	Current             bool           `json:"current"`
	Definition          LoopDefinition `json:"definition"`
	// OutputContract is the contract read for this version's instructions.
	OutputContract *LoopOutputContract `json:"outputContract,omitempty"`
}

// LoopAttachment is a file uploaded for the "Ask Flow to build the loop"
// prompt; it moves onto the loop once a draft is created with it.
type LoopAttachment struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"contentType"`
	Size        int64     `json:"size"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"createdAt"`
	CreatorID   string    `json:"creatorId,omitempty"`
	LoopID      string    `json:"loopId,omitempty"`
}

// LoopRunFeedback is one user's thumbs up/down on a run.
type LoopRunFeedback struct {
	UserID  string    `json:"userId"`
	Rating  string    `json:"rating"` // up | down
	Comment string    `json:"comment,omitempty"`
	At      time.Time `json:"at"`
}

type LoopRunFeedbackCounts struct {
	Up   int `json:"up"`
	Down int `json:"down"`
}

// LoopRun records one execution of a loop by the agent runtime.
type LoopRun struct {
	ID     string `json:"id"`
	LoopID string `json:"loopId"`
	// running | completed | needs_review | failed | cancelled | interrupted
	Status       string `json:"status"`
	Trigger      string `json:"trigger"` // manual | schedule | event
	TriggerLabel string `json:"triggerLabel,omitempty"`
	// TriggerReason is why an event fired the run, as a stable code (created,
	// updated, comment, customerRequest, triage, status, statusChanged,
	// priority, assignee, agent, project, team, label, update, started,
	// completed); TriggerValue is the property's new value, empty when it was
	// cleared. Clients translate them; TriggerLabel stays the English label.
	TriggerReason string `json:"triggerReason,omitempty"`
	TriggerValue  string `json:"triggerValue,omitempty"`
	// Occurrence is the schedule occurrence a scheduled loop's run is for —
	// its date in the loop's timezone (with the hour for hourly schedules) —
	// whether the schedule or a person started it; empty for event runs. A
	// loop appends to a document once per occurrence.
	Occurrence       string            `json:"occurrence,omitempty"`
	EventType        string            `json:"eventType,omitempty"`
	EntityType       string            `json:"entityType,omitempty"`
	EntityID         string            `json:"entityId,omitempty"`
	EntityIdentifier string            `json:"entityIdentifier,omitempty"`
	ActorID          string            `json:"actorId,omitempty"`
	Output           string            `json:"output,omitempty"`
	Steps            []LoopRunStep     `json:"steps,omitempty"`
	ToolCalls        []LoopRunToolCall `json:"toolCalls,omitempty"`
	Error            string            `json:"error,omitempty"`
	Notices          []string          `json:"notices,omitempty"`
	VersionID        string            `json:"versionId,omitempty"`
	Version          int               `json:"version,omitempty"`
	Feedback         []LoopRunFeedback `json:"feedback,omitempty"`
	// Replies continue the run's agent conversation from the run page.
	Replies []LoopRunReply `json:"replies,omitempty"`
	// FailureReason classifies a run that did not complete: cancelled,
	// interrupted, timeout, provider_timeout, provider_error, empty_response,
	// tool_error, budget_exhausted, no_output, incomplete, needs_target (the
	// output's existing document is not chosen) or unavailable.
	FailureReason string `json:"failureReason,omitempty"`
	// Summary is the agent's structured final report (the finish_run tool).
	Summary *LoopRunSummary `json:"summary,omitempty"`
	// ExpectedOutputs are the outputs the run was checked for; Produced
	// counts the successful changes it made by kind.
	ExpectedOutputs []string       `json:"expectedOutputs,omitempty"`
	Produced        map[string]int `json:"produced,omitempty"`
	// OutputContract is the loop's output contract the run used, with its
	// targets resolved for the run (titles rendered, teams found).
	OutputContract *LoopOutputContract `json:"outputContract,omitempty"`
	// Retries counts provider and read-only tool calls that were retried.
	Retries     int        `json:"retries,omitempty"`
	CancelledBy string     `json:"cancelledBy,omitempty"`
	HeartbeatAt *time.Time `json:"heartbeatAt,omitempty"`
	// Viewer fields are computed per request and never stored.
	ViewerRating   *string                `json:"viewerRating"`
	ViewerComment  string                 `json:"viewerComment,omitempty"`
	FeedbackCounts *LoopRunFeedbackCounts `json:"feedbackCounts,omitempty"`
	StartedAt      time.Time              `json:"startedAt"`
	FinishedAt     *time.Time             `json:"finishedAt,omitempty"`
}

// LoopRunReply is a follow-up message on a run and the agent's answer to it.
type LoopRunReply struct {
	ID            string            `json:"id"`
	UserID        string            `json:"userId"`
	Body          string            `json:"body"`
	Status        string            `json:"status"` // running | completed | failed | cancelled | interrupted
	Output        string            `json:"output,omitempty"`
	Steps         []LoopRunStep     `json:"steps,omitempty"`
	ToolCalls     []LoopRunToolCall `json:"toolCalls,omitempty"`
	FailureReason string            `json:"failureReason,omitempty"`
	Error         string            `json:"error,omitempty"`
	CreatedAt     time.Time         `json:"createdAt"`
	FinishedAt    *time.Time        `json:"finishedAt,omitempty"`
	// SkillIDs are the replier's skills applied to the answer.
	SkillIDs []string `json:"skillIds,omitempty"`
	// Attachments describe the files sent with the reply; their contents
	// went to the agent with that turn and are not kept.
	Attachments []LoopRunReplyAttachment `json:"attachments,omitempty"`
}

// LoopRunSummary is the structured report a loop run ends with.
type LoopRunSummary struct {
	// done | incomplete | nothing_to_do | skipped
	Status  string   `json:"status"`
	Summary string   `json:"summary"`
	Done    []string `json:"done,omitempty"`
	NotDone []string `json:"notDone,omitempty"`
	// SkipReason names the skip condition of the instructions that applied
	// (status skipped).
	SkipReason string `json:"skipReason,omitempty"`
}

// LoopRunReplyAttachment is a file sent with a run reply.
type LoopRunReplyAttachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

// LoopRunStep is a progress note (report_progress) the agent made during a run.
type LoopRunStep struct {
	Order   int       `json:"order"`
	Title   string    `json:"title"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

type LoopRunToolCall struct {
	Order int    `json:"order"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// Connector and RemoteTool name a connector tool call, whose Name is only an opaque id ("external_<hash>"):
	// the connector's display name and the tool's own name there. Runs stored before they existed lack both.
	Connector  string     `json:"connector,omitempty"`
	RemoteTool string     `json:"remoteTool,omitempty"`
	Args       string     `json:"args,omitempty"`
	Status     string     `json:"status"` // running | completed | error | blocked
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	// Document is what a save_document call did to the stored document.
	Document *LoopRunDocumentWrite `json:"document,omitempty"`
	// Output is what an issue, comment or status update write was for.
	Output *LoopRunOutputWrite `json:"output,omitempty"`
}

// LoopRunOutputWrite records a loop run's issue, comment or status update
// write as checked against the run's output contract: the item, where it
// went, and the contract outputs whose target it is on (only those count as
// produced).
type LoopRunOutputWrite struct {
	// Kind: issue | comment | statusUpdate; Operation: create | update.
	Kind      string `json:"kind"`
	Operation string `json:"operation"`
	// ID is the issue, comment or status update made or changed.
	ID string `json:"id,omitempty"`
	// ParentType and ParentID are what a comment or status update is on
	// (issue, project, initiative, document) or the issue changed.
	ParentType string `json:"parentType,omitempty"`
	ParentID   string `json:"parentId,omitempty"`
	// Title is a new issue's title.
	Title string `json:"title,omitempty"`
	// Outputs are the indexes of the run contract's outputs the write is on
	// target for.
	Outputs []int `json:"outputs,omitempty"`
}

// LoopRunDocumentWrite records a loop run's document write as the stored
// document shows it, so acceptance checks that the change happened and a
// repeated append is not written twice.
type LoopRunDocumentWrite struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	// TeamIDs are the document's teams after the write.
	TeamIDs []string `json:"teamIds,omitempty"`
	// create | update | append
	Mode          string `json:"mode"`
	VersionBefore int64  `json:"versionBefore,omitempty"`
	VersionAfter  int64  `json:"versionAfter,omitempty"`
	// Changed: the document's title or content differs after the write (a
	// new document always counts).
	Changed bool `json:"changed"`
	// Key identifies an append (document and text) within the run.
	Key string `json:"key,omitempty"`
	// Occurrence is the run's occurrence an append was made for.
	Occurrence string `json:"occurrence,omitempty"`
	// ExistingRunID: an earlier run of the loop already appended to the
	// document for this occurrence, so the append was refused; the
	// occurrence's output exists.
	ExistingRunID string `json:"existingRunId,omitempty"`
}

type ProjectTemplate struct {
	ID                  string              `json:"id"`
	Name                string              `json:"name"`
	ProjectName         string              `json:"projectName,omitempty"`
	TemplateDescription string              `json:"templateDescription,omitempty"`
	Description         string              `json:"description,omitempty"`
	Summary             string              `json:"summary,omitempty"`
	Icon                string              `json:"icon,omitempty"`
	Color               string              `json:"color,omitempty"`
	StatusID            string              `json:"statusId,omitempty"`
	Priority            int                 `json:"priority"`
	TeamIDs             []string            `json:"teamIds"`
	LabelIDs            []string            `json:"labelIds"`
	LeadID              string              `json:"leadId,omitempty"`
	MemberIDs           []string            `json:"memberIds,omitempty"`
	DependencyIDs       []string            `json:"dependencyIds,omitempty"`
	InitiativeIDs       []string            `json:"initiativeIds,omitempty"`
	IssueIDs            []string            `json:"issueIds,omitempty"`
	Milestones          []TemplateMilestone `json:"milestones,omitempty"`
	// Issues are created in every project made from the template.
	Issues []ProjectTemplateIssue `json:"issues,omitempty"`
	// DependencyRelations preset the new project's "blocked by" and "blocking"
	// relations. When set, DependencyIDs mirrors its blocked_by projects.
	DependencyRelations []ProjectDependencyRelationInput `json:"dependencyRelations,omitempty"`
	Visibility          string                           `json:"visibility,omitempty"`
	VisibilityTeamID    string                           `json:"visibilityTeamId,omitempty"`
	Creator             User                             `json:"creator"`
	CreatedAt           time.Time                        `json:"createdAt"`
	UpdatedAt           time.Time                        `json:"updatedAt"`
}

// ProjectTemplateIssue is an issue a project template creates in each new
// project. MilestoneID names one of the template's milestones and ParentID
// another issue of the same template (a sub-issue).
type ProjectTemplateIssue struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	TeamID      string   `json:"teamId,omitempty"`
	Priority    int      `json:"priority"`
	AssigneeID  string   `json:"assigneeId,omitempty"`
	LabelIDs    []string `json:"labelIds,omitempty"`
	MilestoneID string   `json:"milestoneId,omitempty"`
	ParentID    string   `json:"parentId,omitempty"`
}

type TemplateMilestone struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type UserSettings struct {
	UserID                   string `json:"userId"`
	Language                 string `json:"language"`
	HomeView                 string `json:"homeView"`
	DisplayNames             string `json:"displayNames"`
	FirstDay                 string `json:"firstDay"`
	Emoticons                bool   `json:"emoticons"`
	SendComments             string `json:"sendComments"`
	FontSize                 string `json:"fontSize"`
	PointerCursor            bool   `json:"pointerCursor"`
	UnderlineLinks           bool   `json:"underlineLinks"`
	DisableAnimatedImages    bool   `json:"disableAnimatedImages"`
	InterfaceTheme           string `json:"interfaceTheme"`
	LightTheme               string `json:"lightTheme"`
	DarkTheme                string `json:"darkTheme"`
	DesktopLinks             bool   `json:"desktopLinks"`
	AutoAssign               bool   `json:"autoAssign"`
	AssignStarted            bool   `json:"assignStarted"`
	ReviewAutoAssign         bool   `json:"reviewAutoAssign"`
	BranchFormat             string `json:"branchFormat"`
	PersonalSettingsVersion  int    `json:"personalSettingsVersion"`
	CodeReviewsEnabled       bool   `json:"codeReviewsEnabled"`
	AutoConvertDrafts        bool   `json:"autoConvertDrafts"`
	MergeStrategy            string `json:"mergeStrategy"`
	CodeTheme                string `json:"codeTheme"`
	CodeFont                 string `json:"codeFont"`
	ReviewCommentsFilter     string `json:"reviewCommentsFilter"`
	ReviewRequests           bool   `json:"reviewRequests"`
	GithubTeamReviewRequests bool   `json:"githubTeamReviewRequests"`
	ChecksMergeQueue         bool   `json:"checksMergeQueue"`
	RequireSignedCommits     bool   `json:"requireSignedCommits"`
	// CommitSigningKey contains only non-sensitive metadata. The private key is
	// accepted once by the account API, validated, and never persisted or
	// returned in workspace state.
	CommitSigningKey      *CommitSigningKey `json:"commitSigningKey,omitempty"`
	GitAttachmentFormat   string            `json:"gitAttachmentFormat"`
	GitBranchMoveStarted  bool              `json:"gitBranchMoveStarted"`
	CodingToolMoveStarted bool              `json:"codingToolMoveStarted"`
	// Coding tools offered in an issue's "Work on issue" menu.
	EnabledCodingTools        []string `json:"enabledCodingTools,omitempty"`
	CustomDeepLinkURLTemplate string   `json:"customDeepLinkUrlTemplate,omitempty"`
	CodingPromptTemplate      string   `json:"codingPromptTemplate,omitempty"`
	ChangelogUpdates          bool     `json:"changelogUpdates"`
	ChangelogNewsletter       bool     `json:"changelogNewsletter"`
	MarketingUpdates          bool     `json:"marketingUpdates"`
	InviteAcceptedUpdates     bool     `json:"inviteAcceptedUpdates"`
	PrivacyUpdates            bool     `json:"privacyUpdates"`
	DPAUpdates                bool     `json:"dpaUpdates"`
	AgentEnabled              bool     `json:"agentEnabled"`
	AgentInstructions         string   `json:"agentInstructions"`
	PulseSchedule             string   `json:"pulseSchedule"`
	PulseWelcomeDismissed     bool     `json:"pulseWelcomeDismissed,omitempty"`
	FeedLastSeenTime          string   `json:"feedLastSeenTime,omitempty"`
	// Timezone is the IANA zone reported by the user's browser. Pulse
	// summaries are delivered at 06:00 in this zone.
	Timezone  string    `json:"timezone,omitempty"`
	JobTitle  string    `json:"jobTitle,omitempty"`
	Username  string    `json:"username,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CommitSigningKey describes the key used by coding sessions to sign commits.
// Private material is intentionally absent from this model so it cannot leak
// through bootstrap/settings responses or event payloads.
type CommitSigningKey struct {
	Name        string    `json:"name"`
	Fingerprint string    `json:"fingerprint"`
	Type        string    `json:"type"`
	AddedAt     time.Time `json:"addedAt"`
}

// CodingAgentSettings holds workspace-wide coding-agent policy (LS-0125).
// CodingAgentEnvironment is a reusable coding-session environment (LS-0117).
type CodingAgentEnvironment struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Repository   string `json:"repository"`
	SetupCommand string `json:"setupCommand,omitempty"`
	Archived     bool   `json:"archived,omitempty"`
}

// TeamAgentConnector is a team-scoped MCP connector entry (LS-0572).
type TeamAgentConnector struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

type CodingAgentSettings struct {
	// CommitSigningEnabled is "Require signed commits".
	CommitSigningEnabled bool `json:"commitSigningEnabled"`
	// Harness is codex (default) | claudeCode | openSource | pi; the legacy values
	// auto and claude are read as codex and claudeCode.
	Harness string `json:"harness,omitempty"`
	// Model is auto (default) or a model id from the coding model catalog.
	Model string `json:"model,omitempty"`
	// Environments is the legacy config-only list; it is migrated into the
	// coding_environments table at startup and no longer written.
	Environments []CodingAgentEnvironment `json:"environments,omitempty"`
	// ReasoningEffort is "" (model default) | low | medium | high.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// PinnedRegion pins sandboxes to one FLOW_CODING_REGIONS entry; "" = any.
	PinnedRegion string `json:"pinnedRegion,omitempty"`
	// TrustedExternalSources lists external sources (e.g. "slack", "intercom",
	// "github:<org>") whose issues may start sessions without member approval.
	TrustedExternalSources []string `json:"trustedExternalSources,omitempty"`
	// LoopRepositoryAccess is "none" (default) | "all" | "selected";
	// LoopRepositories lists environment ids Loops may start sessions on.
	LoopRepositoryAccess string   `json:"loopRepositoryAccess,omitempty"`
	LoopRepositories     []string `json:"loopRepositories,omitempty"`
}

type WorkspaceSettings struct {
	FiscalMonth              string              `json:"fiscalMonth"`
	WelcomeMessage           string              `json:"welcomeMessage,omitempty"`
	WelcomeMessageEnabled    bool                `json:"welcomeMessageEnabled"`
	WelcomeMessageTitle      string              `json:"welcomeMessageTitle,omitempty"`
	WelcomeMessageEditedByID string              `json:"welcomeMessageEditedById,omitempty"`
	WelcomeMessageEditedAt   *time.Time          `json:"welcomeMessageEditedAt,omitempty"`
	DefaultHomeView          string              `json:"defaultHomeView,omitempty"`
	GuestsAllowed            bool                `json:"guestsAllowed"`
	RequireTwoFactor         bool                `json:"requireTwoFactor"`
	SessionDurationDays      int                 `json:"sessionDurationDays"`
	AllowedDomains           []string            `json:"allowedDomains"`
	InvitePermission         string              `json:"invitePermission"`
	TeamCreatePermission     string              `json:"teamCreatePermission"`
	LabelPermission          string              `json:"labelPermission"`
	TemplatePermission       string              `json:"templatePermission"`
	APIKeyPermission         string              `json:"apiKeyPermission"`
	FeatureFlags             map[string]bool     `json:"featureFlags"`
	FeatureSettings          FeatureSettings     `json:"featureSettings"`
	CodingAgentSettings      CodingAgentSettings `json:"codingAgentSettings"`
	InviteLinksEnabled       bool                `json:"inviteLinksEnabled"`
	GoogleAuthEnabled        bool                `json:"googleAuthEnabled"`
	EmailAuthEnabled         bool                `json:"emailAuthEnabled"`
	// AllowedAuthServices lists login methods for other domains (google, email, passkey, saml, appUser).
	AllowedAuthServices  []string `json:"allowedAuthServices,omitempty"`
	DisableAdminBypass   bool     `json:"disableAdminBypass"`
	InitiativePermission string   `json:"initiativePermission,omitempty"`
	LoopPermission       string   `json:"loopPermission,omitempty"`
	ImportPermission     string   `json:"importPermission,omitempty"`
	// SpendLimits are the AI spend limits of Settings > Usage & limits.
	SpendLimits             SpendLimitSettings `json:"spendLimits,omitempty"`
	PinnedViewPermission    string             `json:"pinnedViewPermission,omitempty"`
	AgentGuidancePermission string             `json:"agentGuidancePermission,omitempty"`
	AgentInstructions       string             `json:"agentInstructions,omitempty"`
	// AgentInstructionsEditedByID and AgentInstructionsEditedAt say who last changed the guidance and when.
	AgentInstructionsEditedByID string     `json:"agentInstructionsEditedById,omitempty"`
	AgentInstructionsEditedAt   *time.Time `json:"agentInstructionsEditedAt,omitempty"`
	PreventGuestAgents          bool       `json:"preventGuestAgents"`
	// AgentWebSearch is the older opt-in flag (off by default before web access became default-on). It is kept in
	// step with the effective setting for older clients and no longer decides it.
	AgentWebSearch bool `json:"agentWebSearch"`
	// AgentWebAccess is Flow Agent's web access as an admin set it: nil when never set (web access is on, like
	// The reference app's agent), or the explicit choice. AgentWebAccessEnabled resolves it.
	AgentWebAccess *bool `json:"agentWebAccess,omitempty"`
	// AgentWebSearchDisabled mirrors !AgentWebAccessEnabled() for older clients; true always meant an admin turned
	// web access off.
	AgentWebSearchDisabled bool `json:"agentWebSearchDisabled"`
	ExternalLoopTriggers   bool `json:"externalLoopTriggers"`
	// MCPConnectors is the admin's explicit choice for Flow Agent's MCP connectors: nil when never set (they are on,
	// like the reference app), or the choice. MCPConnectorsActive resolves it.
	MCPConnectors *bool `json:"mcpConnectors,omitempty"`
	// MCPConnectorsEnabled mirrors the resolved setting (ignoring HIPAA mode) for older clients.
	MCPConnectorsEnabled         bool              `json:"mcpConnectorsEnabled"`
	ReviewThirdPartyApplications bool              `json:"reviewThirdPartyApplications"`
	AllowedMCPConnectors         string            `json:"allowedMcpConnectors,omitempty"`
	RestrictFileUploads          bool              `json:"restrictFileUploads"`
	AllowedFileExtensions        []string          `json:"allowedFileExtensions,omitempty"`
	ReduceSupportPersonalInfo    bool              `json:"reduceSupportPersonalInfo"`
	HIPAACompliance              bool              `json:"hipaaCompliance"`
	SCIMEnabled                  bool              `json:"scimEnabled,omitempty"`
	SCIMRoleClaim                string            `json:"scimRoleClaim,omitempty"`
	SCIMRoleMapping              map[string]string `json:"scimRoleMapping,omitempty"`
	// SCIMRoleGroups maps workspace roles (owner/admin/member/guest) to the
	// IdP-managed group display names used for role provisioning. Group
	// membership updates are applied in push order, with the latest push
	// taking precedence when a user belongs to multiple role groups.
	SCIMRoleGroups map[string]string `json:"scimRoleGroups,omitempty"`
	// SCIMTeamGroupMapping maps Flow team IDs to IdP group display names. A
	// matching SCIM group owns membership for that team.
	SCIMTeamGroupMapping map[string]string `json:"scimTeamGroupMapping,omitempty"`
	SCIMDefaultRole      string            `json:"scimDefaultRole,omitempty"`
	// SCIMDeprovisionAction is what a SCIM deprovision (DELETE, or active
	// false) does to the member: "suspend" (default) keeps them suspended in
	// the workspace; "remove" removes them from the workspace and its teams.
	// The account and its work are never deleted either way.
	SCIMDeprovisionAction string `json:"scimDeprovisionAction,omitempty"`
	// TrustedSourcesMode: "none" | "allowlist" (Wave 10 / LS-0094 foundations).
	TrustedSourcesMode      string    `json:"trustedSourcesMode,omitempty"`
	TrustedSourcesAllowlist []string  `json:"trustedSourcesAllowlist,omitempty"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

// SpendLimitSettings holds the workspace AI spend limits in USD (0 = no limit),
// how often each limit resets, when the periods roll over, and the per-member
// and per-loop overrides keyed by id.
type SpendLimitSettings struct {
	WorkspaceLimit  float64            `json:"workspaceLimit,omitempty"`
	WorkspaceReset  string             `json:"workspaceReset,omitempty"`
	UserLimit       float64            `json:"userLimit,omitempty"`
	UserReset       string             `json:"userReset,omitempty"`
	LoopLimit       float64            `json:"loopLimit,omitempty"`
	LoopReset       string             `json:"loopReset,omitempty"`
	DailyResetHour  *int               `json:"dailyResetHour,omitempty"`
	WeeklyResetDay  *int               `json:"weeklyResetDay,omitempty"`
	MonthlyResetDay *int               `json:"monthlyResetDay,omitempty"`
	UserOverrides   map[string]float64 `json:"userOverrides,omitempty"`
	LoopOverrides   map[string]float64 `json:"loopOverrides,omitempty"`
}

type FeatureOption struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type FeatureSettings struct {
	InitiativeUpdateSchedule       string                     `json:"initiativeUpdateSchedule"`
	InitiativeUpdateFrequencyWeeks int                        `json:"initiativeUpdateFrequencyWeeks,omitempty"`
	InitiativeUpdateWeekday        int                        `json:"initiativeUpdateWeekday,omitempty"`
	InitiativeUpdateHour           int                        `json:"initiativeUpdateHour,omitempty"`
	CustomerDefaultTeamID          string                     `json:"customerDefaultTeamId,omitempty"`
	CustomerRevenueFormat          string                     `json:"customerRevenueFormat"`
	CustomerRevenueCurrency        string                     `json:"customerRevenueCurrency"`
	CustomerManualEdits            bool                       `json:"customerManualEdits"`
	CustomerStatuses               []FeatureOption            `json:"customerStatuses"`
	CustomerTiers                  []FeatureOption            `json:"customerTiers"`
	CustomerExcludedDomains        []string                   `json:"customerExcludedDomains"`
	CustomerGenericDomains         []string                   `json:"customerGenericDomains"`
	PulseWorkspaceSchedule         string                     `json:"pulseWorkspaceSchedule"`
	AsksEmailAddresses             []string                   `json:"asksEmailAddresses"`
	AsksSlackChannels              []AsksSlackChannelMapping  `json:"asksSlackChannels,omitempty"`
	TriageIntelligence             TriageIntelligenceSettings `json:"triageIntelligence"`
	RepositoryAccess               *RepositoryAccessSettings  `json:"repositoryAccess,omitempty"`
	// InitiativeUpdateGuidance and ProjectUpdateGuidance are workspace-wide instructions for how Flow Agent writes
	// initiative and project updates (Settings > AI & agents > Flow Agent > Updates).
	InitiativeUpdateGuidance string `json:"initiativeUpdateGuidance,omitempty"`
	ProjectUpdateGuidance    string `json:"projectUpdateGuidance,omitempty"`
}

// RepositoryAccessSettings gates which connected repos Code Intelligence may use (LS-0110).
type RepositoryAccessSettings struct {
	ExtendAccessToAllMembers bool     `json:"extendAccessToAllMembers"`
	AllowAutomationAccess    bool     `json:"allowAutomationAccess"`
	Scope                    string   `json:"scope"`
	AllowedRepositories      []string `json:"allowedRepositories"`
}

// AsksSlackChannelMapping maps a Slack channel to a Flow team (and optional template) for Asks.
type AsksSlackChannelMapping struct {
	Channel    string `json:"channel"`
	TeamID     string `json:"teamId"`
	TemplateID string `json:"templateId,omitempty"`
	Enabled    bool   `json:"enabled"`
}

// TriageIntelligenceSettings controls how each inferred field is handled.
// "suggest" keeps the result visible, "auto" applies it immediately, and
// "hide" suppresses that suggestion class.
type TriageIntelligenceSettings struct {
	AssigneeAction    string `json:"assigneeAction"`
	ProjectAction     string `json:"projectAction"`
	LabelAction       string `json:"labelAction"`
	TeamAction        string `json:"teamAction"`
	DuplicateAction   string `json:"duplicateAction"`
	RelatedAction     string `json:"relatedAction"`
	WorkspaceGuidance string `json:"workspaceGuidance,omitempty"`
	// ValueRules override the assignee, project, label and team actions for one specific value (a user, project,
	// label or team): the filter button next to each of those rows.
	ValueRules []TriageValueRule `json:"valueRules,omitempty"`
}

// TriageValueRule sets what happens to suggestions of Kind (assignee, project, label or team) that point at Value
// (the user, project, label or team id): suggest, auto or hide.
type TriageValueRule struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Action string `json:"action"`
}

type ReleasePipeline struct {
	ID                          string            `json:"id"`
	SlugID                      string            `json:"slugId"`
	Name                        string            `json:"name"`
	TeamIDs                     []string          `json:"teamIds"`
	Type                        string            `json:"type"`
	Production                  bool              `json:"production"`
	Stages                      []string          `json:"stages"`
	StageStatuses               map[string]string `json:"stageStatuses"`
	StageColors                 map[string]string `json:"stageColors,omitempty"`
	FrozenStages                []string          `json:"frozenStages,omitempty"`
	Position                    float64           `json:"position"`
	PathFilters                 []string          `json:"pathFilters"`
	ReleaseNotesTemplate        string            `json:"releaseNotesTemplate,omitempty"`
	AutoGenerateReleaseNotes    bool              `json:"autoGenerateReleaseNotes"`
	MoveOpenIssuesToNextRelease *bool             `json:"moveOpenIssuesToNextRelease,omitempty"`
	AccessKeyPrefix             string            `json:"accessKeyPrefix,omitempty"`
	AccessKeyHash               string            `json:"accessKeyHash,omitempty"`
	AccessKeyCreatedAt          *time.Time        `json:"accessKeyCreatedAt,omitempty"`
	// AccessKeyLastUsedAt is when CI last authenticated with the current key.
	AccessKeyLastUsedAt *time.Time `json:"accessKeyLastUsedAt,omitempty"`
	// AccessKeyRevokedAt schedules (future) or records (past) the current key's
	// revocation's revokedAt on pipeline access keys.
	AccessKeyRevokedAt *time.Time `json:"accessKeyRevokedAt,omitempty"`
	// PreviousAccessKeyHash keeps a rotated key working until
	// PreviousAccessKeyExpiresAt so CI can be updated without downtime.
	PreviousAccessKeyHash      string     `json:"previousAccessKeyHash,omitempty"`
	PreviousAccessKeyExpiresAt *time.Time `json:"previousAccessKeyExpiresAt,omitempty"`
	CreatedAt                  time.Time  `json:"createdAt"`
	UpdatedAt                  time.Time  `json:"updatedAt"`
}

type CustomEmoji struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ImageURL   string     `json:"imageUrl"`
	Creator    User       `json:"creator"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type APIKey struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Prefix           string   `json:"prefix"`
	SecretHash       string   `json:"secretHash,omitempty"`
	CreatorID        string   `json:"creatorId"`
	SubjectUserID    string   `json:"subjectUserId,omitempty"`
	IssuedByUserID   string   `json:"issuedByUserId,omitempty"`
	Actor            string   `json:"actor,omitempty"`
	GrantType        string   `json:"grantType,omitempty"`
	ClientSecretHash string   `json:"clientSecretHash,omitempty"`
	Scopes           []string `json:"scopes"`
	TeamIDs          []string `json:"teamIds"`
	// TeamRestriction distinguishes an unrestricted key ("all") from a key
	// explicitly constrained to the selected TeamIDs ("selected"). Empty is
	// treated as "all" for backwards compatibility with existing records.
	TeamRestriction string     `json:"teamRestriction,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	LastUsedAt      *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt       *time.Time `json:"revokedAt,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	OAuthClientID   string     `json:"oauthClientId,omitempty"`
	AuthorizationID string     `json:"authorizationId,omitempty"`
	// RefreshFamilyID links an OAuth access token to its refresh-token
	// chain, so revoking a refresh token also revokes the access tokens
	// issued from that chain.
	RefreshFamilyID string `json:"refreshFamilyId,omitempty"`
}

// Passkey stores a WebAuthn credential owned by an account. CredentialJSON is
// an opaque serialized credential record and is never returned to clients.
type Passkey struct {
	ID             string     `json:"id"`
	UserID         string     `json:"userId"`
	Name           string     `json:"name"`
	CredentialJSON string     `json:"credentialJson,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	LastUsedAt     *time.Time `json:"lastUsedAt,omitempty"`
}

// PasskeyRegistrationChallenge is short-lived state binding a registration
// response to the authenticated account and browser origin.
type PasskeyRegistrationChallenge struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userId"`
	SessionJSON string    `json:"sessionJson"`
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type OAuthClient struct {
	ClientID                string    `json:"client_id"`
	ClientName              string    `json:"client_name"`
	AllowedScopes           []string  `json:"allowed_scopes,omitempty"`
	HomeWorkspaceKey        string    `json:"-"`
	PublicEnabled           bool      `json:"-"`
	ClientURI               string    `json:"client_uri,omitempty"`
	LogoURI                 string    `json:"logo_uri,omitempty"`
	RedirectURIs            []string  `json:"redirect_uris"`
	GrantTypes              []string  `json:"grant_types"`
	ResponseTypes           []string  `json:"response_types"`
	TokenEndpointAuthMethod string    `json:"token_endpoint_auth_method"`
	CreatedAt               time.Time `json:"created_at"`
}

type OAuthAuthorizationCode struct {
	Actor           string    `json:"actor,omitempty"`
	InstallerID     string    `json:"installerId,omitempty"`
	TeamIDs         []string  `json:"teamIds,omitempty"`
	AllPublicTeams  bool      `json:"allPublicTeams,omitempty"`
	ClientID        string    `json:"clientId"`
	WorkspaceKey    string    `json:"workspaceKey"`
	UserID          string    `json:"userId"`
	RedirectURI     string    `json:"redirectUri"`
	Scopes          []string  `json:"scopes"`
	CodeChallenge   string    `json:"codeChallenge"`
	AuthorizationID string    `json:"authorizationId"`
	ExpiresAt       time.Time `json:"expiresAt"`
	// ClientAuthRequired marks a confidential-client grant issued without
	// PKCE: redeeming it (and refreshing the resulting tokens) needs the
	// client secret. The refresh grant inherits it from the code's JSON.
	ClientAuthRequired bool `json:"clientAuthRequired,omitempty"`
	// Realtime records that the consent screen listed "Receive realtime
	// updates about your workspace" (the application has a webhook).
	Realtime bool `json:"realtime,omitempty"`
}

type OAuthRefreshGrant struct {
	Actor           string    `json:"actor,omitempty"`
	InstallerID     string    `json:"installerId,omitempty"`
	TeamIDs         []string  `json:"teamIds,omitempty"`
	AllPublicTeams  bool      `json:"allPublicTeams,omitempty"`
	ClientID        string    `json:"clientId"`
	WorkspaceKey    string    `json:"workspaceKey"`
	UserID          string    `json:"userId"`
	Scopes          []string  `json:"scopes"`
	AuthorizationID string    `json:"authorizationId"`
	ExpiresAt       time.Time `json:"expiresAt"`
	// ClientAuthRequired: see OAuthAuthorizationCode.
	ClientAuthRequired bool `json:"clientAuthRequired,omitempty"`
	// FamilyID identifies one refresh-token chain (one code exchange and
	// every rotation after it); access tokens carry it as RefreshFamilyID.
	FamilyID string `json:"familyId,omitempty"`
	// ReplayedRefreshToken is set (never stored) when a refresh request is a
	// replay inside the rotation grace window: the response repeats this
	// successor instead of the freshly generated one.
	ReplayedRefreshToken string `json:"-"`
}

type OAuthAuthorization struct {
	Actor       string     `json:"actor,omitempty"`
	InstallerID string     `json:"installerId,omitempty"`
	ID          string     `json:"id"`
	ClientID    string     `json:"clientId"`
	ClientName  string     `json:"clientName"`
	UserID      string     `json:"userId"`
	Scopes      []string   `json:"scopes"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
}

type OAuthApplication struct {
	ID                        string    `json:"id"`
	Name                      string    `json:"name"`
	Description               string    `json:"description,omitempty"`
	LogoURL                   string    `json:"logoUrl,omitempty"`
	ClientID                  string    `json:"clientId"`
	ClientSecret              string    `json:"clientSecret,omitempty"`
	RedirectURIs              []string  `json:"redirectUris"`
	Scopes                    []string  `json:"scopes"`
	SupportsClientCredentials bool      `json:"supportsClientCredentials"`
	CredentialsAuthorizedBy   string    `json:"credentialsAuthorizedBy,omitempty"`
	PublicEnabled             bool      `json:"publicEnabled"`
	CreatorID                 string    `json:"creatorId"`
	CreatedAt                 time.Time `json:"createdAt"`
	UpdatedAt                 time.Time `json:"updatedAt"`
	// DeveloperName and DeveloperURL are the public developer details from
	// the app manifest (developer.name, oauth.client_uri).
	DeveloperName string `json:"developerName,omitempty"`
	DeveloperURL  string `json:"developerUrl,omitempty"`
	// GitHubUsername associates GitHub activity with the application's app
	// user (actor=app): pull requests opened and events sent by this GitHub
	// account are attributed to the app user. "[bot]" names a GitHub App.
	GitHubUsername string `json:"githubUsername,omitempty"`
	// Application-level webhook, configured once by the developer: every
	// workspace that installs or authorizes the application gets a managed
	// webhook delivering these resource types to WebhookURL, signed with
	// the application's webhook signing secret.
	WebhookEnabled       bool     `json:"webhookEnabled,omitempty"`
	WebhookURL           string   `json:"webhookUrl,omitempty"`
	WebhookResourceTypes []string `json:"webhookResourceTypes,omitempty"`
	WebhookSecretPrefix  string   `json:"webhookSecretPrefix,omitempty"`
}

type Webhook struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	ApplicationID   string     `json:"applicationId,omitempty"`
	ResourceTypes   []string   `json:"resourceTypes"`
	TeamIDs         []string   `json:"teamIds"`
	TeamRestriction string     `json:"teamRestriction,omitempty"`
	Enabled         bool       `json:"enabled"`
	CreatorID       string     `json:"creatorId"`
	SecretHash      string     `json:"-"`
	SecretPrefix    string     `json:"secretPrefix,omitempty"`
	SecretRevokedAt *time.Time `json:"secretRevokedAt,omitempty"`
	// AuditLog marks the workspace's audit log stream. It receives audit
	// entries only, signed with a secret kept outside the workspace document.
	AuditLog bool `json:"auditLog,omitempty"`
	// Stream progress: the last delivered audit entry and retry state.
	AuditCursorAt    *time.Time `json:"auditCursorAt,omitempty"`
	AuditCursorID    string     `json:"auditCursorId,omitempty"`
	DeliveryAttempts int        `json:"deliveryAttempts,omitempty"`
	NextAttemptAt    *time.Time `json:"nextAttemptAt,omitempty"`
	FailingSince     *time.Time `json:"failingSince,omitempty"`
	DisabledReason   string     `json:"disabledReason,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	// PayloadVersion is the delivery envelope (WebhookPayloadVersion for new
	// webhooks; 0 means the legacy version 1 envelope).
	PayloadVersion int `json:"payloadVersion,omitempty"`
	// SigningEnabled is set once a signing secret is stored for the webhook.
	// Webhooks created before signing only kept a hash of their secret and
	// deliver unsigned until the secret is rotated.
	SigningEnabled bool `json:"signingEnabled,omitempty"`
	// ManagedBy is "application" for a webhook created by an OAuth
	// application's webhook settings when the application was installed;
	// it is removed when the application is uninstalled or revoked.
	ManagedBy           string     `json:"managedBy,omitempty"`
	ApplicationClientID string     `json:"applicationClientId,omitempty"`
	DisabledAt          *time.Time `json:"disabledAt,omitempty"`
}

// WebhookFailureEvent is a persisted outbound delivery failure for a workspace webhook.
type WebhookFailureEvent struct {
	ID              string    `json:"id"`
	WebhookID       string    `json:"webhookId"`
	ApplicationID   string    `json:"applicationId,omitempty"`
	ExecutionID     string    `json:"executionId"`
	URL             string    `json:"url"`
	HTTPStatus      *int      `json:"httpStatus,omitempty"`
	ResponseOrError string    `json:"responseOrError"`
	CreatedAt       time.Time `json:"createdAt"`
}

type JiraLink struct {
	ID              string            `json:"id"`
	JiraProjectID   string            `json:"jiraProjectId"`
	JiraProjectKey  string            `json:"jiraProjectKey,omitempty"`
	JiraProjectName string            `json:"jiraProjectName,omitempty"`
	TeamID          string            `json:"teamId"`
	SyncDirection   string            `json:"syncDirection"`       // bidirectional | unidirectional | legacyUnidirectional
	StatusMap       map[string]string `json:"statusMap,omitempty"` // jira status name -> flow workflow state id
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}

type IntegrationConnection struct {
	ID                string            `json:"id"`
	Provider          string            `json:"provider"`
	Name              string            `json:"name"`
	Status            string            `json:"status"`
	Config            map[string]string `json:"config,omitempty"`
	SecretHash        string            `json:"-"`
	ConnectedBy       string            `json:"connectedBy"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
	LastWebhookAt     *time.Time        `json:"lastWebhookAt,omitempty"`
	LastTestAt        *time.Time        `json:"lastTestAt,omitempty"`
	LastTestStatus    string            `json:"lastTestStatus,omitempty"`
	LastError         string            `json:"lastError,omitempty"`
	OAuthState        string            `json:"oauthState,omitempty"`
	OAuthStartedAt    *time.Time        `json:"oauthStartedAt,omitempty"`
	OAuthCompletedAt  *time.Time        `json:"oauthCompletedAt,omitempty"`
	OAuthAccessToken  string            `json:"oauthAccessToken,omitempty"`
	OAuthRefreshToken string            `json:"oauthRefreshToken,omitempty"`
	OAuthExpiresAt    *time.Time        `json:"oauthExpiresAt,omitempty"`
	Scopes            []string          `json:"scopes"`
	Channels          []string          `json:"channels"`
	LinkbackEnabled   bool              `json:"linkbackEnabled"`
	DeliveryAttempts  int               `json:"deliveryAttempts"`
	LastDeliveryAt    *time.Time        `json:"lastDeliveryAt,omitempty"`
}

type IntegrationDelivery struct {
	ID             string          `json:"id"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	ConnectionID   string          `json:"connectionId"`
	EventType      string          `json:"eventType"`
	ResourceID     string          `json:"resourceId"`
	Channel        string          `json:"channel,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	PreviousValues json.RawMessage `json:"previousValues,omitempty"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	NextAttemptAt  *time.Time      `json:"nextAttemptAt,omitempty"`
	LastError      string          `json:"lastError,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

type GitAutomationState struct {
	ID              string    `json:"id"`
	TeamID          string    `json:"teamId"`
	Repository      string    `json:"repository"`
	Event           string    `json:"event"`
	WorkflowStateID string    `json:"workflowStateId"`
	SyncComments    bool      `json:"syncComments"`
	SyncLabels      bool      `json:"syncLabels"`
	SyncAssignees   bool      `json:"syncAssignees"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type TargetBranch struct {
	ID               string            `json:"id"`
	TeamID           string            `json:"teamId"`
	Repository       string            `json:"repository"`
	Branch           string            `json:"branch"`
	Default          bool              `json:"default"`
	AutomationStates map[string]string `json:"automationStates,omitempty"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
}

type ReviewCheck struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	URL    string `json:"url,omitempty"`
}

type ReviewFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
	// PreviousPath is the old path of a renamed or moved file.
	PreviousPath string `json:"previousPath,omitempty"`
	// PatchUnavailable: the code host sent no patch although the file
	// changed (binary or too large to diff).
	PatchUnavailable bool `json:"patchUnavailable,omitempty"`
}

// ReviewEventRemote is the code host object a review event mirrors, so
// syncs and webhooks update it in place instead of duplicating it.
type ReviewEventRemote struct {
	Provider string `json:"provider"`
	// Kind: review_comment (inline), issue_comment, review (GitHub) or note (GitLab).
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// ThreadID is the GitHub review thread node id or the GitLab discussion id.
	ThreadID string `json:"threadId,omitempty"`
	URL      string `json:"url,omitempty"`
}

// ReviewEventSync is a local change not yet confirmed by the code host.
type ReviewEventSync struct {
	State string `json:"state"` // pending | failed
	// Op is what has to reach the code host: create | resolve | unresolve.
	Op          string    `json:"op"`
	Error       string    `json:"error,omitempty"`
	AttemptedAt time.Time `json:"attemptedAt"`
	// ActorID is the member whose change this is (a resolve may come from
	// someone other than the comment's author); retries post as them.
	ActorID string `json:"actorId,omitempty"`
}

type ReviewEvent struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Body     string `json:"body,omitempty"`
	ParentID string `json:"parentId,omitempty"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	// Side is the diff side a code comment is on: LEFT (the old file, e.g. a
	// removed line) or RIGHT (the new file, the default).
	Side string `json:"side,omitempty"`
	// StartLine/StartSide start a multi-line comment range ending at Line.
	StartLine int    `json:"startLine,omitempty"`
	StartSide string `json:"startSide,omitempty"`
	// Outdated: the commented code changed since (Line is the original line).
	Outdated  bool               `json:"outdated,omitempty"`
	CommitID  string             `json:"commitId,omitempty"`
	DiffHunk  string             `json:"diffHunk,omitempty"`
	Resolved  bool               `json:"resolved,omitempty"`
	Actor     User               `json:"actor"`
	CreatedAt time.Time          `json:"createdAt"`
	UpdatedAt *time.Time         `json:"updatedAt,omitempty"`
	Remote    *ReviewEventRemote `json:"remote,omitempty"`
	Sync      *ReviewEventSync   `json:"sync,omitempty"`
	// PostedAs is the code host identity a Flow-originated comment, review
	// or merge was posted to GitHub/GitLab with (set once the code host
	// accepted it).
	PostedAs *ReviewPostedAs `json:"postedAs,omitempty"`
}

// ReviewPostedAs: Via is "member" (the actor's linked account) or
// "workspace" (the workspace integration's credential).
type ReviewPostedAs struct {
	Provider string `json:"provider"`
	Login    string `json:"login"`
	Via      string `json:"via"`
}

type CodeReview struct {
	ID              string          `json:"id"`
	SlugID          string          `json:"slugId"`
	Provider        string          `json:"provider"`
	ExternalID      string          `json:"externalId"`
	Number          int             `json:"number"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Status          string          `json:"status"`
	RepositoryOwner string          `json:"repositoryOwner"`
	RepositoryName  string          `json:"repositoryName"`
	URL             string          `json:"url"`
	Author          User            `json:"author"`
	ReviewerIDs     []string        `json:"reviewerIds"`
	TeamReviewers   []string        `json:"teamReviewers"`
	IssueIDs        []string        `json:"issueIds"`
	BaseBranch      string          `json:"baseBranch"`
	HeadBranch      string          `json:"headBranch"`
	BranchState     string          `json:"branchState"`
	Additions       int             `json:"additions"`
	Deletions       int             `json:"deletions"`
	CommitCount     int             `json:"commitCount"`
	Checks          []ReviewCheck   `json:"checks"`
	Files           []ReviewFile    `json:"files"`
	Events          []ReviewEvent   `json:"events"`
	Previews        []DeployPreview `json:"previews,omitempty"`
	// HeadSHA/BaseSHA/StartSHA are the diff refs inline comments are created against.
	HeadSHA  string `json:"headSha,omitempty"`
	BaseSHA  string `json:"baseSha,omitempty"`
	StartSHA string `json:"startSha,omitempty"`
	// SyncedAt is the last full sync of files and comments from the code
	// host; SyncError the reason the last one failed.
	SyncedAt      *time.Time `json:"syncedAt,omitempty"`
	SyncError     string     `json:"syncError,omitempty"`
	Favorite      bool       `json:"favorite"`
	Draft         bool       `json:"draft"`
	QuickToReview bool       `json:"quickToReview"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	MergedAt      *time.Time `json:"mergedAt,omitempty"`
	ClosedAt      *time.Time `json:"closedAt,omitempty"`
}

// DeployPreview is one environment deployment for a pull request's head
// branch (Vercel / Netlify / GitHub deployments), shown on linked issues.
type DeployPreview struct {
	ID          string    `json:"id"`
	Provider    string    `json:"provider"`
	Environment string    `json:"environment"`
	URL         string    `json:"url"`
	LogURL      string    `json:"logUrl,omitempty"`
	State       string    `json:"state"`
	CommitSHA   string    `json:"commitSha,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type DeployPreviewInput struct {
	Environment string `json:"environment"`
	URL         string `json:"url"`
	LogURL      string `json:"logUrl"`
	State       string `json:"state"`
	CommitSHA   string `json:"commitSha"`
	Provider    string `json:"provider"`
}

type SLARule struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	TeamIDs       []string       `json:"teamIds"`
	Filters       map[string]any `json:"filters"`
	TargetMinutes int            `json:"targetMinutes"`
	PauseStatuses []string       `json:"pauseStatuses"`
	BusinessHours bool           `json:"businessHours"`
	Enabled       bool           `json:"enabled"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

type IssueSLA struct {
	ID               string     `json:"id"`
	IssueID          string     `json:"issueId"`
	RuleID           string     `json:"ruleId"`
	StartedAt        time.Time  `json:"startedAt"`
	DueAt            time.Time  `json:"dueAt"`
	PausedAt         *time.Time `json:"pausedAt,omitempty"`
	PausedMinutes    int        `json:"pausedMinutes"`
	BreachedAt       *time.Time `json:"breachedAt,omitempty"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
	RemainingMinutes int        `json:"remainingMinutes"`
	Status           string     `json:"status"`
}

type SLAEvent struct {
	ID        string    `json:"id"`
	IssueID   string    `json:"issueId"`
	SLAID     string    `json:"slaId"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
}

type Draft struct {
	ID          string         `json:"id"`
	UserID      string         `json:"userId"`
	Type        string         `json:"type"`
	ResourceID  string         `json:"resourceId,omitempty"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	ContentData map[string]any `json:"contentData,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

// Favorite is one sidebar favorite. Tab names a specific page tab of the
// resource (the reference app's pipelineTab, projectTab, initiativeTab); empty is the
// resource's root favorite. A root favorite and each tab favorite are
// separate records, unique per (user, resource type, resource id, tab).
type Favorite struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	Tab          string    `json:"tab,omitempty"`
	FolderID     string    `json:"folderId,omitempty"`
	Position     float64   `json:"position"`
	CreatedAt    time.Time `json:"createdAt"`
}

type FavoriteFolder struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	Name      string    `json:"name"`
	Position  float64   `json:"position"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type TeamDefaultFavorite struct {
	ID           string    `json:"id"`
	WorkspaceKey string    `json:"workspaceKey"`
	TeamID       string    `json:"teamId"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	Position     float64   `json:"position"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Subscription struct {
	ID           string   `json:"id"`
	UserID       string   `json:"userId"`
	ResourceType string   `json:"resourceType"`
	ResourceID   string   `json:"resourceId"`
	Events       []string `json:"events,omitempty"`
	// OptOutEvents are events the user explicitly unsubscribed from, such as
	// "pulse" on a project they would otherwise follow by default.
	OptOutEvents []string  `json:"optOutEvents,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ThreadSubscription is a user's explicit choice for one comment thread
// (the root comment and its replies). Thread participants are subscribed
// implicitly; "muted" silences a thread even for issue subscribers. Exactly
// one of IssueID or ProjectID names the thread's parent resource.
type ThreadSubscription struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	IssueID   string `json:"issueId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	// InitiativeID marks a thread on an initiative (its inline description
	// comments); IssueID and ProjectID are then empty.
	InitiativeID string    `json:"initiativeId,omitempty"`
	CommentID    string    `json:"commentId"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type ThreadSubscriptionInput struct {
	State string `json:"state"`
}

type SubscriptionMutationInput struct {
	Events *[]string `json:"events,omitempty"`
}

type AuditLogEntry struct {
	ID           string         `json:"id"`
	Actor        User           `json:"actor"`
	BotActor     *ActorBot      `json:"botActor,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resourceType"`
	ResourceID   string         `json:"resourceId"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
}

type TrashEntry struct {
	ID           string          `json:"id"`
	ResourceType string          `json:"resourceType"`
	ResourceID   string          `json:"resourceId"`
	Title        string          `json:"title"`
	Payload      json.RawMessage `json:"payload"`
	TeamIDs      []string        `json:"teamIds,omitempty"`
	DeletedBy    User            `json:"deletedBy"`
	DeletedAt    time.Time       `json:"deletedAt"`
	ExpiresAt    time.Time       `json:"expiresAt"`
	// Links are what the deletion detached from other records; restoring
	// re-attaches them. Server-side only (stripped from viewer projections).
	Links *TrashLinks `json:"links,omitempty"`
}

type ImportJob struct {
	ID         string              `json:"id"`
	UserID     string              `json:"userId"`
	Filename   string              `json:"filename"`
	Format     string              `json:"format"`
	Status     string              `json:"status"`
	Headers    []string            `json:"headers"`
	Rows       []map[string]string `json:"rows,omitempty"`
	Mapping    map[string]string   `json:"mapping,omitempty"`
	Imported   int                 `json:"imported"`
	Errors     []string            `json:"errors"`
	CreatedAt  time.Time           `json:"createdAt"`
	UpdatedAt  time.Time           `json:"updatedAt"`
	Progress   int                 `json:"progress"`
	Error      string              `json:"error,omitempty"`
	RetryCount int                 `json:"retryCount,omitempty"`
	TeamID     string              `json:"teamId,omitempty"`
	RowsTotal  int                 `json:"rowsTotal,omitempty"`
	Checkpoint int                 `json:"checkpoint,omitempty"`
	RowErrors  []ImportRowError    `json:"rowErrors,omitempty"`
}

type ImportRowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

type ExportJob struct {
	ID             string     `json:"id"`
	UserID         string     `json:"userId"`
	Format         string     `json:"format"`
	IncludePrivate bool       `json:"includePrivate"`
	Status         string     `json:"status"`
	Filename       string     `json:"filename,omitempty"`
	Data           string     `json:"-"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

type MigrationEntityMapping struct {
	ID         string         `json:"id"`
	EntityType string         `json:"entityType"`
	SourceID   string         `json:"sourceId"`
	SourceName string         `json:"sourceName,omitempty"`
	TargetID   string         `json:"targetId,omitempty"`
	TargetName string         `json:"targetName,omitempty"`
	Action     string         `json:"action"`
	Status     string         `json:"status"`
	Error      string         `json:"error,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type MigrationJob struct {
	ID           string                   `json:"id"`
	UserID       string                   `json:"userId"`
	Filename     string                   `json:"filename"`
	Source       string                   `json:"source"`
	Target       string                   `json:"target"`
	TargetTeamID string                   `json:"targetTeamId,omitempty"`
	Status       string                   `json:"status"`
	Phase        string                   `json:"phase"`
	Progress     int                      `json:"progress"`
	Counts       map[string]int           `json:"counts"`
	Mappings     []MigrationEntityMapping `json:"mappings"`
	Errors       []string                 `json:"errors"`
	Bundle       json.RawMessage          `json:"bundle,omitempty"`
	CreatedAt    time.Time                `json:"createdAt"`
	UpdatedAt    time.Time                `json:"updatedAt"`
	CompletedAt  *time.Time               `json:"completedAt,omitempty"`
}

type MigrationBundle struct {
	Version           int                           `json:"version"`
	Source            string                        `json:"source"`
	ExportedAt        time.Time                     `json:"exportedAt"`
	Workspace         Workspace                     `json:"workspace"`
	Users             []User                        `json:"users"`
	Teams             []Team                        `json:"teams"`
	States            []WorkflowState               `json:"workflowStates"`
	Labels            []IssueLabel                  `json:"labels"`
	LabelGroups       []LabelGroup                  `json:"labelGroups"`
	Projects          []Project                     `json:"projects"`
	ProjectUpdates    map[string][]ProjectUpdate    `json:"projectUpdates"`
	Cycles            []Cycle                       `json:"cycles"`
	Initiatives       []Initiative                  `json:"initiatives"`
	InitiativeUpdates map[string][]InitiativeUpdate `json:"initiativeUpdates"`
	Issues            []Issue                       `json:"issues"`
	Comments          map[string][]Comment          `json:"comments"`
	Activities        map[string][]ActivityEvent    `json:"activities"`
	Releases          []Release                     `json:"releases"`
	ReleasePipelines  []ReleasePipeline             `json:"releasePipelines"`
	Customers         []Customer                    `json:"customers"`
	CustomerRequests  []CustomerRequest             `json:"customerRequests"`
	SLARules          []SLARule                     `json:"slaRules"`
	IssueSLAs         []IssueSLA                    `json:"issueSlas"`
	SLAEvents         []SLAEvent                    `json:"slaEvents"`
	Subscriptions     []Subscription                `json:"subscriptions"`
}

// Initiative is the workspace-level planning aggregate above projects.
// ProjectIDs intentionally stores stable project IDs; list progress and health
// are projections computed by the client from the associated projects.
type Initiative struct {
	ID                   string                          `json:"id"`
	Name                 string                          `json:"name"`
	SlugID               string                          `json:"slugId"`                    // URL slug `<name-slug>-<12 hex>`; the hex suffix stays when the name changes
	PreviousSlugIDs      []string                        `json:"previousSlugIds,omitempty"` // earlier slugs (before a rename, or the legacy name-only slug); links using them still resolve
	Summary              string                          `json:"summary"`
	Description          string                          `json:"description"`
	Icon                 string                          `json:"icon,omitempty"`
	Color                string                          `json:"color"`
	Status               string                          `json:"status"`
	Priority             int                             `json:"priority"`
	Position             float64                         `json:"position"`
	PriorityLabel        string                          `json:"priorityLabel"`
	Health               string                          `json:"health"`
	Owner                *User                           `json:"owner,omitempty"`
	Creator              User                            `json:"creator"`
	BotActor             *ActorBot                       `json:"botActor,omitempty"` // creation through an application or Flow's agent
	LeadTeamID           string                          `json:"leadTeamId,omitempty"`
	ContributingTeamIDs  []string                        `json:"contributingTeamIds"`
	LabelIDs             []string                        `json:"labelIds"`
	ParentInitiativeIDs  []string                        `json:"parentInitiativeIds"`
	ProjectIDs           []string                        `json:"projectIds"`
	Resources            []InitiativeResource            `json:"resources"`
	Comments             []Comment                       `json:"comments"`
	CommentAttachments   []Attachment                    `json:"commentAttachments,omitempty"`
	TargetDate           *string                         `json:"targetDate,omitempty"`
	TargetDateResolution string                          `json:"targetDateResolution,omitempty"`
	Favorite             bool                            `json:"favorite"`
	Subscribed           bool                            `json:"subscribed"`
	NotificationRules    InitiativeNotificationRules     `json:"notificationRules"`
	UpdateSchedule       InitiativeUpdateSchedule        `json:"updateSchedule"`
	DescriptionHistory   []InitiativeDescriptionRevision `json:"descriptionHistory"`
	CreatedAt            time.Time                       `json:"createdAt"`
	UpdatedAt            time.Time                       `json:"updatedAt"`
}

type InitiativeNotificationRules struct {
	DescriptionChanges bool `json:"descriptionChanges"`
	NewUpdate          bool `json:"newUpdate"`
	AllProjectUpdates  bool `json:"allProjectUpdates"`
}

type InitiativeUpdateSchedule struct {
	Cadence   string `json:"cadence"`
	Weekday   int    `json:"weekday"`
	TimeRange string `json:"timeRange"`
}

type InitiativeDescriptionRevision struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	EditedAt    time.Time `json:"editedAt"`
	Editor      User      `json:"editor"`
}

type InitiativeResource struct {
	ID           string    `json:"id"`
	InitiativeID string    `json:"initiativeId"`
	Type         string    `json:"type"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	DocumentID   string    `json:"documentId,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type InitiativeUpdate struct {
	ID           string              `json:"id"`
	InitiativeID string              `json:"initiativeId"`
	Body         string              `json:"body"`
	BodyData     map[string]any      `json:"bodyData,omitempty"`
	Health       string              `json:"health"`
	CreatedAt    time.Time           `json:"createdAt"`
	EditedAt     *time.Time          `json:"editedAt,omitempty"`
	User         User                `json:"user"`
	Comments     []Comment           `json:"comments"`
	Reactions    map[string][]string `json:"reactions"`
	Attachments  []Attachment        `json:"attachments"`
	Snapshot     *PulseSnapshot      `json:"snapshot,omitempty"`
	Diff         *PulseDiff          `json:"diff,omitempty"`
}

type CommentThreadSummary struct {
	Content   string `json:"content"`
	EvalLogID string `json:"evalLogId,omitempty"`
}

type Comment struct {
	ID            string                `json:"id"`
	Version       int64                 `json:"version"`
	Body          string                `json:"body"`
	BodyData      map[string]any        `json:"bodyData,omitempty"`
	ParentID      *string               `json:"parentId,omitempty"`
	Resolved      bool                  `json:"resolved,omitempty"`
	ThreadSummary *CommentThreadSummary `json:"threadSummary,omitempty"`
	Reactions     map[string][]string   `json:"reactions"`
	CreatedAt     time.Time             `json:"createdAt"`
	EditedAt      *time.Time            `json:"editedAt,omitempty"`
	User          User                  `json:"user"`
	BotActor      *ActorBot             `json:"botActor,omitempty"`
	// AnchorID names the inline comment that anchors a thread to text: the
	// comment mark (data-comment-id) in a document, or the selection in a
	// project or initiative description. QuotedText is the selection it was
	// created on. Both are empty for page-level comments and replies.
	AnchorID   string `json:"anchorId,omitempty"`
	QuotedText string `json:"quotedText,omitempty"`
}

type ActivityEvent struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	CreatedAt time.Time         `json:"createdAt"`
	Actor     User              `json:"actor"`
	BotActor  *ActorBot         `json:"botActor,omitempty"`
	Metadata  map[string]string `json:"metadata"`
}

// Notification is an Inbox aggregate. Source IDs retain a stable link to the
// Issue collaboration record while the Issue itself remains the source of
// title, description, and property data.
type Notification struct {
	ID              string     `json:"id"`
	RecipientID     string     `json:"recipientId"`
	Type            string     `json:"type"`
	SourceType      string     `json:"sourceType"`
	SourceID        string     `json:"sourceId"`
	IssueID         string     `json:"issueId"`
	ProjectID       string     `json:"projectId,omitempty"`
	CommentID       string     `json:"commentId,omitempty"`
	ActivityID      string     `json:"activityId,omitempty"`
	ReviewID        string     `json:"reviewId,omitempty"`
	Actor           User       `json:"actor"`
	Category        string     `json:"category"`
	GroupKey        string     `json:"groupKey"`
	OccurrenceCount int        `json:"occurrenceCount"`
	LatestActorIDs  []string   `json:"latestActorIds"`
	ReadAt          *time.Time `json:"readAt,omitempty"`
	FavoritedAt     *time.Time `json:"favoritedAt,omitempty"`
	ArchivedAt      *time.Time `json:"archivedAt,omitempty"`
	DeletedAt       *time.Time `json:"deletedAt,omitempty"`
	SnoozedUntil    *time.Time `json:"snoozedUntil,omitempty"`
	Favorite        bool       `json:"favorite"`
	// Title and Text are the display strings of notifications that do not
	// derive them from an issue (Pulse summaries).
	Title   string               `json:"title,omitempty"`
	Text    string               `json:"text,omitempty"`
	Payload *NotificationPayload `json:"payload,omitempty"`
	// PulseSummary caches the generated summary of a Pulse summary
	// notification; it is served by /api/pulse/summaries/{id}, not in lists.
	PulseSummary   *PulseSummary        `json:"pulseSummary,omitempty"`
	SummaryReports []PulseSummaryReport `json:"summaryReports,omitempty"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
}

type NotificationChannelPreferences struct {
	Enabled    bool            `json:"enabled"`
	Categories map[string]bool `json:"categories"`
}

type NotificationPreferences struct {
	UserID  string                         `json:"userId"`
	Inbox   NotificationChannelPreferences `json:"inbox"`
	Email   NotificationChannelPreferences `json:"email"`
	Desktop NotificationChannelPreferences `json:"desktop"`
	// Chat is personal notifications sent as a direct message by the Lark or
	// Feishu app bot to the member's linked account. Off unless turned on;
	// an update without it leaves it unchanged.
	Chat              *NotificationChannelPreferences `json:"chat,omitempty"`
	EmailFormat       string                          `json:"emailFormat"`
	DelayLowPriority  bool                            `json:"delayLowPriority"`
	ImmediateUrgent   bool                            `json:"immediateUrgent"`
	SoundEnabled      bool                            `json:"soundEnabled"`
	DesktopPermission string                          `json:"desktopPermission,omitempty"`
	UpdatedAt         time.Time                       `json:"updatedAt"`
}

type NotificationDelivery struct {
	ID             string     `json:"id"`
	NotificationID string     `json:"notificationId"`
	RecipientID    string     `json:"recipientId"`
	Channel        string     `json:"channel"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	NextAttemptAt  *time.Time `json:"nextAttemptAt,omitempty"`
	DeliveredAt    *time.Time `json:"deliveredAt,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type NotificationList struct {
	Notifications []Notification `json:"notifications"`
	UnreadCount   int            `json:"unreadCount"`
}

type Bootstrap struct {
	ResourceDetailsOmitted bool          `json:"resourceDetailsOmitted,omitempty"`
	ProviderJobs           []ProviderJob `json:"providerJobs,omitempty"`
	NextIssueNumber        int           `json:"-"`
	// ViewerTeamIDs are the acting viewer's teams read from the membership
	// records while a mutation's authorization callback runs; the stored
	// snapshot's TeamMembers can lag those records. Not serialized.
	ViewerTeamIDs        []string  `json:"-"`
	IssueCollectionPaged bool      `json:"issueCollectionPaged,omitempty"`
	Workspace            Workspace `json:"workspace"`
	Viewer               User      `json:"viewer"`
	Users                []User    `json:"users"`
	Teams                []Team    `json:"teams"`
	// TeamByID/TeamByKey/TeamChildren/LabelIndex are in-memory lookup tables
	// rebuilt on load. They are not serialized.
	TeamByID          map[string]int           `json:"-"`
	TeamByKey         map[string]string        `json:"-"`
	TeamChildren      map[string][]string      `json:"-"`
	LabelIndex        map[string][]int         `json:"-"`
	Customers         []Customer               `json:"customers"`
	States            []WorkflowState          `json:"states"`
	Labels            []IssueLabel             `json:"labels"`
	LabelGroups       []LabelGroup             `json:"labelGroups"`
	Issues            []Issue                  `json:"issues"`
	Cycles            []Cycle                  `json:"cycles"`
	CycleSettings     map[string]CycleSettings `json:"cycleSettings"`
	TeamSettings      map[string]TeamSettings  `json:"teamSettings"`
	TeamParents       map[string]string        `json:"teamParents,omitempty"`
	IssueTemplates    []IssueTemplate          `json:"issueTemplates"`
	ProjectTemplates  []ProjectTemplate        `json:"projectTemplates"`
	DocumentTemplates []DocumentTemplate       `json:"documentTemplates"`
	Documents         []Document               `json:"documents"`
	CustomerRequests  []CustomerRequest        `json:"customerRequests"`
	Releases          []Release                `json:"releases"`
	ReleasePipelines  []ReleasePipeline        `json:"releasePipelines"`
	CustomEmojis      []CustomEmoji            `json:"customEmojis"`
	Asks              []Ask                    `json:"asks"`
	Loops             []Loop                   `json:"loops"`
	// LoopRuns is legacy: runs now live in loop_run_records, and the store
	// moves runs found here into that table once at startup.
	LoopRuns                      []LoopRun                          `json:"loopRuns,omitempty"`
	LoopVersions                  []LoopVersion                      `json:"loopVersions,omitempty"`
	LoopAttachments               []LoopAttachment                   `json:"loopAttachments,omitempty"`
	SLARules                      []SLARule                          `json:"slaRules"`
	IssueSLAs                     []IssueSLA                         `json:"issueSlas"`
	SLAEvents                     []SLAEvent                         `json:"slaEvents"`
	Drafts                        []Draft                            `json:"drafts"`
	Favorites                     []Favorite                         `json:"favorites"`
	FavoriteFolders               []FavoriteFolder                   `json:"favoriteFolders"`
	Subscriptions                 []Subscription                     `json:"subscriptions"`
	ThreadSubscriptions           []ThreadSubscription               `json:"threadSubscriptions"`
	AuditLog                      []AuditLogEntry                    `json:"auditLog"`
	Trash                         []TrashEntry                       `json:"trash"`
	ImportJobs                    []ImportJob                        `json:"importJobs"`
	ExportJobs                    []ExportJob                        `json:"exportJobs"`
	MigrationJobs                 []MigrationJob                     `json:"migrationJobs"`
	ProjectRelations              []ProjectRelation                  `json:"projectRelations"`
	InitiativeRelations           []InitiativeRelation               `json:"initiativeRelations"`
	DocumentContentDrafts         []DocumentContentDraft             `json:"documentContentDrafts"`
	CustomerStatuses              []CustomerStatus                   `json:"customerStatuses"`
	CustomerTiers                 []CustomerTier                     `json:"customerTiers"`
	ReleaseNotes                  []ReleaseNote                      `json:"releaseNotes"`
	ReleaseHistory                []ReleaseHistory                   `json:"releaseHistory"`
	TeamResourceSections          []TeamResourceSection              `json:"teamResourceSections"`
	TeamPinnedResources           []TeamPinnedResource               `json:"teamPinnedResources"`
	AgentActivities               []AgentActivity                    `json:"agentActivities"`
	AIConversations               []AIConversation                   `json:"aiConversations"`
	AIPromptProgress              []AIPromptProgress                 `json:"aiPromptProgress"`
	IssueSuggestions              []IssueSuggestion                  `json:"issueSuggestions"`
	Projects                      []Project                          `json:"projects"`
	ProjectStatuses               []ProjectStatus                    `json:"projectStatuses"`
	ProjectDisplayDefault         json.RawMessage                    `json:"projectDisplayDefault,omitempty"`
	ProjectUpdates                map[string][]ProjectUpdate         `json:"projectUpdates"`
	Initiatives                   []Initiative                       `json:"initiatives"`
	InitiativeUpdates             map[string][]InitiativeUpdate      `json:"initiativeUpdates"`
	Comments                      map[string][]Comment               `json:"comments"`
	Activities                    map[string][]ActivityEvent         `json:"activities"`
	SavedViews                    []SavedView                        `json:"savedViews"`
	Notifications                 []Notification                     `json:"notifications"`
	InboxUnreadCount              int                                `json:"inboxUnreadCount,omitempty"`
	ReviewCount                   int                                `json:"reviewCount,omitempty"`
	NotificationPreferences       map[string]NotificationPreferences `json:"notificationPreferences"`
	NotificationDeliveries        []NotificationDelivery             `json:"notificationDeliveries"`
	PushSubscriptions             []PushSubscription                 `json:"pushSubscriptions"`
	TriageResponsibilities        []TriageResponsibility             `json:"triageResponsibilities"`
	TriageRoutingRules            []TriageRoutingRule                `json:"triageRoutingRules"`
	TriageAssignments             []TriageAssignment                 `json:"triageAssignments"`
	WorkflowDefinitions           []WorkflowDefinition               `json:"workflowDefinitions"`
	WorkflowRuns                  []WorkflowRun                      `json:"workflowRuns"`
	EmailIntakeAddresses          []EmailIntakeAddress               `json:"emailIntakeAddresses"`
	EmailIntakeMessages           []EmailIntakeMessage               `json:"emailIntakeMessages"`
	UserSettings                  map[string]UserSettings            `json:"userSettings"`
	WorkspaceSettings             WorkspaceSettings                  `json:"workspaceSettings"`
	APIKeys                       []APIKey                           `json:"apiKeys"`
	Passkeys                      []Passkey                          `json:"passkeys,omitempty"`
	PasskeyRegistrationChallenges []PasskeyRegistrationChallenge     `json:"passkeyRegistrationChallenges,omitempty"`
	OAuthApplications             []OAuthApplication                 `json:"oauthApplications"`
	OAuthAuthorizations           []OAuthAuthorization               `json:"oauthAuthorizations"`
	Webhooks                      []Webhook                          `json:"webhooks"`
	WebhookFailureEvents          []WebhookFailureEvent              `json:"webhookFailureEvents"`
	IntegrationConnections        []IntegrationConnection            `json:"integrationConnections"`
	JiraLinks                     []JiraLink                         `json:"jiraLinks,omitempty"`
	IdentityProviders             []IdentityProvider                 `json:"identityProviders"`
	IntegrationDeliveries         []IntegrationDelivery              `json:"integrationDeliveries"`
	GitAutomationStates           []GitAutomationState               `json:"gitAutomationStates"`
	TargetBranches                []TargetBranch                     `json:"targetBranches"`
	Reviews                       []CodeReview                       `json:"reviews"`
	AgentSessions                 []AgentSession                     `json:"agentSessions"`
	AgentSkills                   []PersonalAgentSkill               `json:"agentSkills"`
	Settings                      map[string]any                     `json:"settings"`
	Members                       []WorkspaceMember                  `json:"members"`
	// DepartedMembers lists former members (status "departed": removed people
	// and the app users of uninstalled or deleted applications) for workspace
	// admins, as the reference app's Settings › Members shows them under "Inactive" with
	// "Left workspace".
	DepartedMembers        []WorkspaceMember       `json:"departedMembers,omitempty"`
	TeamMembers            []TeamMember            `json:"teamMembers"`
	Invitations            []Invitation            `json:"invitations"`
	WorkspaceInviteLink    *WorkspaceInviteLink    `json:"workspaceInviteLink,omitempty"`
	OAuthSyncGroupRequests []OAuthSyncGroupRequest `json:"oauthSyncGroupRequests,omitempty"`
	ViewerRole             string                  `json:"viewerRole"`
	// FormerMembers is set by the store only while a write's callback runs
	// (never stored or serialized): it reports the people the workspace's
	// memberships mark as no longer members (removed, or suspended), read on
	// first use. The users collection keeps them, so references stay
	// readable. Nil outside a write.
	FormerMembers func() map[string]bool `json:"-"`
	// CollaborationUpdates reads accepted, uncompacted updates through the
	// write's reader, so mention checks preserve the live document too.
	CollaborationUpdates func(documentID string) ([][]byte, error) `json:"-"`

	// TrashRetentionDays is set on bootstrap responses only: how long deleted
	// items stay restorable (FLOW_TRASH_RETENTION_DAYS).
	TrashRetentionDays int `json:"trashRetentionDays,omitempty"`
	// ChatPlatform is set on bootstrap responses only: the deployment's
	// first-class chat platform (FLOW_CHAT_PLATFORM).
	ChatPlatform string `json:"chatPlatform,omitempty" enum:"slack,lark,feishu"`
}

type ProviderJob struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	ConnectionID string    `json:"connectionId"`
	IssueID      string    `json:"issueId"`
	UserID       string    `json:"userId"`
	ExternalID   string    `json:"externalId"`
	TurnID       string    `json:"turnId"`
	Status       string    `json:"status"`
	URL          string    `json:"url,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type SavedView struct {
	ID          string          `json:"id"`
	SlugID      string          `json:"slugId,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Icon        string          `json:"icon,omitempty"`
	Color       string          `json:"color,omitempty"`
	Resource    string          `json:"resource"`
	ProjectID   string          `json:"projectId,omitempty"`
	Scope       string          `json:"scope"`
	TeamID      string          `json:"teamId,omitempty"`
	OwnerID     string          `json:"ownerId,omitempty"`
	Favorite    bool            `json:"favorite,omitempty"`
	Subscribed  bool            `json:"subscribed,omitempty"`
	View        string          `json:"view"`
	Filters     json.RawMessage `json:"filters"`
	Display     json.RawMessage `json:"display"`
	Insights    json.RawMessage `json:"insights"`
	ShareToken  string          `json:"shareToken,omitempty"`
	SharedAt    *time.Time      `json:"sharedAt,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type SavedViewMutationInput struct {
	Name        *string         `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Icon        *string         `json:"icon,omitempty"`
	Color       *string         `json:"color,omitempty"`
	Resource    *string         `json:"resource,omitempty"`
	ProjectID   *string         `json:"projectId,omitempty"`
	Scope       *string         `json:"scope,omitempty"`
	TeamID      *string         `json:"teamId,omitempty"`
	OwnerID     *string         `json:"ownerId,omitempty"`
	Favorite    *bool           `json:"favorite,omitempty"`
	Subscribed  *bool           `json:"subscribed,omitempty"`
	View        *string         `json:"view,omitempty"`
	Filters     json.RawMessage `json:"filters,omitempty"`
	Display     json.RawMessage `json:"display,omitempty"`
	Insights    json.RawMessage `json:"insights,omitempty"`
}

type IssueCreateInput struct {
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	DescriptionState   *string        `json:"descriptionState,omitempty"`
	DescriptionData    map[string]any `json:"descriptionData,omitempty"`
	ContentState       *string        `json:"contentState,omitempty"`
	TeamID             string         `json:"teamId"`
	ParentID           *string        `json:"parentId,omitempty"`
	StateID            *string        `json:"stateId,omitempty"`
	Priority           *int           `json:"priority,omitempty"`
	Estimate           *float64       `json:"estimate,omitempty"`
	AssigneeID         *string        `json:"assigneeId,omitempty"`
	DelegateID         *string        `json:"delegateId,omitempty"`
	ProjectID          *string        `json:"projectId,omitempty"`
	ProjectMilestoneID *string        `json:"projectMilestoneId,omitempty"`
	CycleID            *string        `json:"cycleId,omitempty"`
	DueDate            *string        `json:"dueDate,omitempty"`
	SLABreachesAt      *string        `json:"slaBreachesAt,omitempty"`
	SLAType            *string        `json:"slaType,omitempty"`
	LabelIDs           []string       `json:"labelIds,omitempty"`
	TemplateID         string         `json:"templateId,omitempty"`
	Icon               *string        `json:"icon,omitempty"`
	Recurrence         *string        `json:"recurrence,omitempty"`       // with DueDate as the first due date
	NextOccurrenceAt   *string        `json:"nextOccurrenceAt,omitempty"` // legacy alias for the first due date
	// CreateAsUser names the external person an actor=app token acts for
	// ("<name> via <App>", at most 100 characters). Rejected for user tokens.
	CreateAsUser *string `json:"createAsUser,omitempty"`
	// DisplayIconURL is that person's avatar; requires createAsUser.
	DisplayIconURL *string `json:"displayIconUrl,omitempty"`
}

type ProjectMutationInput struct {
	Name                 *string                          `json:"name,omitempty"`
	Summary              *string                          `json:"summary,omitempty"`
	Description          *string                          `json:"description,omitempty"`
	Icon                 *string                          `json:"icon,omitempty"`
	Color                *string                          `json:"color,omitempty"`
	StatusID             *string                          `json:"statusId,omitempty"`
	Priority             *int                             `json:"priority,omitempty"`
	Position             *float64                         `json:"position,omitempty"`
	Health               *string                          `json:"health,omitempty"`
	LeadID               *string                          `json:"leadId,omitempty"`
	MemberIDs            []string                         `json:"memberIds,omitempty"`
	LabelIDs             []string                         `json:"labelIds,omitempty"`
	TeamIDs              []string                         `json:"teamIds,omitempty"`
	DependencyIDs        []string                         `json:"dependencyIds,omitempty"`
	DependencyRelations  []ProjectDependencyRelationInput `json:"dependencyRelations,omitempty"`
	Initiatives          []string                         `json:"initiatives,omitempty"`
	Customers            []string                         `json:"customers,omitempty"`
	Milestones           []string                         `json:"milestones,omitempty"`
	MilestoneDetails     []ProjectMilestoneMutationInput  `json:"milestoneDetails,omitempty"`
	StartDate            *string                          `json:"startDate,omitempty"`
	StartDateResolution  *string                          `json:"startDateResolution,omitempty"`
	TargetDate           *string                          `json:"targetDate,omitempty"`
	TargetDateResolution *string                          `json:"targetDateResolution,omitempty"`
	SlackChannelID       *string                          `json:"slackChannelId,omitempty"`
	SlackChannelName     *string                          `json:"slackChannelName,omitempty"`
	UpdateCadence        *string                          `json:"updateCadence,omitempty"`
	UpdateSchedule       *ProjectUpdateSchedule           `json:"updateSchedule,omitempty"`
	Archived             *bool                            `json:"archived,omitempty"`
	TemplateID           string                           `json:"templateId,omitempty"`
}

type ProjectDependencyRelationInput struct {
	ProjectID string `json:"projectId"`
	Type      string `json:"type"`
}

type InitiativeMutationInput struct {
	Name                 *string                      `json:"name,omitempty"`
	Summary              *string                      `json:"summary,omitempty"`
	Description          *string                      `json:"description,omitempty"`
	Icon                 *string                      `json:"icon,omitempty"`
	Color                *string                      `json:"color,omitempty"`
	Status               *string                      `json:"status,omitempty"`
	Priority             *int                         `json:"priority,omitempty"`
	Position             *float64                     `json:"position,omitempty"`
	Health               *string                      `json:"health,omitempty"`
	OwnerID              *string                      `json:"ownerId,omitempty"`
	LeadTeamID           *string                      `json:"leadTeamId,omitempty"`
	ContributingTeamIDs  *[]string                    `json:"contributingTeamIds,omitempty"`
	LabelIDs             *[]string                    `json:"labelIds,omitempty"`
	ParentInitiativeIDs  *[]string                    `json:"parentInitiativeIds,omitempty"`
	ProjectIDs           *[]string                    `json:"projectIds,omitempty"`
	TargetDate           *string                      `json:"targetDate,omitempty"`
	TargetDateResolution *string                      `json:"targetDateResolution,omitempty"`
	Favorite             *bool                        `json:"favorite,omitempty"`
	Subscribed           *bool                        `json:"subscribed,omitempty"`
	NotificationRules    *InitiativeNotificationRules `json:"notificationRules,omitempty"`
	UpdateSchedule       *InitiativeUpdateSchedule    `json:"updateSchedule,omitempty"`
}

type InitiativeUpdateCreateInput struct {
	Body     string         `json:"body"`
	BodyData map[string]any `json:"bodyData,omitempty"`
	Health   string         `json:"health"`
}

type ProjectResourceMutationInput struct {
	Type          *string   `json:"type,omitempty"`
	Title         *string   `json:"title,omitempty"`
	URL           *string   `json:"url,omitempty"`
	DocumentID    *string   `json:"documentId,omitempty"`
	PinnedTeamIDs *[]string `json:"pinnedTeamIds,omitempty"`
}

type ProjectMilestoneMutationInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	TargetDate  *string `json:"targetDate,omitempty"`
	// TemplateMilestoneID links a milestone sent with a new project to the
	// project template milestone it came from (template issues follow it).
	TemplateMilestoneID string `json:"templateMilestoneId,omitempty"`
}

type ProjectUpdateCreateInput struct {
	Body     string         `json:"body"`
	BodyData map[string]any `json:"bodyData,omitempty"`
	Health   string         `json:"health"`
}

type ProjectUpdateMutationInput struct {
	Body     *string        `json:"body,omitempty"`
	BodyData map[string]any `json:"bodyData,omitempty"`
	Health   *string        `json:"health,omitempty"`
}

type CommentCreateInput struct {
	Body       string         `json:"body"`
	BodyData   map[string]any `json:"bodyData,omitempty"`
	ParentID   *string        `json:"parentId,omitempty"`
	AnchorID   string         `json:"anchorId,omitempty"`
	QuotedText string         `json:"quotedText,omitempty"`
	// CreateAsUser names the external person an actor=app token acts for
	// ("<name> via <App>", at most 100 characters). Rejected for user tokens.
	CreateAsUser *string `json:"createAsUser,omitempty"`
	// DisplayIconURL is that person's avatar; requires createAsUser.
	DisplayIconURL *string `json:"displayIconUrl,omitempty"`
}

type CommentUpdateInput struct {
	Body            string                `json:"body"`
	BodyData        map[string]any        `json:"bodyData,omitempty"`
	ExpectedVersion *int64                `json:"expectedVersion,omitempty"`
	Resolved        *bool                 `json:"resolved,omitempty"`
	ThreadSummary   *CommentThreadSummary `json:"threadSummary,omitempty"`
}
type ReactionInput struct {
	Emoji string `json:"emoji"`
	// CommentID and Added are set by the server (not the client) so the
	// domain event records which comment was reacted to and whether the
	// reaction was added or removed.
	CommentID string `json:"commentId,omitempty"`
	Added     *bool  `json:"added,omitempty"`
}

type IssueUpdateInput struct {
	ExpectedVersion         *int64         `json:"expectedVersion,omitempty"`
	Title                   *string        `json:"title,omitempty"`
	Description             *string        `json:"description,omitempty"`
	DescriptionState        *string        `json:"descriptionState,omitempty"`
	DescriptionData         map[string]any `json:"descriptionData,omitempty"`
	ContentState            *string        `json:"contentState,omitempty"`
	ExpectedDocumentVersion *int64         `json:"expectedDocumentVersion,omitempty"`
	DocumentUpdateIDs       []string       `json:"documentUpdateIds,omitempty"`
	TeamID                  *string        `json:"teamId,omitempty"` // moves the issue to another team: a new identifier (the old one keeps resolving), the equivalent status; it leaves its cycle and the labels that team cannot use
	StateID                 *string        `json:"stateId,omitempty"`
	Priority                *int           `json:"priority,omitempty"`
	Estimate                *float64       `json:"estimate,omitempty"`
	AssigneeID              *string        `json:"assigneeId,omitempty"`
	DelegateID              *string        `json:"delegateId,omitempty"`
	ProjectID               *string        `json:"projectId,omitempty"`
	ProjectMilestoneID      *string        `json:"projectMilestoneId,omitempty"`
	CycleID                 *string        `json:"cycleId,omitempty"`
	DueDate                 *string        `json:"dueDate,omitempty"`
	SLABreachesAt           *string        `json:"slaBreachesAt,omitempty"`
	SLAType                 *string        `json:"slaType,omitempty"`
	LabelIDs                *[]string      `json:"labelIds,omitempty"`
	SubscriberIDs           *[]string      `json:"subscriberIds,omitempty"`
	Archived                *bool          `json:"archived,omitempty"`
	ParentID                *string        `json:"parentId,omitempty"`
	SortOrder               *float64       `json:"sortOrder,omitempty"`
	Icon                    *string        `json:"icon,omitempty"`
	Recurrence              *string        `json:"recurrence,omitempty"`       // "" stops; DueDate is the current due date
	NextOccurrenceAt        *string        `json:"nextOccurrenceAt,omitempty"` // legacy alias for the first due date
	// SnoozedUntil hides a triage issue until the time passes; "" clears it.
	SnoozedUntil *string `json:"snoozedUntil,omitempty"`
}

type IssueLinkInput struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

type IssueReminderInput struct {
	RemindAt string `json:"remindAt"`
}

type IssueLoopRunInput struct {
	Prompt string `json:"prompt,omitempty"`
}

type CycleMutationInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	StartsAt    *string `json:"startsAt,omitempty"`
	EndsAt      *string `json:"endsAt,omitempty"`
	// ShiftFollowing moves an upcoming cycle's start with its length kept and
	// moves every later upcoming cycle by the same number of days. Without it
	// only the start moves and the end stays.
	ShiftFollowing *bool `json:"shiftFollowing,omitempty"`
	// Capacity sets the cycle's capacity override; null or 0 returns it to
	// the automatic capacity.
	Capacity         OptionalInt               `json:"capacity,omitzero"`
	CapacityByMember map[string]map[string]int `json:"capacityByMember,omitempty"`
	Favorite         *bool                     `json:"favorite,omitempty"`
	Status           *string                   `json:"status,omitempty"`
	Insight          map[string]string         `json:"insight,omitempty"`
}

type CycleSettingsMutationInput struct {
	Enabled          *bool `json:"enabled,omitempty"`
	DurationWeeks    *int  `json:"durationWeeks,omitempty"`
	CooldownWeeks    *int  `json:"cooldownWeeks,omitempty"`
	StartsOn         *int  `json:"startsOn,omitempty"`
	UpcomingCount    *int  `json:"upcomingCount,omitempty"`
	AutoAddStarted   *bool `json:"autoAddStarted,omitempty"`
	AutoAddCompleted *bool `json:"autoAddCompleted,omitempty"`
	Capacity         *int  `json:"capacity,omitempty"`
	AutoCreate       *bool `json:"autoCreate,omitempty"`
	AutoAddActive    *bool `json:"autoAddActive,omitempty"`
	AutoAddDueDate   *bool `json:"autoAddDueDate,omitempty"`
	AutoMigrate      *bool `json:"autoMigrate,omitempty"`
	FavoriteView     *bool `json:"favoriteView,omitempty"`
}

type NotificationBatchInput struct {
	Action       string   `json:"action"`
	IDs          []string `json:"ids,omitempty"`
	SnoozedUntil *string  `json:"snoozedUntil,omitempty"`
}

type WorkflowStateMutationInput struct {
	Name               *string  `json:"name,omitempty"`
	Description        *string  `json:"description,omitempty"`
	Color              *string  `json:"color,omitempty"`
	Type               *string  `json:"type,omitempty"`
	Position           *float64 `json:"position,omitempty"`
	Default            *bool    `json:"default,omitempty"`
	ReplacementStateID string   `json:"replacementStateId,omitempty"`
}

type WorkflowStateReorderInput struct {
	StateIDs []string `json:"stateIds"`
}

type TeamSettingsMutationInput struct {
	Description                         *string                    `json:"description,omitempty"`
	Timezone                            *string                    `json:"timezone,omitempty"`
	EstimateType                        *string                    `json:"estimateType,omitempty"`
	DefaultStateID                      *string                    `json:"defaultStateId,omitempty"`
	DefaultPriority                     *int                       `json:"defaultPriority,omitempty"`
	IssueEmailEnabled                   *bool                      `json:"issueEmailEnabled,omitempty"`
	DetailedHistory                     *bool                      `json:"detailedHistory,omitempty"`
	Identifier                          *string                    `json:"identifier,omitempty"`
	Access                              *string                    `json:"access,omitempty"`
	MembershipRestriction               *string                    `json:"membershipRestriction,omitempty"`
	SettingsPermission                  *string                    `json:"settingsPermission,omitempty"`
	LabelPermission                     *string                    `json:"labelPermission,omitempty"`
	TemplatePermission                  *string                    `json:"templatePermission,omitempty"`
	AgentSkillPermission                *string                    `json:"agentSkillPermission,omitempty"`
	LoopPermission                      *string                    `json:"loopPermission,omitempty"`
	MemberPermission                    *string                    `json:"memberPermission,omitempty"`
	PinnedViewPermission                *string                    `json:"pinnedViewPermission,omitempty"`
	DefaultIssueTemplateForMembersID    *string                    `json:"defaultIssueTemplateForMembersId,omitempty"`
	DefaultIssueTemplateForNonMembersID *string                    `json:"defaultIssueTemplateForNonMembersId,omitempty"`
	DefaultProjectTemplateID            *string                    `json:"defaultProjectTemplateId,omitempty"`
	EstimateAllowZero                   *bool                      `json:"estimateAllowZero,omitempty"`
	EstimateExtended                    *bool                      `json:"estimateExtended,omitempty"`
	EstimateCountUnestimated            *bool                      `json:"estimateCountUnestimated,omitempty"`
	IssueSharingEnabled                 *bool                      `json:"issueSharingEnabled,omitempty"`
	IssueSharingPermission              *string                    `json:"issueSharingPermission,omitempty"`
	ApplyToSubTeams                     *bool                      `json:"applyToSubTeams,omitempty"`
	SlackChannelID                      *string                    `json:"slackChannelId,omitempty"`
	SlackChannelName                    *string                    `json:"slackChannelName,omitempty"`
	SlackNotifications                  *map[string]bool           `json:"slackNotifications,omitempty"`
	PRAutomations                       *map[string]string         `json:"prAutomations,omitempty"`
	AutoCloseParents                    *bool                      `json:"autoCloseParents,omitempty"`
	AutoCloseSubIssues                  *bool                      `json:"autoCloseSubIssues,omitempty"`
	AutoCloseStale                      *bool                      `json:"autoCloseStale,omitempty"`
	StaleMonths                         *int                       `json:"staleMonths,omitempty"`
	StaleStatusID                       *string                    `json:"staleStatusId,omitempty"`
	AutoArchiveMonths                   *int                       `json:"autoArchiveMonths,omitempty"`
	ProgressOrder                       *string                    `json:"progressOrder,omitempty"`
	ReleaseAutomations                  *[]TeamAutomationRule      `json:"releaseAutomations,omitempty"`
	TriageEnabled                       *bool                      `json:"triageEnabled,omitempty"`
	TriageRequirePriority               *bool                      `json:"triageRequirePriority,omitempty"`
	TriageAction                        *string                    `json:"triageAction,omitempty"`
	TriageActionUserIDs                 *[]string                  `json:"triageActionUserIds,omitempty"`
	TriageRules                         *[]TeamAutomationRule      `json:"triageRules,omitempty"`
	AgentSkills                         *[]TeamAgentSkill          `json:"agentSkills,omitempty"`
	AgentConnectors                     *[]TeamAgentConnector      `json:"agentConnectors,omitempty"`
	ProjectUpdatePrompt                 *string                    `json:"projectUpdatePrompt,omitempty"`
	AgentGuidance                       *string                    `json:"agentGuidance,omitempty"`
	ResolvedSummaries                   *bool                      `json:"resolvedThreadSummaries,omitempty"`
	ShowInitiatives                     *bool                      `json:"showInitiatives,omitempty"`
	InheritIssueEstimation              *bool                      `json:"inheritIssueEstimation,omitempty"`
	InheritWorkflowStatuses             *bool                      `json:"inheritWorkflowStatuses,omitempty"`
	InheritProjectStatuses              *bool                      `json:"inheritProjectStatuses,omitempty"`
	InheritCycles                       *bool                      `json:"inheritCycles,omitempty"`
	IssueViewDefaults                   map[string]json.RawMessage `json:"issueViewDefaults,omitempty"`
	IssueViewInsights                   map[string]json.RawMessage `json:"issueViewInsights,omitempty"`
	ParentTeamID                        *string                    `json:"parentTeamId,omitempty"`
}

type IssueTemplateMutationInput struct {
	TeamID           *string              `json:"teamId,omitempty"`
	VisibilityTeamID *string              `json:"visibilityTeamId,omitempty"`
	Icon             *string              `json:"icon,omitempty"`
	Color            *string              `json:"color,omitempty"`
	Name             *string              `json:"name,omitempty"`
	Title            *string              `json:"title,omitempty"`
	Description      *string              `json:"description,omitempty"`
	Body             *string              `json:"body,omitempty"`
	StateID          *string              `json:"stateId,omitempty"`
	Priority         *int                 `json:"priority,omitempty"`
	AssigneeID       *string              `json:"assigneeId,omitempty"`
	ProjectID        *string              `json:"projectId,omitempty"`
	LabelIDs         *[]string            `json:"labelIds,omitempty"`
	TemplateType     *string              `json:"templateType,omitempty"`
	FormFields       *[]TemplateFormField `json:"formFields,omitempty"`
	SubIssues        *[]TemplateSubIssue  `json:"subIssues,omitempty"`
}

type IssueLabelMutationInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Color       *string `json:"color,omitempty"`
	ArchivedAt  *string `json:"archivedAt,omitempty"`
	// ResourceType and GroupID are honored when a team label is created.
	ResourceType *string `json:"resourceType,omitempty"`
	GroupID      *string `json:"groupId,omitempty"`
}

type BatchIssueUpdateInput struct {
	IssueIDs []string         `json:"issueIds"`
	Update   IssueUpdateInput `json:"update"`
}

// NotificationMutationInput supports one lifecycle transition at a time, or
// an atomic combination such as marking a notification read and favorited.
// SnoozedUntil deliberately uses RawMessage so both an RFC3339 timestamp and
// JSON null (clear snooze) can be distinguished from an omitted field.
type NotificationMutationInput struct {
	Read         *bool           `json:"read,omitempty"`
	Favorite     *bool           `json:"favorite,omitempty"`
	Archived     *bool           `json:"archived,omitempty"`
	Deleted      *bool           `json:"deleted,omitempty"`
	SnoozedUntil json.RawMessage `json:"snoozedUntil,omitempty"`
}

type RelationCreateInput struct {
	Type           string `json:"type"`
	RelatedIssueID string `json:"relatedIssueId"`
}

type DomainEvent struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	AggregateID    string          `json:"aggregateId"`
	Payload        json.RawMessage `json:"payload"`
	PreviousValues json.RawMessage `json:"previousValues,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	// ActorID and Snapshot are transient (never persisted): when a committed
	// mutation is published they carry the acting user and the enriched
	// realtime payload ({input, entity, deleted}) for webhook deliveries.
	ActorID  string          `json:"-"`
	Snapshot json.RawMessage `json:"-"`
}

type RealtimeEvent struct {
	ID string `json:"id"`
	// Seq is the event's change-log cursor within its workspace. It is zero
	// for events that are not persisted (presence, agent task streaming).
	Seq         int64           `json:"seq,omitempty"`
	Type        string          `json:"type"`
	AggregateID string          `json:"aggregateId,omitempty"`
	ActorID     string          `json:"actorId,omitempty"`
	ClientID    string          `json:"clientId,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type Presence struct {
	ClientID   string    `json:"clientId"`
	User       User      `json:"user"`
	IssueID    string    `json:"issueId,omitempty"`
	DocumentID string    `json:"documentId,omitempty"`
	Route      string    `json:"route,omitempty"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type SearchResult struct {
	URL           string         `json:"url,omitempty"`
	SlugID        string         `json:"slugId,omitempty"`
	State         *WorkflowState `json:"state,omitempty"`
	ProjectStatus *ProjectStatus `json:"projectStatus,omitempty"`
	StatusType    string         `json:"statusType,omitempty"`
	StatusName    string         `json:"statusName,omitempty"`
	CreatedAt     time.Time      `json:"createdAt,omitempty"`
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	Subtitle      string         `json:"subtitle,omitempty"`
	Identifier    string         `json:"identifier,omitempty"`
	ParentID      string         `json:"parentId,omitempty"`
	ParentType    string         `json:"parentType,omitempty"`
	Email         string         `json:"email,omitempty"`
	Icon          string         `json:"icon,omitempty"`
	Color         string         `json:"color,omitempty"`
	Score         int            `json:"score"`
	UpdatedAt     time.Time      `json:"updatedAt,omitempty"`
}

type SearchHistoryEntry struct {
	Query      string    `json:"query"`
	UseCount   int       `json:"useCount"`
	LastUsedAt time.Time `json:"lastUsedAt"`
}

type RecentResource struct {
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	LastViewedAt time.Time `json:"lastViewedAt"`
}

type SearchResponse struct {
	Results []SearchResult       `json:"results"`
	History []SearchHistoryEntry `json:"history"`
	Recent  []RecentResource     `json:"recent"`
}

// AgentWebAccessEnabled reports whether Flow Agent may use the web in the workspace: the admin's explicit choice when
// there is one, otherwise on (unless an older client stored the disabled flag). HIPAA mode always turns it off.
func (settings WorkspaceSettings) AgentWebAccessEnabled() bool {
	if settings.HIPAACompliance {
		return false
	}
	if settings.AgentWebAccess != nil {
		return *settings.AgentWebAccess
	}
	return !settings.AgentWebSearchDisabled
}

// MCPConnectorsActive reports whether Flow Agent may use MCP connectors in the workspace: the admin's explicit choice
// when there is one, otherwise on. HIPAA mode always turns it off.
func (settings WorkspaceSettings) MCPConnectorsActive() bool {
	if settings.HIPAACompliance {
		return false
	}
	if settings.MCPConnectors != nil {
		return *settings.MCPConnectors
	}
	return true
}

// SetMCPConnectors records an admin's explicit choice and keeps the older mirror in step.
func (settings *WorkspaceSettings) SetMCPConnectors(enabled bool) {
	settings.MCPConnectors = &enabled
	settings.MCPConnectorsEnabled = enabled
}

// SetAgentWebAccess records an admin's explicit web access choice and keeps the older flags in step.
func (settings *WorkspaceSettings) SetAgentWebAccess(enabled bool) {
	settings.AgentWebAccess = &enabled
	settings.AgentWebSearch = enabled
	settings.AgentWebSearchDisabled = !enabled
}
