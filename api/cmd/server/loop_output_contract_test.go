package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Output contracts: extraction plumbing (schema validation, one retry,
// caching by instructions hash, a person's outputs win, the fallback), and
// runs that are prompted, gated and accepted from the contract.

// loopRunPrompt is the system prompt of the provider's nth recorded request.
func loopRunPrompt(t *testing.T, provider *fakeLoopProvider, request int) string {
	t.Helper()
	provider.mu.Lock()
	defer provider.mu.Unlock()
	var payload []any
	if err := json.Unmarshal([]byte(provider.inputs[request]), &payload); err != nil {
		t.Fatal(err)
	}
	prompt, _ := payload[0].(string)
	return prompt
}

func loopRunWrites(run domain.LoopRun) []string {
	names := []string{}
	for _, call := range run.ToolCalls {
		if loopWriteToolName(call.Name) {
			names = append(names, call.Name)
		}
	}
	return names
}

const loopPublishInstruction = "expected to produce output"

// loopReplyOnlyDigestZH is a reported loop: summarise, reply with the text,
// write nothing anywhere.
const loopReplyOnlyDigestZH = "请总结团队今天的工作。\n2. 今日新增事项：简要列出今天新创建的事项及创建者。\n只回复总结文本即可，不要在任何事项、项目、文档或其他资源上发布评论或写入内容。"

// The reported reproduction: create a new document per run with a dated
// title in the target team, never reuse an existing one.
const loopDailyDocumentZH = "在目标团队下创建一个新的 Document，标题格式为'每日总结-YYYY-MM-DD'，将完整的总结内容写入该 Document。每次运行都要创建一个单独的 Document，不要复用已有 Document。"

// loopDailyDocumentContract is that loop's contract as the extractor gives it.
const loopDailyDocumentContract = `{"readOnly":false,"readOnlyEvidence":"","outputs":[{"kind":"document","operation":"create","required":true,"target":{"mode":"new","titleTemplate":"每日总结-{date}","title":"","team":"","description":"a new document in the loop's team each run"},"cardinality":"onePerRun","conditions":[],"group":"","evidence":"在目标团队下创建一个新的 Document，标题格式为'每日总结-YYYY-MM-DD'","reasoning":"Each run creates its own document."}],"always":false,"alwaysEvidence":"","skipConditions":[]}`

func init() {
	setLoopTestContract(loopDailyDocumentZH, loopDailyDocumentContract)
}

func loopDocuments(t *testing.T, handler http.Handler) []domain.Document {
	t.Helper()
	return requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Documents
}

// The reproduction with the reported model calls: (1) a new document whose
// title still says YYYY-MM-DD, (2) an append to an unrelated document, (3)
// done with notDone. The document is created with today's title in the
// team, the append is refused and the unrelated document is untouched, and
// the run is not plain completed because the agent listed work not done.
func TestLoopDailyDocumentReproduction(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	team := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0]
	unrelated := loopTestDocument(t, handler, map[string]any{"title": "发布验证记录", "content": "原始发布验证内容"})
	before := requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+unrelated.ID, nil, http.StatusOK)
	create, _ := json.Marshal(map[string]any{"title": "每日总结-YYYY-MM-DD", "team": team.ID, "content": "测试用每日总结正文"})
	provider.script(
		"tool:save_document "+string(create),
		loopAppendCall(unrelated.ID, "\n\n## 每日总结\n测试用每日总结正文"),
		`tool:finish_run {"status":"done","summary":"已将每日总结追加到现有文档。","notDone":["无法创建要求的独立新文档。"]}`,
	)
	loop, started := startTestRun(t, handler, "每日总结", loopDailyDocumentZH, map[string]any{"teamId": team.ID})
	run := waitForRun(t, handler, loop.ID, started.ID)
	today := time.Now().UTC().Format("2006-01-02")
	title := "每日总结-" + today

	if run.OutputContract == nil || len(run.OutputContract.Outputs) != 1 || run.OutputContract.Outputs[0].Operation != loopOpCreate || run.OutputContract.Outputs[0].Target.RenderedTitle != title || run.OutputContract.Outputs[0].Target.TeamID != team.ID {
		t.Fatalf("run contract = %+v", run.OutputContract)
	}
	created := loopRunCall(run, "save_document", 0)
	if created.Status != "completed" || created.Document == nil || created.Document.Mode != loopOpCreate || created.Document.Title != title || !slices.Contains(created.Document.TeamIDs, team.ID) {
		t.Fatalf("create = %+v document %+v", created, created.Document)
	}
	appended := loopRunCall(run, "save_document", 1)
	if appended.Status != "blocked" || !strings.HasPrefix(appended.Error, loopContractRefusalPrefix) || !strings.Contains(appended.Error, "发布验证记录") {
		t.Fatalf("append to the unrelated document = %s %q", appended.Status, appended.Error)
	}
	after := requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+unrelated.ID, nil, http.StatusOK)
	if after.Content != before.Content || after.Version != before.Version || after.Title != before.Title {
		t.Fatalf("the unrelated document changed: %q v%d (was %q v%d)", after.Content, after.Version, before.Content, before.Version)
	}
	matching := 0
	for _, document := range loopDocuments(t, handler) {
		if document.Title == title {
			matching++
			if document.Content != "测试用每日总结正文" || !slices.Contains(document.TeamIDs, team.ID) {
				t.Fatalf("new document = %q teams %v", document.Content, document.TeamIDs)
			}
		}
	}
	if matching != 1 {
		t.Fatalf("%d documents titled %q", matching, title)
	}
	if run.Status != "needs_review" || run.FailureReason != "incomplete" || !strings.Contains(run.Error, "无法创建要求的独立新文档") || !strings.HasPrefix(run.Error, loopNotDoneNote) {
		t.Fatalf("run = %s %s %q", run.Status, run.FailureReason, run.Error)
	}
	if run.Produced["document"] != 1 || strings.Join(run.ExpectedOutputs, ",") != "document" || !slices.Contains(run.Notices, loopRefusedWriteNotice) {
		t.Fatalf("produced %v expected %v notices %v", run.Produced, run.ExpectedOutputs, run.Notices)
	}
	prompt := loopRunPrompt(t, provider, 0)
	for _, want := range []string{`titled exactly "` + title + `"`, "do not write to any other existing document", "exactly one per run"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt lacks %q:\n%s", want, prompt)
		}
	}

	// The same calls with a model that reports done without notDone: the
	// run completes on the new document alone.
	provider.script("tool:save_document "+string(create), loopAppendCall(unrelated.ID, "\n\n- x"), `tool:finish_run {"status":"done","summary":"已创建每日总结。"}`)
	second := waitForRun(t, handler, loop.ID, startNextRun(t, handler, loop.ID).ID)
	if second.Status != "completed" {
		t.Fatalf("second run = %s %s %q", second.Status, second.FailureReason, second.Error)
	}

	// A second document in the same run is refused: one per run.
	provider.script("tool:save_document "+string(create), "tool:save_document "+string(create), `tool:finish_run {"status":"done","summary":"已创建。"}`)
	third := waitForRun(t, handler, loop.ID, startNextRun(t, handler, loop.ID).ID)
	if call := loopRunCall(third, "save_document", 1); call.Status != "blocked" || !strings.Contains(call.Error, "one document per run") {
		t.Fatalf("second create in one run = %s %q", call.Status, call.Error)
	}
	if after := requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+unrelated.ID, nil, http.StatusOK); after.Version != before.Version {
		t.Fatal("the unrelated document changed")
	}
}

