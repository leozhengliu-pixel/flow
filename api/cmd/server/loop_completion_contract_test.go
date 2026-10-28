package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func (p *fakeLoopProvider) script(replies ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replies, p.inputs, p.tools = replies, nil, nil
}

func (p *fakeLoopProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inputs)
}

// lastInput is the last request's conversation.
func (p *fakeLoopProvider) lastInput() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inputs[len(p.inputs)-1]
}

func countToolCalls(calls []domain.LoopRunToolCall, name string) int {
	count := 0
	for _, call := range calls {
		if call.Name == name {
			count++
		}
	}
	return count
}

// A turn that only promises the next step is not the end of the run: the
// model is asked to continue, and its real answer becomes the result.
func TestLoopRunPromiseIsNotTheFinalAnswer(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	provider.script("I will check the remaining items now.", "Checked all 4 open bugs: 2 need repro steps, 2 are ready.")
	loop, started := startTestRun(t, handler, "Digest", "Summarize open bugs.", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if calls := provider.calls(); calls != 2 {
		t.Fatalf("model calls = %d, want 2 (the run stopped at the promise)", calls)
	}
	if run.Status != "completed" || !strings.Contains(run.Output, "2 are ready") {
		t.Fatalf("run = %s %s %q output %q", run.Status, run.FailureReason, run.Error, run.Output)
	}
	if !strings.Contains(provider.lastInput(), "Do the remaining steps now") {
		t.Fatalf("the continuation did not ask for the final report: %s", provider.lastInput())
	}
}

func TestLoopRunCompletionContract(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	projectID := firstProjectID(t, handler)
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Digest", "instructions": "Summarize open bugs and post what the projects need.", "status": "published"}, http.StatusCreated)
	run := func(replies ...string) domain.LoopRun {
		t.Helper()
		provider.script(replies...)
		started := startNextRun(t, handler, loop.ID)
		return waitForRun(t, handler, loop.ID, started.ID)
	}

	// Missing finish_run: one correction, then the report.
	got := run("All three projects are on track.", `tool:finish_run {"status":"done","summary":"All three projects are on track."}`)
	if provider.calls() != 2 || got.Status != "completed" || got.Summary == nil || got.Summary.Status != "done" {
		t.Fatalf("missing finish_run: calls %d run %+v", provider.calls(), got)
	}

	// A valid short final answer ends after the correction confirms it.
	got = run("Nothing changed today.", "Nothing changed today.")
	if provider.calls() != 2 || got.Status != "completed" || got.FailureReason != "" || !strings.Contains(got.Output, "Nothing changed today.") {
		t.Fatalf("short final answer: calls %d run %+v", provider.calls(), got)
	}

	// finish_run ends at once: nothing_to_do and done need no correction.
	got = run(`tool:finish_run {"status":"nothing_to_do","summary":"No open bugs."}`)
	if provider.calls() != 1 || got.Status != "completed" || len(got.ToolCalls) != 0 || got.Output != "No open bugs." {
		t.Fatalf("nothing_to_do: calls %d run %+v", provider.calls(), got)
	}

	// Explicit incomplete.
	got = run(`tool:finish_run {"status":"incomplete","summary":"Read 2 of 4 projects.","notDone":["Project C: no access"]}`)
	if provider.calls() != 1 || got.Status != "needs_review" || got.FailureReason != "incomplete" || !strings.Contains(got.Error, "Project C: no access") || got.Summary.Status != "incomplete" {
		t.Fatalf("explicit incomplete: calls %d run %+v", provider.calls(), got)
	}

	// Plain-text mid-stop: promises only, the corrections run out, the run is
	// saved as incomplete with the reason, and status, summary and error agree.
	got = run("Let me look at the remaining projects now.", "I'll check the rest next.", "Next, I will review the last project:")
	if provider.calls() != 3 || got.Status != "needs_review" || got.FailureReason != "incomplete" || got.Summary == nil || got.Summary.Status != "incomplete" || !strings.Contains(got.Error, "stopped") || len(got.Summary.NotDone) == 0 || got.Summary.NotDone[0] != got.Error || !strings.Contains(got.Output, "review the last project") {
		t.Fatalf("mid-stop: calls %d run status %s reason %s error %q summary %+v", provider.calls(), got.Status, got.FailureReason, got.Error, got.Summary)
	}

	// Ending after a tool error without reporting it.
	got = run(`tool:save_comment {"issueId":"missing-issue","body":"x"}`, "Done.", "Done.")
	if provider.calls() != 3 || got.Status != "needs_review" || got.FailureReason != "tool_error" || countToolCalls(got.ToolCalls, "save_comment") != 1 || !strings.Contains(got.Error, "failed") {
		t.Fatalf("ending after a tool error: calls %d run %+v", provider.calls(), got)
	}

	// A successful write is never replayed by the correction.
	got = run(`tool:save_status_update {"type":"project","project":"`+projectID+`","body":"On track.","health":"onTrack"}`, "Posted the update.", `tool:finish_run {"status":"done","summary":"Posted the update."}`)
	if provider.calls() != 3 || got.Status != "completed" || got.Produced["statusUpdate"] != 1 || countToolCalls(got.ToolCalls, "save_status_update") != 1 {
		t.Fatalf("write then correction: calls %d run %+v", provider.calls(), got)
	}

	// Budget exhausted: all tool turns used, then a forced summary without
	// finish_run; the run is not completed.
	setLoopLimits(srv, func(l *loopRunLimits) { l.MaxTurns = 2 })
	got = run(`tool:list_projects {"limit":5}`, `tool:list_teams {}`, "Read projects and teams so far.")
	if provider.calls() != 3 || got.Status != "needs_review" || got.FailureReason != "budget_exhausted" || got.Summary == nil || got.Summary.Status != "incomplete" || !strings.Contains(got.Output, "Read projects and teams so far.") {
		t.Fatalf("budget exhausted: calls %d run %+v", provider.calls(), got)
	}
	setLoopLimits(srv, func(l *loopRunLimits) { l.MaxTurns = 8 })

	// Cancel stops the run with one model call.
	provider.script("block")
	started := startNextRun(t, handler, loop.ID)
	deadline := time.Now().Add(5 * time.Second)
	for provider.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancelled := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+started.ID+"/cancel", nil, http.StatusOK)
	waitForRun(t, handler, loop.ID, started.ID)
	if provider.calls() != 1 || cancelled.Status != "cancelled" || cancelled.FailureReason != "cancelled" {
		t.Fatalf("cancel: calls %d run %+v", provider.calls(), cancelled)
	}
}

