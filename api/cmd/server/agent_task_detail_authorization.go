package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

// legacyAgentTaskDetailAuthorization decides GET /api/agent-tasks/{id} in
// legacy metadata mode without the viewer's whole workspace projection.
//
// The projection decided nothing for this route: resourceAllowedLegacy has no
// rule for agent tasks, so a projection that loaded allowed every active
// member, and the handler (authorizedAgentTask) made the real decision. Under
// load, though, the request waited for a shared projection slot and answered
// 503 after the admission timeout, so a saved agent reply could not be read.
//
// Issue-anchored tasks and coding sessions without a resource are authorized
// by the handler with scoped reads only: the task's owner, application and
// guest rules (agentTaskVisibleToViewer), the issue's visibility through the
// viewer's issue query access (private teams, guests, explicit grants and a
// team-restricted key's teams), and the coding anchor's role rule. Workspace
// membership and the credential were checked before this, as for the list
// (GET /api/agent-tasks). A task that does not exist is answered by the
// handler (404). Document, project and environment tasks keep the projection:
// their handler reads the workspace data itself.
func (s *server) legacyAgentTaskDetailAuthorization(r *http.Request, workspace string) (allowed, handled bool) {
	if s.store.BoundedMetadataEnabled() || r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false, false
	}
	id, ok := agentTaskDetailID(r.URL.Path)
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
