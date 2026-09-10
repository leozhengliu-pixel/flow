package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRemoteMCPOAuthDiscoveryPKCERefreshAndScope(t *testing.T) {
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	var base string
	var refreshCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource", scope="read"`)
			w.WriteHeader(401)
		case "/.well-known/oauth-protected-resource":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}, "scopes_supported": []string{"read", "write"}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token", "registration_endpoint": base + "/register", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}})
		case "/register":
			var input map[string]any
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input["token_endpoint_auth_method"] != "none" {
				t.Error("expected public PKCE client")
			}
			input["client_id"] = "client"
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(input)
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("resource") != base+"/mcp" {
				t.Error("resource audience missing")
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshCalls.Add(1)
			} else if len(r.Form.Get("code_verifier")) < 43 {
				t.Error("PKCE verifier missing")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "token_type": "Bearer", "expires_in": 3600})
		default:
			w.WriteHeader(404)
		}
	}))
	defer provider.Close()
	base = provider.URL
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s := &server{store: repo, authDisabled: true, allowedOrigin: "http://127.0.0.1:5173"}
	handler := newHandler(s)
	data := repo.Bootstrap()
	if err := repo.MutateWorkspace(t.Context(), "test-workspace", "application_policy.updated", "remote", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.MCPConnectorsEnabled = true
		setApplicationPolicies(data, []applicationPolicy{{ID: "remote", URL: base + "/mcp", Kind: "mcp", Name: "Remote", Status: "approved", OwnerID: data.Viewer.ID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	started := requestJSON[map[string]string](t, handler, "POST", "/api/application-policies/remote/oauth/start?workspace=test-workspace", nil, 200)
	authorization, _ := url.Parse(started["authorizationURL"])
	q := authorization.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("scope") != "read" || q.Get("resource") != base+"/mcp" {
		t.Fatalf("invalid authorization query: %s", authorization.RawQuery)
	}
	callback := "/api/connector-oauth/callback?state=" + url.QueryEscape(q.Get("state")) + "&code=accepted"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", callback, nil))
	if rec.Code != 303 {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	requestJSON[map[string]any](t, handler, "GET", callback, nil, 400)
	id := connectorSecretID("test-workspace", data.Viewer.ID, "remote")
	raw, err := repo.ReadConnectorSecret(t.Context(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	var saved connectorCredential
	_ = json.Unmarshal(raw, &saved)
	saved.Token.Expiry = time.Now().Add(-time.Minute)
	raw, _ = json.Marshal(saved)
	if err = repo.PutConnectorSecret(t.Context(), id, raw, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	credential, err := s.connectorCredential(t.Context(), "test-workspace", data.Viewer.ID, applicationPolicy{ID: "remote", URL: base + "/mcp"})
	if err != nil || credential.Token.AccessToken != "access" || refreshCalls.Load() != 1 {
		t.Fatalf("refresh failed %v %v", credential, err)
	}
	other, err := s.connectorCredential(t.Context(), "test-workspace", "another-user", applicationPolicy{ID: "remote", URL: base + "/mcp"})
	if err != nil || other != nil {
		t.Fatal("personal OAuth credential visible to another user")
	}
}

func TestSDKElicitationIsBidirectionalAndReturnsUserResponse(t *testing.T) {
	remote := mcp.NewServer(&mcp.Implementation{Name: "Fixture", Version: "1"}, nil)
	mcp.AddTool(remote, &mcp.Tool{Name: "confirm", Description: "Confirm an operation"}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		result, err := req.Session.Elicit(ctx, &mcp.ElicitParams{Mode: "form", Message: "Choose a region", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"region": map[string]any{"type": "string", "enum": []string{"eu", "us"}}}, "required": []string{"region"}}})
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: result.Action + ":" + result.Content["region"].(string)}}}, nil, nil
	})
	host := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
	defer host.Close()
	s := &server{authDisabled: true}
	var called atomic.Bool
	ctx := context.WithValue(t.Context(), connectorContextKey{}, connectorRequestContext{Elicit: func(_ context.Context, item applicationPolicy, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		called.Store(true)
		if item.Name != "Fixture" || params.Message != "Choose a region" {
			t.Error("wrong server attribution")
		}
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"region": "eu"}}, nil
	}})
	client, err := s.openConnector(ctx, applicationPolicy{Name: "Fixture", URL: host.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	raw, err := client.rpc(ctx, "tools/call", map[string]any{"name": "confirm", "arguments": map[string]any{}}, false)
	if err != nil || !strings.Contains(string(raw), "accept:eu") || !called.Load() {
		t.Fatalf("bidirectional elicitation failed: %s %v", raw, err)
	}
}

func TestElicitationInvalidResponsePreservesPendingForm(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	viewer := repo.Bootstrap().Viewer
	if err = repo.MutateWorkspace(t.Context(), "test-workspace", "agent.session_created", "session", nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: "session", UserID: viewer.ID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pending := &agentElicitation{Workspace: "test-workspace", SessionID: "session", UserID: viewer.ID, Mode: "form", Schema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","minimum":1}},"required":["count"],"additionalProperties":false}`), Decision: make(chan mcp.ElicitResult, 1)}
	s := &server{store: repo, authDisabled: true, agentElicitations: map[string]*agentElicitation{"prompt": pending}}
	handler := newHandler(s)
	path := "/api/agent/sessions/session/elicitations/prompt?workspace=test-workspace"
	requestJSON[map[string]any](t, handler, "POST", path, map[string]any{"action": "accept", "content": map[string]any{"count": 0}}, 400)
	if s.agentElicitations["prompt"] != pending {
		t.Fatal("validation error consumed pending form")
	}
	requestJSON[map[string]any](t, handler, "POST", path, map[string]any{"action": "accept", "content": map[string]any{"count": 2}}, 200)
	result := <-pending.Decision
	if result.Action != "accept" {
		t.Fatal("response not delivered")
	}
	requestJSON[map[string]any](t, handler, "POST", path, map[string]any{"action": "cancel"}, 404)
}

func TestElicitationRoundTripWaitsForTheOwningUserAndOmitsAnswersFromParts(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	if err = repo.MutateWorkspace(t.Context(), "test-workspace", "agent.session_created", "elicitation-session", nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: "elicitation-session", UserID: data.Viewer.ID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := &server{store: repo, authDisabled: true}
	handler := newHandler(s)
	parts := make(chan domain.AgentMessagePart, 2)
	done := make(chan *mcp.ElicitResult, 1)
	go func() {
		result, err := s.requestAgentElicitation(t.Context(), "test-workspace", "elicitation-session", data.Viewer.ID, applicationPolicy{Name: "Fixture", URL: "https://example.test/mcp"}, &mcp.ElicitParams{Mode: "form", Message: "Choose a value", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}}}, func(part domain.AgentMessagePart, _ string) error { parts <- part; return nil })
		if err != nil {
			t.Error(err)
		}
		done <- result
	}()
	var part domain.AgentMessagePart
	select {
	case part = <-parts:
	case <-time.After(5 * time.Second):
		t.Fatal("form was not emitted")
	}
	requestJSON[map[string]any](t, handler, "POST", "/api/agent/sessions/elicitation-session/elicitations/"+part.ID+"?workspace=test-workspace", map[string]any{"action": "accept", "content": map[string]any{"value": "private-response-value"}}, 200)
	select {
	case result := <-done:
		if result == nil || result.Content["value"] != "private-response-value" {
			t.Fatal("user response not delivered to server")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("elicitation did not resume")
	}
	final := <-parts
	encoded, _ := json.Marshal(final)
	if strings.Contains(string(encoded), "private-response-value") || final.Elicitation.Action != "accept" {
		t.Fatal("answers leaked into stored message part")
	}
}

func TestRemoteOAuthStateIsBoundToTheInitiatingBrowserSession(t *testing.T) {
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	app := httptest.NewServer(newHandler(&server{store: repo}))
	defer app.Close()
	first, second := authClient(t), authClient(t)
	session := authRequest[domain.AuthSession](t, first, "POST", app.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	authRequest[domain.AuthSession](t, second, "POST", app.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	parsed, _ := url.Parse(app.URL)
	token := ""
	for _, cookie := range first.Jar.Cookies(parsed) {
		if cookie.Name == sessionCookieName {
			token = cookie.Value
		}
	}
	pending := connectorOAuthState{Workspace: "test-workspace", UserID: session.User.ID, ConnectorID: "connector", URL: "https://example.test/mcp", SessionHash: secretHash(token)}
	raw, _ := json.Marshal(pending)
	if err = repo.PutConnectorSecret(t.Context(), "mcp_oauth_fixture", raw, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	path := app.URL + "/api/connector-oauth/callback?state=mcp_oauth_fixture&error=access_denied"
	authRequest[map[string]any](t, second, "GET", path, nil, "", 403)
	if _, err = repo.ReadConnectorSecret(t.Context(), "mcp_oauth_fixture", false); err != nil {
		t.Fatal("wrong browser consumed OAuth state")
	}
	authRequest[map[string]any](t, first, "GET", path, nil, "", 400)
	if _, err = repo.ReadConnectorSecret(t.Context(), "mcp_oauth_fixture", false); err == nil {
		t.Fatal("declined authorization remained replayable")
	}
}

func TestAgentBuiltinToolsRetainAPIKeyTeamRestrictions(t *testing.T) {
	repo, actor, ctx := newMCPToolTestContext(t)
	key := domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}
	ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
	s := &server{store: repo}
	s.agent.ToolsEnabled = true
	request := httptest.NewRequest("POST", "/api/agent?workspace="+actor.WorkspaceKey, nil).WithContext(ctx)
	raw, err := s.executeAgentTool(request, repo.Bootstrap(), domain.AgentToolCall{Name: "list_teams", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Items []domain.Team `json:"items"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Items) != 0 {
		t.Fatalf("Agent bypassed API key team restriction: %s", raw)
	}
}
