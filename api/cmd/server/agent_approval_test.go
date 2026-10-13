package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

func agentTestCall(id, name string, args any) domain.AgentToolCall {
	raw, _ := json.Marshal(args)
	return domain.AgentToolCall{ID: id, Name: name, Arguments: raw}
}

func handlerStatus(handler http.Handler, method, path string) int {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder.Code
}

// The reference app's two exceptions to "writes apply immediately": deleting more than one item, and publishing an update the
// agent wrote itself. Everything else runs without asking.
func TestAgentApprovalPolicy(t *testing.T) {
	srv, handler := newRuntimeTestServer(t, &responsesScript{})
	data := srv.store.Bootstrap()
	first := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – one", "teamId": data.Teams[0].ID}, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – two", "teamId": data.Teams[0].ID}, http.StatusCreated)
	requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "QA fix – Gone customer"}, http.StatusCreated)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	data = srv.workspaceData(request)
	update := agentTestCall("u", "save_status_update", map[string]any{"type": "project", "project": "Missing", "body": "We shipped the export.", "health": "onTrack"})
	for _, test := range []struct {
		name   string
		calls  []domain.AgentToolCall
		latest string
		want   string
	}{
		{"single delete", []domain.AgentToolCall{agentTestCall("a", "delete_issue", map[string]any{"id": first.Identifier})}, "Delete " + first.Identifier, ""},
		{"two deletes", []domain.AgentToolCall{agentTestCall("a", "delete_issue", map[string]any{"id": first.Identifier}), agentTestCall("b", "mcp__flow.delete_issue", map[string]any{"id": second.ID})}, "Delete the QA fix issues", "delete"},
		{"two deletes after the user approved", []domain.AgentToolCall{agentTestCall("a", "delete_issue", map[string]any{"id": first.Identifier}), agentTestCall("b", "delete_issue", map[string]any{"id": second.ID})}, "批准", ""},
		{"two permanent deletes", []domain.AgentToolCall{agentTestCall("a", "delete_customer", map[string]any{"id": "QA fix – Gone customer"}), agentTestCall("b", "delete_issue", map[string]any{"id": first.Identifier})}, "Delete them", "delete"},
		{"one call naming two items", []domain.AgentToolCall{agentTestCall("a", "delete_label", map[string]any{"ids": []string{"Bug", "Feature"}})}, "Remove both labels", "delete"},
		{"normal writes", []domain.AgentToolCall{agentTestCall("a", "save_issue", map[string]any{"title": "x"}), agentTestCall("b", "save_issue", map[string]any{"id": first.Identifier, "priority": 1})}, "Do it", ""},
		{"agent-authored update", []domain.AgentToolCall{update}, "Write a project update for Export revamp", "publish"},
		{"dictated update", []domain.AgentToolCall{update}, "Post this update to Export revamp: We shipped the export.", ""},
		{"dictated update (zh)", []domain.AgentToolCall{update}, "发布这条更新：我们完成了导出。", ""},
		{"editing an update", []domain.AgentToolCall{agentTestCall("u", "save_status_update", map[string]any{"type": "project", "id": "update_1", "body": "Edited"})}, "Fix the typo", ""},
	} {
		approval := srv.agentApprovalForTurn(request, data, test.calls, test.latest)
		got := ""
		if approval != nil {
			got = approval.Kind
		}
		if got != test.want {
			t.Errorf("%s: approval = %q, want %q", test.name, got, test.want)
		}
		if test.name == "two deletes" && approval != nil {
			if len(approval.Targets) != 2 || approval.Targets[0].Label != first.Identifier || approval.Targets[0].Title != first.Title || approval.Targets[1].Label != second.Identifier || len(approval.Calls) != 2 {
				t.Errorf("delete approval = %+v", approval)
			}
			if preview := agentApprovalPreview(*approval); !strings.Contains(preview, "Delete these 2 items") || !strings.Contains(preview, first.Identifier) {
				t.Errorf("preview = %q", preview)
			}
		}
		if test.name == "two permanent deletes" && approval != nil {
			// A customer has no trash; an issue does.
			if !approval.Targets[0].Permanent || approval.Targets[1].Permanent || !strings.Contains(agentApprovalPreview(*approval), "can't be restored") {
				t.Errorf("permanent delete approval = %+v", approval.Targets)
			}
		}
		if test.name == "agent-authored update" && approval != nil {
			if approval.Body != "We shipped the export." || approval.Health != "onTrack" || !strings.Contains(agentApprovalPreview(*approval), "> We shipped the export.") {
				t.Errorf("publish approval = %+v", approval)
			}
		}
	}
	for reply, want := range map[string]string{"approve": "approve", "Yes!": "approve", "批准": "approve", "好的。": "approve", "reject": "reject", "no": "reject", "only delete the first": "reject"} {
		if got := agentApprovalAnswer(reply); got != want {
			t.Errorf("answer %q = %s, want %s", reply, got, want)
		}
	}
}

