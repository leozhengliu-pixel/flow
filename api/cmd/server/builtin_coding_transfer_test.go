package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Investigation, explanation, planning, estimates and reviews never start Coding — neither the model's call (the
// prompt does not make it the first step) nor Flow's own — and their runs keep the normal connector budget, in
// Chinese and English, whether the model searches first or answers at once. A coding environment is known, so
// only the intent decides.
func TestBuiltinNonDevelopmentRequestsNeverStartCoding(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	environment := h.environments[0]
	requests := []struct{ title, description string }{
		{"登录接口偶发 500", "分析一下这个问题的原因"},
		{"Multiply 函数", "解释这段代码：api/multiply.go 里的 Multiply 函数"},
		{"新增 multiply 接口", "给出实施方案，先不要改代码"},
		{"新增 multiply 接口", "评估一下实现这个功能的工作量"},
		{"Billing service design", "Review this design for the billing service"},
		{"Login timeout", "Analyse why the fix for the login timeout failed"},
		{"Flaky invoice test", "Investigate the flaky test and explain what goes wrong"},
		{"Invoice rounding", "Fix the rounding bug later, but don't change code until we agree on the approach"},
	}
	const answer = "分析如下：问题出在超时配置。建议的做法是调整重试。"
	searches := 5 // more than the Coding budget (3), within the normal one (8)
	for _, item := range requests {
		for _, searchFirst := range []bool{true, false} {
			run := h.delegate(t, item.title, item.description+"\nenvironmentId: "+environment.ID, func(index int, _ domain.Issue) string {
				if searchFirst && index < searches {
					return chatToolCallSSE("c_search", h.glob, map[string]any{"pattern": "**/*" + string(rune('a'+index)) + ".go"})
				}
				return chatTextSSE(answer)
			})
			if strings.Contains(run.requests[0].prompt(), "Your first step is start_coding_session") {
				t.Errorf("%q: the prompt does not make Coding the first step", item.description)
			}
			if run.task.CodingSessionID != "" || run.startActions() != 0 {
				t.Errorf("%q (search first %v): Coding did not start: session %q, %d start actions", item.description, searchFirst, run.task.CodingSessionID, run.startActions())
			}
			if errors := run.errors(); len(errors) > 0 {
				t.Errorf("%q: no error: %v", item.description, errors)
			}
			if run.response() != answer {
				t.Errorf("%q: the agent's own answer is posted: %q", item.description, run.response())
			}
			if searchFirst && run.upstream["glob"] != searches {
				t.Errorf("%q: the normal connector budget applies: %d of %d searches ran", item.description, run.upstream["glob"], searches)
			}
		}
	}
}

// Retrieval that fails, finds nothing, or is misused (a file path sent to a list tool) converges within the budget:
// the calls stop counting as progress, connector tools are withdrawn, and the run ends in Coding — or, with no
// known coding environment, with the blocker naming the environments.
func TestBuiltinRetrievalFailuresConverge(t *testing.T) {
	description := "请实现 multiply 接口，补充单元测试并运行测试，创建草稿 MR。"
	distinctGlobs := func(h *delegationHarness) func(int, domain.Issue) string {
		return func(index int, _ domain.Issue) string {
			return chatToolCallSSE("c_glob", h.glob, map[string]any{"pattern": "**/*" + string(rune('a'+index)) + ".go"})
		}
	}
	cases := []struct {
		name    string
		respond codeSearchResponder
		script  func(h *delegationHarness) func(int, domain.Issue) string
		tool    string
		sent    int
	}{
		{"errors", func(string, map[string]any) (string, bool) { return "search backend unavailable", true }, distinctGlobs, "glob", builtinMaxNoProgress},
		{"empty", func(string, map[string]any) (string, bool) { return "", false }, distinctGlobs, "glob", builtinMaxNoProgress},
		{"file args to a list tool", nil, func(h *delegationHarness) func(int, domain.Issue) string {
			return func(index int, _ domain.Issue) string {
				return chatToolCallSSE("c_repos", h.listRepos, map[string]any{"path": "api/" + string(rune('a'+index)) + ".go"})
			}
		}, "list_repos", 0},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			h := newDelegationHarness(t, "acme/api") // the workspace's only environment
			h.respond = item.respond
			run := h.delegate(t, "新增 multiply 接口", description, item.script(h))
			if run.upstream[item.tool] != item.sent {
				t.Fatalf("%d calls reach the connector, want %d", run.upstream[item.tool], item.sent)
			}
			if len(run.requests) > builtinMaxNoProgress+1 || len(run.errors()) > 0 {
				t.Fatalf("the run converges within budget without an error: %d turns, %v", len(run.requests), run.errors())
			}
			if run.task.CodingSessionID == "" || run.startActions() != 1 {
				t.Fatalf("the run ends in Coding: %+v", run.activities)
			}
			session, _ := h.s.store.CodingSession(t.Context(), h.workspace, run.task.CodingSessionID)
			if session.EnvironmentID != h.environments[0].ID {
				t.Fatalf("the workspace's only environment is used: %+v", session)
			}
			if reply := run.response(); !strings.Contains(reply, "Coding 会话") {
				t.Fatalf("the reply states the session: %q", reply)
			}
		})
	}

	t.Run("unknown environment", func(t *testing.T) {
		h := newDelegationHarness(t, "acme/api", "acme/web")
		run := h.delegate(t, "新增 multiply 接口", description, func(int, domain.Issue) string {
			return chatToolCallSSE("c_glob", h.glob, map[string]any{"pattern": "**/go.mod"})
		})
		if run.task.CodingSessionID != "" || run.startActions() != 0 || len(run.errors()) > 0 {
			t.Fatalf("nothing starts without a known environment, and the run does not fail: %+v", run.activities)
		}
		reply := run.response()
		for _, environment := range h.environments {
			if !strings.Contains(reply, environment.ID) || !strings.Contains(reply, environment.Repository) {
				t.Fatalf("the blocker names the environments: %q", reply)
			}
		}
		if !strings.Contains(reply, "尚未为 "+run.issue.Identifier+" 启动 Coding 会话") || !strings.Contains(reply, "environmentId") {
			t.Fatalf("the blocker says what is missing: %q", reply)
		}
	})
}

