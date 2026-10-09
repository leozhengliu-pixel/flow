package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// testLoopLimits are budgets small and fast enough for tests.
func testLoopLimits() *loopRunLimits {
	return &loopRunLimits{MaxConcurrentPerWorkspace: 4, MaxConcurrent: 8, MaxToolOutputBytes: 48 << 10, MaxContextBytes: 768 << 10, MaxToolCalls: 20, MaxTurns: 8, Timeout: 10 * time.Second, LeaseTTL: 200 * time.Millisecond, RetryAttempts: 2, RetryBackoff: 5 * time.Millisecond}
}

func newReliableLoopServer(t *testing.T, provider *fakeLoopProvider) (*server, http.Handler) {
	t.Helper()
	srv, handler := newLoopTestServer(t, provider)
	srv.loopRunLimits.Store(testLoopLimits())
	return srv, handler
}

// setLoopLimits changes a test server's budgets between runs.
func setLoopLimits(srv *server, change func(*loopRunLimits)) {
	limits := srv.loopLimits()
	change(&limits)
	srv.loopRunLimits.Store(&limits)
}

// waitForRun polls a run until it (and any reply on it) stopped running.
func waitForRun(t *testing.T, handler http.Handler, loopID, runID string) domain.LoopRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loopID+"/runs/"+runID, nil, http.StatusOK)
		busy := run.Status == "running"
		for _, reply := range run.Replies {
			busy = busy || reply.Status == "running"
		}
		if !busy {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("loop run did not finish")
	return domain.LoopRun{}
}

func startTestRun(t *testing.T, handler http.Handler, name, instructions string, extra map[string]any) (domain.Loop, domain.LoopRun) {
	t.Helper()
	input := map[string]any{"name": name, "instructions": instructions, "status": "published"}
	for key, value := range extra {
		input[key] = value
	}
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", input, http.StatusCreated)
	run := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	return loop, run
}

func firstProjectID(t *testing.T, handler http.Handler) string {
	t.Helper()
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Projects) == 0 {
		t.Fatal("fixture has no projects")
	}
	return bootstrap.Projects[0].ID
}

