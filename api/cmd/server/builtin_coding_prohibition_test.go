package main

import (
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// An explicit prohibition on starting development, a Coding session or a sandbox, or on calling tools, outranks the
// development words in the same request ("不要启动开发会话" contains "开发"): Flow never starts Coding for it, neither
// the model's prompt nor the server fallback. Real development requests still do.

func TestBuiltinCodingProhibitionOutranksDevelopmentWords(t *testing.T) {
	cases := []struct {
		name                        string
		request, title, description string
		want                        bool
	}{
		{"reported acknowledgement", "请直接回复确认收到。不要调用工具，不要启动开发会话。", "", "", false},
		{"zh no dev session", "不要启动开发会话，只回复收到", "", "", false},
		{"zh forbid development", "禁止开发，回复收到即可。", "", "", false},
		{"zh no Coding", "先别开启 Coding 会话，确认一下需求就行", "", "", false},
		{"zh no sandbox", "实现这个接口的事以后再说，不要创建沙盒。", "", "", false},
		{"zh no tools", "收到请回复。不要调用任何工具。", "", "", false},
		{"zh forbid code changes", "禁止修改代码，修复方案后面再定", "", "", false},
		{"en no coding session", "Just acknowledge. Do not start a coding session for the fix.", "", "", false},
		{"en no sandbox", "Implement nothing yet; don't spin up a sandbox.", "", "", false},
		{"en no tools", "Reply with OK to confirm. Don't call any tools.", "", "", false},
		{"zh-en mixed", "请确认收到，don't start a coding session，不要开发。", "", "", false},
		{"plain acknowledgement", "请直接回复确认收到。", "", "", false},
		{"read-only analysis", "分析一下登录 500 的原因，不要修改代码", "", "", false},
		{"real dev request zh", "请实现 multiply 接口，补充单元测试并创建草稿 MR。", "", "", true},
		{"real dev request en", "Implement the multiply endpoint and open a draft PR.", "", "", true},
		{"dev request naming a tool qualifier", "不要调用工具查资料，直接实现 multiply 接口并补充单元测试。", "", "", true},
		{"dev request avoiding the dev database zh", "修复登录报错，不要使用开发环境的数据库。", "", "", true},
		{"dev request avoiding the dev database en", "Fix the login error, but don't use the dev database.", "", "", true},
		{"dev session wording without prohibition", "启动开发会话，实现 multiply 接口", "", "", true},
		{"delegation: issue asks, instructions forbid", "只回复收到，不要启动开发会话", "实现 multiply 接口", "请实现 multiply 接口并补充单元测试。", false},
		{"delegation: issue forbids, instructions neutral", "请处理一下", "实现 multiply 接口", "不要启动 Coding 会话，先确认需求。", false},
		{"delegation: neutral issue, instructions ask", "请实现它并补充单元测试", "multiply 接口", "", true},
	}
	for _, item := range cases {
		if got := builtinRequestsDevelopment(item.request, item.title, item.description); got != item.want {
			t.Errorf("%s: builtinRequestsDevelopment(%q, %q, %q) = %v, want %v", item.name, item.request, item.title, item.description, got, item.want)
		}
	}
}

// The request is the latest message: a later reply can lift a prohibition (or add one), and the earlier message no
// longer decides.
func TestBuiltinCodingProhibitionFollowsScopeChanges(t *testing.T) {
	s := &server{}
	data := domain.Bootstrap{Issues: []domain.Issue{{Identifier: "ENG-7", Title: "multiply 接口", Description: ""}}}
	cases := []struct {
		name, initial, reply string
		want                 bool
	}{
		{"first message forbids", "请直接回复确认收到。不要调用工具，不要启动开发会话。", "", false},
		{"later reply authorises development", "请直接回复确认收到。不要调用工具，不要启动开发会话。", "现在可以开始了：请实现 multiply 接口并补充单元测试。", true},
		{"later reply withdraws development", "请实现 multiply 接口并补充单元测试。", "先不要启动开发会话，等我确认。", false},
		{"later reply is an acknowledgement only", "请实现 multiply 接口并补充单元测试。", "好的，收到。", false},
	}
	for _, item := range cases {
		task := domain.AgentTask{Trigger: "mention", InitialPrompt: item.initial, Prompt: firstNonEmpty(item.reply, item.initial)}
		if got := s.builtinCodingContextFor(t.Context(), task, data).Development; got != item.want {
			t.Errorf("%s: Development = %v, want %v", item.name, got, item.want)
		}
	}
}

// The reported run end to end: Coding is on with a known environment, the model acknowledges, and Flow's fallback
// does not start Coding — no start_coding_session call, no coding session, no approval, no tool run.
func TestBuiltinChineseNoDevelopmentAcknowledgementStartsNothing(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	const ack = "收到。"
	for _, item := range []struct{ title, description string }{
		{"确认收到", "请直接回复确认收到。不要调用工具，不要启动开发会话。"},
		{"实现 multiply 接口", "请直接回复确认收到。不要调用工具，不要启动开发会话。"},
		{"Multiply endpoint", "Please just confirm you received this. Don't call any tools and do not start a coding session."},
		{"新增 multiply 接口", "先别开 sandbox，也不要启动 Coding 会话，回复收到即可。"},
	} {
		run := h.delegate(t, item.title, item.description+"\nenvironmentId: "+h.environments[0].ID, func(int, domain.Issue) string { return chatTextSSE(ack) })
		if len(run.requests) != 1 {
			t.Errorf("%q: model calls = %d, want 1", item.description, len(run.requests))
		}
		if run.startActions() != 0 || run.count("action") != 0 || len(run.upstream) != 0 {
			t.Errorf("%q: tool executions: %d start_coding_session, %d actions, upstream %v", item.description, run.startActions(), run.count("action"), run.upstream)
		}
		if run.task.CodingSessionID != "" {
			t.Errorf("%q: a coding session was created: %s", item.description, run.task.CodingSessionID)
		}
		if run.count("elicitation") != 0 || run.task.PendingTool != nil {
			t.Errorf("%q: an approval was requested: %+v", item.description, run.activities)
		}
		if run.task.Status != "complete" || run.response() != ack {
			t.Errorf("%q: the agent's own reply completes the run: %q, %q", item.description, run.task.Status, run.response())
		}
		// The completion check of a worker run reads the same guard and starts nothing either.
		h.s.ensureWorkerRunCoding(httptestRequest(t), store.AgentRun{WorkspaceKey: h.workspace, SessionID: run.task.ID})
		if task, _ := h.s.store.AgentTask(t.Context(), h.workspace, run.task.ID); task.CodingSessionID != "" {
			t.Errorf("%q: the completion check started Coding", item.description)
		}
	}
	sessions, err := h.s.store.ListCodingSessions(t.Context(), h.workspace, store.CodingSessionFilter{})
	if err != nil || len(sessions) != 0 {
		t.Fatalf("coding sessions created = %d (%v), want 0", len(sessions), err)
	}

	// The same harness still starts Coding for a real development request.
	run := h.delegate(t, "新增 multiply 接口", "请实现 multiply 接口，补充单元测试并创建草稿 MR。environmentId: "+h.environments[0].ID, func(int, domain.Issue) string {
		return chatTextSSE("好的。")
	})
	if run.task.CodingSessionID == "" || run.startActions() != 1 {
		t.Fatalf("a real development request still enters Coding: %+v", run.activities)
	}
}
