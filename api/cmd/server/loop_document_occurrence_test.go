package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A scheduled loop's run is for one occurrence: the scheduled date in the
// loop's timezone (with the hour for hourly schedules), whether the schedule
// or a person started it. Event runs have none.
func TestLoopRunOccurrence(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	daily := domain.Loop{TriggerType: "schedule", TriggerConfig: map[string]any{"unit": "day", "time": "23:30", "timezone": "Asia/Shanghai"}}
	scheduled := time.Date(2026, 10, 26, 23, 30, 0, 0, shanghai)
	late := scheduled.Add(50 * time.Minute) // started after midnight
	for _, item := range []struct {
		name    string
		loop    domain.Loop
		trigger loopTrigger
		now     time.Time
		want    string
	}{
		{"a late scheduled run keeps its scheduled date", daily, loopTrigger{Kind: "schedule", ScheduledAt: scheduled}, late, "2026-10-26"},
		{"a manual run is for today in the loop's timezone", daily, loopTrigger{Kind: "manual"}, time.Date(2026, 10, 26, 16, 0, 0, 0, time.UTC), "2026-10-27"},
		{"hourly schedules include the hour", domain.Loop{TriggerType: "schedule", TriggerConfig: map[string]any{"unit": "hour", "timezone": "Asia/Shanghai"}}, loopTrigger{Kind: "schedule", ScheduledAt: scheduled}, late, "2026-10-26T23"},
		{"no timezone is UTC", domain.Loop{TriggerType: "schedule"}, loopTrigger{Kind: "manual"}, scheduled, "2026-10-26"},
		{"event loops have no occurrence", domain.Loop{TriggerType: "issue"}, loopTrigger{Kind: "event"}, late, ""},
		{"a manual run of an event loop has none", domain.Loop{TriggerType: "issue"}, loopTrigger{Kind: "manual", EntityID: "issue_1"}, late, ""},
	} {
		if got := loopRunOccurrence(item.loop, item.trigger, item.now); got != item.want {
			t.Errorf("%s: occurrence %q, want %q", item.name, got, item.want)
		}
	}
	if today := loopToday(daily, time.Date(2026, 10, 26, 16, 0, 0, 0, time.UTC)); today != "Tuesday, 2026-10-27 (Asia/Shanghai)" {
		t.Errorf("today = %q", today)
	}
}

// The report: running the team digest again on the same day (run now after
// the scheduled run) must not add the day's section twice. The second run's
// appends are refused with a clear error, the original text stays, and the
// run is accepted because the day's section exists.
func TestLoopAppendsOncePerOccurrence(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	original := "## 2026-10-25\n\n- 已有记录"
	document := loopTestDocument(t, handler, map[string]any{"title": "开发团队工作记录", "content": original})
	schedule := map[string]any{"interval": 1, "unit": "day", "time": "09:00", "timezone": "Asia/Shanghai"}
	entry := "\n\n## 今日\n\n- 完成登录修复"
	provider.script(loopAppendCall(document.ID, entry), `tool:finish_run {"status":"done","summary":"已追加今日总结。"}`)
	loop, started := startTestRun(t, handler, "开发团队日报", loopTeamDigestZH, map[string]any{"triggerConfig": schedule})
	first := waitForRun(t, handler, loop.ID, started.ID)
	want := loopRunOccurrence(loop, loopTrigger{Kind: "manual"}, time.Now())
	if first.Status != "completed" || first.Produced["document"] != 1 || first.Occurrence != want {
		t.Fatalf("first run = %s %s %q produced %v occurrence %q (want %q)", first.Status, first.FailureReason, first.Error, first.Produced, first.Occurrence, want)
	}
	if call := loopRunCall(first, "save_document", 0); call.Document == nil || call.Document.Occurrence != want || call.Document.ExistingRunID != "" {
		t.Fatalf("first append record = %+v", call.Document)
	}

	// Run now again: the same text, then a reworded section for the same day.
	provider.script(
		loopAppendCall(document.ID, entry),
		loopAppendCall(document.ID, "\n\n## 今日（补充）\n\n- 完成登录修复"),
		`tool:finish_run {"status":"done","summary":"今日段落已存在，未重复追加。"}`,
	)
	second := waitForRun(t, handler, loop.ID, startNextRun(t, handler, loop.ID).ID)
	if content := loopTestDocumentContent(t, handler, document.ID); content != original+entry {
		t.Fatalf("document after the second run = %q", content)
	}
	for nth := range 2 {
		call := loopRunCall(second, "save_document", nth)
		if call.Status != "blocked" || !strings.Contains(call.Error, "already appended its section for "+want) || call.Document == nil || call.Document.ExistingRunID != first.ID {
			t.Fatalf("second run append %d = %+v document %+v", nth, call, call.Document)
		}
	}
	if second.Status != "completed" || second.Produced["document"] != 0 || second.Occurrence != want {
		t.Fatalf("second run = %s %s %q produced %v occurrence %q", second.Status, second.FailureReason, second.Error, second.Produced, second.Occurrence)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, "This run is for the "+want+" occurrence") || !strings.Contains(prompt, "(Asia/Shanghai)") {
		t.Fatalf("the prompt does not name the occurrence:\n%s", prompt)
	}
	// The model was told why, as a tool error.
	if input := provider.lastInput(); !strings.Contains(input, "already appended its section for "+want) {
		t.Fatalf("the model was not told the section exists: %s", input)
	}
}

