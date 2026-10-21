package main

import (
	"context"
	"encoding/json"
	"strings"

	"flow/api/internal/domain"
)

// A run whose request clearly asks for code changes must end in Coding, not in a plan or a "done" claim: when the
// model has not called start_coding_session by the time it replies, by the end of its forced-decision turn, or at
// the turn limit, Flow makes the call itself through the same tool path as a model call (the same permission
// checks; a session that needs a team member's approval is held for it, never started around it). The thread
// records it as the agent's action and the run ends with the session's real state. Without a known coding
// environment nothing is started: the run ends with the blocker (which environments exist, what is missing).
// Investigation, explanation, planning, estimates and reviews never reach this (builtinRequestsDevelopment).

// builtinCodingTransfer starts Coding for the agent (or reports why it cannot) and returns the reply that states
// the result. reason says why Flow stepped in; it is shown on the action. A false ok means the session can no
// longer be written.
func (s *server) builtinCodingTransfer(ctx context.Context, task domain.AgentTask, g *builtinRunGuard, issueTitle, reason string, execute func(domain.AgentToolCall) ([]byte, error), appendActivity func(domain.AgentActivity) error) (string, bool) {
	if !g.coding.EnvironmentKnown {
		g.started = &codingStartOutcome{delivery: codingNotStarted, blocker: g.coding.environmentBlocker()}
		return codingDeliveryReply(codingNotStarted, g.started.blocker, g.coding), true
	}
	call := g.transferCall(issueTitle)
	result, err := execute(call)
	content, failed := string(result), err != nil
	if failed {
		content = agentToolErrorContent(err)
	}
	s.recordCodingStart(ctx, task.WorkspaceKey, g, content, failed)
	if g.started == nil {
		g.started = &codingStartOutcome{delivery: codingNotStarted, blocker: "the coding session did not start"}
	}
	outcome := "Started by Flow (" + reason + "): " + g.started.delivery.label()
	if g.started.blocker != "" {
		outcome += " — " + truncateSettingsText(g.started.blocker, 300)
	}
	metadata := map[string]any{"action": startCodingSessionTool, "parameter": "issueId " + g.coding.Issue + ", environmentId " + g.coding.EnvironmentID, "result": outcome}
	if appendActivity(domain.AgentActivity{Type: "action", Body: startCodingSessionTool, Metadata: metadata}) != nil {
		return "", false
	}
	return codingDeliveryReply(g.started.delivery, g.started.blocker, g.coding), true
}

// builtinReplyWithCoding is the final reply of a run that Flow moved into Coding: the model's reply (a "done"
// claim replaced by the session's state, see builtinFinalReply) followed by the session's state.
func (s *server) builtinReplyWithCoding(ctx context.Context, task domain.AgentTask, g *builtinRunGuard, text, state string) string {
	reply := strings.TrimSpace(s.builtinFinalReply(ctx, task, g, text))
	if reply == "" || reply == state {
		return state
	}
	return reply + "\n\n" + state
}

// connectorFoundNothing reports whether a connector result carries no content: no items, or only empty text (a
// search without matches). Such a call adds nothing and counts as a call without progress.
func connectorFoundNothing(result []byte) bool {
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	if json.Unmarshal(result, &parsed) != nil {
		return strings.TrimSpace(string(result)) == ""
	}
	if structured := strings.TrimSpace(string(parsed.StructuredContent)); structured != "" && structured != "null" && structured != "{}" && structured != "[]" {
		return false
	}
	for _, item := range parsed.Content {
		if item.Type != "text" || strings.TrimSpace(item.Text) != "" {
			return false
		}
	}
	return true
}

// callsTool reports whether calls include the named tool.
func callsTool(calls []domain.AgentToolCall, name string) bool {
	for _, call := range calls {
		if call.Name == name {
			return true
		}
	}
	return false
}