// A model that answers with a "done" claim instead of starting Coding: Flow starts it in the environment of the
// repository the issue names, and the reply states the session's real state.
func TestBuiltinDoneClaimWithoutCodingIsTransferred(t *testing.T) {
	h := newDelegationHarness(t, "acme/api", "acme/web")
	run := h.delegate(t, "Add the multiply endpoint", "Implement the multiply endpoint in acme/web, add unit tests and open a draft MR.", func(int, domain.Issue) string {
		return chatTextSSE("I have implemented the endpoint and opened a draft MR.")
	})
	if run.task.CodingSessionID == "" || run.startActions() != 1 {
		t.Fatalf("Flow starts Coding: %+v", run.activities)
	}
	session, _ := h.s.store.CodingSession(t.Context(), h.workspace, run.task.CodingSessionID)
	if session.EnvironmentID != h.environments[1].ID {
		t.Fatalf("the environment of the repository the issue names is used: %+v", session)
	}
	if reply := run.response(); strings.Contains(reply, "I have implemented") || !strings.Contains(reply, "Started a coding session for "+run.issue.Identifier) {
		t.Fatalf("the claim is replaced with the session's state: %q", reply)
	}
}

// The worker runtime runs its own model loop; when a delegated development run finishes without Coding, Flow
// starts it on completion, and leaves analysis runs alone.
func TestWorkerRunCompletionEnsuresCoding(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	finish := func(description string) domain.AgentTask {
		issue := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Multiply", "description": description, "teamId": h.teamID, "delegateId": h.agentUser}, http.StatusCreated)
		request := httptest.NewRequest(http.MethodPost, "/internal/agent-runtime/runs/run/complete", nil).WithContext(t.Context())
		h.s.ensureWorkerRunCoding(request, store.AgentRun{WorkspaceKey: h.workspace, SessionID: issue.AgentSessionID})
		task, _ := h.s.store.AgentTask(t.Context(), h.workspace, issue.AgentSessionID)
		return task
	}
	if task := finish("请实现 multiply 接口并补充单元测试，创建草稿 MR。"); task.CodingSessionID == "" {
		t.Fatalf("a finished development run is moved into Coding: %+v", task)
	}
	if task := finish("分析一下 multiply 接口报错的原因"); task.CodingSessionID != "" {
		t.Fatalf("an analysis run is left alone: %+v", task)
	}
}

func TestBuiltinRequestsDevelopment(t *testing.T) {
	cases := []struct {
		request, title, description string
		want                        bool
	}{
		{"", "新增 multiply 接口", "请实现 multiply 接口，补充单元测试并运行测试，创建草稿 MR。", true},
		{"请处理一下", "新增 multiply 接口", "请实现 multiply 接口。", true},    // neutral instructions defer to the issue
		{"先分析一下原因", "新增 multiply 接口", "请实现 multiply 接口。", false}, // the instructions ask for analysis
		{"", "分析登录 500 的原因并给出修复方案", "", false},                   // mixed: left to the agent
		{"", "新增 multiply 接口", "请实现 multiply 接口，先不要改代码。", false}, // explicit veto
		{"实现它", "", "", true}, // a mention or reply decides on its own words
		{"看一下这个接口", "", "", false},
		{"Fix the flaky invoice test and open a draft PR", "", "", true},
		{"Explain why the fix broke billing", "", "", false},
		{"don't change code, just estimate the effort", "", "", false},
	}
	for _, item := range cases {
		if got := builtinRequestsDevelopment(item.request, item.title, item.description); got != item.want {
			t.Errorf("builtinRequestsDevelopment(%q, %q, %q) = %v, want %v", item.request, item.title, item.description, got, item.want)
		}
	}
}
