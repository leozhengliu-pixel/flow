package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Loop run reliability: per-run budgets, a lease + heartbeat on running runs,
// reconciliation of runs whose process died, cancellation, a structured final
// report with an acceptance check, and explicit failure reasons.

// loopRunLimits are the budgets every loop run works within. They come from
// FLOW_LOOP_* environment variables (see envLoopRunLimits).
type loopRunLimits struct {
	// MaxConcurrentPerWorkspace and MaxConcurrent cap runs (and replies) that
	// execute at the same time in this process.
	MaxConcurrentPerWorkspace int
	MaxConcurrent             int
	// MaxToolOutputBytes truncates each tool result sent to the model.
	MaxToolOutputBytes int
	// MaxContextBytes caps the conversation sent to the model in one turn.
	MaxContextBytes int
	// MaxToolCalls caps tool calls per run; MaxTurns caps model turns that may
	// call tools (the run then has to summarize).
	MaxToolCalls int
	MaxTurns     int
	// Timeout is the wall-clock limit of a run.
	Timeout time.Duration
	// LeaseTTL is how long a run's lease holds without a heartbeat; the
	// runner renews it every LeaseTTL/4.
	LeaseTTL time.Duration
	// RetryAttempts is how often a transient provider or read-only tool
	// failure is retried; RetryBackoff the first delay (doubled each time).
	RetryAttempts int
	RetryBackoff  time.Duration
}

func envInt(name string, fallback int) int {
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && value > 0 {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	if value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name))); err == nil && value > 0 {
		return value
	}
	return fallback
}

var envLoopRunLimits = sync.OnceValue(func() loopRunLimits {
	return loopRunLimits{
		MaxConcurrentPerWorkspace: envInt("FLOW_LOOP_MAX_CONCURRENT_RUNS_PER_WORKSPACE", 2),
		MaxConcurrent:             envInt("FLOW_LOOP_MAX_CONCURRENT_RUNS", 4),
		MaxToolOutputBytes:        envInt("FLOW_LOOP_MAX_TOOL_OUTPUT_BYTES", 48<<10),
		MaxContextBytes:           envInt("FLOW_LOOP_MAX_CONTEXT_BYTES", 768<<10),
		MaxToolCalls:              envInt("FLOW_LOOP_MAX_TOOL_CALLS", 80),
		MaxTurns:                  envInt("FLOW_LOOP_MAX_TURNS", loopMaxToolTurns),
		Timeout:                   envDuration("FLOW_LOOP_RUN_TIMEOUT", loopRunTimeout),
		LeaseTTL:                  envDuration("FLOW_LOOP_LEASE_TTL", 60*time.Second),
		RetryAttempts:             envInt("FLOW_LOOP_RETRY_ATTEMPTS", 2),
		RetryBackoff:              envDuration("FLOW_LOOP_RETRY_BACKOFF", time.Second),
	}
})

func (s *server) loopLimits() loopRunLimits {
	if limits := s.loopRunLimits.Load(); limits != nil {
		return *limits
	}
	return envLoopRunLimits()
}

var (
	errLoopRunCancelled = errors.New("loop run cancelled")
	errLoopLeaseLost    = errors.New("loop run lease lost")
	errLoopRunTimedOut  = errors.New("loop run timed out")
)

// loopRunError is a run failure with its reason code.
type loopRunError struct {
	reason  string
	message string
	err     error
}

func (e *loopRunError) Error() string { return e.message }
func (e *loopRunError) Unwrap() error { return e.err }

func loopBudgetError(format string, args ...any) error {
	return &loopRunError{reason: "budget_exhausted", message: fmt.Sprintf(format, args...)}
}

// loopProviderFailure wraps a model request failure with its reason.
func loopProviderFailure(err error) error {
	var runErr *loopRunError
	if errors.As(err, &runErr) {
		return err
	}
	reason := "provider_error"
	var providerErr *agentProviderError
	var netErr net.Error
	if errors.As(err, &providerErr) && providerErr.timeout || errors.As(err, &netErr) && netErr.Timeout() {
		reason = "provider_timeout"
	}
	return &loopRunError{reason: reason, message: err.Error(), err: err}
}

