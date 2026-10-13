package main

import (
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestWorkspaceAgentPromptTellsTheModelHowToLinkEveryResource(t *testing.T) {
	data := domain.Bootstrap{Workspace: domain.Workspace{Name: "Dev Workspace", URLKey: "dev-workspace"}}
	prompt := workspaceAgentSystemPrompt(data, nil, nil)
	for _, want := range []string{
		"Resource links:",
		"/dev-workspace/issue/ENG-12",
		"/dev-workspace/project/{slugId}/overview",
		"/dev-workspace/initiative/{slugId}/overview",
		"/dev-workspace/document/{slugId}",
		"/dev-workspace/team/{key}/overview",
		"/dev-workspace/team/{teamKey}/cycle/{number}",
		"/dev-workspace/issue-label/{name}",
		"#milestone-{milestoneId}",
		"/dev-workspace/customer/{lowercase-hyphenated-name}-{last 12 characters of the customer id}",
		"/dev-workspace/pipeline/{pipelineSlugId}/release/{releaseSlugId}/issues",
		"/dev-workspace/view/{viewSlugId}",
		"/dev-workspace/review/{slugId}",
		"#update-{updateId}",
		"@Display Name",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}

// Answers that put a made-up host in front of a workspace path ("https://app.flow.dev/…") link to the app instead.
func TestRebaseAgentWorkspaceLinks(t *testing.T) {
	base := "http://127.0.0.1:5173"
	for _, tc := range []struct{ in, want string }{
		{"Created [Q4](https://app.flow.dev/dev-workspace/initiative/q4-reliability/overview).", "Created [Q4](http://127.0.0.1:5173/dev-workspace/initiative/q4-reliability/overview)."},
		{"[Cycle 1](https://flow.dev/dev-workspace/team/DEV/cycle/1) and [P](https://flow.test/dev-workspace/project/qa-项目-alpha/overview#milestone-m1)", "[Cycle 1](http://127.0.0.1:5173/dev-workspace/team/DEV/cycle/1) and [P](http://127.0.0.1:5173/dev-workspace/project/qa-项目-alpha/overview#milestone-m1)"},
		{"See https://app.flow.dev/dev-workspace/issue/DEV-1 now", "See http://127.0.0.1:5173/dev-workspace/issue/DEV-1 now"},
		{"<https://x.example/dev-workspace/document/notes-1>", "<http://127.0.0.1:5173/dev-workspace/document/notes-1>"},
		{"[Team](https://dev-workspace/team/DEV/overview)", "[Team](http://127.0.0.1:5173/dev-workspace/team/DEV/overview)"},
		{"[DEV-212](https://dev-workspace.flow.dev/issue/DEV-212)", "[DEV-212](http://127.0.0.1:5173/dev-workspace/issue/DEV-212)"},
		{"[Cycle 1](https://flow.dev/team/DEV/cycle/1)", "[Cycle 1](http://127.0.0.1:5173/dev-workspace/team/DEV/cycle/1)"},
		{"[DEV](https://127.0.0.1:5173/dev-workspace/team/DEV/overview)", "[DEV](http://127.0.0.1:5173/dev-workspace/team/DEV/overview)"},
		{"[Loop](https://app.flow.dev/dev-workspace/loop/loop_1)", "[Loop](http://127.0.0.1:5173/dev-workspace/loop/loop_1)"},
		// Already on the app, other workspaces, other paths and real external sites stay as written.
		{"[gh](https://github.com/team/DEV/issues)", "[gh](https://github.com/team/DEV/issues)"},
		{"[docs](https://flow.dev/docs/agents)", "[docs](https://flow.dev/docs/agents)"},
		{"[DEV-1](http://127.0.0.1:5173/dev-workspace/issue/DEV-1)", "[DEV-1](http://127.0.0.1:5173/dev-workspace/issue/DEV-1)"},
		{"[x](https://app.flow.dev/other-workspace/issue/DEV-1)", "[x](https://app.flow.dev/other-workspace/issue/DEV-1)"},
		{"[docs](https://docs.example.com/dev-workspace/guide)", "[docs](https://docs.example.com/dev-workspace/guide)"},
		{"[PR](https://github.com/acme/flow/pull/12)", "[PR](https://github.com/acme/flow/pull/12)"},
		{"[DEV-1](/dev-workspace/issue/DEV-1)", "[DEV-1](/dev-workspace/issue/DEV-1)"},
	} {
		if got := rebaseAgentWorkspaceLinks(tc.in, "dev-workspace", base); got != tc.want {
			t.Errorf("rebase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := rebaseAgentWorkspaceLinks("[a](https://app.flow.dev/dev-workspace/issue/DEV-1)", "dev-workspace", ""); got != "[a](https://app.flow.dev/dev-workspace/issue/DEV-1)" {
		t.Errorf("without a base URL the text must be unchanged, got %q", got)
	}
}
