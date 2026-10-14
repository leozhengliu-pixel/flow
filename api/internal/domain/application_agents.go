package domain

import (
	"slices"
	"strings"
	"time"
)

type ApplicationInstallation struct {
	ID             string   `json:"id"`
	WorkspaceKey   string   `json:"workspaceKey"`
	ClientID       string   `json:"clientId"`
	Name           string   `json:"name"`
	AvatarURL      string   `json:"avatarUrl,omitempty"`
	UserID         string   `json:"userId"`
	InstalledBy    string   `json:"installedBy"`
	Scopes         []string `json:"scopes"`
	TeamIDs        []string `json:"teamIds"`
	AllPublicTeams bool     `json:"allPublicTeams,omitempty"`
	GrantedTeamIDs []string `json:"-"`
	Builtin        bool     `json:"builtin"`
	Active         bool     `json:"active"`
	// AllTeams keeps the installation's teams in step with the workspace's
	// teams (as the built-in Flow agent works in every team).
	AllTeams bool `json:"allTeams,omitempty"`
	// ServerSuspended marks an installation deactivated because the server's
	// agent is disabled; it is reactivated when the agent is enabled again.
	ServerSuspended bool `json:"serverSuspended,omitempty"`
	// WithoutRealtime marks an installation authorized while its application
	// had no webhook: it receives no webhook deliveries until an
	// administrator authorizes it again ("Receive realtime updates about
	// your workspace" is then a requested permission on the consent screen).
	WithoutRealtime bool      `json:"withoutRealtime,omitempty"`
	WebhookURL      string    `json:"webhookUrl,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func (a ApplicationInstallation) User() User {
	return User{ID: a.UserID, Name: a.Name, DisplayName: a.Name, AvatarURL: a.AvatarURL, Active: a.Active, App: true, BuiltinAgent: a.Builtin, OAuthClientID: a.ClientID, AppScopes: a.Scopes, AppTeamIDs: a.AccessTeamIDs(), AppAllPublicTeams: a.AllPublicTeams}
}

func (a ApplicationInstallation) AccessTeamIDs() []string {
	if a.GrantedTeamIDs != nil {
		return a.GrantedTeamIDs
	}
	return a.TeamIDs
}

func (a *ApplicationInstallation) ResolveTeamAccess(teams []Team, settings map[string]TeamSettings) {
	a.GrantedTeamIDs = []string{}
	byID := make(map[string]Team, len(teams))
	for _, team := range teams {
		byID[team.ID] = team
	}
	public := func(id string) bool {
		seen := map[string]bool{}
		for id != "" {
			team, ok := byID[id]
			policy := settings[id]
			if !ok || seen[id] || team.Private || team.RetiredAt != nil || team.ArchivedAt != nil || strings.EqualFold(policy.Access, "private") || strings.EqualFold(policy.Access, "restricted") {
				return false
			}
			seen[id] = true
			id = policy.ParentTeamID
		}
		return true
	}
	for _, team := range teams {
		if team.RetiredAt == nil && team.ArchivedAt == nil && (a.AllTeams || slices.Contains(a.TeamIDs, team.ID) || a.AllPublicTeams && public(team.ID)) {
			a.GrantedTeamIDs = append(a.GrantedTeamIDs, team.ID)
		}
	}
}

func (u User) CanDelegateTo(teamID string) bool {
	return u.App && u.Active && slices.Contains(u.AppScopes, "app:assignable") && slices.Contains(u.AppTeamIDs, teamID)
}

func (u User) CanDelegateToTeam(team Team) bool {
	return u.App && u.Active && slices.Contains(u.AppScopes, "app:assignable") && team.RetiredAt == nil && (slices.Contains(u.AppTeamIDs, team.ID) || u.AppAllPublicTeams && !team.Private)
}

func (u User) IsMentionable() bool {
	return !u.App || u.Active && slices.Contains(u.AppScopes, "app:mentionable")
}

type AgentTask struct {
	CommentID     string         `json:"commentId,omitempty"`
	ResourceType  string         `json:"resourceType,omitempty"`
	ResourceID    string         `json:"resourceId,omitempty"`
	PendingTool   *AgentToolCall `json:"pendingTool,omitempty"`
	ID            string         `json:"id"`
	WorkspaceKey  string         `json:"workspaceKey"`
	IssueID       string         `json:"issueId"`
	TeamID        string         `json:"teamId"`
	AppUserID     string         `json:"appUserId"`
	CreatorID     string         `json:"creatorId"`
	Status        string         `json:"status"`
	Prompt        string         `json:"prompt"`
	InitialPrompt string         `json:"initialPrompt,omitempty"`
	Trigger       string         `json:"trigger"`
	Version       int64          `json:"version"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	// ExternalURLs link the session to the agent's own surfaces (a pull
	// request, a run page). Setting one acknowledges the session.
	ExternalURLs []AgentExternalURL `json:"externalUrls,omitempty"`
	// Plan is the agent's checklist; it is replaced as a whole.
	Plan []AgentPlanStep `json:"plan,omitempty"`
	// DeliveredAt is when the latest created/prompted event reached the
	// agent; LastActivityAt is the agent's latest activity or acknowledgement.
	DeliveredAt    *time.Time `json:"deliveredAt,omitempty"`
	LastActivityAt *time.Time `json:"lastActivityAt,omitempty"`
	// Unresponsive is derived when read: the agent did not acknowledge a
	// delivered event within AgentAcknowledgementWindow.
	Unresponsive bool `json:"unresponsive,omitempty"`
	// EphemeralActivityID is the agent's latest ephemeral activity, replaced
	// by the next agent activity.
	EphemeralActivityID string `json:"ephemeralActivityId,omitempty"`
}

// Agent session timing, as in the agent interaction guidelines: an agent
// acknowledges a session within 10 seconds (an activity or an external URL)
// or it is shown as unresponsive; a session with no agent activity for 30
// minutes is stale until the agent emits another activity.
const (
	AgentAcknowledgementWindow = 10 * time.Second
	AgentStaleAfter            = 30 * time.Minute
)

// DeriveTimedState sets the read-only unresponsive flag and the "stale"
// status from the session's timestamps. Stored status is unchanged, so a
// new agent activity recovers the session.
func (t *AgentTask) DeriveTimedState(now time.Time) {
	if t.Status != "pending" && t.Status != "active" {
		return
	}
	if t.DeliveredAt != nil && (t.LastActivityAt == nil || t.LastActivityAt.Before(*t.DeliveredAt)) && now.Sub(*t.DeliveredAt) > AgentAcknowledgementWindow {
		t.Unresponsive = true
	}
	if staleAt := t.StaleAt(); staleAt != nil && now.After(*staleAt) {
		t.Status = "stale"
	}
}

// StaleAt is when a pending or active session goes stale: AgentStaleAfter
// after the agent's latest activity (or the latest delivery to it). It is nil
// for other states and for sessions the agent has not been asked to work on.
func (t AgentTask) StaleAt() *time.Time {
	if t.Status != "pending" && t.Status != "active" {
		return nil
	}
	last := t.LastActivityAt
	if t.DeliveredAt != nil && (last == nil || last.Before(*t.DeliveredAt)) {
		last = t.DeliveredAt
	}
	if last == nil && t.Status == "active" {
		last = &t.UpdatedAt
	}
	if last == nil {
		return nil
	}
	at := last.Add(AgentStaleAfter)
	return &at
}
