package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func teamGuidanceFixture() domain.Bootstrap {
	return domain.Bootstrap{
		Viewer:            domain.User{ID: "u"},
		WorkspaceSettings: domain.WorkspaceSettings{AgentInstructions: "Workspace rule"},
		Teams:             []domain.Team{{ID: "root", Key: "ROOT", Name: "Root"}, {ID: "eng", Key: "ENG", Name: "Engineering"}, {ID: "web", Key: "WEB", Name: "Web"}, {ID: "api", Key: "API", Name: "API"}, {ID: "ops", Key: "OPS", Name: "Ops"}},
		TeamSettings: map[string]domain.TeamSettings{
			"root": {TeamID: "root"},
			"eng":  {TeamID: "eng", ParentTeamID: "root", AgentGuidance: "Engineering rule"},
			"web":  {TeamID: "web", ParentTeamID: "eng", AgentGuidance: "  Web rule  "},
			"api":  {TeamID: "api", ParentTeamID: "eng", AgentGuidance: "API rule"},
			"ops":  {TeamID: "ops", AgentGuidance: "Ops rule"},
		},
	}
}

func guidanceBodies(rules []agentGuidanceRule) []string {
	bodies := []string{}
	for _, rule := range rules {
		bodies = append(bodies, rule.Origin+":"+rule.TeamID+":"+rule.Body)
	}
	return bodies
}

func TestAgentGuidanceChainOrdersWorkspaceParentTeamAndSkipsEmptyLevels(t *testing.T) {
	data := teamGuidanceFixture()
	got := strings.Join(guidanceBodies(agentGuidanceChain(data, "web")), "|")
	// Workspace first, then ancestors (the empty root level is skipped), then the team itself: the team wins.
	if want := "workspace::Workspace rule|team:eng:Engineering rule|team:web:Web rule"; got != want {
		t.Fatalf("chain = %q want %q", got, want)
	}
	rules := agentGuidanceChain(data, "web")
	if rules[2].TeamName != "Web" || rules[2].TeamKey != "WEB" || rules[1].TeamName != "Engineering" {
		t.Fatalf("team identity missing: %#v", rules)
	}
	data.WorkspaceSettings.AgentInstructions = "  "
	if got := strings.Join(guidanceBodies(agentGuidanceChain(data, "root")), "|"); got != "" {
		t.Fatalf("empty levels produced rules: %q", got)
	}
	// Two sibling teams share their parent's guidance once.
	if got := strings.Join(guidanceBodies(teamsAgentGuidance(data, []string{"web", "api", "web", ""})), "|"); got != "team:eng:Engineering rule|team:web:Web rule|team:api:API rule" {
		t.Fatalf("deduped chain = %q", got)
	}
	// A parent cycle (corrupt data) ends instead of looping.
	data.TeamSettings["root"] = domain.TeamSettings{TeamID: "root", ParentTeamID: "web"}
	if rules := teamAgentGuidanceChain(data, "web"); len(rules) != 2 {
		t.Fatalf("cycle chain: %#v", rules)
	}
	// Each level is capped like workspace guidance.
	data.TeamSettings["ops"] = domain.TeamSettings{TeamID: "ops", AgentGuidance: strings.Repeat("界", teamAgentGuidanceLimit+50)}
	if rules := teamAgentGuidanceChain(data, "ops"); len(rules) != 1 || len([]rune(rules[0].Body)) != teamAgentGuidanceLimit {
		t.Fatalf("team guidance not capped: %d", len([]rune(rules[0].Body)))
	}
}

