package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A loop made in a team works for that team: its prompt says so, and its
// tools can only read the teams its "Team access" allows.
func TestLoopRunsStayInTheirTeams(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	service := &server{store: repository, uploadPath: t.TempDir()}
	data := repository.Bootstrap()
	home := data.Teams[0]
	other := domain.Team{ID: "team_other", Key: "OTH", Name: "Other"}
	now := time.Now().UTC()
	if err := repository.MutateWorkspace(ctx, actor.WorkspaceKey, "test.teams", other.ID, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, other)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	created, err := service.callFlowTool(ctx, actor, "save_issue", map[string]any{"title": "Other team weekly item", "team": other.ID})
	if err != nil {
		t.Fatal(err)
	}
	otherIssue := created.(map[string]any)["identifier"].(string)
	data = repository.Bootstrap()

	loop := domain.Loop{ID: "loop_weekly", Name: "Team weekly summary", TeamID: home.ID, TeamAccess: "selected", Instructions: "Write the team's weekly summary.", CreatedAt: now}
	scope := service.newLoopScope(data, loop, loopTrigger{Kind: "schedule"})
	key := scope.toolKey()
	if !apiKeyTeamRestrictionSelected(key) || !slices.Equal(key.TeamIDs, []string{home.ID}) || !slices.Contains(key.Scopes, "write") {
		t.Fatalf("run key = %+v, want read/write limited to %s", key, home.ID)
	}
	actor.APIKey = key
	if _, err := service.callFlowTool(ctx, actor, "get_issue", map[string]any{"id": otherIssue}); err == nil {
		t.Fatal("a team loop read an issue from a team outside its access")
	}
	listed, err := service.callFlowTool(ctx, actor, "list_issues", map[string]any{"limit": 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range listed.(map[string]any)["items"].([]domain.Issue) {
		if item.Identifier == otherIssue {
			t.Fatal("a team loop listed an issue from a team outside its access")
		}
	}

	// With "All public teams" it may read every public team, but the prompt
	// still tells the model whose loop it is.
	loop.TeamAccess = "allPublic"
	scope = service.newLoopScope(data, loop, loopTrigger{Kind: "schedule"})
	if ids := scope.toolKey().TeamIDs; !slices.Contains(ids, home.ID) || !slices.Contains(ids, other.ID) {
		t.Fatalf("all-public run key = %v, want both public teams", ids)
	}
	prompt := loopSystemPrompt(data, loop, loopTrigger{Kind: "schedule"}, "", scope, false, "", loopRunPlan{Limits: service.loopLimits()})
	if !strings.Contains(prompt, "This loop belongs to the "+home.Name+" team (key "+home.Key+")") {
		t.Fatalf("prompt does not name the loop's team:\n%s", prompt)
	}
	workspaceLoop := loop
	workspaceLoop.TeamID = ""
	if strings.Contains(loopSystemPrompt(data, workspaceLoop, loopTrigger{Kind: "schedule"}, "", scope, false, "", loopRunPlan{Limits: service.loopLimits()}), "This loop belongs to") {
		t.Fatal("a workspace loop's prompt names a team")
	}
}
