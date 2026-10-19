package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// Track B: adapters between the coding session core and the server
// (workspace settings, issues and users), and the hooks that let the agent
// session protocol drive a coding session through its anchor AgentTask.

// codingWorkspaces resolves workspace settings for the session core.
type codingWorkspaces struct{ s *server }

func (w codingWorkspaces) CodingWorkspace(_ context.Context, workspaceKey string) (coding.WorkspaceInfo, error) {
	data, ok := w.s.store.WorkspaceSettingsMetadata(workspaceKey)
	if !ok {
		return coding.WorkspaceInfo{}, coding.ErrNotFound
	}
	return coding.WorkspaceInfo{Settings: data.WorkspaceSettings, CreatedAt: data.Workspace.CreatedAt}, nil
}

// codingDirectory resolves issues and users for the session core.
type codingDirectory struct{ s *server }

func (d codingDirectory) CodingIssue(ctx context.Context, workspaceKey, issueID string) (coding.IssueContext, error) {
	issue, err := d.s.store.IssueRecord(ctx, workspaceKey, issueID)
	if err != nil {
		return coding.IssueContext{}, coding.ErrNotFound
	}
	result := coding.IssueContext{ID: issue.ID, TeamID: issue.Team.ID, Identifier: issue.Identifier, Title: issue.Title, Description: issue.Description}
	if base := d.s.loopAppURL(); base != "" && issue.Identifier != "" {
		result.URL = base + "/" + url.PathEscape(workspaceKey) + "/issue/" + url.PathEscape(issue.Identifier)
	}
	return result, nil
}

func (d codingDirectory) CodingUser(ctx context.Context, workspaceKey, userID string) (coding.UserInfo, error) {
	user, err := d.s.store.UserByID(ctx, userID)
	if err != nil {
		return coding.UserInfo{}, coding.ErrNotFound
	}
	info := coding.UserInfo{ID: user.ID, Name: user.Name, Email: user.Email}
	if _, settings, ok := d.s.store.AccountCollections(workspaceKey, userID); ok && settings.CommitSigningKey != nil {
		info.SigningFormat = "ssh"
		if kind := strings.ToLower(settings.CommitSigningKey.Type); strings.Contains(kind, "pgp") || strings.Contains(kind, "gpg") {
			info.SigningFormat = "openpgp"
		}
	}
	return info, nil
}

// codingAnchorTask reports whether an AgentTask anchors a coding session.
// The application worker skips those: the coding session drives the anchor,
// so the in-process agent must never run (or interrupt) it.
func (s *server) codingAnchorTask(ctx context.Context, task domain.AgentTask) bool {
	return s.store != nil && s.store.IsCodingAnchor(ctx, task.WorkspaceKey, task.ID)
}

// codingAnchorActivity routes a person's activity on a coding anchor to the
// session: a stop signal (or cancel) stops it, a reply is a follow-up turn.
func (s *server) codingAnchorActivity(ctx context.Context, task domain.AgentTask, activity domain.AgentActivity) {
	if s.coding == nil || s.coding.service == nil || task.Trigger != coding.AnchorTrigger && !s.codingAnchorTask(ctx, task) {
		return
	}
	session, err := s.coding.service.GetByAnchor(ctx, task.WorkspaceKey, task.ID)
	if err != nil {
		return
	}
	activityID := activity.ID
	if activityID == "" {
		activityID = paddedVersion(task.Version)
	}
	switch {
	case activity.Type == "canceled" || activity.Type == "prompt" && activity.Signal == "stop":
		_, err = s.coding.service.Stop(ctx, task.WorkspaceKey, session.ID, activity.ActorID, true)
	case activity.Type == "prompt" && strings.TrimSpace(activity.Body) != "":
		// Approve/Reject answers to an approval elicitation (track L) go to
		// the triggers; every other reply is a follow-up turn.
		if handled, answerErr := s.handleCodingElicitationAnswer(ctx, task.WorkspaceKey, activity.ActorID, s.codingMemberRoleOf(task.WorkspaceKey, activity.ActorID), activity.Body); handled {
			err = answerErr
			break
		}
		_, err = s.coding.service.Prompt(ctx, task.WorkspaceKey, session.ID, coding.PromptInput{Prompt: activity.Body, AuthorID: activity.ActorID, ActivityID: activityID})
	case activity.Type == "retry":
		_, err = s.coding.service.Restart(ctx, task.WorkspaceKey, session.ID, activity.ActorID, false)
	}
	if err != nil && !errors.Is(err, coding.ErrConflict) {
		log.Printf("coding session %s: anchor activity %s: %v", session.ID, activity.Type, err)
	}
}

func paddedVersion(version int64) string { return fmt.Sprintf("%020d", version) }

// codingMemberRoleOf is a workspace member's role ("" when not found).
func (s *server) codingMemberRoleOf(workspaceKey, userID string) string {
	data, ok := s.store.WorkspaceMetadataFields(workspaceKey, "members")
	if !ok {
		return ""
	}
	for _, member := range data.Members {
		if member.User.ID == userID {
			return member.Role
		}
	}
	return "guest"
}
