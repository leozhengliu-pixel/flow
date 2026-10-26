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

// This file holds the end-to-end harness for built-in agent runs on a delegated issue (a scripted chat-completions
// model, a code-search MCP connector, coding sessions on the fake provider) and the regression test of the
// reported loop. It uses only helpers that predate the run guard, so the regression test also runs against the
// code before the fix.

// codeSearchResponder answers a code-search tool call: the text result and whether it is an MCP error.
type codeSearchResponder func(tool string, args map[string]any) (string, bool)

// sourcebotMCP is a code-search MCP server shaped like the one in the report: glob, read_file and list_repos, where
// list_repos takes only a query. calls counts tools/call requests per tool; respond (nil: a one-line match) answers
// them.
func sourcebotMCP(t *testing.T, mu *sync.Mutex, calls map[string]int, respond codeSearchResponder) *httptest.Server {
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
			args, _ := request.Params["arguments"].(map[string]any)
			mu.Lock()
			calls[name]++
			mu.Unlock()
			text, isError := "acme/api@main api/go.mod", false
			if respond != nil {
				text, isError = respond(name, args)
			}
			content := []any{}
			if text != "" {
				content = append(content, map[string]any{"type": "text", "text": text})
			}
			result = map[string]any{"content": content, "isError": isError}
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

// prompt is the text of the request's system and user messages.
func (r scriptedRequest) prompt() string {
	text := ""
	for _, message := range r.Messages {
		if content, ok := message.Content.(string); ok && (message.Role == "system" || message.Role == "user") {
			text += content + "\n"
		}
	}
	return text
}

// lastContent is the text of the request's last message (the tool result the model reads).
func (r scriptedRequest) lastContent() string {
	if len(r.Messages) == 0 {
		return ""
	}
	text, _ := r.Messages[len(r.Messages)-1].Content.(string)
	return text
}

// delegationHarness runs delegated issues against the built-in agent: Coding on (fake provider), a Sourcebot-like
// connector, and a scripted model.
type delegationHarness struct {
	s            *server
	handler      http.Handler
	workspace    string
	teamID       string
	agentUser    string
	environments []domain.CodingEnvironment
	glob         string
	readFile     string
	listRepos    string

	mcpMu    sync.Mutex
	upstream map[string]int
	respond  codeSearchResponder

	mu       sync.Mutex
	requests []scriptedRequest
	script   func(index int) string
}

// newDelegationHarness sets up a workspace with Coding on and one coding environment per repository.
func newDelegationHarness(t *testing.T, repositories ...string) *delegationHarness {
	t.Helper()
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
	h := &delegationHarness{s: s, handler: handler, workspace: data.Workspace.URLKey, teamID: data.Teams[0].ID, upstream: map[string]int{}}
	for _, repository := range repositories {
		environment, err := s.codingStore().CreateCodingEnvironment(t.Context(), domain.CodingEnvironment{WorkspaceKey: h.workspace, Name: repository, CodeHost: domain.CodeHostGitLab, HostName: "gitlab.com", Repository: repository, DefaultBranch: "main", SandboxSize: domain.CodingSizeMedium})
		if err != nil {
			t.Fatal(err)
		}
		h.environments = append(h.environments, environment)
	}
	upstream := sourcebotMCP(t, &h.mcpMu, h.upstream, func(tool string, args map[string]any) (string, bool) {
		if h.respond != nil {
			return h.respond(tool, args)
		}
		return "acme/api@main api/go.mod", false
	})
	t.Cleanup(upstream.Close)
	setTestConnectors(t, s.store, applicationPolicy{ID: "sourcebot", Kind: "mcp", Name: "Sourcebot", URL: upstream.URL, Status: "approved", Shared: true})
	h.glob, h.readFile, h.listRepos = connectorToolName("sourcebot", "glob"), connectorToolName("sourcebot", "read_file"), connectorToolName("sourcebot", "list_repos")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var request scriptedRequest
		_ = json.Unmarshal(raw, &request)
		h.mu.Lock()
		h.requests = append(h.requests, request)
		reply := h.script(len(h.requests) - 1)
		h.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(provider.Close)
	s.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "glm-5", APIKey: "test", Timeout: 5 * time.Second}
	s.agentClient = provider.Client()
	h.agentUser = s.builtinAgentUserID(t.Context(), h.workspace)
	return h
}

// delegationRun is what one delegated run did.
type delegationRun struct {
	issue      domain.Issue
	task       domain.AgentTask
	activities []domain.AgentActivity
	requests   []scriptedRequest
	upstream   map[string]int
}

// response is the run's final reply; errors are its error activities.
func (r delegationRun) response() string {
	reply := ""
	for _, activity := range r.activities {
		if activity.Type == "response" {
			reply = activity.Body
		}
	}
	return reply
}

func (r delegationRun) errors() []string {
	var errors []string
	for _, activity := range r.activities {
		if activity.Type == "error" {
			errors = append(errors, activity.Body)
		}
	}
	return errors
}