// Progress is written to the run's own row and event log and published as a
// lightweight signal; it never goes through a workspace mutation.
func TestLoopRunProgressSkipsWorkspaceMutations(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	projectID := firstProjectID(t, handler)
	var mu sync.Mutex
	published := []string{}
	events, unsubscribe := srv.realtime.subscribe("test-workspace")
	defer unsubscribe()
	go func() {
		for event := range events {
			mu.Lock()
			published = append(published, event.Type)
			mu.Unlock()
		}
	}()
	provider.replies = []string{
		`tool:report_progress {"title":"Gathering projects"}`,
		`tool:list_projects {"limit":5}`,
		`tool:save_status_update {"type":"project","project":"` + projectID + `","body":"On track this week.","health":"onTrack"}`,
		`tool:finish_run {"status":"done","summary":"Posted the weekly update.","done":["Posted an update on the project"]}`,
	}
	loop, started := startTestRun(t, handler, "Weekly project update", "Post a weekly project update on each active project.", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "completed" || run.FailureReason != "" || run.Summary == nil || run.Summary.Status != "done" || run.Produced["statusUpdate"] != 1 {
		t.Fatalf("run = %+v", run)
	}
	if len(run.Steps) != 1 || len(run.ToolCalls) != 2 || !strings.Contains(run.Output, "Posted the weekly update.") {
		t.Fatalf("recorded steps %+v calls %+v output %q", run.Steps, run.ToolCalls, run.Output)
	}
	if len(run.ExpectedOutputs) != 1 || run.ExpectedOutputs[0] != "statusUpdate" {
		t.Fatalf("expected outputs = %v", run.ExpectedOutputs)
	}
	domainEvents, err := srv.store.Events(context.Background(), loop.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range domainEvents {
		if event.Type == "loop.run_progress" || event.Type == "loop.run_finished" {
			t.Fatalf("run progress went through a workspace mutation: %s", event.Type)
		}
	}
	log, err := srv.store.LoopRunEvents(context.Background(), "test-workspace", run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, event := range log {
		kinds = append(kinds, event.Kind)
	}
	if got := strings.Join(kinds, ","); got != "step,tool_started,tool_finished,tool_started,tool_finished,summary,finished" {
		t.Fatalf("event log = %s", got)
	}
	api := requestJSON[[]store.LoopRunEvent](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+run.ID+"/events?after=5", nil, http.StatusOK)
	if len(api) != 2 || api[0].Kind != "summary" {
		t.Fatalf("events after 5 = %+v", api)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(published, ",")
	if !strings.Contains(joined, "loop_run.progress") || !strings.Contains(joined, "loop_run.finished") || strings.Contains(joined, "loop.run_progress") {
		t.Fatalf("published = %s", joined)
	}
	if loops := requestJSON[[]domain.Loop](t, handler, http.MethodGet, "/api/loops", nil, http.StatusOK); loops[0].RunCount30d != 1 {
		t.Fatalf("run count = %d", loops[0].RunCount30d)
	}
}

// A loop whose instructions call for output is never plain completed when
// the run produced none.
func TestLoopRunAcceptance(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)

	provider.replies = []string{`tool:list_projects {"limit":5}`, "All projects look fine."}
	loop, started := startTestRun(t, handler, "Flow 项目周更新", "List the projects and their issues.", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "needs_review" || run.FailureReason != "no_output" || !strings.Contains(run.Error, "No output produced") {
		t.Fatalf("run without updates = %+v", run)
	}

	provider.replies = []string{`tool:save_status_update {"type":"project","project":"missing-project","body":"x"}`, `tool:finish_run {"status":"done","summary":"Posted."}`}
	started = requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	run = waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "needs_review" || run.FailureReason != "tool_error" || !strings.Contains(run.Error, "Posted status update failed") {
		t.Fatalf("run whose update failed = %+v", run)
	}

	provider.replies = []string{`tool:finish_run {"status":"incomplete","summary":"Could not reach two projects.","notDone":["Project A: no access"]}`}
	plain, started := startTestRun(t, handler, "Digest", "Summarize open bugs.", nil)
	run = waitForRun(t, handler, plain.ID, started.ID)
	if run.Status != "needs_review" || run.FailureReason != "incomplete" || !strings.Contains(run.Error, "Project A: no access") {
		t.Fatalf("incomplete run = %+v", run)
	}

	// Explicit "none" overrides the inference.
	provider.replies = []string{"Nothing needed posting."}
	quiet, started := startTestRun(t, handler, "Weekly update check", "Post a weekly update when something changed.", map[string]any{"expectedOutputs": []string{"none"}})
	if quiet.ExpectedOutputs[0] != "none" {
		t.Fatalf("expected outputs = %v", quiet.ExpectedOutputs)
	}
	if run = waitForRun(t, handler, quiet.ID, started.ID); run.Status != "completed" {
		t.Fatalf("run of a loop expecting nothing = %+v", run)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+quiet.ID, map[string]any{"expectedOutputs": []string{"poem"}}, http.StatusBadRequest)
}

func TestInferLoopOutputs(t *testing.T) {
	cases := map[string]string{
		"Flow 项目周更新":                               "statusUpdate",
		"Post a status update on each project":     "statusUpdate",
		"Create an issue for every failing check":  "issue",
		"Comment on new bugs with a triage note":   "comment",
		"Summarize open bugs":                      "",
		"Label new issues and set their priority.": "",
	}
	for text, want := range cases {
		if got := strings.Join(inferLoopOutputs(text), ","); got != want {
			t.Errorf("inferLoopOutputs(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestLoopRunCancelStopsTheModelRequest(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"block"}}
	srv, handler := newReliableLoopServer(t, provider)
	loop, started := startTestRun(t, handler, "Slow", "Think for a long time.", nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		provider.mu.Lock()
		waiting := len(provider.inputs)
		provider.mu.Unlock()
		if waiting > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancelled := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+started.ID+"/cancel", nil, http.StatusOK)
	if cancelled.Status != "cancelled" || cancelled.FailureReason != "cancelled" || cancelled.FinishedAt == nil {
		t.Fatalf("cancelled run = %+v", cancelled)
	}
	time.Sleep(20 * time.Millisecond)
	provider.mu.Lock()
	aborted := provider.aborted
	provider.mu.Unlock()
	if aborted != 1 {
		t.Fatalf("the in-flight model request was not cancelled (aborted=%d)", aborted)
	}
	if srv.loopRunRegistry().get(started.ID) != nil {
		t.Fatal("cancelled run still registered")
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+started.ID+"/cancel", nil, http.StatusConflict)
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/loop_run_missing/cancel", nil, http.StatusNotFound)
}

// Only people who can edit the loop, or who started the run, can cancel it.
func TestLoopRunCancelPermissions(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"block"}}
	upstream := httptest.NewServer(http.HandlerFunc(provider.serve))
	defer upstream.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	srv := &server{store: repository, uploadPath: t.TempDir(), agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: upstream.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: upstream.Client()}
	srv.loopRunLimits.Store(testLoopLimits())
	api := httptest.NewServer(newHandler(srv))
	defer api.Close()
	login := func(email string) *http.Client {
		client := authClient(t)
		authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": email, "password": "test-password"}, "", http.StatusOK)
		return client
	}
	metadata, _ := repository.WorkspaceSettingsMetadata("test-workspace")
	if err := repository.UpdateMemberRole(context.Background(), metadata.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	admin, member := login("admin@example.test"), login("member@example.test")
	loop := authRequest[domain.Loop](t, admin, http.MethodPost, api.URL+"/api/loops", map[string]any{"name": "Owner only", "instructions": "Wait.", "status": "published", "editPolicy": "owner"}, "test-workspace", http.StatusCreated)
	run := authRequest[domain.LoopRun](t, admin, http.MethodPost, api.URL+"/api/loops/"+loop.ID+"/runs", nil, "test-workspace", http.StatusAccepted)
	authRequest[any](t, member, http.MethodPost, api.URL+"/api/loops/"+loop.ID+"/runs/"+run.ID+"/cancel", nil, "test-workspace", http.StatusForbidden)
	cancelled := authRequest[domain.LoopRun](t, admin, http.MethodPost, api.URL+"/api/loops/"+loop.ID+"/runs/"+run.ID+"/cancel", nil, "test-workspace", http.StatusOK)
	if cancelled.Status != "cancelled" || cancelled.CancelledBy != "usr_admin" {
		t.Fatalf("cancelled = %+v", cancelled)
	}
}

// A run left running by a process that died is reconciled: on startup when
// the previous process ran on this host, else once its lease expires.
func TestLoopRunReconcileInterruptsOrphanedRuns(t *testing.T) {
	srv, handler := newReliableLoopServer(t, &fakeLoopProvider{})
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Weekly", "instructions": "Summarize.", "status": "published"}, http.StatusCreated)
	ctx := context.Background()
	host := loopInstanceHost(srv.loopInstanceID())
	now := time.Now().UTC()
	create := func(id, owner string, expires time.Time, replying bool) {
		run := domain.LoopRun{ID: id, LoopID: loop.ID, Status: "running", Trigger: "manual", StartedAt: now, ToolCalls: []domain.LoopRunToolCall{{Order: 1, Name: "list_projects", Status: "running"}}}
		if replying {
			run.Status = "completed"
			run.Replies = []domain.LoopRunReply{{ID: "reply_1", Body: "again", Status: "running", CreatedAt: now}}
		}
		if err := srv.store.CreateLoopRun(ctx, "test-workspace", run, store.LoopRunLease{Owner: owner, ExpiresAt: expires}); err != nil {
			t.Fatal(err)
		}
	}
	create("run_restarted", host+":1:previous", now.Add(time.Hour), false)
	create("run_expired", "other-host:7:x", now.Add(-time.Second), false)
	create("run_live", "other-host:7:x", now.Add(time.Hour), false)
	create("run_legacy", "", time.Time{}, false)
	create("run_reply", "other-host:7:x", now.Add(-time.Second), true)

	if count := srv.reconcileLoopRuns(ctx, true); count != 4 {
		t.Fatalf("startup reconcile interrupted %d runs, want 4", count)
	}
	for _, id := range []string{"run_restarted", "run_expired", "run_legacy"} {
		run := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+id, nil, http.StatusOK)
		if run.Status != "interrupted" || run.FailureReason != "interrupted" || run.Error != loopInterruptedMessage || run.FinishedAt == nil || run.ToolCalls[0].Status != "error" {
			t.Fatalf("%s = %+v", id, run)
		}
	}
	reply := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/run_reply", nil, http.StatusOK)
	if reply.Status != "completed" || reply.Replies[0].Status != "interrupted" {
		t.Fatalf("reply run = %+v", reply)
	}
	if live := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/run_live", nil, http.StatusOK); live.Status != "running" {
		t.Fatalf("a run with a live lease elsewhere was interrupted: %+v", live)
	}
	// The periodic pass leaves leases of this host alone until they expire.
	create("run_same_host", host+":2:sibling", now.Add(time.Hour), false)
	if count := srv.reconcileLoopRuns(ctx, false); count != 0 {
		t.Fatalf("periodic reconcile interrupted %d runs", count)
	}
}

// The runner renews its lease; when another process reconciles the run
// anyway, the runner stops and keeps the reconciled status.
func TestLoopRunLeaseRenewalAndLoss(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"block"}}
	srv, handler := newReliableLoopServer(t, provider)
	loop, started := startTestRun(t, handler, "Slow", "Wait.", nil)
	time.Sleep(3 * srv.loopLimits().LeaseTTL)
	running, err := srv.store.RunningLoopRuns(context.Background())
	if err != nil || len(running) != 1 || !running[0].LeaseExpiresAt.After(time.Now()) || !running[0].HeartbeatAt.After(started.StartedAt) {
		t.Fatalf("lease not renewed: %+v, %v", running, err)
	}
	if count := srv.reconcileLoopRuns(context.Background(), true); count != 0 {
		t.Fatal("reconcile interrupted a run this process is executing")
	}
	// Simulate another process taking the run over after the lease expired.
	if !srv.interruptLoopRun(context.Background(), "test-workspace", started.ID, srv.loopInstanceID()) {
		t.Fatal("interrupt did not apply")
	}
	run := waitForRun(t, handler, loop.ID, started.ID)
	deadline := time.Now().Add(5 * time.Second)
	for srv.loopRunRegistry().get(started.ID) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.loopRunRegistry().get(started.ID) != nil {
		t.Fatal("runner kept running after losing its lease")
	}
	run = requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+started.ID, nil, http.StatusOK)
	if run.Status != "interrupted" || run.FailureReason != "interrupted" {
		t.Fatalf("run = %+v", run)
	}
}

// Heartbeats renew only the lease columns, so every read path merges the live
// heartbeat into the run it returns: the run page shows the run is alive.
func TestLoopRunAPIReturnsTheCurrentHeartbeat(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"block"}}
	srv, handler := newReliableLoopServer(t, provider)
	loop, started := startTestRun(t, handler, "Slow", "Wait.", nil)
	time.Sleep(3 * srv.loopLimits().LeaseTTL)
	single := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+started.ID, nil, http.StatusOK)
	if single.Status != "running" || single.HeartbeatAt == nil || !single.HeartbeatAt.After(single.StartedAt) {
		t.Fatalf("single run heartbeatAt = %v, startedAt = %v", single.HeartbeatAt, single.StartedAt)
	}
	listed := requestJSON[[]domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs", nil, http.StatusOK)
	if len(listed) != 1 || listed[0].HeartbeatAt == nil || !listed[0].HeartbeatAt.After(listed[0].StartedAt) {
		t.Fatalf("listed runs = %+v", listed)
	}
	// The heartbeat keeps advancing between reads.
	time.Sleep(2 * srv.loopLimits().LeaseTTL)
	later := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+started.ID, nil, http.StatusOK)
	if !later.HeartbeatAt.After(*single.HeartbeatAt) {
		t.Fatalf("heartbeatAt did not advance: %v then %v", single.HeartbeatAt, later.HeartbeatAt)
	}
	srv.interruptLoopRun(context.Background(), "test-workspace", started.ID, srv.loopInstanceID())
	waitForRun(t, handler, loop.ID, started.ID)
}