func TestLoopReplyCompletionContract(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	provider.script(`tool:finish_run {"status":"done","summary":"Summarized 4 open bugs."}`)
	loop, started := startTestRun(t, handler, "Digest", "Summarize open bugs.", nil)
	waitForRun(t, handler, loop.ID, started.ID)
	path := "/api/loops/" + loop.ID + "/runs/" + started.ID + "/replies"
	reply := func(count int, body string, replies ...string) domain.LoopRunReply {
		t.Helper()
		provider.script(replies...)
		requestJSON[domain.LoopRun](t, handler, http.MethodPost, path, map[string]any{"body": body}, http.StatusAccepted)
		run := waitForLoopReply(t, handler, loop.ID, started.ID, count)
		if run.Status != "completed" {
			t.Fatalf("a reply changed the run: %+v", run)
		}
		return run.Replies[count-1]
	}

	// A plain answer to a question needs no correction.
	got := reply(1, "How many bugs were there?", "There were 4 open bugs.")
	if provider.calls() != 1 || got.Status != "completed" || got.Output != "There were 4 open bugs." {
		t.Fatalf("plain answer: calls %d reply %+v", provider.calls(), got)
	}

	// A promise gets a continuation; the real answer is the result.
	got = reply(2, "Which ones need repro steps?", "Let me check those bugs now.", "FLOW-2 and FLOW-3 need repro steps.")
	if provider.calls() != 2 || got.Status != "completed" || !strings.Contains(got.Output, "FLOW-2 and FLOW-3") {
		t.Fatalf("promise in a reply: calls %d reply %+v", provider.calls(), got)
	}

	// Promises only: the reply is saved as unfinished, not completed.
	got = reply(3, "And the rest?", "I will check the rest now.", "Let me continue with the rest.", "I'll look at them next.")
	if provider.calls() != 3 || got.Status != "failed" || got.FailureReason != "incomplete" || !strings.Contains(got.Error, "stopped") {
		t.Fatalf("mid-stop reply: calls %d reply %+v", provider.calls(), got)
	}

	// finish_run incomplete in a reply is not completed either.
	got = reply(4, "Try again", `tool:finish_run {"status":"incomplete","summary":"Could not read FLOW-4.","notDone":["FLOW-4: not found"]}`)
	if provider.calls() != 1 || got.Status != "failed" || got.FailureReason != "incomplete" || !strings.Contains(got.Error, "FLOW-4: not found") || !strings.Contains(got.Output, "Could not read FLOW-4.") {
		t.Fatalf("incomplete reply: calls %d reply %+v", provider.calls(), got)
	}
}
