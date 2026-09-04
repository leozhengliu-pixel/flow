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
}

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

type AgentMessage struct {
	ID         string             `json:"id"`
	Role       string             `json:"role"`
	Content    string             `json:"content"`
	Parts      []AgentMessagePart `json:"parts,omitempty"`
	DurationMS int64              `json:"durationMs,omitempty"`
	CreatedAt  time.Time          `json:"createdAt"`
}

type AgentMessagePart struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	Status   string         `json:"status,omitempty"`
	ToolCall *AgentToolCall `json:"toolCall,omitempty"`
}

type AgentToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Status    string          `json:"status"`
	Error     string          `json:"error,omitempty"`
}

type AgentSession struct {
	ID        string         `json:"id"`
	SlugID    string         `json:"slugId"`
	UserID    string         `json:"userId"`
	Title     string         `json:"title"`
	Favorite  bool           `json:"favorite"`
	Location  string         `json:"location"`
	IssueIDs  []string       `json:"issueIds"`
	SkillIDs  []string       `json:"skillIds"`
	Messages  []AgentMessage `json:"messages"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
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
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URLKey    string    `json:"urlKey"`
	Icon      string    `json:"icon,omitempty"`
	LogoURL   string    `json:"logoUrl,omitempty"`
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
	Viewer                         User                  `json:"viewer"`
	Workspaces                     []WorkspaceMembership `json:"workspaces"`
	LastWorkspaceKey               string                `json:"lastWorkspaceKey,omitempty"`
	WorkspaceRegionSelectorEnabled bool                  `json:"workspaceRegionSelectorEnabled"`
	WorkspaceDefaultRegion         string                `json:"workspaceDefaultRegion"`
}

type Team struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Key       string     `json:"key"`
	Color     string     `json:"color"`
	Icon      string     `json:"icon,omitempty"`
	Private   bool       `json:"private,omitempty"`
	RetiredAt *time.Time `json:"retiredAt,omitempty"`
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
	Version      int64          `json:"version"`
	Content      string         `json:"content"`
	ContentState string         `json:"contentState"`
	ContentData  map[string]any `json:"contentData"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

type Document struct {
	ID            string               `json:"id"`
	SlugID        string               `json:"slugId"`
	Title         string               `json:"title"`
	Icon          string               `json:"icon,omitempty"`
	Color         string               `json:"color,omitempty"`
	Content       string               `json:"content"`
	ContentState  string               `json:"contentState,omitempty"`
	ContentData   map[string]any       `json:"contentData,omitempty"`
	Creator       User                 `json:"creator"`
	ProjectIDs    []string             `json:"projectIds"`
	TeamIDs       []string             `json:"teamIds"`
	IssueID       string               `json:"issueId,omitempty"`
	SubscriberIDs []string             `json:"subscriberIds"`
	Favorite      bool                 `json:"favorite"`
	ArchivedAt    *time.Time           `json:"archivedAt,omitempty"`
	CreatedAt     time.Time            `json:"createdAt"`
	UpdatedAt     time.Time            `json:"updatedAt"`
	Revisions     []DocumentRevision   `json:"revisions"`
	Permissions   []DocumentPermission `json:"permissions,omitempty"`
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
	SLABreachesAt      *time.Time          `json:"slaBreachesAt,omitempty"`
	SLAType            string              `json:"slaType,omitempty"`
	CreatedAt          time.Time           `json:"createdAt"`
	UpdatedAt          time.Time           `json:"updatedAt"`
	CompletedAt        *time.Time          `json:"completedAt,omitempty"`
	StartedAt          *time.Time          `json:"startedAt,omitempty"`
	TriagedAt          *time.Time          `json:"triagedAt,omitempty"`
	StatusChangedAt    *time.Time          `json:"statusChangedAt,omitempty"`
	AutoClosedAt       *time.Time          `json:"autoClosedAt,omitempty"`
	CanceledAt         *time.Time          `json:"canceledAt,omitempty"`
	ArchivedAt         *time.Time          `json:"archivedAt,omitempty"`
	ShareToken         string              `json:"shareToken,omitempty"`
	SharedAt           *time.Time          `json:"sharedAt,omitempty"`
	Team               Team                `json:"team"`
	State              WorkflowState       `json:"state"`
	Assignee           *User               `json:"assignee,omitempty"`
	Delegate           *User               `json:"delegate,omitempty"`
	Creator            User                `json:"creator"`
	Labels             []IssueLabel        `json:"labels"`
	Project            *ProjectSummary     `json:"project,omitempty"`
	ProjectMilestoneID *string             `json:"projectMilestoneId,omitempty"`
	CycleID            *string             `json:"cycleId,omitempty"`
	AddedToCycle       string              `json:"addedToCycle,omitempty"`
	AgentSessionID     string              `json:"agentSessionId,omitempty"`
	SuggestedLabelIDs  []string            `json:"suggestedLabelIds,omitempty"`
	ExternalSource     string              `json:"externalSource,omitempty"`
	AutoClosed         bool                `json:"autoClosed,omitempty"`
	TemplateID         string              `json:"templateId,omitempty"`
	ParentID           *string             `json:"parentId,omitempty"`
	Recurrence         string              `json:"recurrence,omitempty"`
	NextOccurrenceAt   *time.Time          `json:"nextOccurrenceAt,omitempty"`
	SubscriberIDs      []string            `json:"subscriberIds"`
	Reactions          map[string][]string `json:"reactions"`
	SubIssueIDs        []string            `json:"subIssueIds"`
	Relations          []IssueRelation     `json:"relations"`
	Attachments        []Attachment        `json:"attachments"`
	Permissions        []IssuePermission   `json:"permissions,omitempty"`
}

type Cycle struct {
	ID               string                    `json:"id"`
	Number           int                       `json:"number"`
	Name             string                    `json:"name"`
	Description      string                    `json:"description"`
	TeamID           string                    `json:"teamId"`
	StartsAt         time.Time                 `json:"startsAt"`
	EndsAt           time.Time                 `json:"endsAt"`
	Status           string                    `json:"status"`
	Capacity         int                       `json:"capacity"`
	CapacityByMember map[string]map[string]int `json:"capacityByMember,omitempty"`
	Favorite         bool                      `json:"favorite"`
	Resources        []CycleResource           `json:"resources"`
	CalendarToken    string                    `json:"calendarToken,omitempty"`
	Insight          map[string]string         `json:"insight,omitempty"`
	CreatedAt        time.Time                 `json:"createdAt"`
	UpdatedAt        time.Time                 `json:"updatedAt"`
}

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
	TeamID                string               `json:"teamId"`
	Description           string               `json:"description,omitempty"`
	Timezone              string               `json:"timezone"`
	EstimateType          string               `json:"estimateType"`
	DefaultStateID        string               `json:"defaultStateId"`
	DefaultPriority       int                  `json:"defaultPriority"`
	IssueEmailEnabled     bool                 `json:"issueEmailEnabled"`
	DetailedHistory       bool                 `json:"detailedHistory"`
	Access                string               `json:"access"`
	MembershipRestriction string               `json:"membershipRestriction"`
	SettingsPermission    string               `json:"settingsPermission"`
	LabelPermission       string               `json:"labelPermission"`
	TemplatePermission    string               `json:"templatePermission"`
	AgentSkillPermission  string               `json:"agentSkillPermission"`
	LoopPermission        string               `json:"loopPermission"`
	MemberPermission      string               `json:"memberPermission"`
	SlackChannelID        string               `json:"slackChannelId,omitempty"`
	SlackChannelName      string               `json:"slackChannelName,omitempty"`
	SlackNotifications    map[string]bool      `json:"slackNotifications"`
	PRAutomations         map[string]string    `json:"prAutomations"`
	AutoCloseParents      bool                 `json:"autoCloseParents"`
	AutoCloseSubIssues    bool                 `json:"autoCloseSubIssues"`
	AutoCloseStale        bool                 `json:"autoCloseStale"`
	StaleMonths           int                  `json:"staleMonths"`
	StaleStatusID         string               `json:"staleStatusId,omitempty"`
	AutoArchiveMonths     int                  `json:"autoArchiveMonths"`
	ProgressOrder         string               `json:"progressOrder"`
	ReleaseAutomations    []TeamAutomationRule `json:"releaseAutomations"`
	TriageEnabled         bool                 `json:"triageEnabled"`
	TriageRequirePriority bool                 `json:"triageRequirePriority"`
	TriageAction          string               `json:"triageAction"`
	TriageRules           []TeamAutomationRule `json:"triageRules"`
	AgentSkills           []TeamAgentSkill     `json:"agentSkills"`
	ProjectUpdatePrompt   string               `json:"projectUpdatePrompt"`
	ResolvedSummaries     bool                 `json:"resolvedThreadSummaries"`
	ShowInitiatives       bool                 `json:"showInitiatives"`
	ParentTeamID          string               `json:"parentTeamId,omitempty"`
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
	Provider    string    `json:"provider,omitempty"`
	ProviderID  string    `json:"providerId,omitempty"`
	ProviderURL string    `json:"providerUrl,omitempty"`
	LinkbackURL string    `json:"linkbackUrl,omitempty"`
	SyncStatus  string    `json:"syncStatus,omitempty"`
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
	SlugID               string                       `json:"slugId"`
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
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
}