// A scheduled run that started late for yesterday's occurrence and a manual
// run today are different dates: both append. Updating the day's section
// (not appending) is still allowed after the append.
func TestLoopAppendOccurrencesAreDates(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	document := loopTestDocument(t, handler, map[string]any{"title": "Team log", "content": "Existing entry"})
	loop, started := startTestRun(t, handler, "Digest", loopTeamDigestEN, map[string]any{"triggerConfig": map[string]any{"interval": 1, "unit": "day", "time": "23:00"}})
	waitForRun(t, handler, loop.ID, started.ID) // the fake provider has no script: a run that wrote nothing
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	provider.script(loopAppendCall(document.ID, "\n\n- Yesterday"), `tool:finish_run {"status":"done","summary":"Appended."}`)
	var scheduled domain.LoopRun
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var err error
		if scheduled, err = srv.startLoopRun("test-workspace", loop.ID, loopTrigger{Kind: "schedule", ScheduledAt: yesterday}, ""); err == nil || !errors.Is(err, errConflict) || time.Now().After(deadline) {
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if run := waitForRun(t, handler, loop.ID, scheduled.ID); run.Status != "completed" || run.Occurrence != yesterday.Format("2006-01-02") {
		t.Fatalf("scheduled run = %s %s %q occurrence %q", run.Status, run.FailureReason, run.Error, run.Occurrence)
	}
	provider.script(
		loopAppendCall(document.ID, "\n\n- Today"),
		`tool:save_document {"id":"`+document.ID+`","patch":[{"op":"replace","old_string":"- Today","new_string":"- Today (fixed)"}]}`,
		`tool:finish_run {"status":"done","summary":"Appended."}`,
	)
	today := waitForRun(t, handler, loop.ID, startNextRun(t, handler, loop.ID).ID)
	if today.Status != "completed" || today.Produced["document"] != 2 {
		t.Fatalf("today's run = %s %s %q produced %v calls %+v", today.Status, today.FailureReason, today.Error, today.Produced, today.ToolCalls)
	}
	if content := loopTestDocumentContent(t, handler, document.ID); content != "Existing entry\n\n- Yesterday\n\n- Today (fixed)" {
		t.Fatalf("document content = %q", content)
	}
}

// Claims hold across runs and servers: a run that is appending right now
// blocks another run of the occurrence; a run that stopped without writing
// hands the occurrence over; a run that wrote the document but stopped
// before recording it still counts as having appended.
func TestLoopOccurrenceClaimsAcrossRuns(t *testing.T) {
	srv, handler := newReliableLoopServer(t, &fakeLoopProvider{})
	ctx := context.Background()
	const workspace, loopID, occurrence = "test-workspace", "loop_claims", "2026-10-26"
	document := loopTestDocument(t, handler, map[string]any{"title": "Team log", "content": "Existing entry"})
	contract := appendContract()
	entry := "\n\n## 2026-10-26\n\n- Fixed login"
	call := domain.AgentToolCall{ID: "call_append", Name: "save_document", Arguments: json.RawMessage(strings.TrimPrefix(loopAppendCall(document.ID, entry), "tool:save_document "))}
	run := func(id, status string) *loopRunRecorder {
		t.Helper()
		now := time.Now().UTC()
		record := domain.LoopRun{ID: id, LoopID: loopID, Status: status, Occurrence: occurrence, StartedAt: now, HeartbeatAt: &now}
		if err := srv.store.CreateLoopRun(ctx, workspace, record, store.LoopRunLease{Owner: "server-" + id, ExpiresAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		return &loopRunRecorder{s: srv, workspace: workspace, run: record}
	}
	stop := func(id string) {
		t.Helper()
		if _, err := srv.store.UpdateLoopRun(ctx, workspace, id, nil, func(run *domain.LoopRun) error {
			run.Status = "interrupted"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Another server's run claimed the occurrence and is appending.
	other := run("loop_run_other", "running")
	if prepared, err := srv.prepareLoopDocumentWrite(other, domain.Bootstrap{}, contract, call); err != nil || prepared.claim == nil {
		t.Fatalf("first claim = %+v %v", prepared, err)
	}
	mine := run("loop_run_mine", "running")
	prepared, err := srv.prepareLoopDocumentWrite(mine, domain.Bootstrap{}, contract, call)
	if err == nil || !strings.Contains(err.Error(), "right now") || prepared.record.ExistingRunID != other.run.ID {
		t.Fatalf("append while another run appends = %+v %v", prepared, err)
	}
	// It stopped without writing: this run takes the occurrence over.
	stop(other.run.ID)
	if prepared, err = srv.prepareLoopDocumentWrite(mine, domain.Bootstrap{}, contract, call); err != nil || prepared.claim == nil || prepared.claim.RunID != mine.run.ID {
		t.Fatalf("take-over = %+v %v", prepared, err)
	}
	// It writes the document, then its server stops before recording it.
	requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"content": "Existing entry" + entry}, http.StatusOK)
	stop(mine.run.ID)
	third := run("loop_run_third", "running")
	if prepared, err = srv.prepareLoopDocumentWrite(third, domain.Bootstrap{}, contract, call); err == nil || !strings.Contains(err.Error(), "already appended its section for "+occurrence) || prepared.record.ExistingRunID != mine.run.ID {
		t.Fatalf("append after a lost result = %+v %v", prepared, err)
	}
	if claim, err := srv.store.LoopDocumentAppend(ctx, workspace, loopID, occurrence, document.ID); err != nil || claim.Status != store.LoopAppendAppended || claim.RunID != mine.run.ID {
		t.Fatalf("claim after the lost result = %+v %v", claim, err)
	}
	if content := loopTestDocumentContent(t, handler, document.ID); content != "Existing entry"+entry {
		t.Fatalf("document content = %q", content)
	}

	// A call that failed without writing releases its claim.
	next := run("loop_run_next", "running")
	next.run.Occurrence = "2026-10-27"
	prepared, err = srv.prepareLoopDocumentWrite(next, domain.Bootstrap{}, contract, call)
	if err != nil || prepared.claim == nil {
		t.Fatalf("next occurrence claim = %+v %v", prepared, err)
	}
	srv.settleLoopDocumentClaim(workspace, prepared, errors.New("timeout"))
	if _, err := srv.store.LoopDocumentAppend(ctx, workspace, loopID, "2026-10-27", document.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a failed append kept its claim: %v", err)
	}
}

// Two servers claiming the same occurrence at once: exactly one wins.
func TestLoopOccurrenceClaimIsAtomic(t *testing.T) {
	srv, _ := newReliableLoopServer(t, &fakeLoopProvider{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for index := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim := store.LoopDocumentAppend{LoopID: "loop_race", Occurrence: "2026-10-26", DocumentID: "doc_1", RunID: "loop_run_" + string(rune('a'+index)), TextKey: "doc_1:abc", TextProbe: "- Fixed login"}
			_, claimed, err := srv.store.ClaimLoopDocumentAppend(context.Background(), "test-workspace", claim)
			if err != nil {
				t.Error(err)
			}
			if claimed {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("%d runs claimed the same occurrence", winners)
	}
}

// Create, update and append: the prompt asks for the operation the
// instructions name, the tools enforce it, and acceptance checks the stored
// document for it.
func TestLoopDocumentOperationsAgree(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	for _, item := range []struct {
		op, instructions string
		call             func(id string) string
		content          string // the existing document's content after the run; empty for create
	}{
		{loopOpCreate, "Create a new document with today's summary.", func(string) string {
			return `tool:save_document {"title":"Summary 10-26","content":"Today: fixed login."}`
		}, ""},
		{loopOpUpdate, "Update the team document with today's summary.", func(id string) string {
			return `tool:save_document {"id":"` + id + `","patch":[{"op":"replace","old_string":"Existing entry","new_string":"Existing entry; today: fixed login"}]}`
		}, "Existing entry; today: fixed login"},
		{loopOpAppend, "Append today's summary to the team doc.", func(id string) string { return loopAppendCall(id, "\n\n- Fixed login") }, "Existing entry\n\n- Fixed login"},
	} {
		document := loopTestDocument(t, handler, map[string]any{"title": "Team doc " + item.op, "content": "Existing entry"})
		provider.script(item.call(document.ID), `tool:finish_run {"status":"done","summary":"Done."}`)
		loop, started := startTestRun(t, handler, "Doc "+item.op, item.instructions, nil)
		run := waitForRun(t, handler, loop.ID, started.ID)
		if !strings.Contains(loopRunPrompt(t, provider, 0), map[string]string{loopOpCreate: "create a new document with save_document", loopOpUpdate: "then call save_document with its id (patch to change part of it)", loopOpAppend: `patch [{"op":"append","text":"..."}]`}[item.op]) {
			t.Errorf("%s: the prompt does not ask for it", item.op)
		}
		if run.Status != "completed" || run.Produced["document"] != 1 {
			t.Errorf("%s: run = %s %s %q produced %v", item.op, run.Status, run.FailureReason, run.Error, run.Produced)
		}
		if record := loopRunCall(run, "save_document", 0).Document; record == nil || record.Mode != item.op || !record.Changed {
			t.Errorf("%s: write record = %+v", item.op, record)
		}
		if content := loopTestDocumentContent(t, handler, document.ID); item.content != "" && content != item.content || item.content == "" && content != "Existing entry" {
			t.Errorf("%s: existing document content = %q", item.op, content)
		}
		if item.op == loopOpCreate {
			continue
		}
		// A new document instead of the existing one is refused and flagged.
		provider.script(`tool:save_document {"title":"Team doc copy","content":"Today"}`, `tool:finish_run {"status":"done","summary":"Done."}`)
		loop, started = startTestRun(t, handler, "Doc "+item.op+" new", item.instructions, nil)
		run = waitForRun(t, handler, loop.ID, started.ID)
		if call := loopRunCall(run, "save_document", 0); call.Status != "blocked" || !strings.Contains(call.Error, "existing document") || run.Status != "needs_review" {
			t.Errorf("%s: a new document instead = %s %q; run %s %q", item.op, call.Status, call.Error, run.Status, run.Error)
		}
	}
}

// The exact refusal from the report — negation, a preposition (在…上) and an
// enumeration with 、 — forbids comments on every resource it names, while
// reading comments is still allowed and is not counted as posting one.
func TestLoopChineseCommentRefusalAllowsReadingComments(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	instructions := "阅读今天事项下的评论并汇总需要跟进的问题。不要在任何事项、项目或其他资源上发布评论。"
	issue := firstIssueIdentifier(t, handler)
	provider.script(
		`tool:list_comments {"issueId":"`+issue+`","limit":5}`,
		`tool:save_comment {"issueId":"`+issue+`","body":"需要跟进"}`,
		`tool:finish_run {"status":"done","summary":"两条评论需要跟进。"}`,
	)
	loop, started := startTestRun(t, handler, "评论汇总", instructions, nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if read := loopRunCall(run, "list_comments", 0); read.Status != "completed" {
		t.Fatalf("reading comments = %+v", read)
	}
	if post := loopRunCall(run, "save_comment", 0); post.Status != "blocked" || !strings.HasPrefix(post.Error, loopProhibitedCallPrefix) {
		t.Fatalf("posting a comment = %+v", post)
	}
	if run.Produced["comment"] != 0 || len(run.ExpectedOutputs) != 0 {
		t.Fatalf("produced %v expected %v", run.Produced, run.ExpectedOutputs)
	}
	prompt := loopRunPrompt(t, provider, 0)
	if !strings.Contains(prompt, "This loop reads and reports") || strings.Contains(prompt, loopOutputPrompts["comment"]) || strings.Contains(prompt, "occurrence of the loop's schedule") {
		t.Fatalf("prompt:\n%s", prompt)
	}
}
