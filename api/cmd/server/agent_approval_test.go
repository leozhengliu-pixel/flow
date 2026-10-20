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

// Approvals saved by earlier versions (bulk deletes, an update the agent wrote) still read as a preview, and replies
// to an approval are read as approve or reject.
func TestAgentApprovalPreviewAndAnswers(t *testing.T) {
	deletes := domain.AgentApproval{Kind: "delete", Targets: []domain.AgentApprovalTarget{{Kind: "customer", Label: "Gone customer", Permanent: true}, {Kind: "issue", Label: "TST-1", Title: "One", Href: "/test-workspace/issue/TST-1"}}}
	if preview := agentApprovalPreview(deletes); !strings.Contains(preview, "Delete these 2 items") || !strings.Contains(preview, "can't be restored") || !strings.Contains(preview, "[TST-1 One](/test-workspace/issue/TST-1)") {
		t.Errorf("delete preview = %q", preview)
	}
	publish := domain.AgentApproval{Kind: "publish", Body: "We shipped the export.", Health: "onTrack", Parent: &domain.AgentApprovalTarget{Label: "Export revamp"}}
	if preview := agentApprovalPreview(publish); !strings.Contains(preview, "Post this update to Export revamp?") || !strings.Contains(preview, "> We shipped the export.") {
		t.Errorf("publish preview = %q", preview)
	}
	for reply, want := range map[string]string{"approve": "approve", "Yes!": "approve", "批准": "approve", "好的。": "approve", "reject": "reject", "no": "reject", "only delete the first": "reject"} {
		if got := agentApprovalAnswer(reply); got != want {
			t.Errorf("answer %q = %s, want %s", reply, got, want)
		}
	}
}