// When the new document cannot be created, the run says so: needs_review
// with the missing output and the failure, and nothing else changes.
func TestLoopDailyDocumentCreateFailure(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	team := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0]
	unrelated := loopTestDocument(t, handler, map[string]any{"title": "发布验证记录", "content": "原始发布验证内容"})
	create, _ := json.Marshal(map[string]any{"title": "每日总结-YYYY-MM-DD", "team": team.ID, "content": "正文", "template": "no-such-template"})
	provider.script(
		"tool:save_document "+string(create),
		loopAppendCall(unrelated.ID, "\n\n## 每日总结\n正文"),
		`tool:finish_run {"status":"done","summary":"已将每日总结追加到现有文档。"}`,
	)
	loop, started := startTestRun(t, handler, "每日总结", loopDailyDocumentZH, map[string]any{"teamId": team.ID})
	run := waitForRun(t, handler, loop.ID, started.ID)
	if call := loopRunCall(run, "save_document", 0); call.Status != "error" {
		t.Fatalf("create = %s %q", call.Status, call.Error)
	}
	title := "每日总结-" + time.Now().UTC().Format("2006-01-02")
	if run.Status != "needs_review" || run.FailureReason != "tool_error" || !strings.Contains(run.Error, `a new document titled "`+title+`"`) || !strings.Contains(run.Error, loopDoneClaimNote) {
		t.Fatalf("run = %s %s %q", run.Status, run.FailureReason, run.Error)
	}
	if strings.Contains(run.Error, loopContractRefusalPrefix) {
		t.Fatalf("the outcome names the refused append instead of the failed create: %q", run.Error)
	}
	if content := loopTestDocumentContent(t, handler, unrelated.ID); content != "原始发布验证内容" {
		t.Fatalf("unrelated document = %q", content)
	}
	if run.Produced["document"] != 0 {
		t.Fatalf("produced %v", run.Produced)
	}
}

