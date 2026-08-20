package domain

import (
	"encoding/json"
	"time"
)

type User struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DisplayName   string `json:"displayName"`
	Email         string `json:"email"`
	AvatarURL     string `json:"avatarUrl,omitempty"`
	Active        bool   `json:"active"`
	EmailVerified bool   `json:"emailVerified"`
}

type WorkspaceMember struct {
	User       User       `json:"user"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	JoinedAt   time.Time  `json:"joinedAt"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

type TeamMember struct {
	TeamID   string    `json:"teamId"`
	UserID   string    `json:"userId"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joinedAt"`
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

type AuthSession struct {
	User        User                  `json:"user"`
	Memberships []WorkspaceMembership `json:"memberships"`
	ExpiresAt   time.Time             `json:"expiresAt"`
}

type AccountSession struct {
	ID         string    `json:"id"`
	Current    bool      `json:"current"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URLKey    string    `json:"urlKey"`
	Icon      string    `json:"icon,omitempty"`
	Color     string    `json:"color,omitempty"`
	Region    string    `json:"region,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

type WorkspaceMembership struct {
	Workspace  Workspace `json:"workspace"`
	Role       string    `json:"role"`
	JoinedAt   time.Time `json:"joinedAt"`
	IssueCount int       `json:"issueCount"`
}

type AccountBootstrap struct {
	Viewer           User                  `json:"viewer"`
	Workspaces       []WorkspaceMembership `json:"workspaces"`
	LastWorkspaceKey string                `json:"lastWorkspaceKey,omitempty"`
}

type Team struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Key     string `json:"key"`
	Color   string `json:"color"`
	Icon    string `json:"icon,omitempty"`
	Private bool   `json:"private,omitempty"`
}

type Customer struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	LogoURL       string    `json:"logoUrl,omitempty"`
	OwnerID       string    `json:"ownerId,omitempty"`
	Status        string    `json:"status"`
	Tier          string    `json:"tier,omitempty"`
	AnnualRevenue float64   `json:"annualRevenue,omitempty"`
	Size          int       `json:"size,omitempty"`
	Domains       []string  `json:"domains"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
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
	Content      string         `json:"content"`
	ContentState string         `json:"contentState"`
	ContentData  map[string]any `json:"contentData"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

type Document struct {
	ID            string             `json:"id"`
	SlugID        string             `json:"slugId"`
	Title         string             `json:"title"`
	Icon          string             `json:"icon,omitempty"`
	Content       string             `json:"content"`
	ContentState  string             `json:"contentState,omitempty"`
	ContentData   map[string]any     `json:"contentData,omitempty"`
	Creator       User               `json:"creator"`
	ProjectIDs    []string           `json:"projectIds"`
	TeamIDs       []string           `json:"teamIds"`
	IssueID       string             `json:"issueId,omitempty"`
	SubscriberIDs []string           `json:"subscriberIds"`
	Favorite      bool               `json:"favorite"`
	ArchivedAt    *time.Time         `json:"archivedAt,omitempty"`
	CreatedAt     time.Time          `json:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt"`
	Revisions     []DocumentRevision `json:"revisions"`
}

type DocumentRevision struct {
	ID           string         `json:"id"`
	DocumentID   string         `json:"documentId"`
	Title        string         `json:"title"`
	Content      string         `json:"content"`
	ContentState string         `json:"contentState,omitempty"`
	ContentData  map[string]any `json:"contentData,omitempty"`
	Author       User           `json:"author"`
	CreatedAt    time.Time      `json:"createdAt"`
}

type DocumentTemplate struct {
	ID           string         `json:"id"`
	TeamID       string         `json:"teamId"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	Title        string         `json:"title,omitempty"`
	Icon         string         `json:"icon,omitempty"`
	Content      string         `json:"content,omitempty"`
	ContentState string         `json:"contentState,omitempty"`
	ContentData  map[string]any `json:"contentData,omitempty"`
	Creator      User           `json:"creator"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// Issue follows Flow's public GraphQL entity naming and relationship shape.
type Issue struct {
	ID                 string              `json:"id"`
	Version            int64               `json:"version"`
	Identifier         string              `json:"identifier"`
	Number             int                 `json:"number"`
	Title              string              `json:"title"`
	Description        string              `json:"description"`
	DescriptionState   string              `json:"descriptionState,omitempty"`
	DocumentContent    *DocumentContent    `json:"documentContent,omitempty"`
	Priority           int                 `json:"priority"`
	PriorityLabel      string              `json:"priorityLabel"`
	SortOrder          float64             `json:"sortOrder"`
	Estimate           *float64            `json:"estimate,omitempty"`
	DueDate            *string             `json:"dueDate,omitempty"`
	CreatedAt          time.Time           `json:"createdAt"`
	UpdatedAt          time.Time           `json:"updatedAt"`
	CompletedAt        *time.Time          `json:"completedAt,omitempty"`
	CanceledAt         *time.Time          `json:"canceledAt,omitempty"`
	ArchivedAt         *time.Time          `json:"archivedAt,omitempty"`
	Team               Team                `json:"team"`
	State              WorkflowState       `json:"state"`
	Assignee           *User               `json:"assignee,omitempty"`
	Creator            User                `json:"creator"`
	Labels             []IssueLabel        `json:"labels"`
	Project            *ProjectSummary     `json:"project,omitempty"`
	ProjectMilestoneID *string             `json:"projectMilestoneId,omitempty"`
	CycleID            *string             `json:"cycleId,omitempty"`
	ParentID           *string             `json:"parentId,omitempty"`
	Recurrence         string              `json:"recurrence,omitempty"`
	NextOccurrenceAt   *time.Time          `json:"nextOccurrenceAt,omitempty"`
	SubscriberIDs      []string            `json:"subscriberIds"`
	Reactions          map[string][]string `json:"reactions"`
	SubIssueIDs        []string            `json:"subIssueIds"`
	Relations          []IssueRelation     `json:"relations"`
	Attachments        []Attachment        `json:"attachments"`
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
	Capacity    int       `json:"capacity"`
	Favorite    bool      `json:"favorite"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
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
}

type TeamSettings struct {
	TeamID            string `json:"teamId"`
	Description       string `json:"description,omitempty"`
	Timezone          string `json:"timezone"`
	EstimateType      string `json:"estimateType"`
	DefaultStateID    string `json:"defaultStateId"`
	DefaultPriority   int    `json:"defaultPriority"`
	IssueEmailEnabled bool   `json:"issueEmailEnabled"`
	DetailedHistory   bool   `json:"detailedHistory"`
}

type IssueTemplate struct {
	ID           string              `json:"id"`
	TeamID       string              `json:"teamId"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Body         string              `json:"body,omitempty"`
	StateID      string              `json:"stateId,omitempty"`
	Priority     int                 `json:"priority"`
	AssigneeID   string              `json:"assigneeId,omitempty"`
	ProjectID    string              `json:"projectId,omitempty"`
	LabelIDs     []string            `json:"labelIds"`
	Scope        string              `json:"scope,omitempty"`
	TemplateType string              `json:"templateType,omitempty"`
	FormFields   []TemplateFormField `json:"formFields,omitempty"`
	Creator      User                `json:"creator"`
	CreatedAt    time.Time           `json:"createdAt"`
	UpdatedAt    time.Time           `json:"updatedAt"`
}

type TemplateFormField struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Options     []string `json:"options,omitempty"`
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
}

type ProjectStatus struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Color    string  `json:"color"`
	Type     string  `json:"type"`
	Position float64 `json:"position"`
}

type Project struct {
	ID                   string                       `json:"id"`
	Name                 string                       `json:"name"`
	SlugID               string                       `json:"slugId"`
	Summary              string                       `json:"summary"`
	Description          string                       `json:"description"`
	Icon                 string                       `json:"icon,omitempty"`
	Color                string                       `json:"color"`
	Priority             int                          `json:"priority"`
	PriorityLabel        string                       `json:"priorityLabel"`
	Progress             float64                      `json:"progress"`
	Health               string                       `json:"health"`
	Status               ProjectStatus                `json:"status"`
	Lead                 *User                        `json:"lead,omitempty"`
	MemberIDs            []string                     `json:"memberIds"`
	LabelIDs             []string                     `json:"labelIds"`
	TeamIDs              []string                     `json:"teamIds"`
	DependencyIDs        []string                     `json:"dependencyIds"`
	Initiatives          []string                     `json:"initiatives"`
	Customers            []string                     `json:"customers"`
	Resources            []ProjectResource            `json:"resources"`
	Milestones           []ProjectMilestone           `json:"milestones"`
	Comments             []Comment                    `json:"comments"`
	DescriptionRevisions []ProjectDescriptionRevision `json:"descriptionRevisions"`
	UpdateCadence        string                       `json:"updateCadence"`
	StartDate            *string                      `json:"startDate,omitempty"`
	TargetDate           *string                      `json:"targetDate,omitempty"`
	IssueCount           int                          `json:"issueCount"`
	CreatedAt            time.Time                    `json:"createdAt"`
	UpdatedAt            time.Time                    `json:"updatedAt"`
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
	CreatedAt     time.Time `json:"createdAt"`
}

type ProjectMilestone struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	TargetDate  *string   `json:"targetDate,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ProjectUpdate mirrors the update stream shown on a Flow project. Updates
// belong to the project aggregate so they persist alongside project metadata.
type ProjectUpdate struct {
	ID        string              `json:"id"`
	ProjectID string              `json:"projectId"`
	Body      string              `json:"body"`
	Health    string              `json:"health"`
	CreatedAt time.Time           `json:"createdAt"`
	EditedAt  *time.Time          `json:"editedAt,omitempty"`
	User      User                `json:"user"`
	Comments  []Comment           `json:"comments"`
	Reactions map[string][]string `json:"reactions"`
	DueAt     *time.Time          `json:"dueAt,omitempty"`
	Missing   bool                `json:"missing,omitempty"`
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
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
}

type Release struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Version       string     `json:"version"`
	Description   string     `json:"description"`
	Status        string     `json:"status"`
	TargetDate    *string    `json:"targetDate,omitempty"`
	ProjectIDs    []string   `json:"projectIds"`
	IssueIDs      []string   `json:"issueIds"`
	SubscriberIDs []string   `json:"subscriberIds"`
	Creator       User       `json:"creator"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
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

type ProjectTemplate struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Icon          string    `json:"icon,omitempty"`
	Color         string    `json:"color,omitempty"`
	StatusID      string    `json:"statusId,omitempty"`
	Priority      int       `json:"priority"`
	TeamIDs       []string  `json:"teamIds"`
	LabelIDs      []string  `json:"labelIds"`
	LeadID        string    `json:"leadId,omitempty"`
	MemberIDs     []string  `json:"memberIds,omitempty"`
	DependencyIDs []string  `json:"dependencyIds,omitempty"`
	InitiativeIDs []string  `json:"initiativeIds,omitempty"`
	IssueIDs      []string  `json:"issueIds,omitempty"`
	Visibility    string    `json:"visibility,omitempty"`
	Creator       User      `json:"creator"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type UserSettings struct {
	UserID            string    `json:"userId"`
	Language          string    `json:"language"`
	HomeView          string    `json:"homeView"`
	DisplayNames      string    `json:"displayNames"`
	FirstDay          string    `json:"firstDay"`
	Emoticons         bool      `json:"emoticons"`
	SendComments      string    `json:"sendComments"`
	FontSize          string    `json:"fontSize"`
	PointerCursor     bool      `json:"pointerCursor"`
	UnderlineLinks    bool      `json:"underlineLinks"`
	InterfaceTheme    string    `json:"interfaceTheme"`
	LightTheme        string    `json:"lightTheme"`
	DarkTheme         string    `json:"darkTheme"`
	DesktopLinks      bool      `json:"desktopLinks"`
	AutoAssign        bool      `json:"autoAssign"`
	AssignStarted     bool      `json:"assignStarted"`
	ReviewAutoAssign  bool      `json:"reviewAutoAssign"`
	BranchFormat      string    `json:"branchFormat"`
	AgentEnabled      bool      `json:"agentEnabled"`
	AgentInstructions string    `json:"agentInstructions"`
	JobTitle          string    `json:"jobTitle,omitempty"`
	Username          string    `json:"username,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type WorkspaceSettings struct {
	FiscalMonth          string          `json:"fiscalMonth"`
	GuestsAllowed        bool            `json:"guestsAllowed"`
	RequireTwoFactor     bool            `json:"requireTwoFactor"`
	SessionDurationDays  int             `json:"sessionDurationDays"`
	AllowedDomains       []string        `json:"allowedDomains"`
	InvitePermission     string          `json:"invitePermission"`
	TeamCreatePermission string          `json:"teamCreatePermission"`
	LabelPermission      string          `json:"labelPermission"`
	TemplatePermission   string          `json:"templatePermission"`
	APIKeyPermission     string          `json:"apiKeyPermission"`
	FeatureFlags         map[string]bool `json:"featureFlags"`
	BillingEmail         string          `json:"billingEmail,omitempty"`
	Plan                 string          `json:"plan"`
	UpdatedAt            time.Time       `json:"updatedAt"`
}

