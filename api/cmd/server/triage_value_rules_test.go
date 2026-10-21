package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func suggestionsOf(t *testing.T, handler http.Handler, issueID string) []domain.IssueSuggestion {
	t.Helper()
	listed := requestJSON[struct {
		Suggestions []domain.IssueSuggestion `json:"suggestions"`
	}](t, handler, http.MethodGet, "/api/issues/"+issueID+"/suggestions", nil, http.StatusOK)
	return listed.Suggestions
}

func TestTriageActionForTargetPrefersValueRule(t *testing.T) {
	settings := domain.TriageIntelligenceSettings{
		LabelAction: "suggest", ProjectAction: "auto", TeamAction: "hide",
		ValueRules: []domain.TriageValueRule{
			{Kind: "label", Value: "l_bug", Action: "hide"},
			{Kind: "project", Value: "p_old", Action: "suggest"},
			{Kind: "team", Value: "t_core", Action: "auto"},
		},
	}
	for _, item := range []struct{ kind, target, want string }{
		{"label", "l_bug", "hide"},
		{"label", "l_other", "suggest"},
		{"project", "p_old", "suggest"},
		{"project", "p_new", "auto"},
		{"team", "t_core", "auto"},
		{"team", "t_other", "hide"},
		// A label rule never leaks onto a project with the same id.
		{"project", "l_bug", "auto"},
		{"assignee", "u_1", "suggest"},
	} {
		if got, ok := triageActionForTarget(settings, item.kind, item.target); !ok || got != item.want {
			t.Errorf("%s %s = %q (%v), want %q", item.kind, item.target, got, ok, item.want)
		}
	}
	if _, ok := triageActionForTarget(settings, "bogus", "x"); ok {
		t.Fatal("unknown kinds have no action")
	}
}

func TestTriageAIPlanSuggestionsHonorValueRules(t *testing.T) {
	plan := triageAIPlan{Picks: []triageAIPick{
		{Kind: "label", TargetID: "l_bug", Reasons: []string{"a"}},
		{Kind: "label", TargetID: "l_docs", Reasons: []string{"b"}},
		{Kind: "project", TargetID: "p_one", Reasons: []string{"c"}},
	}}
	settings := domain.TriageIntelligenceSettings{
		LabelAction: "suggest", ProjectAction: "hide",
		ValueRules: []domain.TriageValueRule{
			{Kind: "label", Value: "l_bug", Action: "hide"},
			{Kind: "project", Value: "p_one", Action: "suggest"},
		},
	}
	items := plan.suggestions(settings, "i_new", time.Now().UTC())
	if len(items) != 2 || items[0].SuggestedLabelID != "l_docs" || items[1].SuggestedProjectID != "p_one" {
		t.Fatalf("suggestions=%#v", items)
	}
}

func TestTriageValueRulesAreValidatedAndStored(t *testing.T) {
	_, handler := newLoopTestServer(t, &fakeLoopProvider{})
	read := func() domain.WorkspaceSettings {
		return requestJSON[domain.WorkspaceSettings](t, handler, http.MethodGet, "/api/workspace/preferences", nil, http.StatusOK)
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Users) == 0 || len(bootstrap.Teams) == 0 {
		t.Fatal("fixture has no users or teams")
	}
	current := read().FeatureSettings
	save := func(rules []domain.TriageValueRule, status int) {
		// The web app sends the whole featureSettings with valueRules always present (an empty list clears them).
		next := map[string]any{}
		raw, _ := json.Marshal(current)
		if err := json.Unmarshal(raw, &next); err != nil {
			t.Fatal(err)
		}
		triage := next["triageIntelligence"].(map[string]any)
		if rules == nil {
			rules = []domain.TriageValueRule{}
		}
		triage["valueRules"] = rules
		requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": next}, status)
	}

	save([]domain.TriageValueRule{
		{Kind: "assignee", Value: bootstrap.Users[0].ID, Action: "hide"},
		{Kind: "team", Value: bootstrap.Teams[0].ID, Action: "auto"},
		{Kind: "team", Value: bootstrap.Teams[0].ID, Action: "suggest"},
	}, http.StatusOK)
	rules := read().FeatureSettings.TriageIntelligence.ValueRules
	if len(rules) != 2 || rules[1].Action != "suggest" {
		t.Fatalf("rules were not stored and de-duplicated (last wins): %#v", rules)
	}

	save([]domain.TriageValueRule{{Kind: "assignee", Value: "nobody", Action: "hide"}}, http.StatusBadRequest)
	save([]domain.TriageValueRule{{Kind: "duplicate", Value: bootstrap.Teams[0].ID, Action: "hide"}}, http.StatusBadRequest)
	save([]domain.TriageValueRule{{Kind: "team", Value: bootstrap.Teams[0].ID, Action: "maybe"}}, http.StatusBadRequest)
	if got := read().FeatureSettings.TriageIntelligence.ValueRules; len(got) != 2 {
		t.Fatalf("a rejected save changed the rules: %#v", got)
	}
	save(nil, http.StatusOK)
	if got := read().FeatureSettings.TriageIntelligence.ValueRules; len(got) != 0 {
		t.Fatalf("rules were not cleared: %#v", got)
	}
}

