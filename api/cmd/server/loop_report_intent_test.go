package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// Report sections ("2. 今日新增事项：列出…", "New issues today: list…") describe
// what the report covers, not something to create; real requests to create
// an issue still ask for one.
func TestInferLoopOutputReportSections(t *testing.T) {
	for _, item := range []loopIntentCase{
		// The report.
		{text: "请总结团队今天的工作。\n2. 今日新增事项：简要列出今天新创建的事项及创建者。\n将总结结果追加到已有团队文档中，不要在任何事项、项目或其他资源上发布评论。", outputs: "document", operations: "document:append", prohibited: "comment"},
		{text: "1. 今日完成事项：列出已完成的事项。\n2. 新增事项：列出今天新建的事项。\n3. 风险：汇总阻塞。\n将日报追加到团队文档。", outputs: "document", operations: "document:append"},
		{text: "- 本周新增事项：统计数量并按团队分组\n- 新建事项：列出创建者\n把总结追加到已有文档", outputs: "document", operations: "document:append"},
		{text: "汇总今日新增事项和新建事项的数量。", readOnly: true},
		{text: "列出今天新增的事项及创建者。", readOnly: true},
		{text: "New issues today: list them with their creators.\nAppend the report to the existing team doc.", outputs: "document", operations: "document:append"},
		{text: "Summarize issues created today and who created them. Append the summary to the team doc.", outputs: "document", operations: "document:append"},
		{text: "1. Issues created today: count them per team.\n2. Append the digest to the team doc.", outputs: "document", operations: "document:append"},
		{text: "List who filed issues today and append the list to the team doc.", outputs: "document", operations: "document:append"},
		// Real create requests.
		{text: "新建一个事项跟踪登录失败的问题。", outputs: "issue", operations: "issue:create"},
		{text: "如果发现阻塞，创建事项并指派给负责人。", outputs: "issue", operations: "issue:create"},
		{text: "检查今日的错误日志，为每个新错误新增一个事项。", outputs: "issue", operations: "issue:create"},
		{text: "Create an issue for each failing check.", outputs: "issue", operations: "issue:create"},
		{text: "Review the error log and open a new issue for every new error.", outputs: "issue", operations: "issue:create"},
		{text: "新增事项：标题为“修复登录”，指派给我。", outputs: "issue", operations: "issue:create"},
	} {
		checkLoopIntent(t, item)
	}
}

// loopReplyOnlyDigestZH is the report of issue 4: summarise, reply with the
// text, write nothing anywhere.
const loopReplyOnlyDigestZH = "请总结团队今天的工作。\n2. 今日新增事项：简要列出今天新创建的事项及创建者。\n只回复总结文本即可，不要在任何事项、项目、文档或其他资源上发布评论或写入内容。"

// A loop that says to reply only and write nothing is read-only: it asks for
// no output and refuses every write, whatever else its text names.
func TestInferLoopReplyOnlyRefusesEveryWrite(t *testing.T) {
	const all = "statusUpdate,issue,comment,document"
	for _, item := range []loopIntentCase{
		{text: loopReplyOnlyDigestZH, prohibited: all, readOnly: true},
		{text: "汇总今天关闭的事项。只回复总结文本即可。", prohibited: all, readOnly: true},
		{text: "汇总今日工作，不要在任何事项、项目、文档或其他资源上发布评论或写入内容。", prohibited: all, readOnly: true},
		{text: "Summarize today's work. Reply only; don't post comments or write anything to any issue, project, document or other resource.", prohibited: all, readOnly: true},
		{text: "List today's new issues and just reply with the summary.", prohibited: all, readOnly: true},
		// A blanket refusal wins over an output another clause names.
		{text: "汇总今日工作并追加到团队文档。只回复总结文本即可，不要写入任何内容。", prohibited: all, readOnly: true},
		// Exceptions and "only X" keep their output.
		{text: "汇总今日进展并写入团队文档，除此之外不要做任何修改。", outputs: "document", operations: "document:update", prohibited: "statusUpdate,issue,comment"},
		{text: "除追加团队文档外，不要发布评论或创建事项。", outputs: "document", operations: "document:append", prohibited: "issue,comment"},
		// Replying to people is a comment, not a reply-only run.
		{text: "Reply to each new comment that mentions the team.", outputs: "comment", operations: "comment:write"},
	} {
		checkLoopIntent(t, item)
	}
	if intent := inferLoopOutputIntent(loopReplyOnlyDigestZH); !intent.NoWrites {
		t.Fatalf("reply-only intent = %+v, want NoWrites", intent)
	}
}

// The report end to end: the model tries to create a summary issue and then
// reports done. The create is refused with a tool error, no issue is made or
// counted, and the run completes with its text reply and a notice that the
// summary may list the refused change.
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
	if !webTranslationKeys(t)[loopRefusedWriteNotice] {
		t.Errorf("missing translation of the notice %q", loopRefusedWriteNotice)
	}
}