type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	SecretHash string     `json:"secretHash,omitempty"`
	CreatorID  string     `json:"creatorId"`
	Scopes     []string   `json:"scopes"`
	TeamIDs    []string   `json:"teamIds"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

type OAuthApplication struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	ClientID     string    `json:"clientId"`
	ClientSecret string    `json:"clientSecret,omitempty"`
	RedirectURIs []string  `json:"redirectUris"`
	Scopes       []string  `json:"scopes"`
	CreatorID    string    `json:"creatorId"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type IntegrationConnection struct {
	ID          string            `json:"id"`
	Provider    string            `json:"provider"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Config      map[string]string `json:"config,omitempty"`
	ConnectedBy string            `json:"connectedBy"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
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

type Favorite struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	Position     float64   `json:"position"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Subscription struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	Events       []string  `json:"events,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type SubscriptionMutationInput struct {
	Events *[]string `json:"events,omitempty"`
}

type AuditLogEntry struct {
	ID           string         `json:"id"`
	Actor        User           `json:"actor"`
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
	DeletedBy    User            `json:"deletedBy"`
	DeletedAt    time.Time       `json:"deletedAt"`
	ExpiresAt    time.Time       `json:"expiresAt"`
}

type ImportJob struct {
	ID        string              `json:"id"`
	UserID    string              `json:"userId"`
	Filename  string              `json:"filename"`
	Format    string              `json:"format"`
	Status    string              `json:"status"`
	Headers   []string            `json:"headers"`
	Rows      []map[string]string `json:"rows,omitempty"`
	Mapping   map[string]string   `json:"mapping,omitempty"`
	Imported  int                 `json:"imported"`
	Errors    []string            `json:"errors"`
	CreatedAt time.Time           `json:"createdAt"`
	UpdatedAt time.Time           `json:"updatedAt"`
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

// Initiative is the workspace-level planning aggregate above projects.
// ProjectIDs intentionally stores stable project IDs; list progress and health
// are projections computed by the client from the associated projects.
type Initiative struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	SlugID        string               `json:"slugId"`
	Summary       string               `json:"summary"`
	Description   string               `json:"description"`
	Icon          string               `json:"icon,omitempty"`
	Color         string               `json:"color"`
	Status        string               `json:"status"`
	Priority      int                  `json:"priority"`
	PriorityLabel string               `json:"priorityLabel"`
	Health        string               `json:"health"`
	Owner         *User                `json:"owner,omitempty"`
	LabelIDs      []string             `json:"labelIds"`
	ProjectIDs    []string             `json:"projectIds"`
	Resources     []InitiativeResource `json:"resources"`
	Comments      []Comment            `json:"comments"`
	TargetDate    *string              `json:"targetDate,omitempty"`
	Favorite      bool                 `json:"favorite"`
	Subscribed    bool                 `json:"subscribed"`
	CreatedAt     time.Time            `json:"createdAt"`
	UpdatedAt     time.Time            `json:"updatedAt"`
}