// A per-value rule decides whether a suggestion pointing at that value is shown, auto-applied or hidden, whatever the
// row's default says.
func TestTriageIntelligenceValueRulesDecideShowAutoHide(t *testing.T) {
	setup := func(t *testing.T, projectAction string, rules func(project domain.ProjectSummary) []domain.TriageValueRule) (http.Handler, domain.Team, domain.ProjectSummary, string) {
		t.Helper()
		repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repository.Close() })
		bootstrap := repository.Bootstrap()
		team := bootstrap.Teams[0]
		project := bootstrap.Projects[0]
		backlog := slices.IndexFunc(bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "backlog" })
		if backlog < 0 {
			t.Fatal("test workspace has no backlog state")
		}
		if err := repository.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "test.triage_rules_setup", team.ID, nil, func(data *domain.Bootstrap) error {
			settings := teamSettings(data, team.ID)
			settings.TriageEnabled = true
			data.TeamSettings[team.ID] = settings
			data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = true
			data.WorkspaceSettings.FeatureSettings.TriageIntelligence.ProjectAction = projectAction
			data.WorkspaceSettings.FeatureSettings.TriageIntelligence.ValueRules = rules(domain.ProjectSummary{ID: project.ID})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true}), team, domain.ProjectSummary{ID: project.ID}, bootstrap.States[backlog].ID
	}
	create := func(t *testing.T, handler http.Handler, team domain.Team, project domain.ProjectSummary, stateID string) domain.Issue {
		t.Helper()
		requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
			"title": "Vehicle marketplace image preview", "teamId": team.ID, "stateId": stateID, "projectId": project.ID,
		}, http.StatusCreated)
		return requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
			"title": "Vehicle marketplace image preview behavior", "teamId": team.ID, "stateId": stateID,
		}, http.StatusCreated)
	}

	t.Run("a value rule auto-applies while the row only shows", func(t *testing.T) {
		handler, team, project, state := setup(t, "suggest", func(p domain.ProjectSummary) []domain.TriageValueRule {
			return []domain.TriageValueRule{{Kind: "project", Value: p.ID, Action: "auto"}}
		})
		created := create(t, handler, team, project, state)
		if created.Project == nil || created.Project.ID != project.ID {
			t.Fatalf("rule did not auto-apply the project: %#v", created.Project)
		}
	})
	t.Run("a value rule hides while the row auto-applies", func(t *testing.T) {
		handler, team, project, state := setup(t, "auto", func(p domain.ProjectSummary) []domain.TriageValueRule {
			return []domain.TriageValueRule{{Kind: "project", Value: p.ID, Action: "hide"}}
		})
		created := create(t, handler, team, project, state)
		if created.Project != nil {
			t.Fatalf("hidden value was applied: %#v", created.Project)
		}
		for _, item := range suggestionsOf(t, handler, created.ID) {
			if item.Type == "project" {
				t.Fatalf("hidden value was suggested: %#v", item)
			}
		}
	})
	t.Run("a value rule shows while the row auto-applies", func(t *testing.T) {
		handler, team, project, state := setup(t, "auto", func(p domain.ProjectSummary) []domain.TriageValueRule {
			return []domain.TriageValueRule{{Kind: "project", Value: p.ID, Action: "suggest"}}
		})
		created := create(t, handler, team, project, state)
		if created.Project != nil {
			t.Fatalf("a shown value was auto-applied: %#v", created.Project)
		}
		if !slices.ContainsFunc(suggestionsOf(t, handler, created.ID), func(item domain.IssueSuggestion) bool {
			return item.Type == "project" && item.SuggestedProjectID == project.ID
		}) {
			t.Fatalf("the project was not suggested")
		}
	})
}
