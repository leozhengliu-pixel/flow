package main

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
	"flow/api/internal/store"
)

// aiModelPrimitives are the functions that send a request to a model or
// another AI service themselves (or only wrap one that does).
var aiModelPrimitives = map[string]bool{
	"requestAgentTurn": true, "requestAgentTurnWithoutTools": true, "requestAgentStructuredTurn": true,
	"requestStructuredProviderTurn": true, "requestOpenAIResponses": true, "requestChatCompletions": true,
	"requestAnthropicMessages": true, "agentProviderRequest": true, "requestAgentChatTurn": true, "requestAgent": true,
	"transcribeAgentMedia": true, "transcribeAgentAudio": true, "synthesizeSpeech": true,
}

// aiModelCallers lists every function outside the primitives that reaches a model, with the AI entry point (a name
// in aiPolicyEntryPoints) whose workspace AI policy gate covers it. A new caller fails
// TestEveryModelCallerHasAnAIPolicyEntryPoint until it is gated and listed here.
var aiModelCallers = map[string]string{
	"agentChat":                 "agent routes",
	"runAgentSession":           "agent chat sessions",
	"startAgentSessionTitle":    "agent chat sessions",
	"aiIssueFilter":             "AI issue filter",
	"executeAgentFileTool":      "agent tool calls",
	"executeWebTool":            "agent tool calls",
	"executeBuiltinApplication": "agent applications",
	"executeLoopRun":            "loop runs",
	"requestLoopIssueMatch":     "loop runs",
	"describeLoopAsync":         "loop descriptions",
	"requestLoopContract":       "loop output contracts",
	"generateCommentActions":    "comment actions",
	"aiPulseSummaries":          "pulse summaries",
	"getPulseSummaryAudio":      "pulse audio",
	"requestTriageAIPlan":       "triage intelligence",
	"classifyCodingTurnScope":   "coding sessions",
}

// aiPolicyEnv is a workspace (the SQLite test fixture) with the workspace AI policy applied, served by a server
// whose Flow Agent, media and speech endpoints are one fake provider that counts its requests.
type aiPolicyEnv struct {
	repo      *store.SQLiteStore
	s         *server
	handler   http.Handler
	workspace string
	viewer    domain.User
	calls     *atomic.Int32
}

func aiPolicyProvider(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	content := `{"filters":[],"text":""}`
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			raw, _ := json.Marshal(content)
			_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":`+string(raw)+`}}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(provider.Close)
	return provider, calls
}

func applyAIPolicy(t *testing.T, repo *store.SQLiteStore, workspace string, policy func(*domain.WorkspaceSettings)) {
	t.Helper()
	if policy == nil {
		return
	}
	if err := repo.MutateWorkspace(t.Context(), workspace, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
		policy(&data.WorkspaceSettings)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func newAIPolicyEnv(t *testing.T, policy func(*domain.WorkspaceSettings)) *aiPolicyEnv {
	t.Helper()
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	provider, calls := aiPolicyProvider(t)
	s := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true, agentAutoTitle: true, agentClient: provider.Client(), ttsClient: provider.Client()}
	s.agent = appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, APIKey: "test", Model: "test-model", MaxOutputTokens: 400, Timeout: 5 * time.Second, ToolsEnabled: true, WriteTools: true,
		Media: appconfig.AgentMediaConfig{BaseURL: provider.URL, TranscribeModel: "whisper-1", VideoModel: "video", Timeout: 5 * time.Second}}
	s.tts.Enabled, s.tts.BaseURL, s.tts.Model, s.tts.Voice = true, provider.URL, "tts", "alloy"
	data := repo.Bootstrap()
	applyAIPolicy(t, repo, data.Workspace.URLKey, policy)
	return &aiPolicyEnv{repo: repo, s: s, handler: newHandler(s), workspace: data.Workspace.URLKey, viewer: repo.Account().Viewer, calls: calls}
}

// request is a workspace request as the signed-in member (AUTH_DISABLED's viewer).
func (env *aiPolicyEnv) request(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Key", env.workspace)
	ctx := context.WithValue(request.Context(), workspaceKeyContextKey{}, env.workspace)
	ctx = context.WithValue(ctx, authUserContextKey{}, env.viewer)
	return request.WithContext(store.ContextWithActor(ctx, env.viewer))
}

