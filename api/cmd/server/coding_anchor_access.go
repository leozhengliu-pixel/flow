package main

import (
	"net/http"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Coding sessions are read through their AgentTask anchor
// (authorizedAgentTask), so the session, its thread, artifacts, diff, logs
// and actions all follow one rule. Issue, document, project and
// codingEnvironment anchors use their resource's rule; the anchors below
// belong to sessions that have no such resource.
const (
	codingAnchorAgentChat   = domain.CodingSourceAgentChat // a standalone session started from Flow Agent chat
	codingAnchorPullRequest = "pullRequest"                // a review or webhook session on a PR/MR without an issue
	codingAnchorLoop        = "loop"                       // a loop run's session without an issue
)

// codingAnchorResource reports whether a task is the anchor of a coding
// session that has no issue, document, project or environment behind it.
func codingAnchorResource(task domain.AgentTask) bool {
	switch task.ResourceType {
	case "", "issue", "document", "project", codingEnvironmentAnchorResource:
		return false
	case codingAnchorAgentChat:
		return true
	}
	// pullRequest, loop and the other trigger sources (external, comment,
	// environmentSetup without an environment) only anchor coding sessions.
	return task.CodingSessionID != ""
}

// codingAnchorAccess is who may read and act on a coding session
// anchored outside an issue:
//
//   - agentChat: Flow Agent chats are private to their owner (agent chats
//     have no sharing), so the session is the creator's. Workspace admins and
//     owners may read and act on it too, as they may on every other anchor
//     (they own issues and documents and are the only ones on environment
//     sessions). Other members and guests are refused.
//   - pullRequest and loop: reviews and loops are workspace-wide, so every
//     member reads them; guests only their own sessions.
//   - anything else: the creator and admins.
//
// The anchor's own application (the Flow agent) may always read it;
// authorizedAgentTask limits an application to its own anchors.
func codingAnchorAccess(viewer domain.User, role string, task domain.AgentTask) error {
	switch {
	case viewer.App:
		return nil
	case viewer.ID != "" && viewer.ID == task.CreatorID:
		return nil
	case workspaceAdminRole(role):
		return nil
	}
	switch task.ResourceType {
	case codingAnchorPullRequest, codingAnchorLoop:
		if role == "member" {
			return nil
		}
	}
	return store.ErrAuthForbidden
}

// codingSessionAnchorAccess applies the anchor's rule to a coding session
// for a web caller: ErrNotFound when they may not read it (the session is not
// revealed), ErrForbidden when they may read but not act. A session's creator
// may always act on a session they can still read.
func (s *server) codingSessionAnchorAccess(r *http.Request, session domain.CodingSession, editing bool) error {
	if s.authDisabled {
		return nil
	}
	viewer := authUser(r)
	creator := viewer.ID != "" && viewer.ID == session.CreatorID
	if !creator {
		// Guests see only their own sessions; a session without an anchor
		// (sessions are created with one) only its creator and the admins.
		metadata, _, err := s.pagedRealtimeMetadata(r)
		if err != nil || metadata.ViewerRole == "guest" || session.AgentSessionID == "" && !workspaceAdminRole(metadata.ViewerRole) {
			return coding.ErrNotFound
		}
	}
	if session.AgentSessionID == "" {
		return nil
	}
	if _, _, err := s.authorizedAgentTask(r, session.AgentSessionID, false); err != nil {
		return coding.ErrNotFound
	}
	if !editing || creator {
		return nil
	}
	if _, _, err := s.authorizedAgentTask(r, session.AgentSessionID, true); err != nil {
		return coding.ErrForbidden
	}
	return nil
}
