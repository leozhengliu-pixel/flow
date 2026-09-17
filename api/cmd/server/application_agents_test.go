package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	appconfig "flow/api/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func applicationFixture(t *testing.T) (*server, http.Handler, domain.Bootstrap, domain.ApplicationInstallation) {
	t.Helper()
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	data := repo.Bootstrap()
	if err := repo.RegisterOAuthClient(t.Context(), domain.OAuthClient{ClientID: "agent-test", ClientName: "Test agent", RedirectURIs: []string{"https://example.test/callback"}}); err != nil {
		t.Fatal(err)
	}
	s := &server{store: repo, authDisabled: true, uploadPath: t.TempDir()}
	handler := newHandler(s)
	response := requestJSON[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, handler, "POST", "/api/application-installations", map[string]any{"clientId": "agent-test", "teamIds": []string{data.Teams[0].ID}, "scopes": []string{"read", "write", "app:assignable", "app:mentionable"}, "active": true}, 200)
	return s, handler, data, response.Application
}

func TestApplicationDelegationCreatesDurableSessionAndPreservesAssignee(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Agent task", "teamId": data.Teams[0].ID, "assigneeId": data.Viewer.ID}, 201)
	updated := requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+issue.ID, map[string]any{"delegateId": app.UserID}, 200)
	if updated.Assignee == nil || updated.Assignee.ID != data.Viewer.ID || updated.Delegate == nil || updated.AgentSessionID == "" {
		t.Fatalf("delegation: %+v", updated)
	}
	task, err := s.store.AgentTask(t.Context(), data.Workspace.URLKey, updated.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "pending" || task.AppUserID != app.UserID || task.CreatorID != data.Viewer.ID {
		t.Fatalf("task: %+v", task)
	}
	requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+issue.ID, map[string]any{"delegateId": ""}, 200)
	task, err = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	if err != nil || task.Status != "canceled" {
		t.Fatalf("stale task: %+v %v", task, err)
	}
}

func TestApplicationMentionAndActivityStateMachine(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Mention", "teamId": data.Teams[0].ID}, 201)
	requestJSON[domain.Comment](t, handler, "POST", "/api/issues/"+issue.ID+"/comments", map[string]any{"body": "Please investigate", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": app.UserID, "label": "Agent"}}}}}, 201)
	tasks, err := s.store.ListAgentTasks(t.Context(), data.Workspace.URLKey, issue.ID, app.UserID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("mention did not start session: %+v %v", tasks, err)
	}
	task := tasks[0]
	task, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{Type: "elicitation", Body: "Which branch?", ActorID: app.UserID})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "awaitingInput" {
		t.Fatal(task.Status)
	}
	requestJSON[any](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "response", "body": "Cannot impersonate app"}, 403)
	task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "main"}, 200)
	if task.Status != "pending" || task.InitialPrompt != "Please investigate" || task.Prompt != "main" {
		t.Fatal(task.Status)
	}
	_, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version-1, domain.AgentActivity{Type: "response", Body: "stale"})
	if err != store.ErrIssueVersion {
		t.Fatalf("concurrent response accepted: %v", err)
	}
	task, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{Type: "response", Body: "Done", ActorID: app.UserID})
	if err != nil || task.Status != "complete" {
		t.Fatal(err)
	}
}

func TestApplicationDocumentAndProjectMentionsUseResourceAccess(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	body := map[string]any{"body": "@Agent inspect this resource", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": app.UserID, "label": "Agent"}}}}}}}
	doc := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "Agent document", "teamIds": app.TeamIDs}, 201)
	requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+doc.ID+"/comments", body, 201)
	tasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=document&resourceId="+doc.ID, nil, 200)
	if len(tasks) != 1 || tasks[0].ResourceType != "document" || tasks[0].IssueID != "" {
		t.Fatalf("document task context: %+v", tasks)
	}
	project := data.Projects[0]
	app.TeamIDs = project.TeamIDs
	app.InstalledBy = data.Viewer.ID
	if _, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, app, ""); err != nil {
		t.Fatal(err)
	}
	requestJSON[domain.Comment](t, handler, "POST", "/api/projects/"+project.ID+"/comments", body, 201)
	tasks = requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=project&resourceId="+project.ID, nil, 200)
	if len(tasks) != 1 || tasks[0].ResourceType != "project" {
		t.Fatalf("project task context: %+v", tasks)
	}
}

