package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

// A built-in run whose model turn has no visible text and no tool calls (a thinking model that spent its whole
// output budget on reasoning, an empty or whitespace-only stream) is not an answer: the run retries once within its
// turn budget and then fails with the provider's finish reason, never completing on an empty "response".

// chatReasoningOnlySSE is a streamed turn with only reasoning_content and the given finish reason.
func chatReasoningOnlySSE(reasoning, finish string) string {
	first, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": reasoning}}}})
	last, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": finish}}})
	return "data: " + string(first) + "\n\ndata: " + string(last) + "\n\ndata: [DONE]\n\n"
}

// chatEmptyDeltasSSE is a streamed turn of empty deltas that ends normally.
func chatEmptyDeltasSSE() string {
	empty, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": ""}}}})
	last, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}}})
	return "data: " + string(empty) + "\n\ndata: " + string(empty) + "\n\ndata: " + string(last) + "\n\ndata: [DONE]\n\n"
}

// chatReasoningToolCallSSE is a streamed turn that reasons, then calls tool without any text.
func chatReasoningToolCallSSE(id, tool string, args map[string]any) string {
	reasoning, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": "Look up the file first."}}}})
	return "data: " + string(reasoning) + "\n\n" + chatToolCallSSE(id, tool, args)
}

const emptyTurnSecret = "PRIVATE-CHAIN-OF-THOUGHT"

func (r delegationRun) count(kind string) int {
	count := 0
	for _, activity := range r.activities {
		if activity.Type == kind {
			count++
		}
	}
	return count
}

func (r delegationRun) last() domain.AgentActivity {
	if len(r.activities) == 0 {
		return domain.AgentActivity{}
	}
	return r.activities[len(r.activities)-1]
}

// assertEmptyTurnFailed checks a run that failed on empty turns: two model calls (one retry), no tool executed, the
// task in error with an error activity last, no response, and an error that names the finish reason without the
// model's reasoning.
func assertEmptyTurnFailed(t *testing.T, run delegationRun, finish string) {
	t.Helper()
	if len(run.requests) != 2 {
		t.Fatalf("model calls = %d, want 2 (the turn and one retry)", len(run.requests))
	}
	if !strings.Contains(run.requests[1].lastContent(), "no visible reply") {
		t.Fatalf("the retry tells the model its turn was empty: %q", run.requests[1].lastContent())
	}
	if run.startActions() != 0 || run.count("action") != 0 || len(run.upstream) != 0 {
		t.Fatalf("no tool runs: %d actions, upstream %v", run.count("action"), run.upstream)
	}
	if run.task.Status != "error" {
		t.Fatalf("task status = %q, want error", run.task.Status)
	}
	if run.count("response") != 0 {
		t.Fatalf("no response activity: %+v", run.activities)
	}
	last := run.last()
	if last.Type != "error" || !strings.Contains(last.Body, "finish reason: "+finish) {
		t.Fatalf("the run ends with an error naming the finish reason %q: %+v", finish, last)
	}
	for _, activity := range run.activities {
		if strings.Contains(activity.Body, emptyTurnSecret) {
			t.Fatalf("the model's reasoning reached the thread: %+v", activity)
		}
	}
}

func TestBuiltinEmptyTurnReasoningOnlyLengthFails(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	run := h.delegate(t, "Release notes", "Summarize the release notes.", func(int, domain.Issue) string {
		return chatReasoningOnlySSE(emptyTurnSecret+" thinking at length", "length")
	})
	assertEmptyTurnFailed(t, run, "length")
	if !strings.Contains(run.last().Body, "output") {
		t.Fatalf("a length stop says the output budget ran out: %q", run.last().Body)
	}
}

func TestBuiltinEmptyTurnEmptyDeltasFails(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	run := h.delegate(t, "Release notes", "Summarize the release notes.", func(int, domain.Issue) string { return chatEmptyDeltasSSE() })
	assertEmptyTurnFailed(t, run, "stop")
}

func TestBuiltinEmptyTurnWhitespaceOnlyFails(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	run := h.delegate(t, "Release notes", "Summarize the release notes.", func(int, domain.Issue) string { return chatTextSSE(" \n\t \n") })
	assertEmptyTurnFailed(t, run, "none")
}