func TestLoopRunFailureReasonsAndRetries(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)

	// Transient provider failures are retried with backoff.
	provider.replies = []string{"status:503", "status:502", "All done."}
	loop, started := startTestRun(t, handler, "Digest", "Summarize open bugs.", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "completed" || run.Retries != 2 || run.Output != "All done." {
		t.Fatalf("retried run = %+v", run)
	}
	// They stop after the retry budget, with an explicit reason.
	provider.replies = []string{"status:503", "status:503", "status:503", "never reached"}
	started = requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	run = waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "failed" || run.FailureReason != "provider_error" || run.Retries != 2 || !strings.Contains(run.Error, "503") {
		t.Fatalf("exhausted retries = %+v", run)
	}
	// Client errors are not retried.
	provider.mu.Lock()
	provider.replies, provider.inputs = []string{"status:400", "never reached"}, nil
	provider.mu.Unlock()
	started = requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	run = waitForRun(t, handler, loop.ID, started.ID)
	provider.mu.Lock()
	requests := len(provider.inputs)
	provider.mu.Unlock()
	if run.Status != "failed" || run.FailureReason != "provider_error" || run.Retries != 0 || requests != 1 {
		t.Fatalf("client error = %+v after %d requests", run, requests)
	}
	// An empty answer is its own failure.
	provider.replies = []string{""}
	started = requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	if run = waitForRun(t, handler, loop.ID, started.ID); run.Status != "failed" || run.FailureReason != "empty_response" {
		t.Fatalf("empty answer = %+v", run)
	}
	// The wall-clock budget ends a run that takes too long.
	setLoopLimits(srv, func(l *loopRunLimits) { l.Timeout = 150 * time.Millisecond })
	provider.replies = []string{"block"}
	started = requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	if run = waitForRun(t, handler, loop.ID, started.ID); run.Status != "failed" || run.FailureReason != "timeout" || !strings.Contains(run.Error, "time limit") {
		t.Fatalf("timed out run = %+v", run)
	}
}