type Release struct {
	ID            string            `json:"id"`
	SlugID        string            `json:"slugId"`
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	Description   string            `json:"description"`
	Status        string            `json:"status"`
	PipelineID    string            `json:"pipelineId,omitempty"`
	Stage         string            `json:"stage,omitempty"`
	CommitSHA     string            `json:"commitSha,omitempty"`
	ReleaseNotes  string            `json:"releaseNotes,omitempty"`
	Position      float64           `json:"position"`
	StartDate     *string           `json:"startDate,omitempty"`
	TargetDate    *string           `json:"targetDate,omitempty"`
	ProjectIDs    []string          `json:"projectIds"`
	IssueIDs      []string          `json:"issueIds"`
	SubscriberIDs []string          `json:"subscriberIds"`
	Resources     []ReleaseResource `json:"resources"`
	Creator       User              `json:"creator"`
	StartedAt     *time.Time        `json:"startedAt,omitempty"`
	ReleasedAt    *time.Time        `json:"releasedAt,omitempty"`
	StageFrozenAt *time.Time        `json:"stageFrozenAt,omitempty"`
	ArchivedAt    *time.Time        `json:"archivedAt,omitempty"`
	CreatedAt     time.Time         `json:"createdAt"`
	UpdatedAt     time.Time         `json:"updatedAt"`
}

