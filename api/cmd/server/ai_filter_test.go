package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
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

	disabled := &server{}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/ai/issue-filter", strings.NewReader(input))
	request.Header.Set("Content-Type", "application/json")
	disabled.aiIssueFilter(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without an Agent, got %d", recorder.Code)
	}
}