func TestClassifyLoopRunFailure(t *testing.T) {
	limits := *testLoopLimits()
	background := context.Background()
	cancelled, cancel := context.WithCancelCause(background)
	cancel(errLoopRunCancelled)
	lost, loseLease := context.WithCancelCause(background)
	loseLease(errLoopLeaseLost)
	expired, stop := context.WithTimeoutCause(background, 0, errLoopRunTimedOut)
	defer stop()
	<-expired.Done()
	cases := []struct {
		ctx            context.Context
		err            error
		status, reason string
	}{
		{cancelled, context.Canceled, "cancelled", "cancelled"},
		{lost, context.Canceled, "interrupted", "interrupted"},
		{expired, context.DeadlineExceeded, "failed", "timeout"},
		{background, loopBudgetError("too many"), "failed", "budget_exhausted"},
		{background, loopProviderFailure(&agentProviderError{message: "slow", transient: true, timeout: true}), "failed", "provider_timeout"},
		{background, loopProviderFailure(errors.New("bad gateway")), "failed", "provider_error"},
		{background, &loopRunError{reason: "empty_response", message: "empty"}, "failed", "empty_response"},
		{background, fmt.Errorf("%w: paused", errLoopUnavailable), "failed", "unavailable"},
	}
	for _, item := range cases {
		status, reason, _ := classifyLoopRunFailure(item.ctx, item.err, limits)
		if status != item.status || reason != item.reason {
			t.Errorf("%v → %s/%s, want %s/%s", item.err, status, reason, item.status, item.reason)
		}
	}
	for err, want := range map[error]bool{
		&agentProviderError{transient: true}:  true,
		&agentProviderError{status: 400}:      false,
		io.ErrUnexpectedEOF:                   true,
		errors.New("database is locked"):      true,
		errors.New("issue \"X-1\" not found"): false,
		context.Canceled:                      false,
	} {
		if got := loopTransientError(err); got != want {
			t.Errorf("loopTransientError(%v) = %v, want %v", err, got, want)
		}
	}
}

