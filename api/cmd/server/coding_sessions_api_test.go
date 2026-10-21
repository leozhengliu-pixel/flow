package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func codingTestServer(t *testing.T, runtimeAddr string) (*server, http.Handler) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "coding-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	s.agent.Enabled = true
	for _, key := range repository.WorkspaceKeys() {
		s.ensureBuiltinApplication(context.Background(), key)
	}
	s.initCoding(appconfig.CodingConfig{
		Enabled: true, Provider: appconfig.CodingProviderFake, RuntimeAddr: runtimeAddr, MaxSessions: 5, WorkspaceSessions: 5, ReconcileInterval: time.Second,
		Timeouts: appconfig.CodingTimeouts{Queue: time.Hour, Approval: time.Hour, Provision: time.Hour, Start: time.Hour, Setup: time.Hour, Turn: time.Hour, Heartbeat: time.Hour, Publish: time.Hour, Idle: time.Hour, StopGrace: time.Minute, MaxSession: time.Hour},
		LLM:      appconfig.CodingLLMConfig{CodexModels: []string{"gpt-5-codex"}},
		Regions:  []string{"eu"},
	})
	return s, newHandler(s)
}

func TestCodingSessionsAPI(t *testing.T) {
	s, handler := codingTestServer(t, "")
	if !s.codingEnabled() {
		t.Fatal("the fake provider must enable coding sessions")
	}
	// Disabled for the workspace until the feature flag is on.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/coding/sessions", map[string]any{"prompt": "Fix it"}, http.StatusNotFound)
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/coding/settings", map[string]any{"pinnedRegion": "mars"}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/coding/settings", map[string]any{"harness": "nope"}, http.StatusBadRequest)
	settings := requestJSON[codingSettingsView](t, handler, http.MethodPatch, "/api/coding/settings", map[string]any{"enabled": true, "harness": "claude", "pinnedRegion": "eu", "reasoningEffort": "high", "loopRepositoryAccess": "selected", "loopRepositories": []string{"env1", "env1", " "}}, http.StatusOK)
	if !settings.Enabled || settings.Harness != domain.CodingHarnessClaudeCode || settings.PinnedRegion != "eu" || len(settings.LoopRepositories) != 1 || settings.Model != "auto" {
		t.Fatalf("settings: %+v", settings)
	}
	settings = requestJSON[codingSettingsView](t, handler, http.MethodPatch, "/api/coding/settings", map[string]any{"harness": "codex"}, http.StatusOK)
	if got := requestJSON[codingSettingsView](t, handler, http.MethodGet, "/api/coding/settings", nil, http.StatusOK); got.Harness != domain.CodingHarnessCodex || !got.Enabled {
		t.Fatalf("persisted settings: %+v", got)
	}
	if models := requestJSON[[]map[string]any](t, handler, http.MethodGet, "/api/coding/models", nil, http.StatusOK); len(models) != 2 {
		t.Fatalf("models: %+v", models)
	}
	status := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/coding/status", nil, http.StatusOK)
	if status["enabled"] != true || status["provider"] != "fake" {
		t.Fatalf("status: %+v", status)
	}

	create := func(key string, want int) domain.CodingSession {
		body := bytes.NewBufferString(`{"prompt":"Fix the flaky test","source":"agentChat"}`)
		request := httptest.NewRequest(http.MethodPost, "/api/coding/sessions", body)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
		}
		var session domain.CodingSession
		if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		return session
	}
	session := create("k1", http.StatusCreated)
	if again := create("k1", http.StatusOK); again.ID != session.ID {
		t.Fatalf("Idempotency-Key must return the same session: %s %s", again.ID, session.ID)
	}
	if session.Phase != domain.CodingPhaseQueued || session.State != domain.CodingSessionPending || session.ReasoningEffort != "high" {
		t.Fatalf("created: %+v", session)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/coding/sessions", map[string]any{"prompt": "x", "source": "loop"}, http.StatusBadRequest)
	if list := requestJSON[[]domain.CodingSession](t, handler, http.MethodGet, "/api/coding/sessions", nil, http.StatusOK); len(list) != 1 {
		t.Fatalf("list: %d", len(list))
	}
	got := requestJSON[domain.CodingSession](t, handler, http.MethodGet, "/api/coding/sessions/"+session.ID, nil, http.StatusOK)
	if got.AgentSessionID == "" {
		t.Fatalf("get: %+v", got)
	}
	requestJSON[map[string]any](t, handler, http.MethodGet, "/api/coding/sessions/missing", nil, http.StatusNotFound)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/coding/sessions/"+session.ID+"/restart", nil, http.StatusConflict)
	// "Start over with <model>": a catalog model, on a finished session only.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/coding/sessions/"+session.ID+"/restart", map[string]any{"model": "gpt-9"}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/coding/sessions/"+session.ID+"/restart", map[string]any{"model": "gpt-5-codex"}, http.StatusConflict)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/coding/sessions/"+session.ID+"/dismiss", nil, http.StatusConflict)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/coding/sessions/"+session.ID+"/approve", map[string]any{}, http.StatusConflict)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/coding/sessions/"+session.ID+"/prompt", map[string]any{"prompt": ""}, http.StatusBadRequest)
	stopped := requestJSON[domain.CodingSession](t, handler, http.MethodPost, "/api/coding/sessions/"+session.ID+"/stop", nil, http.StatusOK)
	if !stopped.StopRequested {
		t.Fatalf("stop: %+v", stopped)
	}
	// Archiving (removing the agent and choosing to archive its session) is idempotent and keeps the first time.
	archived := requestJSON[domain.CodingSession](t, handler, http.MethodPost, "/api/coding/sessions/"+session.ID+"/archive", nil, http.StatusOK)
	if archived.ArchivedAt == nil {
		t.Fatalf("archive: %+v", archived)
	}
	if again := requestJSON[domain.CodingSession](t, handler, http.MethodPost, "/api/coding/sessions/"+session.ID+"/archive", nil, http.StatusOK); again.ArchivedAt == nil || !again.ArchivedAt.Equal(*archived.ArchivedAt) {
		t.Fatalf("second archive: %+v", again)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/coding/sessions/missing/archive", nil, http.StatusNotFound)
	usage := requestJSON[codingUsageView](t, handler, http.MethodGet, "/api/coding/usage", nil, http.StatusOK)
	if usage.ByUser == nil || usage.TotalCostMicros != 0 {
		t.Fatalf("usage: %+v", usage)
	}
	requestJSON[map[string]any](t, handler, http.MethodGet, "/api/coding/usage?since=yesterday", nil, http.StatusBadRequest)

	// The application worker never delivers a coding anchor.
	metadata, _ := s.store.WorkspaceSettingsMetadata("")
	task, err := s.store.AgentTask(context.Background(), metadata.Workspace.URLKey, got.AgentSessionID)
	if err != nil || !s.codingAnchorTask(context.Background(), task) {
		t.Fatalf("anchor: %v", err)
	}

	// Runner routes require a sandbox token.
	for _, token := range []string{"", "Bearer nope", "Bearer fcs_unknown"} {
		request := httptest.NewRequest(http.MethodGet, "/internal/coding-runtime/bootstrap", nil)
		if token != "" {
			request.Header.Set("Authorization", token)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d %s", token, recorder.Code, recorder.Body.String())
		}
	}
}

func TestCodingRuntimeListener(t *testing.T) {
	s, handler := codingTestServer(t, "127.0.0.1:5111")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := s.serveCodingRuntime(ctx, handler)
	if err != nil || addr == "" {
		t.Fatalf("listener: %q %v", addr, err)
	}
	get := func(url string) int {
		response, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if code := get("http://" + addr + "/internal/coding-runtime/bootstrap"); code != http.StatusUnauthorized {
		t.Fatalf("runtime route on the runtime listener: %d", code)
	}
	if code := get("http://" + addr + "/api/health"); code != http.StatusNotFound {
		t.Fatalf("web routes are not served on the runtime listener: %d", code)
	}
	main := s.codingMainHandler(handler)
	for path, want := range map[string]int{"/internal/coding-runtime/bootstrap": http.StatusNotFound, "/internal/llm-proxy/openai/v1/models": http.StatusNotFound} {
		recorder := httptest.NewRecorder()
		main.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != want {
			t.Fatalf("main listener %s: %d", path, recorder.Code)
		}
	}
	cancel()
	// Without FLOW_CODING_RUNTIME_ADDR nothing listens and the main handler is unchanged.
	plain, plainHandler := codingTestServer(t, "")
	if addr, err := plain.serveCodingRuntime(context.Background(), plainHandler); addr != "" || err != nil {
		t.Fatalf("no listener expected: %q %v", addr, err)
	}
	recorder := httptest.NewRecorder()
	plain.codingMainHandler(plainHandler).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/internal/coding-runtime/bootstrap", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("runtime routes stay on the main listener: %d", recorder.Code)
	}
}
