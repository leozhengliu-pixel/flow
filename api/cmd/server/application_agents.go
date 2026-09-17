package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	}
	if input.Builtin {
		if !s.agent.Enabled {
			writeError(w, 409, "Configure Flow Agent before enabling its application member")
			return
		}
		input.ClientID = "builtin-flow-agent"
		input.Name = "Flow Agent"
		input.WebhookURL = ""
		input.Scopes = []string{"read", "write", "app:assignable", "app:mentionable"}
	} else {
		if input.ClientID == "builtin-flow-agent" {
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
		input.AvatarURL = client.LogoURI
		if input.WebhookURL != "" && !integrationEndpointSafe(r.Context(), input.WebhookURL, s.authDisabled) {
			writeError(w, 400, "Invalid application webhook URL")
			return
		}
	}
	if len(input.TeamIDs) == 0 || len(input.TeamIDs) > 500 {
		writeError(w, 400, "Select at least one team (up to 500)")
		return
	}
	if !slices.Contains(input.Scopes, "read") {
		writeError(w, 400, "Application read scope is required")
		return
	}
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
	writeJSON(w, 200, map[string]any{"session": task, "activities": activities})
}

func (s *server) appendApplicationAgentActivity(w http.ResponseWriter, r *http.Request) {
	task, data, err := s.authorizedAgentTask(r, r.PathValue("id"), true)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	var input struct {
		Version int64  `json:"expectedVersion"`
		Type    string `json:"type"`
		Body    string `json:"body"`
		URL     string `json:"url"`
		Approve *bool  `json:"approve,omitempty"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 80<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	if !data.Viewer.App && input.Type != "prompt" && input.Type != "canceled" && input.Type != "retry" {
		writeError(w, 403, "Only the session application may emit agent activities")
		return
	}
	if input.Type == "prompt" && input.Approve == nil && strings.TrimSpace(input.Body) == "" {
		writeError(w, 400, "A reply is required")
		return
	}
	if data.Viewer.App && (input.Type == "prompt" || input.Type == "canceled" || input.Type == "retry") {
		writeError(w, 403, "Application cannot impersonate user input")
		return
	}
	if input.URL != "" && !validOAuthMetadataURI(input.URL) {
		writeError(w, 400, "Activity links must use HTTPS")
		return
	}
	activity := domain.AgentActivity{ActorID: data.Viewer.ID, Type: input.Type, Body: input.Body, URL: input.URL}
	if input.Type == "prompt" && task.PendingTool != nil {
		if input.Approve == nil {
			writeError(w, 400, "Explicit approval or rejection is required")
			return
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
	app, err := s.store.ApplicationByUser(ctx, task.AppUserID)
	if err != nil || !app.Active || !slices.Contains(app.TeamIDs, task.TeamID) {
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
					if err != nil || !installation.Active || !slices.Contains(installation.TeamIDs, task.TeamID) {
						cancel()
						return
					}
				}
			}
		}(ctx)
	}
	user := app.User()
	key := domain.APIKey{CreatorID: user.ID, Scopes: app.Scopes, TeamRestriction: "selected", TeamIDs: app.TeamIDs, OAuthClientID: app.ClientID}
	ctx = context.WithValue(ctx, authUserContextKey{}, user)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, task.WorkspaceKey)
	ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
	ctx = store.ContextWithActor(ctx, user)
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://flow.internal/api/agent-tasks/"+task.ID, nil)
	data, err := s.applicationTaskContext(r, task, false)
	if err != nil {
		_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{Type: "canceled", Body: "The application does not have access to this resource. Grant access before retrying."})
		return
	}
	data.Viewer = app.User()
	data.ViewerRole = "app"
	if agentWorkspacePolicy(data.WorkspaceSettings, "app") != nil {
		_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{Type: "canceled", Body: "Agent execution is disabled by workspace policy."})
		return
	}
	if app.Builtin {
		if task.Status == "active" {
			_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "error", Body: "Execution was interrupted. Inspect previous tool results before retrying."})
			return
		}
		s.executeBuiltinApplication(r, app, task, data)
		return
	}
	if task.Status == "active" {
		_ = s.store.FinishApplicationDelivery(ctx, task.ID, task.Version, true)
		return
	}
	if app.WebhookURL == "" {
		_ = s.store.FinishApplicationDelivery(ctx, task.ID, task.Version, true)
		return
	}
	secret, err := s.store.ApplicationWebhookSecret(ctx, app.ID)
	if err != nil {
		return
	}
	action := "created"
	if task.Version > 1 {
		action = "prompted"
	}
	payload := map[string]any{"type": "AgentSessionEvent", "action": action, "eventId": fmt.Sprintf("%s:%d", task.ID, task.Version), "agentSession": task}
	if len(data.Issues) == 1 {
		payload["issue"] = data.Issues[0]
	} else if task.ResourceType == "document" && len(data.Documents) == 1 {
		payload["document"] = data.Documents[0]
	} else if task.ResourceType == "project" && len(data.Projects) == 1 {
		payload["project"] = data.Projects[0]
	}
	body, _ := json.Marshal(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	success := false
	if integrationEndpointSafe(ctx, app.WebhookURL, s.authDisabled) {
		req, _ := http.NewRequestWithContext(ctx, "POST", app.WebhookURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Flow-Signature", hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-Flow-Delivery", fmt.Sprintf("%s:%d", task.ID, task.Version))
		client := secureOutboundClient(10 * time.Second)
		if s.authDisabled && safeLocalDevelopmentURL(app.WebhookURL) {
			client = &http.Client{Timeout: 10 * time.Second}
		}
		resp, err := client.Do(req)
		if err == nil {
			success = resp.StatusCode >= 200 && resp.StatusCode < 300
			resp.Body.Close()
		}
	}
	_ = s.store.FinishApplicationDelivery(ctx, task.ID, task.Version, success)
}

func (s *server) executeBuiltinApplication(r *http.Request, app domain.ApplicationInstallation, task domain.AgentTask, data domain.Bootstrap) {
	appendWithTool := func(kind, body string, call *domain.AgentToolCall) error {
		current, err := s.store.ApplicationByUser(r.Context(), task.AppUserID)
		if err != nil || !current.Active || !slices.Contains(current.TeamIDs, task.TeamID) {
			return store.ErrAuthForbidden
		}
		next, err := s.store.AppendAgentActivity(r.Context(), task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: kind, Body: body, ToolCall: call})
		if err == nil {
			task = next
		}
		return err
	}
	appendActivity := func(kind, body string) error { return appendWithTool(kind, body, nil) }
	execute := func(call domain.AgentToolCall) ([]byte, error) {
		current, err := s.store.ApplicationByUser(r.Context(), task.AppUserID)
		if err != nil || !current.Active || !slices.Contains(current.TeamIDs, task.TeamID) {
			return nil, store.ErrAuthForbidden
		}
		key := domain.APIKey{CreatorID: current.UserID, Scopes: current.Scopes, TeamRestriction: "selected", TeamIDs: current.TeamIDs, OAuthClientID: current.ClientID}
		req := r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, key))
		return s.executeAgentTool(req, data, call)
	}
	if !s.agent.Enabled {
		_ = appendActivity("error", "Flow Agent is not configured.")
		return
	}
	var pending strings.Builder
	pendingKind := "output"
	streamBytes := 0
	last := time.Now()
	emit := func(event agentProviderEvent) error {
		if event.Type != "text.delta" && event.Type != "reasoning.delta" {
			return nil
		}
		streamBytes += len(event.Delta)
		if streamBytes > 1<<20 {
			return fmt.Errorf("agent response exceeds the session budget")
		}
		kind := "output"
		if event.Type == "reasoning.delta" {
			kind = "thought"
		}
		if pending.Len() > 0 && kind != pendingKind {
			if err := appendActivity(pendingKind, pending.String()); err != nil {
				return err
			}
			pending.Reset()
		}
		pendingKind = kind
		pending.WriteString(event.Delta)
		if pending.Len() > 32000 || time.Since(last) > time.Second && pending.Len() > 100 {
			if err := appendActivity(pendingKind, pending.String()); err != nil {
				return err
			}
			pending.Reset()
			last = time.Now()
		}
		return nil
	}
	prompt := task.InitialPrompt
	if prompt == "" {
		prompt = task.Prompt
	}
	messages := []agentProviderMessage{{Role: "system", Content: workspaceAgentSystemPrompt(data, data.Issues, nil)}, {Role: "user", Content: prompt}}
	if task.ResourceType == "document" && len(data.Documents) == 1 {
		messages[1].Content += "\nDocument: " + data.Documents[0].Title + "\n" + data.Documents[0].Content
	}
	if task.ResourceType == "project" && len(data.Projects) == 1 {
		messages[1].Content += "\nProject: " + data.Projects[0].Name + "\n" + data.Projects[0].Description
	}
	for after := ""; ; {
		history, err := s.store.AgentActivities(r.Context(), task.WorkspaceKey, task.ID, after)
		if err != nil {
			_ = appendActivity("error", "Could not load the agent conversation. Retry the task.")
			return
		}
		for _, activity := range history {
			if activity.Type == "elicitation" {
				messages = append(messages, agentProviderMessage{Role: "assistant", Content: activity.Body})
			} else if activity.Type == "prompt" && strings.TrimSpace(activity.Body) != "" {
				messages = append(messages, agentProviderMessage{Role: "user", Content: activity.Body})
			}
		}
		if len(history) < 100 {
			break
		}
		after = history[len(history)-1].ID
	}
	if task.PendingTool != nil {
		call := *task.PendingTool
		if call.Status != "approved" {
			_ = appendActivity("action", "Action rejected: "+call.Name)
			_ = appendActivity("response", "The requested action was not executed.")
			return
		}
		if err := appendActivity("action", "Approved action: "+call.Name); err != nil {
			return
		}
		result, err := execute(call)
		if err != nil {
			result = []byte(`{"error":"Approved tool execution failed"}`)
		}
		messages = append(messages, agentProviderMessage{Role: "assistant", ToolCalls: []domain.AgentToolCall{call}}, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: string(result), IsError: err != nil}})
	}
	for round := 0; round < maxAgentToolTurns; round++ {
		turn, err := s.requestAgentTurn(r.Context(), messages, emit)
		if err != nil {
			_ = appendActivity("error", "Agent execution failed. Check the provider configuration and retry.")
			return
		}
		if len(turn.ToolCalls) == 0 {
			if pending.Len() > 0 {
				_ = appendActivity(pendingKind, pending.String())
			}
			_ = appendActivity("response", turn.Text)
			return
		}
		messages = append(messages, agentProviderMessage{Role: "assistant", Content: turn.Text, ToolCalls: turn.ToolCalls})
		for _, call := range turn.ToolCalls {
			if s.agentToolRequiresApproval(call.Name) || strings.HasPrefix(call.Name, "external_") {
				call.Status = "pending"
				_ = appendWithTool("elicitation", "This action requires your confirmation: "+call.Name+"\n"+string(call.Arguments), &call)
				return
			}
			if err := appendActivity("action", call.Name); err != nil {
				return
			}
			result, err := execute(call)
			if err != nil {
				result = []byte(`{"error":"Tool execution failed"}`)
			}
			messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: string(result), IsError: err != nil}})
		}
	}
	_ = appendActivity("error", "Agent reached its tool execution limit.")
}
