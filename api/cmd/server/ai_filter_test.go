package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

func TestAIIssueFilterValidatesModelChoices(t *testing.T) {
	var prompt string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		prompt = body.Messages[len(body.Messages)-1].Content
		reply := "Here you go: {\"filters\":[{\"field\":\"priority\",\"ids\":[\"1\"]},{\"field\":\"labels\",\"ids\":[\"bug\",\"invented\"]},{\"field\":\"secret\",\"ids\":[\"x\"]}],\"text\":\"checkout\"}"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": "stop"}}})
	}))
	defer provider.Close()
	s := &server{agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, Model: "model", MaxOutputTokens: 200}, agentClient: provider.Client()}
	input := `{"query":"urgent checkout bugs","viewerName":"Ada","fields":{"priority":[{"id":"1","label":"Urgent"}],"labels":[{"id":"bug","label":"Bug"}]}}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ai/issue-filter", strings.NewReader(input))
	request.Header.Set("Content-Type", "application/json")
	s.aiIssueFilter(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var got struct {
		Filters []aiFilterResult `json:"filters"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []aiFilterResult{{Field: "priority", Option: aiFilterOption{ID: "1", Label: "Urgent"}}, {Field: "labels", Option: aiFilterOption{ID: "bug", Label: "Bug"}}, {Field: "content", Option: aiFilterOption{ID: "query:checkout", Label: "checkout"}}}
	if len(got.Filters) != len(want) {
		t.Fatalf("filters=%#v", got.Filters)
	}
	for index := range want {
		if got.Filters[index] != want[index] {
			t.Fatalf("filters=%#v", got.Filters)
		}
	}
	if !strings.Contains(prompt, `"bug"=Bug`) || !strings.Contains(prompt, "Current user: Ada") {
		t.Fatalf("prompt missing vocabulary: %s", prompt)
	}

	// Without viewerName the signed-in person is the current user, and their assignee option is marked "(me)", so
	// "assigned to me" / "分配给我" resolve on every page.
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/ai/issue-filter", strings.NewReader(`{"query":"分配给我的高优先级 Bug","fields":{"assignee":[{"id":"usr_dev","label":"Dev User"},{"id":"usr_qa","label":"QA member"}]}}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, domain.User{ID: "usr_dev", Name: "Dev User", DisplayName: "Dev User"}))
	s.aiIssueFilter(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(prompt, "Current user: Dev User") || !strings.Contains(prompt, `"usr_dev"=Dev User (me)`) || strings.Contains(prompt, "QA member (me)") {
		t.Fatalf("status %d, prompt: %s", recorder.Code, prompt)
	}

	disabled := &server{}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/ai/issue-filter", strings.NewReader(input))
	request.Header.Set("Content-Type", "application/json")
	disabled.aiIssueFilter(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without an Agent, got %d", recorder.Code)
	}
}
