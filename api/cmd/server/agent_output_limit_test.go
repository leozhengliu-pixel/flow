package main

import (
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// A reply the provider cut off at its output limit keeps its text but ends with an error row saying so, in the
// stream's completed session and in the stored conversation; a reply that ended on its own has none.
func TestAgentChatMarksRepliesCutOffAtTheOutputLimit(t *testing.T) {
	provider := &thinkingChatProvider{}
	_, handler := newThinkingChatServer(t, provider)
	provider.turns = []string{
		`data: {"choices":[{"delta":{"content":"1. Book the room.\n2. Share the"},"finish_reason":"length"}]}` + "\n\ndata: [DONE]\n\n",
		chatText("All done."),
	}
	stream := streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"List 200 tips.","location":"page"}`)
	if !strings.Contains(stream, agentOutputLimitError) {
		t.Fatalf("the completed session must carry the output-limit row: %s", stream)
	}
	session := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)[0]
	reply := session.Messages[len(session.Messages)-1]
	last := reply.Parts[len(reply.Parts)-1]
	if reply.Content != "1. Book the room.\n2. Share the" || last.Type != "error" || last.Text != agentOutputLimitError {
		t.Fatalf("stored reply = %q %+v", reply.Content, reply.Parts)
	}

	streamRuntime(t, handler, "/api/agent/sessions/"+session.ID+"/messages/stream", `{"message":"Thanks."}`)
	session = requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	reply = session.Messages[len(session.Messages)-1]
	for _, part := range reply.Parts {
		if part.Type == "error" {
			t.Fatalf("a finished reply must not be marked incomplete: %+v", reply.Parts)
		}
	}
	if reply.Content != "All done." {
		t.Fatalf("second reply = %q", reply.Content)
	}
}

func TestAgentTurnTruncated(t *testing.T) {
	for reason, want := range map[string]bool{"length": true, "max_tokens": true, "incomplete": true, "stop": false, "end_turn": false, "completed": false, "tool_calls": false, "": false} {
		if got := agentTurnTruncated(reason); got != want {
			t.Errorf("agentTurnTruncated(%q) = %v", reason, got)
		}
	}
}
