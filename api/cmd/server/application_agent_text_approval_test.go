package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

// providerAssistantTexts is what the assistant messages of a recorded Chat Completions request say.
func providerAssistantTexts(t *testing.T, request string) []string {
	t.Helper()
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(request), &payload); err != nil {
		t.Fatal(err)
	}
	texts := []string{}
	for _, message := range payload.Messages {
		if text, ok := message.Content.(string); ok && message.Role == "assistant" {
			texts = append(texts, text)
		}
	}
	return texts
}

// Connector approval is driven only by real tool calls, across several approvals in one delegated session: a held
// call is an elicitation with Approve / Reject options and a pending tool; later runs never replay Flow's
// "Allow …?" question as the agent's own words; a reply that asks for approval in text is retried once and then
// fails the session instead of completing it; only the delegator (or an admin) answers the approval.
func TestBuiltinConnectorApprovalRequiresToolCalls(t *testing.T) {
	s, handler, data, _ := applicationFixture(t)
	var calls, toolCount atomic.Int32
	toolCount.Store(1)
	upstream := fakeCodeMCP(t, &calls, &toolCount)
	defer upstream.Close()

	var mu sync.Mutex
	var requests []string
	script := map[int]string{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(raw))
		reply, ok := script[len(requests)-1]
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if !ok {
			reply = "data: {\"choices\":[{\"delta\":{\"content\":\"Found it in src/billing/invoice.go:42.\"}}]}\n\ndata: [DONE]\n\n"
		}
		_, _ = io.WriteString(w, reply)
	}))
	defer provider.Close()
	toolCall := func(id, name string) string {
		return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"" + id + "\",\"function\":{\"name\":\"" + name + "\",\"arguments\":\"{\\\"query\\\":\\\"roundTotal\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	}
	text := func(body string) string {
		raw, _ := json.Marshal(body)
		return "data: {\"choices\":[{\"delta\":{\"content\":" + string(raw) + "},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	}
	search := connectorToolName("code", "search_code")
	// next scripts the model's next requests in order.
	next := func(replies ...string) {
		mu.Lock()
		for index, reply := range replies {
			script[len(requests)+index] = reply
		}
		mu.Unlock()
	}
	requestCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(requests)
	}
	request := func(index int) string {
		mu.Lock()
		defer mu.Unlock()
		return requests[index]
	}
	// noApprovalText fails when a request from index on replays an "Allow …?" question as the agent's words.
	noApprovalText := func(from int, step string) {
		t.Helper()
		for index := from; index < requestCount(); index++ {
			for _, said := range providerAssistantTexts(t, request(index)) {
				if strings.Contains(said, "Allow ") {
					t.Fatalf("%s: request %d replays an approval question as the agent's own text: %q", step, index, said)
				}
			}
		}
	}

	s.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "test", APIKey: "test", Timeout: 5 * time.Second}
	s.agentClient = provider.Client()
	app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	setTestConnectors(t, s.store, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", OwnerID: data.Viewer.ID, ToolApproval: connectorToolApprovalAlways})
	activities := func(task domain.AgentTask) []domain.AgentActivity {
		items, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
		return items
	}
	reload := func(task domain.AgentTask) domain.AgentTask {
		task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
		return task
	}
	prompt := func(task domain.AgentTask, body string) domain.AgentTask {
		task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": body}, 200)
		s.deliverApplicationTask(t.Context(), task)
		return reload(task)
	}
	awaitsApproval := func(task domain.AgentTask, step string) {
		t.Helper()
		items := activities(task)
		last := items[len(items)-1]
		options, _ := last.SignalMetadata["options"].([]any)
		if task.Status != "awaitingInput" || task.PendingTool == nil || last.Type != "elicitation" || last.Signal != "select" || len(options) != 2 || last.ToolCall == nil || !strings.Contains(last.Body, "Allow Code search: search_code?") {
			t.Fatalf("%s: a held call must wait as an approval: status=%s pending=%+v last=%+v", step, task.Status, task.PendingTool, last)
		}
	}

	// A held call: elicitation with Approve / Reject, the pending tool set, nothing run yet.
	next(toolCall("c_1", search))
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Where are invoice totals rounded?", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	task, _ := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	s.deliverApplicationTask(t.Context(), task)
	task = reload(task)
	awaitsApproval(task, "first approval")
	if calls.Load() != 0 {
		t.Fatalf("a held call ran before approval (%d calls)", calls.Load())
	}

	// Approve → the call runs and the run completes; the resumed run reads the decision, not the question.
	from := requestCount()
	task = prompt(task, agentApprovalApprove)
	if task.Status != "complete" || calls.Load() != 1 {
		t.Fatalf("approve must run the call and complete: status=%s calls=%d", task.Status, calls.Load())
	}
	if resumed := request(from); !strings.Contains(resumed, "Approved: Code search: search_code") {
		t.Fatalf("the resumed run must read the approval outcome: %s", resumed)
	}
	noApprovalText(from, "first approval resumed")

	// A second approval in the same session: its run and its resume replay neither question as the agent's text.
	from = requestCount()
	next(toolCall("c_2", search))
	task = prompt(task, "Search again")
	awaitsApproval(task, "second approval")
	task = prompt(task, agentApprovalApprove)
	if task.Status != "complete" || calls.Load() != 2 {
		t.Fatalf("the second approval must run: status=%s calls=%d", task.Status, calls.Load())
	}
	noApprovalText(from, "second approval")

	// Reject → nothing runs, the model reads the rejection.
	from = requestCount()
	next(toolCall("c_3", search))
	task = prompt(task, "And once more")
	awaitsApproval(task, "third approval")
	task = prompt(task, agentApprovalReject)
	if task.Status != "complete" || calls.Load() != 2 {
		t.Fatalf("a rejected call must not run: status=%s calls=%d", task.Status, calls.Load())
	}
	if rejected := request(requestCount() - 1); !strings.Contains(rejected, "Rejected: Code search: search_code") || !strings.Contains(rejected, "The user rejected this action") {
		t.Fatalf("the rejection must reach the model: %s", rejected)
	}
	noApprovalText(from, "rejection")

	// An unknown tool name fails as a tool result; the model recovers with the real tool, which waits for approval.
	from = requestCount()
	next(toolCall("c_4", "external_00000000000000000000000000000000"), toolCall("c_5", search))
	task = prompt(task, "Find the rounding helper")
	awaitsApproval(task, "after an unknown tool")
	if recovered := request(from + 1); !strings.Contains(recovered, `"tool_call_id":"c_4"`) {
		t.Fatalf("the unknown tool's failure must reach the model: %s", recovered)
	}
	task = prompt(task, agentApprovalApprove)
	if task.Status != "complete" || calls.Load() != 3 {
		t.Fatalf("the recovered call must run once approved: status=%s calls=%d", task.Status, calls.Load())
	}

	// Approval asked in text is retried once with a nudge; the real call then waits for approval as usual.
	from = requestCount()
	next(text("I need to search the code.\n\nAllow Code search: search_code, search_code?"), toolCall("c_6", search))
	task = prompt(task, "Look at invoice.go")
	awaitsApproval(task, "corrective retry")
	if retried := request(from + 1); !strings.Contains(retried, agentTextApprovalNudge) {
		t.Fatalf("the retry must tell the model to call the tool: %s", retried)
	}
	for _, activity := range activities(task) {
		if activity.Type == "response" && strings.Contains(activity.Body, "Allow ") {
			t.Fatalf("approval text must not be saved as an answer: %+v", activity)
		}
	}
	task = prompt(task, agentApprovalApprove)
	if task.Status != "complete" || calls.Load() != 4 {
		t.Fatalf("the retried call must run once approved: status=%s calls=%d", task.Status, calls.Load())
	}

	// Approval asked in text twice: the session fails with an accurate error, nothing pending, nothing run.
	next(text("Allow Code search: search_code?"), text("**Allow search_code?**"))
	before := len(activities(task))
	task = prompt(task, "Check it again")
	items := activities(task)
	last := items[len(items)-1]
	if task.Status != "error" || task.PendingTool != nil || last.Type != "error" || last.Body != agentTextApprovalError || last.Signal != "" || calls.Load() != 4 {
		t.Fatalf("approval text twice must fail the session: status=%s pending=%+v last=%+v calls=%d", task.Status, task.PendingTool, last, calls.Load())
	}
	for _, activity := range items[before:] {
		if activity.Type == "response" || activity.Type == "elicitation" {
			t.Fatalf("a failed approval-text run must not leave an answer or a question: %+v", activity)
		}
	}

	// Only the delegator (or an admin) answers an approval; a member who did not delegate is refused.
	next(toolCall("c_7", search))
	task = prompt(task, "Try one last time")
	awaitsApproval(task, "requester permissions")
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	// The fixture's second user is an admin; as a plain member they did not delegate and may not answer.
	if err := s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(newHandler(&server{store: s.store, uploadPath: t.TempDir()}))
	defer host.Close()
	member := authClient(t)
	authRequest[domain.AuthSession](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", http.StatusOK)
	raw, _ := json.Marshal(map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": agentApprovalApprove})
	answer, err := http.NewRequestWithContext(context.Background(), "POST", host.URL+"/api/agent-tasks/"+task.ID+"/activities", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	answer.Header.Set("Content-Type", "application/json")
	answer.Header.Set("X-Workspace-Key", data.Workspace.URLKey)
	response, err := member.Do(answer)
	if err != nil {
		t.Fatal(err)
	}
	refused, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode < 400 || reload(task).PendingTool == nil {
		t.Fatalf("a member who did not delegate must not answer the approval: %d %s", response.StatusCode, refused)
	}
	task = prompt(reload(task), agentApprovalApprove)
	if task.Status != "complete" || calls.Load() != 5 {
		t.Fatalf("the delegator's approval must run the call: status=%s calls=%d", task.Status, calls.Load())
	}
}

// The approval-text detector is narrow: only Flow's "Allow <names>?" phrasing naming a tool offered in the turn.
func TestAgentTextApprovalRequest(t *testing.T) {
	s := &server{}
	s.agent.ToolsEnabled, s.agent.WriteTools = true, true
	ctx := context.WithValue(t.Context(), connectorToolsKey{}, []connectorTool{{Definition: agentProviderTool{Name: "external_abc"}, Policy: applicationPolicy{Name: "Sourcebot"}, RemoteName: "grep"}})
	for text, want := range map[string]bool{
		"Allow Sourcebot: read_file, read_file, grep, grep?": true,
		"Allow Sourcebot: grep?":                             true,
		"I'll look first.\n\n**Allow grep?**":                true,
		"allow save_issue?":                                  true,
		"允许 Sourcebot: read_file, read_file, grep, grep 吗？":  true,
		"允许 Sourcebot：grep 吗？":                               true,
		"我先搜索一下。\n\n**是否允许 grep？**":                          true,
		"允许 grep、read_file?":                                 true,
		"允许我解释一下吗？":                                          false,
		"我用 grep 搜过了，允许我再试一次吗？":                              false,
		"允许 grep 运行。":                                        false,
		"Allow me to explain?":                               false,
		"Should I allow grep?":                               false,
		"Allow grep to run.":                                 false,
		"I searched with grep. Allow more time?":             false,
		"":                                                   false,
	} {
		if got := s.agentTextApprovalRequest(ctx, agentProviderTurn{Text: text}); got != want {
			t.Errorf("%q: got %v, want %v", text, got, want)
		}
	}
	if s.agentTextApprovalRequest(ctx, agentProviderTurn{Text: "Allow grep?", ToolCalls: []domain.AgentToolCall{{ID: "c", Name: "external_abc"}}}) {
		t.Fatal("a turn with tool calls is not an approval written as text")
	}
}

// Chat retries a reply that asks for approval in text and keeps that text out of the answer.
func TestAgentSessionTextApprovalRetry(t *testing.T) {
	var providerCalls, connectorCalls, toolCount atomic.Int32
	toolCount.Store(1)
	upstream := fakeCodeMCP(t, &connectorCalls, &toolCount)
	defer upstream.Close()
	var mu sync.Mutex
	var requests []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		switch providerCalls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Allow Code search: search_code?\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_search\",\"function\":{\"name\":\""+connectorToolName("code", "search_code")+"\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		default:
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Found it.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer provider.Close()
	s, handler, _, _ := applicationFixture(t)
	setTestConnectors(t, s.store, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true})
	s.agent = appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true, Timeout: 5 * time.Second}
	s.agentClient = provider.Client()
	host := httptest.NewServer(handler)
	defer host.Close()
	defer s.stopDeliveryScheduler(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, host.URL+"/api/agent/sessions/stream", strings.NewReader(`{"message":"Where are totals rounded?","location":"page"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if providerCalls.Load() != 3 || connectorCalls.Load() != 1 {
		t.Fatalf("the retry must call the tool: provider=%d connector=%d: %s", providerCalls.Load(), connectorCalls.Load(), body)
	}
	mu.Lock()
	retried := requests[1]
	mu.Unlock()
	if !strings.Contains(retried, agentTextApprovalNudge) {
		t.Fatalf("the retry must carry the nudge: %s", retried)
	}
	sessions := s.store.Bootstrap().AgentSessions
	if len(sessions) == 0 {
		t.Fatal("no session saved")
	}
	saved := sessions[len(sessions)-1].Messages
	if answer := saved[len(saved)-1]; answer.Content != "Found it." {
		t.Fatalf("the approval text must not stay in the answer: %q", answer.Content)
	}
}

// scriptedChineseProvider serves a scripted Chat Completions stream per request (unscripted requests get a plain answer).
type scriptedChineseProvider struct {
	mu       sync.Mutex
	requests []string
	script   map[int]string
}

func (p *scriptedChineseProvider) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.requests = append(p.requests, string(raw))
	reply, ok := p.script[len(p.requests)-1]
	p.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	if !ok {
		reply = chineseSSEText("已在 src/billing/invoice.go:42 找到。")
	}
	_, _ = io.WriteString(w, reply)
}

func (p *scriptedChineseProvider) next(replies ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for index, reply := range replies {
		p.script[len(p.requests)+index] = reply
	}
}

func (p *scriptedChineseProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *scriptedChineseProvider) request(index int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[index]
}

func chineseSSEText(body string) string {
	raw, _ := json.Marshal(body)
	return "data: {\"choices\":[{\"delta\":{\"content\":" + string(raw) + "},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
}

func chineseSSEToolCall(id, name string) string {
	return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"" + id + "\",\"function\":{\"name\":\"" + name + "\",\"arguments\":\"{\\\"query\\\":\\\"roundTotal\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
}

// A Chinese approval question written as text goes through the real delegated turn flow: one bounded corrective retry,
// then the real tool call waits for approval (connector toolApproval "always") and does not run before it; two such
// texts in a row fail the session accurately with nothing run, no answer, no approval question and no pending tool.
func TestBuiltinConnectorChineseApprovalTextFlow(t *testing.T) {
	s, handler, data, _ := applicationFixture(t)
	var calls, toolCount atomic.Int32
	toolCount.Store(1)
	upstream := fakeCodeMCP(t, &calls, &toolCount)
	defer upstream.Close()
	p := &scriptedChineseProvider{script: map[int]string{}}
	provider := httptest.NewServer(http.HandlerFunc(p.handler))
	defer provider.Close()
	search := connectorToolName("code", "search_code")
	s.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "test", APIKey: "test", Timeout: 5 * time.Second}
	s.agentClient = provider.Client()
	app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	setTestConnectors(t, s.store, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", OwnerID: data.Viewer.ID, ToolApproval: connectorToolApprovalAlways})
	activities := func(task domain.AgentTask) []domain.AgentActivity {
		items, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
		return items
	}
	reload := func(task domain.AgentTask) domain.AgentTask {
		task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
		return task
	}
	prompt := func(task domain.AgentTask, body string) domain.AgentTask {
		task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": body}, 200)
		s.deliverApplicationTask(t.Context(), task)
		return reload(task)
	}
	awaitsApproval := func(task domain.AgentTask, step string) {
		t.Helper()
		items := activities(task)
		last := items[len(items)-1]
		options, _ := last.SignalMetadata["options"].([]any)
		if task.Status != "awaitingInput" || task.PendingTool == nil || last.Type != "elicitation" || last.Signal != "select" || len(options) != 2 || last.ToolCall == nil || !strings.Contains(last.Body, "Allow Code search: search_code?") {
			t.Fatalf("%s: a held call must wait as an approval: status=%s pending=%+v last=%+v", step, task.Status, task.PendingTool, last)
		}
	}
	noChineseAnswer := func(task domain.AgentTask, step string) {
		t.Helper()
		for _, activity := range activities(task) {
			if (activity.Type == "response" || activity.Type == "elicitation") && strings.Contains(activity.Body, "允许") {
				t.Fatalf("%s: Chinese approval text must not be saved as an answer or approval: %+v", step, activity)
			}
		}
	}

	// 1+2: Chinese approval text, exactly one retry (2 provider requests), then a real call that waits for approval.
	p.next(chineseSSEText("我需要搜索代码。\n\n允许 Code search: search_code, search_code 吗？"), chineseSSEToolCall("c_1", search))
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "发票金额在哪里取整？", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	task, _ := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	s.deliverApplicationTask(t.Context(), task)
	task = reload(task)
	awaitsApproval(task, "chinese corrective retry")
	if p.count() != 2 || !strings.Contains(p.request(1), agentTextApprovalNudge) || strings.Contains(p.request(0), agentTextApprovalNudge) {
		t.Fatalf("exactly one corrective retry carrying the nudge expected: requests=%d", p.count())
	}
	if calls.Load() != 0 {
		t.Fatalf("the tool ran before approval (%d calls)", calls.Load())
	}
	noChineseAnswer(task, "after retry")
	task = prompt(task, agentApprovalApprove)
	if task.Status != "complete" || calls.Load() != 1 || task.PendingTool != nil {
		t.Fatalf("approve must run the call once and complete: status=%s calls=%d pending=%+v", task.Status, calls.Load(), task.PendingTool)
	}
	if p.count() != 3 || !strings.Contains(p.request(2), "Approved: Code search: search_code") {
		t.Fatalf("the resumed run must read the approval outcome: requests=%d", p.count())
	}

	// 3: two consecutive Chinese approval texts fail accurately; nothing runs and nothing is left to approve.
	from := p.count()
	p.next(chineseSSEText("允许 Code search: search_code 吗？"), chineseSSEText("**是否允许 search_code？**"))
	before := len(activities(task))
	task = prompt(task, "再检查一次")
	items := activities(task)
	last := items[len(items)-1]
	if task.Status != "error" || task.PendingTool != nil || last.Type != "error" || last.Body != agentTextApprovalError || last.Signal != "" || last.ToolCall != nil || calls.Load() != 1 {
		t.Fatalf("two Chinese approval texts must fail the session: status=%s pending=%+v last=%+v calls=%d", task.Status, task.PendingTool, last, calls.Load())
	}
	if p.count() != from+2 {
		t.Fatalf("the failure must happen after exactly one retry: %d requests", p.count()-from)
	}
	for _, activity := range items[before:] {
		if activity.Type == "response" || activity.Type == "elicitation" || activity.Signal != "" {
			t.Fatalf("a failed run must not leave an answer, question or approve option: %+v", activity)
		}
	}

	// 4: regressions after the failure. A plain Chinese answer needs no retry; a wrong tool name recovers with the real
	// tool, which waits for approval; a later approval in the same session still works.
	from = p.count()
	task = prompt(task, "总结一下")
	if task.Status != "complete" || p.count() != from+1 || calls.Load() != 1 {
		t.Fatalf("a plain Chinese answer must complete in one request: status=%s requests=%d calls=%d", task.Status, p.count()-from, calls.Load())
	}
	from = p.count()
	p.next(chineseSSEToolCall("c_2", "external_00000000000000000000000000000000"), chineseSSEToolCall("c_3", search))
	task = prompt(task, "找到取整函数")
	awaitsApproval(task, "after an unknown tool")
	if !strings.Contains(p.request(from+1), `"tool_call_id":"c_2"`) || calls.Load() != 1 {
		t.Fatalf("the unknown tool's failure must reach the model without running anything: calls=%d", calls.Load())
	}
	task = prompt(task, agentApprovalApprove)
	if task.Status != "complete" || calls.Load() != 2 {
		t.Fatalf("the recovered call must run once approved: status=%s calls=%d", task.Status, calls.Load())
	}
	noChineseAnswer(task, "end")
}

// The chat session path has the same guard: a Chinese approval text is retried once with the nudge and kept out of the
// answer; two in a row fail the reply with the accurate error, run no tool and save no approval question.
func TestAgentSessionChineseApprovalTextFlow(t *testing.T) {
	var connectorCalls, toolCount atomic.Int32
	toolCount.Store(1)
	upstream := fakeCodeMCP(t, &connectorCalls, &toolCount)
	defer upstream.Close()
	p := &scriptedChineseProvider{script: map[int]string{}}
	provider := httptest.NewServer(http.HandlerFunc(p.handler))
	defer provider.Close()
	s, handler, _, _ := applicationFixture(t)
	setTestConnectors(t, s.store, applicationPolicy{ID: "code", Kind: "mcp", Name: "Code search", URL: upstream.URL, Status: "approved", Shared: true})
	s.agent = appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true, Timeout: 5 * time.Second}
	s.agentClient = provider.Client()
	host := httptest.NewServer(handler)
	defer host.Close()
	defer s.stopDeliveryScheduler(context.Background())
	search := connectorToolName("code", "search_code")
	stream := func(message string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, host.URL+"/api/agent/sessions/stream", strings.NewReader(`{"message":"`+message+`","location":"page"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := host.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return string(body)
	}
	lastMessage := func() domain.AgentMessage {
		t.Helper()
		sessions := s.store.Bootstrap().AgentSessions
		if len(sessions) == 0 {
			t.Fatal("no session saved")
		}
		saved := sessions[len(sessions)-1].Messages
		return saved[len(saved)-1]
	}

	// Retry: Chinese approval text, then the real tool call (runs by default in chat), then the answer.
	p.next(chineseSSEText("允许 Code search: search_code 吗？"), chineseSSEToolCall("call_1", search), chineseSSEText("已找到。"))
	body := stream("发票金额在哪里取整？")
	if p.count() != 3 || connectorCalls.Load() != 1 || !strings.Contains(p.request(1), agentTextApprovalNudge) {
		t.Fatalf("one retry then the real call expected: requests=%d connector=%d: %s", p.count(), connectorCalls.Load(), body)
	}
	if answer := lastMessage(); answer.Content != "已找到。" || strings.Contains(answer.Content, "允许") {
		t.Fatalf("the approval text must not stay in the answer: %q", answer.Content)
	}

	// Failure: two Chinese approval texts in a row.
	from := p.count()
	p.next(chineseSSEText("允许 Code search: search_code 吗？"), chineseSSEText("**是否允许 search_code？**"))
	body = stream("再查一次")
	if p.count() != from+2 || connectorCalls.Load() != 1 {
		t.Fatalf("exactly one retry and no tool run expected: requests=%d connector=%d", p.count()-from, connectorCalls.Load())
	}
	failed := lastMessage()
	hasError := false
	for _, part := range failed.Parts {
		if part.Type == "error" && part.Text == agentTextApprovalError {
			hasError = true
		}
		if part.Type == "tool" || strings.Contains(part.Text, "允许") {
			t.Fatalf("a failed reply must keep no tool row or approval text: %+v", part)
		}
	}
	if !hasError || strings.Contains(failed.Content, "允许") || !strings.Contains(body, agentTextApprovalError) {
		t.Fatalf("the failure must be accurate and leave no approval text: content=%q parts=%+v body=%s", failed.Content, failed.Parts, body)
	}
}

// Parallel calls streamed without an index (all at 0) stay separate calls.
func TestChatCompletionsUnindexedParallelToolCalls(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_a\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"\\\"a.go\\\"}\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_b\",\"function\":{\"name\":\"grep\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	s := &server{agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "test", Timeout: 5 * time.Second}, agentClient: provider.Client()}
	turn, err := s.requestChatCompletions(t.Context(), []agentProviderMessage{{Role: "user", Content: "hi"}}, nil, func(agentProviderEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.ToolCalls) != 2 || turn.ToolCalls[0].ID != "call_a" || turn.ToolCalls[0].Name != "read_file" || string(turn.ToolCalls[0].Arguments) != `{"path":"a.go"}` || turn.ToolCalls[1].ID != "call_b" || turn.ToolCalls[1].Name != "grep" {
		t.Fatalf("unindexed parallel calls: %+v", turn.ToolCalls)
	}
}
