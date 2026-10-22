package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
)

// The follow-up scope model is the configured Flow Agent model; a provider
// failure or an unusable answer leaves the parser's decision in place.
func TestCodingTurnScopeModelWithFakeProvider(t *testing.T) {
	reply, status, calls := "readOnly", http.StatusOK, 0
	var system, user string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		system, user = body.Messages[0].Content, body.Messages[len(body.Messages)-1].Content
		if status != http.StatusOK {
			http.Error(w, `{"error":{"message":"overloaded"}}`, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": "stop"}}})
	}))
	defer provider.Close()
	s := &server{agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, Model: "model", MaxOutputTokens: 200}, agentClient: provider.Client()}
	model := coding.TurnScopeModelFunc(s.classifyCodingTurnScope)
	ctx := context.Background()
	const unsure = "Why does the build fail on CI?"

	if got := coding.ResolveTurnScope(ctx, coding.TurnFollowUp, "", unsure, model); got.Scope != coding.TurnScopeReadOnly || got.Source != coding.TurnScopeSourceModel {
		t.Fatalf("model answer: %+v", got)
	}
	if !strings.Contains(system, "Reply with exactly one word") || !strings.Contains(user, unsure) {
		t.Fatalf("prompt: %q / %q", system, user)
	}
	reply = "**noPublish**."
	if got := coding.ResolveTurnScope(ctx, coding.TurnFollowUp, "", unsure, model); got.Scope != coding.TurnScopeNoPublish {
		t.Fatalf("decorated answer: %+v", got)
	}
	// Sure requests and explicit scopes never reach the provider.
	before := calls
	coding.ResolveTurnScope(ctx, coding.TurnFollowUp, "", "先改一下，但不要提交。", model)
	coding.ResolveTurnScope(ctx, coding.TurnFollowUp, "write", unsure, model)
	if calls != before {
		t.Fatalf("provider called for a sure or explicit scope: %d calls", calls-before)
	}
	// Failures fall back to the parser.
	reply = "It depends on what you want."
	if got := coding.ResolveTurnScope(ctx, coding.TurnFollowUp, "", unsure, model); got.Scope != coding.TurnScopeWrite || got.Source != coding.TurnScopeSourceRules {
		t.Fatalf("unusable answer: %+v", got)
	}
	status = http.StatusServiceUnavailable
	if got := coding.ResolveTurnScope(ctx, coding.TurnFollowUp, "", "Just check the README and fix the typos.", model); got.Scope != coding.TurnScopeNoPublish || got.Source != coding.TurnScopeSourceRules {
		t.Fatalf("provider error: %+v", got)
	}
	// No Flow Agent model: no call at all.
	off := &server{}
	if got := coding.ResolveTurnScope(ctx, coding.TurnFollowUp, "", unsure, coding.TurnScopeModelFunc(off.classifyCodingTurnScope)); got.Source != coding.TurnScopeSourceRules {
		t.Fatalf("agent off: %+v", got)
	}
}
