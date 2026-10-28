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
		{&agentWriteLedger{}, "All moves failed.", false, "All moves failed."},
	} {
		if got := item.ledger.correct(item.answer, item.chinese); got != item.want {
			t.Errorf("correct(%q) = %q, want %q", item.answer, got, item.want)
		}
	}
	// report appends the summary once; a reported answer is left alone.
	reported := partial.report("Both updates failed.", false)
	if !strings.HasPrefix(reported, en+"\n\n"+agentSummaryHeading) || partial.report(reported, false) != reported {
		t.Fatalf("report: %q", reported)
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
