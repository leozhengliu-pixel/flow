package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Archiving a whole agent session (POST /api/agent-tasks/{id}/archive).
//
// A session is one run of an agent on a resource: a delegation, an
// @-mention in a comment, or a session the agent opened itself. Deleting a
// comment removes that one comment; deleting an agent reply ("deleted"
// activity) hides that one reply. Archiving the session hides the session
// and every message in it, agent replies and people's replies alike, from
// the resource's activity, for everyone. It is soft: the activities (the
// execution log), audit entries, the coding session it anchors with its
// pull requests and artifacts, documents and reports it produced,
// attachments and linked issues are all kept, and GET
// /api/agent-tasks/{id} still reads it. The comment that mentioned the agent
// is an ordinary comment and stays.
//
// Only the person who started the session (CreatorID, while they can still
// see the resource) or a workspace admin may archive it. An application may
// archive only a session it opened itself; it can never hide a person's.
// A running session is stopped in the same commit, and a coding session it
// anchors is stopped (not archived: archiving a coding session closes its
// pull requests, which this must not do). Archiving again changes nothing
// and publishes nothing.

type agentSessionArchiveResponse struct {
	Session         domain.AgentTask `json:"session"`
	Archived        bool             `json:"archived"`
	AlreadyArchived bool             `json:"alreadyArchived"`
	Stopped         bool             `json:"stopped"`
	PreviousStatus  string           `json:"previousStatus"`
}

// agentSessionArchiveDenial is why viewer may not archive task ("" when they may).
func agentSessionArchiveDenial(task domain.AgentTask, viewer domain.User, role string) string {
	if viewer.App {
		if task.CreatorID == viewer.ID && task.AppUserID == viewer.ID {
			return ""
		}
		return "Applications can archive only agent sessions they opened themselves"
	}
	if viewer.ID != "" && viewer.ID == task.CreatorID || workspaceAdminRole(role) {
		return ""
	}
	return "Only the person who started this agent session or a workspace admin can archive it"
}

func (s *server) archiveApplicationAgentTask(w http.ResponseWriter, r *http.Request) {
	task, data, err := s.authorizedAgentTask(r, r.PathValue("id"), false)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	if reason := agentSessionArchiveDenial(task, data.Viewer, data.ViewerRole); reason != "" {
		writeError(w, http.StatusForbidden, reason)
		return
	}
	outcome, err := s.store.ArchiveAgentTask(r.Context(), task.WorkspaceKey, task.ID, data.Viewer.ID)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	if outcome.Archived {
		ctx := context.WithoutCancel(r.Context())
		s.stopArchivedCodingSession(ctx, outcome.Task, data.Viewer.ID)
		s.auditAgentSessionArchive(r, outcome)
	}
	outcome.Task.DeriveTimedState(time.Now())
	writeJSON(w, http.StatusOK, agentSessionArchiveResponse{Session: outcome.Task, Archived: true, AlreadyArchived: !outcome.Archived, Stopped: outcome.Stopped, PreviousStatus: outcome.PreviousStatus})
}

// stopArchivedCodingSession stops the coding session an archived task
// anchors when it is still working or waiting, whatever the anchor's own
// status (a delegation's reply can end the anchor while its session works).
// The session, its sandbox output, pull requests and artifacts are kept.
func (s *server) stopArchivedCodingSession(ctx context.Context, task domain.AgentTask, actorID string) {
	if task.CodingSessionID == "" || s.coding == nil || s.coding.service == nil {
		return
	}
	session, err := s.coding.service.GetByAnchor(ctx, task.WorkspaceKey, task.ID)
	if err != nil || session.Phase == domain.CodingPhaseDone {
		return
	}
	if _, err := s.coding.service.Stop(ctx, task.WorkspaceKey, session.ID, actorID, true); err != nil && !errors.Is(err, coding.ErrConflict) {
		log.Printf("agent session %s archived: stop coding session %s: %v", task.ID, session.ID, err)
	}
}

func (s *server) auditAgentSessionArchive(r *http.Request, outcome store.AgentTaskArchive) {
	task := outcome.Task
	resourceType, resourceID := task.ResourceType, task.IssueID
	if resourceType == "" {
		resourceType = "issue"
	}
	if resourceID == "" {
		resourceID = task.ResourceID
	}
	metadata := map[string]any{
		"resourceType": resourceType, "resourceId": resourceID, "agentUserId": task.AppUserID, "initiatorId": task.CreatorID,
		"trigger": task.Trigger, "previousStatus": outcome.PreviousStatus, "stopped": outcome.Stopped,
	}
	if task.CommentID != "" {
		metadata["commentId"] = task.CommentID
	}
	if task.CodingSessionID != "" {
		metadata["codingSessionId"] = task.CodingSessionID
	}
	ctx := store.WithMetadataFields(context.WithoutCancel(r.Context()), "auditLog")
	if err := s.store.MutateWorkspace(ctx, task.WorkspaceKey, "audit.agent_session", task.ID, map[string]any{"action": "agent_session.archived"}, func(data *domain.Bootstrap) error {
		appendAudit(data, "agent_session.archived", "agent_session", task.ID, metadata)
		return nil
	}); err != nil {
		log.Printf("agent session audit workspace=%s session=%s: %v", task.WorkspaceKey, task.ID, err)
	}
}
