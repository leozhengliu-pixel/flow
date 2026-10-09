package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func triageAITestContext() triageAIContext {
	creator := domain.User{ID: "u_creator", Name: "Casey Creator", Active: true}
	owner := domain.User{ID: "u_owner", Name: "Skyler Anderson", Active: true}
	team := domain.Team{ID: "t_flow", Key: "FLO", Name: "Flow"}
	other := domain.Team{ID: "t_ops", Key: "OPS", Name: "Operations"}
	bug := domain.IssueLabel{ID: "l_bug", Name: "Bug"}
	existing := domain.IssueLabel{ID: "l_ui", Name: "UI"}
	return triageAIContext{
		Issue: domain.Issue{ID: "i_new", Identifier: "FLO-9", Title: "Project overview spacing differs from Compare Test", Team: team, Creator: creator, Labels: []domain.IssueLabel{existing}},
		Candidates: []domain.Issue{
			{ID: "i_1", Identifier: "FLO-1", Title: "Connect your tools", Team: team, Assignee: &owner, Labels: []domain.IssueLabel{bug}},
			{ID: "i_2", Identifier: "FLO-2", Title: "Import your data", Team: team},
		},
		Scores:   map[string]float64{"i_1": 0.4},
		Projects: []domain.Project{{ID: "p_compare", Name: "Compare Test"}},
		Members:  []triageAIMember{{User: creator}, {User: owner}},
		Labels:   []triageAILabel{{Label: bug, Uses: 2}, {Label: existing, Uses: 1}},
		Teams:    []triageAITeam{{Team: team}, {Team: other}},
		users:    map[string]domain.User{creator.ID: creator, owner.ID: owner},
	}
}

func TestParseTriageAIReplyDropsUnknownTargets(t *testing.T) {
	input := triageAITestContext()
	reply := "Sure:\n" + `{"duplicate":{"identifier":"FLO-404","reasons":["x"]},
"related":[{"identifier":"flo-1","reasons":["- Both concern connecting tools","one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty twentyone","third","fourth"]},{"identifier":"FLO-9","reasons":["self"]},{"identifier":"NOPE-1","reasons":["unknown"]}],
"assignee":{"name":"Nobody Here","reasons":["x"]},
"project":{"name":"compare test","reasons":["This project is the explicit Compare Test fixture referenced by the issue"]},
"labels":[{"name":"Invented","reasons":["x"]},{"name":"UI","reasons":["already applied"]},{"name":"Bug","reasons":["Similar issues use Bug"]}],
"team":{"key":"FLO","reasons":["same team"]},
"thinking":"The issue names the Compare Test project.   Nothing else is specific."}`
	plan, err := parseTriageAIReply(reply, &input)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, pick := range plan.Picks {
		kinds = append(kinds, pick.Kind+":"+pick.TargetID)
	}
	want := []string{"relatedIssue:i_1", "project:p_compare", "label:l_bug"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("picks=%v want %v", kinds, want)
	}
	reasons := plan.Picks[0].Reasons
	if len(reasons) != 3 || reasons[0] != "Both concern connecting tools" || len(strings.Fields(reasons[1])) != triageAIMaxReasonWords+1 {
		t.Fatalf("reasons were not normalized: %#v", reasons)
	}
	if plan.Thinking != "The issue names the Compare Test project. Nothing else is specific." {
		t.Fatalf("thinking=%q", plan.Thinking)
	}
	if _, err := parseTriageAIReply("no json here", &input); err == nil {
		t.Fatal("expected an error for a non-JSON reply")
	}
}

func TestParseTriageAIReplyNeedsEvidenceForLabelsAndCreator(t *testing.T) {
	input := triageAITestContext()
	feature := domain.IssueLabel{ID: "l_feature", Name: "Feature"}
	input.Labels = append(input.Labels, triageAILabel{Label: feature, Uses: 0})
	// A label that merely fits the issue, with no similar issue carrying it, is dropped.
	plan, err := parseTriageAIReply(`{"labels":[{"name":"Feature","reasons":["Requests a new capability"]}]}`, &input)
	if err != nil || len(plan.Picks) != 0 {
		t.Fatalf("picks=%v err=%v", plan.Picks, err)
	}
	// The creator is a fair assignee when they lead the suggested project.
	creator := input.Issue.Creator
	input.Projects[0].Lead = &creator
	plan, err = parseTriageAIReply(`{"project":{"name":"Compare Test","reasons":["Named in the issue"]},"assignee":{"name":"Casey Creator","reasons":["Leads Compare Test"]}}`, &input)
	if err != nil || len(plan.Picks) != 2 || plan.Picks[1].Kind != "assignee" || plan.Picks[1].TargetID != creator.ID {
		t.Fatalf("picks=%v err=%v", plan.Picks, err)
	}
}

