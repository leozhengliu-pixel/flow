package main

import (
	"testing"

	"flow/api/internal/domain"
)

func TestAgentReplyKindFinishesDelegatedCodingSessions(t *testing.T) {
	response := domain.AgentActivity{Type: "response"}
	delegated := domain.AgentTask{CodingSessionID: "cs_1"}
	if got := agentReplyKind(delegated, response); got != "finished" {
		t.Fatalf("the final response of a delegated coding session is finished, got %q", got)
	}
	if got := agentReplyKind(domain.AgentTask{CodingSessionID: "cs_1", CommentID: "c1"}, response); got != "response" {
		t.Fatalf("an answer to a comment stays a reply, got %q", got)
	}
	if got := agentReplyKind(domain.AgentTask{}, response); got != "response" {
		t.Fatalf("a non-coding reply stays a reply, got %q", got)
	}
	if got := agentReplyKind(delegated, domain.AgentActivity{Type: "elicitation"}); got != "elicitation" {
		t.Fatalf("questions and errors keep their kind, got %q", got)
	}
	if got := agentReplyNotificationText("Flow", "finished", "Added farewell(name)"); got != "Flow finished: Added farewell(name)" {
		t.Fatalf("text = %q", got)
	}
	if got := agentReplyNotificationText("Flow", "finished", ""); got != "Flow finished" {
		t.Fatalf("text = %q", got)
	}
}