// In chat, deleting several issues and posting an update the agent wrote run at once, without an approval question.
func TestAgentChatBulkDeleteAndUpdateRunWithoutApproval(t *testing.T) {
	provider := &responsesScript{}
	srv, handler := newRuntimeTestServer(t, provider)
	data := srv.store.Bootstrap()
	first := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – one", "teamId": data.Teams[0].ID}, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – two", "teamId": data.Teams[0].ID}, http.StatusCreated)
	provider.turns = []func(http.ResponseWriter, *http.Request){
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, responsesCall("del_1", "delete_issue", map[string]any{"id": first.Identifier})+responsesCall("del_2", "delete_issue", map[string]any{"id": second.Identifier})+
				responsesCall("upd_1", "save_status_update", map[string]any{"type": "project", "project": data.Projects[0].Name, "body": "We shipped the export.", "health": "onTrack"})+responsesCompleted)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, responsesText("Deleted both and posted the update."))
		},
	}
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"QA fix – delete `+first.Identifier+` and `+second.Identifier+`, then write a project update","location":"page"}`)
	if len(provider.bodies) != 2 {
		t.Fatalf("the run must not stop for approval; provider calls = %d", len(provider.bodies))
	}
	responsesInputPairs(t, provider.body(1))
	for _, issue := range []domain.Issue{first, second} {
		if status := handlerStatus(handler, http.MethodGet, "/api/issue-records/"+issue.ID); status == http.StatusOK {
			t.Fatalf("%s was not deleted", issue.Identifier)
		}
	}
	session := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)[0]
	reply := session.Messages[len(session.Messages)-1]
	if question := agentQuestionPart(reply); question != nil {
		t.Fatalf("no approval question may be asked: %+v", question)
	}
	updated := 0
	for _, part := range reply.Parts {
		if part.ToolCall != nil && part.ToolCall.Name == "save_status_update" && part.ToolCall.Status == "completed" {
			updated++
		}
	}
	if updated != 1 {
		t.Fatalf("the update must be posted without asking: %+v", reply.Parts)
	}
}

// A chat approval saved by an earlier version (a bulk delete held for approval) still resolves: approving runs exactly
// the held calls once.
func TestAgentChatSavedApprovalStillResolves(t *testing.T) {
	provider := &responsesScript{}
	srv, handler := newRuntimeTestServer(t, provider)
	data := srv.store.Bootstrap()
	first := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – one", "teamId": data.Teams[0].ID}, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "QA fix – two", "teamId": data.Teams[0].ID}, http.StatusCreated)
	provider.turns = []func(http.ResponseWriter, *http.Request){
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, responsesText("Delete both?"))
		},
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, responsesText("Deleted both; they are in the trash."))
		},
	}
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"QA fix – delete `+first.Identifier+` and `+second.Identifier+`","location":"page"}`)
	session := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)[0]
	// The reply as an earlier version saved it: the held deletes on a pending approval question.
	held := []domain.AgentToolCall{agentTestCall("del_1", "delete_issue", map[string]any{"id": first.ID}), agentTestCall("del_2", "delete_issue", map[string]any{"id": second.ID})}
	approval := &domain.AgentApproval{Kind: "delete", Targets: []domain.AgentApprovalTarget{{Kind: "issue", ID: first.ID, Label: first.Identifier}, {Kind: "issue", ID: second.ID, Label: second.Identifier}}, Calls: held}
	questionID := session.Messages[1].ID + "_approval_0"
	if err := srv.store.MutateWorkspace(t.Context(), data.Workspace.URLKey, "agent.message_completed", session.ID, nil, func(data *domain.Bootstrap) error {
		current, err := ownedAgentSession(data, session.ID)
		if err != nil {
			return err
		}
		reply := &current.Messages[1]
		reply.Parts = append(reply.Parts, domain.AgentMessagePart{ID: questionID, Type: "question", Text: agentApprovalPreview(*approval), Status: "pending", Question: &domain.AgentQuestion{Options: []domain.AgentQuestionOption{{Label: "Approve", Value: agentApprovalApprove}, {Label: "Reject", Value: agentApprovalReject}}, AllowFreeText: true, Approval: approval}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	streamRuntime(t, handler, "/api/agent/sessions/"+session.ID+"/messages/stream", fmt.Sprintf(`{"message":"Approve","answer":{"partId":%q,"value":"approve"}}`, questionID))
	for _, issue := range []domain.Issue{first, second} {
		if status := handlerStatus(handler, http.MethodGet, "/api/issue-records/"+issue.ID); status == http.StatusOK {
			t.Fatalf("%s was not deleted after approval", issue.Identifier)
		}
	}
	session = requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	if decision := agentQuestionPart(session.Messages[1]).Question.Approval.Decision; decision != "approved" {
		t.Fatalf("decision = %q", decision)
	}
	if _, _, _, _, ok := pendingAgentApproval(&session); ok {
		t.Fatal("a handled approval is still pending")
	}
}

// In a comment thread, a bulk delete runs at once, provider reasoning never becomes a thought, tool errors reach the
// model with their real reason, and an approval an earlier version saved still runs its held calls once answered.
func TestBuiltinApplicationThreadDeletesAndErrors(t *testing.T) {
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
	if task.Status != "complete" || task.PendingTool != nil {
		t.Fatalf("a bulk delete must not pause for approval: status=%s pending=%+v", task.Status, task.PendingTool)
	}
	for _, issue := range []domain.Issue{first, second} {
		if status := handlerStatus(handler, http.MethodGet, "/api/issue-records/"+issue.ID); status == http.StatusOK {
			t.Fatalf("%s was not deleted", issue.Identifier)
		}
	}
	activities, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
	for _, activity := range activities {
		if activity.Type == "thought" && strings.Contains(activity.Body, "Figuring out") {
			t.Fatalf("provider reasoning became a thought: %+v", activity)
		}
		if activity.Type == "elicitation" {
			t.Fatalf("no approval may be asked: %+v", activity)
		}
	}
	mu.Lock()
	if !strings.Contains(requests[1], "NOPE-999") || !strings.Contains(requests[1], `not found`) || strings.Contains(requests[1], "Tool execution failed") {
		mu.Unlock()
		t.Fatalf("the tool error must reach the model with its reason: %s", requests[1])
	}
	mu.Unlock()

	// An approval an earlier version saved (held deletes) still runs its calls once the person says "批准".
	third := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "QA fix – three", "teamId": data.Teams[0].ID}, 201)
	fourth := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "QA fix – four", "teamId": data.Teams[0].ID}, 201)
	task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "Delete three and four"}, 200)
	saved := domain.AgentApproval{Kind: "delete", Targets: []domain.AgentApprovalTarget{{Kind: "issue", ID: third.ID, Label: third.Identifier}, {Kind: "issue", ID: fourth.ID, Label: fourth.Identifier}}, Calls: []domain.AgentToolCall{agentTestCall("c_3", "delete_issue", map[string]any{"id": third.ID}), agentTestCall("c_4", "delete_issue", map[string]any{"id": fourth.ID})}}
	pending := agentApprovalBatchCall(saved)
	task, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{ActorID: app.UserID, Type: "elicitation", Body: agentApprovalPreview(saved), Signal: "select", SignalMetadata: map[string]any{"options": []any{map[string]any{"label": "Approve", "value": agentApprovalApprove}, map[string]any{"label": "Reject", "value": agentApprovalReject}}}, ToolCall: &pending})
	if err != nil || task.Status != "awaitingInput" {
		t.Fatalf("saved approval: %v %+v", err, task)
	}
	task = requestJSON[domain.AgentTask](t, handler, "POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "批准"}, 200)
	s.deliverApplicationTask(t.Context(), task)
	task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	if task.Status != "complete" {
		t.Fatalf("the saved approval was not consumed: %+v", task)
	}
	for _, issue := range []domain.Issue{third, fourth} {
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
		if content, _ := message["content"].(string); message["role"] == "assistant" && strings.Contains(content, "Delete these 2 items") {
			t.Fatalf("a saved approval question must not be replayed as the agent's words: %q", content)
		}
	}
	if !assistantDone {
		t.Fatalf("the agent's previous response is missing from the follow-up: %s", followUp)
	}
}
