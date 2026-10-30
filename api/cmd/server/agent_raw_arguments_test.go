package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestMCPMalformedArgumentsAreToolErrors(t *testing.T) {
	f := newContractPaths(t)
	for _, arguments := range []string{`[]`, `"{\"limit\":"`, `42`} {
		request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_teams","arguments":`+arguments+`}}`))
		request.Header.Set("Authorization", "Bearer "+f.secret)
		response := httptest.NewRecorder()
		f.mcp(response, request)
		var reply struct {
			Result struct {
				IsError bool `json:"isError"`
			} `json:"result"`
			Error any `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || !reply.Result.IsError || reply.Error != nil {
			t.Fatalf("arguments %s: %s (%v)", arguments, response.Body.String(), err)
		}
	}
	result, ok := mcpToolCallResult(json.RawMessage(`{"cut":`), nil, nil)
	if !ok || result["isError"] != true {
		t.Fatalf("bad result was a protocol failure: %+v", result)
	}
}

func TestChatRecoversFromMalformedJSONArguments(t *testing.T) {
	chat := newFileToolChat(t)
	chat.provider.replies = []string{
		`tool:list_teams`,
		`tool:list_issues {"limit":`,
		`tool:list_teams {}`,
		"Found the team.",
	}
	session := chat.ask(t, "Look at my teams")
	calls := toolCalls(session)
	if len(calls) != 3 || calls[0].Status != "completed" || calls[1].Status != "error" || calls[2].Status != "completed" {
		t.Fatalf("calls = %+v", calls)
	}
	if !strings.Contains(chat.inputs()[2], "expected a complete JSON object") {
		t.Fatal("model never received the malformed-argument error")
	}
	loaded := requestJSON[domain.AgentSession](t, chat.handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	if loaded.Messages[len(loaded.Messages)-1].Content != "Found the team." {
		t.Fatalf("saved session could not be reloaded: %+v", loaded.Messages)
	}
}

func TestLoopRecoversFromMalformedJSONArguments(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	provider.script(`tool:list_teams`, `tool:list_issues {"limit":`, `tool:list_issues {"limit":1}`, `tool:finish_run {"status":"done","summary":"Checked the issues."}`)
	loop, started := startTestRun(t, handler, "Check issues", "Read the issues and reply with a summary only. Do not change anything.", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "completed" || len(run.ToolCalls) != 3 || run.ToolCalls[0].Status != "completed" || run.ToolCalls[1].Status != "error" || run.ToolCalls[2].Status != "completed" {
		t.Fatalf("run = %+v", run)
	}
	if !strings.Contains(provider.inputs[2], "expected a complete JSON object") {
		t.Fatal("model never received the malformed-argument error")
	}
}

func TestLoopBuilderRecoversFromMalformedJSONArguments(t *testing.T) {
	chat := newFileToolChat(t)
	loop := requestJSON[domain.Loop](t, chat.handler, http.MethodPost, "/api/loops", map[string]any{"name": "Draft", "status": "draft"}, http.StatusCreated)
	chat.provider.script(`tool:list_teams`, `tool:save_loop {"id":`, `tool:save_loop {"id":"`+loop.ID+`","name":"Team digest","instructions":"Summarize the completed issues."}`, "The draft is ready for review.")
	stream := streamAgent(t, chat.srv, chat.host, "/api/agent/sessions/stream", map[string]any{"message": "Draft a team digest loop. Do not publish it.", "loopIds": []string{loop.ID}}, nil)
	if !strings.Contains(stream, "session.completed") {
		t.Fatal("builder failed instead of recovering")
	}
	saved := requestJSON[domain.Loop](t, chat.handler, http.MethodGet, "/api/loops/"+loop.ID, nil, http.StatusOK)
	if saved.Name != "Team digest" || saved.Status != "draft" || saved.Instructions == "" {
		t.Fatalf("draft = %+v", saved)
	}
	if !strings.Contains(chat.inputs()[2], "expected a complete JSON object") {
		t.Fatal("builder never received the argument error")
	}
}
