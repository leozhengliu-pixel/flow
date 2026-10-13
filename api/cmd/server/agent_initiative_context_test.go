package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// promptCapturingProvider answers every chat turn with "ok" and remembers the last messages it was sent.
func promptCapturingProvider(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	var lastPrompt string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		raw, _ := json.Marshal(payload["messages"])
		mu.Lock()
		lastPrompt = string(raw)
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "ok"}}}})
	}))
	t.Cleanup(provider.Close)
	return provider, func() string {
		mu.Lock()
		defer mu.Unlock()
		return lastPrompt
	}
}

func TestAgentSessionInitiativeContextReachesThePrompt(t *testing.T) {
	provider, lastPrompt := promptCapturingProvider(t)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, Model: "flow-test"}, agentClient: provider.Client()})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Projects) == 0 {
		t.Skip("fixture has no projects")
	}
	target := "2026-12-31"
	err = repository.MutateWorkspace(context.Background(), "test-workspace", "test.initiative", "", nil, func(data *domain.Bootstrap) error {
		project := &data.Projects[0]
		project.Name, project.Progress = "Checkout rebuild", 0.4
		data.Projects = append(data.Projects, domain.Project{ID: "project_side", Name: "Side quest", SlugID: "side-quest", Status: project.Status, Initiatives: []string{"initiative_ctx"}, MemberIDs: []string{}, TeamIDs: []string{}})
		owner := data.Viewer
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "initiative_ctx", Name: "Payments Q4", SlugID: "payments-q4", Summary: "Make checkout fast", Description: "Cut checkout latency in half.", Status: "Active", Health: "atRisk", Owner: &owner, TargetDate: &target, ProjectIDs: []string{project.ID}, ContributingTeamIDs: []string{}, LabelIDs: []string{}, ParentInitiativeIDs: []string{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
		if data.InitiativeUpdates == nil {
			data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
		}
		now := time.Now().UTC()
		data.InitiativeUpdates["initiative_ctx"] = []domain.InitiativeUpdate{
			{ID: "initiative_update_old", InitiativeID: "initiative_ctx", Body: "Kickoff happened.", Health: "onTrack", User: owner, CreatedAt: now.Add(-72 * time.Hour)},
			{ID: "initiative_update_new", InitiativeID: "initiative_ctx", Body: "Vendor contract slipped two weeks.", Health: "atRisk", User: owner, CreatedAt: now.Add(-time.Hour)},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	session := requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "How is this going?", "location": "toolbar", "initiativeIds": []string{"initiative_ctx", "initiative_ctx"}}, http.StatusCreated)
	if !slices.Equal(session.InitiativeIDs, []string{"initiative_ctx"}) {
		t.Fatalf("initiative context not stored on the session: %#v", session.InitiativeIDs)
	}
	stored := requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	if !slices.Equal(stored.InitiativeIDs, []string{"initiative_ctx"}) {
		t.Fatalf("initiative context not returned by the session API: %#v", stored.InitiativeIDs)
	}
	prompt := lastPrompt()
	for _, want := range []string{"Initiatives in context", `Initiative \"Payments Q4\"`, "status Active", "health atRisk", "target 2026-12-31", "Make checkout fast", "Cut checkout latency in half.", "Checkout rebuild", "40% complete", "Side quest", "Vendor contract slipped two weeks.", "Kickoff happened."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q: %s", want, prompt)
		}
	}
	if strings.Index(prompt, "Vendor contract slipped") > strings.Index(prompt, "Kickoff happened") {
		t.Fatalf("initiative updates are not newest first: %s", prompt)
	}

	// Follow-ups can @-mention initiatives; unknown ones are rejected like unknown projects.
	requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]any{
		"message":       "And @Payments Q4?",
		"initiativeIds": []string{"initiative_ctx"},
		"mentions":      []map[string]string{{"type": "initiative", "id": "initiative_ctx", "label": "Payments Q4"}},
	}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]any{"message": "And this?", "initiativeIds": []string{"initiative_missing"}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "Hi", "initiativeIds": []string{"initiative_missing"}}, http.StatusBadRequest)
	tooMany := make([]string, 26)
	for index := range tooMany {
		tooMany[index] = "initiative_ctx"
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "Hi", "initiativeIds": tooMany}, http.StatusBadRequest)
}