func TestParseTriageAIReplyIsConservativeAboutCreatorAndTeam(t *testing.T) {
	input := triageAITestContext()
	plan, err := parseTriageAIReply(`{"assignee":{"name":"Casey Creator","reasons":["Created the issue"]},"team":{"key":"OPS","reasons":["Ops owns deploys"]}}`, &input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Picks) != 1 || plan.Picks[0].Kind != "team" || plan.Picks[0].TargetID != "t_ops" {
		t.Fatalf("creator should not be suggested for creating the issue; a different team may be: %#v", plan.Picks)
	}
	plan, err = parseTriageAIReply(`{"assignee":{"name":"skyler anderson","reasons":["Owns FLO-1"]}}`, &input)
	if err != nil || len(plan.Picks) != 1 || plan.Picks[0].TargetID != "u_owner" {
		t.Fatalf("owner assignee dropped: %#v %v", plan, err)
	}
	input.Issue.Assignee = &domain.User{ID: "u_owner"}
	plan, _ = parseTriageAIReply(`{"assignee":{"name":"Skyler Anderson","reasons":["x"]}}`, &input)
	if len(plan.Picks) != 0 {
		t.Fatalf("assigned issues must not get assignee suggestions: %#v", plan.Picks)
	}
}

func TestTriageAIPlanSuggestionsHonorActions(t *testing.T) {
	plan := triageAIPlan{Thinking: "why", Picks: []triageAIPick{
		{Kind: "relatedIssue", TargetID: "i_1", Reasons: []string{"r"}, Score: 0.4},
		{Kind: "label", TargetID: "l_bug", Reasons: []string{"l"}},
		{Kind: "project", TargetID: "p_compare", Reasons: []string{"p"}},
	}}
	items := plan.suggestions(domain.TriageIntelligenceSettings{LabelAction: "hide"}, "i_new", time.Now().UTC())
	if len(items) != 2 || items[0].SuggestedIssueID != "i_1" || items[1].SuggestedProjectID != "p_compare" {
		t.Fatalf("suggestions=%#v", items)
	}
	metadata := items[1].Metadata
	if metadata["source"] != triageSourceAI || metadata["thinking"] != "why" || metadata["rank"] != 2 || !slices.Equal(metadata["reasons"].([]string), []string{"p"}) {
		t.Fatalf("metadata=%#v", metadata)
	}
	if _, ok := metadata["score"]; ok {
		t.Fatal("non-issue picks carry no heuristic score")
	}
}

func TestHeuristicNoLongerSuggestsCreatorAsAssignee(t *testing.T) {
	creator := domain.User{ID: "u_creator", Name: "Casey", Active: true}
	data := domain.Bootstrap{Users: []domain.User{creator}}
	issue := domain.Issue{ID: "i", Title: "Add keyboard shortcut to toggle dark mode", Creator: creator}
	if id, _, _ := inferAssignee(&data, &issue, nil); id != "" {
		t.Fatalf("creator suggested without evidence: %s", id)
	}
	related := domain.Issue{ID: "r", Title: "Keyboard shortcut dark mode", Creator: creator}
	if id, _, _ := inferAssignee(&data, &issue, []triageSuggestionCandidate{{issue: related, score: 0.9}}); id != "" {
		t.Fatalf("creator of an unassigned related issue suggested: %s", id)
	}
}

type triageAIProvider struct {
	mu      sync.Mutex
	prompts []string
	calls   atomic.Int32
	fail    atomic.Bool
	reply   func() string
}

