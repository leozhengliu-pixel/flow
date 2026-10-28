package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// A connector's readOnlyHint is its own claim. A workspace admin can override it per tool in the connector's
// policy: "read" treats the tool as read-only, "write" as a tool that changes data; a tool not listed follows its
// annotation. The override decides the reply-only loop refusal and the outcome ledger.
func TestConnectorToolAccessOverridesTheAnnotation(t *testing.T) {
	notes := newAnnotatedMCP(t)
	s := &server{authDisabled: true}
	s.agent.ToolsEnabled, s.agent.WriteTools = true, true
	policy := applicationPolicy{ID: "notes", Kind: "mcp", Name: "Notes", URL: notes.URL, Status: "approved", Shared: true, ToolAccess: map[string]string{"read_notes": connectorToolWrite, "tidy_notes": connectorToolRead}}
	tools, err := s.listConnectorTools(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]connectorTool{}
	for _, tool := range tools {
		got[tool.RemoteName] = tool
	}
	if tool := got["read_notes"]; tool.ReadOnly || tool.AccessOverride != connectorToolWrite {
		t.Errorf("read_notes is treated as a write: %+v", tool)
	}
	if tool := got["tidy_notes"]; !tool.ReadOnly || tool.AccessOverride != connectorToolRead {
		t.Errorf("tidy_notes is treated as read-only: %+v", tool)
	}
	if tool := got["post_note"]; tool.ReadOnly || tool.AccessOverride != "" {
		t.Errorf("post_note follows its annotation: %+v", tool)
	}

	ctx := context.WithValue(t.Context(), connectorToolsKey{}, tools)
	if err := loopConnectorWriteRefusal(ctx, domain.AgentToolCall{Name: got["read_notes"].Definition.Name}); err == nil || !strings.Contains(err.Error(), "by the workspace's connector settings") {
		t.Errorf("a reply-only loop refuses a tool the policy treats as a write: %v", err)
	}
	if err := loopConnectorWriteRefusal(ctx, domain.AgentToolCall{Name: got["tidy_notes"].Definition.Name}); err != nil {
		t.Errorf("a reply-only loop runs a tool the policy treats as read-only: %v", err)
	}
	ledger := &agentWriteLedger{}
	for _, name := range []string{"read_notes", "tidy_notes", "post_note"} {
		ledger.record(ctx, domain.AgentToolCall{Name: got[name].Definition.Name, Arguments: json.RawMessage(`{}`)}, agentWriteSucceeded, "")
	}
	if len(ledger.outcomes) != 2 || ledger.outcomes[0].Tool != "read_notes · Notes" || ledger.outcomes[1].Tool != "post_note · Notes" {
		t.Errorf("the ledger counts the tools treated as writes: %+v", ledger.outcomes)
	}
}

// A reply-only loop follows the admin's override end to end: the tool the policy treats as a write never reaches
// the connector, and the one it treats as read-only runs.
func TestReplyOnlyLoopFollowsTheConnectorPolicy(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	notes := newAnnotatedMCP(t)
	setTestConnectors(t, srv.store, applicationPolicy{ID: "notes", Kind: "mcp", Name: "Notes", URL: notes.URL, Status: "approved", Shared: true, ToolAccess: map[string]string{"read_notes": connectorToolWrite, "tidy_notes": connectorToolRead}})
	read, unknown := connectorToolName("notes", "read_notes"), connectorToolName("notes", "tidy_notes")
	provider.script("tool:"+read+" {}", "tool:"+unknown+" {}", `tool:finish_run {"status":"done","summary":"今天团队发布了导入器。"}`)
	loop, started := startTestRun(t, handler, "Daily summary", replyOnlyInstructions, map[string]any{"connectorIds": []string{"notes"}})
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "completed" || notes.called("read_notes") != 0 || notes.called("tidy_notes") != 1 {
		t.Fatalf("run %s (%s): connector calls %v", run.Status, run.Error, notes.calls)
	}
	for _, call := range run.ToolCalls {
		if call.Name == read && (call.Status != "blocked" || !strings.Contains(call.Error, "workspace's connector settings")) {
			t.Fatalf("read_notes is refused by the policy: %+v", call)
		}
	}
}

