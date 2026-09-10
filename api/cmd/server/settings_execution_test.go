package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestOAuthRequiresAdminApprovalAndRevokesExistingToken(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.MutateWorkspace(t.Context(), "test-workspace", "workspace_preferences.updated", "workspace", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.ReviewThirdPartyApplications = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer app.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, "POST", app.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	registered := authRequest[domain.OAuthClient](t, client, "POST", app.URL+"/oauth/register", map[string]any{"client_name": "Policy test", "redirect_uris": []string{"http://127.0.0.1:43119/callback"}, "token_endpoint_auth_method": "none"}, "", 201)
	verifier := strings.Repeat("v", 48)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	input := map[string]any{"clientId": registered.ClientID, "redirectUri": registered.RedirectURIs[0], "responseType": "code", "scope": "read", "codeChallenge": challenge, "codeChallengeMethod": "S256", "workspaceKey": "test-workspace", "approve": true}
	authRequest[map[string]any](t, client, "POST", app.URL+"/api/oauth/authorization-request", input, "", 403)
	policies := authRequest[[]applicationPolicy](t, client, "GET", app.URL+"/api/application-policies?workspace=test-workspace", nil, "", 200)
	if len(policies) != 1 || policies[0].Status != "pending" {
		t.Fatalf("missing approval request: %+v", policies)
	}
	policy := policies[0]
	policy.Status = "approved"
	authRequest[applicationPolicy](t, client, "PUT", app.URL+"/api/application-policies?workspace=test-workspace", policy, "", 200)
	decision := authRequest[map[string]string](t, client, "POST", app.URL+"/api/oauth/authorization-request", input, "", 200)
	redirect, _ := url.Parse(decision["redirect"])
	tokens := postOAuthForm[struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}](t, app.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "client_id": {registered.ClientID}, "redirect_uri": {registered.RedirectURIs[0]}, "code_verifier": {verifier}}, 200)
	if tokens.AccessToken == "" {
		t.Fatal("approved application did not receive a token")
	}
	policy.Status = "rejected"
	authRequest[applicationPolicy](t, client, "PUT", app.URL+"/api/application-policies?workspace=test-workspace", policy, "", 200)
	request, _ := http.NewRequest("POST", app.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("revoked app still accepted: %d", response.StatusCode)
	}
	postOAuthForm[map[string]any](t, app.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {registered.ClientID}}, 400)
	if err := repo.MutateWorkspace(t.Context(), "test-workspace", "workspace_preferences.updated", "workspace", nil, func(data *domain.Bootstrap) error { data.WorkspaceSettings.RequireTwoFactor = true; return nil }); err != nil {
		t.Fatal(err)
	}
	blocked := authRequest[map[string]string](t, client, "POST", app.URL+"/api/oauth/authorization-request", input, "", 403)
	if blocked["code"] != "mfa_required" {
		t.Fatalf("OAuth grant bypassed target workspace MFA: %+v", blocked)
	}
}

func TestReviewNotificationPreferencesFilterRealEvents(t *testing.T) {
	settings := domain.UserSettings{ReviewRequests: false, GithubTeamReviewRequests: true, ChecksMergeQueue: false, Username: "engineer", ReviewCommentsFilter: "Mentions only"}
	event := externalCodeReviewEvent{Action: "review_requested"}
	if reviewNotificationEnabled(settings, event) {
		t.Fatal("disabled personal review requests still notify")
	}
	event.Action = "check_run"
	if reviewNotificationEnabled(settings, event) {
		t.Fatal("disabled checks still notify")
	}
	event.Action = "submitted"
	event.Comment.Body = "@engineer-two please review"
	if reviewNotificationEnabled(settings, event) {
		t.Fatal("partial username matches a mention")
	}
	event.Comment.Body = "Please review @engineer."
	if !reviewNotificationEnabled(settings, event) {
		t.Fatal("exact mention was suppressed")
	}
	settings.ReviewCommentsFilter = "Exclude Bots"
	event.Sender.Type = "Bot"
	if reviewNotificationEnabled(settings, event) {
		t.Fatal("bot exclusion not applied")
	}
}

