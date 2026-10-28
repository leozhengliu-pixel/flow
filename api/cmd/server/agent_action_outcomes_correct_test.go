package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// A sentence that says every change failed (or succeeded) against the ledger is rewritten into Flow's count; a
// qualified sentence, or one the ledger agrees with, is kept.
func TestAgentLedgerCorrectsContraryAllClaims(t *testing.T) {
	partial := &agentWriteLedger{outcomes: []agentWriteOutcome{{Tool: "save_issue", Target: "DEV-1", Status: agentWriteSucceeded}, {Tool: "save_issue", Target: "DEV-2", Status: agentWriteFailed, Reason: "not found"}}}
	failedOnly := &agentWriteLedger{outcomes: []agentWriteOutcome{{Tool: "save_issue", Target: "DEV-1", Status: agentWriteFailed}, {Tool: "save_issue", Target: "DEV-2", Status: agentWriteFailed}}}
	succeededOnly := &agentWriteLedger{outcomes: []agentWriteOutcome{{Tool: "save_issue", Target: "DEV-1", Status: agentWriteSucceeded}}}
	const en = "Correction from Flow: of 2 change(s), 1 succeeded, 1 failed and 0 were not executed."
	const zh = "Flow 更正：2 项更改中，成功 1 项，失败 1 项，未执行 0 项。"
	for _, item := range []struct {
		ledger  *agentWriteLedger
		answer  string
		chinese bool
		want    string
	}{
		{partial, "All four moves failed because you lack permission. Anything else?", false, en + " Anything else?"},
		{partial, "- Both updates failed.\n- See the team settings.", false, "- " + en + "\n- See the team settings."},
		{partial, "None of the issues were moved.", false, en},
		{partial, "All issues have been moved to Vault.", false, en},
		{partial, "两个 issue 都迁移失败：你没有权限。请联系管理员。", true, zh + "请联系管理员。"},
		{partial, "所有 issue 均已移动到 VLT。", true, zh},
		{partial, "Both moves failed. All of them failed.", false, en},
		// Qualified or agreeing sentences stay.
		{partial, "All moves but DEV-2 succeeded.", false, "All moves but DEV-2 succeeded."},
		{partial, "DEV-1 moved; DEV-2 failed.", false, "DEV-1 moved; DEV-2 failed."},
		{partial, "除了 DEV-2，其余都已移动。", true, "除了 DEV-2，其余都已移动。"},
		{failedOnly, "Both moves failed.", false, "Both moves failed."},
		{succeededOnly, "All moves succeeded.", false, "All moves succeeded."},
		// A real run's answer: "nothing failed" is not a failure claim, and emphasis around the sentence holds.
		{succeededOnly, "**结论：两个都迁移成功了，没有失败项。** 这与描述里的预判相反。", true, "**结论：两个都迁移成功了，没有失败项。** 这与描述里的预判相反。"},
		{partial, "**结论：两个都迁移成功了，没有失败项。** 这与描述里的预判相反。", true, "**" + zh + "** 这与描述里的预判相反。"},
		{partial, "**Both moves failed.** Sorry.", false, "**" + en + "** Sorry."},
		{partial, "Result: **both moves failed**.", false, en},
		{succeededOnly, "All moves went through, no failures.", false, "All moves went through, no failures."},
		{&agentWriteLedger{}, "All moves failed.", false, "All moves failed."},
	} {
		if got := item.ledger.correct(item.answer, item.chinese); got != item.want {
			t.Errorf("correct(%q) = %q, want %q", item.answer, got, item.want)
		}
	}
	// "Nothing failed" wording does not earn an all-success reply the summary.
	if summary := succeededOnly.summary("两个都迁移成功了，没有失败项。", true); summary != "" {
		t.Errorf("no failure claimed: %q", summary)
	}
	// report appends the summary once; a reported answer is left alone.
	reported := partial.report("Both updates failed.", false)
	if !strings.HasPrefix(reported, en+"\n\n"+agentSummaryHeading) || partial.report(reported, false) != reported {
		t.Fatalf("report: %q", reported)
	}
}

// A request that asks for no change at all is an assessment: its answer may talk about failures and permissions
// without a write. A real run asked "先别真的改动任何 issue … 告诉我迁移结果" and had its assessment replaced.
func TestAgentUngroundedTextSkipsAssessments(t *testing.T) {
	s := &server{}
	s.agent.ToolsEnabled, s.agent.WriteTools = true, true
	answer := "两个都能迁移，看不到权限导致的失败点。"
	for request, want := range map[string]bool{
		"把 REA-11 和 REA-12 迁移到 VLT 团队": true,
		"Move ENG-1 to the Vault team": true,
		"我想把 REA-11 和 REA-12 迁移到 VLT 团队。先别真的改动任何 issue：只查看一下，然后告诉我迁移结果":                   false,
		"不要做任何修改，告诉我能否把 ENG-1 移到 VLT":                                                     false,
		"Don't actually move anything, just tell me if moving ENG-1 to Vault would fail.": false,
		"Can ENG-1 move to Vault? Do not change any issues.":                              false,
	} {
		if got := s.agentUngroundedText(request, answer, &agentWriteLedger{}); got != want {
			t.Errorf("%q: ungrounded = %v, want %v", request, got, want)
		}
	}
}

// Connector tools that may write are changes in the ledger; ones their connector (or the workspace's policy) marks
// read-only are not.
func TestAgentLedgerRecordsConnectorWrites(t *testing.T) {
	policy := applicationPolicy{ID: "jira", Name: "Jira"}
	ctx := context.WithValue(t.Context(), connectorToolsKey{}, []connectorTool{
		{Definition: agentProviderTool{Name: "external_write"}, Policy: policy, RemoteName: "create_ticket"},
		{Definition: agentProviderTool{Name: "external_read"}, Policy: policy, RemoteName: "search", ReadOnly: true, Annotated: true},
	})
	ledger := &agentWriteLedger{}
	ledger.record(ctx, domain.AgentToolCall{Name: "external_write", Arguments: json.RawMessage(`{"title":"Outage"}`)}, agentWriteFailed, "403 Forbidden")
	ledger.record(ctx, domain.AgentToolCall{Name: "external_read", Arguments: json.RawMessage(`{"query":"x"}`)}, agentWriteSucceeded, "")
	ledger.record(ctx, domain.AgentToolCall{Name: "get_issue", Arguments: json.RawMessage(`{"id":"DEV-1"}`)}, agentWriteSucceeded, "")
	if len(ledger.outcomes) != 1 || ledger.outcomes[0].Tool != "create_ticket · Jira" || ledger.outcomes[0].Target != "Outage" || ledger.outcomes[0].Reason != "403 Forbidden" {
		t.Fatalf("only the connector write is recorded: %+v", ledger.outcomes)
	}
	if summary := ledger.summary("", false); !strings.Contains(summary, "Failed: Outage (create_ticket · Jira): 403 Forbidden") {
		t.Fatalf("summary: %q", summary)
	}
}
