package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Opt-in live check. scripts/check-worker-paths.sh holds the heavy-command lock for the entire scratch session.
func TestWorkerRuntimeRealPaths(t *testing.T) {
	if os.Getenv("FLOW_WORKER_REAL_TEST") != "1" {
		t.Skip("requires the local model proxy and agent-worker dependencies")
	}
	f := newWorkerChatFixture(t)
	f.s.agent.Protocol, f.s.agent.NativeWebSearch = "openai-responses", true
	var mu sync.Mutex
	var faults []string
	setFaults := func(values ...string) { mu.Lock(); faults = values; mu.Unlock() }
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fault := ""
		if len(faults) > 0 {
			fault, faults = faults[0], faults[1:]
		}
		mu.Unlock()
		if fault != "" {
			w.Header().Set("Content-Type", "text/event-stream")
			delta := map[string]any{"role": "assistant"}
			stop := "stop"
			switch fault {
			case "reasoning":
				delta["reasoning_content"] = "Consider the next action."
			case "truncated":
				stop = "length"
			case "approval":
				delta["content"] = "Allow save_issue?"
			}
			chunk := func(value any, reason any) {
				raw, _ := json.Marshal(map[string]any{"id": "injected", "object": "chat.completion.chunk", "created": 1, "model": "deepseek-v4.1-flash", "choices": []any{map[string]any{"index": 0, "delta": value, "finish_reason": reason}}})
				fmt.Fprintf(w, "data: %s\n\n", raw)
			}
			chunk(delta, nil)
			chunk(map[string]any{}, stop)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, "http://127.0.0.1:8787"+r.URL.Path, r.Body)
		if err != nil {
			http.Error(w, "proxy request failed", 500)
			return
		}
		request.Header = r.Header.Clone()
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			http.Error(w, "local model proxy unavailable", 502)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		buffer := make([]byte, 4096)
		for {
			n, readErr := response.Body.Read(buffer)
			if n > 0 {
				_, _ = w.Write(buffer[:n])
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			if readErr != nil {
				break
			}
		}
	}))
	defer proxy.Close()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(os.Getenv("FLOW_WORKER_REAL_SCRATCH"), "worker.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	command := exec.Command("node", "--import", "tsx", "src/queue-worker.ts")
	command.Dir = filepath.Join(root, "agent-worker")
	command.Env = append(os.Environ(), "FLOW_AGENT_RUNTIME=worker", "FLOW_WORKER_FLOW_URL="+f.api.URL, "FLOW_WORKER_PROTOCOL=openai-chat", "FLOW_WORKER_MODEL=deepseek-v4.1-flash", "FLOW_WORKER_BASE_URL="+proxy.URL+"/v1", "FLOW_WORKER_API_KEY=local-go-proxy", "FLOW_WORKER_SECRET="+testWorkerSecret, "FLOW_WORKER_ID=real-paths", "FLOW_WORKER_SLOTS=1", "FLOW_WORKER_STATE_DIR="+t.TempDir(), "NODE_OPTIONS=--max-old-space-size=384")
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}()
	waitRun := func(loopID, runID string, replyIndex int) (domain.LoopRun, bool) {
		deadline := time.Now().Add(3 * time.Minute)
		streamed := false
		for time.Now().Before(deadline) {
			run, err := f.s.store.LoopRun(t.Context(), "test-workspace", runID)
			if err != nil {
				t.Fatal(err)
			}
			status, output := run.Status, run.Output
			if replyIndex >= 0 {
				status, output = run.Replies[replyIndex].Status, run.Replies[replyIndex].Output
			}
			if status == "running" && output != "" {
				streamed = true
			}
			if status != "running" {
				return run, streamed
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("real loop timed out; see scratch worker.log")
		return domain.LoopRun{}, false
	}
	loop := requestJSON[domain.Loop](t, f.handler, http.MethodPost, "/api/loops", map[string]any{"name": "Worker paths live check", "instructions": "Read the team list. Explain in 180 words what the available teams mean, then call finish_run with a short summary. Only read; do not create or change anything. In finish_run set notDone to [] when the read-only work is done; not modifying data is intentional, not unfinished work.", "status": "published"}, http.StatusCreated)
	started := requestJSON[domain.LoopRun](t, f.handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	run, streamed := waitRun(loop.ID, started.ID, -1)
	if run.Status != "completed" && run.Status != "needs_review" {
		t.Fatalf("loop status=%s reason=%s streamed=%t error=%s", run.Status, run.FailureReason, streamed, run.Error)
	}
	t.Logf("loop completed (text before finish_run: %t)", streamed)
	for index, body := range []string{"What's the status? Give a detailed 180-word explanation.", "Continue: read the team list again and finish with a concise report."} {
		requestJSON[domain.LoopRun](t, f.handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+run.ID+"/replies", map[string]any{"body": body}, http.StatusAccepted)
		record, streamed := waitRun(loop.ID, run.ID, index)
		if record.Replies[index].Status != "completed" || index == 0 && !streamed {
			t.Fatalf("reply %d status=%s streamed=%t error=%s", index, record.Replies[index].Status, streamed, record.Replies[index].Error)
		}
	}
	t.Log("loop status reply and continuation completed on the worker")
	for _, fault := range []string{"reasoning", "truncated", "empty_after_tools"} {
		if fault == "empty_after_tools" {
			setFaults("", "empty")
		} else {
			setFaults(fault)
		}
		started = requestJSON[domain.LoopRun](t, f.handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
		record, _ := waitRun(loop.ID, started.ID, -1)
		records := modelTurnRecords(t, f.handler, record)
		recovered := false
		for _, turn := range records {
			recovered = recovered || turn.Continuation != ""
		}
		if record.Status != "completed" && record.Status != "needs_review" || len(records) < 2 || !recovered {
			t.Fatalf("%s recovery status=%s diagnostics=%+v error=%s", fault, record.Status, records, record.Error)
		}
	}
	t.Log("reasoning-only, token-limited and empty-after-tools turns recovered before real model continuations")
	chat := func(message string) string {
		events := f.stream("/api/agent/sessions/stream", fmt.Sprintf(`{"message":%q,"location":"page"}`, message))
		sessionID := nextEvent(t, events, "session.started").Session.ID
		deadline := time.After(3 * time.Minute)
		for {
			select {
			case event, ok := <-events:
				if !ok {
					reply := f.reply(t, sessionID)
					if reply.Status == "error" || strings.TrimSpace(reply.Content) == "" {
						t.Fatalf("chat failed: %+v", reply)
					}
					return sessionID
				}
				if event.Type == "error" {
					t.Fatalf("real chat error: %+v", event)
				}
			case <-deadline:
				t.Fatal("real chat timed out")
			}
		}
	}
	for _, fault := range []string{"empty", "approval"} {
		setFaults(fault)
		chat("List the available teams. Do not create or change anything.")
	}
	t.Log("chat empty and text-approval retries reached the real model")
	sessionID := chat("Say ready in one word. Do not call tools.")
	seedWorkerLegacyApproval(t, f, sessionID)
	events := f.stream("/api/agent/sessions/"+sessionID+"/messages/stream", `{"message":"Approve the saved call and briefly summarize its result."}`)
	nextEvent(t, events, "session.started")
	deadline := time.After(3 * time.Minute)
	for events != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case <-deadline:
			t.Fatal("saved approval timed out")
		}
	}
	reply := f.reply(t, sessionID)
	found := false
	for _, part := range reply.Parts {
		if part.ToolCall != nil && part.ToolCall.Name == "list_teams" && part.ToolCall.Status == "completed" {
			found = true
		}
	}
	if !found {
		t.Fatal("saved call did not execute on the worker")
	}
	t.Log("saved approval without RunState executed on the worker")
	issue := requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Saved thread approval", "teamId": f.data.Teams[0].ID}, http.StatusCreated)
	_, task := mentionAgentInComment(t, f.s, f.handler, f.data, f.app, issue.ID)
	held := agentApprovalBatchCall(domain.AgentApproval{Kind: "connector", Calls: []domain.AgentToolCall{{ID: "saved_thread_call", Name: "list_teams", Arguments: json.RawMessage(`{}`)}}})
	// Store the same approval shape the worker's elicitation handler stores; this is the legacy saved-decision path.
	task, err = f.s.store.AppendAgentActivity(t.Context(), task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "elicitation", Signal: "select", Body: "Allow list_teams?", ToolCall: &held, SignalMetadata: map[string]any{"options": []any{map[string]any{"label": "Approve", "value": "approve"}}}})
	if err != nil {
		t.Fatal(err)
	}
	task = requestJSON[domain.AgentTask](t, f.handler, http.MethodPost, "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "Approve the saved team-list call and summarize its result. Do not change anything.", "approve": true}, http.StatusOK)
	f.s.deliverApplicationTask(t.Context(), task)
	deadline = time.After(3 * time.Minute)
	for {
		task, err = f.s.store.AgentTask(t.Context(), task.WorkspaceKey, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == "complete" {
			break
		}
		if task.Status == "error" || task.Status == "awaitingInput" {
			t.Fatalf("saved thread ended %s", task.Status)
		}
		select {
		case <-deadline:
			t.Fatal("saved thread timed out")
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Log("saved thread approval without RunState completed on the worker")
	chat("Web search is unavailable here. In one sentence say whether you can search the web, without using tools.")
	t.Log("API Responses / worker Chat protocol mismatch completed with the worker's web note")
}