// One empty turn, then a real answer: the retry recovers and the run completes on the answer.
func TestBuiltinEmptyTurnRetryRecovers(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	run := h.delegate(t, "Release notes", "Summarize the release notes.", func(index int, _ domain.Issue) string {
		if index == 0 {
			return chatReasoningOnlySSE(emptyTurnSecret, "length")
		}
		return chatTextSSE("Two fixes and one feature.")
	})
	if len(run.requests) != 2 || run.task.Status != "complete" || run.response() != "Two fixes and one feature." || run.count("error") != 0 {
		t.Fatalf("calls %d, status %q, reply %q, errors %v", len(run.requests), run.task.Status, run.response(), run.errors())
	}
}

func TestBuiltinEmptyTurnNormalShortAnswer(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	run := h.delegate(t, "Release notes", "Summarize the release notes.", func(int, domain.Issue) string { return chatTextSSE("OK.") })
	if len(run.requests) != 1 || run.task.Status != "complete" || run.response() != "OK." || run.last().Type != "response" {
		t.Fatalf("calls %d, status %q, reply %q, last %+v", len(run.requests), run.task.Status, run.response(), run.last())
	}
}

// A turn that reasons and calls a tool without any text is a real step: the tool runs and the run goes on.
func TestBuiltinEmptyTurnToolCallWithoutTextRuns(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	run := h.delegate(t, "Release notes", "Summarize the release notes.", func(index int, _ domain.Issue) string {
		if index == 0 {
			return chatReasoningToolCallSSE("c_glob", h.glob, map[string]any{"pattern": "CHANGELOG.md"})
		}
		return chatTextSSE("Found the changelog.")
	})
	if len(run.requests) != 2 || run.upstream["glob"] != 1 || run.task.Status != "complete" || run.response() != "Found the changelog." {
		t.Fatalf("calls %d, glob %d, status %q, reply %q, errors %v", len(run.requests), run.upstream["glob"], run.task.Status, run.response(), run.errors())
	}
	if strings.Contains(run.requests[1].lastContent(), "no visible reply") {
		t.Fatal("a tool-call turn is not treated as empty")
	}
}

// A development request whose model only ever thinks still enters Coding (Flow starts it), after the one retry,
// and the reply is the session's state rather than an empty "done".
func TestBuiltinEmptyTurnDevelopmentRequestTransfersToCoding(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	description := "Implement the multiply endpoint and add unit tests. environmentId: " + h.environments[0].ID
	run := h.delegate(t, "Add the multiply endpoint", description, func(int, domain.Issue) string {
		return chatReasoningOnlySSE(emptyTurnSecret, "length")
	})
	if len(run.requests) != 2 || run.task.CodingSessionID == "" || run.startActions() != 1 || len(run.errors()) != 0 {
		t.Fatalf("calls %d, session %q, start actions %d, errors %v", len(run.requests), run.task.CodingSessionID, run.startActions(), run.errors())
	}
	if reply := run.response(); !strings.Contains(reply, "coding session") || strings.Contains(reply, emptyTurnSecret) {
		t.Fatalf("the reply reports the session's state: %q", reply)
	}
}

// syncBuffer is a log sink written from the run's goroutine and read by the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The error cannot be written (the task moved on meanwhile): the failure is logged with the run's diagnostics, and
// the task never reads as complete.
func TestBuiltinEmptyTurnPersistenceErrorIsReported(t *testing.T) {
	logs := &syncBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)

	h := newDelegationHarness(t, "acme/api")
	var taskID string
	run := h.delegate(t, "Release notes", "Summarize the release notes.", func(index int, issue domain.Issue) string {
		if index == 1 {
			// Another writer moves the task on before the run records its failure.
			task, err := h.s.store.AgentTask(t.Context(), h.workspace, issue.AgentSessionID)
			if err == nil {
				taskID = task.ID
				_, _ = h.s.store.AppendAgentActivity(t.Context(), h.workspace, task.ID, task.Version, domain.AgentActivity{ActorID: h.agentUser, Type: "thought", Body: "elsewhere", Ephemeral: true})
			}
		}
		return chatReasoningOnlySSE(emptyTurnSecret, "length")
	})
	if taskID == "" {
		t.Fatal("the retry ran")
	}
	if len(run.requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(run.requests))
	}
	if run.task.Status == "complete" || run.count("response") != 0 {
		t.Fatalf("the task never completes on an empty turn: status %q, %+v", run.task.Status, run.activities)
	}
	output := logs.String()
	if !strings.Contains(output, "could not record") || !strings.Contains(output, "stop_reason=length") {
		t.Fatalf("the write-back failure is logged with its diagnostics:\n%s", output)
	}
	if strings.Contains(output, emptyTurnSecret) {
		t.Fatal("the log carries no reasoning text")
	}
}
