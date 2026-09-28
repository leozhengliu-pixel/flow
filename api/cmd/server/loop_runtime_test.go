package main

import (
	"encoding/json"
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

// fakeLoopProvider answers each Responses request with the next scripted reply.
type fakeLoopProvider struct {
	mu      sync.Mutex
	replies []string
	tools   [][]string
	inputs  []string
}

func (p *fakeLoopProvider) serve(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	names := []string{}
	if tools, ok := payload["tools"].([]any); ok {
		for _, tool := range tools {
			if item, ok := tool.(map[string]any); ok {
				names = append(names, item["name"].(string))
			}
		}
	}
	raw, _ := json.Marshal([]any{payload["instructions"], payload["input"]})
	p.mu.Lock()
	p.tools, p.inputs = append(p.tools, names), append(p.inputs, string(raw))
	reply := "Nothing to do."
	if len(p.replies) > 0 {
		reply, p.replies = p.replies[0], p.replies[1:]
	}
	p.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	if strings.HasPrefix(reply, "tool:") {
		name, args, _ := strings.Cut(strings.TrimPrefix(reply, "tool:"), " ")
		quoted, _ := json.Marshal(args)
		_, _ = w.Write([]byte("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"call_id\":\"call_1\",\"type\":\"function_call\",\"name\":\"" + name + "\",\"arguments\":\"\"}}\n\n" +
			"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_1\",\"arguments\":" + string(quoted) + "}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
		return
	}
	quoted, _ := json.Marshal(reply)
	_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":" + string(quoted) + "}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
}

func newLoopTestServer(t *testing.T, provider *fakeLoopProvider) (*server, http.Handler) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(upstream.Close)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	srv := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: upstream.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: upstream.Client()}
	return srv, newHandler(srv)
}

func waitForLoopRun(t *testing.T, handler http.Handler, loopID string) domain.LoopRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs := requestJSON[[]domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loopID+"/runs", nil, http.StatusOK)
		if len(runs) > 0 && runs[0].Status != "running" {
			return runs[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("loop run did not finish")
	return domain.LoopRun{}
}

func TestScheduledLoopRunsNowAndRecordsOutput(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"Posted the weekly summary."}}
	_, handler := newLoopTestServer(t, provider)
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Weekly summary", "instructions": "Summarize open bugs."}, http.StatusCreated)
	run := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	if run.Status != "running" || run.Trigger != "manual" {
		t.Fatalf("started run = %#v", run)
	}
	finished := waitForLoopRun(t, handler, loop.ID)
	if finished.Status != "completed" || finished.Output != "Posted the weekly summary." {
		t.Fatalf("finished run = %#v", finished)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !strings.Contains(provider.inputs[0], "Summarize open bugs.") {
		t.Fatalf("loop instructions missing from prompt: %s", provider.inputs[0])
	}
	for _, name := range provider.tools[0] {
		if strings.Contains(name, "diff") {
			t.Fatalf("code intelligence tool %s offered while disabled", name)
		}
	}
	if bootstrap := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK); bootstrap["loopRuns"] != nil {
		t.Fatal("loop runs must not be sent in the bootstrap")
	}
}

func TestIssueLoopTriggersAndStaysOnTriggeringIssue(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newLoopTestServer(t, provider)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	teamID := bootstrap.Teams[0].ID
	other := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Unrelated", "teamId": teamID}, http.StatusCreated)
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{
		"name": "Triage bugs", "instructions": "Comment on new bugs.", "triggerType": "issue",
		"triggerConfig": map[string]any{"action": "created"}, "allowChangesOutsideTrigger": false,
	}, http.StatusCreated)
	provider.mu.Lock()
	provider.replies = []string{"tool:save_comment " + `{"issueId":"` + other.ID + `","body":"hi"}`, "Done."}
	provider.mu.Unlock()
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Crash on save", "teamId": teamID}, http.StatusCreated)
	run := waitForLoopRun(t, handler, loop.ID)
	if run.Trigger != "event" || run.EntityID != issue.ID || run.EntityIdentifier != issue.Identifier {
		t.Fatalf("run = %#v", run)
	}
	if len(run.ToolCalls) != 1 || run.ToolCalls[0].Status != "blocked" || !strings.Contains(run.ToolCalls[0].Error, "only change the issue") {
		t.Fatalf("write outside the trigger was not blocked: %#v", run.ToolCalls)
	}
	// Updates do not match a created-only trigger.
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"priority": 2}, http.StatusOK)
	time.Sleep(100 * time.Millisecond)
	if runs := requestJSON[[]domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs", nil, http.StatusOK); len(runs) != 1 {
		t.Fatalf("created-only loop ran %d times", len(runs))
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusBadRequest)
}

func TestLoopRunNeedsConfiguredAgent(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "No agent"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusConflict)
}

func TestNextLoopRun(t *testing.T) {
	created := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		config map[string]any
		after  time.Time
		want   time.Time
	}{
		{map[string]any{"interval": float64(1), "unit": "day", "time": "10:00"}, time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC), time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)},
		{map[string]any{"interval": float64(1), "unit": "day", "time": "10:00"}, time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC), time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)},
		{map[string]any{"interval": float64(2), "unit": "week", "time": "09:30", "starting": "2026-09-07"}, time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 21, 9, 30, 0, 0, time.UTC)},
		{map[string]any{"interval": float64(1), "unit": "month", "time": "10:00", "starting": "2026-01-15"}, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)},
		{map[string]any{"interval": float64(1), "unit": "day", "time": "10:00", "timezone": "Asia/Shanghai"}, time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC), time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC)},
		{map[string]any{"interval": float64(1), "unit": "day", "time": "10:00", "starting": "2026-12-01"}, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)},
	}
	for index, item := range cases {
		if got := nextLoopRun(item.config, created, item.after); !got.Equal(item.want) {
			t.Errorf("case %d: next = %s, want %s", index, got, item.want)
		}
	}
}

func TestLoopSourceTrust(t *testing.T) {
	loop := domain.Loop{TrustedSourceKeys: []string{"integration:slack"}}
	if loopSourceTrusted(domain.WorkspaceSettings{}, loop, "integration:slack") {
		t.Fatal("external triggers must be off unless the workspace allows them")
	}
	settings := domain.WorkspaceSettings{ExternalLoopTriggers: true}
	if !loopSourceTrusted(settings, loop, "integration:slack") || loopSourceTrusted(settings, loop, "integration:email") {
		t.Fatal("loop trusted sources were not applied")
	}
	settings.TrustedSourcesMode, settings.TrustedSourcesAllowlist = "allowlist", []string{"integration:email"}
	if !loopSourceTrusted(settings, loop, "integration:email") {
		t.Fatal("workspace allowlist was not applied")
	}
}
