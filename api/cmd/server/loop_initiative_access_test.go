package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// Initiatives are workspace-level and reach teams through their projects (or
// lead and contributing teams). A loop's tools see the initiatives that touch
// its teams; one that touches only other teams is refused with the team-access
// reason, never answered with "not found".
func TestLoopInitiativeAccessFollowsLinkedProjects(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	other := domain.Team{ID: "team_other", Key: "OTH", Name: "Other"}
	if err := srv.store.MutateWorkspace(context.Background(), "test-workspace", "test.teams", other.ID, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, other)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	home := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0]
	mobile := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Mobile App", "teamIds": []string{home.ID}}, http.StatusCreated)
	backend := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Backend", "teamIds": []string{other.ID}}, http.StatusCreated)
	newInitiative := func(name string, projectIDs ...string) domain.Initiative {
		created := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": name}, http.StatusCreated)
		if len(projectIDs) > 0 {
			created = requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID, map[string]any{"projectIds": projectIDs}, http.StatusOK)
		}
		return created
	}
	linked := newInitiative("Growth push", mobile.ID)
	both := newInitiative("Platform bets", mobile.ID, backend.ID)
	unlinked := newInitiative("Company goals")
	foreign := newInitiative("Infra rewrite", backend.ID)

	instructions := "Post a weekly update on the initiatives."
	setLoopTestContract(instructions, `{"readOnly":false,"outputs":[{"kind":"statusUpdate","operation":"create","required":true,"target":{"mode":"any"}}],"always":false,"skipConditions":[]}`)
	post := func(ref string) string {
		return `tool:save_status_update {"type":"initiative","initiative":"` + ref + `","body":"Update.","health":"onTrack"}`
	}
	setLoopLimits(srv, func(limits *loopRunLimits) { limits.MaxTurns, limits.MaxToolCalls = 16, 30 })
	provider.script(
		post(linked.ID),
		post("Growth push"),
		post(both.ID),
		post(unlinked.ID),
		post(foreign.ID),
		post("Infra rewrite"),
		`tool:list_initiatives {}`,
		`tool:get_initiative {"query":"`+linked.ID+`"}`,
		`tool:get_initiative {"query":"`+foreign.ID+`"}`,
		`tool:finish_run {"status":"done","summary":"Posted."}`,
	)
	loop, started := startTestRun(t, handler, "Initiative updates", instructions, map[string]any{"teamId": home.ID, "level": "team", "teamAccess": "selected"})
	run := waitForRun(t, handler, loop.ID, started.ID)

	for index, name := range []string{"by id", "by name", "linked to both teams", "linked to no team"} {
		call := loopRunCall(run, "save_status_update", index)
		if call.Status != "completed" || call.Error != "" {
			t.Errorf("status update on the initiative %s = %s %q", name, call.Status, call.Error)
		}
	}
	for index, name := range []string{"by id", "by name"} {
		call := loopRunCall(run, "save_status_update", 4+index)
		if call.Status != "blocked" || !strings.Contains(call.Error, "outside the loop's team access") || strings.Contains(call.Error, "not found") {
			t.Errorf("status update on the other team's initiative %s = %s %q", name, call.Status, call.Error)
		}
	}
	for _, id := range []string{linked.ID, both.ID, unlinked.ID} {
		if updates := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).InitiativeUpdates[id]; len(updates) == 0 {
			t.Errorf("initiative %s has no status update", id)
		}
	}
	if updates := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).InitiativeUpdates[foreign.ID]; len(updates) != 0 {
		t.Errorf("the other team's initiative got %d updates", len(updates))
	}

	// The read tools show the same set: what a run may post to, not more.
	listed := loopToolResult(t, provider, 6)
	for _, item := range []domain.Initiative{linked, both, unlinked} {
		if !strings.Contains(listed, item.Name) {
			t.Errorf("list_initiatives lacks %q: %s", item.Name, listed)
		}
	}
	if strings.Contains(listed, foreign.Name) {
		t.Errorf("list_initiatives shows the other team's initiative: %s", listed)
	}
	if got := loopRunCall(run, "get_initiative", 0); got.Status != "completed" || got.Error != "" {
		t.Errorf("get_initiative on a linked initiative = %s %q", got.Status, got.Error)
	}
	if got := loopRunCall(run, "get_initiative", 1); got.Status == "completed" {
		t.Errorf("get_initiative on the other team's initiative succeeded")
	}
}

// loopToolResult is what the tool call made in request n returned to the
// model: the part of the next request that the previous one did not have.
func loopToolResult(t *testing.T, provider *fakeLoopProvider, n int) string {
	t.Helper()
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.inputs) <= n+1 {
		t.Fatalf("only %d requests", len(provider.inputs))
	}
	before, after := provider.inputs[n], provider.inputs[n+1]
	common := 0
	for common < len(before) && common < len(after) && before[common] == after[common] {
		common++
	}
	return after[common:]
}

func TestInitiativeInTeams(t *testing.T) {
	mine := func(id string) bool { return id == "t1" }
	project := func(id string) bool { return id == "p1" }
	for _, item := range []struct {
		name       string
		initiative domain.Initiative
		role       string
		want       bool
	}{
		{"lead in teams", domain.Initiative{LeadTeamID: "t1"}, "member", true},
		{"lead elsewhere, project in teams", domain.Initiative{LeadTeamID: "t2", ProjectIDs: []string{"p1"}}, "member", false},
		{"linked project", domain.Initiative{ProjectIDs: []string{"p2", "p1"}}, "member", true},
		{"contributing team", domain.Initiative{ContributingTeamIDs: []string{"t1"}}, "member", true},
		{"only other teams", domain.Initiative{ContributingTeamIDs: []string{"t2"}, ProjectIDs: []string{"p2"}}, "member", false},
		{"workspace-wide", domain.Initiative{}, "member", true},
		{"workspace-wide for a guest", domain.Initiative{}, "guest", false},
	} {
		if got := initiativeInTeams(item.initiative, item.role, mine, project); got != item.want {
			t.Errorf("%s = %v, want %v", item.name, got, item.want)
		}
	}
}