// Only a workspace admin sets the overrides; an owner's save keeps them; values are "read" or "write"; connecting
// records the tools the connector marks read-only.
func TestConnectorToolAccessPolicyAPI(t *testing.T) {
	f := newAgentAttributionFixture(t)
	notes := newAnnotatedMCP(t)
	setTestConnectors(t, f.s.store, applicationPolicy{ID: "notes", Kind: "mcp", Name: "Notes", URL: notes.URL, Status: "approved", Shared: false, OwnerID: f.data.Viewer.ID})
	policy := func() applicationPolicy {
		t.Helper()
		data := f.s.store.Bootstrap()
		for _, item := range applicationPolicies(&data) {
			if item.ID == "notes" {
				return item
			}
		}
		t.Fatal("no policy")
		return applicationPolicy{}
	}
	save := func(input applicationPolicy, admin bool, want int) {
		t.Helper()
		f.s.authDisabled = admin
		defer func() { f.s.authDisabled = true }()
		requestJSON[map[string]any](t, f.handler, http.MethodPut, "/api/application-policies", input, want)
	}
	current := policy()
	current.ToolAccess = map[string]string{"read_notes": "write", "tidy_notes": "read"}
	save(current, true, http.StatusOK)
	if got := policy().ToolAccess; len(got) != 2 || got["read_notes"] != "write" || got["tidy_notes"] != "read" {
		t.Fatalf("the admin's overrides are stored: %v", got)
	}
	bad := policy()
	bad.ToolAccess = map[string]string{"read_notes": "maybe"}
	save(bad, true, http.StatusBadRequest)

	connected := requestJSON[connectorConnectResult](t, f.handler, http.MethodPost, "/api/application-policies/notes/connect", map[string]any{}, http.StatusOK)
	if !connected.Connected || connected.Policy == nil || strings.Join(connected.Policy.ReadOnlyTools, ",") != "read_notes" || len(connected.Policy.Tools) != 3 || connected.Policy.ToolAccess["read_notes"] != "write" {
		t.Fatalf("connecting records the read-only tools and keeps the overrides: %+v", connected)
	}
}

// A connector's owner who is not an admin saves the connector (a rename) without changing the admin's overrides,
// whatever toolAccess they send; an admin changes them.
func TestConnectorToolAccessIsAdminOnly(t *testing.T) {
	f := newOutcomeFixture(t)
	const endpoint = "https://93.184.216.34/mcp"
	setTestConnectors(t, f.repository, applicationPolicy{ID: "notes", Kind: "mcp", Name: "Notes", URL: endpoint, Status: "approved", OwnerID: "usr_member", ToolAccess: map[string]string{"post_note": connectorToolWrite}})
	stored := func() applicationPolicy {
		t.Helper()
		data := f.repository.Bootstrap()
		for _, item := range applicationPolicies(&data) {
			if item.ID == "notes" {
				return item
			}
		}
		t.Fatal("no policy")
		return applicationPolicy{}
	}
	url := f.host.URL + "/api/application-policies"
	owner := applicationPolicy{ID: "notes", Kind: "mcp", Name: "Notes", URL: endpoint, Status: "pending", DisplayName: "My notes", ToolAccess: map[string]string{"post_note": connectorToolRead, "read_notes": connectorToolWrite}}
	authRequest[applicationPolicy](t, f.member, http.MethodPut, url, owner, outcomeWorkspace, http.StatusOK)
	if item := stored(); item.DisplayName != "My notes" || len(item.ToolAccess) != 1 || item.ToolAccess["post_note"] != connectorToolWrite {
		t.Fatalf("the owner's save keeps the admin's overrides: %+v", item)
	}
	admin := stored()
	admin.ToolAccess = map[string]string{"post_note": connectorToolRead}
	authRequest[applicationPolicy](t, f.admin, http.MethodPut, url, admin, outcomeWorkspace, http.StatusOK)
	if item := stored(); len(item.ToolAccess) != 1 || item.ToolAccess["post_note"] != connectorToolRead || item.OwnerID != "usr_member" {
		t.Fatalf("the admin changes the overrides: %+v", item)
	}
}
