package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

// sourcebotMCP is a code-search MCP server shaped like the one in the report: glob, read_file and list_repos, where
// list_repos takes only a query. calls counts tools/call requests per tool.
func sourcebotMCP(t *testing.T, mu *sync.Mutex, calls map[string]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(200)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sourcebot")
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "sourcebot", "version": "1"}}
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{"name": "glob", "description": "Find files by pattern", "inputSchema": map[string]any{"type": "object", "required": []any{"pattern"}, "properties": map[string]any{"pattern": map[string]any{"type": "string"}, "repo": map[string]any{"type": "string"}}}},
				map[string]any{"name": "read_file", "description": "Read a file", "inputSchema": map[string]any{"type": "object", "required": []any{"path"}, "properties": map[string]any{"path": map[string]any{"type": "string"}, "repo": map[string]any{"type": "string"}}}},
				map[string]any{"name": "list_repos", "description": "List indexed repositories", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}},
			}}
		case "tools/call":
			name, _ := request.Params["name"].(string)
			mu.Lock()
			calls[name]++
			mu.Unlock()
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "acme/api@main api/go.mod"}}, "isError": false}
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
			return
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
}

// chatToolCallSSE is one chat-completions streamed turn calling tool with args.
func chatToolCallSSE(id, tool string, args map[string]any) string {
	rawArgs, _ := json.Marshal(args)
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "function": map[string]any{"name": tool, "arguments": string(rawArgs)}}}}, "finish_reason": "tool_calls"}}})
	return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
}

func chatTextSSE(text string) string {
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
	return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
}

type scriptedRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"function"`
	} `json:"tools"`
}

func (r scriptedRequest) offers(name string) bool {
	for _, tool := range r.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func (r scriptedRequest) offersConnectors() bool {
	for _, tool := range r.Tools {
		if strings.HasPrefix(tool.Function.Name, "external_") {
			return true
		}
	}
	return false
}

// The reported loop: a delegated development issue (Chinese instructions with an environmentId) where the model
// keeps searching code — the same glob twice, then list_repos with a path it does not take. The repeat is answered
// from the first result, the bad arguments are refused before they reach the server, the preparation budget then
// takes connector tools away and tells the model to decide, start_coding_session is offered and called with the
// environment, and the model's "done" claim is replaced with the session's real state.
func TestBuiltinDelegationDevelopmentLoopStartsCoding(t *testing.T) {
	s, handler := codingTestServer(t, "")
	requestJSON[codingSettingsView](t, handler, http.MethodPatch, "/api/coding/settings", map[string]any{"enabled": true}, http.StatusOK)
	if err := mutateTestWorkspace(t, s.store, func(data *domain.Bootstrap) {
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags[coding.FeatureFlag] = true
	}); err != nil {
		t.Fatal(err)
	}
	data := s.store.Bootstrap()
	workspace := data.Workspace.URLKey
	environment, err := s.codingStore().CreateCodingEnvironment(t.Context(), domain.CodingEnvironment{WorkspaceKey: workspace, Name: "API", CodeHost: domain.CodeHostGitLab, HostName: "gitlab.com", Repository: "acme/api", DefaultBranch: "main", SandboxSize: domain.CodingSizeMedium})
	if err != nil {
		t.Fatal(err)
	}

	var mcpMu sync.Mutex
	upstreamCalls := map[string]int{}
	upstream := sourcebotMCP(t, &mcpMu, upstreamCalls)
	defer upstream.Close()
	setTestConnectors(t, s.store, applicationPolicy{ID: "sourcebot", Kind: "mcp", Name: "Sourcebot", URL: upstream.URL, Status: "approved", Shared: true})
	glob, listRepos := connectorToolName("sourcebot", "glob"), connectorToolName("sourcebot", "list_repos")

	var mu sync.Mutex
	var requests []scriptedRequest
	var issueIdentifier string
	script := func(index int) string {
		switch index {
		case 0:
			return chatToolCallSSE("c_glob", glob, map[string]any{"pattern": "**/go.mod"})
		case 1:
			return chatToolCallSSE("c_glob_again", glob, map[string]any{"pattern": "**/go.mod"})
		case 2:
			return chatToolCallSSE("c_repos", listRepos, map[string]any{"path": "api/go.mod"})
		case 3:
			return chatToolCallSSE("c_code", startCodingSessionTool, map[string]any{"issueId": issueIdentifier, "environmentId": environment.ID, "prompt": "Add the multiply endpoint, run the tests and open a draft MR"})
		}
		return chatTextSSE("开发已完成，已创建草稿 MR。")
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var request scriptedRequest
		_ = json.Unmarshal(raw, &request)
		mu.Lock()
		requests = append(requests, request)
		reply := script(len(requests) - 1)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, reply)
	}))
	defer provider.Close()
	s.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "glm-5", APIKey: "test", Timeout: 5 * time.Second}
	s.agentClient = provider.Client()

	agentUser := s.builtinAgentUserID(t.Context(), workspace)
	description := "请实现 multiply 接口，补充单元测试并运行测试，创建草稿 MR。environmentId：" + environment.ID + "。"
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "新增 multiply 接口", "description": description, "teamId": data.Teams[0].ID, "delegateId": agentUser}, http.StatusCreated)
	issueIdentifier = issue.Identifier
	task, err := s.store.AgentTask(t.Context(), workspace, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	s.deliverApplicationTask(t.Context(), task)

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 5 {
		t.Fatalf("the run takes five model turns (3 searches, Coding, reply), got %d", len(requests))
	}
	first := requests[0]
	prompt := ""
	for _, message := range first.Messages {
		if text, ok := message.Content.(string); ok {
			prompt += text + "\n"
		}
	}
	if !strings.Contains(prompt, "Your first step is start_coding_session with issueId "+issue.Identifier+" and environmentId "+environment.ID) || !strings.Contains(prompt, "Do not search or read the code before it") {
		t.Fatalf("a development delegation with an environment starts with Coding:\n%s", prompt)
	}
	if !first.offers(startCodingSessionTool) || !first.offersConnectors() {
		t.Fatalf("the first turn offers start_coding_session and the connector tools")
	}
	described := false
	for _, tool := range first.Tools {
		if tool.Function.Name == listRepos {
			described = strings.HasPrefix(tool.Function.Description, "Sourcebot tool list_repos. Arguments: query.")
		}
	}
	if !described {
		t.Fatalf("a hashed connector tool's description names the tool and its arguments: %+v", first.Tools)
	}
	if upstreamCalls["glob"] != 1 || upstreamCalls["list_repos"] != 0 {
		t.Fatalf("the repeated glob is answered from the first result and list_repos with a path is not sent: %v", upstreamCalls)
	}
	toolResult := func(request scriptedRequest) string {
		last := request.Messages[len(request.Messages)-1]
		text, _ := last.Content.(string)
		return text
	}
	if repeat := toolResult(requests[2]); !strings.Contains(repeat, "You already made this exact call") || !strings.Contains(repeat, "acme/api@main api/go.mod") {
		t.Fatalf("an identical repeat returns the earlier result with a nudge: %s", repeat)
	}
	rejected := toolResult(requests[3])
	if !strings.Contains(rejected, `unknown property \"path\"`) || !strings.Contains(rejected, "accepted properties: query") {
		t.Fatalf("list_repos with a path is rejected against its schema: %s", rejected)
	}
	if requests[3].offersConnectors() || !requests[3].offers(startCodingSessionTool) || !strings.Contains(rejected, "Connector tools are no longer offered") || !strings.Contains(rejected, "call start_coding_session (issueId "+issue.Identifier+", environmentId "+environment.ID+")") {
		t.Fatalf("once the budget is spent the model must decide without connector tools: %s", rejected)
	}
	if started := toolResult(requests[4]); !strings.Contains(started, `"deliveryStatus"`) || !strings.Contains(started, "Do not say the development is done") {
		t.Fatalf("the start_coding_session result tells the model the delivery state: %s", started)
	}

	task, _ = s.store.AgentTask(t.Context(), workspace, task.ID)
	if task.CodingSessionID == "" {
		t.Fatalf("Coding started on the delegation: %+v", task)
	}
	session, err := s.store.CodingSession(t.Context(), workspace, task.CodingSessionID)
	if err != nil || session.EnvironmentID != environment.ID || session.IssueID != issue.ID {
		t.Fatalf("session: %+v %v", session, err)
	}
	activities, _ := s.store.AgentActivities(t.Context(), workspace, task.ID, "")
	reply := ""
	for _, activity := range activities {
		if activity.Type == "response" {
			reply = activity.Body
		}
		if activity.Type == "error" {
			t.Fatalf("the run ends without an error: %+v", activity)
		}
	}
	if strings.Contains(reply, "开发已完成") || !strings.Contains(reply, "Coding 会话") || !strings.Contains(reply, issue.Identifier) {
		t.Fatalf("a running session is not reported as done: %q", reply)
	}
}