// loopTransientError reports whether retrying the failed call may succeed.
func loopTransientError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var providerErr *agentProviderError
	if errors.As(err, &providerErr) {
		return providerErr.transient
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"timeout", "timed out", "deadlock", "database is locked", "connection reset", "broken pipe", "bad connection", "too many connections", "temporarily unavailable"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// loopRetry runs call until it succeeds, fails permanently or the attempts
// run out, backing off between attempts. It returns the retries made.
func loopRetry(ctx context.Context, limits loopRunLimits, call func() error) (int, error) {
	delay := limits.RetryBackoff
	retries := 0
	for {
		err := call()
		if err == nil || retries >= limits.RetryAttempts || ctx.Err() != nil || !loopTransientError(err) {
			return retries, err
		}
		retries++
		select {
		case <-ctx.Done():
			return retries, err
		case <-time.After(delay):
		}
		delay *= 2
	}
}

// loopFailureReasons are the failure reason codes runs and replies store. The
// web app labels each one (REASON_LABELS in web/src/components/loops/loop-run-status.ts).
var loopFailureReasons = []string{"cancelled", "interrupted", "timeout", "provider_timeout", "provider_error", "empty_response", "tool_error", "budget_exhausted", "no_output", "incomplete", "unavailable", "error"}

// classifyLoopRunFailure maps how a run ended to its status, failure reason
// and message.
func classifyLoopRunFailure(ctx context.Context, err error, limits loopRunLimits) (string, string, string) {
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, errLoopRunCancelled):
		return "cancelled", "cancelled", "Cancelled"
	case errors.Is(cause, errLoopLeaseLost):
		return "interrupted", "interrupted", "Interrupted: the run lost its lease (the server stopped renewing it)"
	case errors.Is(cause, errLoopRunTimedOut), errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil:
		return "failed", "timeout", fmt.Sprintf("The run exceeded its %s time limit", limits.Timeout.Round(time.Second))
	}
	var runErr *loopRunError
	if errors.As(err, &runErr) {
		return "failed", runErr.reason, runErr.message
	}
	if errors.Is(err, errLoopUnavailable) {
		return "failed", "unavailable", strings.TrimPrefix(err.Error(), errLoopUnavailable.Error()+": ")
	}
	return "failed", "error", err.Error()
}

// loopRunRegistry tracks the runs (and replies) executing in this process:
// how to cancel them and how many run per workspace.
type loopRunRegistry struct {
	mu           sync.Mutex
	active       map[string]*activeLoopRun
	perWorkspace map[string]int
	total        int
}

type activeLoopRun struct {
	workspace   string
	cancel      context.CancelCauseFunc
	done        chan struct{}
	cancelledBy string
}

func (s *server) loopRunRegistry() *loopRunRegistry {
	s.loopRunsOnce.Do(func() {
		s.loopRuns = &loopRunRegistry{active: map[string]*activeLoopRun{}, perWorkspace: map[string]int{}}
	})
	return s.loopRuns
}

// reserve claims a concurrency slot, or explains which budget is full.
func (registry *loopRunRegistry) reserve(workspace string, limits loopRunLimits) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if limits.MaxConcurrent > 0 && registry.total >= limits.MaxConcurrent {
		return loopBudgetError("Too many loop runs are in progress on this server (limit %d); try again when one finishes", limits.MaxConcurrent)
	}
	if limits.MaxConcurrentPerWorkspace > 0 && registry.perWorkspace[workspace] >= limits.MaxConcurrentPerWorkspace {
		return loopBudgetError("Too many loop runs are in progress in this workspace (limit %d); try again when one finishes", limits.MaxConcurrentPerWorkspace)
	}
	registry.perWorkspace[workspace]++
	registry.total++
	return nil
}

// unreserve returns a slot that was reserved but never ran.
func (registry *loopRunRegistry) unreserve(workspace string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.perWorkspace[workspace]--
	registry.total--
}

// register records a run that holds a reserved slot.
func (registry *loopRunRegistry) register(runID, workspace string, cancel context.CancelCauseFunc) *activeLoopRun {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := &activeLoopRun{workspace: workspace, cancel: cancel, done: make(chan struct{})}
	registry.active[runID] = entry
	return entry
}

