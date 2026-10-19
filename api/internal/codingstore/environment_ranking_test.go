package codingstore

import (
	"context"
	"testing"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// An ambiguous repository: the trigger's ranked suggestions (track L) reach
// the provisioning select elicitation (track B) in their order, with the
// clear leader preselected.
func TestAmbiguousRepositoryUsesTriggerRanking(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	envs := fakeEnvs{envs: []domain.CodingEnvironment{
		{ID: "env-api", Name: "API", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/api", DefaultBranch: "main"},
		{ID: "env-docs", Name: "Docs", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/docs", DefaultBranch: "main"},
		{ID: "env-web", Name: "Web", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/web", DefaultBranch: "main"},
	}}
	h.svc = coding.NewService(coding.Options{Config: h.cfg, Store: h.store, Anchors: h.store, Workspaces: harnessWorkspaces{h}, Environments: envs, Hosts: map[string]codehost.Host{domain.CodeHostGitHub: h.host}})
	starter := coding.ServiceStarter{Service: h.svc}

	cases := []struct {
		name        string
		suggestions []coding.EnvironmentSuggestion
		order       []string
		selected    string
	}{
		{
			name: "clear leader is preselected",
			suggestions: []coding.EnvironmentSuggestion{
				{EnvironmentID: "env-web", Score: 60}, {EnvironmentID: "env-api", Score: 35}, {EnvironmentID: "env-docs", Score: 0},
			},
			order: []string{"env-web", "env-api", "env-docs"}, selected: "env-web",
		},
		{
			name: "tie keeps the order without a preselection",
			suggestions: []coding.EnvironmentSuggestion{
				{EnvironmentID: "env-docs", Score: 10}, {EnvironmentID: "env-web", Score: 10},
			},
			order: []string{"env-docs", "env-web", "env-api"},
		},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session, _, err := starter.StartCodingSession(ctx, coding.TriggerStartRequest{
				WorkspaceKey: workspace, Source: domain.CodingSourceIssue, Prompt: "Fix the build", CreatorID: "user1",
				AwaitEnvironment: true, EnvironmentSuggestions: tc.suggestions, IdempotencyKey: "rank-" + string(rune('a'+index)),
			})
			if err != nil {
				t.Fatal(err)
			}
			if session.PreferredEnvironmentID != tc.selected {
				t.Fatalf("preferred = %q, want %q", session.PreferredEnvironmentID, tc.selected)
			}
			h.drive(session.ID, domain.CodingPhaseAwaitingUser)
			var elicitation *domain.AgentActivity
			for _, activity := range h.activities(session.AgentSessionID) {
				if activity.Type == "elicitation" && activity.Signal == "select" {
					elicitation = &activity
				}
			}
			if elicitation == nil {
				t.Fatal("no repository elicitation")
			}
			options, _ := elicitation.SignalMetadata["options"].([]any)
			got := []string{}
			for _, raw := range options {
				option, _ := raw.(map[string]any)
				value, _ := option["value"].(string)
				got = append(got, value)
				if recommended, _ := option["recommended"].(bool); recommended != (value == tc.selected && tc.selected != "") {
					t.Errorf("option %s recommended=%v", value, recommended)
				}
			}
			if len(got) != len(tc.order) {
				t.Fatalf("options = %v, want %v", got, tc.order)
			}
			for i := range got {
				if got[i] != tc.order[i] {
					t.Fatalf("options = %v, want %v", got, tc.order)
				}
			}
			if selected, _ := elicitation.SignalMetadata["selected"].(string); selected != tc.selected {
				t.Fatalf("selected = %q, want %q", selected, tc.selected)
			}
		})
	}
}
