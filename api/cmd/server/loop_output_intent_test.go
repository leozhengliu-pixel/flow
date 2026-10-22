package main

import (
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// Read, check and summarise wording names outputs without asking for them;
// only explicit create/publish wording (or a bare title) expects a write.
func TestInferLoopOutputsReadVersusWrite(t *testing.T) {
	cases := map[string]string{
		// Read-only reports.
		"检查项目是否有新的状态更新，汇总今天的事项；无变化时输出今日无变动":                                                                                       "",
		"Check whether projects have new status updates and summarize today's items; if nothing changed, reply No changes today.": "",
		"阅读现有的项目状态更新并汇总风险":                                                                                                        "",
		"Read the latest project updates and list the risks.":                                                                     "",
		"查看今天的评论，汇总需要跟进的问题":                                                                                                       "",
		"Review new comments and summarize what needs a follow-up.":                                                               "",
		// Explicitly no writes.
		"汇总本周进展，不要发布状态更新":                                             "",
		"Summarize this week's status updates. Do not post anything.": "",
		"Read-only: list the project updates from today.":             "",
		// Explicit writes.
		"每周五为每个项目发布状态更新":                                        "statusUpdate",
		"Post a status update on each project":                  "statusUpdate",
		"Check the projects, then post a weekly update on each": "statusUpdate",
		"检查新 bug，并为每个缺少复现步骤的 bug 留言":                            "comment",
		"Create an issue for every failing check":               "issue",
		"Flow 项目周更新":                                            "statusUpdate",
	}
	for text, want := range cases {
		if got := strings.Join(inferLoopOutputs(text), ","); got != want {
			t.Errorf("inferLoopOutputs(%q) = %q, want %q", text, got, want)
		}
	}
}

// loopRunPrompt is the system prompt of the provider's first recorded request.
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
		provider.mu.Lock()
		provider.replies, provider.inputs, provider.tools = item.replies, nil, nil
		provider.mu.Unlock()
		loop, started := startTestRun(t, handler, item.name, item.instructions, nil)
		run := waitForRun(t, handler, loop.ID, started.ID)
		if run.Status != "completed" || run.FailureReason != "" || len(run.ExpectedOutputs) != 0 || !strings.Contains(run.Output, item.output) {
			t.Errorf("%s: run = status %s reason %s error %q expected %v output %q", item.name, run.Status, run.FailureReason, run.Error, run.ExpectedOutputs, run.Output)
		}
		if writes := loopRunWrites(run); len(writes) != 0 {
			t.Errorf("%s: read-only run called write tools %v", item.name, writes)
		}
		prompt := loopRunPrompt(t, provider, 0)
		if strings.Contains(prompt, loopPublishInstruction) || strings.Contains(prompt, "save_status_update with") {
			t.Errorf("%s: read-only run got a publish instruction:\n%s", item.name, prompt)
		}
		if !strings.Contains(prompt, "reads and reports") {
			t.Errorf("%s: read-only run lacks the read-only guidance:\n%s", item.name, prompt)
		}
	}
}

func TestExplicitLoopOutputsTakePrecedence(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	projectID := firstProjectID(t, handler)

	// Explicit outputs override read-only wording: the run must publish.
	provider.replies = []string{`tool:finish_run {"status":"done","summary":"Checked."}`}
	loop, started := startTestRun(t, handler, "Status check", "检查项目是否有新的状态更新，汇总今天的事项", map[string]any{"expectedOutputs": []string{"statusUpdate"}})
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "needs_review" || run.FailureReason != "no_output" {
		t.Fatalf("explicit output without a write = %+v", run)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, loopPublishInstruction) || strings.Contains(prompt, "reads and reports") {
		t.Fatalf("explicit output prompt:\n%s", prompt)
	}

	// A genuine successful write is verified.
	provider.mu.Lock()
	provider.replies, provider.inputs = []string{
		`tool:save_status_update {"type":"project","project":"` + projectID + `","body":"On track.","health":"onTrack"}`,
		`tool:finish_run {"status":"done","summary":"Posted."}`,
	}, nil
	provider.mu.Unlock()
	started = startNextRun(t, handler, loop.ID)
	if run = waitForRun(t, handler, loop.ID, started.ID); run.Status != "completed" || run.Produced["statusUpdate"] != 1 {
		t.Fatalf("explicit output with a write = %+v", run)
	}

	// A write that failed is not counted.
	provider.mu.Lock()
	provider.replies = []string{`tool:save_status_update {"type":"project","project":"missing-project","body":"x"}`, `tool:finish_run {"status":"done","summary":"Posted."}`}
	provider.mu.Unlock()
	started = startNextRun(t, handler, loop.ID)
	if run = waitForRun(t, handler, loop.ID, started.ID); run.Status != "needs_review" || run.FailureReason != "tool_error" || run.Produced["statusUpdate"] != 0 {
		t.Fatalf("explicit output whose write failed = %+v", run)
	}

	// Explicit publish wording still requires the write.
	provider.mu.Lock()
	provider.replies, provider.inputs = []string{`tool:finish_run {"status":"done","summary":"Done."}`}, nil
	provider.mu.Unlock()
	publish, started := startTestRun(t, handler, "周报", "每周五为每个项目发布状态更新", nil)
	if run = waitForRun(t, handler, publish.ID, started.ID); run.Status != "needs_review" || run.FailureReason != "no_output" || strings.Join(run.ExpectedOutputs, ",") != "statusUpdate" {
		t.Fatalf("publish loop without a write = %+v", run)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, loopPublishInstruction) {
		t.Fatalf("publish loop prompt lacks the publish instruction:\n%s", prompt)
	}

	// Explicit "none" overrides publish wording: no instruction, no check.
	provider.mu.Lock()
	provider.replies, provider.inputs = []string{`tool:finish_run {"status":"nothing_to_do","summary":"Nothing changed."}`}, nil
	provider.mu.Unlock()
	quiet, started := startTestRun(t, handler, "Weekly update when needed", "Post a weekly update when something changed.", map[string]any{"expectedOutputs": []string{"none"}})
	if run = waitForRun(t, handler, quiet.ID, started.ID); run.Status != "completed" || len(run.ExpectedOutputs) != 0 {
		t.Fatalf("loop expecting nothing = %+v", run)
	}
	if prompt := loopRunPrompt(t, provider, 0); strings.Contains(prompt, loopPublishInstruction) {
		t.Fatalf("loop expecting nothing got a publish instruction:\n%s", prompt)
	}
}

func TestPlainReportingLoopsAreReadOnly(t *testing.T) {
	for _, text := range []string{
		"统计每个团队的未完成事项数量并汇总",
		"Count open issues per team and summarize.",
	} {
		if got := inferLoopOutputIntent(text); !got.ReadOnly || len(got.Outputs) != 0 {
			t.Errorf("inferLoopOutputIntent(%q) = %+v, want read-only", text, got)
		}
	}
	for _, text := range []string{"统计未完成事项并发布状态更新", "Triage new issues: set priority", "Count open issues and create a document"} {
		if got := inferLoopOutputIntent(text); got.ReadOnly {
			t.Errorf("inferLoopOutputIntent(%q) must not be read-only", text)
		}
	}
}