// Past misreadings, each with the contract the extractor gives for it: the
// run's prompt, its tool gates and its acceptance follow the contract.
func TestLoopContractPastMisreadings(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	team := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0]
	issue := firstIssueIdentifier(t, handler)
	newIssue := `tool:save_issue {"title":"今日总结","team":"` + team.ID + `","description":"x"}`

	// "今日新增事项" is a report section, not an issue to create: the append
	// is the output, a new issue is refused.
	sections := "请总结团队今天的工作。\n2. 今日新增事项：简要列出今天新创建的事项及创建者。\n将总结结果追加到已有团队文档中，不要在任何事项、项目或其他资源上发布评论。"
	setLoopTestContract(sections, `{"readOnly":false,"outputs":[{"kind":"document","operation":"append","required":true,"target":{"mode":"existing","description":"the team's existing document"},"cardinality":"onePerRun","evidence":"将总结结果追加到已有团队文档中"},{"kind":"issue","operation":"read","required":false,"target":{"mode":"any"},"evidence":"今日新增事项：简要列出今天新创建的事项及创建者"},{"kind":"comment","operation":"forbidden","required":false,"target":{"mode":"any"},"evidence":"不要在任何事项、项目或其他资源上发布评论"}],"always":false,"skipConditions":[]}`)
	document := loopTestDocument(t, handler, map[string]any{"title": "团队日志", "content": "旧内容"})
	provider.script(newIssue, loopAppendCall(document.ID, "\n\n## 今日\n- 新增 2 个事项"), `tool:finish_run {"status":"done","summary":"已追加。"}`)
	loop, started := startTestRun(t, handler, "团队日报", sections, nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if call := loopRunCall(run, "save_issue", 0); call.Status != "blocked" || !strings.HasPrefix(call.Error, loopContractRefusalPrefix) || !strings.Contains(call.Error, "creating issues") {
		t.Fatalf("new issue in a report loop = %s %q", call.Status, call.Error)
	}
	if run.Status != "completed" || run.Produced["document"] != 1 || run.Produced["issue"] != 0 {
		t.Fatalf("report-section run = %s %s %q produced %v", run.Status, run.FailureReason, run.Error, run.Produced)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, "Do not create status updates, issues") || !strings.Contains(prompt, "Do not post comments") {
		t.Fatalf("report-section prompt:\n%s", prompt)
	}

	// "周报" alone is the run's reply, not a status update.
	weekly := "每周五写周报，汇总本周完成的事项。"
	setLoopTestContract(weekly, `{"readOnly":true,"readOnlyEvidence":"每周五写周报","outputs":[{"kind":"issue","operation":"read","required":false,"target":{"mode":"any"}}],"always":false,"skipConditions":[]}`)
	provider.script(`tool:save_status_update {"type":"project","project":"`+firstProjectID(t, handler)+`","body":"周报","health":"onTrack"}`, `tool:finish_run {"status":"done","summary":"本周完成 5 项。"}`)
	loop, started = startTestRun(t, handler, "周报", weekly, nil)
	run = waitForRun(t, handler, loop.ID, started.ID)
	if call := loopRunCall(run, "save_status_update", 0); call.Status != "blocked" || !strings.HasPrefix(call.Error, loopProhibitedCallPrefix) {
		t.Fatalf("status update in a weekly report loop = %s %q", call.Status, call.Error)
	}
	if run.Status != "completed" || len(run.ExpectedOutputs) != 0 || run.Produced["statusUpdate"] != 0 {
		t.Fatalf("weekly report run = %s %s %q expected %v", run.Status, run.FailureReason, run.Error, run.ExpectedOutputs)
	}

	// A refusal enumerated with 、 covers each object it names.
	enumerated := "汇总今日工作，无需在事项、项目上发布评论或创建事项。"
	setLoopTestContract(enumerated, `{"readOnly":true,"readOnlyEvidence":"汇总今日工作","outputs":[{"kind":"comment","operation":"forbidden","required":false,"target":{"mode":"any"},"evidence":"无需在事项、项目上发布评论"},{"kind":"issue","operation":"forbidden","required":false,"target":{"mode":"any"},"evidence":"或创建事项"}],"always":false,"skipConditions":[]}`)
	provider.script(newIssue, `tool:save_comment {"issueId":"`+issue+`","body":"总结"}`, `tool:finish_run {"status":"done","summary":"今日完成 3 项。"}`)
	loop, started = startTestRun(t, handler, "日报", enumerated, nil)
	run = waitForRun(t, handler, loop.ID, started.ID)
	for _, name := range []string{"save_issue", "save_comment"} {
		if call := loopRunCall(run, name, 0); call.Status != "blocked" || !strings.HasPrefix(call.Error, loopProhibitedCallPrefix) {
			t.Fatalf("%s in an enumerated refusal = %s %q", name, call.Status, call.Error)
		}
	}
	if run.Status != "completed" || run.Produced["change"] != 0 {
		t.Fatalf("enumerated refusal run = %s %q produced %v", run.Status, run.Error, run.Produced)
	}

	// An exception keeps its output while the refused kinds stay refused.
	exception := "除追加团队文档外，不要发布评论或创建事项。"
	setLoopTestContract(exception, `{"readOnly":false,"outputs":[{"kind":"document","operation":"append","required":true,"target":{"mode":"existing","description":"the team document"},"evidence":"除追加团队文档外"},{"kind":"comment","operation":"forbidden","required":false,"target":{"mode":"any"},"evidence":"不要发布评论"},{"kind":"issue","operation":"forbidden","required":false,"target":{"mode":"any"},"evidence":"创建事项"}],"always":false,"skipConditions":[]}`)
	provider.script(newIssue, `tool:save_document {"title":"新文档","content":"x"}`, loopAppendCall(document.ID, "\n\n- 例外"), `tool:finish_run {"status":"done","summary":"已追加。"}`)
	loop, started = startTestRun(t, handler, "例外", exception, nil)
	run = waitForRun(t, handler, loop.ID, started.ID)
	if call := loopRunCall(run, "save_issue", 0); call.Status != "blocked" || !strings.Contains(call.Error, "create issues") {
		t.Fatalf("issue in the exception loop = %s %q", call.Status, call.Error)
	}
	if call := loopRunCall(run, "save_document", 0); call.Status != "blocked" || !strings.Contains(call.Error, "existing document") {
		t.Fatalf("new document in an append loop = %s %q", call.Status, call.Error)
	}
	if run.Status != "completed" || run.Produced["document"] != 1 {
		t.Fatalf("exception run = %s %s %q", run.Status, run.FailureReason, run.Error)
	}

	// An existing document named by title is the only one the loop writes to.
	named := "Append today's release notes to the \"Release log\" document."
	setLoopTestContract(named, `{"readOnly":false,"outputs":[{"kind":"document","operation":"append","required":true,"target":{"mode":"existing","title":"Release log"},"cardinality":"onePerRun","evidence":"Append today's release notes to the \"Release log\" document."}],"always":false,"skipConditions":[]}`)
	log := loopTestDocument(t, handler, map[string]any{"title": "Release log", "content": "v1"})
	other := loopTestDocument(t, handler, map[string]any{"title": "Release checklist", "content": "keep"})
	provider.script(loopAppendCall(other.ID, "\n- v2"), loopAppendCall(log.ID, "\n- v2"), `tool:finish_run {"status":"done","summary":"Appended."}`)
	loop, started = startTestRun(t, handler, "Release log", named, nil)
	run = waitForRun(t, handler, loop.ID, started.ID)
	if call := loopRunCall(run, "save_document", 0); call.Status != "blocked" || !strings.Contains(call.Error, `"Release log"`) {
		t.Fatalf("append to another document = %s %q", call.Status, call.Error)
	}
	if run.Status != "completed" || run.OutputContract.Outputs[0].Target.DocumentID != log.ID || loopTestDocumentContent(t, handler, other.ID) != "keep" || loopTestDocumentContent(t, handler, log.ID) != "v1\n- v2" {
		t.Fatalf("named-document run = %s %q contract %+v", run.Status, run.Error, run.OutputContract)
	}
}