func TestAsksVerifiedEmailRoutingPrivacyAndDeduplication(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	teamID := repo.Bootstrap().Teams[0].ID
	created := requestJSON[struct {
		Address      domain.EmailIntakeAddress `json:"address"`
		InboundToken string                    `json:"inboundToken"`
		DNSRecord    map[string]string         `json:"dnsRecord"`
	}](t, handler, "POST", "/api/teams/"+teamID+"/email-intake-addresses", map[string]string{"localPart": "requests", "domain": "example.test"}, 201)
	patch := map[string]any{"reduceSupportPersonalInfo": true, "featureSettings": map[string]any{"asksEmailAddresses": []string{created.Address.Address}}}
	requestJSON[map[string]any](t, handler, "PATCH", "/api/workspace/preferences", patch, 400)
	requestJSON[domain.EmailIntakeAddress](t, handler, "POST", "/api/teams/"+teamID+"/email-intake-addresses/"+created.Address.ID+"/verify", map[string]string{"txtValue": created.DNSRecord["value"]}, 200)
	requestJSON[domain.WorkspaceSettings](t, handler, "PATCH", "/api/workspace/preferences", patch, 200)
	input := map[string]any{"messageId": "ask-settings-mail", "from": "Private Person <private@example.test>", "subject": "Customer request", "text": "Please add a feature"}
	first := requestJSON[domain.Issue](t, handler, "POST", "/api/email-intake/"+created.InboundToken+"/receive", input, 201)
	second := requestJSON[domain.Issue](t, handler, "POST", "/api/email-intake/"+created.InboundToken+"/receive", input, 200)
	if first.ID != second.ID {
		t.Fatal("duplicate message created another issue")
	}
	data := repo.Bootstrap()
	count := 0
	for _, ask := range data.Asks {
		if ask.IssueID == first.ID {
			count++
			if ask.Source != "email" || ask.TeamID != teamID || ask.Requester.Email != "" || ask.Requester.DisplayName == "Private Person" {
				t.Fatalf("unsafe Ask projection: %+v", ask)
			}
		}
	}
	if count != 1 {
		t.Fatalf("want one Ask, got %d", count)
	}
	for _, message := range data.EmailIntakeMessages {
		if message.IssueID == first.ID && message.From != "" {
			t.Fatal("sender identity retained")
		}
	}
	events, err := repo.Events(t.Context(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "email_intake.received" && strings.Contains(string(event.Payload), "private@example.test") {
			t.Fatal("sender identity leaked into event history")
		}
	}
}

func TestApplicationApprovalScopesAndConnectorOwnership(t *testing.T) {
	data := domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{ReviewThirdPartyApplications: true, MCPConnectorsEnabled: true, AllowedMCPConnectors: "approved"}}
	policy := applicationPolicy{ID: "client", Kind: "oauth", Status: "approved", Scopes: []string{"read"}}
	setApplicationPolicies(&data, []applicationPolicy{policy})
	if !applicationApproved(&data, "client", []string{"read"}) || applicationApproved(&data, "client", []string{"write"}) || applicationApproved(&data, "other", []string{"read"}) {
		t.Fatal("approval must bind both client and scopes")
	}
	policy = applicationPolicy{Kind: "mcp", OwnerID: "alice", Status: "approved"}
	if !connectorAllowed(data.WorkspaceSettings, policy, "alice") || connectorAllowed(data.WorkspaceSettings, policy, "bob") {
		t.Fatal("personal connector ownership ignored")
	}
	policy.Shared = true
	if !connectorAllowed(data.WorkspaceSettings, policy, "bob") {
		t.Fatal("approved workspace connector not available")
	}
	policy.Status = "pending"
	if connectorAllowed(data.WorkspaceSettings, policy, "bob") {
		t.Fatal("pending connector bypasses approvals")
	}
	data.WorkspaceSettings.AllowedMCPConnectors = "all"
	policy.Status = "rejected"
	if connectorAllowed(data.WorkspaceSettings, policy, "alice") {
		t.Fatal("rejected connector bypasses deny")
	}
	data.WorkspaceSettings.HIPAACompliance = true
	if agentWorkspacePolicy(data.WorkspaceSettings, "owner") == nil {
		t.Fatal("external processing policy not enforced")
	}
}