// finish removes a run and frees its slot.
func (registry *loopRunRegistry) finish(runID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, ok := registry.active[runID]
	if !ok {
		return
	}
	delete(registry.active, runID)
	registry.perWorkspace[entry.workspace]--
	registry.total--
	close(entry.done)
}

func (registry *loopRunRegistry) get(runID string) *activeLoopRun {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.active[runID]
}

// requestCancel cancels a run executing in this process.
func (registry *loopRunRegistry) requestCancel(runID, userID string) *activeLoopRun {
	registry.mu.Lock()
	entry := registry.active[runID]
	if entry != nil && entry.cancelledBy == "" {
		entry.cancelledBy = userID
	}
	registry.mu.Unlock()
	if entry != nil && entry.cancel != nil {
		entry.cancel(errLoopRunCancelled)
	}
	return entry
}

func (registry *loopRunRegistry) cancelledBy(runID string) string {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if entry := registry.active[runID]; entry != nil {
		return entry.cancelledBy
	}
	return ""
}

// loopInstanceID names this process in run leases: host, process and start time.
func (s *server) loopInstanceID() string {
	s.loopInstanceOnce.Do(func() {
		host, _ := os.Hostname()
		s.loopInstance = fmt.Sprintf("%s:%d:%s", firstNonEmpty(host, "flow"), os.Getpid(), strconv.FormatInt(time.Now().UnixNano(), 36))
	})
	return s.loopInstance
}

func loopInstanceHost(instance string) string {
	host, _, _ := strings.Cut(instance, ":")
	return host
}

// runLoopWork executes work for a run under its lease: it registers the run
// for cancellation, renews the lease until work returns, cancels the work when
// the lease is lost or another process asks to cancel, and calls finish with
// the work's context (whose cause says why it ended) before the run leaves
// the registry. The caller has reserved a concurrency slot.
func (s *server) runLoopWork(workspace, runID string, limits loopRunLimits, work func(context.Context) error, finish func(context.Context, error)) {
	registry := s.loopRunRegistry()
	ctx, cancel := context.WithCancelCause(context.Background())
	ctx, stop := context.WithTimeoutCause(ctx, limits.Timeout, errLoopRunTimedOut)
	registry.register(runID, workspace, cancel)
	defer registry.finish(runID)
	defer cancel(nil)
	defer stop()
	done := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(max(limits.LeaseTTL/4, 10*time.Millisecond))
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			now := time.Now().UTC()
			requested, err := s.store.HeartbeatLoopRun(context.Background(), workspace, runID, s.loopInstanceID(), now, now.Add(limits.LeaseTTL))
			switch {
			case errors.Is(err, store.ErrLoopRunLeaseLost):
				cancel(errLoopLeaseLost)
				return
			case err != nil:
				log.Printf("Loop run heartbeat workspace=%s run=%s: %v", workspace, runID, err)
			case requested:
				cancel(errLoopRunCancelled)
			}
		}
	}()
	err := work(ctx)
	close(done)
	<-heartbeatDone
	finish(ctx, err)
}

// startLoopRunReconciler marks runs whose process died as interrupted: once
// at startup and then periodically.
func (s *server) startLoopRunReconciler() {
	if !s.loopReconcilerStarted.CompareAndSwap(false, true) {
		return
	}
	go func() {
		ctx := s.store.WorkerContext()
		s.reconcileLoopRuns(ctx, true)
		interval := max(s.loopLimits().LeaseTTL/2, 50*time.Millisecond)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileLoopRuns(ctx, false)
			}
		}
	}()
}

// reconcileLoopRuns interrupts runs (and replies) whose lease expired, that
// never had one (runs migrated from the workspace metadata), or — at startup
// — that an earlier process on this host still held.
func (s *server) reconcileLoopRuns(ctx context.Context, startup bool) int {
	running, err := s.store.RunningLoopRuns(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Loop run reconcile: %v", err)
		}
		return 0
	}
	self := s.loopInstanceID()
	now := time.Now().UTC()
	registry := s.loopRunRegistry()
	count := 0
	for _, item := range running {
		if registry.get(item.ID) != nil {
			continue
		}
		previousProcess := startup && item.LeaseOwner != self && loopInstanceHost(item.LeaseOwner) == loopInstanceHost(self)
		if item.LeaseOwner != "" && now.Before(item.LeaseExpiresAt) && !previousProcess {
			continue
		}
		if s.interruptLoopRun(ctx, item.Workspace, item.ID, item.LeaseOwner) {
			count++
		}
	}
	return count
}