// Read-only tools are retried on transient failures; changes never are.
func TestLoopRunToolRetryPolicy(t *testing.T) {
	srv, _ := newReliableLoopServer(t, &fakeLoopProvider{})
	_, readOnly := srv.loopRunTools(context.Background())
	for _, name := range []string{"list_issues", "list_projects", "get_issue", loopFinishToolName, agentProgressTool} {
		if !readOnly[name] {
			t.Errorf("%s should be read-only", name)
		}
	}
	for _, name := range []string{"save_issue", "save_comment", "save_status_update", "delete_comment"} {
		if readOnly[name] {
			t.Errorf("%s must never be retried", name)
		}
	}
	calls := 0
	limits := *testLoopLimits()
	retries, err := loopRetry(context.Background(), limits, func() error {
		calls++
		return errors.New("database is locked")
	})
	if calls != 3 || retries != 2 || err == nil {
		t.Fatalf("calls=%d retries=%d err=%v", calls, retries, err)
	}
}

func TestLoopRunBudgets(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)

	// Tool call budget.
	setLoopLimits(srv, func(l *loopRunLimits) { l.MaxToolCalls = 1 })
	provider.replies = []string{`tool:list_projects {"limit":5}`, `tool:list_teams {}`, "Done."}
	loop, started := startTestRun(t, handler, "Busy", "Look around.", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "failed" || run.FailureReason != "budget_exhausted" || !strings.Contains(run.Error, "more than 1 tool calls") {
		t.Fatalf("tool budget = %+v", run)
	}
	setLoopLimits(srv, func(l *loopRunLimits) { l.MaxToolCalls = 20 })

	// Tool output is cut to its budget before it reaches the model.
	setLoopLimits(srv, func(l *loopRunLimits) { l.MaxToolOutputBytes = 40 })
	provider.mu.Lock()
	provider.replies, provider.inputs = []string{`tool:list_teams {}`, "Done."}, nil
	provider.mu.Unlock()
	started = requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	waitForRun(t, handler, loop.ID, started.ID)
	provider.mu.Lock()
	second := provider.inputs[1]
	provider.mu.Unlock()
	if !strings.Contains(second, "[truncated:") {
		t.Fatalf("tool output not truncated: %s", second[max(0, len(second)-700):])
	}
	setLoopLimits(srv, func(l *loopRunLimits) { l.MaxToolOutputBytes = 48 << 10 })

	// Context budget.
	setLoopLimits(srv, func(l *loopRunLimits) { l.MaxContextBytes = 64 })
	started = requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	if run = waitForRun(t, handler, loop.ID, started.ID); run.Status != "failed" || run.FailureReason != "budget_exhausted" || !strings.Contains(run.Error, "context budget") {
		t.Fatalf("context budget = %+v", run)
	}
	setLoopLimits(srv, func(l *loopRunLimits) { l.MaxContextBytes = 768 << 10 })

	// Concurrency budget: a second run while one works is recorded as failed.
	setLoopLimits(srv, func(l *loopRunLimits) { l.MaxConcurrentPerWorkspace = 1 })
	provider.replies = []string{"block"}
	slow, first := startTestRun(t, handler, "Slow", "Wait.", nil)
	_, second2 := startTestRun(t, handler, "Other", "Wait too.", nil)
	if second2.Status != "failed" || second2.FailureReason != "budget_exhausted" || !strings.Contains(second2.Error, "limit 1") {
		t.Fatalf("over-budget run = %+v", second2)
	}
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+slow.ID+"/runs/"+first.ID+"/cancel", nil, http.StatusOK)
}

func TestLoopSystemPromptListsToolsLimitsAndFinish(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"Done."}}
	_, handler := newReliableLoopServer(t, provider)
	loop, started := startTestRun(t, handler, "Weekly project update", "Post a status update on each active project.", nil)
	waitForRun(t, handler, loop.ID, started.ID)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	var payload []any
	_ = json.Unmarshal([]byte(provider.inputs[0]), &payload)
	prompt, _ := payload[0].(string)
	for _, want := range []string{"Available tools:", "list_issues", "finish_run", "Limits: at most 8 tool turns and 20 tool calls", "Self-check before finishing", "expected to produce output: post the status update(s)"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt lacks %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(strings.Join(provider.tools[0], ","), "finish_run") {
		t.Errorf("finish_run not offered: %v", provider.tools[0])
	}
}
