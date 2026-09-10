package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type providerJob = domain.ProviderJob

func providerJobs(data *domain.Bootstrap) []providerJob {
	return data.ProviderJobs
}
func (s *server) saveProviderJob(ctx context.Context, key string, job providerJob) error {
	return s.store.MutateWorkspace(ctx, key, "integration.job_updated", job.ID, nil, func(data *domain.Bootstrap) error {
		items := providerJobs(data)
		index := slices.IndexFunc(items, func(item providerJob) bool { return item.ID == job.ID })
		if index >= 0 && items[index].Status == job.Status && items[index].URL == job.URL && items[index].ExternalID == job.ExternalID && items[index].TurnID == job.TurnID {
			return store.ErrNoMutation
		}
		job.UpdatedAt = time.Now().UTC()
		if index >= 0 {
			items[index] = job
		} else {
			if len(items) >= 1000 {
				return fmt.Errorf("Archive old coding runs before starting more")
			}
			items = append(items, job)
		}
		data.ProviderJobs = items
		return nil
	})
}
func (s *server) findProviderJob(r *http.Request, data domain.Bootstrap, connection domain.IntegrationConnection, id string) (providerJob, error) {
	for _, job := range providerJobs(&data) {
		if job.ID == id && job.ConnectionID == connection.ID && job.UserID == data.Viewer.ID {
			if _, err := s.agentIssueContext(r, []string{job.IssueID}); err != nil {
				return job, err
			}
			return job, nil
		}
	}
	return providerJob{}, errNotFound
}
func (s *server) providerIssue(r *http.Request, id string) (domain.Issue, error) {
	data, err := s.agentIssueContext(r, []string{id})
	if err != nil {
		_, query, queryErr := s.pagedRealtimeMetadata(r)
		if queryErr != nil {
			return domain.Issue{}, queryErr
		}
		query.Text = id
		query.Limit = 25
		query.Archived = "all"
		page, queryErr := s.store.QueryIssueRecords(r.Context(), query)
		if queryErr != nil {
			return domain.Issue{}, queryErr
		}
		for _, issue := range page.Items {
			if strings.EqualFold(issue.Identifier, id) {
				return issue, nil
			}
		}
		return domain.Issue{}, err
	}
	for _, issue := range data.Issues {
		if issue.ID == id {
			return issue, nil
		}
	}
	return domain.Issue{}, errNotFound
}

func (s *server) cursorProviderAction(r *http.Request, data domain.Bootstrap, connection domain.IntegrationConnection, input providerActionInput) (providerResult, error) {
	var job providerJob
	var raw map[string]any
	var err error
	if input.Action == "launch" {
		issue, e := s.providerIssue(r, input.IssueID)
		if e != nil {
			return providerResult{}, e
		}
		repo, e := url.Parse(input.Repository)
		if e != nil || repo.Scheme != "https" || repo.Host != "github.com" || len(strings.Split(strings.Trim(repo.Path, "/"), "/")) != 2 {
			return providerResult{}, fmt.Errorf("Provide a GitHub repository URL")
		}
		raw, err = s.providerJSON(r.Context(), connection, workspaceKey(r), "POST", "/agents", map[string]any{"prompt": map[string]string{"text": issue.Identifier + " " + issue.Title + "\n\n" + issue.Description + "\n\n" + input.Prompt}, "repos": []any{map[string]string{"url": repo.String(), "startingRef": firstNonEmpty(input.Branch, "main")}}, "autoCreatePR": true}, "")
		if err != nil {
			return providerResult{}, err
		}
		agent, _ := raw["agent"].(map[string]any)
		run, _ := raw["run"].(map[string]any)
		external, _ := agent["id"].(string)
		runID, _ := run["id"].(string)
		if external == "" || runID == "" {
			return providerResult{}, fmt.Errorf("Cursor did not return an agent ID")
		}
		id, _ := randomSecret("coding_")
		job = providerJob{ID: id, Provider: "cursor", ConnectionID: connection.ID, IssueID: issue.ID, UserID: data.Viewer.ID, ExternalID: external, TurnID: runID}
		raw = run
	} else {
		job, err = s.findProviderJob(r, data, connection, input.ResourceID)
		if err != nil {
			return providerResult{}, err
		}
		method, path := "GET", "/agents/"+url.PathEscape(job.ExternalID)+"/runs/"+url.PathEscape(job.TurnID)
		if input.Action == "stop" {
			method = "POST"
			path += "/cancel"
		}
		raw, err = s.providerJSON(r.Context(), connection, workspaceKey(r), method, path, nil, "")
		if err != nil {
			return providerResult{}, err
		}
	}
	job.Status, _ = raw["status"].(string)
	if input.Action == "stop" {
		job.Status = "CANCEL_REQUESTED"
	}
	if job.Status == "" {
		return providerResult{}, fmt.Errorf("Cursor did not return a run status")
	}
	git, _ := raw["git"].(map[string]any)
	for _, value := range asArray(git["branches"]) {
		branch, _ := value.(map[string]any)
		if link, ok := branch["prUrl"].(string); ok && strings.HasPrefix(link, "https://github.com/") {
			job.URL = link
			break
		}
	}
	if err = s.saveProviderJob(r.Context(), workspaceKey(r), job); err != nil {
		return providerResult{}, err
	}
	if job.URL != "" {
		if err = s.attachProviderResource(r, data, providerResult{Title: "Cursor pull request", URL: job.URL}, job.IssueID); err != nil {
			return providerResult{}, err
		}
	}
	body, _ := raw["result"].(string)
	return providerResult{ID: job.ID, IssueID: job.IssueID, Status: job.Status, URL: job.URL, Title: "Cursor coding run", Body: truncateSettingsText(body, 32000)}, nil
}

