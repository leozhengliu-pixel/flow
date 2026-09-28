package main

import (
	"encoding/json"
	"testing"

	"flow/api/internal/domain"
)

func TestCleanAgentSessionTitle(t *testing.T) {
	cases := map[string]string{
		"\"Summarize Compare Test Project Status.\"": "Summarize Compare Test Project Status",
		"# 本周站会总结。\nextra line":                      "本周站会总结",
		"  ":                                         "",
	}
	for input, want := range cases {
		if got := cleanAgentSessionTitle(input); got != want {
			t.Fatalf("cleanAgentSessionTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLeakedProgressSteps(t *testing.T) {
	steps, rest := leakedProgressSteps("{\"title\":\"Gathering weekly activity\",\"message\":\"I'll review it.\"}{\"title\":\"Checking history…\"}## Summary\nDone")
	if len(steps) != 2 || steps[0].Title != "Gathering weekly activity" || steps[0].Message != "I'll review it." || steps[1].Title != "Checking history" || rest != "## Summary\nDone" {
		t.Fatalf("unexpected: %+v %q", steps, rest)
	}
	if steps, rest := leakedProgressSteps(`{"name":"x"} text`); steps != nil || rest != `{"name":"x"} text` {
		t.Fatalf("non-progress JSON must be kept: %+v %q", steps, rest)
	}
}

func TestRemoveAgentPartKeepsIndexes(t *testing.T) {
	parts := []domain.AgentMessagePart{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	index := map[string]int{"text": 0, "tool:1": 1, "tool:2": 2}
	parts = removeAgentPart(parts, index, 1)
	if len(parts) != 2 || parts[1].ID != "c" || index["tool:2"] != 1 || index["text"] != 0 {
		t.Fatalf("unexpected parts %+v index %+v", parts, index)
	}
	if _, ok := index["tool:1"]; ok {
		t.Fatal("removed part must leave the index")
	}
}

func TestAgentTurnHasProgressMessage(t *testing.T) {
	calls := []domain.AgentToolCall{{Name: "list_issues"}, {Name: agentProgressTool, Arguments: json.RawMessage(`{"title":"Reviewing inbox","message":"I'll check it."}`)}}
	if !agentTurnHasProgressMessage(calls) {
		t.Fatal("expected a narrated progress call")
	}
	if agentTurnHasProgressMessage([]domain.AgentToolCall{{Name: agentProgressTool, Arguments: json.RawMessage(`{"title":"Reviewing inbox"}`)}}) {
		t.Fatal("a title-only call does not narrate")
	}
}
