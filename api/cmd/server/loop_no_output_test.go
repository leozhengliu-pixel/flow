package main

import (
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// no_output means a required output of the run's contract was not written; an empty reply is empty_response. The two
// are never conflated, and a summary that describes a write is not the write.
func TestLoopNoOutputIsMissingOutputNotAnEmptyReply(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	projectID := firstProjectID(t, handler)
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Weekly update", "instructions": "Post a status update on the project every week.", "status": "published", "expectedOutputs": []string{"statusUpdate"}}, http.StatusCreated)
	run := func(replies ...string) domain.LoopRun {
		t.Helper()
		provider.script(replies...)
		started := startNextRun(t, handler, loop.ID)
		return waitForRun(t, handler, loop.ID, started.ID)
	}

	// The agent says it posted the update and has a reply, but nothing was written: missing output.
	got := run(`tool:finish_run {"status":"done","summary":"Posted the weekly update."}`)
	if got.Status != "needs_review" || got.FailureReason != "no_output" || got.Output == "" || got.Produced["statusUpdate"] != 0 ||
		!strings.HasPrefix(got.Error, "Required output missing:") || !strings.Contains(got.Error, loopDoneClaimNote) {
		t.Fatalf("summary without the write: %s/%s %q output %q produced %v", got.Status, got.FailureReason, got.Error, got.Output, got.Produced)
	}

	// No reply at all and nothing done: an empty response, not a missing output.
	got = run("")
	if got.Status != "failed" || got.FailureReason != "empty_response" || got.Output != "" {
		t.Fatalf("empty reply: %s/%s %q", got.Status, got.FailureReason, got.Error)
	}

	// The update was written and the report says so: completed.
	got = run(`tool:save_status_update {"type":"project","project":"`+projectID+`","body":"On track.","health":"onTrack"}`, `tool:finish_run {"status":"done","summary":"Posted."}`)
	if got.Status != "completed" || got.FailureReason != "" || got.Produced["statusUpdate"] != 1 {
		t.Fatalf("written update: %s/%s %q produced %v", got.Status, got.FailureReason, got.Error, got.Produced)
	}

	// The update was written, then the model went quiet: the output is there, so the run is not no_output; the
	// missing report is what needs review.
	got = run(`tool:save_status_update {"type":"project","project":"`+projectID+`","body":"On track.","health":"onTrack"}`, "", "", "", "")
	if got.Status != "needs_review" || got.FailureReason != "empty_response" || got.Produced["statusUpdate"] != 1 {
		t.Fatalf("written update, empty reply: %s/%s %q produced %v", got.Status, got.FailureReason, got.Error, got.Produced)
	}
}
