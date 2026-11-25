package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP18LargeAIContextKeepsCompatibility(t *testing.T) {
	repo, actor, ctx := scaleP18Fixture(t)
	service := &server{store: repo}
	long := strings.Repeat("x", 3<<20)
	if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "scale_p18.seeded", "", nil, func(data *domain.Bootstrap) error {
		data.AIConversations = []domain.AIConversation{{ID: "p18-large", UserID: actor.User.ID, Context: map[string]any{"history": long}}, {ID: "p18-keep", UserID: actor.User.ID, Context: map[string]any{"keep": true}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, mode := range []string{"off", "on"} {
		t.Setenv("FLOW_INTERNAL_CONVERSATION_RECORDS", mode)
		r := httptest.NewRequest(http.MethodPatch, "/api/ai/conversations/p18-large", strings.NewReader(`{"title":"`+mode+`"}`)).WithContext(ctx)
		r.SetPathValue("id", "p18-large")
		w := httptest.NewRecorder()
		service.updateAIConversation(w, r)
		var saved domain.AIConversation
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Title != mode || saved.Context["history"] != long {
			t.Fatalf("%s large conversation changed: status=%d response_bytes=%d", mode, w.Code, w.Body.Len())
		}
	}
}

func TestScaleP18ConversationSlugAndWorkerIDCompatibility(t *testing.T) {
	repo, actor, ctx := scaleP18Fixture(t)
	service := &server{store: repo}
	deleted := time.Now().UTC()
	if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "scale_p18.seeded", "", nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = []domain.AgentSession{
			{ID: "p18-deleted", SlugID: "p18-live", UserID: actor.User.ID, DeletedAt: &deleted},
			{ID: "p18-live", SlugID: "p18-shared", UserID: actor.User.ID, Messages: []domain.AgentMessage{{ID: "p18-message", Content: "Complete reply", Status: agentMessageStreaming}}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"off", "on"} {
		t.Setenv("FLOW_INTERNAL_CONVERSATION_RECORDS", mode)
		for _, ref := range []string{"p18-live", "p18-shared"} {
			r := httptest.NewRequest(http.MethodGet, "/api/agent/sessions/"+ref, nil).WithContext(ctx)
			r.SetPathValue("id", ref)
			w := httptest.NewRecorder()
			service.getAgentSession(w, r)
			var session domain.AgentSession
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &session) != nil || session.ID != "p18-live" {
				t.Fatalf("%s ref=%s resolved a deleted alias: status=%d", mode, ref, w.Code)
			}
		}
		reply, streaming := service.workerChatStoredReply(ctx, actor.WorkspaceKey, "p18-live", actor.User.ID, "p18-message")
		if !streaming || reply.Content != "Complete reply" {
			t.Fatalf("%s worker ID was shadowed by another session's slug", mode)
		}
		if _, err := service.internalAgentConversation(ctx, actor.WorkspaceKey, actor.User.ID, "p18-deleted", false); err != nil {
			t.Fatalf("worker recovery lost a deleted session: %v", err)
		}
	}
}
