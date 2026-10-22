package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The worker runtime's run guard is Flow's builtinRunGuard behind POST /internal/agent-runtime/runs/{id}/guard. The
// shared vectors in testdata/worker_run_guard are the exact requests the worker sends for a scripted model (the
// agent-worker test drives its loop and checks it sends them, answering with the recorded responses); here Flow
// answers the same requests and must give the recorded responses, and the run must end as the in-process agent's
// would (Coding started or not, in which environment, and the reply).
//
// Placeholders: {{env0}}, {{env1}} are the coding environments of the repositories (in order), {{issue}} the
// delegated issue's identifier. FLOW_UPDATE_WORKER_GUARD_FIXTURES=1 records Flow's responses into the files.

type workerGuardExchange struct {
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

type workerGuardFixture struct {
	Name         string `json:"name"`
	Repositories []string
	Issue        struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"issue"`
	MaxTurns  int               `json:"maxTurns"`
	Connector string            `json:"connector"`
	Model     []json.RawMessage `json:"model"`
	Expect    json.RawMessage   `json:"expect"`
	Exchanges []workerGuardExchange
}

type workerGuardExpect struct {
	Coding        bool     `json:"coding"`
	Environment   string   `json:"environment"`
	ReplyContains []string `json:"replyContains"`
	ReplyOmits    []string `json:"replyOmits"`
}

func TestWorkerRunGuardSharedVectors(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "worker_run_guard", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no shared vectors: %v", err)
	}
	update := os.Getenv("FLOW_UPDATE_WORKER_GUARD_FIXTURES") == "1"
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".json"), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var fixture workerGuardFixture
			if err := json.Unmarshal(raw, &fixture); err != nil {
				t.Fatal(err)
			}
			h := newDelegationHarness(t, fixture.Repositories...)
			h.s.agent.WorkerSecret = testWorkerSecret
			placeholders := map[string]string{}
			for index, environment := range h.environments {
				placeholders["{{env"+string(rune('0'+index))+"}}"] = environment.ID
			}
			forward := func(text string) string {
				for placeholder, value := range placeholders {
					text = strings.ReplaceAll(text, placeholder, value)
				}
				return text
			}
			issue := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": fixture.Issue.Title, "description": forward(fixture.Issue.Description), "teamId": h.teamID, "delegateId": h.agentUser}, http.StatusCreated)
			placeholders["{{issue}}"] = issue.Identifier
			backward := func(text string) string {
				for placeholder, value := range placeholders {
					text = strings.ReplaceAll(text, value, placeholder)
				}
				return text
			}
			task, err := h.s.store.AgentTask(t.Context(), h.workspace, issue.AgentSessionID)
			if err != nil {
				t.Fatal(err)
			}
			principal := agentRunPrincipal{Mode: agentRunModeApp, UserID: task.CreatorID, AppUserID: h.agentUser, TeamID: h.teamID}
			run, _ := startWorkerTestRun(t, h.s, h.handler, store.AgentRunInput{WorkspaceKey: h.workspace, SessionID: task.ID, Kind: store.AgentRunKindDelegation}, principal)
			reply := ""
			for index := range fixture.Exchanges {
				exchange := &fixture.Exchanges[index]
				status, body, _ := runtimeDo(t, h.handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", bearer: testWorkerSecret, body: []byte(forward(string(exchange.Request)))})
				if status != http.StatusOK {
					t.Fatalf("exchange %d: status %d: %s", index, status, body)
				}
				got := canonicalGuardJSON(t, backward(string(body)))
				if update {
					exchange.Response = got
				} else if !sameGuardJSON(t, got, exchange.Response) {
					t.Errorf("exchange %d %s:\n got %s\nwant %s", index, exchange.Request, got, exchange.Response)
				}
				var answer workerRunGuardResponse
				_ = json.Unmarshal(body, &answer)
				if answer.Reply != "" {
					reply = answer.Reply
				}
			}
			var request struct {
				Text string `json:"text"`
			}
			if reply == "" && len(fixture.Exchanges) > 0 {
				_ = json.Unmarshal([]byte(forward(string(fixture.Exchanges[len(fixture.Exchanges)-1].Request))), &request)
				reply = request.Text
			}

			var expect workerGuardExpect
			if err := json.Unmarshal([]byte(forward(string(fixture.Expect))), &expect); err != nil {
				t.Fatal(err)
			}
			task, _ = h.s.store.AgentTask(t.Context(), h.workspace, task.ID)
			if (task.CodingSessionID != "") != expect.Coding {
				t.Fatalf("Coding started = %v, want %v", task.CodingSessionID != "", expect.Coding)
			}
			if expect.Environment != "" {
				session, err := h.s.store.CodingSession(t.Context(), h.workspace, task.CodingSessionID)
				if err != nil || session.EnvironmentID != expect.Environment {
					t.Fatalf("the session runs in %q, want %q (%v)", session.EnvironmentID, expect.Environment, err)
				}
			}
			for _, part := range expect.ReplyContains {
				if !strings.Contains(reply, part) {
					t.Errorf("the reply %q does not contain %q", reply, part)
				}
			}
			for _, part := range expect.ReplyOmits {
				if strings.Contains(reply, part) {
					t.Errorf("the reply %q contains %q", reply, part)
				}
			}
			// The run had its last word through the guard: the completion check does not start or report anything again.
			before, _ := h.s.store.AgentActivities(t.Context(), h.workspace, task.ID, "")
			h.s.ensureWorkerRunCoding(httptestRequest(t), run)
			after, _ := h.s.store.AgentActivities(t.Context(), h.workspace, task.ID, "")
			if len(after) != len(before) {
				t.Fatalf("the completion check acted again: %d → %d activities", len(before), len(after))
			}
			if update {
				writeWorkerGuardFixture(t, file, raw, fixture)
			}
		})
	}
}

// A run without a session (chat) has no guard; an unknown operation is refused.
func TestWorkerRunGuardOffForChatRuns(t *testing.T) {
	f := newAgentAttributionFixture(t)
	f.s.agent.WorkerSecret = testWorkerSecret
	run, _ := startWorkerTestRun(t, f.s, f.handler, store.AgentRunInput{WorkspaceKey: f.data.Workspace.URLKey, SessionID: "agent_session_chat_guard", Kind: store.AgentRunKindChat}, agentRunPrincipal{Mode: agentRunModeUser, UserID: f.data.Viewer.ID})
	path := "/internal/agent-runtime/runs/" + run.ID + "/guard"
	for _, op := range []string{"start", "turn", "before", "finish"} {
		answer := runtimeJSON[workerRunGuardResponse](t, f.handler, runtimeCall{method: http.MethodPost, path: path, worker: "w1", body: map[string]any{"op": op, "phase": "reply"}}, http.StatusOK)
		if answer.Enabled || answer.Handled || answer.Reply != "" {
			t.Fatalf("%s: a chat run has no guard: %+v", op, answer)
		}
	}
	if status, _, _ := runtimeDo(t, f.handler, runtimeCall{method: http.MethodPost, path: path, worker: "w2", bearer: testWorkerSecret, body: map[string]any{"op": "start"}}); status != http.StatusConflict {
		t.Fatalf("another worker cannot use the run's guard: %d", status)
	}
}

func httptestRequest(t *testing.T) *http.Request {
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://flow.test/internal/agent-runtime/runs/run/complete", nil)
	return request
}

func canonicalGuardJSON(t *testing.T, text string) json.RawMessage {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return json.RawMessage(bytes.TrimSpace(buffer.Bytes()))
}

func sameGuardJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// writeWorkerGuardFixture rewrites a vector with the recorded responses, one exchange per line.
func writeWorkerGuardFixture(t *testing.T, file string, original []byte, fixture workerGuardFixture) {
	t.Helper()
	compact := func(raw json.RawMessage) string {
		var buffer bytes.Buffer
		if err := json.Compact(&buffer, raw); err != nil {
			t.Fatal(err)
		}
		return buffer.String()
	}
	var out strings.Builder
	head := strings.Index(string(original), `"exchanges"`)
	if head < 0 {
		t.Fatalf("%s has no exchanges", file)
	}
	out.WriteString(string(original[:head]))
	out.WriteString("\"exchanges\": [\n")
	for index, exchange := range fixture.Exchanges {
		out.WriteString(`    {"request": ` + compact(exchange.Request) + `, "response": ` + compact(exchange.Response) + "}")
		if index < len(fixture.Exchanges)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("  ]\n}\n")
	if err := os.WriteFile(file, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
