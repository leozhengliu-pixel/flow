package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) listApplicationInstallations(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	items, err := s.store.ListApplications(r.Context(), data.Workspace.URLKey)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	// The avatar comes from the third-party registration: an avatar stored
	// before icon URLs were validated is left out (the initials show).
	for index := range items {
		if !items[index].Builtin && items[index].AvatarURL != "" && !validApplicationIconURL(items[index].AvatarURL, true) {
			items[index].AvatarURL = ""
		}
	}
	writeJSON(w, 200, items)
}

func (s *server) configureApplicationInstallation(w http.ResponseWriter, r *http.Request) {
	var input domain.ApplicationInstallation
	if !decodeJSON(w, r, &input) {
		return
	}
	data := s.workspaceData(r)
	if !s.authDisabled && !workspaceAdminRole(data.ViewerRole) {
		writeError(w, 403, "Workspace admin access required")
		return
	}
	if r.PathValue("id") != "" {
		items, err := s.store.ListApplications(r.Context(), data.Workspace.URLKey)
		if err != nil {
			respondMutation(w, err, 200, nil)
			return
		}
		index := slices.IndexFunc(items, func(a domain.ApplicationInstallation) bool { return a.ID == r.PathValue("id") })
		if index < 0 {
			writeError(w, 404, "Application not found")
			return
		}
		old := items[index]
		input.ClientID = old.ClientID
		input.Builtin = old.Builtin
		// Only authorizing again grants realtime updates (webhooks).
		input.WithoutRealtime = old.WithoutRealtime
	}
	if input.Builtin {
		if !s.agent.Enabled {
			writeError(w, 409, "Configure Flow Agent before enabling its application member")
			return
		}
		input.ClientID = store.BuiltinApplicationClientID
		input.Name = "Flow"
		input.AvatarURL = "/favicon.svg"
		input.WebhookURL = ""
		input.ServerSuspended = false
		input.Scopes = domain.ApplicationScopes()
	} else {
		scopes, err := domain.NormalizeApplicationScopes(input.Scopes)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		input.Scopes = scopes
		if input.ClientID == store.BuiltinApplicationClientID {
			writeError(w, 400, "Reserved application client")
			return
		}
		client, err := s.store.OAuthClient(r.Context(), input.ClientID)
		if err != nil {
			var ok bool
			client, ok = s.store.OAuthApplicationClient(input.ClientID)
			if !ok {
				writeError(w, 400, "Register the OAuth application first")
				return
			}
		}
		input.Name = client.ClientName
		// The registration's logo becomes the member's avatar. Logos stored
		// before icon URLs were validated are dropped (the initials show).
		input.AvatarURL = ""
		if validApplicationIconURL(client.LogoURI, true) {
			input.AvatarURL = client.LogoURI
		}
		if client.HomeWorkspaceKey != "" && client.HomeWorkspaceKey != data.Workspace.URLKey && !client.PublicEnabled {
			writeError(w, 403, "This application is private to its home workspace")
			return
		}
		if client.AllowedScopes != nil && slices.ContainsFunc(input.Scopes, func(scope string) bool {
			return !slices.Contains(client.AllowedScopes, scope)
		}) {
			writeError(w, 400, "Requested scopes exceed the application registration")
			return
		}
		if input.WebhookURL != "" && !integrationEndpointSafe(r.Context(), input.WebhookURL, s.authDisabled, outboundTrustedPrivate) {
			writeError(w, 400, "Invalid application webhook URL")
			return
		}
	}
	if !input.AllPublicTeams && len(input.TeamIDs) == 0 || len(input.TeamIDs) > 500 {
		writeError(w, 400, "Select at least one team (up to 500)")
		return
	}
	scopes, scopeErr := domain.NormalizeApplicationScopes(input.Scopes)
	if scopeErr != nil {
		writeError(w, 400, scopeErr.Error())
		return
	}
	input.Scopes = scopes
	input.InstalledBy = data.Viewer.ID
	secret, err := randomSecret("flow_webhook_")
	if err != nil {
		writeError(w, 500, "Could not generate webhook secret")
		return
	}
	installed, err := s.store.InstallApplication(r.Context(), data.Workspace.URLKey, input, secret)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	writeJSON(w, 200, map[string]any{"application": installed, "webhookSecret": secret})
}

// ensureBuiltinApplications installs (or suspends) the built-in Flow agent
// member in every workspace to match FLOW_AGENT_ENABLED. It runs at startup.
func (s *server) ensureBuiltinApplications(ctx context.Context) {
	for _, workspace := range s.store.WorkspaceKeys() {
		s.ensureBuiltinApplication(ctx, workspace)
	}
}

// syncApplicationIdentity shows an edited OAuth application's name and logo on
// its installed app users (assignee, delegate, comment and history authors).
func (s *server) syncApplicationIdentity(ctx context.Context, clientID string) {
	if err := s.store.SyncApplicationIdentity(ctx, clientID); err != nil {
		log.Printf("sync application identity client=%s: %v", clientID, err)
	}
}

func (s *server) ensureBuiltinApplication(ctx context.Context, workspace string) {
	if _, err := s.store.EnsureBuiltinApplication(ctx, workspace, s.agent.Enabled); err != nil {
		log.Printf("ensure built-in Flow agent workspace=%s: %v", workspace, err)
	}
}

func (s *server) applicationConsentTeams(w http.ResponseWriter, r *http.Request) {
	actor := s.oauthRequestUser(r)
	data, ok := s.oauthWorkspaceForRequest(w, r, r.URL.Query().Get("workspace"), actor)
	if !ok {
		return
	}
	if !workspaceAdminRole(data.ViewerRole) {
		writeError(w, 403, "Administrator required to install an application")
		return
	}
	metadata, _ := s.store.WorkspaceMetadata(data.Workspace.URLKey)
	writeJSON(w, 200, metadata.Teams)
}

func (s *server) authorizedAgentTask(r *http.Request, id string, editing bool) (domain.AgentTask, domain.Bootstrap, error) {
	workspace := workspaceKey(r)
	if workspace == "" {
		if metadata, ok := s.store.WorkspaceMetadata(""); ok {
			workspace = metadata.Workspace.URLKey
		}
	}
	task, err := s.store.AgentTask(r.Context(), workspace, id)
	if err != nil {
		return task, domain.Bootstrap{}, errNotFound
	}
	data, err := s.applicationTaskContext(r, task, editing)
	if err != nil {
		return task, data, errNotFound
	}
	if data.Viewer.App && task.AppUserID != data.Viewer.ID {
		return task, data, errNotFound
	}
	if task.CodingSessionID != "" && data.ViewerRole == "guest" && task.CreatorID != data.Viewer.ID {
		// Guests see only their own coding sessions, whatever the anchor.
		return task, data, errNotFound
	}
	return task, data, nil
}

