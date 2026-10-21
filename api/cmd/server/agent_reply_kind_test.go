package main

import (
	"testing"
	"time"

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

func TestAgentReplyNotificationsShareOneRowPerIssue(t *testing.T) {
	user := domain.User{ID: "u1", Name: "Sky"}
	agent := domain.User{ID: "agent1", Name: "Flow", DisplayName: "Flow", App: true}
	data := &domain.Bootstrap{}
	thread := agentReplyThread{issueID: "issue1", sourceType: "issue", muted: map[string]bool{},
		user: func(id string) *domain.User {
			if id == "u1" {
				return &user
			}
			return nil
		}, canOpen: func(string) bool { return true }}
	withCodingSessionAudience(&thread, &domain.CodingSession{ID: "cs_1", LatestPullRequest: &domain.SessionPullRequest{ReviewID: "review1"}})
	now := time.Now().UTC()
	finished := domain.AgentTask{ID: "task1", CreatorID: "u1", CodingSessionID: "cs_1"}
	appendAgentReplyNotifications(data, thread, finished, domain.AgentActivity{ID: "a1", Type: "response", Body: "Done"}, agent, nil, now)
	if len(data.Notifications) != 1 || data.Notifications[0].Payload.AgentActivity != "finished" || data.Notifications[0].Payload.ReviewID != "review1" || data.Notifications[0].Payload.CodingSessionID != "cs_1" {
		t.Fatalf("finished notification = %+v", data.Notifications)
	}
	answer := domain.AgentTask{ID: "task2", CreatorID: "u1", CodingSessionID: "cs_1", CommentID: "c1"}
	appendAgentReplyNotifications(data, thread, answer, domain.AgentActivity{ID: "a2", Type: "response", Body: "Use node:test"}, agent, nil, now.Add(time.Minute))
	if len(data.Notifications) != 1 {
		t.Fatalf("one issue is one row, got %d", len(data.Notifications))
	}
	if got := data.Notifications[0].Payload.AgentActivity; got != "response" {
		t.Fatalf("the newest item wins, got %q", got)
	}
}