const loopInterruptedMessage = "Interrupted by server restart"

func (s *server) interruptLoopRun(ctx context.Context, workspace, runID, owner string) bool {
	now := time.Now().UTC()
	changed := false
	run, err := s.store.UpdateLoopRun(ctx, workspace, runID, []store.LoopRunEvent{loopRunEvent("interrupted", map[string]any{"leaseOwner": owner})}, func(run *domain.LoopRun) error {
		if run.Status == "running" {
			run.Status, run.FailureReason, run.Error, run.FinishedAt = "interrupted", "interrupted", loopInterruptedMessage, &now
			closeRunningToolCalls(run.ToolCalls)
			changed = true
		}
		for index := range run.Replies {
			reply := &run.Replies[index]
			if reply.Status == "running" {
				reply.Status, reply.FailureReason, reply.Error, reply.FinishedAt = "interrupted", "interrupted", loopInterruptedMessage, &now
				closeRunningToolCalls(reply.ToolCalls)
				changed = true
			}
		}
		if !changed {
			return store.ErrNoMutation
		}
		return nil
	})
	if err != nil {
		log.Printf("Loop run reconcile workspace=%s run=%s: %v", workspace, runID, err)
		return false
	}
	if err := s.store.ReleaseLoopRunLease(ctx, workspace, runID, owner); err != nil {
		log.Printf("Loop run lease release workspace=%s run=%s: %v", workspace, runID, err)
	}
	if changed {
		log.Printf("Loop run interrupted workspace=%s loop=%s run=%s lease=%s", workspace, run.LoopID, runID, owner)
		s.publishLoopRunEvent(workspace, "loop_run.finished", run)
	}
	return changed
}

func closeRunningToolCalls(calls []domain.LoopRunToolCall) {
	for index := range calls {
		if calls[index].Status == "running" {
			calls[index].Status = "error"
			if calls[index].Error == "" {
				calls[index].Error = "Stopped before the call finished"
			}
		}
	}
}

func loopRunEvent(kind string, data any) store.LoopRunEvent {
	raw, _ := json.Marshal(data)
	return store.LoopRunEvent{Kind: kind, Data: raw, CreatedAt: time.Now().UTC()}
}

// publishLoopRunEvent tells clients a run changed without a workspace
// mutation; the run page refetches the run.
func (s *server) publishLoopRunEvent(workspace, eventType string, run domain.LoopRun) {
	if s.realtime == nil {
		return
	}
	now := time.Now().UTC()
	payload, _ := json.Marshal(map[string]string{"loopId": run.LoopID, "runId": run.ID, "status": run.Status})
	s.publishRealtime(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("evt_loop_run_%d", now.UnixNano()), Type: eventType, AggregateID: run.LoopID, Payload: payload, CreatedAt: now})
}

// cancelLoopRun stops a running run (or the agent's answer to a reply),
// including the model request in flight.
func (s *server) cancelLoopRun(w http.ResponseWriter, r *http.Request) {
	id, runID := r.PathValue("id"), r.PathValue("runId")
	data, err := s.loopViewerData(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "workspace access denied")
		return
	}
	workspace := data.Workspace.URLKey
	loop := loopByID(&data, id)
	if loop == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	run, err := s.store.LoopRun(r.Context(), workspace, runID)
	if err != nil || run.LoopID != id {
		writeError(w, http.StatusNotFound, "loop run not found")
		return
	}
	replying := slices.ContainsFunc(run.Replies, func(reply domain.LoopRunReply) bool { return reply.Status == "running" })
	if run.Status != "running" && !replying {
		writeError(w, http.StatusConflict, "This run is not running")
		return
	}
	viewerID := data.Viewer.ID
	if !s.authDisabled && !canEditLoop(&data, *loop, viewerID) && (viewerID == "" || run.ActorID != viewerID) {
		writeError(w, http.StatusForbidden, "Only people who can edit this loop, or who started the run, can cancel it")
		return
	}
	if entry := s.loopRunRegistry().requestCancel(runID, viewerID); entry != nil {
		select {
		case <-entry.done:
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	} else if requested, err := s.store.RequestLoopRunCancel(r.Context(), workspace, runID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not cancel the run")
		return
	} else if !requested {
		// No process holds the run: finish it here.
		s.finishOrphanedLoopRun(r.Context(), workspace, runID, viewerID)
	}
	updated, err := s.store.LoopRun(r.Context(), workspace, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "loop run not found")
		return
	}
	status := http.StatusOK
	if updated.Status == "running" || slices.ContainsFunc(updated.Replies, func(reply domain.LoopRunReply) bool { return reply.Status == "running" }) {
		// Another process runs it and stops at its next heartbeat.
		status = http.StatusAccepted
	}
	writeJSON(w, status, presentLoopRun(updated, viewerID))
}

