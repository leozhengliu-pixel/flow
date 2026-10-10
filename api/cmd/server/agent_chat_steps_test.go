package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func phaseCall(name, args, result string) domain.AgentToolCall {
	call := domain.AgentToolCall{ID: name + args, Name: name, Arguments: json.RawMessage(args)}
	if result != "" {
		call.Result = json.RawMessage(result)
	}
	return call
}

func TestAgentPhaseTitleNamesTheWorkFromToolCalls(t *testing.T) {
	cases := []struct {
		calls []domain.AgentToolCall
		want  string
	}{
		// Like Linear, a single tool call needs no title above its own row.
		{[]domain.AgentToolCall{phaseCall("get_issue", `{"id":"DEV-16"}`, "")}, ""},
		{[]domain.AgentToolCall{phaseCall("report_progress", `{"title":"x"}`, ""), phaseCall("get_issue", `{"id":"DEV-16"}`, "")}, ""},
		{[]domain.AgentToolCall{phaseCall("get_issue", `{"id":"DEV-16"}`, ""), phaseCall("get_issue", `{"id":"DEV-24"}`, "")}, "Looking up DEV-16, DEV-24"},
		// Results name what arguments only reference by internal id.
		{[]domain.AgentToolCall{phaseCall("get_issue", `{"id":"issue_16"}`, `{"identifier":"DEV-16"}`), phaseCall("list_comments", `{"issueId":"issue_16"}`, "")}, "Looking up DEV-16"},
		{[]domain.AgentToolCall{phaseCall("search_issues", `{"queries":["导出","export"]}`, ""), phaseCall("get_issue", `{"id":"DEV-16"}`, "")}, `Searching issues for "导出", "export"`},
		{[]domain.AgentToolCall{phaseCall("save_issue", `{"id":"DEV-16","priority":2}`, ""), phaseCall("save_comment", `{"issueId":"DEV-16","body":"x"}`, "")}, "Updating priority of DEV-16"},
		{[]domain.AgentToolCall{phaseCall("save_issue", `{"id":"DEV-16","assignee":"me"}`, ""), phaseCall("save_issue", `{"id":"DEV-24","assignee":"me"}`, "")}, "Assigning DEV-16, DEV-24"},
		{[]domain.AgentToolCall{phaseCall("save_issue", `{"title":"Fix login","team":"DEV"}`, ""), phaseCall("list_teams", `{}`, "")}, "Creating issue Fix login"},
		{[]domain.AgentToolCall{phaseCall("list_projects", `{}`, ""), phaseCall("list_users", `{}`, ""), phaseCall("list_projects", `{"state":"started"}`, "")}, "Looking at projects, users"},
		{[]domain.AgentToolCall{phaseCall("list_comments", `{"issueId":"DEV-3"}`, ""), phaseCall("list_issue_history", `{"id":"DEV-3"}`, "")}, "Reading comments on DEV-3"},
		{[]domain.AgentToolCall{phaseCall("get_issue", `{"id":"A-1"}`, ""), phaseCall("get_issue", `{"id":"A-2"}`, ""), phaseCall("get_issue", `{"id":"A-3"}`, ""), phaseCall("get_issue", `{"id":"A-4"}`, "")}, "Looking up A-1, A-2, A-3, …"},
	}
	for _, test := range cases {
		if got := agentPhaseTitle(test.calls); got != test.want {
			t.Errorf("agentPhaseTitle(%s) = %q, want %q", test.calls[0].Name, got, test.want)
		}
	}
	// Every Flow tool gets a phrase built from a known template, never a raw internal id.
	var inventory []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, item := range inventory {
		for _, args := range []string{`{}`, `{"id":"issue_123"}`, `{"id":"DEV-1","title":"T"}`} {
			template, subject := agentCallPhrase(phaseCall(item.Name, args, ""))
			if !slices.Contains(agentPhaseTemplates, template) || subject == "" || strings.Contains(subject, "issue_123") {
				t.Errorf("%s %s -> %q %q", item.Name, args, template, subject)
			}
		}
	}
}

// The web translates phase titles by template; its list must match the server's.
func TestAgentPhaseTemplatesFixtureCurrent(t *testing.T) {
	path := filepath.Join(webSourceDir, "components", "agent", "agent-phase-titles.json")
	if os.Getenv("FLOW_UPDATE_FIXTURES") == "1" {
		raw, _ := json.MarshalIndent(agentPhaseTemplates, "", "  ")
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, agentPhaseTemplates) {
		t.Fatalf("%s is stale; run FLOW_UPDATE_FIXTURES=1 go test -run TestAgentPhaseTemplatesFixtureCurrent", path)
	}
}

// A turn with several tools streams a phase title placed above its tool rows and saves it there, without any
// extra model call.
func TestAgentChatStreamsPhaseTitlesAboveTheirTools(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "text/event-stream")
		if payload["instructions"] == agentTitleSystemPrompt {
			_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			return
		}
		if calls.Add(1) == 1 {
			item := func(id, name, args string) string {
				return "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"" + id + "\",\"call_id\":\"" + id + "\",\"type\":\"function_call\",\"name\":\"" + name + "\",\"arguments\":\"\"}}\n\n" +
					"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"" + id + "\",\"arguments\":" + args + "}\n\n"
			}
			_, _ = fmt.Fprint(w, item("call_a", "search_issues", `"{\"queries\":[\"login\"]}"`)+item("call_b", "get_issue", `"{\"id\":\"TST-1\"}"`)+
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Done.\"}\n\n"+
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true}, agentClient: provider.Client()})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/sessions/stream", strings.NewReader(`{"message":"login bugs?","location":"page"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("status=%d provider calls=%d (a phase title must not cost a model call) body=%s", recorder.Code, calls.Load(), body)
	}
	if !strings.Contains(body, `"title":"Searching issues for \"login\""`) || !strings.Contains(body, `"beforePartId":"`) {
		t.Fatalf("phase title not streamed ahead of its tool rows: %s", body)
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	parts := sessions[0].Messages[1].Parts
	kinds := []string{}
	for _, part := range parts {
		kinds = append(kinds, part.Type)
	}
	if len(parts) < 4 || parts[0].Type != "step" || parts[0].Title != `Searching issues for "login"` || parts[1].Type != "toolCall" || parts[2].Type != "toolCall" {
		t.Fatalf("saved parts = %v %#v", kinds, parts[0])
	}
}