func (p *triageAIProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.calls.Add(1)
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	p.mu.Lock()
	if len(body.Messages) > 0 {
		p.prompts = append(p.prompts, body.Messages[len(body.Messages)-1].Content)
	}
	p.mu.Unlock()
	if p.fail.Load() {
		http.Error(w, `{"error":{"message":"overloaded"}}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": p.reply()}, "finish_reason": "stop"}}})
}

type triageAIFixture struct {
	repository *store.SQLiteStore
	service    *server
	handler    http.Handler
	provider   *triageAIProvider
	bootstrap  domain.Bootstrap
	team       domain.Team
	backlogID  string
}

func newTriageAIFixture(t *testing.T, reply func() string) *triageAIFixture {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := repository.Bootstrap()
	team := bootstrap.Teams[0]
	backlog := slices.IndexFunc(bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "backlog" })
	if backlog < 0 {
		t.Fatal("test workspace has no backlog state")
	}
	if err := repository.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "test.triage_ai_setup", team.ID, nil, func(data *domain.Bootstrap) error {
		settings := teamSettings(data, team.ID)
		settings.TriageEnabled = true
		data.TeamSettings[team.ID] = settings
		data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = true
		data.WorkspaceSettings.FeatureSettings.TriageIntelligence.WorkspaceGuidance = "Route importer crashes to the data team."
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	provider := &triageAIProvider{reply: reply}
	upstream := httptest.NewServer(provider)
	service := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, BaseURL: upstream.URL, Model: "model", MaxOutputTokens: 800, Timeout: 5 * time.Second}, agentClient: upstream.Client()}
	fixture := &triageAIFixture{repository: repository, service: service, handler: newHandler(service), provider: provider, bootstrap: bootstrap, team: team, backlogID: bootstrap.States[backlog].ID}
	t.Cleanup(func() {
		fixture.waitIdle(t)
		upstream.Close()
		repository.Close()
	})
	return fixture
}

func (f *triageAIFixture) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		idle := true
		f.service.triageRuns.Range(func(any, any) bool { idle = false; return false })
		f.service.triageEdits.Range(func(any, any) bool { idle = false; return false })
		if idle {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background Triage Intelligence run did not finish")
}

func (f *triageAIFixture) waitGenerated(t *testing.T, issueID string) domain.Issue {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data := requestJSON[domain.Bootstrap](t, f.handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
		if issue, err := issueByID(&data, issueID); err == nil && issue.SuggestionsGeneratedAt != nil {
			return *issue
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("suggestionsGeneratedAt was never set")
	return domain.Issue{}
}

type triageSuggestionList struct {
	IssueID     string                   `json:"issueId"`
	Pending     bool                     `json:"pending"`
	Source      string                   `json:"source"`
	Thinking    string                   `json:"thinking"`
	Suggestions []domain.IssueSuggestion `json:"suggestions"`
}

func TestTriageIntelligenceAIGeneratesInBackground(t *testing.T) {
	var relatedIdentifier atomic.Value
	relatedIdentifier.Store("")
	release := make(chan struct{})
	var fixture *triageAIFixture
	fixture = newTriageAIFixture(t, func() string {
		<-release
		project := fixture.bootstrap.Projects[0].Name
		return `{"duplicate":null,"related":[{"identifier":"` + relatedIdentifier.Load().(string) + `","reasons":["Both are about importing data files"]},{"identifier":"ZZZ-999","reasons":["unknown"]}],"assignee":null,"project":{"name":"` + project + `","reasons":["The importer belongs to this project"]},"labels":[],"team":null,"thinking":"Only the importer issue overlaps."}`
	})
	existing := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Import your data", "teamId": fixture.team.ID, "stateId": fixture.bootstrap.States[slices.IndexFunc(fixture.bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "unstarted" })].ID,
	}, http.StatusCreated)
	relatedIdentifier.Store(existing.Identifier)
	created := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "App crashes when importing a large CSV file", "description": "Uploading a 200MB CSV crashes the importer.", "teamId": fixture.team.ID, "stateId": fixture.backlogID,
	}, http.StatusCreated)
	if created.SuggestionsGeneratedAt != nil {
		t.Fatal("create must not wait for the model")
	}
	pending := requestJSON[triageSuggestionList](t, fixture.handler, http.MethodGet, "/api/issues/"+created.ID+"/suggestions", nil, http.StatusOK)
	if !pending.Pending || len(pending.Suggestions) != 0 {
		t.Fatalf("expected a pending, empty list: %#v", pending)
	}
	// A second trigger while the run is in flight must not start another run.
	requestJSON[domain.Issue](t, fixture.handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"priority": 2}, http.StatusOK)
	close(release)
	generated := fixture.waitGenerated(t, created.ID)
	fixture.waitIdle(t)
	if calls := fixture.provider.calls.Load(); calls != 1 {
		fixture.provider.mu.Lock()
		defer fixture.provider.mu.Unlock()
		for _, prompt := range fixture.provider.prompts {
			t.Logf("prompt: %.300s", prompt)
		}
		t.Fatalf("expected one deduplicated model call, got %d", calls)
	}
	if generated.SuggestionsSource != triageSourceAI || generated.SuggestionsThinking != "Only the importer issue overlaps." {
		t.Fatalf("issue did not record the AI run: %#v", generated)
	}
	listed := requestJSON[triageSuggestionList](t, fixture.handler, http.MethodGet, "/api/issues/"+created.ID+"/suggestions", nil, http.StatusOK)
	if listed.Pending || listed.Source != triageSourceAI || listed.Thinking == "" || len(listed.Suggestions) != 2 {
		t.Fatalf("listed=%#v", listed)
	}
	related := listed.Suggestions[0]
	if related.Type != "relatedIssue" || related.SuggestedIssueID != existing.ID || related.Metadata["source"] != triageSourceAI {
		t.Fatalf("related=%#v", related)
	}
	if reasons, _ := related.Metadata["reasons"].([]any); len(reasons) != 1 || reasons[0] != "Both are about importing data files" {
		t.Fatalf("reasons=%#v", related.Metadata["reasons"])
	}
	if listed.Suggestions[1].Type != "project" || listed.Suggestions[1].SuggestedProjectID != fixture.bootstrap.Projects[0].ID {
		t.Fatalf("project=%#v", listed.Suggestions[1])
	}
	fixture.provider.mu.Lock()
	prompt := fixture.provider.prompts[0]
	fixture.provider.mu.Unlock()
	for _, want := range []string{"App crashes when importing a large CSV file", existing.Identifier, "Route importer crashes to the data team.", `"teams"`, `"labels"`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestTriageIntelligenceRefreshRunsModelSynchronously(t *testing.T) {
	var fixture *triageAIFixture
	fixture = newTriageAIFixture(t, func() string {
		return `{"duplicate":null,"related":[],"assignee":null,"project":{"name":"` + fixture.bootstrap.Projects[0].Name + `","reasons":["Named in the issue"]},"labels":[],"team":null,"thinking":"The project is named."}`
	})
	created := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Project overview spacing differs from the reference", "teamId": fixture.team.ID, "stateId": fixture.backlogID,
	}, http.StatusCreated)
	fixture.waitGenerated(t, created.ID)
	fixture.waitIdle(t)
	before := fixture.provider.calls.Load()
	refreshed := requestJSON[[]domain.IssueSuggestion](t, fixture.handler, http.MethodPost, "/api/issue-records/"+created.ID+"/suggestions/refresh", nil, http.StatusOK)
	if fixture.provider.calls.Load() != before+1 {
		t.Fatal("Run again did not call the model")
	}
	if len(refreshed) != 1 || refreshed[0].Type != "project" || refreshed[0].Metadata["source"] != triageSourceAI || refreshed[0].Metadata["thinking"] != "The project is named." {
		t.Fatalf("refreshed=%#v", refreshed)
	}
}

func TestTriageIntelligenceFallsBackToHeuristicOnModelError(t *testing.T) {
	fixture := newTriageAIFixture(t, func() string { return "" })
	fixture.provider.fail.Store(true)
	project := fixture.bootstrap.Projects[0]
	requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Vehicle marketplace image preview", "teamId": fixture.team.ID, "stateId": fixture.bootstrap.States[slices.IndexFunc(fixture.bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "unstarted" })].ID, "projectId": project.ID,
	}, http.StatusCreated)
	created := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Vehicle marketplace image preview behavior", "teamId": fixture.team.ID, "stateId": fixture.backlogID,
	}, http.StatusCreated)
	generated := fixture.waitGenerated(t, created.ID)
	if generated.SuggestionsSource != triageSourceHeuristic || generated.SuggestionsThinking != "" {
		t.Fatalf("fallback was not recorded: %#v", generated)
	}
	listed := requestJSON[triageSuggestionList](t, fixture.handler, http.MethodGet, "/api/issues/"+created.ID+"/suggestions", nil, http.StatusOK)
	if listed.Pending || listed.Source != triageSourceHeuristic || len(listed.Suggestions) == 0 {
		t.Fatalf("listed=%#v", listed)
	}
	for _, item := range listed.Suggestions {
		if item.Metadata["source"] != triageSourceHeuristic {
			t.Fatalf("suggestion source=%#v", item.Metadata)
		}
		if item.Type == "assignee" && item.SuggestedUserID == created.Creator.ID {
			t.Fatalf("heuristic suggested the creator: %#v", item)
		}
	}
}

func TestAgentMaxOutputTokensOverrideOnlyRaisesTheBudget(t *testing.T) {
	s := &server{agent: appconfig.AgentConfig{MaxOutputTokens: 4096}}
	ctx := t.Context()
	if got := s.agentMaxOutputTokens(ctx); got != 4096 {
		t.Fatalf("default budget = %d", got)
	}
	if got := s.agentMaxOutputTokens(withAgentMaxOutputTokens(ctx, triageAIMaxOutputTokens)); got != triageAIMaxOutputTokens {
		t.Fatalf("raised budget = %d", got)
	}
	if got := s.agentMaxOutputTokens(withAgentMaxOutputTokens(ctx, 1024)); got != 4096 {
		t.Fatalf("an override must not lower the configured budget, got %d", got)
	}
}

func (f *triageAIFixture) unstartedStateID(t *testing.T) string {
	t.Helper()
	index := slices.IndexFunc(f.bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "unstarted" })
	if index < 0 {
		t.Fatal("test workspace has no unstarted state")
	}
	return f.bootstrap.States[index].ID
}

func (f *triageAIFixture) list(t *testing.T, issueID string) triageSuggestionList {
	t.Helper()
	return requestJSON[triageSuggestionList](t, f.handler, http.MethodGet, "/api/issues/"+issueID+"/suggestions", nil, http.StatusOK)
}

func (f *triageAIFixture) lastPrompt() string {
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.prompts) == 0 {
		return ""
	}
	return f.provider.prompts[len(f.provider.prompts)-1]
}

// The DEV-14 case: the model names DEV-10 as the duplicate of a 60MB CSV
// importer hang. It must become a "Duplicate of" suggestion — and when the
// issue is already marked as a duplicate of DEV-10 the model is told so and
// the pick is not resurfaced (as a duplicate or as a related issue).
func TestTriageIntelligenceModelDuplicateBecomesSimilarIssueSuggestion(t *testing.T) {
	var duplicate, related atomic.Value
	duplicate.Store("")
	related.Store("")
	fixture := newTriageAIFixture(t, func() string {
		return `{"duplicate":{"identifier":"` + duplicate.Load().(string) + `","reasons":["Both describe large CSV imports freezing"]},"related":[{"identifier":"` + related.Load().(string) + `","reasons":["Large CSV import failure"]},{"identifier":"` + duplicate.Load().(string) + `","reasons":["Same import freeze"]}],"assignee":null,"project":null,"labels":[],"team":null,"thinking":"DEV-10 is a strong duplicate because it specifically describes large CSV imports freezing the browser."}`
	})
	freeze := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "CSV import freezes the browser tab for large files", "description": "Importing a 50MB CSV via Import your data freezes the tab and it never finishes.", "teamId": fixture.team.ID, "stateId": fixture.unstartedStateID(t),
	}, http.StatusCreated)
	crash := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "App crashes when importing a large CSV file", "teamId": fixture.team.ID, "stateId": fixture.unstartedStateID(t),
	}, http.StatusCreated)
	duplicate.Store(freeze.Identifier)
	related.Store(crash.Identifier)
	created := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Uploading a 60MB CSV hangs the importer", "description": "When I import a large CSV (about 60MB) through Import your data, the progress bar stops at 10% and the tab freezes.", "teamId": fixture.team.ID, "stateId": fixture.backlogID,
	}, http.StatusCreated)
	fixture.waitGenerated(t, created.ID)
	fixture.waitIdle(t)
	listed := fixture.list(t, created.ID)
	kinds := []string{}
	for _, item := range listed.Suggestions {
		kinds = append(kinds, item.Type+":"+item.SuggestedIssueID)
	}
	if want := []string{"similarIssue:" + freeze.ID, "relatedIssue:" + crash.ID}; !slices.Equal(kinds, want) {
		t.Fatalf("suggestions=%v want %v", kinds, want)
	}
	if listed.Pending || listed.Source != triageSourceAI {
		t.Fatalf("listed=%#v", listed)
	}

	// Already marked as a duplicate: the model sees the link and the pick is dropped.
	requestJSON[domain.IssueRelation](t, fixture.handler, http.MethodPost, "/api/issues/"+created.ID+"/relations", map[string]any{"type": "duplicate", "relatedIssueId": freeze.ID}, http.StatusCreated)
	refreshed := requestJSON[[]domain.IssueSuggestion](t, fixture.handler, http.MethodPost, "/api/issue-records/"+created.ID+"/suggestions/refresh", nil, http.StatusOK)
	if len(refreshed) != 1 || refreshed[0].Type != "relatedIssue" || refreshed[0].SuggestedIssueID != crash.ID {
		t.Fatalf("an already-linked duplicate was resurfaced: %#v", refreshed)
	}
	prompt := fixture.lastPrompt()
	for _, want := range []string{`"linkedIssues"`, `"identifier": "` + freeze.Identifier + `",` + "\n" + `    "relation": "duplicate"`, `"alreadyLinkedAs": "duplicate"`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// Issues generated by the heuristic while the model was not configured are
// regenerated by the model when their card is viewed — once, in the
// background, reported as pending meanwhile.
func TestTriageIntelligenceUpgradesHeuristicSuggestionsWhenViewed(t *testing.T) {
	release := make(chan struct{})
	var fixture *triageAIFixture
	fixture = newTriageAIFixture(t, func() string {
		<-release
		return `{"duplicate":null,"related":[],"assignee":null,"project":{"name":"` + fixture.bootstrap.Projects[0].Name + `","reasons":["Named in the issue"]},"labels":[],"team":null,"thinking":"The project is named."}`
	})
	fixture.service.agent.Enabled = false
	created := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Uploading a 60MB CSV hangs the importer", "teamId": fixture.team.ID, "stateId": fixture.backlogID,
	}, http.StatusCreated)
	if created.SuggestionsGeneratedAt == nil || created.SuggestionsSource != triageSourceHeuristic {
		t.Fatalf("expected heuristic suggestions while the model is off: %#v", created)
	}
	fixture.service.agent.Enabled = true
	viewed := fixture.list(t, created.ID)
	if !viewed.Pending || viewed.Source != triageSourceHeuristic {
		t.Fatalf("viewing a heuristic issue should start a model run and report pending: %#v", viewed)
	}
	fixture.list(t, created.ID) // a second view while the run is in flight starts nothing
	close(release)
	fixture.waitIdle(t)
	listed := fixture.list(t, created.ID)
	if listed.Pending || listed.Source != triageSourceAI || len(listed.Suggestions) != 1 || listed.Suggestions[0].Type != "project" {
		t.Fatalf("listed=%#v", listed)
	}
	fixture.waitIdle(t)
	if calls := fixture.provider.calls.Load(); calls != 1 {
		t.Fatalf("expected exactly one model call, got %d", calls)
	}
}

// Material title/description edits of a triage issue regenerate its
// suggestions once the edits settle; a typo fix does not.
func TestTriageIntelligenceRegeneratesAfterMaterialEdits(t *testing.T) {
	fixture := newTriageAIFixture(t, func() string {
		return `{"duplicate":null,"related":[],"assignee":null,"project":null,"labels":[],"team":null,"thinking":"Nothing fits."}`
	})
	fixture.service.triageEditDebounce = 40 * time.Millisecond
	created := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Export to PDF cuts off long tables", "teamId": fixture.team.ID, "stateId": fixture.backlogID,
	}, http.StatusCreated)
	fixture.waitGenerated(t, created.ID)
	fixture.waitIdle(t)
	if fixture.provider.calls.Load() != 1 {
		t.Fatalf("calls=%d", fixture.provider.calls.Load())
	}
	requestJSON[domain.Issue](t, fixture.handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"title": "Export to PDF cuts off long tabels"}, http.StatusOK)
	requestJSON[domain.Issue](t, fixture.handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"priority": 2}, http.StatusOK)
	time.Sleep(100 * time.Millisecond)
	fixture.waitIdle(t)
	if calls := fixture.provider.calls.Load(); calls != 1 {
		t.Fatalf("a typo fix regenerated suggestions (calls=%d)", calls)
	}
	// Two quick material edits settle into one run.
	requestJSON[domain.Issue](t, fixture.handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"description": "When I export an issue list to PDF, tables longer than one page are cut off after the first page."}, http.StatusOK)
	requestJSON[domain.Issue](t, fixture.handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"title": "PDF export truncates multi-page tables"}, http.StatusOK)
	time.Sleep(100 * time.Millisecond)
	fixture.waitIdle(t)
	if calls := fixture.provider.calls.Load(); calls != 2 {
		t.Fatalf("material edits should regenerate once, calls=%d", calls)
	}
	if prompt := fixture.lastPrompt(); !strings.Contains(prompt, "PDF export truncates multi-page tables") || !strings.Contains(prompt, "cut off after the first page") {
		t.Fatalf("regeneration did not use the edited text:\n%s", prompt)
	}
}

func TestTriageIntelligenceSkipsTheModelForContentlessIssues(t *testing.T) {
	fixture := newTriageAIFixture(t, func() string { return `{"labels":[{"name":"Bug","reasons":["x"]}]}` })
	created := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "212312", "teamId": fixture.team.ID, "stateId": fixture.backlogID,
	}, http.StatusCreated)
	generated := fixture.waitGenerated(t, created.ID)
	fixture.waitIdle(t)
	if fixture.provider.calls.Load() != 0 || generated.SuggestionsSource != triageSourceAI || generated.SuggestionsThinking == "" {
		t.Fatalf("calls=%d issue=%#v", fixture.provider.calls.Load(), generated)
	}
	if listed := fixture.list(t, created.ID); listed.Pending || len(listed.Suggestions) != 0 {
		t.Fatalf("listed=%#v", listed)
	}
}

func TestTriageInputSketchJudgesMaterialEdits(t *testing.T) {
	base := domain.Issue{Title: "Export to PDF cuts off long tables", Description: "When I export an issue list to PDF, tables longer than one page are cut off after the first page."}
	sketch := triageInputSketch(&base)
	for name, edit := range map[string]domain.Issue{
		"typo":              {Title: "Export to PDF cuts off long tabels", Description: base.Description},
		"punctuation":       {Title: "Export to PDF: cuts off long tables!", Description: base.Description + "  "},
		"stub description":  {Title: base.Title, Description: base.Description + " Thanks."},
		"appended sentence": {Title: base.Title, Description: base.Description + " Happens in Chrome."},
	} {
		if triageSketchMaterial(sketch, triageInputSketch(&edit)) {
			t.Errorf("%s should not be material", name)
		}
	}
	for name, edit := range map[string]domain.Issue{
		"rewritten title":      {Title: "PDF export truncates multi-page tables", Description: base.Description},
		"replaced description": {Title: base.Title, Description: "Exports fail entirely for workspaces with custom fonts enabled in settings."},
	} {
		if !triageSketchMaterial(sketch, triageInputSketch(&edit)) {
			t.Errorf("%s should be material", name)
		}
	}
	titleOnly := domain.Issue{Title: base.Title}
	if !triageSketchMaterial(triageInputSketch(&titleOnly), sketch) {
		t.Error("writing a description should be material")
	}
	if triageInputChanged(&domain.Issue{Title: "anything"}) {
		t.Error("issues without a recorded sketch have no baseline")
	}
}

func TestTriageIssueHasSubstance(t *testing.T) {
	for title, want := range map[string]bool{
		"212312": false, "asd": false, "Fix it": false, "": false,
		"Login fails": true, "导入大文件卡死": true, "Uploading a 60MB CSV hangs the importer": true,
	} {
		if got := triageIssueHasSubstance(&domain.Issue{Title: title}); got != want {
			t.Errorf("%q: got %v want %v", title, got, want)
		}
	}
}

func TestParseTriageAIReplyAllowsUsedLabelsThatFit(t *testing.T) {
	input := triageAITestContext()
	performance := domain.IssueLabel{ID: "l_perf", Name: "Performance"}
	unused := domain.IssueLabel{ID: "l_unused", Name: "Regression"}
	input.Labels = append(input.Labels, triageAILabel{Label: performance, Uses: 4}, triageAILabel{Label: unused})
	plan, err := parseTriageAIReply(`{"labels":[{"name":"Performance","reasons":["The importer hangs on large files"]},{"name":"Regression","reasons":["x"]}]}`, &input)
	if err != nil || len(plan.Picks) != 1 || plan.Picks[0].TargetID != "l_perf" {
		t.Fatalf("picks=%#v err=%v", plan.Picks, err)
	}
}