// done with notDone is never plain completed; incomplete stays incomplete.
func TestLoopRunNotDoneIsNotCompleted(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	provider.script(`tool:finish_run {"status":"done","summary":"Summary.","notDone":["Could not read project B"]}`)
	loop, started := startTestRun(t, handler, "Digest", "Summarize open bugs.", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "needs_review" || run.FailureReason != "incomplete" || !strings.Contains(run.Error, "Could not read project B") {
		t.Fatalf("done with notDone = %s %s %q", run.Status, run.FailureReason, run.Error)
	}
	if !webTranslationKeys(t)[loopNotDoneNote] || !webTranslationKeys(t)[loopContractFallbackNotice] {
		t.Errorf("missing translations of %q or %q", loopNotDoneNote, loopContractFallbackNotice)
	}
}

func TestReadOnlyLoopRunsSucceedWithoutPublishing(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	cases := []struct {
		name, instructions string
		replies            []string
		output             string
	}{
		{"每日状态检查", "检查项目是否有新的状态更新，汇总今天的事项；无变化时输出今日无变动", []string{`tool:get_status_updates {"type":"project","limit":5}`, `tool:finish_run {"status":"nothing_to_do","summary":"今日无变动"}`}, "今日无变动"},
		{"Daily status check", "Check whether projects have new status updates and summarize today's items; if nothing changed, reply No changes today.", []string{`tool:list_projects {"limit":5}`, `tool:finish_run {"status":"done","summary":"Two projects posted updates today: both on track."}`}, "both on track"},
		{"Update digest", "Read the existing project status updates and summarize the risks.", []string{`tool:get_status_updates {"type":"project","limit":5}`, `tool:finish_run {"status":"done","summary":"No risks reported."}`}, "No risks reported."},
		{"只读周报汇总", "汇总本周的项目进展，不要发布状态更新。", []string{`tool:finish_run {"status":"done","summary":"本周三个项目推进正常。"}`}, "本周三个项目推进正常。"},
	}
	for _, item := range cases {
		provider.script(item.replies...)
		loop, started := startTestRun(t, handler, item.name, item.instructions, nil)
		run := waitForRun(t, handler, loop.ID, started.ID)
		if run.Status != "completed" || run.FailureReason != "" || len(run.ExpectedOutputs) != 0 || !strings.Contains(run.Output, item.output) {
			t.Errorf("%s: run = status %s reason %s error %q expected %v output %q", item.name, run.Status, run.FailureReason, run.Error, run.ExpectedOutputs, run.Output)
		}
		if writes := loopRunWrites(run); len(writes) != 0 {
			t.Errorf("%s: read-only run called write tools %v", item.name, writes)
		}
		prompt := loopRunPrompt(t, provider, 0)
		if strings.Contains(prompt, loopPublishInstruction) || strings.Contains(prompt, "save_status_update with") || !strings.Contains(prompt, "reads and reports") {
			t.Errorf("%s: read-only prompt:\n%s", item.name, prompt)
		}
	}
}

// Legacy expectedOutputs are a person's outputs: they win over the model's.
func TestExplicitLoopOutputsTakePrecedence(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	projectID := firstProjectID(t, handler)

	provider.script(`tool:finish_run {"status":"done","summary":"Checked."}`)
	loop, started := startTestRun(t, handler, "Status check", "检查项目是否有新的状态更新，汇总今天的事项", map[string]any{"expectedOutputs": []string{"statusUpdate"}})
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "needs_review" || run.FailureReason != "no_output" || run.OutputContract == nil || run.OutputContract.Source != loopContractPerson {
		t.Fatalf("explicit output without a write = %+v", run)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, loopPublishInstruction) || strings.Contains(prompt, "reads and reports") {
		t.Fatalf("explicit output prompt:\n%s", prompt)
	}

	provider.script(`tool:save_status_update {"type":"project","project":"`+projectID+`","body":"On track.","health":"onTrack"}`, `tool:finish_run {"status":"done","summary":"Posted."}`)
	if run = waitForRun(t, handler, loop.ID, startNextRun(t, handler, loop.ID).ID); run.Status != "completed" || run.Produced["statusUpdate"] != 1 {
		t.Fatalf("explicit output with a write = %+v", run)
	}

	provider.script(`tool:save_status_update {"type":"project","project":"missing-project","body":"x"}`, `tool:finish_run {"status":"done","summary":"Posted."}`)
	if run = waitForRun(t, handler, loop.ID, startNextRun(t, handler, loop.ID).ID); run.Status != "needs_review" || run.FailureReason != "tool_error" || run.Produced["statusUpdate"] != 0 {
		t.Fatalf("explicit output whose write failed = %+v", run)
	}

	// The model's contract requires the write.
	provider.script(`tool:finish_run {"status":"done","summary":"Done."}`)
	publish, started := startTestRun(t, handler, "周报", "每周五为每个项目发布状态更新", nil)
	if run = waitForRun(t, handler, publish.ID, started.ID); run.Status != "needs_review" || run.FailureReason != "no_output" || strings.Join(run.ExpectedOutputs, ",") != "statusUpdate" {
		t.Fatalf("publish loop without a write = %+v", run)
	}

	// Explicit "none": nothing required.
	provider.script(`tool:finish_run {"status":"nothing_to_do","summary":"Nothing changed."}`)
	quiet, started := startTestRun(t, handler, "Weekly update when needed", "Post a weekly update when something changed.", map[string]any{"expectedOutputs": []string{"none"}})
	if run = waitForRun(t, handler, quiet.ID, started.ID); run.Status != "completed" || len(run.ExpectedOutputs) != 0 {
		t.Fatalf("loop expecting nothing = %+v", run)
	}
	if prompt := loopRunPrompt(t, provider, 0); strings.Contains(prompt, loopPublishInstruction) {
		t.Fatalf("loop expecting nothing got a publish instruction:\n%s", prompt)
	}
}

// The reply-only loop: the model tries to create a summary issue and then
// reports done. The create is refused, no issue is made or counted, and the
// run completes with its text reply and the refused-write notice.
func TestLoopReplyOnlyRunCreatesNothing(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issues := func() int {
		t.Helper()
		page := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/issue-records?limit=200", nil, http.StatusOK)
		items, _ := page["items"].([]any)
		return len(items)
	}
	before := issues()
	provider.script(
		`tool:save_issue {"title":"今日工作总结","team":"`+bootstrap.Teams[0].ID+`","description":"今日完成 3 项"}`,
		`tool:finish_run {"status":"done","summary":"今日完成 3 项，新增 2 个事项。","done":["创建了今日总结事项"]}`,
	)
	loop, started := startTestRun(t, handler, "团队日报", loopReplyOnlyDigestZH, nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	call := loopRunCall(run, "save_issue", 0)
	if call.Status != "blocked" || !strings.HasPrefix(call.Error, loopProhibitedCallPrefix) {
		t.Fatalf("save_issue in a reply-only loop = %s %q", call.Status, call.Error)
	}
	if input := provider.lastInput(); !strings.Contains(input, loopProhibitedCallPrefix) {
		t.Fatalf("the model was not told the create was refused: %s", input)
	}
	if got := issues(); got != before {
		t.Fatalf("issues %d after the run, want %d", got, before)
	}
	if run.Status != "completed" || len(run.ExpectedOutputs) != 0 || run.Produced["issue"] != 0 || run.Produced["change"] != 0 {
		t.Fatalf("run = %s %s %q expected %v produced %v", run.Status, run.FailureReason, run.Error, run.ExpectedOutputs, run.Produced)
	}
	if !strings.Contains(run.Output, "今日完成 3 项") || !slices.Contains(run.Notices, loopRefusedWriteNotice) {
		t.Fatalf("output %q notices %v", run.Output, run.Notices)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, "This loop reads and reports") || !strings.Contains(prompt, "forbid every change") {
		t.Fatalf("prompt:\n%s", prompt)
	}
}

// Extraction through the model: an invalid contract is sent back once with
// its problems; the valid one is cached by instructions hash, so runs do not
// read it again until the text changes.
func TestLoopContractExtractionValidatesRetriesAndCaches(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	srv.loopContractLive = true
	provider.contracts = []string{
		`tool:record_output_contract {"readOnly":true,"outputs":[{"kind":"document","operation":"create","required":true,"target":{"mode":"new","titleTemplate":"每日总结-{date}"}}],"always":false,"skipConditions":[]}`,
		"tool:record_output_contract " + loopDailyDocumentContract,
	}
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "每日总结", "instructions": loopDailyDocumentZH, "status": "published"}, http.StatusCreated)
	analyzed := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/output-contract", map[string]any{}, http.StatusOK)
	contract := analyzed.OutputContract
	if contract == nil || contract.Source != loopContractModel || contract.ReadOnly || len(contract.Outputs) != 1 || contract.Outputs[0].Target.TitleTemplate != "每日总结-{date}" || contract.InstructionsHash != loopContractHashOf(analyzed) || contract.Stale {
		t.Fatalf("extracted contract = %+v", contract)
	}
	provider.mu.Lock()
	extractions := slices.Clone(provider.extractions)
	provider.mu.Unlock()
	if len(extractions) != 2 || !strings.Contains(extractions[1], "readOnly is true but some outputs create") || !strings.Contains(extractions[0], "Instructions:") {
		t.Fatalf("extraction requests = %d: %v", len(extractions), extractions)
	}
	// Runs use the cached contract; analyzing again reads nothing.
	provider.script(`tool:finish_run {"status":"done","summary":"x"}`)
	run := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	waitForRun(t, handler, loop.ID, run.ID)
	requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/output-contract", nil, http.StatusOK)
	provider.mu.Lock()
	count := len(provider.extractions)
	provider.mu.Unlock()
	if count != 2 {
		t.Fatalf("%d extraction requests, want the cached contract", count)
	}
	// New instructions are read again.
	changed := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"instructions": "Summarize open bugs."}, http.StatusOK)
	if changed.OutputContract == nil || !changed.OutputContract.Stale {
		t.Fatalf("contract after the instructions changed = %+v", changed.OutputContract)
	}
	again := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/output-contract", nil, http.StatusOK)
	if again.OutputContract == nil || !again.OutputContract.ReadOnly || again.OutputContract.Stale {
		t.Fatalf("contract for the new instructions = %+v", again.OutputContract)
	}
	// The version published with the first instructions keeps its contract.
	versions := requestJSON[[]domain.LoopVersion](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/versions", nil, http.StatusOK)
	first := versions[len(versions)-1]
	if first.OutputContract == nil || first.OutputContract.Outputs[0].Operation != loopOpCreate {
		t.Fatalf("first version contract = %+v", first.OutputContract)
	}
}