func TestWorkspaceAgentPromptIncludesTeamGuidanceForTeamsInContext(t *testing.T) {
	data := teamGuidanceFixture()
	prompt := workspaceAgentSystemPrompt(data, []domain.Issue{{Identifier: "WEB-1", Team: domain.Team{ID: "web", Name: "Web"}}, {Identifier: "WEB-2", Team: domain.Team{ID: "web", Name: "Web"}}}, nil)
	engineering, web := strings.Index(prompt, "Engineering rule"), strings.Index(prompt, "Web rule")
	if strings.Index(prompt, "Workspace rule") < 0 || engineering < 0 || web < engineering || strings.Count(prompt, "Web rule") != 1 {
		t.Fatalf("team guidance missing or out of order:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Team Web guidance:") || !strings.Contains(prompt, "wins over workspace guidance") {
		t.Fatalf("team guidance not labelled:\n%s", prompt)
	}
	if strings.Contains(prompt, "API rule") || strings.Contains(prompt, "Ops rule") {
		t.Fatalf("unrelated team guidance leaked:\n%s", prompt)
	}
	// A project or document session names its team explicitly.
	if prompt := workspaceAgentSystemPrompt(data, nil, nil, "ops"); !strings.Contains(prompt, "Team Ops guidance:\nOps rule") || strings.Contains(prompt, "Web rule") {
		t.Fatalf("explicit team guidance missing:\n%s", prompt)
	}
	if prompt := workspaceAgentSystemPrompt(data, nil, nil); strings.Contains(prompt, "Team guidance") {
		t.Fatalf("team guidance without a team in context:\n%s", prompt)
	}
	if ids := agentProjectTeamIDs([]domain.Project{{TeamIDs: []string{"web", "api"}}}); strings.Join(ids, ",") != "web,api" {
		t.Fatalf("project teams = %v", ids)
	}
}

func TestTeamAgentGuidanceIsSavedValidatedAndReachesChatAndThreads(t *testing.T) {
	var mu sync.Mutex
	prompts := []string{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		raw, _ := json.Marshal(payload["messages"])
		mu.Lock()
		prompts = append(prompts, string(raw))
		mu.Unlock()
		if payload["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "ok"}}}})
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "team-guidance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, APIKey: "test", Model: "flow-test", Timeout: 5 * time.Second}, agentClient: provider.Client()}
	handler := newHandler(s)
	parent := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Platform", "key": "PLA"}, http.StatusCreated)
	child := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Runtime", "key": "RTM", "parentTeamId": parent.ID}, http.StatusCreated)

	requestJSON[any](t, handler, http.MethodPatch, "/api/teams/"+child.ID+"/settings", map[string]any{"agentGuidance": strings.Repeat("a", teamAgentGuidanceLimit+1)}, http.StatusBadRequest)
	saved := requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+child.ID+"/settings", map[string]any{"agentGuidance": "  Runtime rule: end with RUNTIME-OK  "}, http.StatusOK)
	if saved.AgentGuidance != "Runtime rule: end with RUNTIME-OK" {
		t.Fatalf("saved guidance = %q", saved.AgentGuidance)
	}
	requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+parent.ID+"/settings", map[string]any{"agentGuidance": "Platform rule"}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPatch, "/api/workspace/agent-guidance", map[string]any{"instructions": "Workspace rule"}, http.StatusOK)
	reloaded := requestJSON[domain.TeamSettings](t, handler, http.MethodGet, "/api/teams/"+child.ID+"/settings", nil, http.StatusOK)
	if reloaded.AgentGuidance != saved.AgentGuidance {
		t.Fatalf("guidance not persisted: %q", reloaded.AgentGuidance)
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Guided", "teamId": child.ID}, http.StatusCreated)

	// Chat about the team's issue.
	session := requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "Hi", "location": "toolbar"}, http.StatusCreated)
	mu.Lock()
	prompts = nil
	mu.Unlock()
	requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]any{"message": "Summarize it", "issueIds": []string{issue.ID}}, http.StatusOK)
	mu.Lock()
	chat := strings.Join(prompts, "\n")
	mu.Unlock()
	workspace, platform, runtime := strings.Index(chat, "Workspace rule"), strings.Index(chat, "Platform rule"), strings.Index(chat, "Runtime rule")
	if workspace < 0 || platform < workspace || runtime < platform {
		t.Fatalf("chat prompt guidance order (workspace %d, parent %d, team %d):\n%s", workspace, platform, runtime, chat)
	}

	// The built-in agent working the issue (delegation / comment thread).
	app, err := repository.InstallApplication(t.Context(), "test-workspace", domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: repository.Bootstrap().Viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{child.ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	delegated := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Delegated", "teamId": child.ID, "delegateId": app.UserID}, http.StatusCreated)
	task, err := repository.AgentTask(t.Context(), "test-workspace", delegated.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	prompts = nil
	mu.Unlock()
	s.deliverApplicationTask(context.Background(), task)
	mu.Lock()
	thread := strings.Join(prompts, "\n")
	mu.Unlock()
	if !strings.Contains(thread, "Team Platform guidance") || !strings.Contains(thread, "Team Runtime guidance") || strings.Index(thread, "Platform rule") > strings.Index(thread, "Runtime rule") {
		t.Fatalf("thread prompt lacks team guidance:\n%s", thread)
	}
}

func TestTeamAgentGuidanceFollowsTeamSettingsPermission(t *testing.T) {
	settings := domain.TeamSettings{SettingsPermission: "owners"}
	request := httptest.NewRequest(http.MethodPatch, "/api/teams/team_1/settings", strings.NewReader(`{"agentGuidance":"x"}`))
	if permission := teamOperationPermission(settings, request); permission != "owners" || teamOperationAllowed(settings, permission, "member", "member") || !teamOperationAllowed(settings, permission, "owner", "member") {
		t.Fatalf("owners-only team settings must gate guidance: %q", permission)
	}
	settings.SettingsPermission = "allMembers"
	request = httptest.NewRequest(http.MethodPatch, "/api/teams/team_1/settings", strings.NewReader(`{"agentGuidance":"x"}`))
	if permission := teamOperationPermission(settings, request); !teamOperationAllowed(settings, permission, "member", "member") || teamOperationAllowed(settings, permission, "", "member") || teamOperationAllowed(settings, permission, "member", "guest") {
		t.Fatalf("team guidance permission: %q", permission)
	}
}

func TestAgentSessionWebhookCarriesTeamGuidanceChain(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	parent := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams?workspace=test-workspace", map[string]any{"name": "Platform", "key": "PLA"}, http.StatusCreated)
	child := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams?workspace=test-workspace", map[string]any{"name": "R&D <Runtime>", "key": "RTM", "parentTeamId": parent.ID}, http.StatusCreated)
	app, _ := installableApplication(t, handler, receiver.server.URL, []string{"AgentSessionEvent"})
	installed := requestJSON[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, handler, http.MethodPost, "/api/application-installations?workspace=test-workspace", map[string]any{
		"clientId": app.ClientID, "teamIds": []string{seed.Teams[0].ID, child.ID}, "scopes": []string{"read", "write", "app:assignable", "app:mentionable"}, "active": true,
	}, http.StatusOK).Application
	s.reconcileApplicationWebhooks(context.Background(), "test-workspace")
	requestJSON[any](t, handler, http.MethodPatch, "/api/workspace/agent-guidance?workspace=test-workspace", map[string]any{"instructions": "Always open draft pull requests."}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPatch, "/api/teams/"+parent.ID+"/settings?workspace=test-workspace", map[string]any{"agentGuidance": "Use the platform repo."}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPatch, "/api/teams/"+child.ID+"/settings?workspace=test-workspace", map[string]any{"agentGuidance": "Run <make test> first & report."}, http.StatusOK)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Guided", "teamId": child.ID, "delegateId": installed.UserID}, http.StatusCreated)
	task, err := s.store.AgentTask(context.Background(), "test-workspace", issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	s.deliverApplicationTask(context.Background(), task)
	event, _ := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "AgentSessionEvent" })
	guidance, _ := event["guidance"].([]any)
	if len(guidance) != 3 {
		t.Fatalf("guidance: %#v", event["guidance"])
	}
	first, _ := guidance[0].(map[string]any)
	second, _ := guidance[1].(map[string]any)
	third, _ := guidance[2].(map[string]any)
	if first["origin"] != "workspace" || first["teamId"] != nil || second["origin"] != "team" || second["teamId"] != parent.ID || second["teamName"] != "Platform" || second["body"] != "Use the platform repo." {
		t.Fatalf("guidance order or identity: %#v", guidance)
	}
	if third["origin"] != "team" || third["teamId"] != child.ID || third["teamKey"] != "RTM" || third["teamName"] != "R&D <Runtime>" || third["body"] != "Run <make test> first & report." {
		t.Fatalf("team rule: %#v", third)
	}
	promptContext, _ := event["promptContext"].(string)
	want := "<guidance>\n<guidance-rule origin=\"workspace\">Always open draft pull requests.</guidance-rule>\n" +
		"<guidance-rule origin=\"team\" team-name=\"Platform\">Use the platform repo.</guidance-rule>\n" +
		"<guidance-rule origin=\"team\" team-name=\"R&amp;D &lt;Runtime&gt;\">Run &lt;make test&gt; first &amp; report.</guidance-rule>\n</guidance>\n"
	if !strings.Contains(promptContext, want) {
		t.Fatalf("promptContext guidance:\n%s", promptContext)
	}
}
