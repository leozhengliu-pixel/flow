package main

import (
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// A delegated issue that asks for code changes goes to a coding session when
// the agent can start one; otherwise the agent says it has no code access.
func TestBuiltinDelegationPromptCodingSessions(t *testing.T) {
	task := domain.AgentTask{Trigger: "delegation", Prompt: "Add multiply"}
	data := domain.Bootstrap{Issues: []domain.Issue{{Identifier: "LIV-2", Title: "Add multiply", State: domain.WorkflowState{Name: "In Progress"}}}}
	tests := []struct {
		canCode     bool
		want, avoid string
	}{
		{true, "call start_coding_session now with its issueId", "no access to code"},
		{false, "no access to code", "start_coding_session"},
	}
	for _, test := range tests {
		prompt := builtinTaskPromptFor(task, data, test.canCode)
		if !strings.Contains(prompt, test.want) || strings.Contains(prompt, test.avoid) {
			t.Errorf("canCode=%v prompt:\n%s", test.canCode, prompt)
		}
		if !strings.Contains(prompt, "delegated issue LIV-2 to you") || !strings.Contains(prompt, "do not repeat its title") {
			t.Errorf("canCode=%v: the delegation framing is kept:\n%s", test.canCode, prompt)
		}
	}
}
