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
	// teams (the built-in Flow agent works in every team).
	AllTeams bool `json:"allTeams,omitempty"`
	// ServerSuspended marks an installation deactivated because the server's
	// agent is disabled; it is reactivated when the agent is enabled again.
	ServerSuspended bool      `json:"serverSuspended,omitempty"`
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
}
