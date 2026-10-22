package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A follow-up works from the saved record of the run: its status, error,
// report, outputs and tool evidence, and the earlier follow-ups. A status
// question is answered from it without tools.
func TestLoopReplyAnswersFromTheSavedRunRecord(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	// The run reads, answers in plain text twice and posts nothing: it has an
	// output and a no_output failure.
	provider.script(`tool:list_projects {"limit":5}`, "Everything looks fine.", "Everything looks fine.")
	loop, started := startTestRun(t, handler, "Weekly update", "Post a status update on each active project.", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "needs_review" || run.FailureReason != "no_output" || run.Output == "" {
		t.Fatalf("run = %+v", run)
	}
	path := "/api/loops/" + loop.ID + "/runs/" + started.ID + "/replies"

	// A pure status question: one model call, no tools offered or called.
	provider.script("没有完成：这次运行没有发布任何状态更新。")
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, path, map[string]any{"body": "完成了吗？"}, http.StatusAccepted)
	after := waitForLoopReply(t, handler, loop.ID, started.ID, 1)
	reply := after.Replies[0]
	provider.mu.Lock()
	tools, input := provider.tools[0], provider.inputs[0]
	provider.mu.Unlock()
	if provider.calls() != 1 || len(tools) != 0 || len(reply.ToolCalls) != 0 || reply.Status != "completed" || !strings.Contains(reply.Output, "没有完成") {
		t.Fatalf("status question: calls %d tools %v reply %+v", provider.calls(), tools, reply)
	}
	if after.Status != "needs_review" || after.FailureReason != "no_output" || len(after.ToolCalls) != len(run.ToolCalls) {
		t.Fatalf("the status question changed the run: %+v", after)
	}
	for _, want := range []string{
		"Status: needs_review (reason: no_output)", "No output produced", "Required outputs: statusUpdate; successful changes made: nothing",
		"Listed projects", "Final report: none", "no tools are available", "Everything looks fine.", "[The run ended needs_review (no_output)",
	} {
		if !strings.Contains(input, want) {
			t.Errorf("status question prompt lacks %q", want)
		}
	}

	// A failing follow-up keeps its failure in the record.
	provider.script("status:400")
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, path, map[string]any{"body": "Why did it fail?"}, http.StatusAccepted)
	if failed := waitForLoopReply(t, handler, loop.ID, started.ID, 2).Replies[1]; failed.Status != "failed" || failed.FailureReason != "provider_error" {
		t.Fatalf("failing follow-up = %+v", failed)
	}
	provider.script("The last follow-up could not be answered: the model request failed.")
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, path, map[string]any{"body": "Tell me what the last follow-up did and list the projects again"}, http.StatusAccepted)
	waitForLoopReply(t, handler, loop.ID, started.ID, 3)
	input = provider.lastInput()
	for _, want := range []string{`Earlier follow-up \"完成了吗？\": completed`, `Earlier follow-up \"Why did it fail?\": failed (reason: provider_error)`, "call tools only for information the record does not have"} {
		if !strings.Contains(input, want) {
			t.Errorf("follow-up prompt lacks %q: %s", want, input)
		}
	}
	provider.mu.Lock()
	offered := strings.Join(provider.tools[0], ",")
	provider.mu.Unlock()
	if !strings.Contains(offered, "list_projects") {
		t.Errorf("an ordinary follow-up lost its tools: %s", offered)
	}
}

// The saved context stays bounded however long the run was.
func TestLoopReplyContextIsBounded(t *testing.T) {
	run := domain.LoopRun{Status: "completed", Output: strings.Repeat("x", 50000)}
	for index := range 200 {
		run.ToolCalls = append(run.ToolCalls, domain.LoopRunToolCall{Name: "list_issues", Label: "Listed issues", Args: "query " + strconv.Itoa(index), Status: "completed", Error: strings.Repeat("e", 2000)})
	}
	for index := range 30 {
		run.Replies = append(run.Replies, domain.LoopRunReply{ID: "r" + strconv.Itoa(index), Body: strings.Repeat("b", 3000), Status: "completed", ToolCalls: run.ToolCalls})
	}
	prompt := loopReplyContextPrompt(run, "new", loopReplyStatus)
	if len(prompt) > 64<<10 || !strings.Contains(prompt, "(180 earlier calls omitted)") || !strings.Contains(prompt, "(25 earlier follow-ups omitted)") || strings.Contains(prompt, strings.Repeat("x", 100)) {
		t.Fatalf("context is %d bytes", len(prompt))
	}
}

func TestLoopReplyIntent(t *testing.T) {
	cases := map[string]string{
		"完成了吗？": loopReplyStatus, "做完了吗": loopReplyStatus, "为什么失败了？": loopReplyStatus,
		"Is it done?": loopReplyStatus, "Did it work?": loopReplyStatus, "What's the status?": loopReplyStatus,
		"继续": loopReplyContinue, "把剩下的做完": loopReplyContinue, "Continue with the rest": loopReplyContinue, "Please try again": loopReplyContinue,
		"How many bugs were there?": loopReplyOther, "Also comment on FLOW-2": loopReplyOther,
	}
	for body, want := range cases {
		if got := loopReplyIntent(body); got != want {
			t.Errorf("loopReplyIntent(%q) = %s, want %s", body, got, want)
		}
	}
}