func TestAgentSessionRejectsInitiativesTheViewerCannotSee(t *testing.T) {
	provider, lastPrompt := promptCapturingProvider(t)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	key := "test-workspace"
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir(), agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, Model: "flow-test"}, agentClient: provider.Client()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/"+key+"/teams", map[string]any{"name": "Private", "key": "PRV", "private": true}, "", http.StatusCreated)
	err = repository.MutateWorkspace(context.Background(), key, "test.initiative", "", nil, func(data *domain.Bootstrap) error {
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "initiative_private", Name: "Secret plan", SlugID: "secret-plan", Status: "Active", LeadTeamID: private.ID, ProjectIDs: []string{}, ContributingTeamIDs: []string{}, LabelIDs: []string{}, ParentInitiativeIDs: []string{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	member := authClient(t)
	authRequest[domain.AuthSession](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", http.StatusOK)
	if rejected := authRequest[map[string]any](t, member, "POST", host.URL+"/api/agent/sessions", map[string]any{"message": "What is the plan?", "initiativeIds": []string{"initiative_private"}}, key, http.StatusBadRequest); !strings.Contains(rejected["error"].(string), "initiatives were not found") {
		t.Fatalf("hidden initiative rejected for another reason: %v", rejected)
	}
	authRequest[map[string]any](t, member, "POST", host.URL+"/api/agent/sessions/stream", map[string]any{"message": "What is the plan?", "initiativeIds": []string{"initiative_private"}}, key, http.StatusBadRequest)
	if strings.Contains(lastPrompt(), "Secret plan") {
		t.Fatal("a hidden initiative reached the prompt")
	}

	session := authRequest[domain.AgentSession](t, admin, "POST", host.URL+"/api/agent/sessions", map[string]any{"message": "What is the plan?", "initiativeIds": []string{"initiative_private"}}, key, http.StatusCreated)
	if !slices.Equal(session.InitiativeIDs, []string{"initiative_private"}) || !strings.Contains(lastPrompt(), "Secret plan") {
		t.Fatalf("admin could not chat about the initiative: %#v", session.InitiativeIDs)
	}
}

func TestAgentInitiativePromptBoundsItsContext(t *testing.T) {
	data := domain.Bootstrap{InitiativeUpdates: map[string][]domain.InitiativeUpdate{}}
	initiative := domain.Initiative{ID: "initiative_big", Name: "Big", Description: strings.Repeat("d", 5000)}
	for index := range 20 {
		id := "project_" + strings.Repeat("x", index+1)
		data.Projects = append(data.Projects, domain.Project{ID: id, Name: id, Initiatives: []string{initiative.ID}})
	}
	for index := range 6 {
		data.InitiativeUpdates[initiative.ID] = append(data.InitiativeUpdates[initiative.ID], domain.InitiativeUpdate{ID: "u", Body: strings.Repeat("b", 2000), CreatedAt: time.Now().Add(time.Duration(index) * time.Hour)})
	}
	prompt := agentInitiativePrompt(data, []domain.Initiative{initiative})
	if !strings.Contains(prompt, "Projects (20)") || !strings.Contains(prompt, "…and 5 more") {
		t.Fatalf("projects are not capped: %s", prompt)
	}
	if strings.Count(prompt, strings.Repeat("b", agentInitiativeUpdateRunes)) != agentInitiativeUpdateLimit || strings.Contains(prompt, strings.Repeat("b", agentInitiativeUpdateRunes+1)) {
		t.Fatal("updates are not capped in count and length")
	}
	if strings.Contains(prompt, strings.Repeat("d", agentInitiativeDescRunes+1)) {
		t.Fatal("description is not truncated")
	}
	if got := agentInitiativeTeamIDs([]domain.Initiative{{LeadTeamID: "team_a", ContributingTeamIDs: []string{"team_b"}}}); !slices.Equal(got, []string{"team_a", "team_b"}) {
		t.Fatalf("initiative teams = %v", got)
	}
}
