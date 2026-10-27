package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// The report that started this: a team digest appended to an existing team
// document, with comments explicitly forbidden.
const (
	loopTeamDigestZH = "汇总开发团队今日工作，将结果追加到已有团队文档中。不要在任何事项、项目或其他资源上发布评论。"
	loopTeamDigestEN = "Summarize the dev team's work today and append the result to the existing team document. Do not post comments on any issue, project, or other resource."
)

type loopIntentCase struct {
	text       string
	outputs    string // required kinds, comma separated
	operations string // kind:op pairs of the required kinds, comma separated
	prohibited string // forbidden kinds, comma separated
	readOnly   bool
	always     bool
	skips      bool
}

func loopIntentOperations(intent loopOutputExpectation) string {
	pairs := []string{}
	for _, kind := range intent.Outputs {
		if op := intent.Operations[kind]; op != "" {
			pairs = append(pairs, kind+":"+op)
		}
	}
	return strings.Join(pairs, ",")
}

func checkLoopIntent(t *testing.T, item loopIntentCase) {
	t.Helper()
	got := inferLoopOutputIntent(item.text)
	if outputs := strings.Join(got.Outputs, ","); outputs != item.outputs {
		t.Errorf("%q: outputs %q, want %q (%+v)", item.text, outputs, item.outputs, got)
	}
	if item.operations != "" && loopIntentOperations(got) != item.operations {
		t.Errorf("%q: operations %q, want %q", item.text, loopIntentOperations(got), item.operations)
	}
	if prohibited := strings.Join(got.Prohibited, ","); prohibited != item.prohibited {
		t.Errorf("%q: prohibited %q, want %q", item.text, prohibited, item.prohibited)
	}
	if got.ReadOnly != item.readOnly {
		t.Errorf("%q: read-only %v, want %v", item.text, got.ReadOnly, item.readOnly)
	}
	if got.Always != item.always {
		t.Errorf("%q: always %v, want %v", item.text, got.Always, item.always)
	}
	if (len(got.Skips) > 0) != item.skips {
		t.Errorf("%q: skips %q, want any=%v", item.text, got.Skips, item.skips)
	}
}

func TestInferLoopOutputOperations(t *testing.T) {
	for _, item := range []loopIntentCase{
		// The report, in Chinese and English.
		{text: loopTeamDigestZH, outputs: "document", operations: "document:append", prohibited: "comment"},
		{text: loopTeamDigestEN, outputs: "document", operations: "document:append", prohibited: "comment"},
		// Read-only mentions are not outputs.
		{text: "Read the doc and comment on the issue.", outputs: "comment", operations: "comment:write"},
		{text: "阅读团队文档，并在事项上评论", outputs: "comment", operations: "comment:write"},
		{text: "Read the team document and summarize it.", readOnly: true},
		// Create, update and append.
		{text: "Create a new document with today's summary.", outputs: "document", operations: "document:create"},
		{text: "新建一篇文档记录今日总结", outputs: "document", operations: "document:create"},
		{text: "Update the team document with today's summary.", outputs: "document", operations: "document:update"},
		{text: "将今日总结写入已有的团队文档", outputs: "document", operations: "document:update"},
		{text: "Append today's summary to the team doc.", outputs: "document", operations: "document:append"},
		{text: "在团队文档末尾追加今日总结", outputs: "document", operations: "document:append"},
		{text: "Read the existing team doc, then append today's summary to it.", outputs: "document", operations: "document:append"},
		{text: "Don't overwrite the team document, only append to it.", outputs: "document", operations: "document:append", prohibited: "statusUpdate,issue,comment"},
		// Conditional outputs.
		{text: "汇总今日工作并追加到团队文档。如果没有变动也要写日报。", outputs: "document", operations: "document:append", always: true},
		{text: "Append the daily report to the team doc, even if nothing changed.", outputs: "document", operations: "document:append", always: true},
		{text: "每天把日报追加到团队文档，节假日跳过。", outputs: "document", operations: "document:append", skips: true},
		{text: "Append the daily report to the team doc; skip on public holidays and weekends.", outputs: "document", operations: "document:append", skips: true},
		// Skipping items is not skipping the run.
		{text: "Comment on new bugs; skip issues that already have a comment.", outputs: "comment", operations: "comment:write"},
		{text: "Post a weekly update on each project. If nothing changed, don't post.", outputs: "statusUpdate", operations: "statusUpdate:write", skips: true},
		// Documents named as references are not outputs.
		{text: "Post a comment on the issue with links to the documents you used.", outputs: "comment", operations: "comment:write"},
		{text: "Reply in the thread with a short answer, followed by links to the issues, projects or documents you relied on.", readOnly: true},
		{text: "Merge requests that describe the same need even when worded differently, then append the themes to the team doc.", outputs: "document", operations: "document:append"},
	} {
		checkLoopIntent(t, item)
	}
	// A choice of outputs needs one of them.
	choice := inferLoopOutputIntent(`Post the report as a document or project update titled "Feature requests".`)
	if strings.Join(choice.Outputs, ",") != "statusUpdate,document" || len(choice.Alternatives) != 1 || strings.Join(choice.Alternatives[0], ",") != "statusUpdate,document" {
		t.Errorf("choice of outputs = %+v", choice)
	}
	run := domain.LoopRun{Produced: map[string]int{"statusUpdate": 1, "change": 1}}
	if status, reason, message := acceptLoopRun(domain.Loop{Instructions: `Post the report as a document or project update titled "Feature requests".`}, &run, nil); status != "completed" {
		t.Errorf("one output of a choice = %s %s %q", status, reason, message)
	}
}