func TestValidateConnectorArguments(t *testing.T) {
	listRepos := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}}}`)
	err := validateConnectorArguments("list_repos", listRepos, map[string]any{"path": "api/go.mod"})
	if err == nil || !strings.Contains(err.Error(), `unknown property "path"`) || !strings.Contains(err.Error(), "accepted properties: limit, query") {
		t.Fatalf("an unknown property is rejected with what the tool takes: %v", err)
	}
	if err := validateConnectorArguments("list_repos", listRepos, map[string]any{"query": "api", "limit": 2.5}); err == nil || !strings.Contains(err.Error(), `"limit" must be integer`) {
		t.Fatalf("a value of the wrong type is rejected: %v", err)
	}
	if err := validateConnectorArguments("list_repos", listRepos, map[string]any{"query": "api", "limit": 5.0}); err != nil {
		t.Fatalf("valid arguments pass: %v", err)
	}
	readFile := json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
	if err := validateConnectorArguments("read_file", readFile, map[string]any{}); err == nil || !strings.Contains(err.Error(), `missing required property "path"`) {
		t.Fatalf("a missing required property is rejected: %v", err)
	}
	open := []json.RawMessage{
		json.RawMessage(`{"type":"object"}`),
		json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"additionalProperties":true}`),
		json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"additionalProperties":{"type":"string"}}`),
	}
	for _, schema := range open {
		if err := validateConnectorArguments("tool", schema, map[string]any{"anything": "x"}); err != nil {
			t.Fatalf("a schema that allows other properties accepts them (%s): %v", schema, err)
		}
	}
	if err := validateConnectorArguments("tool", json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"additionalProperties":false}`), map[string]any{"path": "x"}); err == nil {
		t.Fatal("additionalProperties false rejects other properties")
	}
}

