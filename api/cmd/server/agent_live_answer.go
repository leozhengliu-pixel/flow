package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"flow/api/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A waiting approval or connector question lives in the process running the reply. With several API servers (Redis
// coordination) the owner's answer, or Stop, can reach a server that does not run it; that server checks the saved
// reply shows the request waiting, then forwards the answer over the coordination channel, and the server running the
// reply hands it to the run (the same checks as a local answer: workspace, chat, owner). Without coordination there is
// a single server, so a request it does not hold has expired.

const agentAnswerEventType = "agent_reply.answer"

const agentAnswerElsewhereError = "This request is waiting on another server that cannot be reached. Try again in a moment."

type agentForwardedAnswer struct {
	Kind        string            `json:"kind"` // approval, elicitation or stop
	ID          string            `json:"id,omitempty"`
	SessionID   string            `json:"sessionId"`
	UserID      string            `json:"userId"`
	Decision    string            `json:"decision,omitempty"`
	Elicitation *mcp.ElicitResult `json:"elicitation,omitempty"`
}

// forwardAgentAnswer publishes answer to the other servers; false when there are none or publishing failed.
func (s *server) forwardAgentAnswer(r *http.Request, answer agentForwardedAnswer) bool {
	if s.coordinator == nil {
		return false
	}
	payload, err := json.Marshal(answer)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	event := domain.RealtimeEvent{ID: fmt.Sprintf("agent_answer_%d", time.Now().UnixNano()), Type: agentAnswerEventType, Payload: payload, CreatedAt: time.Now().UTC()}
	if err := s.coordinator.Publish(ctx, workspaceKey(r), event); err != nil {
		log.Printf("forward agent %s answer: %v", answer.Kind, err)
		return false
	}
	return true
}

// receiveForwardedAgentAnswer applies a peer's forwarded answer when this process runs the reply. It never blocks:
// the waiting run reads from a buffered channel.
func (s *server) receiveForwardedAgentAnswer(workspace string, event domain.RealtimeEvent) {
	var answer agentForwardedAnswer
	if json.Unmarshal(event.Payload, &answer) != nil || answer.SessionID == "" || answer.UserID == "" {
		return
	}
	switch answer.Kind {
	case "approval":
		s.answerAgentApproval(workspace, answer.SessionID, answer.UserID, answer.ID, answer.Decision)
	case "elicitation":
		if answer.Elicitation != nil {
			s.answerAgentElicitation(workspace, answer.SessionID, answer.UserID, answer.ID, *answer.Elicitation)
		}
	case "stop":
		if s.agentRunOwnedBy(workspace, answer.SessionID, answer.UserID) {
			s.stopAgentRun(workspace, answer.SessionID)
		}
	}
}

// agentRunOwnedBy reports whether the chat sessionID in workspace belongs to userID.
func (s *server) agentRunOwnedBy(workspace, sessionID, userID string) bool {
	data, ok := s.store.WorkspaceMetadataFields(workspace, "agentSessions")
	if !ok {
		return false
	}
	for _, session := range data.AgentSessions {
		if session.ID == sessionID {
			return session.UserID == userID && session.DeletedAt == nil
		}
	}
	return false
}