// A refusal covers every object it names, across enumeration punctuation
// (、 , or) and the different ways of saying no.
func TestInferLoopOutputProhibitions(t *testing.T) {
	for _, item := range []loopIntentCase{
		{text: "汇总今日工作。禁止在事项、项目或其他资源上发布评论。", prohibited: "comment", readOnly: true},
		{text: "汇总今日工作。不得在任何事项、项目上留言。", prohibited: "comment", readOnly: true},
		{text: "汇总今日工作，别在事项、项目或文档上发评论", prohibited: "comment", readOnly: true},
		{text: "汇总今日工作，请勿在事项、项目或其他资源上发布评论", prohibited: "comment", readOnly: true},
		{text: "汇总今日工作，无需在事项、项目上发布评论或创建事项", prohibited: "issue,comment", readOnly: true},
		{text: "除追加团队文档外，不要发布评论或创建事项。", outputs: "document", operations: "document:append", prohibited: "issue,comment"},
		{text: "汇总今日进展并写入团队文档，除此之外不要做任何修改。", outputs: "document", operations: "document:update", prohibited: "statusUpdate,issue,comment"},
		{text: "Summarize today's work. Don't post comments on any issue, project, or other resource.", prohibited: "comment", readOnly: true},
		{text: "Summarize today's work. Never comment on issues or projects.", prohibited: "comment", readOnly: true},
		{text: "Append today's notes to the team doc without commenting on any issue.", outputs: "document", operations: "document:append", prohibited: "comment"},
		{text: "Only update the doc.", outputs: "document", operations: "document:update", prohibited: "statusUpdate,issue,comment"},
		{text: "Do not comment on status updates; post a status update on each project.", outputs: "statusUpdate", operations: "statusUpdate:write", prohibited: "comment"},
		{text: "Comment on bugs that don't have repro steps.", outputs: "comment", operations: "comment:write"},
		{text: "Triage new issues and set their priority; do not comment.", prohibited: "comment"},
	} {
		checkLoopIntent(t, item)
	}
}

