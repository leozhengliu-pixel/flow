package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

// legacyAgentTaskDetailAuthorization decides GET /api/agent-tasks/{id} and
// POST /api/agent-tasks/{id}/activities in legacy metadata mode without the
// viewer's whole workspace projection.
//
// The projection decided nothing for these routes: resourceAllowedLegacy has
// no rule for agent tasks, so a projection that loaded allowed every active
// member, and the handlers (authorizedAgentTask) made the real decision.
// Under load, though, a read waited for a shared projection slot and answered
// 503 after the admission timeout, so a saved agent reply could not be read.
// A reply in the thread is a write, which waits for a slot until its client
// leaves: behind a gateway it timed out and was never saved.
//
// Issue-anchored tasks and coding sessions without a resource are authorized
// by the handler with scoped reads only: the task's owner, application and
// guest rules (agentTaskVisibleToViewer), the issue's visibility through the
// viewer's issue query access (private teams, guests, explicit grants and a
// team-restricted key's teams), edit access to the issue for an activity,
// and the coding anchor's role rule. The activity handler then applies its
// own rules (who may approve, delete a reply or act as the application) and
// appends with the expected version. Workspace membership and the credential
// (scopes, revocation) were checked before this, as for the list (GET
// /api/agent-tasks). A task that does not exist is answered by the handler
// (404). Document, project and environment tasks, and a task that could not
// be read, keep the projection: their handler reads the workspace data itself.
func (s *server) legacyAgentTaskDetailAuthorization(r *http.Request, workspace string) (allowed, handled bool) {
	if s.store.BoundedMetadataEnabled() {
		return false, false
	}
	var id string
	var ok bool
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		id, ok = agentTaskDetailID(r.URL.Path)
	case http.MethodPost:
		id, ok = agentTaskActivityID(r.URL.Path)
	}
	if !ok {
		return false, false
	}
	task, err := s.store.AgentTask(r.Context(), workspace, id)
	if errors.Is(err, sql.ErrNoRows) {
		return true, true
	}
	if err != nil {
		return false, false
	}
	if task.ResourceType == "" || task.ResourceType == "issue" || codingAnchorResource(task) {
		return true, true
	}
	return false, false
}

// agentTaskDetailID is the id in /api/agent-tasks/{id}.
func agentTaskDetailID(path string) (string, bool) {
	id, ok := strings.CutPrefix(path, "/api/agent-tasks/")
	if !ok || id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

// agentTaskActivityID is the id in /api/agent-tasks/{id}/activities.
func agentTaskActivityID(path string) (string, bool) {
	rest, ok := strings.CutSuffix(path, "/activities")
	if !ok {
		return "", false
	}
	return agentTaskDetailID(rest)
}
