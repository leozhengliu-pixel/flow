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
