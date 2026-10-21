package main

import (
	"context"
	"log/slog"
	"net/http"
	"slices"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

// archiveCodingSession answers POST /api/coding/sessions/{id}/archive: the
// member removed the agent from the issue (or chose Archive on the session
// card) and wants the session archived. The session ends, its open pull
// requests are closed on the code host (best effort), and the issue records
// "<member> archived the session with <agent>". Archiving twice is a no-op.
func (s *server) archiveCodingSession(w http.ResponseWriter, r *http.Request) {
	viewer, session, ok := s.codingSessionAccess(w, r, true)
	if !ok {
		return
	}
	updated, archived, err := s.coding.service.Archive(r.Context(), viewer.workspace, session.ID, viewer.user.ID)
	if err == nil && archived {
		full := updated
		if enriched, getErr := s.coding.service.Get(r.Context(), viewer.workspace, updated.ID); getErr == nil {
			full = enriched // with its pull requests
		}
		s.closeCodingSessionPullRequests(r.Context(), full)
		s.recordCodingSessionArchived(r.Context(), updated, viewer.user)
	}
	s.respondCodingSession(w, r.Context(), viewer, updated, err)
}

// closeCodingSessionPullRequests closes the session's open PRs/MRs. A host
// that cannot close one (or a failure) is logged; the archive stands.
func (s *server) closeCodingSessionPullRequests(ctx context.Context, session domain.CodingSession) {
	if s.coding == nil {
		return
	}
	for _, pullRequest := range session.PullRequests {
		if pullRequest.State != "" && pullRequest.State != "open" {
			continue
		}
		closer, ok := s.coding.hosts[codehost.Kind(pullRequest.CodeHost)].(codehost.ChangeRequestCloser)
		if !ok {
			continue
		}
		repo := codehost.RepoRef{CodeHost: codehost.Kind(pullRequest.CodeHost), HostName: pullRequest.HostName, FullName: pullRequest.Repository}
		if err := closer.CloseChangeRequest(ctx, session.WorkspaceKey, repo, pullRequest.Number); err != nil {
			slog.WarnContext(ctx, "coding session archive: close change request", "session", session.ID, "repository", pullRequest.Repository, "number", pullRequest.Number, "error", err)
		}
	}
}

// recordCodingSessionArchived adds the issue activity for a session a member archived.
func (s *server) recordCodingSessionArchived(ctx context.Context, session domain.CodingSession, member domain.User) {
	if session.IssueID == "" {
		return
	}
	issueID := session.IssueID
	agentID := s.builtinAgentUserID(ctx, session.WorkspaceKey)
	err := s.store.MutateWorkspace(withIssueScope(ctx, &issueID), session.WorkspaceKey, "coding_session.archived", session.ID, map[string]any{"sessionId": session.ID, "archivedBy": member.ID}, func(data *domain.Bootstrap) error {
		if _, err := issueByID(data, issueID); err != nil {
			return nil // the issue is gone
		}
		if slices.ContainsFunc(data.Activities[issueID], func(event domain.ActivityEvent) bool {
			return event.Type == activityCodingSessionArchived && event.Metadata["sessionId"] == session.ID
		}) {
			return nil
		}
		actor := data.Viewer
		if user := userByID(data, member.ID); user != nil {
			actor = *user
		}
		appendActivity(data, issueID, activityCodingSessionArchived, actor, map[string]string{"sessionId": session.ID, "agentSessionId": session.AgentSessionID, "agentId": agentID, "archivedBy": member.ID})
		return nil
	})
	if err != nil {
		slog.WarnContext(ctx, "coding session archive: record activity", "session", session.ID, "error", err)
	}
}
