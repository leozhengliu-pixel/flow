package main

import (
	"os"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
)

// One direct message conversation with a real model through the fake chat
// platform, on a scratch database. It runs only when
// FLOW_CHAT_DM_LIVE_MODEL_URL names an OpenAI-compatible chat completions
// endpoint (for example a local proxy at http://127.0.0.1:8787/v1), with
// FLOW_CHAT_DM_LIVE_MODEL (default deepseek-v4.1-flash) and
// FLOW_CHAT_DM_LIVE_KEY.
func TestChatDirectMessageLiveModel(t *testing.T) {
	base := os.Getenv("FLOW_CHAT_DM_LIVE_MODEL_URL")
	if base == "" {
		t.Skip("set FLOW_CHAT_DM_LIVE_MODEL_URL to run against a real model")
	}
	f := newChatDMFixture(t, nil)
	f.s.agent = appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: base, APIKey: firstNonEmpty(os.Getenv("FLOW_CHAT_DM_LIVE_KEY"), "local-go-proxy"), Model: firstNonEmpty(os.Getenv("FLOW_CHAT_DM_LIVE_MODEL"), "deepseek-v4.1-flash"), Timeout: 3 * time.Minute, MaxOutputTokens: 2048, ToolsEnabled: true, WriteTools: true}
	f.s.agentClient = newAgentHTTPClient()
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]

	f.text(t, "om_live_1", "List three issues in this workspace with their identifiers.")
	chatDMReplies.Wait()
	sessions := f.sessions(t)
	if len(sessions) != 1 || sessions[0].Origin != "feishu" || len(sessions[0].Messages) != 2 {
		t.Fatalf("agent chat: %+v", sessions)
	}
	first := sessions[0].Messages[1]
	card := f.sent()[0]
	final := f.lastUpdate(card.ID)
	t.Logf("answer: %s", first.Content)
	t.Logf("card updates: %d; final card: %s", len(f.updates[card.ID]), final)
	tools := 0
	for _, part := range first.Parts {
		if part.ToolCall != nil {
			tools++
			t.Logf("tool %s: %s", part.ToolCall.Name, part.ToolCall.Status)
		}
	}
	if tools == 0 || !strings.Contains(final, `"template":"green"`) || !strings.Contains(final, issue.Team.Key) {
		t.Fatalf("the reply did not come from a tool call or did not reach the card: tools=%d", tools)
	}

	f.reply(t, "om_live_2", "Which of those was updated most recently? Answer with its identifier only.", "om_live_1", card.ID)
	chatDMReplies.Wait()
	sessions = f.sessions(t)
	if len(sessions) != 1 || len(sessions[0].Messages) != 4 {
		t.Fatalf("the reply did not continue the chat: %d sessions", len(sessions))
	}
	second := f.sent()[len(f.sent())-1]
	t.Logf("follow-up answer: %s", sessions[0].Messages[3].Content)
	t.Logf("follow-up card: %s", f.lastUpdate(second.ID))
	if !strings.Contains(f.lastUpdate(second.ID), issue.Team.Key) {
		t.Fatal("the follow-up card has no issue identifier")
	}
}