func TestUnauthorizedApprovalAttemptDoesNotConsumePendingDecision(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	err = repo.MutateWorkspace(t.Context(), "test-workspace", "agent.session_created", "session", nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: "session", UserID: data.Viewer.ID})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s := server{store: repo, authDisabled: true}
	s.agent.Enabled = true
	pending := &agentApproval{WorkspaceKey: "test-workspace", SessionID: "another-session", UserID: "another-user", Decision: make(chan string, 1)}
	s.registerAgentApproval("pending", pending)
	handler := newHandler(&s)
	requestJSON[map[string]any](t, handler, "POST", "/api/agent/sessions/session/approvals/pending", map[string]string{"decision": "approve"}, 404)
	if s.takeAgentApproval("pending") != pending {
		t.Fatal("unauthorized request consumed another session's approval")
	}
}

func TestMCPConnectorDiscoveryExecutionAndRevocation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					w.WriteHeader(200)
					return
				}
				if r.Method != "POST" {
					w.WriteHeader(405)
					return
				}
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params map[string]any  `json:"params"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("bad request")
					w.WriteHeader(400)
					return
				}
				var result any
				switch request.Method {
				case "server/discover":
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
					return
				case "initialize":
					w.Header().Set("Mcp-Session-Id", "local-session")
					result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}}
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "tools/list":
					result = map[string]any{"tools": []any{map[string]any{"name": "lookup", "description": "Find an item", "inputSchema": map[string]any{"type": "object"}}}}
				case "tools/call":
					calls.Add(1)
					if request.Params["name"] != "lookup" {
						t.Error("wrong remote name")
					}
					result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "found"}}, "isError": false}
				default:
					t.Errorf("unexpected method %s", request.Method)
				}
				if request.Method != "initialize" && (r.Header.Get("Mcp-Session-Id") != "local-session" || r.Header.Get("MCP-Protocol-Version") != "2025-11-25") {
					t.Error("negotiated session headers absent")
				}
				raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: message\ndata: %s\n\n", raw)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(raw)
				}
			}))
			defer upstream.Close()
			repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			s := server{store: repo, authDisabled: true}
			s.agent.ToolsEnabled = true
			s.agent.WriteTools = true
			err = repo.MutateWorkspace(t.Context(), "test-workspace", "application_policy.updated", "connector", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings.MCPConnectorsEnabled = true
				data.WorkspaceSettings.AllowedMCPConnectors = "approved"
				setApplicationPolicies(data, []applicationPolicy{{ID: "connector", Kind: "mcp", Name: "Local fixture", URL: upstream.URL, Status: "approved", OwnerID: data.Viewer.ID}})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := repo.WorkspaceMetadata("test-workspace")
			tools, err := s.discoverConnectorTools(t.Context(), data)
			if err != nil || len(tools) != 1 {
				t.Fatalf("discovery: %v, %v", tools, err)
			}
			request := httptest.NewRequest("POST", "/api/agent?workspace=test-workspace", nil).WithContext(context.WithValue(t.Context(), connectorToolsKey{}, tools))
			call := domain.AgentToolCall{Name: tools[0].Definition.Name, Arguments: json.RawMessage(`{"query":"test"}`)}
			result, err := s.executeAgentTool(request, data, call)
			if err != nil || !strings.Contains(string(result), "found") {
				t.Fatalf("execution: %s %v", result, err)
			}
			err = repo.MutateWorkspace(t.Context(), "test-workspace", "application_policy.updated", "connector", nil, func(data *domain.Bootstrap) error { data.WorkspaceSettings.MCPConnectorsEnabled = false; return nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.executeAgentTool(request, data, call); err == nil || calls.Load() != 1 {
				t.Fatal("revoked connection still executes")
			}
		})
	}
}

func TestReleaseRulesCompletionEdgeAndPipelineScope(t *testing.T) {
	backlog := domain.WorkflowState{ID: "backlog", TeamID: "team", Type: "backlog"}
	done := domain.WorkflowState{ID: "done", TeamID: "team", Type: "completed"}
	data := domain.Bootstrap{Viewer: domain.User{ID: "owner"}, States: []domain.WorkflowState{backlog, done}, Teams: []domain.Team{{ID: "team"}}, Issues: []domain.Issue{{ID: "issue", Team: domain.Team{ID: "team"}, State: backlog}}, TeamSettings: map[string]domain.TeamSettings{"team": {ProgressOrder: "noAction", ReleaseAutomations: []domain.TeamAutomationRule{{Trigger: "pipeline", Action: "done", Enabled: true}}}}, ReleasePipelines: []domain.ReleasePipeline{{ID: "pipeline", Production: true, TeamIDs: []string{"team"}, Stages: []string{"Production"}, StageStatuses: map[string]string{"Production": "released"}}}, Activities: map[string][]domain.ActivityEvent{}}
	release := domain.Release{ID: "release", PipelineID: "pipeline", Status: "inProgress", IssueIDs: []string{"issue"}}
	stage := "Production"
	if err := applyReleaseInput(&data, &release, releaseInput{Stage: &stage}); err != nil {
		t.Fatal(err)
	}
	if data.Issues[0].State.ID != "done" || release.ReleasedAt == nil {
		t.Fatal("stage change did not execute completion rule")
	}
	data.Issues[0].State = backlog
	if err := applyReleaseSettingAutomations(&data, "released", release, time.Now()); err != nil {
		t.Fatal(err)
	}
	if data.Issues[0].State.ID != "backlog" {
		t.Fatal("duplicate callback overwrote manual state")
	}
	data.ReleasePipelines[0].Production = false
	if err := applyReleaseSettingAutomations(&data, "inProgress", release, time.Now()); err != nil {
		t.Fatal(err)
	}
	if data.Issues[0].State.ID != "backlog" {
		t.Fatal("non-production pipeline applied production rule")
	}
}

func TestPulseScheduleCreatesOneSummaryWithoutReplacingHistory(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	before := repo.Bootstrap()
	userID := before.Viewer.ID
	err = repo.MutateWorkspace(t.Context(), "test-workspace", "test.pulse", userID, nil, func(data *domain.Bootstrap) error {
		for id, settings := range data.UserSettings {
			settings.PulseSchedule = "never"
			data.UserSettings[id] = settings
		}
		settings := data.UserSettings[userID]
		settings.PulseSchedule = "default"
		data.UserSettings[userID] = settings
		data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule = "daily"
		data.WorkspaceSettings.FeatureFlags["pulse"] = true
		for id, settings := range data.TeamSettings {
			settings.Timezone = "UTC"
			data.TeamSettings[id] = settings
		}
		project := data.Projects[0]
		data.ProjectUpdates[project.ID] = append(data.ProjectUpdates[project.ID], domain.ProjectUpdate{ID: "pulse-test-update", ProjectID: project.ID, Body: "A new update", User: data.Viewer, CreatedAt: now.Add(-2 * time.Hour)})
		data.Notifications = append(data.Notifications, domain.Notification{ID: "keep-notification", RecipientID: userID, Type: "activity", CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-48 * time.Hour)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s := server{store: repo}
	for i := 0; i < 2; i++ {
		if err := s.preparePulseSummaries(t.Context(), "test-workspace", now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	after := repo.Bootstrap()
	count := 0
	for _, notification := range after.Notifications {
		if notification.Type == "pulseSummary" && notification.RecipientID == userID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want one summary, got %d", count)
	}
	if !slices.ContainsFunc(after.Notifications, func(n domain.Notification) bool { return n.ID == "keep-notification" }) {
		t.Fatal("summary replaced existing notification history")
	}
	if len(before.Issues) != len(after.Issues) {
		t.Fatal("summary touched issue records")
	}
	zone, _ := time.LoadLocation("America/New_York")
	if _, _, due := pulseWindow("weekly", time.Date(2026, 9, 15, 15, 0, 0, 0, time.UTC), zone); due {
		t.Fatal("weekly schedule ran on Tuesday")
	}
	if _, _, due := pulseWindow("daily", time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC), zone); due {
		t.Fatal("daily summary sent before local 9am")
	}
}