// Two invalid answers: the loop falls back to the report-only contract with
// a visible reason, so its runs change nothing until a person sets outputs.
// A person's outputs are authoritative; resetting them reads the model's.
func TestLoopContractFallbackAndPersonOverrides(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	srv.loopContractLive = true
	provider.contracts = []string{"I think it creates a document.", `tool:record_output_contract {"outputs":[{"kind":"page","operation":"create"}]}`}
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "每日总结", "instructions": loopDailyDocumentZH, "status": "published"}, http.StatusCreated)
	analyzed := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/output-contract", nil, http.StatusOK)
	if contract := analyzed.OutputContract; contract == nil || contract.Source != loopContractFallback || !contract.ReadOnly || !strings.HasPrefix(contract.Error, loopContractFallbackNotice) {
		t.Fatalf("fallback contract = %+v", contract)
	}
	team := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0]
	provider.script(`tool:save_document {"title":"每日总结","content":"x","team":"`+team.ID+`"}`, `tool:finish_run {"status":"done","summary":"x"}`)
	run := waitForRun(t, handler, loop.ID, requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted).ID)
	if call := loopRunCall(run, "save_document", 0); call.Status != "blocked" || run.OutputContract.Source != loopContractFallback {
		t.Fatalf("fallback run write = %s %q", call.Status, call.Error)
	}

	// A person sets the outputs: a dated document in the team.
	person := map[string]any{"readOnly": false, "outputs": []any{map[string]any{"kind": "document", "operation": "create", "required": true, "target": map[string]any{"mode": "new", "titleTemplate": "Daily {date}", "teamId": team.ID}, "cardinality": "onePerRun"}}}
	updated := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"outputContract": person}, http.StatusOK)
	if contract := updated.OutputContract; contract == nil || contract.Source != loopContractPerson || contract.Outputs[0].Target.TeamID != team.ID {
		t.Fatalf("person contract = %+v", contract)
	}
	extractions := func() int {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return len(provider.extractions)
	}
	before := extractions()
	provider.script(`tool:save_document {"title":"whatever","content":"x"}`, `tool:finish_run {"status":"done","summary":"x"}`)
	run = waitForRun(t, handler, loop.ID, startNextRun(t, handler, loop.ID).ID)
	want := "Daily " + time.Now().UTC().Format("2006-01-02")
	if call := loopRunCall(run, "save_document", 0); call.Status != "completed" || call.Document.Title != want || !slices.Contains(call.Document.TeamIDs, team.ID) || run.Status != "completed" {
		t.Fatalf("person contract run = %s %q call %+v %+v", run.Status, run.Error, call, call.Document)
	}
	// Changing the instructions keeps the person's outputs (marked stale), without a model call.
	changed := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"instructions": loopDailyDocumentZH + "\n内容要简短。"}, http.StatusOK)
	if changed.OutputContract == nil || changed.OutputContract.Source != loopContractPerson || !changed.OutputContract.Stale || extractions() != before {
		t.Fatalf("person contract after an edit = %+v (%d extractions, %d before)", changed.OutputContract, extractions(), before)
	}
	// An invalid person contract is refused.
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"outputContract": map[string]any{"outputs": []any{map[string]any{"kind": "issue", "operation": "append"}}}}, http.StatusBadRequest)
	// Reset: the model reads it again.
	provider.mu.Lock()
	provider.contracts = []string{"tool:record_output_contract " + loopDailyDocumentContract}
	provider.mu.Unlock()
	requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"outputContract": nil}, http.StatusOK)
	reset := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/output-contract", nil, http.StatusOK)
	if reset.OutputContract == nil || reset.OutputContract.Source != loopContractModel || reset.OutputContract.Outputs[0].Target.TitleTemplate != "每日总结-{date}" {
		t.Fatalf("contract after reset = %+v", reset.OutputContract)
	}
}