// Repeats converge: the same call with its keys in another order is the same call; repeats, rejected arguments and
// failed searches count as calls without progress, and the budget forces a decision.
func TestBuiltinRunGuardConverges(t *testing.T) {
	a := domain.AgentToolCall{Name: "external_x", Arguments: json.RawMessage(`{"pattern":"**/go.mod","repo":"acme/api"}`)}
	b := domain.AgentToolCall{Name: "external_x", Arguments: json.RawMessage(`{ "repo": "acme/api", "pattern": "**/go.mod" }`)}
	if canonicalToolCallKey(a) != canonicalToolCallKey(b) {
		t.Fatal("argument order and spacing do not make a new call")
	}
	if canonicalToolCallKey(a) == canonicalToolCallKey(domain.AgentToolCall{Name: "external_y", Arguments: a.Arguments}) {
		t.Fatal("another tool is another call")
	}

	guard := newBuiltinRunGuard(builtinCodingContext{})
	if _, _, handled := guard.before(a); handled {
		t.Fatal("a first call runs")
	}
	guard.after(a, "result", false)
	content, failed, handled := guard.before(b)
	if !handled || failed || !strings.Contains(content, "result") || guard.noProgress != 1 {
		t.Fatalf("a repeat is answered from the earlier result: %q %v %v", content, failed, handled)
	}
	if guard.mustDecide(0) {
		t.Fatal("one repeat does not yet force a decision")
	}
	failing := domain.AgentToolCall{Name: "external_z", Arguments: json.RawMessage(`{"q":"x"}`)}
	guard.after(failing, `{"error":"MCP tool reported a failure"}`, true) // a failed search
	if !guard.mustDecide(0) || !strings.Contains(guard.decisionNudge(), "reply now with what you found") {
		t.Fatal("repeats and failed searches without progress force a decision")
	}
	if _, failed, handled := guard.before(domain.AgentToolCall{Name: "external_new", Arguments: json.RawMessage(`{}`)}); !handled || !failed {
		t.Fatal("over budget, a new call is refused without running")
	}

	coding := newBuiltinRunGuard(builtinCodingContext{CanCode: true, Development: true, Issue: "ENG-1", EnvironmentID: "env_1"})
	for index := range builtinCodingPrepMaxCalls {
		call := domain.AgentToolCall{Name: "external_x", Arguments: json.RawMessage(`{"n":` + string(rune('0'+index)) + `}`)}
		coding.after(call, "ok", false)
	}
	if !coding.mustDecide(0) || !strings.Contains(coding.decisionText(), "call start_coding_session (issueId ENG-1, environmentId env_1)") {
		t.Fatalf("a development run decides after %d searches: %s", builtinCodingPrepMaxCalls, coding.decisionText())
	}
	if fresh := newBuiltinRunGuard(builtinCodingContext{}); !fresh.mustDecide(maxAgentToolTurns-builtinDecisionTurns) || fresh.mustDecide(maxAgentToolTurns-builtinDecisionTurns-1) {
		t.Fatal("the last turns of a run are decision turns")
	}
	big := newBuiltinRunGuard(builtinCodingContext{})
	big.after(a, strings.Repeat("x", builtinPrepMaxBytes), false)
	if !big.mustDecide(0) {
		t.Fatal("reading the output budget forces a decision")
	}
}

func TestBuiltinCodingIntentAndEnvironment(t *testing.T) {
	for _, text := range []string{"请实现该功能并运行测试", "修复登录报错，提交草稿 MR", "Implement the endpoint and open a draft PR", "fix the flaky test"} {
		if !builtinDevelopmentIntent.MatchString(text) {
			t.Errorf("development intent: %q", text)
		}
	}
	for _, text := range []string{"这个接口在哪里定义的？", "Where are invoice totals rounded?", "Summarize the discussion"} {
		if builtinDevelopmentIntent.MatchString(text) {
			t.Errorf("not a development request: %q", text)
		}
	}
	for text, want := range map[string]string{
		"environmentId：cenv_abc123。":           "cenv_abc123",
		"Use environmentId: `env-42` please":   "env-42",
		"coding environment env_9 for the api": "env_9",
		"环境 ID: env_7":                         "env_7",
	} {
		match := builtinEnvironmentMention.FindStringSubmatch(text)
		if match == nil || match[1] != want {
			t.Errorf("environment in %q: %v, want %s", text, match, want)
		}
	}
}