func (s *server) applicationTaskContext(r *http.Request, task domain.AgentTask, editing bool) (domain.Bootstrap, error) {
	if task.ResourceType == "" || task.ResourceType == "issue" {
		data, err := s.agentIssueContext(r, []string{task.IssueID})
		if err != nil || len(data.Issues) != 1 {
			return data, errNotFound
		}
		if editing && !data.Viewer.App && issuePermissionRank(issueRole(s, data, data.Issues[0])) < issuePermissionRank("editor") {
			return data, store.ErrAuthForbidden
		}
		return data, nil
	}
	if codingAnchorResource(task) {
		// A coding session without an issue (agent chat, pull request, loop,
		// external): the workspace role is all the rule needs, so it skips
		// the full bootstrap the document and project branches load.
		metadata, _, err := s.pagedRealtimeMetadata(r)
		if err != nil {
			return metadata, errNotFound
		}
		if err := codingAnchorAccess(metadata.Viewer, metadata.ViewerRole, task); err != nil {
			return metadata, err
		}
		metadata.Issues = nil
		return metadata, nil
	}
	data := s.workspaceData(r)
	if task.ResourceType == "document" {
		doc, err := documentByID(&data, task.ResourceID)
		if err != nil {
			return data, errNotFound
		}
		role := documentRole(s, data, *doc)
		if role == "none" || editing && !data.Viewer.App && !canCommentDocument(role) {
			return data, store.ErrAuthForbidden
		}
		data.Documents = []domain.Document{*doc}
		data.Issues = nil
		return data, nil
	}
	if task.ResourceType == codingEnvironmentAnchorResource {
		// "Configure/Fix with agent" sessions: environments are admin settings.
		if !slices.Contains([]string{"admin", "owner"}, data.ViewerRole) && !data.Viewer.App {
			return data, store.ErrAuthForbidden
		}
		data.Issues = nil
		return data, nil
	}
	if task.ResourceType == "project" {
		index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == task.ResourceID })
		if index < 0 || editing && data.ViewerRole == "guest" {
			return data, store.ErrAuthForbidden
		}
		data.Projects = []domain.Project{data.Projects[index]}
		data.Issues = nil
		return data, nil
	}
	return data, errNotFound
}

func (s *server) listApplicationAgentTasks(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	issueID := r.URL.Query().Get("issueId")
	resourceType := r.URL.Query().Get("resourceType")
	if resourceType == "" {
		resourceType = "issue"
	}
	if r.URL.Query().Get("resourceId") != "" {
		issueID = r.URL.Query().Get("resourceId")
	}
	if issueID == "" && !actor.App {
		writeError(w, 400, "issueId is required")
		return
	}
	if issueID != "" {
		if _, err := s.applicationTaskContext(r, domain.AgentTask{ResourceType: resourceType, ResourceID: issueID, IssueID: issueID}, false); err != nil {
			writeError(w, 404, "Issue not found")
			return
		}
	}
	appID := ""
	if actor.App {
		appID = actor.ID
	}
	workspace := workspaceKey(r)
	if workspace == "" {
		if metadata, ok := s.store.WorkspaceMetadata(""); ok {
			workspace = metadata.Workspace.URLKey
		}
	}
	tasks, err := s.store.ListAgentTasks(r.Context(), workspace, issueID, appID)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	visible := []domain.AgentTask{}
	for _, task := range tasks {
		if _, _, err := s.authorizedAgentTask(r, task.ID, false); err == nil {
			task.DeriveTimedState(time.Now())
			visible = append(visible, task)
		}
	}
	writeJSON(w, 200, visible)
}

func (s *server) getApplicationAgentTask(w http.ResponseWriter, r *http.Request) {
	task, _, err := s.authorizedAgentTask(r, r.PathValue("id"), false)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	activities, err := s.store.AgentActivities(r.Context(), task.WorkspaceKey, task.ID, r.URL.Query().Get("after"))
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	task.DeriveTimedState(time.Now())
	writeJSON(w, 200, map[string]any{"session": task, "activities": activities})
}