func TestApplicationWebhookSignedAndReplayIdentifiable(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	var body []byte
	var signature, delivery string
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		signature = r.Header.Get("X-Flow-Signature")
		delivery = r.Header.Get("X-Flow-Delivery")
		w.WriteHeader(204)
	}))
	defer host.Close()
	app.WebhookURL = host.URL
	app.InstalledBy = data.Viewer.ID
	if _, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, app, "secret"); err != nil {
		t.Fatal(err)
	}
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Webhook", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	task, err := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	s.deliverApplicationTask(context.Background(), task)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	if len(body) == 0 || signature != hex.EncodeToString(mac.Sum(nil)) || delivery != task.ID+":1" {
		t.Fatalf("unsigned delivery: %s %s", signature, delivery)
	}
	claimed, err := s.store.ClaimApplicationTask(t.Context(), task.ID, task.Version)
	if err != nil || !claimed {
		t.Fatal(err)
	}
	claimed, err = s.store.ClaimApplicationTask(t.Context(), task.ID, task.Version)
	if err != nil || claimed {
		t.Fatal("duplicate worker claim")
	}
}

func TestApplicationTokenCannotReadAnotherTeamOrImpersonateAnotherAgent(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	other, setupErr := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "other-app", Name: "Other app", Active: true, InstalledBy: data.Viewer.ID, TeamIDs: app.TeamIDs, Scopes: app.Scopes}, "")
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	otherIssue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Other agent", "teamId": data.Teams[0].ID, "delegateId": other.UserID}, 201)
	foreignTeam := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": "Another team", "key": "OTH"}, 201)
	foreignIssue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Outside app scope", "teamId": foreignTeam.ID}, 201)
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Assigned", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	client, _ := s.store.OAuthClient(t.Context(), app.ClientID)
	_, err := s.store.CreateOAuthAuthorizationGrant(t.Context(), "app-code", domain.OAuthAuthorizationCode{ClientID: client.ClientID, Actor: "app", InstallerID: data.Viewer.ID, UserID: data.Viewer.ID, WorkspaceKey: data.Workspace.URLKey, TeamIDs: app.TeamIDs, Scopes: []string{"read", "app:assignable"}, ExpiresAt: time.Now().Add(time.Hour)}, domain.OAuthAuthorization{ID: "app-grant"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.store.ExchangeOAuthGrant(t.Context(), "authorization_code", "app-code", client.ClientID, "refresh", domain.APIKey{ID: "access", SecretHash: secretHash("token"), CreatedAt: time.Now()}, func(domain.OAuthAuthorizationCode) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.authDisabled = false
	call := func(method, path, body string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s = %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
	}
	call("GET", "/api/agent-tasks?issueId="+issue.ID, "", 200)
	call("GET", "/api/agent-tasks/"+otherIssue.AgentSessionID, "", 404)
	call("POST", "/api/agent-tasks/"+otherIssue.AgentSessionID+"/activities", `{"expectedVersion":1,"type":"response","body":"spoof"}`, 404)
	call("GET", "/api/issue-records/"+foreignIssue.ID, "", 404)
	call("POST", "/api/agent-tasks/"+issue.AgentSessionID+"/activities", `{"expectedVersion":1,"type":"response","body":"Completed by app"}`, 200)
	call("POST", "/api/agent-tasks/"+issue.AgentSessionID+"/activities", `{"expectedVersion":2,"type":"prompt","body":"spoof user"}`, 403)
	call("POST", "/api/application-installations", `{}`, 401)
	app.Active = false
	app.InstalledBy = data.Viewer.ID
	if _, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, app, ""); err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/agent-tasks?issueId="+issue.ID, "", 401)
}

func TestBuiltinApplicationStreamsAndFinishesUnderItsOwnIdentity(t *testing.T) {
	s, handler, data, _ := applicationFixture(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"**Done**\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	s.agent = appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, APIKey: "test", Model: "test", Timeout: time.Second * 5}
	s.agentClient = provider.Client()
	app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Builtin", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	task, err := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	s.deliverApplicationTask(t.Context(), task)
	task, err = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	if err != nil || task.Status != "complete" {
		t.Fatalf("builtin failed: %+v %v", task, err)
	}
	activities, err := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
	if err != nil || len(activities) == 0 {
		t.Fatal(err)
	}
	last := activities[len(activities)-1]
	if last.Type != "response" || last.ActorID != app.UserID || last.Body != "**Done**" {
		t.Fatalf("wrong app output: %+v", last)
	}
}

func TestApplicationExplicitToolApprovalCannotBeInferredFromText(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Approval", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	task, _ := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	call := domain.AgentToolCall{ID: "call", Name: "save_issue", Arguments: json.RawMessage(`{"title":"changed"}`), Status: "pending"}
	task, err := s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{Type: "elicitation", ToolCall: &call, ActorID: app.UserID})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON[any](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "approve"}, 400)
	task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "approve": false}, 200)
	if task.PendingTool == nil || task.PendingTool.Status != "rejected" {
		t.Fatal("lost explicit approval decision")
	}
	if task.Prompt != "Approval" {
		t.Fatal("approval reply erased the original task prompt")
	}
}