// In chat, a bulk delete stops on one approval listing every target; approving runs exactly the held calls.
func TestAgentChatBulkDeleteWaitsForApproval(t *testing.T) {
	provider := &responsesScript{}
	srv, handler := newRuntimeTestServer(t, provider)
	data := srv.store.Bootstrap()
	first := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – one", "teamId": data.Teams[0].ID}, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – two", "teamId": data.Teams[0].ID}, http.StatusCreated)
	provider.turns = []func(http.ResponseWriter, *http.Request){
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, responsesCall("del_1", "delete_issue", map[string]any{"id": first.Identifier})+responsesCall("del_2", "delete_issue", map[string]any{"id": second.Identifier})+responsesCompleted)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, responsesText("Deleted both; they are in the trash."))
		},
	}
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"QA fix – delete the two QA fix issues","location":"page"}`)
	if len(provider.bodies) != 1 {
		t.Fatalf("the run must stop on the approval; provider calls = %d", len(provider.bodies))
	}
	if status := handlerStatus(handler, http.MethodGet, "/api/issue-records/"+first.ID); status != http.StatusOK {
		t.Fatalf("nothing may be deleted before approval (status %d)", status)
	}
	session := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)[0]
	question := agentQuestionPart(session.Messages[1])
	if question == nil || question.Question.Approval == nil || question.Question.Approval.Kind != "delete" || len(question.Question.Approval.Targets) != 2 || len(question.Question.Options) != 2 || question.Question.Options[0].Value != agentApprovalApprove {
		t.Fatalf("approval question = %#v", question)
	}
	for _, part := range session.Messages[1].Parts {
		if part.Type == "toolCall" {
			t.Fatalf("held calls must not show as tool rows: %+v", part)
		}
	}
	streamRuntime(t, handler, "/api/agent/sessions/"+session.ID+"/messages/stream", fmt.Sprintf(`{"message":"Approve","answer":{"partId":%q,"value":"approve"}}`, question.ID))
	if len(provider.bodies) != 2 {
		t.Fatalf("approving must resume the run once; provider calls = %d", len(provider.bodies))
	}
	responsesInputPairs(t, provider.body(1))
	for _, issue := range []domain.Issue{first, second} {
		if status := handlerStatus(handler, http.MethodGet, "/api/issue-records/"+issue.ID); status == http.StatusOK {
			t.Fatalf("%s was not deleted after approval", issue.Identifier)
		}
	}
	session = requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	if decision := agentQuestionPart(session.Messages[1]).Question.Approval.Decision; decision != "approved" {
		t.Fatalf("decision = %q", decision)
	}
	steps := 0
	for _, part := range session.Messages[3].Parts {
		if part.ToolCall != nil && strings.HasPrefix(part.ToolCall.ID, "approved_") && part.ToolCall.Status == "completed" {
			steps++
		}
	}
	if steps != 2 {
		t.Fatalf("approved calls must show as steps of the new reply: %+v", session.Messages[3].Parts)
	}
	// A retried answer never runs the held calls again.
	if _, _, _, _, ok := pendingAgentApproval(&session); ok {
		t.Fatal("a handled approval is still pending")
	}
}

// In a comment thread, a bulk delete pauses on one readable select elicitation (no raw JSON), a "批准" reply runs the
// held calls, provider reasoning never becomes a thought, and tool errors reach the model with their real reason.
func TestBuiltinApplicationThreadApprovalsAndErrors(t *testing.T) {
	s, handler, data, _ := applicationFixture(t)
	var mu sync.Mutex
	var requests []string
	script := []string{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(raw))
		index := len(requests) - 1
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if index < len(script) {
			_, _ = io.WriteString(w, script[index])
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	s.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, Model: "test", APIKey: "test", Timeout: 5 * time.Second}
	s.agentClient = provider.Client()
	app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	first := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "QA fix – one", "teamId": data.Teams[0].ID}, 201)
	second := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "QA fix – two", "teamId": data.Teams[0].ID}, 201)
	toolCalls := func(calls ...[3]string) string {
		var deltas []string
		for index, call := range calls {
			arguments, _ := json.Marshal(call[2])
			deltas = append(deltas, fmt.Sprintf(`{"index":%d,"id":%q,"function":{"name":%q,"arguments":%s}}`, index, call[0], call[1], arguments))
		}
		return "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Figuring out which issues\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[" + strings.Join(deltas, ",") + "]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	}
	script = []string{
		toolCalls([3]string{"c_bad", "get_issue", `{"id":"NOPE-999"}`}),
		toolCalls([3]string{"c_1", "delete_issue", `{"id":"` + first.Identifier + ` QA fix – one"}`}, [3]string{"c_2", "delete_issue", `{"id":"` + second.Identifier + `"}`}),
	}
	host := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "QA fix – host", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	task, _ := s.store.AgentTask(t.Context(), data.Workspace.URLKey, host.AgentSessionID)
	s.deliverApplicationTask(t.Context(), task)
	task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	activities, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
	last := activities[len(activities)-1]
	if task.Status != "awaitingInput" || last.Type != "elicitation" || last.Signal != "select" || task.PendingTool == nil || task.PendingTool.Name != agentApprovalBatchTool {
		t.Fatalf("bulk delete must pause on one approval: status=%s last=%+v pending=%+v", task.Status, last, task.PendingTool)
	}
	if strings.Contains(last.Body, "{") || !strings.Contains(last.Body, first.Identifier) || !strings.Contains(last.Body, second.Identifier) || last.SignalMetadata["approval"] == nil {
		t.Fatalf("the approval must be readable and list both issues: %q %v", last.Body, last.SignalMetadata)
	}
	for _, activity := range activities {
		if activity.Type == "thought" && strings.Contains(activity.Body, "Figuring out") {
			t.Fatalf("provider reasoning became a thought: %+v", activity)
		}
	}
	mu.Lock()
	if !strings.Contains(requests[1], "NOPE-999") || !strings.Contains(requests[1], `not found`) || strings.Contains(requests[1], "Tool execution failed") {
		mu.Unlock()
		t.Fatalf("the tool error must reach the model with its reason: %s", requests[1])
	}
	mu.Unlock()
	if status := handlerStatus(handler, http.MethodGet, "/api/issue-records/"+first.ID); status != http.StatusOK {
		t.Fatalf("deleted before approval (status %d)", status)
	}
	task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "批准"}, 200)
	s.deliverApplicationTask(t.Context(), task)
	task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	if task.Status != "complete" || task.PendingTool != nil {
		t.Fatalf("approval was not consumed: %+v", task)
	}
	for _, issue := range []domain.Issue{first, second} {
		if status := handlerStatus(handler, http.MethodGet, "/api/issue-records/"+issue.ID); status == http.StatusOK {
			t.Fatalf("%s was not deleted after approval", issue.Identifier)
		}
	}
	// A follow-up sees the agent's earlier answers, not only the prompts.
	task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "Thanks, what did you delete?"}, 200)
	s.deliverApplicationTask(t.Context(), task)
	mu.Lock()
	defer mu.Unlock()
	followUp := requests[len(requests)-1]
	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.Unmarshal([]byte(followUp), &payload)
	assistantDone := false
	for _, message := range payload.Messages {
		if message["role"] == "assistant" && message["content"] == "Done." {
			assistantDone = true
		}
	}
	if !assistantDone {
		t.Fatalf("the agent's previous response is missing from the follow-up: %s", followUp)
	}
}