func (s *server) appendApplicationAgentActivity(w http.ResponseWriter, r *http.Request) {
	task, data, err := s.authorizedAgentTask(r, r.PathValue("id"), true)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	var input struct {
		Version    int64  `json:"expectedVersion"`
		Type       string `json:"type"`
		Body       string `json:"body"`
		ActivityID string `json:"activityId"`
		Enabled    *bool  `json:"enabled"`
		URL        string `json:"url"`
		Approve    *bool  `json:"approve,omitempty"`
		// Ephemeral thought or action activities are replaced by the next one.
		Ephemeral      bool           `json:"ephemeral,omitempty"`
		Signal         string         `json:"signal,omitempty"`
		SignalMetadata map[string]any `json:"signalMetadata,omitempty"`
		Content        *struct {
			Type      string `json:"type"`
			Body      string `json:"body"`
			Action    string `json:"action"`
			Parameter string `json:"parameter"`
			Result    string `json:"result"`
		} `json:"content,omitempty"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 80<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Content != nil {
		input.Type, input.Body = input.Content.Type, input.Content.Body
	}
	if err := validateApplicationSignal(input.Type, input.Signal, input.SignalMetadata, data.Viewer.App); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if !data.Viewer.App && input.Type != "prompt" && input.Type != "canceled" && input.Type != "retry" && input.Type != "feedback" && input.Type != "deleted" {
		writeError(w, 403, "Only the session application may emit agent activities")
		return
	}
	if input.Type == "prompt" && task.PendingTool == nil && input.Signal != "stop" && strings.TrimSpace(input.Body) == "" {
		writeError(w, 400, "A reply is required")
		return
	}
	if data.Viewer.App && (input.Type == "prompt" || input.Type == "canceled" || input.Type == "retry" || input.Type == "feedback" || input.Type == "deleted") {
		writeError(w, 403, "Application cannot impersonate user input")
		return
	}
	if input.URL != "" && !validOAuthMetadataURI(input.URL) {
		writeError(w, 400, "Activity links must use HTTPS")
		return
	}
	if input.Ephemeral && input.Type != "thought" && input.Type != "action" {
		writeError(w, 400, "Only thought and action activities can be ephemeral")
		return
	}
	activity := domain.AgentActivity{ActorID: data.Viewer.ID, Type: input.Type, Body: input.Body, URL: input.URL, Signal: input.Signal, SignalMetadata: input.SignalMetadata, Ephemeral: input.Ephemeral}
	if input.Type == "feedback" {
		if input.ActivityID == "" || input.Enabled == nil || input.Signal != "" || input.URL != "" {
			writeError(w, 400, "Feedback requires an activity ID and an explicit enabled value")
			return
		}
		activity.Metadata = map[string]any{"activityId": input.ActivityID, "enabled": *input.Enabled}
	}
	if input.Type == "deleted" {
		if input.ActivityID == "" || input.Signal != "" || input.URL != "" || input.Body != "" {
			writeError(w, 400, "Deleting a reply requires only its activity ID")
			return
		}
		if data.Viewer.ID != task.CreatorID && !workspaceAdminRole(data.ViewerRole) {
			writeError(w, 403, "Only the requesting user or an administrator may delete agent replies")
			return
		}
		activity.Metadata = map[string]any{"activityId": input.ActivityID}
	}
	if input.Content != nil && input.Type == "action" {
		activity.Metadata = map[string]any{"action": input.Content.Action, "parameter": input.Content.Parameter, "result": input.Content.Result}
	}
	if input.Type == "prompt" && input.Signal != "stop" && task.PendingTool != nil {
		if input.Approve == nil {
			// Like 参照应用, a reply answers the approval: a picked Approve option or "yes" / "批准" approves,
			// anything else rejects (and the agent reads the reply).
			approve := agentApprovalAnswer(input.Body) == agentApprovalApprove
			input.Approve = &approve
		}
		if data.Viewer.ID != task.CreatorID && !workspaceAdminRole(data.ViewerRole) {
			writeError(w, 403, "Only the requesting user or an administrator may approve this action")
			return
		}
		call := *task.PendingTool
		call.Status = "rejected"
		if *input.Approve {
			call.Status = "approved"
		}
		activity.ToolCall = &call
	}
	updated, err := s.store.AppendAgentActivity(r.Context(), task.WorkspaceKey, task.ID, input.Version, activity)
	if err == nil {
		// coding sessions (track B): stop and replies on a coding anchor drive the session.
		activity.ID = paddedVersion(updated.Version)
		s.codingAnchorActivity(r.Context(), updated, activity)
	}
	if err == store.ErrIssueQuery {
		writeError(w, 400, "Invalid activity or feedback target")
		return
	}
	if err == store.ErrIssueVersion {
		writeError(w, 409, "Session changed; reload before replying")
		return
	}
	respondMutation(w, err, 200, updated)
}

func (s *server) runApplicationAgentWorker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	slots := make(chan struct{}, 4)
	var workers sync.WaitGroup
	defer workers.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tasks, err := s.store.PendingApplicationTasks(ctx)
			if err != nil {
				continue
			}
			for _, task := range tasks {
				if ctx.Err() != nil {
					return
				}
				select {
				case slots <- struct{}{}:
				default:
					continue
				}
				claimed, err := s.store.ClaimApplicationTask(ctx, task.ID, task.Version)
				if err != nil || !claimed {
					<-slots
					continue
				}
				workers.Add(1)
				go func(task domain.AgentTask) {
					defer workers.Done()
					defer func() { <-slots }()
					s.deliverApplicationTask(ctx, task)
				}(task)
			}
		}
	}
}

func (s *server) deliverApplicationTask(ctx context.Context, task domain.AgentTask) {
	// coding sessions (track B): a coding anchor is driven by its session, never by the application worker.
	if s.codingAnchorTask(ctx, task) {
		_ = s.store.FinishApplicationDelivery(ctx, task.ID, task.Version, true)
		return
	}
	app, err := s.store.ApplicationByUser(ctx, task.AppUserID)
	if err != nil || !app.Active || !slices.Contains(app.AccessTeamIDs(), task.TeamID) {
		_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{Type: "canceled", Body: "Application access was revoked."})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	if app.Builtin {
		go func(ctx context.Context) {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					current, err := s.store.AgentTask(ctx, task.WorkspaceKey, task.ID)
					if err != nil || current.Status == "canceled" {
						cancel()
						return
					}
					installation, err := s.store.ApplicationByUser(ctx, task.AppUserID)
					if err != nil || !installation.Active || !slices.Contains(installation.AccessTeamIDs(), task.TeamID) {
						cancel()
						return
					}
				}
			}
		}(ctx)
	}
	user := app.User()
	key := domain.APIKey{CreatorID: user.ID, SubjectUserID: user.ID, Actor: "app", Scopes: app.Scopes, TeamRestriction: "selected", TeamIDs: app.AccessTeamIDs(), OAuthClientID: app.ClientID}
	ctx = context.WithValue(ctx, authUserContextKey{}, user)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, task.WorkspaceKey)
	ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
	ctx = store.ContextWithActor(ctx, user)
	// Links in tool results and the reply point at the app the person uses, not the worker's internal host
	// (relative when no app URL is known).
	ctx = context.WithValue(ctx, agentBaseURLKey{}, s.loopAppURL())
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://flow.internal/api/agent-tasks/"+task.ID, nil)
	data, err := s.applicationTaskContext(r, task, false)
	if err != nil {
		_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{Type: "canceled", Body: "The application does not have access to this resource. Grant access before retrying."})
		return
	}
	data.Viewer = app.User()
	data.ViewerRole = "app"
	if app.Builtin {
		var err error
		r, data, err = s.builtinApplicationContext(r, app, task)
		if err != nil {
			_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{Type: "canceled", Body: "The requesting user no longer has access to this resource."})
			return
		}
	}
	if agentWorkspacePolicy(data.WorkspaceSettings, "app") != nil {
		_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{Type: "canceled", Body: "Agent execution is disabled by workspace policy."})
		return
	}
	if app.Builtin {
		if task.Status == "active" {
			_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "error", Body: "Execution was interrupted. Inspect previous tool results before retrying."})
			return
		}
		// Instant feedback (AIG): the session shows the agent at work before the model's first token.
		task = s.acknowledgeBuiltinTask(ctx, task, data)
		s.executeBuiltinApplication(r, app, task, data)
		return
	}
	if task.Status == "active" {
		_ = s.store.FinishApplicationDelivery(ctx, task.ID, task.Version, true)
		return
	}
	target, err := s.agentSessionTarget(ctx, app)
	if err != nil {
		return
	}
	if target.url == "" {
		_ = s.store.FinishApplicationDelivery(ctx, task.ID, task.Version, true)
		return
	}
	success := s.sendAgentSessionEvent(ctx, app, task, data, target)
	if success {
		// The agent now has 10 seconds to acknowledge (an activity or an
		// external URL) before the session shows as unresponsive.
		_ = s.store.MarkAgentTaskDelivered(ctx, task.WorkspaceKey, task.ID, task.Version, time.Now())
	}
	_ = s.store.FinishApplicationDelivery(ctx, task.ID, task.Version, success)
}

// appendBuiltinActivity appends the built-in agent's activity at the version
// it last wrote. When the task has meanwhile become a coding session's anchor
// (start_coding_session reuses the delegation task), the session writes to it
// as well, so the agent's later activities — its final reply — are appended at
// the current version instead of being dropped as a conflict.
func appendBuiltinActivity(ctx context.Context, repo *store.SQLiteStore, task domain.AgentTask, activity domain.AgentActivity) (domain.AgentTask, error) {
	next, err := repo.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, activity)
	if !errors.Is(err, store.ErrIssueVersion) {
		return next, err
	}
	current, readErr := repo.AgentTask(ctx, task.WorkspaceKey, task.ID)
	if readErr != nil || current.CodingSessionID == "" || current.Version == task.Version {
		return next, err
	}
	return repo.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, current.Version, activity)
}

func (s *server) executeBuiltinApplication(r *http.Request, app domain.ApplicationInstallation, task domain.AgentTask, data domain.Bootstrap) {
	appendAgentActivity := func(activity domain.AgentActivity) error {
		current, err := s.store.ApplicationByUser(r.Context(), task.AppUserID)
		if err != nil || !current.Active || !slices.Contains(current.AccessTeamIDs(), task.TeamID) {
			return store.ErrAuthForbidden
		}
		activity.ActorID = task.AppUserID
		next, err := appendBuiltinActivity(r.Context(), s.store, task, activity)
		if err == nil {
			task = next
		}
		return err
	}
	appendActivity := func(kind, body string) error {
		return appendAgentActivity(domain.AgentActivity{Type: kind, Body: body})
	}
	execute := func(call domain.AgentToolCall) ([]byte, error) {
		current, err := s.store.ApplicationByUser(r.Context(), task.AppUserID)
		if err != nil || !current.Active || !slices.Contains(current.AccessTeamIDs(), task.TeamID) {
			return nil, store.ErrAuthForbidden
		}
		key := domain.APIKey{CreatorID: current.UserID, SubjectUserID: current.UserID, Actor: "app", Scopes: current.Scopes, TeamRestriction: "selected", TeamIDs: current.AccessTeamIDs(), OAuthClientID: current.ClientID}
		req := r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, key))
		req, scoped, err := s.builtinApplicationContext(req, current, task)
		if err != nil {
			return nil, err
		}
		return s.executeAgentTool(req, scoped, call)
	}
	// runTool records a tool step and runs it; the result is what the model reads. A connector (MCP) call is
	// logged like an agent's action activity (action, parameter, result) so the thread's activity shows what the
	// connector was asked and what it answered; false means the session can no longer be written.
	// guard bounds the run's read-only preparation (connector calls) and answers identical repeats from the
	// earlier result, so a run asked to implement something decides instead of searching until the turn limit.
	guard := newBuiltinRunGuard(s.builtinCodingContextFor(r.Context(), task, data))
	guard.hadSession = task.CodingSessionID != ""
	issueTitle := ""
	if len(data.Issues) == 1 {
		issueTitle = data.Issues[0].Title
	}
	// transfer moves a run that must enter Coding into it when the model did not (see builtin_coding_transfer.go)
	// and posts the reply with the session's state, after text (the model's own reply, if any).
	transfer := func(reason, text string) {
		state, ok := s.builtinCodingTransfer(r.Context(), task, guard, issueTitle, reason, execute, appendAgentActivity)
		if !ok {
			return
		}
		reply := s.builtinReplyWithCoding(r.Context(), task, guard, text, state)
		_ = appendActivity("response", rebaseAgentWorkspaceLinks(reply, data.Workspace.URLKey, agentLinkBaseURL(r)))
	}
	runTool := func(call domain.AgentToolCall) (string, bool, bool) {
		connector, external := offeredConnectorTool(r.Context(), call.Name)
		if !external {
			if err := appendActivity("action", call.Name); err != nil {
				return "", false, false
			}
			result, err := execute(call)
			content, failed := string(result), err != nil
			if failed {
				// The model reports what failed, so it needs the real reason.
				content = agentToolErrorContent(err)
			}
			if call.Name == startCodingSessionTool {
				content = s.recordCodingStart(r.Context(), task.WorkspaceKey, guard, content, failed)
			}
			return content, failed, true
		}
		metadata := connectorActionMetadata(connector, call)
		if content, failed, handled := guard.before(call); handled {
			// Not run: an identical repeat or a call over budget, logged so the thread shows it was skipped.
			metadata["result"] = connectorNotRunResult(content)
			if appendAgentActivity(domain.AgentActivity{Type: "action", Body: connector.RemoteName, Metadata: metadata}) != nil {
				return "", false, false
			}
			return content, failed, true
		}
		if err := appendAgentActivity(domain.AgentActivity{Type: "action", Body: connector.RemoteName, Ephemeral: true, Metadata: maps.Clone(metadata)}); err != nil {
			return "", false, false
		}
		result, err := execute(call)
		content, logged := string(result), connectorActionResult(result, err)
		if text, reported := connectorFailureText(result, err); reported {
			// The connector said why it failed: the model reads its own text (still a failed call).
			content = text
		} else if err != nil {
			content = agentToolErrorContent(err)
		}
		// A failed call or one that found nothing is a call without progress.
		guard.after(call, content, err != nil || connectorFoundNothing(result))
		metadata["result"] = logged
		if appendAgentActivity(domain.AgentActivity{Type: "action", Body: connector.RemoteName, Metadata: metadata}) != nil {
			return "", false, false
		}
		return content, err != nil, true
	}
	if !s.agent.Enabled {
		_ = appendActivity("error", "Flow Agent is not configured.")
		return
	}
	var pending strings.Builder
	streamBytes := 0
	last := time.Now()
	emit := func(event agentProviderEvent) error {
		// Provider reasoning is the model's scratchpad, not a thought for the thread (often English in a Chinese
		// workspace); the thread shows the running step instead (an action per tool call).
		if event.Type != "text.delta" {
			return nil
		}
		streamBytes += len(event.Delta)
		if streamBytes > 1<<20 {
			return fmt.Errorf("agent response exceeds the session budget")
		}
		pending.WriteString(event.Delta)
		if pending.Len() > 32000 || time.Since(last) > time.Second && pending.Len() > 100 {
			if err := appendActivity("output", pending.String()); err != nil {
				return err
			}
			pending.Reset()
			last = time.Now()
		}
		return nil
	}
	if agentWebAccess(data.WorkspaceSettings) {
		r = r.WithContext(withAgentWebTools(r.Context()))
	}
	// Connector (MCP) tools as in chat, discovered for this run as the person who delegated or mentioned the agent
	// (their personal connectors and credentials, and the workspace's shared ones). Calls wait for approval and
	// re-check access when they run.
	connectorFailure := ""
	if metadata, ok := s.store.WorkspaceMetadata(task.WorkspaceKey); ok {
		connectorCtx, _, err := s.withConnectorTools(r.Context(), metadata, connectorRequestContext{Workspace: task.WorkspaceKey, UserID: task.CreatorID})
		r = r.WithContext(connectorCtx)
		if err != nil {
			connectorFailure = err.Error()
		}
	}
	messages := s.builtinThreadMessages(r, task, data, data.Issues)
	if connectorFailure != "" {
		messages[0].Content += "\nThe workspace's connected MCP tools are unavailable in this session (" + truncateSettingsText(connectorFailure, 300) + "). If the request needs them, say so.\n"
	}
	// An approval answered since the last run (a connector set to always ask, or a delete or update held by an
	// earlier version): run or drop the held calls first.
	if task.PendingTool != nil {
		call := *task.PendingTool
		held := agentApprovalBatchCalls(call)
		results := make([]agentProviderMessage, 0, len(held))
		if call.Status != "approved" {
			_ = appendActivity("action", "Action rejected: "+agentHeldCallNames(r.Context(), held))
			for _, item := range held {
				results = append(results, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: item.ID, Content: `{"error":"The user rejected this action; nothing was changed. Do not retry it unless they ask again."}`, IsError: true}})
			}
		} else {
			if err := appendActivity("action", "Approved action: "+agentHeldCallNames(r.Context(), held)); err != nil {
				return
			}
			for _, item := range held {
				// Connectors were discovered for this run: one removed or revoked since the approval is refused.
				content, failed, ok := runTool(item)
				if !ok {
					return
				}
				results = append(results, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: item.ID, Content: content, IsError: failed}})
			}
		}
		messages = append(append(messages, agentProviderMessage{Role: "assistant", ToolCalls: held}), results...)
	}
	// A comment-thread agent can stop on a question with options, like chat (the reference app's select elicitation).
	r = r.WithContext(context.WithValue(r.Context(), agentExtraToolsKey{}, []agentProviderTool{askUserToolDefinition}))
	textApprovalRetried, emptyTurnRetried := false, false
	decisionRound := -1
	for round := 0; round < maxAgentToolTurns; round++ {
		turnCtx := r.Context()
		if offered, _ := turnCtx.Value(connectorToolsKey{}).([]connectorTool); len(offered) > 0 && guard.mustDecide(round) {
			// The preparation budget is spent or the run is near its turn limit: the model decides without
			// connector tools (start Coding, or reply with what it found and the blocker).
			turnCtx = context.WithValue(turnCtx, agentToolFilterKey{}, func(tool agentProviderTool) bool { return !strings.HasPrefix(tool.Name, "external_") })
			if !guard.decided {
				guard.decided, decisionRound = true, round
				if last := len(messages) - 1; last >= 0 && messages[last].ToolResult != nil {
					messages[last].ToolResult.Content += guard.decisionNudge()
				}
			}
		}
		turn, err := s.requestAgentTurn(turnCtx, messages, emit)
		if err != nil {
			_ = appendActivity("error", "Agent execution failed. Check the provider configuration and retry.")
			return
		}
		// A turn with no visible text and no tool calls (a thinking model that spent its output budget on
		// reasoning, an empty stream) is not an answer: one retry within the turn budget, then the run fails with
		// the provider's finish reason. Only sizes are logged; the reasoning never reaches the thread.
		if len(turn.ToolCalls) == 0 && strings.TrimSpace(turn.Text) == "" {
			pending.Reset()
			slog.WarnContext(r.Context(), "agent turn returned no visible reply", "task", task.ID, "model", s.agent.Model, "round", round, "retried", emptyTurnRetried, "stop_reason", agentStopReasonLabel(turn.StopReason), "text_bytes", len(turn.Text), "reasoning_bytes", len(turn.Reasoning))
			if !emptyTurnRetried {
				emptyTurnRetried = true
				messages = append(messages, agentProviderMessage{Role: "user", Content: agentEmptyTurnNudge})
				continue
			}
			if guard.codingDue() {
				// A development request still enters Coding; the reply is the session's real state.
				transfer("the agent replied without starting Coding", "")
				return
			}
			if err := appendActivity("error", agentEmptyTurnError(turn.StopReason)); err != nil {
				slog.ErrorContext(r.Context(), "agent run could not record its empty-reply failure", "task", task.ID, "stop_reason", agentStopReasonLabel(turn.StopReason), "error", err)
			}
			return
		}
		// Asking for approval in text instead of calling the tool is not an answer: nothing ran and there is nothing
		// to approve. One corrective retry, then the run fails instead of completing on the question.
		if s.agentTextApprovalRequest(r.Context(), turn) {
			pending.Reset()
			if textApprovalRetried {
				_ = appendActivity("error", agentTextApprovalError)
				return
			}
			textApprovalRetried = true
			messages = append(messages, agentProviderMessage{Role: "assistant", Content: turn.Text, Reasoning: turn.Reasoning}, agentProviderMessage{Role: "user", Content: agentTextApprovalNudge})
			continue
		}
		if len(turn.ToolCalls) == 0 {
			if guard.codingDue() {
				// A plan or a "done" claim instead of Coding: Flow starts it and reports its real state.
				transfer("the agent replied without starting Coding", turn.Text)
				return
			}
			// Links to this workspace's pages under a made-up host point at the app instead.
			// "Done" is only said with the coding session's evidence; otherwise its real state is posted.
			reply := s.builtinFinalReply(r.Context(), task, guard, turn.Text)
			if err := appendActivity("response", rebaseAgentWorkspaceLinks(reply, data.Workspace.URLKey, agentLinkBaseURL(r))); err != nil {
				slog.ErrorContext(r.Context(), "agent run could not record its reply", "task", task.ID, "reply_bytes", len(reply), "error", err)
			}
			return
		}
		if round == decisionRound && guard.codingDue() && !callsTool(turn.ToolCalls, startCodingSessionTool) && !callsTool(turn.ToolCalls, askUserTool) {
			// The forced-decision turn passed without Coding (the model kept searching or did something else):
			// Flow starts it instead of running this turn's calls, and the run ends with its state.
			pending.Reset()
			transfer("the read-only budget was spent without starting Coding", "")
			return
		}
		messages = append(messages, agentProviderMessage{Role: "assistant", Content: turn.Text, ToolCalls: turn.ToolCalls, Reasoning: turn.Reasoning})
		// Mid-task narration written with tool calls is not the answer; only the final turn's text is posted.
		pending.Reset()
		if index := slices.IndexFunc(turn.ToolCalls, func(call domain.AgentToolCall) bool { return call.Name == askUserTool }); index >= 0 {
			question, options, parseErr := parseAskUser(turn.ToolCalls[index])
			if parseErr == nil {
				// The question (with the turn's text before it) ends the run as a select elicitation; the person's
				// reply — a picked option or free text — starts the next run.
				choices := make([]any, 0, len(options.Options))
				for _, option := range options.Options {
					choices = append(choices, map[string]any{"label": option.Label, "value": option.Value})
				}
				body := strings.TrimSpace(strings.TrimSpace(turn.Text) + "\n\n" + question)
				current, err := s.store.ApplicationByUser(r.Context(), task.AppUserID)
				if err != nil || !current.Active || !slices.Contains(current.AccessTeamIDs(), task.TeamID) {
					return
				}
				if next, err := s.store.AppendAgentActivity(r.Context(), task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "elicitation", Body: body, Signal: "select", SignalMetadata: map[string]any{"options": choices}}); err == nil {
					task = next
				}
				return
			}
		}
		// Like chat: only tools of a connector set to always ask wait for approval.
		approval := &domain.AgentApproval{Kind: "connector"}
		for _, call := range turn.ToolCalls {
			if agentToolNeedsApproval(r.Context(), call.Name) {
				approval.Calls = append(approval.Calls, call)
			}
		}
		for _, call := range turn.ToolCalls {
			if call.Name == askUserTool {
				_, _, parseErr := parseAskUser(call)
				messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: fmt.Sprint(parseErr), IsError: true}})
				continue
			}
			if slices.ContainsFunc(approval.Calls, func(item domain.AgentToolCall) bool { return item.ID == call.ID }) {
				continue
			}
			content, failed, ok := runTool(call)
			if !ok {
				return
			}
			messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: content, IsError: failed}})
		}
		if len(approval.Calls) > 0 {
			// One approval for everything held, with Approve / Reject options.
			pendingCall := agentApprovalBatchCall(*approval)
			body := strings.TrimSpace(strings.TrimSpace(turn.Text) + "\n\nAllow " + agentHeldCallNames(r.Context(), approval.Calls) + "?")
			metadata := map[string]any{"options": []any{map[string]any{"label": "Approve", "value": agentApprovalApprove}, map[string]any{"label": "Reject", "value": agentApprovalReject}}}
			if raw, err := json.Marshal(agentApprovalSummary(*approval)); err == nil {
				var summary map[string]any
				if json.Unmarshal(raw, &summary) == nil {
					metadata["approval"] = summary
				}
			}
			current, err := s.store.ApplicationByUser(r.Context(), task.AppUserID)
			if err != nil || !current.Active || !slices.Contains(current.AccessTeamIDs(), task.TeamID) {
				return
			}
			if next, err := s.store.AppendAgentActivity(r.Context(), task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "elicitation", Body: body, Signal: "select", SignalMetadata: metadata, ToolCall: &pendingCall}); err == nil {
				task = next
			}
			return
		}
	}
	if guard.codingDue() {
		transfer("the run reached its turn limit without starting Coding", "")
		return
	}
	if guard.started != nil && guard.started.delivery != codingNotStarted {
		// Coding was requested: the thread reports the session's state rather than a failed run.
		_ = appendActivity("response", codingDeliveryReply(guard.started.delivery, guard.started.blocker, guard.coding))
		return
	}
	_ = appendActivity("error", "Agent reached its tool execution limit without starting Coding or reporting a blocker.")
}

// builtinThreadMessages is the conversation a built-in thread run starts from: the system prompt (rules, workspace,
// team and personal guidance, contextIssues), the task prompt with the document or project it is about, and the
// thread so far. The worker runtime reads the same conversation from the run payload.
func (s *server) builtinThreadMessages(r *http.Request, task domain.AgentTask, data domain.Bootstrap, contextIssues []domain.Issue) []agentProviderMessage {
	issueTitle := agentIssueTitles(data.Issues)
	messages := []agentProviderMessage{{Role: "system", Content: workspaceAgentSystemPrompt(data, contextIssues, nil, task.TeamID) + builtinTaskContext(task)}, {Role: "user", Content: agentModelUserText(s.builtinTaskPrompt(r.Context(), task, data), nil, issueTitle)}}
	if task.ResourceType == "document" && len(data.Documents) == 1 {
		messages[1].Content += "\nDocument: " + documentDisplayTitle(data.Documents[0].Title) + "\n" + data.Documents[0].Content
	}
	if task.ResourceType == "project" && len(data.Projects) == 1 {
		messages[1].Content += "\nProject: " + data.Projects[0].Name + "\n" + data.Projects[0].Description
	}
	if agentWebAccess(data.WorkspaceSettings) {
		messages[0].Content += s.agentWebNote()
	}
	return append(messages, s.builtinThreadHistory(r, task, issueTitle)...)
}

// builtinThreadHistory is the thread so far as a conversation: the person's replies, and the agent's answers and
// questions (its earlier response activities too, so a follow-up has the context of what it said).
func (s *server) builtinThreadHistory(r *http.Request, task domain.AgentTask, issueTitle func(string) string) []agentProviderMessage {
	messages := []agentProviderMessage{}
	deleted := map[string]bool{}
	for after := ""; ; {
		history, err := s.store.AgentActivities(r.Context(), task.WorkspaceKey, task.ID, after)
		if err != nil {
			return messages
		}
		for _, activity := range history {
			body := strings.TrimSpace(activity.Body)
			switch {
			case activity.Type == "deleted":
				if id, ok := activity.Metadata["activityId"].(string); ok {
					deleted[id] = true
				}
			case activity.Type == "elicitation" && activity.ToolCall != nil:
				// An approval question: Flow asked it, not the agent, so only the agent's own words before it are
				// replayed (the person's answer follows as an outcome, the calls' results as connector calls).
				if narration := agentApprovalNarration(activity); narration != "" && !deleted[activity.ID] {
					messages = append(messages, agentProviderMessage{Role: "assistant", Content: narration})
				}
			case activity.Type == "elicitation" || activity.Type == "response":
				if body != "" && !deleted[activity.ID] {
					messages = append(messages, agentProviderMessage{Role: "assistant", Content: body})
				}
			case activity.Type == "prompt" && activity.ToolCall != nil:
				messages = append(messages, agentProviderMessage{Role: "user", Content: agentApprovalOutcome(r.Context(), activity)})
			case activity.Type == "prompt" && body != "":
				messages = append(messages, agentProviderMessage{Role: "user", Content: agentModelUserText(body, nil, issueTitle)})
			case activity.Type == "action" && !activity.Ephemeral && activity.Metadata["connector"] != nil:
				// A later run (after an approval or a reply) keeps what earlier calls returned (as logged,
				// at most 2 KB each) instead of calling the connector again.
				parameter, _ := activity.Metadata["parameter"].(string)
				result, _ := activity.Metadata["result"].(string)
				messages = append(messages, agentProviderMessage{Role: "assistant", Content: "Earlier connector call " + activity.Body + " (" + parameter + ") returned:\n" + result})
			}
		}
		if len(history) < 100 {
			break
		}
		after = history[len(history)-1].ID
	}
	return messages
}

// agentToolErrorContent is a failed tool's result for the model: the real reason, so it can report it.
func agentToolErrorContent(err error) string {
	raw, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(raw)
}

// agentHeldCallNames lists held calls' tool names for activity labels ("delete_issue, delete_issue"); a connector
// tool offered in ctx shows as its connector and tool ("Code search: search_code").
func agentHeldCallNames(ctx context.Context, calls []domain.AgentToolCall) string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		if tool, ok := offeredConnectorTool(ctx, call.Name); ok {
			names = append(names, tool.Policy.Name+": "+tool.RemoteName)
			continue
		}
		names = append(names, call.Name)
	}
	return strings.Join(names, ", ")
}

// agentApprovalSummary is an approval without its held calls, for the thread's question metadata.
func agentApprovalSummary(approval domain.AgentApproval) domain.AgentApproval {
	approval.Calls = nil
	return approval
}

// delegationStartedState follows the agent interaction guidelines: when a
// person delegates an issue to an agent, the issue moves to the team's first
// "started" status unless it is already started, completed or canceled. An
// update that sets a status itself keeps it, and agents delegating (to
// themselves) leave the status alone. It also returns the agent, which the
// history credits with that status change.
func delegationStartedState(data *domain.Bootstrap, issue domain.Issue, input domain.IssueUpdateInput) (*string, string) {
	if input.StateID != nil || data.Viewer.App || data.Viewer.ID == "" {
		return nil, ""
	}
	target := ""
	if input.DelegateID != nil {
		target = *input.DelegateID
	} else if input.AssigneeID != nil {
		if candidate := userByID(data, *input.AssigneeID); candidate != nil && candidate.App {
			target = candidate.ID
		}
	}
	if target == "" || issue.Delegate != nil && issue.Delegate.ID == target {
		return nil, ""
	}
	if delegate := userByID(data, target); delegate == nil || !delegate.CanDelegateToTeam(issue.Team) {
		return nil, ""
	}
	switch issue.State.Type {
	case "started", "completed", "canceled":
		return nil, ""
	}
	priority := issue.Priority
	if input.Priority != nil {
		priority = *input.Priority
	}
	if settings := teamSettings(data, issue.Team.ID); issue.State.Type == "backlog" && issue.TriagedAt == nil && settings.TriageEnabled && settings.TriageRequirePriority && priority == 0 {
		// The team requires a priority before an issue leaves triage.
		return nil, ""
	}
	state := stateForTeamByType(data, issue.Team.ID, "started")
	if state == nil {
		return nil, ""
	}
	return &state.ID, target
}

// delegationStatusActorChange marks an update's changes when delegating moved the issue to a started status;
// its value is the agent. appendIssueUpdateActivity reads and removes it.
const delegationStatusActorChange = "stateChangedByDelegate"

// delegationStatusChanges are the change keys of a status move that the history credits to the agent.
var delegationStatusChanges = []string{"stateBefore", "stateBeforeId", "stateBeforeType", "state", "stateId", "stateType"}

// appendIssueUpdateActivity records an issue update in the history. When delegating moved the issue to a
// started status, the person's row keeps the delegation (and any other change) and the status move becomes a
// second row by the agent, as the agent picking the issue up is what starts it. The returned event is the
// person's row (the one notifications follow); it is the agent's row when nothing else changed.
func appendIssueUpdateActivity(data *domain.Bootstrap, issueID string, changes map[string]string) domain.ActivityEvent {
	agentID := changes[delegationStatusActorChange]
	delete(changes, delegationStatusActorChange)
	agent := userByID(data, agentID)
	if agent == nil || changes["stateId"] == "" {
		return appendActivity(data, issueID, "issue.updated", data.Viewer, changes)
	}
	status := map[string]string{}
	for _, key := range delegationStatusChanges {
		if value, ok := changes[key]; ok {
			status[key] = value
			delete(changes, key)
		}
	}
	var primary domain.ActivityEvent
	if len(changes) > 0 {
		primary = appendActivity(data, issueID, "issue.updated", data.Viewer, changes)
	}
	moved := appendActivity(data, issueID, "issue.updated", *agent, status)
	if primary.ID == "" {
		return moved
	}
	if moved.ID == primary.ID {
		// Both rows were stamped in the same nanosecond; keep the ids distinct.
		items := data.Activities[issueID]
		items[len(items)-1].ID += "_status"
		moved = items[len(items)-1]
	}
	if !moved.CreatedAt.After(primary.CreatedAt) {
		// The agent's row follows the delegation in the history.
		items := data.Activities[issueID]
		items[len(items)-1].CreatedAt = primary.CreatedAt.Add(time.Microsecond)
	}
	return primary
}

// acknowledgeBuiltinTask posts the built-in agent's first activity as soon as
// it picks a session up (the guidelines ask for one within 10 seconds): an
// ephemeral "looking at the issue" step that the next activity replaces.
func (s *server) acknowledgeBuiltinTask(ctx context.Context, task domain.AgentTask, data domain.Bootstrap) domain.AgentTask {
	if task.PendingTool != nil {
		// Resuming an approved or rejected action reports that action at once.
		return task
	}
	parameter := task.ResourceType
	if len(data.Issues) == 1 && (task.ResourceType == "" || task.ResourceType == "issue") {
		parameter = data.Issues[0].Identifier
	}
	activity := domain.AgentActivity{ActorID: task.AppUserID, Type: "action", Body: "get_issue", Ephemeral: true, Metadata: map[string]any{"action": "get_issue", "parameter": parameter}}
	if task.ResourceType == "document" {
		activity.Body, activity.Metadata["action"] = "get_document", "get_document"
	} else if task.ResourceType == "project" {
		activity.Body, activity.Metadata["action"] = "get_project", "get_project"
	}
	if next, err := s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, activity); err == nil {
		return next
	}
	return task
}

// builtinTaskContext tells the built-in agent where its reply goes: a thread
// on the resource, where (unlike the chat panel) there are no suggestion chips.
func builtinTaskContext(task domain.AgentTask) string {
	return "\nThis session is a comment thread on the " + firstNonEmpty(task.ResourceType, "issue") + ", not the chat panel: your final reply is posted in the thread as Markdown. Do not end it with a ```suggestions block.\n"
}

// builtinTaskPrompt is builtinTaskPromptWith with what this run knows about coding (whether it can start coding
// sessions, whether the issue asks for code changes, a coding environment the instructions name).
func (s *server) builtinTaskPrompt(ctx context.Context, task domain.AgentTask, data domain.Bootstrap) string {
	connectors := []string{}
	offered, _ := ctx.Value(connectorToolsKey{}).([]connectorTool)
	for _, tool := range offered {
		if !slices.Contains(connectors, tool.Policy.Name) {
			connectors = append(connectors, tool.Policy.Name)
		}
	}
	return builtinTaskPromptWith(task, data, s.builtinCodingContextFor(ctx, task, data), connectors)
}

// builtinTaskPromptFor is builtinTaskPromptWith knowing only whether the agent can start coding sessions.
func builtinTaskPromptFor(task domain.AgentTask, data domain.Bootstrap, canCode bool, connectors []string) string {
	return builtinTaskPromptWith(task, data, builtinCodingContext{CanCode: canCode}, connectors)
}

// builtinTaskPromptWith is the first user message of a built-in agent session:
// the mention's comment, or — for a delegated issue — what delegation means
// and what work to do. With coding.CanCode (coding sessions on and the
// start_coding_session tool offered) code changes go to a coding session, and
// when the issue clearly asks for them that is the first step; connectors
// names the MCP connectors whose tools the session is offered.
func builtinTaskPromptWith(task domain.AgentTask, data domain.Bootstrap, coding builtinCodingContext, connectors []string) string {
	prompt := task.InitialPrompt
	if prompt == "" {
		prompt = task.Prompt
	}
	if task.Trigger != "delegation" || len(data.Issues) != 1 {
		return prompt
	}
	issue := data.Issues[0]
	delegator := "A teammate"
	if user := userByID(&data, task.CreatorID); user != nil {
		delegator = firstNonEmpty(user.DisplayName, user.Name, delegator)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s delegated issue %s to you. You are its delegate", delegator, issue.Identifier)
	if issue.Assignee != nil && !issue.Assignee.App {
		fmt.Fprintf(&text, "; %s stays the responsible assignee", firstNonEmpty(issue.Assignee.DisplayName, issue.Assignee.Name))
	}
	fmt.Fprintf(&text, ".\nIts status is now %s. Read the issue with get_issue before you mention its status or other fields, and only report values you read.\n", issue.State.Name)
	if instructions := strings.TrimSpace(strings.TrimPrefix(prompt, issue.Title)); instructions != "" {
		fmt.Fprintf(&text, "Instructions from %s:\n%s\n", delegator, instructions)
	}
	developing := coding.CanCode && coding.Development
	if developing {
		fmt.Fprintf(&text, "This issue asks for code changes. Your first step is start_coding_session with issueId %s", issue.Identifier)
		if coding.EnvironmentKnown {
			fmt.Fprintf(&text, " and environmentId %s (its coding environment)", coding.EnvironmentID)
		} else if len(coding.Environments) > 0 {
			fmt.Fprintf(&text, " (no coding environment is known: %s; leave environmentId out and the session asks which one)", coding.environmentBlocker())
		}
		text.WriteString(" and a prompt that restates the change, the tests to run and that a draft pull/merge request is wanted. " +
			"Do not search or read the code before it: the coding agent clones the repository and reads it itself. " +
			"Its result says whether the session started, waits for approval or could not start; report exactly that. " +
			"The work is delivered only when the session shows the code changes, test results and the draft pull/merge request, so do not say the development is done in this reply.\n" +
			"Then do the work you can do in Flow:\n")
	} else {
		text.WriteString("Start working on it now and do the work you can do in Flow:\n")
	}
	text.WriteString("- triage it: set the priority, labels, estimate or project when they are unset and the issue makes them clear;\n" +
		"- break larger work down into sub-issues;\n" +
		"- write the plan or spec it needs (as your reply, or as a comment or description update);\n" +
		"- when what is wanted is genuinely ambiguous, call ask_user with a short question and options instead of guessing.\n")
	if coding.CanCode && !developing {
		text.WriteString("If the issue asks for code changes (a fix, an implementation, tests or a pull request), call start_coding_session now with its issueId and a prompt " +
			"that describes the change: a coding agent changes the code in a sandbox and opens a draft pull request. Do not write the code or a plan for it yourself. " +
			"Pass environmentId when the instructions name a coding environment; otherwise leave it out and the session picks the repository or asks.\n")
	} else if !coding.CanCode && len(connectors) == 0 {
		text.WriteString("You have no access to code or repositories. If the issue needs code changes, say so in one sentence and do the parts you can do here.\n")
	}
	if len(connectors) > 0 {
		fmt.Fprintf(&text, "You can use the tools of the connected MCP servers (%s). ", strings.Join(connectors, ", "))
		if coding.CanCode {
			text.WriteString("Use them to answer questions about code, not to prepare code changes: for those, start the coding session instead of reading the code yourself. ")
		} else {
			text.WriteString("When the issue needs what they provide, such as searching or reading code, call them instead of guessing. ")
		}
		fmt.Fprintf(&text, "Cite what you found: for code, the repository and branch, file paths and line numbers, and your reasoning. "+
			"Pass each tool only the arguments its schema lists, never repeat a call with the same arguments, and stop after a few calls: "+
			"this session allows at most %d connector calls before you must answer or start Coding.\n", builtinPrepMaxCalls)
	}
	text.WriteString("End with a short reply in the language of the issue: what you did and what is left. Refer to the issue by its identifier only; do not repeat its title.")
	return text.String()
}

// agentEmptyTurnNudge is the corrective message after a model turn with no visible text and no tool calls.
const agentEmptyTurnNudge = "Your last turn produced no visible reply and no tool call, so the user saw nothing. Answer now in plain text (briefly, without long deliberation), or call the tool you need."

// agentStopReasonLabel is a provider finish reason fit for an error message and the logs: lower-case letters,
// digits and underscores only, "none" when the provider gave none.
func agentStopReasonLabel(reason string) string {
	label := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return -1
	}, reason)
	if len(label) > 32 {
		label = label[:32]
	}
	return firstNonEmpty(label, "none")
}

// agentEmptyTurnError is the error a run ends with when its retry also came back without a visible reply.
func agentEmptyTurnError(stopReason string) string {
	label := agentStopReasonLabel(stopReason)
	switch label {
	case "length", "max_tokens", "max_output_tokens":
		return "The model used its whole output budget without writing a reply (finish reason: " + label + "), twice. Retry, or raise the Agent's output token limit."
	}
	return "The model returned no reply and no tool call (finish reason: " + label + "), twice. Retry the request."
}