// "Continue" resumes the unfinished steps and never repeats a write that
// already succeeded.
func TestLoopReplyContinueDoesNotRepeatWrites(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Projects) < 2 {
		t.Fatal("fixture needs two projects")
	}
	first, second := bootstrap.Projects[0].ID, bootstrap.Projects[1].ID
	update := func(project string) string {
		return `tool:save_status_update {"type":"project","project":"` + project + `","body":"On track.","health":"onTrack"}`
	}
	provider.script(update(first), `tool:finish_run {"status":"incomplete","summary":"Posted one of two updates.","notDone":["Second project: ran out of time"]}`)
	loop, started := startTestRun(t, handler, "Weekly update", "Post a status update on each active project.", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "needs_review" || run.FailureReason != "incomplete" || run.Produced["statusUpdate"] != 1 {
		t.Fatalf("run = %+v", run)
	}

	provider.script(update(first), update(second), `tool:finish_run {"status":"done","summary":"Posted the second update."}`)
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+started.ID+"/replies", map[string]any{"body": "继续"}, http.StatusAccepted)
	after := waitForLoopReply(t, handler, loop.ID, started.ID, 1)
	reply := after.Replies[0]
	if provider.calls() != 3 || reply.Status != "completed" || len(reply.ToolCalls) != 2 || reply.ToolCalls[0].Status != "blocked" || !strings.Contains(reply.ToolCalls[0].Error, "already succeeded") || reply.ToolCalls[1].Status != "completed" {
		t.Fatalf("continue: calls %d reply %+v", provider.calls(), reply)
	}
	provider.mu.Lock()
	firstInput := provider.inputs[0]
	provider.mu.Unlock()
	for _, want := range []string{"Changes that already succeeded (never repeat them)", "Posted status update (project " + first, "Not done: Second project: ran out of time", "asks you to continue"} {
		if !strings.Contains(firstInput, want) {
			t.Errorf("continue prompt lacks %q", want)
		}
	}
	// Each project got exactly one update.
	for _, project := range []string{first, second} {
		updates := requestJSON[[]domain.ProjectUpdate](t, handler, http.MethodGet, "/api/projects/"+project+"/updates", nil, http.StatusOK)
		posted := 0
		for _, item := range updates {
			if item.Body == "On track." {
				posted++
			}
		}
		if posted != 1 {
			t.Errorf("project %s has %d updates from the loop, want 1", project, posted)
		}
	}
}

var countResultPattern = regexp.MustCompile(`\\"count\\":(\d+)`)

// Loop runs can count issues in one call, within the teams the loop may use;
// other agents' tool sets are unchanged.
func TestLoopRunCountsIssuesWithinItsTeams(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	home := bootstrap.Teams[0]
	other := domain.Team{ID: "team_other", Key: "OTH", Name: "Other"}
	if err := srv.store.MutateWorkspace(context.Background(), "test-workspace", "test.teams", other.ID, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, other)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Other one", "Other two"} {
		requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": other.ID}, http.StatusCreated)
	}
	count := func(access string) int {
		t.Helper()
		provider.script(`tool:count_issues {}`, `tool:finish_run {"status":"done","summary":"Counted."}`)
		loop, started := startTestRun(t, handler, "Count "+access, "Count the issues.", map[string]any{"level": "team", "teamId": home.ID, "teamAccess": access})
		run := waitForRun(t, handler, loop.ID, started.ID)
		if run.Status != "completed" || len(run.ToolCalls) != 1 || run.ToolCalls[0].Status != "completed" || run.ToolCalls[0].Label != "Counted issues" {
			t.Fatalf("count run = %+v", run)
		}
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if !strings.Contains(strings.Join(provider.tools[0], ","), "count_issues") {
			t.Fatalf("count_issues not offered to the loop: %v", provider.tools[0])
		}
		match := countResultPattern.FindStringSubmatch(provider.inputs[1])
		if match == nil {
			t.Fatalf("no count in the tool result: %s", provider.inputs[1])
		}
		value, _ := strconv.Atoi(match[1])
		return value
	}
	scoped, public := count("selected"), count("allPublic")
	if public-scoped != 2 {
		t.Fatalf("selected-team count %d, all public teams %d: want the other team's 2 issues only in the second", scoped, public)
	}
	// Other agents do not get MCP-only tools.
	definitions, _ := srv.agentToolDefinitions()
	for _, tool := range definitions {
		if tool.Name == "count_issues" {
			t.Fatal("count_issues leaked into the in-process agent tool set")
		}
	}
	if _, err := srv.executeAgentTool(agentTestRequest(t), domain.Bootstrap{Viewer: domain.User{ID: "usr_admin"}, ViewerRole: "admin"}, domain.AgentToolCall{ID: "c1", Name: "count_issues", Arguments: json.RawMessage(`{}`)}); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("count_issues ran outside a loop: %v", err)
	}
}

func agentTestRequest(t *testing.T) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://flow.internal/api/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Workspace-Key", "test-workspace")
	return request.WithContext(context.WithValue(request.Context(), workspaceKeyContextKey{}, "test-workspace"))
}

// A run's time is split into setup, model wait and tool time.
func TestLoopRunRecordsTimingSplit(t *testing.T) {
	provider := &fakeLoopProvider{wait: func(int) { time.Sleep(60 * time.Millisecond) }}
	srv, handler := newReliableLoopServer(t, provider)
	provider.script(`tool:list_projects {"limit":5}`, `tool:finish_run {"status":"done","summary":"Listed."}`)
	loop, started := startTestRun(t, handler, "Digest", "Summarize projects.", nil)
	waitForRun(t, handler, loop.ID, started.ID)
	events, err := srv.store.LoopRunEvents(context.Background(), "test-workspace", started.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var timing loopRunTiming
	for _, event := range events {
		if event.Kind == "timing" {
			_ = json.Unmarshal(event.Data, &timing)
		}
	}
	if timing.ModelTurns != 2 || timing.ModelMs < 120 || timing.ToolMs >= timing.ModelMs || timing.TotalMs < timing.ModelMs+timing.ToolMs {
		t.Fatalf("timing = %+v", timing)
	}
}