func (s *server) listProviderJobs(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	result := []providerResult{}
	for i := len(data.ProviderJobs) - 1; i >= 0 && len(result) < 50; i-- {
		job := data.ProviderJobs[i]
		if job.UserID != data.Viewer.ID || job.Provider != r.PathValue("provider") {
			continue
		}
		issue, err := s.providerIssue(r, job.IssueID)
		if err != nil {
			continue
		}
		result = append(result, providerResult{ID: job.ID, IssueID: job.IssueID, Title: issue.Identifier + " · " + issue.Title, Status: job.Status, URL: job.URL})
	}
	writeJSON(w, 200, result)
}

func (s *server) codexConnection(ctx context.Context) (*websocket.Conn, error) {
	raw := os.Getenv("FLOW_CODEX_APP_SERVER_URL")
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "ws" && endpoint.Scheme != "wss") {
		return nil, fmt.Errorf("Configure FLOW_CODEX_APP_SERVER_URL for a Codex app-server")
	}
	check := *endpoint
	if check.Scheme == "wss" {
		check.Scheme = "https"
	} else {
		check.Scheme = "http"
	}
	if !integrationEndpointSafe(ctx, check.String(), s.authDisabled) {
		return nil, fmt.Errorf("Codex app-server must use a public WSS endpoint")
	}
	headers := http.Header{}
	if token := os.Getenv("FLOW_CODEX_APP_SERVER_TOKEN"); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	} else if !s.authDisabled {
		return nil, fmt.Errorf("Configure FLOW_CODEX_APP_SERVER_TOKEN")
	}
	conn, _, err := websocket.Dial(ctx, raw, &websocket.DialOptions{HTTPClient: s.connectorHTTPClient(), HTTPHeader: headers})
	if err != nil {
		return nil, fmt.Errorf("Could not connect to Codex app-server")
	}
	conn.SetReadLimit(2 << 20)
	if _, err = codexRPC(ctx, conn, 1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "flow", "title": "Flow", "version": "1"}}); err != nil {
		conn.CloseNow()
		return nil, err
	}
	if err = wsjson.Write(ctx, conn, map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		conn.CloseNow()
		return nil, err
	}
	return conn, nil
}
func codexRPC(ctx context.Context, conn *websocket.Conn, id int, method string, params any) (json.RawMessage, error) {
	if err := wsjson.Write(ctx, conn, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for count := 0; count < 1000; count++ {
		var message struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := wsjson.Read(ctx, conn, &message); err != nil {
			return nil, err
		}
		if message.Method != "" && message.ID != nil {
			_ = wsjson.Write(ctx, conn, map[string]any{"id": *message.ID, "error": map[string]any{"code": -32601, "message": "Interactive approval requires a connected coding client"}})
			continue
		}
		if message.ID != nil && *message.ID == id {
			if len(message.Error) > 0 && string(message.Error) != "null" {
				return nil, fmt.Errorf("Codex rejected %s", method)
			}
			return message.Result, nil
		}
	}
	return nil, fmt.Errorf("Codex response limit exceeded")
}
func (s *server) probeCodex(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := s.codexConnection(ctx)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	raw, err := codexRPC(ctx, conn, 2, "account/read", map[string]any{"refreshToken": false})
	if err != nil {
		return err
	}
	var account struct {
		Account  json.RawMessage `json:"account"`
		Requires bool            `json:"requiresOpenaiAuth"`
	}
	if json.Unmarshal(raw, &account) != nil || account.Requires && (len(account.Account) == 0 || string(account.Account) == "null") {
		return fmt.Errorf("Sign into the configured Codex app-server first")
	}
	return nil
}
func (s *server) codexProviderAction(r *http.Request, data domain.Bootstrap, connection domain.IntegrationConnection, input providerActionInput) (providerResult, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	conn, err := s.codexConnection(ctx)
	if err != nil {
		return providerResult{}, err
	}
	defer conn.CloseNow()
	var job providerJob
	var body string
	if input.Action == "launch" {
		issue, err := s.providerIssue(r, input.IssueID)
		if err != nil {
			return providerResult{}, err
		}
		var roots map[string]string
		_ = json.Unmarshal([]byte(os.Getenv("FLOW_CODEX_WORKSPACES")), &roots)
		cwd := roots[workspaceKey(r)]
		if cwd == "" {
			return providerResult{}, fmt.Errorf("Configure a coding checkout for this workspace in FLOW_CODEX_WORKSPACES")
		}
		raw, err := codexRPC(ctx, conn, 2, "thread/start", map[string]any{"cwd": cwd, "sandbox": "workspace-write", "approvalPolicy": "never"})
		if err != nil {
			return providerResult{}, err
		}
		var thread struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if json.Unmarshal(raw, &thread) != nil || thread.Thread.ID == "" {
			return providerResult{}, fmt.Errorf("Codex did not create a thread")
		}
		raw, err = codexRPC(ctx, conn, 3, "turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []any{map[string]string{"type": "text", "text": issue.Identifier + " " + issue.Title + "\n\n" + issue.Description + "\n\n" + input.Prompt}}})
		if err != nil {
			return providerResult{}, err
		}
		var turn struct {
			Turn struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turn"`
		}
		if json.Unmarshal(raw, &turn) != nil || turn.Turn.ID == "" {
			return providerResult{}, fmt.Errorf("Codex did not start a turn")
		}
		id, _ := randomSecret("coding_")
		job = providerJob{ID: id, Provider: "codex", ConnectionID: connection.ID, IssueID: issue.ID, UserID: data.Viewer.ID, ExternalID: thread.Thread.ID, TurnID: turn.Turn.ID, Status: turn.Turn.Status}
	} else {
		job, err = s.findProviderJob(r, data, connection, input.ResourceID)
		if err != nil {
			return providerResult{}, err
		}
		if input.Action == "stop" {
			_, err = codexRPC(ctx, conn, 2, "turn/interrupt", map[string]string{"threadId": job.ExternalID, "turnId": job.TurnID})
			job.Status = "interruptRequested"
		} else {
			raw, e := codexRPC(ctx, conn, 2, "thread/read", map[string]any{"threadId": job.ExternalID, "includeTurns": true})
			err = e
			var result struct {
				Thread struct {
					Turns []struct {
						ID, Status string
						Items      []struct{ Type, Text string } `json:"items"`
					} `json:"turns"`
				} `json:"thread"`
			}
			if err == nil {
				if json.Unmarshal(raw, &result) != nil {
					return providerResult{}, errInvalid
				}
				for _, turn := range result.Thread.Turns {
					if turn.ID == job.TurnID {
						job.Status = turn.Status
						for _, item := range turn.Items {
							if item.Type == "agentMessage" {
								body += item.Text + "\n\n"
							}
						}
					}
				}
			}
		}
		if err != nil {
			return providerResult{}, err
		}
	}
	if err = s.saveProviderJob(r.Context(), workspaceKey(r), job); err != nil {
		return providerResult{}, err
	}
	return providerResult{ID: job.ID, Title: "Codex coding run", IssueID: job.IssueID, Status: job.Status, Body: truncateSettingsText(body, 32000)}, nil
}

func (s *server) zapierProviderAction(r *http.Request, data domain.Bootstrap, connection domain.IntegrationConnection, input providerActionInput) (providerResult, error) {
	ctx := r.Context()
	if _, ok := ctx.Value(apiKeyContextKey{}).(domain.APIKey); !ok && !s.authDisabled && !s.applicationPolicyAdmin(r, data) {
		return providerResult{}, store.ErrAuthForbidden
	}
	switch input.Action {
	case "createIssue":
		if input.ResourceID == "" || len(input.ResourceID) > 200 {
			return providerResult{}, fmt.Errorf("Provide a stable Zap run ID")
		}
		id, err := s.importProviderResource(r, data, connection, providerResult{ID: input.ResourceID, Title: input.Title, Body: input.Description}, input.TeamID)
		return providerResult{IssueID: id, Status: "created"}, err
	case "updateIssue":
		if _, err := s.providerIssue(r, input.IssueID); err != nil {
			return providerResult{}, err
		}
		patch := domain.IssueUpdateInput{}
		if input.Title != "" {
			patch.Title = &input.Title
		}
		if input.Description != "" {
			patch.Description = &input.Description
		}
		if input.StateID != "" {
			patch.StateID = &input.StateID
		}
		_, err := invokeJSONHandler(ctx, "PATCH", map[string]string{"id": input.IssueID}, patch, s.updateIssueRecord)
		return providerResult{IssueID: input.IssueID, Status: "updated"}, err
	case "subscribe":
		name := "Zapier"
		types := []string{"issues", "projects", "comments"}
		teams := []string{}
		if input.TeamID != "" {
			teams = []string{input.TeamID}
		}
		enabled := true
		saved, err := invokeJSONHandler(ctx, "POST", nil, webhookInput{Name: &name, URL: &input.URL, ResourceTypes: &types, TeamIDs: &teams, Enabled: &enabled}, s.createWebhook)
		if err != nil {
			return providerResult{}, err
		}
		raw, _ := json.Marshal(saved)
		var webhook struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &webhook)
		return providerResult{ID: webhook.ID, Status: "subscribed"}, nil
	case "unsubscribe":
		_, err := invokeJSONHandler(ctx, "DELETE", map[string]string{"id": input.ResourceID}, nil, s.deleteWebhook)
		return providerResult{ID: input.ResourceID, Status: "unsubscribed"}, err
	}
	return providerResult{}, errInvalid
}

func (s *server) calendarProviderAction(r *http.Request, data domain.Bootstrap, connection domain.IntegrationConnection) (providerResult, error) {
	now := time.Now().UTC()
	var until *time.Time
	cursor := ""
	for page := 0; page < 10; page++ {
		query := url.Values{"timeMin": {now.Format(time.RFC3339)}, "timeMax": {now.Add(24 * time.Hour).Format(time.RFC3339)}, "singleEvents": {"true"}, "eventTypes": {"outOfOffice"}, "maxResults": {"250"}}
		if cursor != "" {
			query.Set("pageToken", cursor)
		}
		raw, err := s.providerJSON(r.Context(), connection, workspaceKey(r), "GET", "/calendars/primary/events?"+query.Encode(), nil, "")
		if err != nil {
			return providerResult{}, err
		}
		for _, v := range asArray(raw["items"]) {
			event, _ := v.(map[string]any)
			if event["eventType"] != "outOfOffice" || event["status"] == "cancelled" {
				continue
			}
			start, _ := event["start"].(map[string]any)
			end, _ := event["end"].(map[string]any)
			from, _ := time.Parse(time.RFC3339, fmt.Sprint(start["dateTime"]))
			to, _ := time.Parse(time.RFC3339, fmt.Sprint(end["dateTime"]))
			if !from.IsZero() && !to.IsZero() && !from.After(now) && to.After(now) && (until == nil || to.After(*until)) {
				until = &to
			}
		}
		next, _ := raw["nextPageToken"].(string)
		if next == "" {
			break
		}
		if next == cursor || page == 9 {
			return providerResult{}, fmt.Errorf("Calendar result exceeded pagination limit")
		}
		cursor = next
	}
	// Only the authorizing user's primary calendar can update their availability.
	if connection.ConnectedBy != data.Viewer.ID {
		return providerResult{}, fmt.Errorf("Only the account that connected this calendar can sync its availability")
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.calendar_synced", data.Viewer.ID, nil, func(next *domain.Bootstrap) error {
		if next.Settings == nil {
			next.Settings = map[string]any{}
		}
		key := "calendarAvailability"
		values, _ := next.Settings[key].(map[string]any)
		if values == nil {
			values = map[string]any{}
		}
		previous, _ := values[data.Viewer.ID].(string)
		value := ""
		if until != nil {
			value = until.Format(time.RFC3339)
		}
		if previous == value {
			return store.ErrNoMutation
		}
		if until == nil {
			delete(values, data.Viewer.ID)
		} else {
			values[data.Viewer.ID] = until.Format(time.RFC3339)
		}
		next.Settings[key] = values
		return nil
	})
	status := "Available"
	if until != nil {
		status = "Out of office"
	}
	return providerResult{Status: status, AvailableUntil: until}, err
}

func (s *server) maintainProviderSettings(ctx context.Context, key string) {
	metadata, ok := s.store.WorkspaceMetadata(key)
	if !ok || metadata.WorkspaceSettings.HIPAACompliance {
		return
	}
	for _, connection := range metadata.IntegrationConnections {
		if connection.Provider != "google-calendar" || connection.Status != "connected" {
			continue
		}
		data, err := s.store.PagedWorkspaceMetadata(ctx, key, connection.ConnectedBy)
		if err != nil {
			continue
		}
		actorContext := context.WithValue(ctx, authUserContextKey{}, data.Viewer)
		actorContext = context.WithValue(actorContext, workspaceKeyContextKey{}, key)
		actorContext = store.ContextWithActor(actorContext, data.Viewer)
		request, _ := http.NewRequestWithContext(actorContext, "POST", "http://flow.internal/api/integrations/google-calendar/actions", nil)
		if _, err := s.calendarProviderAction(request, data, connection); err != nil {
			log.Printf("Calendar availability sync workspace=%s connection=%s: %v", key, connection.ID, err)
		}
	}
}