// The repro end to end: the prompt asks for the append and forbids comments,
// and a run that writes nothing is flagged for missing the document.
func TestLoopTeamDigestPromptAndAcceptance(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	provider.script(`tool:finish_run {"status":"done","summary":"今日完成 3 项。"}`)
	loop, started := startTestRun(t, handler, "开发团队日报", loopTeamDigestZH, nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if strings.Join(run.ExpectedOutputs, ",") != "document" {
		t.Fatalf("expected outputs = %v, want [document]", run.ExpectedOutputs)
	}
	if run.Status != "needs_review" || run.FailureReason != "no_output" || !strings.Contains(run.Error, "document") {
		t.Fatalf("a run that wrote nothing = %s %s %q", run.Status, run.FailureReason, run.Error)
	}
	prompt := loopRunPrompt(t, provider, 0)
	if strings.Contains(prompt, loopOutputPrompts["comment"]) {
		t.Fatalf("the prompt asks for a comment the instructions forbid:\n%s", prompt)
	}
	for _, want := range []string{`"op":"append"`, "Do not post comments"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// loopTestDocument creates a document through the API.
func loopTestDocument(t *testing.T, handler http.Handler, input map[string]any) domain.Document {
	t.Helper()
	return requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", input, http.StatusCreated)
}

func loopTestDocumentContent(t *testing.T, handler http.Handler, id string) string {
	t.Helper()
	return requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+id, nil, http.StatusOK).Content
}

func loopAppendCall(id, text string) string {
	args, _ := json.Marshal(map[string]any{"id": id, "patch": []any{map[string]any{"op": "append", "text": text}}})
	return "tool:save_document " + string(args)
}

func loopRunCall(run domain.LoopRun, name string, nth int) domain.LoopRunToolCall {
	seen := 0
	for _, call := range run.ToolCalls {
		if call.Name == name {
			if seen == nth {
				return call
			}
			seen++
		}
	}
	return domain.LoopRunToolCall{}
}

// Appending keeps the document's content, a repeated append in the same run
// is not written twice, the forbidden comment is refused, and the run is
// accepted because the document really changed.
func TestLoopRunAppendsToTheTeamDocumentOnce(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := bootstrap.Teams[0]
	document := loopTestDocument(t, handler, map[string]any{"title": "开发团队工作记录", "content": "## 10-25\n\n- 已有记录", "teamIds": []string{team.ID}})
	entry := "\n\n## 10-26\n\n- 完成登录修复"
	provider.script(
		`tool:get_document {"id":"`+document.ID+`"}`,
		loopAppendCall(document.ID, entry),
		loopAppendCall(document.ID, entry),
		`tool:save_comment {"issueId":"`+firstIssueIdentifier(t, handler)+`","body":"今日总结"}`,
		`tool:finish_run {"status":"done","summary":"已追加今日总结。"}`,
	)
	loop, started := startTestRun(t, handler, "开发团队日报", loopTeamDigestZH, map[string]any{"teamId": team.ID})
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "completed" || run.Produced["document"] != 1 {
		t.Fatalf("append run = %s %s %q produced %v calls %+v", run.Status, run.FailureReason, run.Error, run.Produced, run.ToolCalls)
	}
	if content := loopTestDocumentContent(t, handler, document.ID); content != "## 10-25\n\n- 已有记录"+entry {
		t.Fatalf("document content = %q", content)
	}
	if first := loopRunCall(run, "save_document", 0); first.Document == nil || !first.Document.Changed || first.Document.Mode != "append" || first.Document.VersionAfter <= first.Document.VersionBefore {
		t.Fatalf("first append record = %+v", first.Document)
	}
	if repeat := loopRunCall(run, "save_document", 1); repeat.Document == nil || repeat.Document.Changed {
		t.Fatalf("the repeated append was written again: %+v", repeat)
	}
	if comment := loopRunCall(run, "save_comment", 0); comment.Status != "blocked" || !strings.Contains(comment.Error, "comment") {
		t.Fatalf("forbidden comment call = %+v", comment)
	}
	if run.Produced["comment"] != 0 {
		t.Fatalf("produced = %v", run.Produced)
	}
}

func firstIssueIdentifier(t *testing.T, handler http.Handler) string {
	t.Helper()
	page := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/issue-records?limit=1", nil, http.StatusOK)
	items, _ := page["items"].([]any)
	if len(items) == 0 {
		t.Fatal("fixture has no issues")
	}
	return items[0].(map[string]any)["identifier"].(string)
}

// A write that does not change the document, a full replacement that would
// drop its content, or a new document instead of the existing one does not
// satisfy an append.
func TestLoopRunAppendAcceptanceNeedsARealChange(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	document := loopTestDocument(t, handler, map[string]any{"title": "Team log", "content": "Existing entry"})
	cases := []struct {
		name    string
		call    string
		message string
	}{
		{"empty append", loopAppendCall(document.ID, ""), "did not change"},
		{"replace", `tool:save_document {"id":"` + document.ID + `","content":"Only today"}`, "append"},
		{"new document", `tool:save_document {"title":"Team log 10-26","content":"Today"}`, "existing document"},
	}
	for _, item := range cases {
		provider.script(item.call, `tool:finish_run {"status":"done","summary":"Appended."}`)
		loop, started := startTestRun(t, handler, "Digest "+item.name, loopTeamDigestEN, nil)
		run := waitForRun(t, handler, loop.ID, started.ID)
		if run.Status != "needs_review" || run.Produced["document"] != 0 || !strings.Contains(run.Error+loopRunCall(run, "save_document", 0).Error, item.message) {
			t.Errorf("%s: run = %s %s %q produced %v call %+v", item.name, run.Status, run.FailureReason, run.Error, run.Produced, loopRunCall(run, "save_document", 0))
		}
		if content := loopTestDocumentContent(t, handler, document.ID); content != "Existing entry" {
			t.Fatalf("%s: document content = %q", item.name, content)
		}
	}
}

// A continuation of the run ("继续") that repeats the append does not write
// it a second time.
func TestLoopRunAppendIsIdempotentAcrossContinuations(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	document := loopTestDocument(t, handler, map[string]any{"title": "Team log", "content": "Existing entry"})
	entry := "\n\n- Fixed login"
	provider.script(loopAppendCall(document.ID, entry), `tool:finish_run {"status":"incomplete","summary":"Provider hiccup.","notDone":["Check the remaining items"]}`)
	loop, started := startTestRun(t, handler, "Digest", loopTeamDigestEN, nil)
	if run := waitForRun(t, handler, loop.ID, started.ID); run.Produced["document"] != 1 {
		t.Fatalf("first attempt = %+v", run)
	}
	provider.script(loopAppendCall(document.ID, entry), `tool:finish_run {"status":"done","summary":"Done."}`)
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+started.ID+"/replies", map[string]any{"body": "继续"}, http.StatusAccepted)
	waitForLoopReply(t, handler, loop.ID, started.ID, 1)
	if content := loopTestDocumentContent(t, handler, document.ID); content != "Existing entry"+entry {
		t.Fatalf("document content after the continuation = %q", content)
	}
}

// An append whose call failed after the document was written (the result was
// lost) is not repeated when the model retries it.
func TestLoopDocumentAppendRetryAfterLostResult(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	document := loopTestDocument(t, handler, map[string]any{"title": "Team log", "content": "Existing entry"})
	entry := "\n\n- Fixed login"
	key := loopDocumentAppendKey(document.ID, entry)
	// The first attempt reached the document but reported an error.
	updated := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"content": "Existing entry" + entry}, http.StatusOK)
	rec := &loopRunRecorder{s: srv, workspace: "test-workspace", run: domain.LoopRun{ID: "loop_run_lost", ToolCalls: []domain.LoopRunToolCall{{Name: "save_document", Status: "error", Error: "timeout", Document: &domain.LoopRunDocumentWrite{ID: document.ID, Mode: "append", Key: key, VersionBefore: document.Version}}}}}
	call := domain.AgentToolCall{ID: "call_retry", Name: "save_document", Arguments: json.RawMessage(strings.TrimPrefix(loopAppendCall(document.ID, entry), "tool:save_document "))}
	prepared, err := srv.prepareLoopDocumentWrite(rec, loopOutputExpectation{Outputs: []string{"document"}, Operations: map[string]string{"document": "append"}}, call)
	if err != nil || prepared == nil || prepared.applied == "" || !prepared.record.Changed || prepared.record.VersionAfter != updated.Version {
		t.Fatalf("retry after a lost result = %+v err %v", prepared, err)
	}
	if content := loopTestDocumentContent(t, handler, document.ID); content != "Existing entry"+entry {
		t.Fatalf("document content = %q", content)
	}
}

