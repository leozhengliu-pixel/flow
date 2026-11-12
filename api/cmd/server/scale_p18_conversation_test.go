package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