type InitiativeResource struct {
	ID           string    `json:"id"`
	InitiativeID string    `json:"initiativeId"`
	Type         string    `json:"type"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	CreatedAt    time.Time `json:"createdAt"`
}

type InitiativeUpdate struct {
	ID           string              `json:"id"`
	InitiativeID string              `json:"initiativeId"`
	Body         string              `json:"body"`
	Health       string              `json:"health"`
	CreatedAt    time.Time           `json:"createdAt"`
	EditedAt     *time.Time          `json:"editedAt,omitempty"`
	User         User                `json:"user"`
	Comments     []Comment           `json:"comments"`
	Reactions    map[string][]string `json:"reactions"`
}

type Comment struct {
	ID        string              `json:"id"`
	Version   int64               `json:"version"`
	Body      string              `json:"body"`
	BodyData  map[string]any      `json:"bodyData,omitempty"`
	ParentID  *string             `json:"parentId,omitempty"`
	Reactions map[string][]string `json:"reactions"`
	CreatedAt time.Time           `json:"createdAt"`
	EditedAt  *time.Time          `json:"editedAt,omitempty"`
	User      User                `json:"user"`
}

type ActivityEvent struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	CreatedAt time.Time         `json:"createdAt"`
	Actor     User              `json:"actor"`
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
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type NotificationChannelPreferences struct {
	Enabled    bool            `json:"enabled"`
	Categories map[string]bool `json:"categories"`
}

type NotificationPreferences struct {
	UserID            string                         `json:"userId"`
	Inbox             NotificationChannelPreferences `json:"inbox"`
	Email             NotificationChannelPreferences `json:"email"`
	Desktop           NotificationChannelPreferences `json:"desktop"`
	EmailFormat       string                         `json:"emailFormat"`
	DelayLowPriority  bool                           `json:"delayLowPriority"`
	ImmediateUrgent   bool                           `json:"immediateUrgent"`
	SoundEnabled      bool                           `json:"soundEnabled"`
	DesktopPermission string                         `json:"desktopPermission,omitempty"`
	UpdatedAt         time.Time                      `json:"updatedAt"`
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
	Workspace               Workspace                          `json:"workspace"`
	Viewer                  User                               `json:"viewer"`
	Users                   []User                             `json:"users"`
	Teams                   []Team                             `json:"teams"`
	Customers               []Customer                         `json:"customers"`
	States                  []WorkflowState                    `json:"states"`
	Labels                  []IssueLabel                       `json:"labels"`
	LabelGroups             []LabelGroup                       `json:"labelGroups"`
	Issues                  []Issue                            `json:"issues"`
	Cycles                  []Cycle                            `json:"cycles"`
	CycleSettings           map[string]CycleSettings           `json:"cycleSettings"`
	TeamSettings            map[string]TeamSettings            `json:"teamSettings"`
	IssueTemplates          []IssueTemplate                    `json:"issueTemplates"`
	ProjectTemplates        []ProjectTemplate                  `json:"projectTemplates"`
	DocumentTemplates       []DocumentTemplate                 `json:"documentTemplates"`
	Documents               []Document                         `json:"documents"`
	CustomerRequests        []CustomerRequest                  `json:"customerRequests"`
	Releases                []Release                          `json:"releases"`
	Asks                    []Ask                              `json:"asks"`
	SLARules                []SLARule                          `json:"slaRules"`
	IssueSLAs               []IssueSLA                         `json:"issueSlas"`
	SLAEvents               []SLAEvent                         `json:"slaEvents"`
	Drafts                  []Draft                            `json:"drafts"`
	Favorites               []Favorite                         `json:"favorites"`
	Subscriptions           []Subscription                     `json:"subscriptions"`
	AuditLog                []AuditLogEntry                    `json:"auditLog"`
	Trash                   []TrashEntry                       `json:"trash"`
	ImportJobs              []ImportJob                        `json:"importJobs"`
	ExportJobs              []ExportJob                        `json:"exportJobs"`
	Projects                []Project                          `json:"projects"`
	ProjectStatuses         []ProjectStatus                    `json:"projectStatuses"`
	ProjectDisplayDefault   json.RawMessage                    `json:"projectDisplayDefault,omitempty"`
	ProjectUpdates          map[string][]ProjectUpdate         `json:"projectUpdates"`
	Initiatives             []Initiative                       `json:"initiatives"`
	InitiativeUpdates       map[string][]InitiativeUpdate      `json:"initiativeUpdates"`
	Comments                map[string][]Comment               `json:"comments"`
	Activities              map[string][]ActivityEvent         `json:"activities"`
	SavedViews              []SavedView                        `json:"savedViews"`
	Notifications           []Notification                     `json:"notifications"`
	NotificationPreferences map[string]NotificationPreferences `json:"notificationPreferences"`
	NotificationDeliveries  []NotificationDelivery             `json:"notificationDeliveries"`
	UserSettings            map[string]UserSettings            `json:"userSettings"`
	WorkspaceSettings       WorkspaceSettings                  `json:"workspaceSettings"`
	APIKeys                 []APIKey                           `json:"apiKeys"`
	OAuthApplications       []OAuthApplication                 `json:"oauthApplications"`
	IntegrationConnections  []IntegrationConnection            `json:"integrationConnections"`
	Settings                map[string]any                     `json:"settings"`
	Members                 []WorkspaceMember                  `json:"members"`
	TeamMembers             []TeamMember                       `json:"teamMembers"`
	Invitations             []Invitation                       `json:"invitations"`
	ViewerRole              string                             `json:"viewerRole"`
}

type SavedView struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Icon        string          `json:"icon,omitempty"`
	Color       string          `json:"color,omitempty"`
	Resource    string          `json:"resource"`
	Scope       string          `json:"scope"`
	TeamID      string          `json:"teamId,omitempty"`
	OwnerID     string          `json:"ownerId,omitempty"`
	Favorite    bool            `json:"favorite,omitempty"`
	Subscribed  bool            `json:"subscribed,omitempty"`
	View        string          `json:"view"`
	Filters     json.RawMessage `json:"filters"`
	Display     json.RawMessage `json:"display"`
	Insights    json.RawMessage `json:"insights"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type SavedViewMutationInput struct {
	Name        *string         `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Icon        *string         `json:"icon,omitempty"`
	Color       *string         `json:"color,omitempty"`
	Resource    *string         `json:"resource,omitempty"`
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
	AssigneeID         *string        `json:"assigneeId,omitempty"`
	ProjectID          *string        `json:"projectId,omitempty"`
	ProjectMilestoneID *string        `json:"projectMilestoneId,omitempty"`
	CycleID            *string        `json:"cycleId,omitempty"`
	DueDate            *string        `json:"dueDate,omitempty"`
	LabelIDs           []string       `json:"labelIds,omitempty"`
	TemplateID         string         `json:"templateId,omitempty"`
}

type ProjectMutationInput struct {
	Name          *string  `json:"name,omitempty"`
	Summary       *string  `json:"summary,omitempty"`
	Description   *string  `json:"description,omitempty"`
	Icon          *string  `json:"icon,omitempty"`
	Color         *string  `json:"color,omitempty"`
	StatusID      *string  `json:"statusId,omitempty"`
	Priority      *int     `json:"priority,omitempty"`
	Health        *string  `json:"health,omitempty"`
	LeadID        *string  `json:"leadId,omitempty"`
	MemberIDs     []string `json:"memberIds,omitempty"`
	LabelIDs      []string `json:"labelIds,omitempty"`
	TeamIDs       []string `json:"teamIds,omitempty"`
	DependencyIDs []string `json:"dependencyIds,omitempty"`
	Initiatives   []string `json:"initiatives,omitempty"`
	Customers     []string `json:"customers,omitempty"`
	StartDate     *string  `json:"startDate,omitempty"`
	TargetDate    *string  `json:"targetDate,omitempty"`
	UpdateCadence *string  `json:"updateCadence,omitempty"`
	TemplateID    string   `json:"templateId,omitempty"`
}

type InitiativeMutationInput struct {
	Name        *string   `json:"name,omitempty"`
	Summary     *string   `json:"summary,omitempty"`
	Description *string   `json:"description,omitempty"`
	Icon        *string   `json:"icon,omitempty"`
	Color       *string   `json:"color,omitempty"`
	Status      *string   `json:"status,omitempty"`
	Priority    *int      `json:"priority,omitempty"`
	Health      *string   `json:"health,omitempty"`
	OwnerID     *string   `json:"ownerId,omitempty"`
	LabelIDs    *[]string `json:"labelIds,omitempty"`
	ProjectIDs  *[]string `json:"projectIds,omitempty"`
	TargetDate  *string   `json:"targetDate,omitempty"`
	Favorite    *bool     `json:"favorite,omitempty"`
	Subscribed  *bool     `json:"subscribed,omitempty"`
}

type InitiativeUpdateCreateInput struct {
	Body   string `json:"body"`
	Health string `json:"health"`
}

type ProjectResourceMutationInput struct {
	Type          *string   `json:"type,omitempty"`
	Title         *string   `json:"title,omitempty"`
	URL           *string   `json:"url,omitempty"`
	PinnedTeamIDs *[]string `json:"pinnedTeamIds,omitempty"`
}

type ProjectMilestoneMutationInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	TargetDate  *string `json:"targetDate,omitempty"`
}

type ProjectUpdateCreateInput struct {
	Body   string `json:"body"`
	Health string `json:"health"`
}

type ProjectUpdateMutationInput struct {
	Body   *string `json:"body,omitempty"`
	Health *string `json:"health,omitempty"`
}

type CommentCreateInput struct {
	Body     string         `json:"body"`
	BodyData map[string]any `json:"bodyData,omitempty"`
	ParentID *string        `json:"parentId,omitempty"`
}

type CommentUpdateInput struct {
	Body            string         `json:"body"`
	BodyData        map[string]any `json:"bodyData,omitempty"`
	ExpectedVersion *int64         `json:"expectedVersion,omitempty"`
}
type ReactionInput struct {
	Emoji string `json:"emoji"`
}

type IssueUpdateInput struct {
	ExpectedVersion    *int64         `json:"expectedVersion,omitempty"`
	Title              *string        `json:"title,omitempty"`
	Description        *string        `json:"description,omitempty"`
	DescriptionState   *string        `json:"descriptionState,omitempty"`
	DescriptionData    map[string]any `json:"descriptionData,omitempty"`
	ContentState       *string        `json:"contentState,omitempty"`
	StateID            *string        `json:"stateId,omitempty"`
	Priority           *int           `json:"priority,omitempty"`
	AssigneeID         *string        `json:"assigneeId,omitempty"`
	ProjectID          *string        `json:"projectId,omitempty"`
	ProjectMilestoneID *string        `json:"projectMilestoneId,omitempty"`
	CycleID            *string        `json:"cycleId,omitempty"`
	DueDate            *string        `json:"dueDate,omitempty"`
	LabelIDs           *[]string      `json:"labelIds,omitempty"`
	SubscriberIDs      *[]string      `json:"subscriberIds,omitempty"`
	Archived           *bool          `json:"archived,omitempty"`
	ParentID           *string        `json:"parentId,omitempty"`
	SortOrder          *float64       `json:"sortOrder,omitempty"`
	Recurrence         *string        `json:"recurrence,omitempty"`
	NextOccurrenceAt   *string        `json:"nextOccurrenceAt,omitempty"`
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
	Capacity    *int    `json:"capacity,omitempty"`
	Favorite    *bool   `json:"favorite,omitempty"`
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
}

type NotificationBatchInput struct {
	Action string   `json:"action"`
	IDs    []string `json:"ids,omitempty"`
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
	Description       *string `json:"description,omitempty"`
	Timezone          *string `json:"timezone,omitempty"`
	EstimateType      *string `json:"estimateType,omitempty"`
	DefaultStateID    *string `json:"defaultStateId,omitempty"`
	DefaultPriority   *int    `json:"defaultPriority,omitempty"`
	IssueEmailEnabled *bool   `json:"issueEmailEnabled,omitempty"`
	DetailedHistory   *bool   `json:"detailedHistory,omitempty"`
	Identifier        *string `json:"identifier,omitempty"`
}

type IssueTemplateMutationInput struct {
	TeamID       *string              `json:"teamId,omitempty"`
	Name         *string              `json:"name,omitempty"`
	Description  *string              `json:"description,omitempty"`
	Body         *string              `json:"body,omitempty"`
	StateID      *string              `json:"stateId,omitempty"`
	Priority     *int                 `json:"priority,omitempty"`
	AssigneeID   *string              `json:"assigneeId,omitempty"`
	ProjectID    *string              `json:"projectId,omitempty"`
	LabelIDs     *[]string            `json:"labelIds,omitempty"`
	TemplateType *string              `json:"templateType,omitempty"`
	FormFields   *[]TemplateFormField `json:"formFields,omitempty"`
}

type IssueLabelMutationInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Color       *string `json:"color,omitempty"`
	ArchivedAt  *string `json:"archivedAt,omitempty"`
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
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	AggregateID string          `json:"aggregateId"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type RealtimeEvent struct {
	ID          string          `json:"id"`
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
	Route      string    `json:"route,omitempty"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type SearchResult struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	Subtitle   string    `json:"subtitle,omitempty"`
	Identifier string    `json:"identifier,omitempty"`
	ParentID   string    `json:"parentId,omitempty"`
	ParentType string    `json:"parentType,omitempty"`
	Email      string    `json:"email,omitempty"`
	Icon       string    `json:"icon,omitempty"`
	Color      string    `json:"color,omitempty"`
	Score      int       `json:"score"`
	UpdatedAt  time.Time `json:"updatedAt,omitempty"`
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
