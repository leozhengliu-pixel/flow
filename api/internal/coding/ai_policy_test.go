package coding

import (
	"context"
	"errors"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
)

type aiPolicyWorkspaces struct{}

func (aiPolicyWorkspaces) CodingWorkspace(context.Context, string) (WorkspaceInfo, error) {
	return WorkspaceInfo{Settings: domain.WorkspaceSettings{FeatureFlags: map[string]bool{FeatureFlag: true}}}, nil
}

// Every action that leads to a model call asks the workspace AI policy first,
// for the person acting, and the LLM proxy asks it for each model request.
func TestServiceAsksTheWorkspaceAIPolicy(t *testing.T) {
	refuse := errors.New("AI is off")
	var asked []string
	policy := func(_ context.Context, workspace, userID string) error {
		asked = append(asked, workspace+"/"+userID)
		return refuse
	}
	done, _, _ := memberFixture(domain.CodingPhaseDone)
	done.cfg.Enabled = true
	done.workspaces = aiPolicyWorkspaces{}
	done.AIPolicy = policy
	ctx := t.Context()
	if _, _, err := done.Start(ctx, StartInput{WorkspaceKey: "w", CreatorID: "alice", Prompt: "Fix it", Source: domain.CodingSourceAgentChat}); !errors.Is(err, ErrAIPolicy) {
		t.Fatalf("Start: %v", err)
	}
	if _, err := done.Prompt(ctx, "w", "s-alice", PromptInput{Prompt: "And the docs", AuthorID: "bob"}); !errors.Is(err, ErrAIPolicy) {
		t.Fatalf("Prompt: %v", err)
	}
	if _, err := done.Restart(ctx, "w", "s-alice", "alice", false); !errors.Is(err, ErrAIPolicy) {
		t.Fatalf("Restart: %v", err)
	}
	if _, err := done.StartOver(ctx, "w", "s-alice", "alice", "gpt-5-codex"); !errors.Is(err, ErrAIPolicy) {
		t.Fatalf("StartOver: %v", err)
	}
	if _, err := done.Approve(ctx, "w", "s-alice", "carol", false); !errors.Is(err, ErrAIPolicy) {
		t.Fatalf("Approve: %v", err)
	}
	want := []string{"w/alice", "w/bob", "w/alice", "w/alice", "w/carol"}
	if len(asked) != len(want) {
		t.Fatalf("asked %v, want %v", asked, want)
	}
	for index := range want {
		if asked[index] != want[index] {
			t.Fatalf("asked %v, want %v", asked, want)
		}
	}

	running, _, _ := memberFixture(domain.CodingPhaseRunning)
	if _, err := running.AuthenticateLLMCaller(ctx, SandboxTokenPrefix+"s-alice"); err != nil {
		t.Fatalf("without a policy the caller is allowed: %v", err)
	}
	running.AIPolicy = policy
	if _, err := running.AuthenticateLLMCaller(ctx, SandboxTokenPrefix+"s-alice"); !errors.Is(err, llmproxy.ErrPolicy) {
		t.Fatalf("AuthenticateLLMCaller: %v", err)
	}
}
