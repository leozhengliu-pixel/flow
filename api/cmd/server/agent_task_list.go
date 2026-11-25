package main

import (
	"net/http"
	"slices"
	"time"

	"flow/api/internal/domain"
)

func (s *server) listApplicationAgentTasks(w http.ResponseWriter, r *http.Request) {
	actor := authUser(r)
	query := r.URL.Query()
	ref, kind := query.Get("issueId"), query.Get("resourceType")
	if kind == "" {
		kind = "issue"
	}
	if resource := query.Get("resourceId"); resource != "" {
		ref = resource
	}
	if ref == "" && !actor.App {
		writeError(w, http.StatusBadRequest, "issueId is required")
		return
	}

	workspace := workspaceKey(r)
	if workspace == "" {
		// Only an in-process or development caller reaches the default;
		// authenticated HTTP requests already have a bound workspace.
		if s.store.BoundedMetadataEnabled() {
			read, found, err := s.store.BeginInstalledWorkspaceIdentityRead(r.Context(), "")
			if err != nil {
				respondMutation(w, err, http.StatusOK, nil)
				return
			}
			if found {
				defer read.Close()
				workspace = read.Identity.URLKey
			}
		} else {
			metadata, _ := s.store.WorkspaceSettingsMetadata("")
			workspace = metadata.Workspace.URLKey
		}
	}

	type anchor struct{ kind, ref string }
	type decision struct {
		viewer taskListViewer
		ok     bool
	}
	// The filter target and several tasks may all point to the same resource.
	// Keep just the decision and viewer identity, never the resource contents.
	checked := map[anchor]decision{}
	check := func(task domain.AgentTask) decision {
		if task.ResourceType == "" {
			task.ResourceType = "issue"
		}
		key := anchor{task.ResourceType, task.ResourceID}
		if key.kind == "issue" {
			key.ref = task.IssueID
		}
		if hit, ok := checked[key]; ok {
			return hit
		}
		viewer, ok := s.agentTaskListResource(r, key.kind, key.ref)
		result := decision{viewer, ok}
		checked[key] = result
		return result
	}
	if ref != "" && !check(domain.AgentTask{ResourceType: kind, ResourceID: ref, IssueID: ref}).ok {
		writeError(w, http.StatusNotFound, "Issue not found")
		return
	}

	appID := ""
	if actor.App {
		appID = actor.ID
	}
	// MCP's list_agent_sessions also asks for archived sessions.
	list := s.store.ListAgentTasks
	if query.Get("includeArchived") == "true" {
		list = s.store.ListAgentTasksIncludingArchived
	}
	tasks, err := list(r.Context(), workspace, ref, appID)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	visible := []domain.AgentTask{}
	now := time.Now()
	// Coding-session checks need only the viewer and role. Read the workspace
	// view once per request and keep just those, not the copy itself.
	var codingViewer *struct {
		user domain.User
		role string
		err  error
	}
	for _, task := range tasks {
		var result decision
		if codingAnchorResource(task) {
			// Standalone coding sessions also depend on the individual creator.
			if codingViewer == nil {
				data, _, err := s.pagedRealtimeMetadata(r)
				codingViewer = &struct {
					user domain.User
					role string
					err  error
				}{data.Viewer, data.ViewerRole, err}
			}
			viewer := taskListViewer{domain.User{ID: codingViewer.user.ID, App: codingViewer.user.App}, codingViewer.role}
			result = decision{viewer, codingViewer.err == nil && codingAnchorAccess(codingViewer.user, codingViewer.role, task) == nil}
		} else {
			result = check(task)
		}
		if result.ok && agentTaskVisibleToViewer(task, result.viewer.user, result.viewer.role) {
			task.DeriveTimedState(now)
			visible = append(visible, task)
		}
	}
	// A credential can be revoked or have its teams narrowed between
	// authentication and delivery. In-process calls have their own ingress.
	if len(visible) > 0 && !s.authDisabled && r.Pattern == "GET /api/agent-tasks" {
		if err := s.issueReadCredentialValid(r.Context(), r); err != nil {
			if !writeAuthCheckFailure(w, err) {
				writeError(w, http.StatusUnauthorized, "Invalid authorization credentials")
			}
			return
		}
	}
	writeJSON(w, http.StatusOK, visible)
}

func (s *server) agentTaskListResource(r *http.Request, kind, ref string) (viewer taskListViewer, allowed bool) {
	switch kind {
	case "issue":
		data, err := s.agentIssueContext(r, []string{ref})
		return agentTaskListViewer(data), err == nil && len(data.Issues) == 1
	case codingAnchorAgentChat:
		// The filter names no creator. Check the actual chat owner per task.
		data, _, err := s.pagedRealtimeMetadata(r)
		return agentTaskListViewer(data), err == nil && (workspaceAdminRole(data.ViewerRole) || data.Viewer.App)
	case codingEnvironmentAnchorResource:
		data, _, err := s.pagedRealtimeMetadata(r)
		return agentTaskListViewer(data), err == nil && (workspaceAdminRole(data.ViewerRole) || data.Viewer.App)
	case "project", "document":
		userID := authUser(r).ID
		if s.authDisabled {
			userID = ""
		} else if userID == "" {
			return viewer, false
		}
		var credential *domain.APIKey
		if !s.authDisabled {
			credential = documentRecordCredential(r)
		}
		err := s.store.ReadAgentTaskResource(r.Context(), workspaceKey(r), userID, credential, kind, ref, func(data domain.Bootstrap) {
			viewer = agentTaskListViewer(data)
			if !s.authDisabled {
				filterBootstrapForAPIKey(&data, r)
			}
			if kind == "project" {
				allowed = slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == ref })
			} else if document, err := documentByID(&data, ref); err == nil {
				allowed = documentRole(s, data, *document) != "none"
			}
		})
		return viewer, err == nil && allowed
	default:
		return viewer, false
	}
}

type taskListViewer struct {
	user domain.User
	role string
}

func agentTaskListViewer(data domain.Bootstrap) taskListViewer {
	return taskListViewer{domain.User{ID: data.Viewer.ID, App: data.Viewer.App}, data.ViewerRole}
}
