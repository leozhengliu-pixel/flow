package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A comment-thread run's calls reach both API servers: its parallel /mcp calls count against the one tool budget
// and its guard's turns against the one turn budget, whichever server takes them.
func TestWorkerThreadBudgetHoldsAcrossAPIServers(t *testing.T) {
	peers := newWorkerChatPeers(t)
	a, b := peers[0], peers[1]
	app, err := a.s.store.InstallApplication(t.Context(), "test-workspace", domain.ApplicationInstallation{ClientID: store.BuiltinApplicationClientID, Name: "Flow", AvatarURL: builtinAgentAvatarURL, Builtin: true, Active: true, InstalledBy: a.data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable", "app:mentionable"}, TeamIDs: []string{a.data.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	issue := requestJSON[domain.Issue](t, a.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Peer thread", "teamId": a.data.Teams[0].ID}, http.StatusCreated)
	_, task := mentionAgentInComment(t, a.s, a.handler, a.data, app, issue.ID)
	info := workerThreadRunInfo{TaskVersion: task.Version, MaxTurns: 3, MaxCalls: 4}
	payload, _ := json.Marshal(map[string]any{"thread": info})
	run, token := startWorkerTestRun(t, a.s, a.handler, store.AgentRunInput{WorkspaceKey: "test-workspace", SessionID: task.ID, Kind: store.AgentRunKindThread, Payload: payload}, agentRunPrincipal{Mode: agentRunModeApp, UserID: a.data.Viewer.ID, AppUserID: app.UserID, TeamID: a.data.Teams[0].ID})
	handlers := []http.Handler{a.handler, b.handler}
	var mu sync.Mutex
	refused := 0
	inParallel(t, 8, func(index int) error {
		result, err := peerTool(handlers[index%2], token, "list_issues", map[string]any{"limit": index + 1}, "")
		if err == nil && result["isError"] == true && strings.Contains(runToolText(result), "tool-call budget") {
			mu.Lock()
			refused++
			mu.Unlock()
		}
		return err
	})
	if refused != 4 {
		t.Fatalf("refused %d of 8 calls with a budget of 4", refused)
	}
	guard := func(handler http.Handler, request map[string]any) workerRunGuardResponse {
		status, raw := peerRuntimeCall(handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/guard", worker: "w1", bearer: testWorkerSecret, body: request})
		var response workerRunGuardResponse
		if status != http.StatusOK || json.Unmarshal(raw, &response) != nil {
			t.Fatalf("guard %v = %d %s", request, status, raw)
		}
		return response
	}
	guard(a.handler, map[string]any{"op": "start"})
	// The spent call budget withholds the tools on whichever server asks, with the nudge given once.
	first := guard(b.handler, map[string]any{"op": "turn", "round": 1, "maxTurns": 5})
	second := guard(a.handler, map[string]any{"op": "turn", "round": 2, "maxTurns": 5})
	if !first.Withhold || !second.Withhold || first.Nudge == "" || second.Nudge != "" {
		t.Fatalf("turns = %+v / %+v", first, second)
	}
}