// startCalls counts start_coding_session calls: the model's, and Flow's own (an action activity for the tool).
func (r delegationRun) startActions() int {
	count := 0
	for _, activity := range r.activities {
		if activity.Type == "action" && strings.Contains(activity.Body, startCodingSessionTool) {
			count++
		}
	}
	return count
}

// delegate creates an issue delegated to the built-in agent and runs it with script (turn index → SSE reply;
// issue is set before the run starts).
func (h *delegationHarness) delegate(t *testing.T, title, description string, script func(index int, issue domain.Issue) string) delegationRun {
	t.Helper()
	h.mu.Lock()
	h.requests = nil
	h.mu.Unlock()
	h.mcpMu.Lock()
	for key := range h.upstream {
		delete(h.upstream, key)
	}
	h.mcpMu.Unlock()
	issue := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "description": description, "teamId": h.teamID, "delegateId": h.agentUser}, http.StatusCreated)
	h.mu.Lock()
	h.script = func(index int) string { return script(index, issue) }
	h.mu.Unlock()
	task, err := h.s.store.AgentTask(t.Context(), h.workspace, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	h.s.deliverApplicationTask(t.Context(), task)
	task, _ = h.s.store.AgentTask(t.Context(), h.workspace, task.ID)
	activities, _ := h.s.store.AgentActivities(t.Context(), h.workspace, task.ID, "")
	run := delegationRun{issue: issue, task: task, activities: activities, upstream: map[string]int{}}
	h.mu.Lock()
	run.requests = append(run.requests, h.requests...)
	h.mu.Unlock()
	h.mcpMu.Lock()
	for key, value := range h.upstream {
		run.upstream[key] = value
	}
	h.mcpMu.Unlock()
	return run
}

// The reported loop, as the user's diagnostic reproduced it: Coding on, start_coding_session offered with the
// "start now" instruction, and a model that sends the SAME connector call (same arguments) every turn. Before the
// fix Flow ran it until the turn cap and ended with "Agent reached its tool execution limit". Now the call reaches
// the connector once, the repeats are answered from the first result, and the run ends in Coding — started by Flow,
// since the model never calls it — without a tool-limit error. The run converges through the guard's own budgets,
// within a few turns whatever the turn cap (FLOW_AGENT_MAX_TOOL_TURNS) is, so the cap is not what ends it.
func TestBuiltinRegressionRepeatedConnectorCallEndsInCoding(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	environment := h.environments[0]
	description := "请实现 multiply 接口，补充单元测试并运行测试，创建草稿 MR。environmentId：" + environment.ID + "。"
	run := h.delegate(t, "新增 multiply 接口", description, func(index int, _ domain.Issue) string {
		return chatToolCallSSE("c_glob_"+string(rune('a'+index)), h.glob, map[string]any{"pattern": "**/go.mod"})
	})

	first := run.requests[0]
	if !first.offers(startCodingSessionTool) || !strings.Contains(first.prompt(), "start_coding_session") {
		t.Fatal("the run offers start_coding_session and tells the agent to start it")
	}
	if run.upstream["glob"] != 1 {
		t.Fatalf("the repeated call reaches the connector once, got %d", run.upstream["glob"])
	}
	if len(run.requests) < 2 || !strings.Contains(run.requests[1].lastContent(), "acme/api@main api/go.mod") {
		t.Fatal("the first call's result reaches the model")
	}
	for index, request := range run.requests[2:] {
		if content := request.lastContent(); !strings.Contains(content, "already made this exact call") {
			t.Fatalf("repeat %d is short-circuited with the earlier result: %s", index+1, content)
		}
	}
	// The first call, builtinMaxNoProgress answered repeats, then the forced decision: far below any turn cap.
	if limit := builtinMaxNoProgress + 3; len(run.requests) > limit {
		t.Fatalf("the run converges through the guard within %d model turns, not the turn cap: %d", limit, len(run.requests))
	}
	if errors := run.errors(); len(errors) > 0 {
		t.Fatalf("no tool-limit error: %v", errors)
	}
	if run.task.CodingSessionID == "" {
		t.Fatalf("the run ends in Coding: %+v", run.task)
	}
	session, err := h.s.store.CodingSession(t.Context(), h.workspace, run.task.CodingSessionID)
	if err != nil || session.EnvironmentID != environment.ID || session.IssueID != run.issue.ID {
		t.Fatalf("the session runs in the named environment for the issue: %+v %v", session, err)
	}
	if run.startActions() != 1 {
		t.Fatalf("the thread shows the start_coding_session action once: %+v", run.activities)
	}
	if reply := run.response(); !strings.Contains(reply, "Coding 会话") || !strings.Contains(reply, run.issue.Identifier) || strings.Contains(reply, "已完成") {
		t.Fatalf("the reply states the session's real state: %q", reply)
	}
}