func (s *server) finishOrphanedLoopRun(ctx context.Context, workspace, runID, userID string) {
	now := time.Now().UTC()
	run, err := s.store.UpdateLoopRun(ctx, workspace, runID, []store.LoopRunEvent{loopRunEvent("cancelled", map[string]any{"userId": userID})}, func(run *domain.LoopRun) error {
		changed := false
		if run.Status == "running" {
			run.Status, run.FailureReason, run.Error, run.CancelledBy, run.FinishedAt = "cancelled", "cancelled", "Cancelled", userID, &now
			closeRunningToolCalls(run.ToolCalls)
			changed = true
		}
		for index := range run.Replies {
			if reply := &run.Replies[index]; reply.Status == "running" {
				reply.Status, reply.FailureReason, reply.Error, reply.FinishedAt = "cancelled", "cancelled", "Cancelled", &now
				closeRunningToolCalls(reply.ToolCalls)
				changed = true
			}
		}
		if !changed {
			return store.ErrNoMutation
		}
		return nil
	})
	if err == nil {
		_ = s.store.ReleaseLoopRunLease(ctx, workspace, runID, "")
		s.publishLoopRunEvent(workspace, "loop_run.finished", run)
	}
}

// listLoopRunEvents returns a run's append-only event log after a sequence
// number (?after=), oldest first.
func (s *server) listLoopRunEvents(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "loops")
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id, runID := r.PathValue("id"), r.PathValue("runId")
	run, err := s.store.LoopRun(r.Context(), data.Workspace.URLKey, runID)
	if err != nil || run.LoopID != id {
		writeError(w, http.StatusNotFound, "loop run not found")
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	events, err := s.store.LoopRunEvents(r.Context(), data.Workspace.URLKey, runID, after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load run events")
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// loopViewerData is the small projection loop run handlers check permissions
// on: loops, team roles and the viewer's workspace role.
func (s *server) loopViewerData(r *http.Request) (domain.Bootstrap, error) {
	workspace := workspaceKey(r)
	data, ok := s.store.WorkspaceMetadataFields(workspace, "loops", "viewer", "workspaceSettings", "teamSettings", "teamMembers", "agentSkills")
	if !ok {
		return data, errNotFound
	}
	if s.authDisabled {
		data.ViewerRole = "admin"
		return data, nil
	}
	data.Viewer = authUser(r)
	role, status, err := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, data.Viewer.ID)
	if err != nil || status != "active" {
		return data, store.ErrAuthForbidden
	}
	data.ViewerRole = role
	if members, err := s.store.ListTeamMembers(r.Context(), data.Workspace.ID); err == nil {
		data.TeamMembers = members
	}
	return data, nil
}

// workspaceURLKey is the request's workspace key, resolved to the default
// workspace when the request names none (local development).
func (s *server) workspaceURLKey(r *http.Request) string {
	if key := workspaceKey(r); key != "" {
		return key
	}
	data, _ := s.store.WorkspaceSettingsMetadata("")
	return data.Workspace.URLKey
}

// loopRunCounts counts each loop's runs in the last 30 days.
func (s *server) loopRunCounts(ctx context.Context, workspace string) map[string]int {
	counts, err := s.store.LoopRunCounts(ctx, workspace, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		log.Printf("Loop run counts workspace=%s: %v", workspace, err)
		return nil
	}
	return counts
}

// applyLoopRunCounts fills the 30-day run count of loops in a response.
func (s *server) applyLoopRunCounts(ctx context.Context, workspace string, loops []domain.Loop) {
	if len(loops) == 0 {
		return
	}
	counts := s.loopRunCounts(ctx, workspace)
	for index := range loops {
		loops[index].RunCount30d = counts[loops[index].ID]
	}
}

// Final report and acceptance.

const loopFinishToolName = "finish_run"

var loopFinishToolDefinition = agentProviderTool{
	Name:        loopFinishToolName,
	Description: "End the loop run with your final report. Call it exactly once, after the self-check, as your last action; call no other tool after it. status: done (everything the instructions ask for was done), incomplete (something could not be done) or nothing_to_do (the instructions did not apply this time). summary: under about 150 words, in the workspace's language, linking the issues, projects and updates you changed with the urls the tools returned. done: each change you made. notDone: each thing you did not do and why.",
	Parameters:  json.RawMessage(`{"type":"object","required":["status","summary"],"properties":{"status":{"type":"string","enum":["done","incomplete","nothing_to_do"]},"summary":{"type":"string","description":"Short final summary with links"},"done":{"type":"array","items":{"type":"string"},"description":"Changes made, one per item, with identifiers or links"},"notDone":{"type":"array","items":{"type":"string"},"description":"What was not done and why"}},"additionalProperties":false}`),
	Access:      "read",
}

func parseLoopSummary(arguments json.RawMessage) (*domain.LoopRunSummary, error) {
	var summary domain.LoopRunSummary
	if err := json.Unmarshal(arguments, &summary); err != nil {
		return nil, fmt.Errorf("finish_run needs a JSON object with status and summary")
	}
	summary.Status = strings.TrimSpace(summary.Status)
	summary.Summary = strings.TrimSpace(summary.Summary)
	if !slices.Contains([]string{"done", "incomplete", "nothing_to_do"}, summary.Status) {
		return nil, fmt.Errorf("finish_run status must be done, incomplete or nothing_to_do")
	}
	if summary.Summary == "" {
		return nil, fmt.Errorf("finish_run needs a summary")
	}
	clean := func(items []string) []string {
		items = slices.DeleteFunc(slices.Clone(items), func(item string) bool { return strings.TrimSpace(item) == "" })
		for index := range items {
			items[index] = strings.TrimSpace(items[index])
		}
		if len(items) > 50 {
			items = items[:50]
		}
		return items
	}
	summary.Done, summary.NotDone = clean(summary.Done), clean(summary.NotDone)
	return &summary, nil
}

// renderLoopSummary is the run's answer when the model reported through
// finish_run without writing one.
func renderLoopSummary(summary *domain.LoopRunSummary) string {
	var text strings.Builder
	text.WriteString(summary.Summary)
	if len(summary.Done) > 0 {
		text.WriteString("\n\n**Done**\n")
		for _, item := range summary.Done {
			text.WriteString("- " + item + "\n")
		}
	}
	if len(summary.NotDone) > 0 {
		text.WriteString("\n\n**Not done**\n")
		for _, item := range summary.NotDone {
			text.WriteString("- " + item + "\n")
		}
	}
	return strings.TrimSpace(text.String())
}

var loopOutputKinds = []string{"statusUpdate", "issue", "comment", "document", "change"}

var loopOutputLabels = map[string]string{
	"statusUpdate": "a project or initiative status update",
	"issue":        "a new issue",
	"comment":      "a comment",
	"document":     "a document",
	"change":       "a change in Flow",
}

// effectiveLoopOutputs are the outputs a run of the loop must produce: the
// loop's explicit expectedOutputs, else what its name and instructions imply.
func effectiveLoopOutputs(loop domain.Loop) []string {
	explicit := []string{}
	for _, item := range loop.ExpectedOutputs {
		if item == "none" {
			return nil
		}
		if slices.Contains(loopOutputKinds, item) && !slices.Contains(explicit, item) {
			explicit = append(explicit, item)
		}
	}
	if len(explicit) > 0 {
		return explicit
	}
	return inferLoopOutputs(loop.Name + "\n" + loop.Instructions)
}

// inferLoopOutputs reads side effects off the loop's text. It only infers
// what the text clearly asks for; anything else needs no particular output.
func inferLoopOutputs(text string) []string {
	text = strings.ToLower(text)
	has := func(markers ...string) bool {
		return slices.ContainsFunc(markers, func(marker string) bool { return strings.Contains(text, marker) })
	}
	outputs := []string{}
	if has("status update", "project update", "initiative update", "post an update", "post a weekly update", "post the weekly update", "weekly update", "post updates", "post an update", "周更新", "项目更新", "状态更新", "发布更新", "进展更新", "周报") {
		outputs = append(outputs, "statusUpdate")
	}
	if has("create an issue", "create issues", "create a new issue", "create new issues", "file an issue", "file issues", "open an issue", "创建 issue", "创建issue", "新建 issue", "新建issue", "创建问题", "创建任务") {
		outputs = append(outputs, "issue")
	}
	if has("comment on", "leave a comment", "add a comment", "post a comment", "reply with a comment", "评论", "留言") {
		outputs = append(outputs, "comment")
	}
	if has("create a document", "write a document", "创建文档", "撰写文档") {
		outputs = append(outputs, "document")
	}
	return outputs
}

// loopOutputKind is the kind of output a successful write tool call made.
func loopOutputKind(name string, args map[string]any) string {
	switch strings.TrimPrefix(name, "mcp__flow.") {
	case "save_status_update":
		return "statusUpdate"
	case "save_issue":
		if loopStringArg(args, "id") == "" {
			return "issue"
		}
	case "save_comment":
		return "comment"
	case "save_document":
		return "document"
	}
	return ""
}

// acceptLoopRun decides how a run that ended without an error finishes: a
// run whose loop implies side effects completes only when it produced them.
func acceptLoopRun(loop domain.Loop, run *domain.LoopRun) (string, string, string) {
	run.ExpectedOutputs = effectiveLoopOutputs(loop)
	if run.Summary != nil && run.Summary.Status == "incomplete" {
		message := "The agent reported unfinished work"
		if len(run.Summary.NotDone) > 0 {
			message += ": " + strings.Join(run.Summary.NotDone, "; ")
		}
		return "needs_review", "incomplete", message
	}
	missing := []string{}
	for _, kind := range run.ExpectedOutputs {
		if run.Produced[kind] == 0 {
			missing = append(missing, loopOutputLabels[kind])
		}
	}
	if len(missing) == 0 {
		return "completed", "", ""
	}
	for index := len(run.ToolCalls) - 1; index >= 0; index-- {
		call := run.ToolCalls[index]
		if (call.Status == "error" || call.Status == "blocked") && loopWriteToolName(call.Name) {
			return "needs_review", "tool_error", fmt.Sprintf("No output produced: expected %s, but %s failed: %s", strings.Join(missing, " and "), firstNonEmpty(call.Label, call.Name), call.Error)
		}
	}
	message := fmt.Sprintf("No output produced: the loop's instructions call for %s, but the run made none", strings.Join(missing, " and "))
	if run.Summary != nil && run.Summary.Status == "nothing_to_do" {
		message += " (the agent reported there was nothing to do)"
	}
	return "needs_review", "no_output", message
}

// loopWriteToolName reports whether a recorded tool call could change data.
func loopWriteToolName(name string) bool {
	name = strings.TrimPrefix(name, "mcp__flow.")
	for _, prefix := range []string{"save_", "create_", "delete_", "update_", "triage_", "merge_", "submit_", "resolve_", "restore_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// truncateLoopToolOutput keeps a tool result within the run's budget.
func truncateLoopToolOutput(content string, limit int) string {
	if limit <= 0 || len(content) <= limit {
		return content
	}
	cut := strings.ToValidUTF8(content[:limit], "")
	return cut + fmt.Sprintf("\n[truncated: %d more bytes omitted. Narrow the query with filters, a smaller limit or the cursor.]", len(content)-len(cut))
}

func loopContextBytes(messages []agentProviderMessage) int {
	total := 0
	for _, message := range messages {
		total += len(message.Content)
		for _, call := range message.ToolCalls {
			total += len(call.Arguments) + len(call.Name)
		}
		if message.ToolResult != nil {
			total += len(message.ToolResult.Content)
		}
		for _, image := range message.Images {
			total += len(image.Data)
		}
	}
	return total
}