func TestApplicationOAuthHTTPConsentAndPKCE(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "oauth-app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	registered := authRequest[domain.OAuthClient](t, client, "POST", host.URL+"/oauth/register", map[string]any{"client_name": "External worker", "redirect_uris": []string{"https://example.test/callback"}, "token_endpoint_auth_method": "none"}, "", 201)
	data := repo.Bootstrap()
	verifier := strings.Repeat("v", 48)
	digest := sha256.Sum256([]byte(verifier))
	input := map[string]any{"actor": "app", "clientId": registered.ClientID, "redirectUri": registered.RedirectURIs[0], "responseType": "code", "scope": "read app:mentionable app:assignable", "codeChallenge": base64.RawURLEncoding.EncodeToString(digest[:]), "codeChallengeMethod": "S256", "workspaceKey": data.Workspace.URLKey, "approve": true}
	authRequest[map[string]any](t, client, "POST", host.URL+"/api/oauth/authorization-request", input, "", 403)
	input["teamIds"] = []string{data.Teams[0].ID}
	decision := authRequest[map[string]string](t, client, "POST", host.URL+"/api/oauth/authorization-request", input, "", 200)
	redirect, err := url.Parse(decision["redirect"])
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatal("missing application authorization code")
	}
	tokens := postOAuthForm[struct {
		AccessToken string `json:"access_token"`
	}](t, host.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "client_id": {registered.ClientID}, "redirect_uri": {registered.RedirectURIs[0]}, "code_verifier": {verifier}}, 200)
	auth, err := repo.AuthenticateAPIKeyRecord(t.Context(), data.Workspace.URLKey, secretHash(tokens.AccessToken), nil)
	if err != nil || !auth.User.App || auth.User.ID == data.Viewer.ID || auth.Key.TeamRestriction != "selected" || len(auth.Key.TeamIDs) != 1 {
		t.Fatalf("application authorization lost identity or scope: %v", err)
	}
	input["scope"] = "read email app:assignable"
	authRequest[map[string]any](t, client, "POST", host.URL+"/api/oauth/authorization-request", input, "", 400)
}

func TestBuiltinApplicationExecutesOnlyTheApprovedStoredTool(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "approved"}[approve], func(t *testing.T) {
			s, handler, data, _ := applicationFixture(t)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer provider.Close()
			s.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "test", APIKey: "test", Timeout: 5 * time.Second}
			s.agentClient = provider.Client()
			app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
			if err != nil {
				t.Fatal(err)
			}
			issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Before approval", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
			task, _ := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
			args, _ := json.Marshal(map[string]any{"id": issue.ID, "title": "Approved edit"})
			call := domain.AgentToolCall{ID: "stored-call", Name: "save_issue", Arguments: args, Status: "pending"}
			task, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{Type: "elicitation", ToolCall: &call})
			if err != nil {
				t.Fatal(err)
			}
			task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "approve": approve}, 200)
			s.deliverApplicationTask(t.Context(), task)
			updated := requestJSON[domain.Issue](t, handler, "GET", "/api/issue-records/"+issue.ID, nil, 200)
			want := "Before approval"
			if approve {
				want = "Approved edit"
			}
			if updated.Title != want {
				t.Fatalf("approval=%v: got title %q, want %q", approve, updated.Title, want)
			}
			task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
			if task.Status != "complete" || task.PendingTool != nil {
				t.Fatalf("approval was not consumed: %+v", task)
			}
		})
	}
}
