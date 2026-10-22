package coding

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

func contextWindowLLM() config.CodingLLMConfig {
	return config.CodingLLMConfig{
		CodexModels:      []string{"glm-5", "gpt-5-codex"},
		ClaudeCodeModels: []string{"glm-5"},
		OpenSourceModels: []string{"glm-5"},
		ModelInfo:        map[string]config.ModelInfo{"glm-5": {Label: "GLM-5", ContextWindow: 200000}},
	}
}

// Only a Codex session on a model with a configured window gets one.
func TestCodexContextWindowPerModelAndHarness(t *testing.T) {
	llm := contextWindowLLM()
	for _, tc := range []struct {
		harness, model string
		want           int64
	}{
		{domain.CodingHarnessCodex, "glm-5", 200000},
		{"auto", "glm-5", 200000}, // legacy alias of codex
		{domain.CodingHarnessCodex, "gpt-5-codex", 0},
		{domain.CodingHarnessCodex, "unknown", 0},
		{domain.CodingHarnessClaudeCode, "glm-5", 0},
		{domain.CodingHarnessOpenSource, "glm-5", 0},
	} {
		if got := CodexContextWindow(llm, tc.harness, tc.model); got != tc.want {
			t.Errorf("%s/%s: %d, want %d", tc.harness, tc.model, got, tc.want)
		}
	}
	if got := CodexContextWindow(config.CodingLLMConfig{}, domain.CodingHarnessCodex, "glm-5"); got != 0 {
		t.Errorf("no model info: %d", got)
	}
}

// The bootstrap carries harness.contextWindow only when it is set, so the
// runner writes model_context_window for GLM-5 and nothing for the others.
func TestBootstrapContextWindow(t *testing.T) {
	service, _, _ := memberFixture(domain.CodingPhaseRunning)
	service.workspaces = staticWorkspaces{}
	service.cfg.LLM = contextWindowLLM()
	base := domain.CodingSession{ID: "s-alice", WorkspaceKey: "w", CreatorID: "alice", SandboxID: "sb-alice", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app", BranchName: "alice/eng-1"}
	for _, tc := range []struct {
		harness, model, resume string
		want                   int64
	}{
		{domain.CodingHarnessCodex, "glm-5", "", 200000},
		{domain.CodingHarnessCodex, "glm-5", "thread-1", 200000}, // resumed turn
		{domain.CodingHarnessCodex, "gpt-5-codex", "", 0},
		{domain.CodingHarnessClaudeCode, "glm-5", "", 0},
		{domain.CodingHarnessOpenSource, "glm-5", "", 0},
	} {
		session := base
		session.Harness, session.Model, session.HarnessResumeID = tc.harness, tc.model, tc.resume
		response, err := service.buildBootstrap(context.Background(), SandboxGrant{SandboxID: "sb-alice", SessionID: "s-alice"}, session, nil)
		if err != nil {
			t.Fatal(err)
		}
		if response.Harness.ContextWindow != tc.want {
			t.Errorf("%s/%s: contextWindow %d, want %d", tc.harness, tc.model, response.Harness.ContextWindow, tc.want)
		}
		raw, _ := json.Marshal(response.Harness)
		if has := strings.Contains(string(raw), `"contextWindow"`); has != (tc.want > 0) {
			t.Errorf("%s/%s: harness JSON %s", tc.harness, tc.model, raw)
		}
		if tc.want > 0 && !strings.Contains(string(raw), `"contextWindow":200000`) {
			t.Errorf("window must serialize as a JSON integer: %s", raw)
		}
	}
}

// A bootstrap from before the field (or without it) still decodes.
func TestBootstrapHarnessWithoutContextWindowDecodes(t *testing.T) {
	var harness BootstrapHarness
	if err := json.Unmarshal([]byte(`{"name":"codex","model":"gpt-5","resumeId":"t"}`), &harness); err != nil || harness.ContextWindow != 0 || harness.Model != "gpt-5" {
		t.Fatalf("%+v %v", harness, err)
	}
	if err := json.Unmarshal([]byte(`{"name":"codex","model":"glm-5","contextWindow":200000}`), &harness); err != nil || harness.ContextWindow != 200000 {
		t.Fatalf("%+v %v", harness, err)
	}
}
