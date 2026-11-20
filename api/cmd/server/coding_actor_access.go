package main

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"slices"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A coding session runs as its creator, so what it may use and read is the
// creator's, checked when the session starts and again before it gets a
// sandbox (coding.ActorDirectory). Flow has no per-environment use ACL:
// "may use the environment" is "active non-guest member of the workspace
// that owns the environment" (coding.Service checks the environment itself).

var _ coding.ActorDirectory = codingDirectory{}

// CodingActorAllowed refuses a user who is not an active member of the
// workspace, or is a guest (unless the session was approved after an external
// request).
func (d codingDirectory) CodingActorAllowed(ctx context.Context, workspaceKey, userID string, allowGuest bool) error {
	if d.s.authDisabled {
		return nil
	}
	data, ok := d.s.store.WorkspaceSettingsMetadata(workspaceKey)
	if !ok {
		return coding.ErrNotFound
	}
	role, status, err := d.s.store.WorkspaceRole(ctx, data.Workspace.ID, userID)
	if err != nil || status != "active" || role == "guest" && !allowGuest {
		return coding.ErrForbidden
	}
	return nil
}

// CodingIssueFor reads an issue with the actor's access, the rule of
// GET /api/issues/{id}: IssueQueryAccess plus the visible-issue query, and the
// API key's team scope when the request carries one.
func (d codingDirectory) CodingIssueFor(ctx context.Context, workspaceKey, actorID, issueID string) (coding.IssueContext, error) {
	issue, err := d.s.codingIssueReadable(ctx, workspaceKey, actorID, issueID)
	if err != nil {
		return coding.IssueContext{}, err
	}
	return d.s.codingIssueContext(workspaceKey, issue), nil
}

func (s *server) codingIssueContext(workspaceKey string, issue domain.Issue) coding.IssueContext {
	result := coding.IssueContext{ID: issue.ID, TeamID: issue.Team.ID, Identifier: issue.Identifier, Title: issue.Title, Description: issue.Description}
	if base := s.loopAppURL(); base != "" && issue.Identifier != "" {
		result.URL = base + "/" + url.PathEscape(workspaceKey) + "/issue/" + url.PathEscape(issue.Identifier)
	}
	return result
}

// codingIssueReadable returns the issue (by id or identifier) when userID can
// read it, with the API key in ctx limiting teams. Anything unreadable or
// missing is coding.ErrNotFound, so the issue is not revealed.
func (s *server) codingIssueReadable(ctx context.Context, workspaceKey, userID, issueID string) (domain.Issue, error) {
	if s.authDisabled {
		issue, err := s.store.IssueRecord(ctx, workspaceKey, issueID)
		if err != nil {
			if issue, err = s.issueByIdentifier(ctx, workspaceKey, issueID); err != nil {
				return domain.Issue{}, coding.ErrNotFound
			}
		}
		return issue, nil
	}
	if !s.store.BoundedMetadataEnabled() {
		if err := s.store.RefreshColdMetadataRead(ctx, workspaceKey); err != nil {
			return domain.Issue{}, err
		}
	}
	data, access, err := s.store.IssueQueryAccess(ctx, workspaceKey, userID)
	if err != nil {
		if errors.Is(err, store.ErrAuthForbidden) {
			return domain.Issue{}, coding.ErrNotFound
		}
		return domain.Issue{}, err
	}
	query := store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}
	if key, ok := ctx.Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
		query.AllowedTeamIDs = slices.Clone(key.TeamIDs)
		if query.AllowedTeamIDs == nil {
			query.AllowedTeamIDs = []string{}
		}
	}
	issue, err := s.store.AuthorizedIssueRecord(ctx, query, issueID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Issue{}, coding.ErrNotFound
		}
		return domain.Issue{}, err
	}
	return issue, nil
}

// CodingSourceReadable checks the SourceID of a session started from source:
// a comment source names a comment of the (readable) issue, an issue source
// the issue itself. Other sources carry server-generated ids and no content.
func (d codingDirectory) CodingSourceReadable(ctx context.Context, workspaceKey, actorID, source, sourceID, issueID string) error {
	switch source {
	case domain.CodingSourceIssue:
		if issueID == "" || sourceID == issueID {
			return nil
		}
		issue, err := d.s.codingIssueReadable(ctx, workspaceKey, actorID, issueID)
		if err != nil {
			return err
		}
		if sourceID == issue.ID || sourceID == issue.Identifier {
			return nil
		}
		return coding.ErrNotFound
	case domain.CodingSourceComment:
		if issueID == "" {
			return coding.ErrNotFound
		}
		issue, err := d.s.codingIssueReadable(ctx, workspaceKey, actorID, issueID)
		if err != nil {
			return err
		}
		if sourceID == issue.ID {
			// The tool path names the issue the comment is on.
			return nil
		}
		if _, err := d.s.store.ResourceComment(ctx, workspaceKey, issue.ID, sourceID); err != nil {
			return coding.ErrNotFound
		}
	}
	return nil
}