func (env *aiPolicyEnv) serve(method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Key", env.workspace)
	recorder := httptest.NewRecorder()
	env.handler.ServeHTTP(recorder, request)
	return recorder
}

// aiPolicyRefusal reports whether a response is the AI policy's 403.
func aiPolicyRefusal(recorder *httptest.ResponseRecorder) bool {
	return recorder.Code == http.StatusForbidden && strings.Contains(recorder.Body.String(), aiPolicyRefusalMessage)
}

// aiPolicyEntryPoints are the paths by which workspace content reaches a model or another AI service. Each run
// drives its entry point in a workspace with the given policy and reports whether the entry point refused; with
// no policy it must not refuse (so the policy, not the fixture, is what refuses), and when it refuses no request
// may have reached the provider.
var aiPolicyEntryPoints = []struct {
	name string
	run  func(t *testing.T, policy func(*domain.WorkspaceSettings)) (refused bool, providerCalls int32)
}{
	{"agent routes", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		env := newAIPolicyEnv(t, policy)
		recorder := env.serve(http.MethodPost, "/api/agent/chat", `{"message":"What is open?"}`)
		return aiPolicyRefusal(recorder), env.calls.Load()
	}},
	{"agent chat sessions", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		env := newAIPolicyEnv(t, policy)
		_, err := env.s.runAgentSession(env.request(http.MethodPost, "/api/agent/sessions/missing/stream", ""), "missing", nil)
		return errors.Is(err, store.ErrAuthForbidden), env.calls.Load()
	}},
	{"AI issue filter", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		env := newAIPolicyEnv(t, policy)
		body := `{"query":"urgent bugs assigned to Ada","fields":{"assignee":[{"id":"usr_ada","label":"Ada Lovelace"}]}}`
		routed := aiPolicyRefusal(env.serve(http.MethodPost, "/api/ai/issue-filter", body))
		// The handler refuses on its own too (requests that reach it without the workspace middleware).
		recorder := httptest.NewRecorder()
		env.s.aiIssueFilter(recorder, env.request(http.MethodPost, "/api/ai/issue-filter", body))
		direct := aiPolicyRefusal(recorder)
		if routed != direct {
			t.Errorf("route refused=%t, handler refused=%t", routed, direct)
		}
		return routed && direct, env.calls.Load()
	}},
	{"agent tool calls", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		// transcribe_media (media transcription), web_search / fetch_url and read_file: every tool call checks the
		// policy again, in chat, agent threads, loops and on the agent worker (runAgentToolCall).
		env := newAIPolicyEnv(t, policy)
		request := env.request(http.MethodPost, "/api/agent/sessions/s/stream", "")
		data := env.s.workspaceData(request)
		refused := true
		for _, name := range []string{transcribeMediaTool, webSearchToolName, "fetch_url"} {
			_, err := env.s.runAgentToolCall(request, data, domain.AgentToolCall{ID: "call_" + name, Name: name, Arguments: json.RawMessage(`{"fileId":"file_missing","query":"flow","url":"https://example.com"}`)})
			refused = refused && errors.Is(err, store.ErrAuthForbidden)
		}
		return refused, env.calls.Load()
	}},
	{"agent applications", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		// Delegating an issue to the built-in agent (or an installed agent application).
		env := newAIPolicyEnv(t, nil)
		data := env.repo.Bootstrap()
		app, err := env.repo.InstallApplication(t.Context(), env.workspace, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: env.viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
		if err != nil {
			t.Fatal(err)
		}
		unstarted := stateForTeamByType(&data, data.Teams[0].ID, "unstarted")
		issue := requestJSON[domain.Issue](t, env.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Export revamp", "teamId": data.Teams[0].ID, "stateId": unstarted.ID}, http.StatusCreated)
		issue = requestJSON[domain.Issue](t, env.handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"delegateId": app.UserID}, http.StatusOK)
		task, err := env.repo.AgentTask(t.Context(), env.workspace, issue.AgentSessionID)
		if err != nil {
			t.Fatal(err)
		}
		applyAIPolicy(t, env.repo, env.workspace, policy)
		env.s.deliverApplicationTask(t.Context(), task)
		activities, _ := env.repo.AgentActivities(t.Context(), env.workspace, task.ID, "")
		refused := slices.ContainsFunc(activities, func(item domain.AgentActivity) bool {
			return item.Type == "canceled" && strings.Contains(item.Body, "disabled by workspace policy")
		})
		return refused, env.calls.Load()
	}},
	{"loop runs", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		env := newAIPolicyEnv(t, policy)
		loop := domain.Loop{ID: "loop_policy", Name: "Policy", OwnerID: env.viewer.ID, Instructions: "Summarize open issues."}
		stop := errors.New("connectors are not part of this test")
		_, err := env.s.prepareLoopRun(t.Context(), env.workspace, loop, loopTrigger{}, &loopRunRecorder{s: env.s, workspace: env.workspace}, func(*http.Request, domain.Bootstrap, domain.User) (context.Context, error) {
			return nil, stop
		})
		return errors.Is(err, store.ErrAuthForbidden), env.calls.Load()
	}},
	{"loop descriptions", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		env := newAIPolicyEnv(t, policy)
		return !env.s.loopDescriptionAllowed(env.workspace, domain.Loop{ID: "loop_policy", Instructions: "Summarize open issues."}), env.calls.Load()
	}},
	{"loop output contracts", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		env := newAIPolicyEnv(t, policy)
		contract := env.s.loopContractFor(t.Context(), env.workspace, domain.Loop{ID: "loop_policy", Instructions: "Label new bugs."})
		return contract != nil && contract.Error == aiPolicyRefusalMessage, env.calls.Load()
	}},
	{"comment actions", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		env := newAIPolicyEnv(t, policy)
		data := env.s.workspaceData(env.request(http.MethodGet, "/api/bootstrap", ""))
		data.Viewer = env.viewer
		if len(data.Issues) == 0 {
			t.Fatal("the fixture has no issue")
		}
		refused := !env.s.commentActionsAllowed(data, data.Issues[0])
		if refused {
			// A queued run checks again before it reads the issue.
			env.s.generateCommentActions(t.Context(), env.workspace, data.Issues[0].ID, "comment_missing", env.viewer)
		}
		return refused, env.calls.Load()
	}},
	{"pulse summaries", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		env := newAIPolicyEnv(t, policy)
		metadata, _ := env.repo.WorkspaceSettingsMetadata(env.workspace)
		return !env.s.pulseAIAllowed(metadata.WorkspaceSettings, "member"), env.calls.Load()
	}},
	{"pulse audio", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		env := newAIPolicyEnv(t, policy)
		metadata, _ := env.repo.WorkspaceSettingsMetadata(env.workspace)
		return !env.s.pulseAudioAllowed(metadata.WorkspaceSettings, "member"), env.calls.Load()
	}},
	{"triage intelligence", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		fixture := newTriageAIFixture(t, func() string {
			return `{"duplicate":null,"related":[],"assignee":null,"project":null,"labels":[],"team":null,"thinking":"Nothing related."}`
		})
		applyAIPolicy(t, fixture.repository, fixture.bootstrap.Workspace.URLKey, policy)
		created := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{
			"title": "App crashes when importing a large CSV file", "description": "Uploading a 200MB CSV crashes the importer.", "teamId": fixture.team.ID, "stateId": fixture.backlogID,
		}, http.StatusCreated)
		generated := fixture.waitGenerated(t, created.ID)
		fixture.waitIdle(t)
		return generated.SuggestionsSource == triageSourceHeuristic, fixture.provider.calls.Load()
	}},
	{"coding sessions", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		// Starting, prompting, restarting and approving a session (coding.Service.AIPolicy), and each model request
		// of a running sandbox through the LLM proxy (codingLLMPolicy).
		s, handler := codingTestServer(t, "")
		requestJSON[codingSettingsView](t, handler, http.MethodPatch, "/api/coding/settings", map[string]any{"enabled": true}, http.StatusOK)
		workspace := s.store.Bootstrap().Workspace.URLKey
		applyAIPolicy(t, s.store, workspace, policy)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/coding/sessions", strings.NewReader(`{"prompt":"Fix the flaky test","source":"agentChat"}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		started := recorder.Code == http.StatusForbidden && strings.Contains(recorder.Body.String(), "aiDisabledByPolicy")
		proxy := errors.Is(s.codingLLMPolicy(t.Context(), codingSandboxCaller{grant: coding.SandboxGrant{WorkspaceKey: workspace}, session: domain.CodingSession{CreatorID: s.store.Account().Viewer.ID}}), llmproxy.ErrPolicy)
		if started != proxy {
			t.Errorf("session start refused=%t (%d %s), LLM proxy refused=%t", started, recorder.Code, recorder.Body.String(), proxy)
		}
		return started && proxy, 0
	}},
	{"agent worker runs", func(t *testing.T, policy func(*domain.WorkspaceSettings)) (bool, int32) {
		// The worker calls the model itself: a run queued before the policy changed is failed at claim time.
		env := newAIPolicyEnv(t, policy)
		principal, _ := json.Marshal(agentRunPrincipal{Mode: agentRunModeUser, UserID: env.viewer.ID})
		runs := env.s.aiPolicyClaimedRuns(t.Context(), "worker_test", []store.AgentRun{{ID: "run_policy", WorkspaceKey: env.workspace, Kind: "background", Status: "running", Principal: principal}})
		return len(runs) == 0, env.calls.Load()
	}},
}