// Explicit expected outputs win over the inference: the loop expects a
// comment, so the comment is asked for and allowed.
func TestExplicitLoopOutputsOverrideTheInferredOperations(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	issue := firstIssueIdentifier(t, handler)
	provider.script(`tool:save_comment {"issueId":"`+issue+`","body":"今日总结"}`, `tool:finish_run {"status":"done","summary":"已评论。"}`)
	loop, started := startTestRun(t, handler, "开发团队日报", loopTeamDigestZH, map[string]any{"expectedOutputs": []string{"comment"}})
	run := waitForRun(t, handler, loop.ID, started.ID)
	if strings.Join(run.ExpectedOutputs, ",") != "comment" || run.Status != "completed" || run.Produced["comment"] != 1 {
		t.Fatalf("explicit comment run = %s %s %q expected %v produced %v", run.Status, run.FailureReason, run.Error, run.ExpectedOutputs, run.Produced)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, loopOutputPrompts["comment"]) || strings.Contains(prompt, "Do not post comments") {
		t.Fatalf("explicit comment prompt:\n%s", prompt)
	}
}

// "Even if nothing changed" keeps the output required: nothing_to_do does not
// excuse it, and the prompt says so.
func TestLoopOutputRequiredEvenWhenNothingChanged(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	provider.script(`tool:finish_run {"status":"nothing_to_do","summary":"今日无变动。"}`)
	loop, started := startTestRun(t, handler, "日报", "汇总今日工作并追加到团队文档。如果没有变动也要写日报。", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "needs_review" || run.FailureReason != "no_output" {
		t.Fatalf("nothing_to_do run of an always loop = %s %s %q", run.Status, run.FailureReason, run.Error)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, "even when nothing changed") {
		t.Fatalf("the prompt does not say the output is required when nothing changed:\n%s", prompt)
	}
}

// A run may skip when the instructions say when to skip; the skip is
// reported explicitly and honoured. A skip the instructions do not allow
// still needs the output.
func TestLoopRunSkipFollowsTheInstructions(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	skip := `tool:finish_run {"status":"skipped","summary":"今天是国庆假期，按说明跳过。","skipReason":"节假日跳过"}`
	provider.script(skip)
	loop, started := startTestRun(t, handler, "日报", "每天把日报追加到团队文档，节假日跳过。", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "completed" || run.Summary == nil || run.Summary.Status != "skipped" || strings.Join(run.ExpectedOutputs, ",") != "document" {
		t.Fatalf("holiday skip = %s %s %q summary %+v expected %v", run.Status, run.FailureReason, run.Error, run.Summary, run.ExpectedOutputs)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, "skipped") || !strings.Contains(prompt, "节假日跳过") {
		t.Fatalf("the prompt does not explain the skip:\n%s", prompt)
	}

	provider.script(skip)
	strict, started := startTestRun(t, handler, "日报 strict", "每天把日报追加到团队文档。", nil)
	if run = waitForRun(t, handler, strict.ID, started.ID); run.Status != "needs_review" || run.FailureReason != "no_output" || !strings.Contains(run.Error, "skip") {
		t.Fatalf("a skip the instructions do not allow = %s %s %q", run.Status, run.FailureReason, run.Error)
	}

	// skipped needs its reason.
	if _, err := parseLoopSummary(json.RawMessage(`{"status":"skipped","summary":"Holiday."}`)); err == nil {
		t.Fatal("skipped without skipReason was accepted")
	}
}

// A document outside the loop's team access cannot be written: the call is
// refused with a clear reason and the run is flagged, not passed.
func TestLoopDocumentWritesStayInTheLoopTeams(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	other := domain.Team{ID: "team_other", Key: "OTH", Name: "Other"}
	if err := srv.store.MutateWorkspace(context.Background(), "test-workspace", "test.teams", other.ID, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, other)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	home := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0]
	document := loopTestDocument(t, handler, map[string]any{"title": "Other team log", "content": "Existing entry", "teamIds": []string{other.ID}})
	provider.script(loopAppendCall(document.ID, "\n\n- Fixed login"), `tool:finish_run {"status":"done","summary":"Appended."}`)
	loop, started := startTestRun(t, handler, "Digest", loopTeamDigestEN, map[string]any{"teamId": home.ID, "level": "team", "teamAccess": "selected"})
	run := waitForRun(t, handler, loop.ID, started.ID)
	call := loopRunCall(run, "save_document", 0)
	if call.Status != "blocked" || !strings.Contains(call.Error, "cannot access that document") {
		t.Fatalf("out-of-scope document write = %+v", call)
	}
	if run.Status != "needs_review" || run.FailureReason != "tool_error" || !strings.Contains(run.Error, "cannot access that document") {
		t.Fatalf("run = %s %s %q", run.Status, run.FailureReason, run.Error)
	}
	if content := loopTestDocumentContent(t, handler, document.ID); content != "Existing entry" {
		t.Fatalf("document content = %q", content)
	}
	if !slices.Contains(run.ExpectedOutputs, "document") {
		t.Fatalf("output validation was dropped: %v", run.ExpectedOutputs)
	}
}