// Without a configured model the contract is the fallback.
func TestLoopContractWithoutModel(t *testing.T) {
	srv, handler := newReliableLoopServer(t, &fakeLoopProvider{})
	srv.agent.Enabled = false
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "x", "instructions": "Create an issue for each failing check."}, http.StatusCreated)
	analyzed := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/output-contract", nil, http.StatusOK)
	if analyzed.OutputContract == nil || analyzed.OutputContract.Source != loopContractFallback || !strings.Contains(analyzed.OutputContract.Error, "no model") {
		t.Fatalf("contract without a model = %+v", analyzed.OutputContract)
	}
}

func TestNormalizeLoopContract(t *testing.T) {
	for _, item := range []struct {
		contract string
		problem  string
	}{
		{`{"outputs":[{"kind":"page","operation":"create"}]}`, "kind must be one of"},
		{`{"outputs":[{"kind":"issue","operation":"append"}]}`, "append is only for documents"},
		{`{"outputs":[{"kind":"document","operation":"rewrite"}]}`, "operation must be one of"},
		{`{"outputs":[{"kind":"document","operation":"create","target":{"mode":"existing"}}]}`, "a created document is new"},
		{`{"readOnly":true,"outputs":[{"kind":"comment","operation":"create","required":true}]}`, "readOnly is true"},
		{`{"outputs":[{"kind":"comment","operation":"create","cardinality":"daily"}]}`, "cardinality must be one of"},
	} {
		var contract domain.LoopOutputContract
		if err := json.Unmarshal([]byte(item.contract), &contract); err != nil {
			t.Fatal(err)
		}
		if err := normalizeLoopContract(&contract); err == nil || !strings.Contains(err.Error(), item.problem) {
			t.Errorf("%s: %v, want %q", item.contract, err, item.problem)
		}
	}
	contract := domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{{Kind: "comment", Operation: loopOpForbidden, Required: true, Target: domain.LoopContractTarget{TitleTemplate: "x"}}}}
	if err := normalizeLoopContract(&contract); err != nil || contract.Outputs[0].Required || contract.Outputs[0].Target.Mode != "any" || contract.Outputs[0].Target.TitleTemplate != "" || contract.Outputs[0].Cardinality != "any" {
		t.Fatalf("normalized = %+v %v", contract, err)
	}
	if _, err := parseLoopContractArguments("```json\n" + loopDailyDocumentContract + "\n```"); err != nil {
		t.Fatalf("a fenced JSON answer: %v", err)
	}
	if _, err := parseLoopContractArguments(`{"outputs":[]}`); err == nil || !strings.Contains(err.Error(), "missing required fields") {
		t.Fatalf("missing fields: %v", err)
	}
}