// Requested is not delivered: a done claim needs the session's evidence (changes and a pull/merge request).
func TestBuiltinFinalReplyDeliveryState(t *testing.T) {
	now := time.Now()
	sessions := map[codingDelivery]domain.CodingSession{
		codingAwaitingApproval: {State: domain.CodingSessionAwaitingInput, Phase: domain.CodingPhaseAwaitingApproval, ApprovalState: "required"},
		codingRunning:          {State: domain.CodingSessionActive, Phase: domain.CodingPhaseRunning},
		codingAwaitingInput:    {State: domain.CodingSessionAwaitingInput, Phase: domain.CodingPhaseAwaitingUser},
		codingFailed:           {State: domain.CodingSessionError, Phase: domain.CodingPhaseDone},
		codingDelivered:        {State: domain.CodingSessionComplete, Phase: domain.CodingPhaseIdle, Diffs: []domain.CodingDiffSummary{{Path: "api/multiply.go"}}, LatestPullRequest: &domain.SessionPullRequest{URL: "https://gitlab.com/acme/api/-/merge_requests/1"}, EndedAt: &now},
	}
	for want, session := range sessions {
		if got := codingSessionDelivery(session); got != want {
			t.Errorf("delivery %+v = %s, want %s", session, got, want)
		}
	}
	if got := codingSessionDelivery(domain.CodingSession{State: domain.CodingSessionComplete, Phase: domain.CodingPhaseIdle}); got == codingDelivered {
		t.Error("a session that ended without changes or an MR is not delivered")
	}

	for _, claim := range []string{"开发已完成，已创建草稿 MR。", "代码已提交", "I have implemented the endpoint.", "The fix is done.", "Opened a draft MR for it."} {
		if !claimsCodingCompletion(claim) {
			t.Errorf("a completion claim: %q", claim)
		}
	}
	for _, status := range []string{"已启动 Coding 会话，完成后会创建草稿 MR。", "Started a coding session; it will open a draft MR when it finishes.", "已完成分流并设置优先级。"} {
		if claimsCodingCompletion(status) {
			t.Errorf("not a completion claim: %q", status)
		}
	}

	s := &server{}
	chinese := builtinCodingContext{CanCode: true, Development: true, Issue: "ENG-7", Chinese: true}
	guard := newBuiltinRunGuard(chinese)
	guard.started = &codingStartOutcome{delivery: codingAwaitingApproval}
	if reply := s.builtinFinalReply(t.Context(), domain.AgentTask{}, guard, "开发已完成。"); !strings.Contains(reply, "等待团队成员批准") {
		t.Fatalf("awaiting approval is reported as such: %q", reply)
	}
	guard.started = &codingStartOutcome{delivery: codingNotStarted, blocker: "Coding sessions are not enabled"}
	if reply := s.builtinFinalReply(t.Context(), domain.AgentTask{}, guard, "已实现该功能。"); !strings.Contains(reply, "尚未为 ENG-7 启动 Coding 会话") || !strings.Contains(reply, "not enabled") {
		t.Fatalf("a run that could not start Coding reports the blocker: %q", reply)
	}
	guard.started = &codingStartOutcome{delivery: codingRunning}
	if reply := s.builtinFinalReply(t.Context(), domain.AgentTask{}, guard, "已启动 Coding 会话，正在运行。"); reply != "已启动 Coding 会话，正在运行。" {
		t.Fatalf("an accurate status is kept: %q", reply)
	}
	plain := newBuiltinRunGuard(builtinCodingContext{})
	if reply := s.builtinFinalReply(t.Context(), domain.AgentTask{}, plain, "I have fixed the labels."); reply != "I have fixed the labels." {
		t.Fatalf("a run that is not about code keeps its reply: %q", reply)
	}
	english := newBuiltinRunGuard(builtinCodingContext{CanCode: true, Development: true, Issue: "ENG-8"})
	english.started = &codingStartOutcome{delivery: codingRunning}
	if reply := s.builtinFinalReply(t.Context(), domain.AgentTask{}, english, "I have implemented it and opened a draft MR."); !strings.Contains(reply, "nothing is delivered yet") {
		t.Fatalf("an English run reports the running session: %q", reply)
	}
}