func TestAIEntryPointsHonourWorkspaceAIPolicy(t *testing.T) {
	policies := []struct {
		name   string
		policy func(*domain.WorkspaceSettings)
	}{
		{"AI switch off", func(settings *domain.WorkspaceSettings) { settings.FeatureFlags["ai-agent"] = false }},
		{"legacy ai switch off", func(settings *domain.WorkspaceSettings) { settings.FeatureFlags["ai"] = false }},
		{"HIPAA", func(settings *domain.WorkspaceSettings) { settings.HIPAACompliance = true }},
	}
	for _, entry := range aiPolicyEntryPoints {
		t.Run(entry.name, func(t *testing.T) {
			t.Run("allowed", func(t *testing.T) {
				if refused, _ := entry.run(t, nil); refused {
					t.Fatal("refused without a policy: the fixture, not the policy, refuses")
				}
			})
			for _, mode := range policies {
				t.Run(mode.name, func(t *testing.T) {
					refused, calls := entry.run(t, mode.policy)
					if !refused {
						t.Fatal("not refused")
					}
					if calls != 0 {
						t.Fatalf("the provider got %d requests", calls)
					}
				})
			}
		})
	}
}

func TestAIPolicyRegistryNamesKnownEntryPoints(t *testing.T) {
	names := map[string]bool{}
	for _, entry := range aiPolicyEntryPoints {
		if names[entry.name] {
			t.Fatalf("duplicate entry point %q", entry.name)
		}
		names[entry.name] = true
	}
	for caller, entry := range aiModelCallers {
		if !names[entry] {
			t.Errorf("%s names entry point %q, which aiPolicyEntryPoints does not test", caller, entry)
		}
	}
}

// modelCallers parses the package's non-test sources and returns each function that calls a primitive (or the web
// search / fetch providers) directly.
func modelCallers(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || aiModelPrimitives[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				hit := aiModelPrimitives[selector.Sel.Name]
				if inner, ok := selector.X.(*ast.SelectorExpr); ok && (inner.Sel.Name == "webSearch" && selector.Sel.Name == "Search" || inner.Sel.Name == "webFetcher") {
					hit = true
				}
				if hit {
					found[fn.Name.Name] = true
				}
				return true
			})
		}
	}
	callers := make([]string, 0, len(found))
	for name := range found {
		callers = append(callers, name)
	}
	sort.Strings(callers)
	return callers
}

func TestEveryModelCallerHasAnAIPolicyEntryPoint(t *testing.T) {
	callers := modelCallers(t)
	for _, caller := range callers {
		if _, ok := aiModelCallers[caller]; !ok {
			t.Errorf("%s reaches a model but is not in aiModelCallers: gate it with the workspace AI policy and register it", caller)
		}
	}
	for caller := range aiModelCallers {
		if !slices.Contains(callers, caller) {
			t.Errorf("aiModelCallers lists %s, which no longer reaches a model", caller)
		}
	}
}
