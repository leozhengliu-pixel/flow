package domain

import (
	"slices"
	"time"
)

type ApplicationInstallation struct {
	ID           string    `json:"id"`
	WorkspaceKey string    `json:"workspaceKey"`
	ClientID     string    `json:"clientId"`
	Name         string    `json:"name"`
	AvatarURL    string    `json:"avatarUrl,omitempty"`
	UserID       string    `json:"userId"`
	InstalledBy  string    `json:"installedBy"`
	Scopes       []string  `json:"scopes"`
	TeamIDs      []string  `json:"teamIds"`
	Builtin      bool      `json:"builtin"`
	Active       bool      `json:"active"`
	WebhookURL   string    `json:"webhookUrl,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func (a ApplicationInstallation) User() User {
	return User{ID: a.UserID, Name: a.Name, DisplayName: a.Name, AvatarURL: a.AvatarURL, Active: a.Active, App: true, BuiltinAgent: a.Builtin, OAuthClientID: a.ClientID, AppScopes: a.Scopes, AppTeamIDs: a.TeamIDs}
}

func (u User) CanDelegateTo(teamID string) bool {
	return u.App && u.Active && slices.Contains(u.AppScopes, "app:assignable") && slices.Contains(u.AppTeamIDs, teamID)
}

func (u User) IsMentionable() bool {
	return !u.App || u.Active && slices.Contains(u.AppScopes, "app:mentionable")
}

type AgentTask struct {
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