type ReleaseResource struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	URL        string    `json:"url,omitempty"`
	DocumentID string    `json:"documentId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
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
	ID                         string         `json:"id"`
	Name                       string         `json:"name"`
	Icon                       string         `json:"icon,omitempty"`
	Color                      string         `json:"color,omitempty"`
	Level                      string         `json:"level"`
	TriggerType                string         `json:"triggerType"`
	TriggerConfig              map[string]any `json:"triggerConfig,omitempty"`
	Instructions               string         `json:"instructions"`
	ConnectorIDs               []string       `json:"connectorIds"`
	TeamAccess                 string         `json:"teamAccess"`
	AllowChangesOutsideTrigger bool           `json:"allowChangesOutsideTrigger"`
	AllowExternalSync          bool           `json:"allowExternalSync"`
	Enabled                    bool           `json:"enabled"`
	Creator                    User           `json:"creator"`
	LastRunAt                  *time.Time     `json:"lastRunAt,omitempty"`
	NextRunAt                  *time.Time     `json:"nextRunAt,omitempty"`
	CreatedAt                  time.Time      `json:"createdAt"`
	UpdatedAt                  time.Time      `json:"updatedAt"`
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
	Visibility          string              `json:"visibility,omitempty"`
	VisibilityTeamID    string              `json:"visibilityTeamId,omitempty"`
	Creator             User                `json:"creator"`
	CreatedAt           time.Time           `json:"createdAt"`
	UpdatedAt           time.Time           `json:"updatedAt"`
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
	ChangelogUpdates      bool              `json:"changelogUpdates"`
	ChangelogNewsletter   bool              `json:"changelogNewsletter"`
	MarketingUpdates      bool              `json:"marketingUpdates"`
	InviteAcceptedUpdates bool              `json:"inviteAcceptedUpdates"`
	PrivacyUpdates        bool              `json:"privacyUpdates"`
	DPAUpdates            bool              `json:"dpaUpdates"`
	AgentEnabled          bool              `json:"agentEnabled"`
	AgentInstructions     string            `json:"agentInstructions"`
	PulseSchedule         string            `json:"pulseSchedule"`
	JobTitle              string            `json:"jobTitle,omitempty"`
	Username              string            `json:"username,omitempty"`
	UpdatedAt             time.Time         `json:"updatedAt"`
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

type WorkspaceSettings struct {
	FiscalMonth                  string            `json:"fiscalMonth"`
	WelcomeMessage               string            `json:"welcomeMessage,omitempty"`
	DefaultHomeView              string            `json:"defaultHomeView,omitempty"`
	GuestsAllowed                bool              `json:"guestsAllowed"`
	RequireTwoFactor             bool              `json:"requireTwoFactor"`
	SessionDurationDays          int               `json:"sessionDurationDays"`
	AllowedDomains               []string          `json:"allowedDomains"`
	InvitePermission             string            `json:"invitePermission"`
	TeamCreatePermission         string            `json:"teamCreatePermission"`
	LabelPermission              string            `json:"labelPermission"`
	TemplatePermission           string            `json:"templatePermission"`
	APIKeyPermission             string            `json:"apiKeyPermission"`
	FeatureFlags                 map[string]bool   `json:"featureFlags"`
	FeatureSettings              FeatureSettings   `json:"featureSettings"`
	InviteLinksEnabled           bool              `json:"inviteLinksEnabled"`
	GoogleAuthEnabled            bool              `json:"googleAuthEnabled"`
	EmailAuthEnabled             bool              `json:"emailAuthEnabled"`
	DisableAdminBypass           bool              `json:"disableAdminBypass"`
	InitiativePermission         string            `json:"initiativePermission,omitempty"`
	LoopPermission               string            `json:"loopPermission,omitempty"`
	AgentGuidancePermission      string            `json:"agentGuidancePermission,omitempty"`
	PreventGuestAgents           bool              `json:"preventGuestAgents"`
	AgentWebSearch               bool              `json:"agentWebSearch"`
	ExternalLoopTriggers         bool              `json:"externalLoopTriggers"`
	MCPConnectorsEnabled         bool              `json:"mcpConnectorsEnabled"`
	ReviewThirdPartyApplications bool              `json:"reviewThirdPartyApplications"`
	AllowedMCPConnectors         string            `json:"allowedMcpConnectors,omitempty"`
	RestrictFileUploads          bool              `json:"restrictFileUploads"`
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
	UpdatedAt            time.Time         `json:"updatedAt"`
}

type FeatureOption struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type FeatureSettings struct {
	InitiativeUpdateSchedule string          `json:"initiativeUpdateSchedule"`
	CustomerDefaultTeamID    string          `json:"customerDefaultTeamId,omitempty"`
	CustomerRevenueFormat    string          `json:"customerRevenueFormat"`
	CustomerRevenueCurrency  string          `json:"customerRevenueCurrency"`
	CustomerManualEdits      bool            `json:"customerManualEdits"`
	CustomerStatuses         []FeatureOption `json:"customerStatuses"`
	CustomerTiers            []FeatureOption `json:"customerTiers"`
	CustomerExcludedDomains  []string        `json:"customerExcludedDomains"`
	CustomerGenericDomains   []string        `json:"customerGenericDomains"`
	PulseWorkspaceSchedule   string          `json:"pulseWorkspaceSchedule"`
	AsksEmailAddresses       []string        `json:"asksEmailAddresses"`
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
	Position                    float64           `json:"position"`
	PathFilters                 []string          `json:"pathFilters"`
	ReleaseNotesTemplate        string            `json:"releaseNotesTemplate,omitempty"`
	AutoGenerateReleaseNotes    bool              `json:"autoGenerateReleaseNotes"`
	MoveOpenIssuesToNextRelease *bool             `json:"moveOpenIssuesToNextRelease,omitempty"`
	AccessKeyPrefix             string            `json:"accessKeyPrefix,omitempty"`
	AccessKeyHash               string            `json:"accessKeyHash,omitempty"`
	AccessKeyCreatedAt          *time.Time        `json:"accessKeyCreatedAt,omitempty"`
	CreatedAt                   time.Time         `json:"createdAt"`
	UpdatedAt                   time.Time         `json:"updatedAt"`
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
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	SecretHash string   `json:"secretHash,omitempty"`
	CreatorID  string   `json:"creatorId"`
	Scopes     []string `json:"scopes"`
	TeamIDs    []string `json:"teamIds"`
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
	ClientURI               string    `json:"client_uri,omitempty"`
	LogoURI                 string    `json:"logo_uri,omitempty"`
	RedirectURIs            []string  `json:"redirect_uris"`
	GrantTypes              []string  `json:"grant_types"`
	ResponseTypes           []string  `json:"response_types"`
	TokenEndpointAuthMethod string    `json:"token_endpoint_auth_method"`
	CreatedAt               time.Time `json:"created_at"`
}

type OAuthAuthorizationCode struct {
	ClientID        string    `json:"clientId"`
	WorkspaceKey    string    `json:"workspaceKey"`
	UserID          string    `json:"userId"`
	RedirectURI     string    `json:"redirectUri"`
	Scopes          []string  `json:"scopes"`
	CodeChallenge   string    `json:"codeChallenge"`
	AuthorizationID string    `json:"authorizationId"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type OAuthRefreshGrant struct {
	ClientID        string    `json:"clientId"`
	WorkspaceKey    string    `json:"workspaceKey"`
	UserID          string    `json:"userId"`
	Scopes          []string  `json:"scopes"`
	AuthorizationID string    `json:"authorizationId"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type OAuthAuthorization struct {
	ID         string     `json:"id"`
	ClientID   string     `json:"clientId"`
	ClientName string     `json:"clientName"`
	UserID     string     `json:"userId"`
	Scopes     []string   `json:"scopes"`
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

type Webhook struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	ResourceTypes   []string   `json:"resourceTypes"`
	TeamIDs         []string   `json:"teamIds"`
	TeamRestriction string     `json:"teamRestriction,omitempty"`
	Enabled         bool       `json:"enabled"`
	CreatorID       string     `json:"creatorId"`
	SecretHash      string     `json:"-"`
	SecretPrefix    string     `json:"secretPrefix,omitempty"`
	SecretRevokedAt *time.Time `json:"secretRevokedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
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
	ID         string    `json:"id"`
	TeamID     string    `json:"teamId"`
	Repository string    `json:"repository"`
	Branch     string    `json:"branch"`
	Default    bool      `json:"default"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
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
}

type ReviewEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Body      string    `json:"body,omitempty"`
	ParentID  string    `json:"parentId,omitempty"`
	Path      string    `json:"path,omitempty"`
	Line      int       `json:"line,omitempty"`
	Resolved  bool      `json:"resolved,omitempty"`
	Actor     User      `json:"actor"`
	CreatedAt time.Time `json:"createdAt"`
}

type CodeReview struct {
	ID              string        `json:"id"`
	SlugID          string        `json:"slugId"`
	Provider        string        `json:"provider"`
	ExternalID      string        `json:"externalId"`
	Number          int           `json:"number"`
	Title           string        `json:"title"`
	Description     string        `json:"description"`
	Status          string        `json:"status"`
	RepositoryOwner string        `json:"repositoryOwner"`
	RepositoryName  string        `json:"repositoryName"`
	URL             string        `json:"url"`
	Author          User          `json:"author"`
	ReviewerIDs     []string      `json:"reviewerIds"`
	TeamReviewers   []string      `json:"teamReviewers"`
	IssueIDs        []string      `json:"issueIds"`
	BaseBranch      string        `json:"baseBranch"`
	HeadBranch      string        `json:"headBranch"`
	BranchState     string        `json:"branchState"`
	Additions       int           `json:"additions"`
	Deletions       int           `json:"deletions"`
	CommitCount     int           `json:"commitCount"`
	Checks          []ReviewCheck `json:"checks"`
	Files           []ReviewFile  `json:"files"`
	Events          []ReviewEvent `json:"events"`
	Favorite        bool          `json:"favorite"`
	Draft           bool          `json:"draft"`
	QuickToReview   bool          `json:"quickToReview"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
	MergedAt        *time.Time    `json:"mergedAt,omitempty"`
	ClosedAt        *time.Time    `json:"closedAt,omitempty"`
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
	TeamIDs      []string        `json:"teamIds,omitempty"`
	DeletedBy    User            `json:"deletedBy"`
	DeletedAt    time.Time       `json:"deletedAt"`
	ExpiresAt    time.Time       `json:"expiresAt"`
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
	SlugID               string                          `json:"slugId"`
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
	LeadTeamID           string                          `json:"leadTeamId,omitempty"`
	ContributingTeamIDs  []string                        `json:"contributingTeamIds"`
	LabelIDs             []string                        `json:"labelIds"`
	ParentInitiativeIDs  []string                        `json:"parentInitiativeIds"`
	ProjectIDs           []string                        `json:"projectIds"`
	Resources            []InitiativeResource            `json:"resources"`
	Comments             []Comment                       `json:"comments"`
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
	Workspace                     Workspace                          `json:"workspace"`
	Viewer                        User                               `json:"viewer"`
	Users                         []User                             `json:"users"`
	Teams                         []Team                             `json:"teams"`
	Customers                     []Customer                         `json:"customers"`
	States                        []WorkflowState                    `json:"states"`
	Labels                        []IssueLabel                       `json:"labels"`
	LabelGroups                   []LabelGroup                       `json:"labelGroups"`
	Issues                        []Issue                            `json:"issues"`
	Cycles                        []Cycle                            `json:"cycles"`
	CycleSettings                 map[string]CycleSettings           `json:"cycleSettings"`
	TeamSettings                  map[string]TeamSettings            `json:"teamSettings"`
	IssueTemplates                []IssueTemplate                    `json:"issueTemplates"`
	ProjectTemplates              []ProjectTemplate                  `json:"projectTemplates"`
	DocumentTemplates             []DocumentTemplate                 `json:"documentTemplates"`
	Documents                     []Document                         `json:"documents"`
	CustomerRequests              []CustomerRequest                  `json:"customerRequests"`
	Releases                      []Release                          `json:"releases"`
	ReleasePipelines              []ReleasePipeline                  `json:"releasePipelines"`
	CustomEmojis                  []CustomEmoji                      `json:"customEmojis"`
	Asks                          []Ask                              `json:"asks"`
	Loops                         []Loop                             `json:"loops"`
	SLARules                      []SLARule                          `json:"slaRules"`
	IssueSLAs                     []IssueSLA                         `json:"issueSlas"`
	SLAEvents                     []SLAEvent                         `json:"slaEvents"`
	Drafts                        []Draft                            `json:"drafts"`
	Favorites                     []Favorite                         `json:"favorites"`
	FavoriteFolders               []FavoriteFolder                   `json:"favoriteFolders"`
	Subscriptions                 []Subscription                     `json:"subscriptions"`
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
	IntegrationConnections        []IntegrationConnection            `json:"integrationConnections"`
	IdentityProviders             []IdentityProvider                 `json:"identityProviders"`
	IntegrationDeliveries         []IntegrationDelivery              `json:"integrationDeliveries"`
	GitAutomationStates           []GitAutomationState               `json:"gitAutomationStates"`
	TargetBranches                []TargetBranch                     `json:"targetBranches"`
	Reviews                       []CodeReview                       `json:"reviews"`
	AgentSessions                 []AgentSession                     `json:"agentSessions"`
	AgentSkills                   []PersonalAgentSkill               `json:"agentSkills"`
	Settings                      map[string]any                     `json:"settings"`
	Members                       []WorkspaceMember                  `json:"members"`
	TeamMembers                   []TeamMember                       `json:"teamMembers"`
	Invitations                   []Invitation                       `json:"invitations"`
	ViewerRole                    string                             `json:"viewerRole"`
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
	Recurrence         *string        `json:"recurrence,omitempty"`
	NextOccurrenceAt   *string        `json:"nextOccurrenceAt,omitempty"`
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
	ExpectedVersion         *int64         `json:"expectedVersion,omitempty"`
	Title                   *string        `json:"title,omitempty"`
	Description             *string        `json:"description,omitempty"`
	DescriptionState        *string        `json:"descriptionState,omitempty"`
	DescriptionData         map[string]any `json:"descriptionData,omitempty"`
	ContentState            *string        `json:"contentState,omitempty"`
	ExpectedDocumentVersion *int64         `json:"expectedDocumentVersion,omitempty"`
	DocumentUpdateIDs       []string       `json:"documentUpdateIds,omitempty"`
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
	Recurrence              *string        `json:"recurrence,omitempty"`
	NextOccurrenceAt        *string        `json:"nextOccurrenceAt,omitempty"`
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
	Name             *string                   `json:"name,omitempty"`
	Description      *string                   `json:"description,omitempty"`
	StartsAt         *string                   `json:"startsAt,omitempty"`
	EndsAt           *string                   `json:"endsAt,omitempty"`
	Capacity         *int                      `json:"capacity,omitempty"`
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
	Description           *string               `json:"description,omitempty"`
	Timezone              *string               `json:"timezone,omitempty"`
	EstimateType          *string               `json:"estimateType,omitempty"`
	DefaultStateID        *string               `json:"defaultStateId,omitempty"`
	DefaultPriority       *int                  `json:"defaultPriority,omitempty"`
	IssueEmailEnabled     *bool                 `json:"issueEmailEnabled,omitempty"`
	DetailedHistory       *bool                 `json:"detailedHistory,omitempty"`
	Identifier            *string               `json:"identifier,omitempty"`
	Access                *string               `json:"access,omitempty"`
	MembershipRestriction *string               `json:"membershipRestriction,omitempty"`
	SettingsPermission    *string               `json:"settingsPermission,omitempty"`
	LabelPermission       *string               `json:"labelPermission,omitempty"`
	TemplatePermission    *string               `json:"templatePermission,omitempty"`
	AgentSkillPermission  *string               `json:"agentSkillPermission,omitempty"`
	LoopPermission        *string               `json:"loopPermission,omitempty"`
	MemberPermission      *string               `json:"memberPermission,omitempty"`
	SlackChannelID        *string               `json:"slackChannelId,omitempty"`
	SlackChannelName      *string               `json:"slackChannelName,omitempty"`
	SlackNotifications    *map[string]bool      `json:"slackNotifications,omitempty"`
	PRAutomations         *map[string]string    `json:"prAutomations,omitempty"`
	AutoCloseParents      *bool                 `json:"autoCloseParents,omitempty"`
	AutoCloseSubIssues    *bool                 `json:"autoCloseSubIssues,omitempty"`
	AutoCloseStale        *bool                 `json:"autoCloseStale,omitempty"`
	StaleMonths           *int                  `json:"staleMonths,omitempty"`
	StaleStatusID         *string               `json:"staleStatusId,omitempty"`
	AutoArchiveMonths     *int                  `json:"autoArchiveMonths,omitempty"`
	ProgressOrder         *string               `json:"progressOrder,omitempty"`
	ReleaseAutomations    *[]TeamAutomationRule `json:"releaseAutomations,omitempty"`
	TriageEnabled         *bool                 `json:"triageEnabled,omitempty"`
	TriageRequirePriority *bool                 `json:"triageRequirePriority,omitempty"`
	TriageAction          *string               `json:"triageAction,omitempty"`
	TriageRules           *[]TeamAutomationRule `json:"triageRules,omitempty"`
	AgentSkills           *[]TeamAgentSkill     `json:"agentSkills,omitempty"`
	ProjectUpdatePrompt   *string               `json:"projectUpdatePrompt,omitempty"`
	ResolvedSummaries     *bool                 `json:"resolvedThreadSummaries,omitempty"`
	ShowInitiatives       *bool                 `json:"showInitiatives,omitempty"`
	ParentTeamID          *string               `json:"parentTeamId,omitempty"`
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
	DocumentID string    `json:"documentId,omitempty"`
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
