package main

import "testing"

// TestSimpleMCPTeamCountIgnoresInjectedBaseURL keeps count_issues on the
// maintained team counters: the MCP dispatcher injects __flowBaseURL into
// every tool call, which is not a filter.
func TestSimpleMCPTeamCountIgnoresInjectedBaseURL(t *testing.T) {
	if !simpleMCPTeamCount(map[string]any{"team": "T1", "__flowBaseURL": "http://127.0.0.1:18300"}) {
		t.Fatal("injected __flowBaseURL must not disable the team counter fast path")
	}
	if simpleMCPTeamCount(map[string]any{"team": "T1", "priority": "1"}) {
		t.Fatal("a real filter must disable the team counter fast path")
	}
}