func TestRenderLoopTitle(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	for template, want := range map[string]string{
		"每日总结-{date}":        "每日总结-2026-10-28",
		"每日总结-YYYY-MM-DD":    "每日总结-2026-10-28",
		"Week {week} notes":  "Week 2026-W44 notes",
		"月报 {month}":         "月报 2026-10",
		"Report YYYY-MM":     "Report 2026-10",
		"{year} review":      "2026 review",
		"Standup YYYYMMDD":   "Standup 20261028",
		"Notes for {topic}":  "Notes for {topic}",
		"Plain title":        "Plain title",
		"Weekly {week}":      "Weekly 2026-W44",
		"Daily {date} {day}": "Daily 2026-10-28 2026-10-28",
	} {
		if got := renderLoopTitle(template, at, ""); got != want {
			t.Errorf("renderLoopTitle(%q) = %q, want %q", template, got, want)
		}
	}
	if got := renderLoopTitle("周报 {week}", at, "2026-W43"); got != "周报 2026-W43" {
		t.Errorf("weekly occurrence title = %q", got)
	}
	if !loopTitleMatches("Notes for {topic}", "Notes for login") || loopTitleMatches("Notes for {topic}", "Other") || !loopTitleMatches("每日总结-2026-10-28", "每日总结-2026-10-28") {
		t.Error("loopTitleMatches")
	}
}
