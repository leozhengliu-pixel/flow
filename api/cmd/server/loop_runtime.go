package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const (
	// Runs kept per loop for the history view and the 30-day run count.
	loopRunHistory = 50
	// A loop does not re-trigger on the same entity within this window, so
	// changes a run makes to its own trigger entity cannot start a cycle.
	loopRetriggerCooldown = 2 * time.Minute
	loopRunTimeout        = 10 * time.Minute
	// Loop runs work through whole queues, so they get more tool turns than chat.
	loopMaxToolTurns = 24
	// Live progress is written at most this often (steps and tool calls force a write).
	loopRunSaveInterval = 750 * time.Millisecond
)

type agentToolFilterKey struct{}

// agentBaseURLKey overrides the base URL tool results use for links.
type agentBaseURLKey struct{}

// loopTrigger describes why a loop run started.
type loopTrigger struct {
	Kind       string // manual | schedule | event
	EventType  string
	EntityType string // issue | project | initiative | release | team | cycle
	EntityID   string
	SourceKey  string
	Label      string
}

var errLoopUnavailable = errors.New("loop cannot run")

type loopRunGuard struct {
	mu       sync.Mutex
	inFlight map[string]bool
	recent   map[string]time.Time
}

var loopGuards = &loopRunGuard{inFlight: map[string]bool{}, recent: map[string]time.Time{}}

// claim reserves a loop/entity pair for a run, honoring the retrigger cooldown.
func (g *loopRunGuard) claim(key string, event bool, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight[key] || event && now.Sub(g.recent[key]) < loopRetriggerCooldown {
		return false
	}
	g.inFlight[key] = true
	return true
}

func (g *loopRunGuard) release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.inFlight, key)
	g.recent[key] = time.Now()
}

func loopByID(data *domain.Bootstrap, id string) *domain.Loop {
	for index := range data.Loops {
		if data.Loops[index].ID == id {
			return &data.Loops[index]
		}
	}
	return nil
}

// loopRunnable reports why a loop cannot run right now, if it cannot.
func (s *server) loopRunnable(data domain.Bootstrap, loop domain.Loop) error {
	if enabled, ok := data.WorkspaceSettings.FeatureFlags["loops"]; ok && !enabled {
		return fmt.Errorf("%w: Loops are disabled for this workspace", errLoopUnavailable)
	}
	if loop.Status == "draft" {
		return fmt.Errorf("%w: the loop is a draft; create it before running it", errLoopUnavailable)
	}
	if !loop.Enabled {
		return fmt.Errorf("%w: the loop is paused", errLoopUnavailable)
	}
	if !s.agent.Enabled {
		return fmt.Errorf("%w: Flow Agent is not configured on this server", errLoopUnavailable)
	}
	return nil
}

func (s *server) listLoopRuns(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "loops", "viewer")
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id := r.PathValue("id")
	if loopByID(&data, id) == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	runs, err := s.store.ListLoopRuns(r.Context(), data.Workspace.URLKey, id, loopRunHistory)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load loop runs")
		return
	}
	viewerID := firstNonEmpty(authUser(r).ID, data.Viewer.ID)
	for index := range runs {
		runs[index] = presentLoopRun(runs[index], viewerID)
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *server) getLoopRun(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "viewer")
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
	writeJSON(w, http.StatusOK, presentLoopRun(run, firstNonEmpty(authUser(r).ID, data.Viewer.ID)))
}

// presentLoopRun fills the trigger label for runs stored before labels existed
// and the viewer's feedback fields.
func presentLoopRun(run domain.LoopRun, viewerID string) domain.LoopRun {
	if run.TriggerLabel == "" {
		run.TriggerLabel = loopTriggerLabel(loopTrigger{Kind: run.Trigger, EntityType: run.EntityType}, run.EntityIdentifier)
	}
	counts := domain.LoopRunFeedbackCounts{}
	run.ViewerRating, run.ViewerComment = nil, ""
	for _, item := range run.Feedback {
		switch item.Rating {
		case "up":
			counts.Up++
		case "down":
			counts.Down++
		}
		if viewerID != "" && item.UserID == viewerID {
			rating := item.Rating
			run.ViewerRating, run.ViewerComment = &rating, item.Comment
		}
	}
	run.FeedbackCounts = &counts
	return run
}

func loopTriggerLabel(trigger loopTrigger, entityName string) string {
	switch trigger.Kind {
	case "schedule":
		return "Scheduled run"
	case "event":
		if trigger.Label != "" {
			return trigger.Label
		}
		if entityName != "" {
			return "Triggered by " + entityName
		}
		return "Triggered run"
	default:
		return "Manual run"
	}
}

type loopRunInput struct {
	EntityType string `json:"entityType,omitempty"`
	EntityID   string `json:"entityId,omitempty"`
}

// runLoopNow starts a manual run. Event loops run on an entity the caller
// picks ("Run loop on…"); scheduled loops run without one.
func (s *server) runLoopNow(w http.ResponseWriter, r *http.Request) {
	var input loopRunInput
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	data, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), loopEntityFields...)
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	loop := loopByID(&data, r.PathValue("id"))
	if loop == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	trigger := loopTrigger{Kind: "manual"}
	if loop.TriggerType != "schedule" {
		if strings.TrimSpace(input.EntityID) == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Choose the %s to run this loop on", loop.TriggerType))
			return
		}
		entityType := firstNonEmpty(input.EntityType, loop.TriggerType)
		if entityType != loop.TriggerType {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("This loop runs on a %s", loop.TriggerType))
			return
		}
		entityID, issue, err := s.resolveLoopEntity(r.Context(), data.Workspace.URLKey, data, entityType, strings.TrimSpace(input.EntityID))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !loopScopeMatches(data, *loop, loopEntityTeams(data, entityType, entityID, issue)) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("That %s is outside this loop's teams", entityType))
			return
		}
		trigger.EntityType, trigger.EntityID = entityType, entityID
	}
	run, err := s.startLoopRun(data.Workspace.URLKey, loop.ID, trigger, authUser(r).ID)
	if errors.Is(err, errLoopUnavailable) {
		writeError(w, http.StatusConflict, strings.TrimPrefix(err.Error(), errLoopUnavailable.Error()+": "))
		return
	}
	if errors.Is(err, errConflict) {
		writeError(w, http.StatusConflict, "This loop is already running")
		return
	}
	respondMutation(w, err, http.StatusAccepted, presentLoopRun(run, ""))
}

// resolveLoopEntity finds a run target by ID (or identifier/name).
func (s *server) resolveLoopEntity(ctx context.Context, workspace string, data domain.Bootstrap, entityType, id string) (string, *domain.Issue, error) {
	switch entityType {
	case "issue":
		issue, err := s.store.IssueRecord(ctx, workspace, id)
		if err != nil {
			issue, err = s.issueByIdentifier(ctx, workspace, id)
		}
		if err != nil {
			return "", nil, fmt.Errorf("issue %q not found", id)
		}
		return issue.ID, &issue, nil
	case "project":
		for _, item := range data.Projects {
			if item.ID == id || item.SlugID == id || strings.EqualFold(item.Name, id) {
				return item.ID, nil, nil
			}
		}
	case "initiative":
		for _, item := range data.Initiatives {
			if item.ID == id || strings.EqualFold(item.Name, id) {
				return item.ID, nil, nil
			}
		}
	case "release":
		for _, item := range data.Releases {
			if item.ID == id || item.SlugID == id || strings.EqualFold(item.Name, id) {
				return item.ID, nil, nil
			}
		}
	case "team":
		for _, item := range data.Teams {
			if item.ID == id || strings.EqualFold(item.Key, id) || strings.EqualFold(item.Name, id) {
				return item.ID, nil, nil
			}
		}
	case "cycle":
		for _, item := range data.Cycles {
			if item.ID == id {
				return item.ID, nil, nil
			}
		}
	}
	return "", nil, fmt.Errorf("%s %q not found", entityType, id)
}

// loopEntityFields is the metadata a run start reads: the loop, the
// workspace settings and the entities a run can be started on.
var loopEntityFields = []string{"loops", "loopVersions", "workspaceSettings", "projects", "initiatives", "releases", "teams", "teamSettings", "cycles"}

// startLoopRun records a running run and executes it in the background.
// Runs over the concurrency budget are recorded as failed.
func (s *server) startLoopRun(workspace, loopID string, trigger loopTrigger, actorID string) (domain.LoopRun, error) {
	data, ok := s.store.WorkspaceMetadataFields(workspace, loopEntityFields...)
	if !ok {
		return domain.LoopRun{}, errNotFound
	}
	workspace = data.Workspace.URLKey
	loop := loopByID(&data, loopID)
	if loop == nil {
		return domain.LoopRun{}, errNotFound
	}
	if err := s.loopRunnable(data, *loop); err != nil {
		return domain.LoopRun{}, err
	}
	guardKey := loopID + "|" + trigger.EntityID
	if !loopGuards.claim(guardKey, trigger.Kind == "event", time.Now()) {
		return domain.LoopRun{}, errConflict
	}
	now := time.Now().UTC()
	run := domain.LoopRun{ID: fmt.Sprintf("loop_run_%d", now.UnixNano()), LoopID: loopID, Status: "running", Trigger: trigger.Kind, EventType: trigger.EventType, EntityType: trigger.EntityType, EntityID: trigger.EntityID, ActorID: actorID, StartedAt: now}
	if trigger.EntityType == "issue" {
		if issue, err := s.store.IssueRecord(context.Background(), workspace, trigger.EntityID); err == nil {
			run.EntityIdentifier = issue.Identifier
		}
	} else if trigger.EntityID != "" {
		run.EntityIdentifier = loopEventEntityName(data, loopEvent{EntityType: trigger.EntityType, EntityID: trigger.EntityID})
	}
	run.TriggerLabel = loopTriggerLabel(trigger, run.EntityIdentifier)
	if loop.WebSearch && !s.webSearchAvailable() {
		run.Notices = append(run.Notices, loopWebSearchUnavailable)
	}
	run.ExpectedOutputs = effectiveLoopOutputs(*loop)
	limits := s.loopLimits()
	registry := s.loopRunRegistry()
	budgetErr := registry.reserve(workspace, limits)
	// Stamping the loop touches only the loop catalog and its versions.
	stampCtx := store.WithMetadataFields(context.Background(), "loops", "loopVersions")
	err := s.store.MutateWorkspace(stampCtx, workspace, "loop.run_started", loopID, map[string]any{"runId": run.ID, "trigger": trigger.Kind}, func(data *domain.Bootstrap) error {
		item := loopByID(data, loopID)
		if item == nil {
			return errNotFound
		}
		version := ensureLoopVersion(data, item)
		run.VersionID, run.Version = version.ID, version.Version
		item.LastRunAt = &now
		return nil
	})
	if err != nil {
		if budgetErr == nil {
			registry.unreserve(workspace)
		}
		loopGuards.release(guardKey)
		return domain.LoopRun{}, err
	}
	if budgetErr != nil {
		defer loopGuards.release(guardKey)
		run.Status, run.FailureReason, run.Error, run.FinishedAt = "failed", "budget_exhausted", budgetErr.Error(), &now
		if err := s.store.CreateLoopRun(context.Background(), workspace, run, store.LoopRunLease{}); err != nil {
			return domain.LoopRun{}, err
		}
		s.publishLoopRunEvent(workspace, "loop_run.finished", run)
		return run, nil
	}
	run.HeartbeatAt = &now
	if err := s.store.CreateLoopRun(context.Background(), workspace, run, store.LoopRunLease{Owner: s.loopInstanceID(), ExpiresAt: now.Add(limits.LeaseTTL)}); err != nil {
		registry.unreserve(workspace)
		loopGuards.release(guardKey)
		return domain.LoopRun{}, err
	}
	current := *loop
	go func() {
		defer loopGuards.release(guardKey)
		recorder := &loopRunRecorder{s: s, workspace: workspace, run: run}
		s.runLoopWork(workspace, run.ID, limits, func(ctx context.Context) error {
			return s.executeLoopRun(ctx, workspace, current, trigger, recorder)
		}, func(ctx context.Context, runErr error) {
			s.finishLoopRun(ctx, workspace, current, recorder, runErr)
		})
	}()
	return run, nil
}

// loopRunRecorder keeps a running run's record and persists it as it changes,
// so the run page shows steps, tool calls and output while the run works.
// Each write updates the run's own row (loop_run_records) and appends to its
// event log; nothing touches the workspace metadata.
type loopRunRecorder struct {
	s         *server
	workspace string
	run       domain.LoopRun
	// reply, when set, receives the steps, tool calls and output instead of
	// the run: the agent is answering a reply on the run page.
	reply *domain.LoopRunReply
	// skills are the replier's skills, added to the system prompt for the answer.
	skills      []domain.PersonalAgentSkill
	order       int
	lastSave    time.Time
	lastPublish time.Time
	dirty       bool
	events      []store.LoopRunEvent
}

// parts is where the agent's steps, tool calls and output are recorded.
func (rec *loopRunRecorder) parts() (*[]domain.LoopRunStep, *[]domain.LoopRunToolCall, *string) {
	if rec.reply != nil {
		return &rec.reply.Steps, &rec.reply.ToolCalls, &rec.reply.Output
	}
	return &rec.run.Steps, &rec.run.ToolCalls, &rec.run.Output
}

func (rec *loopRunRecorder) output() string {
	_, _, output := rec.parts()
	return *output
}

func (rec *loopRunRecorder) toolCount() int {
	_, calls, _ := rec.parts()
	return len(*calls)
}

func (rec *loopRunRecorder) nextOrder() int {
	rec.order++
	return rec.order
}

func (rec *loopRunRecorder) logEvent(kind string, data any) {
	rec.events = append(rec.events, loopRunEvent(kind, data))
}

// save writes the run when forced or when the throttle interval has passed.
func (rec *loopRunRecorder) save(force bool) {
	rec.dirty = true
	if rec.s == nil || rec.s.store == nil || !force && time.Since(rec.lastSave) < loopRunSaveInterval {
		return
	}
	rec.lastSave, rec.dirty = time.Now(), false
	events := rec.events
	rec.events = nil
	if rec.reply != nil {
		rec.saveReply(events)
		return
	}
	snapshot := rec.run
	snapshot.Steps = slices.Clone(rec.run.Steps)
	snapshot.ToolCalls = slices.Clone(rec.run.ToolCalls)
	snapshot.Produced = maps.Clone(rec.run.Produced)
	updated, err := rec.s.store.UpdateLoopRun(context.Background(), rec.workspace, snapshot.ID, events, func(run *domain.LoopRun) error {
		if run.Status != "running" {
			return store.ErrNoMutation
		}
		run.Steps, run.ToolCalls, run.Output = snapshot.Steps, snapshot.ToolCalls, snapshot.Output
		run.Produced, run.Retries, run.Summary, run.Notices = snapshot.Produced, snapshot.Retries, snapshot.Summary, snapshot.Notices
		return nil
	})
	if err != nil {
		if !errors.Is(err, store.ErrLoopRunNotFound) {
			log.Printf("Loop run progress workspace=%s run=%s: %v", rec.workspace, snapshot.ID, err)
		}
		return
	}
	rec.publishProgress(updated)
}

// publishProgress tells clients the run changed, at most once a second.
func (rec *loopRunRecorder) publishProgress(run domain.LoopRun) {
	if time.Since(rec.lastPublish) < time.Second {
		return
	}
	rec.lastPublish = time.Now()
	rec.s.publishLoopRunEvent(rec.workspace, "loop_run.progress", run)
}

func (rec *loopRunRecorder) addStep(title, message string) {
	title = strings.TrimRight(strings.TrimSpace(title), ".…")
	if title == "" {
		return
	}
	steps, _, _ := rec.parts()
	step := domain.LoopRunStep{Order: rec.nextOrder(), Title: title, Message: strings.TrimSpace(message), At: time.Now().UTC()}
	*steps = append(*steps, step)
	rec.logEvent("step", step)
	rec.save(true)
}

func (rec *loopRunRecorder) startTool(call domain.AgentToolCall) int {
	now := time.Now().UTC()
	args := loopToolArgs(call)
	_, calls, _ := rec.parts()
	record := domain.LoopRunToolCall{Order: rec.nextOrder(), ID: call.ID, Name: call.Name, Label: loopToolLabel(call.Name, args), Args: loopToolArgsSummary(args), Status: "running", StartedAt: &now}
	*calls = append(*calls, record)
	rec.logEvent("tool_started", record)
	rec.save(true)
	return len(*calls) - 1
}

// finishTool records a tool call's outcome; a successful change counts
// toward the run's produced outputs.
func (rec *loopRunRecorder) finishTool(index int, status string, callErr error, write bool, args map[string]any) {
	now := time.Now().UTC()
	_, calls, _ := rec.parts()
	record := &(*calls)[index]
	record.Status, record.FinishedAt = status, &now
	if callErr != nil {
		record.Error = callErr.Error()
	}
	if status == "completed" && write && rec.reply == nil {
		if rec.run.Produced == nil {
			rec.run.Produced = map[string]int{}
		}
		rec.run.Produced["change"]++
		if kind := loopOutputKind(record.Name, args); kind != "" {
			rec.run.Produced[kind]++
		}
	}
	rec.logEvent("tool_finished", *record)
	rec.save(true)
}

func (rec *loopRunRecorder) addRetries(count int) {
	if count > 0 && rec.reply == nil {
		rec.run.Retries += count
		rec.logEvent("retried", map[string]int{"count": count})
	}
}

func (rec *loopRunRecorder) setSummary(summary *domain.LoopRunSummary) {
	if rec.reply == nil {
		rec.run.Summary = summary
	}
	rec.logEvent("summary", summary)
}

func (rec *loopRunRecorder) summary() *domain.LoopRunSummary {
	if rec.reply != nil {
		return nil
	}
	return rec.run.Summary
}

// setOutput records streamed text and persists it on the throttle.
func (rec *loopRunRecorder) setOutput(text string) {
	rec.setOutputNow(text)
	rec.save(false)
}

// loopRunOutputLimit caps the answer kept on a run.
const loopRunOutputLimit = 64 << 10

// setOutputNow records the output without writing it.
func (rec *loopRunRecorder) setOutputNow(text string) {
	if len(text) > loopRunOutputLimit {
		text = strings.ToValidUTF8(text[:loopRunOutputLimit], "") + "\n\n[output truncated]"
	}
	_, _, output := rec.parts()
	*output = text
}

// finishLoopRun records how a run ended: its failure (cancelled, interrupted,
// timed out, provider or budget failures) or, when the agent finished, the
// acceptance check of what the run produced.
func (s *server) finishLoopRun(ctx context.Context, workspace string, loop domain.Loop, rec *loopRunRecorder, runErr error) {
	limits := s.loopLimits()
	now := time.Now().UTC()
	run := rec.run
	run.FinishedAt = &now
	run.Output = strings.TrimSpace(run.Output)
	run.Steps, run.ToolCalls = slices.Clone(run.Steps), slices.Clone(run.ToolCalls)
	closeRunningToolCalls(run.ToolCalls)
	if runErr != nil || ctx.Err() != nil {
		if runErr == nil {
			runErr = ctx.Err()
		}
		run.Status, run.FailureReason, run.Error = classifyLoopRunFailure(ctx, runErr, limits)
		if run.Status == "cancelled" {
			run.CancelledBy = s.loopRunRegistry().cancelledBy(run.ID)
		}
	} else {
		run.Status, run.FailureReason, run.Error = acceptLoopRun(loop, &run)
	}
	events := append(rec.events, loopRunEvent("finished", map[string]string{"status": run.Status, "reason": run.FailureReason, "error": run.Error}))
	rec.events = nil
	stored, err := s.store.UpdateLoopRun(context.Background(), workspace, run.ID, events, func(current *domain.LoopRun) error {
		if current.Status != "running" {
			// Reconciled (interrupted) or cancelled elsewhere first.
			return store.ErrNoMutation
		}
		run.Feedback, run.Replies = current.Feedback, current.Replies
		*current = run
		return nil
	})
	if err != nil {
		log.Printf("Loop run finish workspace=%s run=%s: %v", workspace, run.ID, err)
	}
	if err := s.store.ReleaseLoopRunLease(context.Background(), workspace, run.ID, s.loopInstanceID()); err != nil {
		log.Printf("Loop run lease release workspace=%s run=%s: %v", workspace, run.ID, err)
	}
	if run.Status != "completed" {
		log.Printf("Loop run finished workspace=%s loop=%s run=%s status=%s reason=%s: %s", workspace, run.LoopID, run.ID, run.Status, run.FailureReason, run.Error)
	}
	if err == nil {
		run = stored
	}
	s.publishLoopRunEvent(workspace, "loop_run.finished", run)
	if err := s.store.TrimLoopRuns(context.Background(), workspace, run.LoopID, loopRunHistory); err != nil {
		log.Printf("Loop run history trim workspace=%s loop=%s: %v", workspace, run.LoopID, err)
	}
}

// loopAppURL is the browser origin links in loop output point to: the
// configured app URL, else the origin last seen on an agent request.
func (s *server) loopAppURL() string {
	if origin := strings.TrimRight(strings.TrimSpace(s.allowedOrigin), "/"); origin != "" && origin != "*" {
		return origin
	}
	if origin, ok := s.agentOrigin.Load().(string); ok {
		return origin
	}
	return ""
}

// rememberAgentOrigin records the origin a browser used for an agent request.
func (s *server) rememberAgentOrigin(r *http.Request) {
	if origin := strings.TrimRight(r.Header.Get("Origin"), "/"); origin != "" && origin != "null" {
		s.agentOrigin.Store(origin)
		return
	}
	if r.Host != "" && r.Host != "flow.internal" {
		s.agentOrigin.Store(externalBaseURL(r))
	}
}

// loopRunRequest builds the request context a loop run acts under: the loop
// owner in the loop's workspace.
func (s *server) loopRunRequest(ctx context.Context, workspace string, owner domain.User) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://flow.internal/api/loops/run", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Workspace-Key", workspace)
	ctx = context.WithValue(request.Context(), workspaceKeyContextKey{}, workspace)
	ctx = context.WithValue(ctx, authUserContextKey{}, owner)
	// Links in tool results must open in the app, not on the synthetic host.
	ctx = context.WithValue(ctx, agentBaseURLKey{}, s.loopAppURL())
	return request.WithContext(ctx), nil
}

const loopSummaryPrompt = "You have used all tool turns available to this run. Do not call any more tools. Do the final self-check from what you already know, then reply with the short summary of what you did, including links to the issues you changed, and list anything you could not finish."

// loopRunTools lists the tools a run's model is offered (names) and which of
// them only read (retried on transient failures; never counted as output).
func (s *server) loopRunTools(ctx context.Context) ([]string, map[string]bool) {
	names := []string{}
	readOnly := map[string]bool{agentProgressTool: true, loopFinishToolName: true, webSearchToolName: true, fetchURLToolName: true}
	for _, tool := range s.agentTurnTools(ctx) {
		names = append(names, tool.Name)
		if tool.Access == "read" && !strings.HasPrefix(tool.Name, "external_") {
			readOnly[tool.Name] = true
		}
	}
	return names, readOnly
}

// executeLoopRun runs the loop's agent. history continues an earlier run: the
// run's answer and the replies that followed it.
func (s *server) executeLoopRun(ctx context.Context, workspace string, loop domain.Loop, trigger loopTrigger, rec *loopRunRecorder, history ...agentProviderMessage) error {
	limits := s.loopLimits()
	directory, ok := s.store.WorkspaceMetadataFields(workspace, "users")
	if !ok {
		return errNotFound
	}
	owner := userByID(&directory, loop.OwnerID)
	if owner == nil {
		return &loopRunError{reason: "unavailable", message: "the loop owner is no longer in this workspace"}
	}
	// The run reads workspace metadata only; issues and discussions are read
	// through the paged tool queries, never loaded whole.
	var data domain.Bootstrap
	if s.authDisabled {
		data, ok = s.store.WorkspaceMetadata(workspace)
		if !ok {
			return errNotFound
		}
		data.Viewer, data.ViewerRole = *owner, "admin"
	} else {
		var err error
		if data, err = s.store.PagedWorkspaceMetadata(ctx, workspace, owner.ID); err != nil {
			return &loopRunError{reason: "unavailable", message: "the loop owner cannot access this workspace"}
		}
	}
	if err := agentWorkspacePolicy(data.WorkspaceSettings, data.ViewerRole); err != nil {
		return &loopRunError{reason: "unavailable", message: err.Error(), err: err}
	}
	request, err := s.loopRunRequest(ctx, workspace, *owner)
	if err != nil {
		return err
	}
	scope := s.newLoopScope(data, loop, trigger)
	if connectors, err := s.discoverConnectorTools(ctx, data); err == nil {
		connectors = slices.DeleteFunc(connectors, func(tool connectorTool) bool { return !slices.Contains(loop.ConnectorIDs, tool.Policy.ID) })
		request = request.WithContext(context.WithValue(request.Context(), connectorToolsKey{}, connectors))
	}
	request = request.WithContext(context.WithValue(request.Context(), connectorContextKey{}, connectorRequestContext{Workspace: workspace, UserID: owner.ID}))
	request = request.WithContext(context.WithValue(request.Context(), agentToolFilterKey{}, scope.offers))
	request = request.WithContext(context.WithValue(request.Context(), agentExtraToolsKey{}, []agentProviderTool{loopFinishToolDefinition}))
	if loop.WebSearch && s.webSearchAvailable() {
		request = request.WithContext(withAgentWebTools(request.Context()))
	}
	toolNames, readOnly := s.loopRunTools(request.Context())
	plan := loopRunPlan{Tools: toolNames, Limits: limits, Outputs: effectiveLoopOutputs(loop)}

	entity := s.loopEntityContext(ctx, workspace, data, trigger)
	messages := []agentProviderMessage{
		{Role: "system", Content: loopSystemPrompt(data, loop, trigger, entity, scope, s.webSearchAvailable(), s.loopInstructionReferences(ctx, workspace, data, loop), plan) + loopReplySkillsPrompt(rec.skills)},
		{Role: "user", Content: "Run this loop now and follow its instructions."},
	}
	messages = append(messages, history...)
	output := ""
	turnText := &strings.Builder{}
	emit := func(event agentProviderEvent) error {
		if event.Type == "text.delta" {
			turnText.WriteString(event.Delta)
			rec.setOutput(strings.TrimSpace(output + turnText.String()))
		}
		return nil
	}
	toolCalls := 0
	for turnIndex := 0; ; turnIndex++ {
		final := turnIndex >= limits.MaxTurns
		if final {
			messages = append(messages, agentProviderMessage{Role: "user", Content: loopSummaryPrompt})
		}
		if size := loopContextBytes(messages); limits.MaxContextBytes > 0 && size > limits.MaxContextBytes {
			rec.setOutputNow(strings.TrimSpace(output))
			return loopBudgetError("The run's conversation reached %d KB, over its %d KB context budget. Narrow the instructions or the tool queries (filters, smaller limits).", size>>10, limits.MaxContextBytes>>10)
		}
		var turn agentProviderTurn
		retries, err := loopRetry(ctx, limits, func() error {
			turnText.Reset()
			var callErr error
			if final {
				turn, callErr = s.requestAgentTurnWithoutTools(request.Context(), messages)
			} else {
				turn, callErr = s.requestAgentTurn(request.Context(), messages, emit)
			}
			return callErr
		})
		rec.addRetries(retries)
		if err != nil {
			rec.setOutputNow(strings.TrimSpace(output))
			if ctx.Err() != nil {
				return err
			}
			return loopProviderFailure(err)
		}
		// Some gateways print report_progress as JSON text; keep it as steps instead.
		if leaked, rest := leakedProgressSteps(turn.Text); len(leaked) > 0 {
			turn.Text = rest
			for _, step := range leaked {
				rec.addStep(step.Title, step.Message)
			}
		}
		if final {
			turn.ToolCalls = nil
		}
		if len(turn.ToolCalls) == 0 {
			output += turn.Text
			rec.setOutputNow(strings.TrimSpace(output))
			if rec.output() == "" && rec.toolCount() == 0 && rec.summary() == nil {
				return &loopRunError{reason: "empty_response", message: "Flow Agent returned an empty response"}
			}
			return nil
		}
		// Narration between tool calls stays in the output so the run reads like a log.
		if text := strings.TrimSpace(turn.Text); text != "" {
			output += text + "\n\n"
		}
		rec.setOutput(strings.TrimSpace(output))
		messages = append(messages, agentProviderMessage{Role: "assistant", Content: turn.Text, ToolCalls: turn.ToolCalls})
		var finished *domain.LoopRunSummary
		for _, call := range turn.ToolCalls {
			reply := func(content string, isError bool) {
				messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: content, IsError: isError}})
			}
			if finished != nil {
				reply("The run already finished with finish_run; this call was not executed.", true)
				continue
			}
			switch call.Name {
			case agentProgressTool:
				var progress struct {
					Title   string `json:"title"`
					Message string `json:"message"`
				}
				_ = json.Unmarshal(call.Arguments, &progress)
				rec.addStep(progress.Title, progress.Message)
				reply(`{"ok":true}`, false)
				continue
			case loopFinishToolName:
				summary, err := parseLoopSummary(call.Arguments)
				if err != nil {
					reply(err.Error(), true)
					continue
				}
				finished = summary
				rec.setSummary(summary)
				reply(`{"ok":true}`, false)
				continue
			}
			toolCalls++
			if limits.MaxToolCalls > 0 && toolCalls > limits.MaxToolCalls {
				rec.setOutputNow(strings.TrimSpace(output))
				return loopBudgetError("The run made more than %d tool calls, its budget. Narrow the instructions or raise FLOW_LOOP_MAX_TOOL_CALLS.", limits.MaxToolCalls)
			}
			index := rec.startTool(call)
			status := "completed"
			var result []byte
			callErr := scope.check(ctx, call)
			if callErr != nil {
				status = "blocked"
			} else {
				execute := func() error {
					result, callErr = s.executeAgentTool(request, data, call)
					return callErr
				}
				if readOnly[strings.TrimPrefix(call.Name, "mcp__flow.")] {
					// Reads are safe to repeat; changes are never retried.
					retries, _ := loopRetry(ctx, limits, execute)
					rec.addRetries(retries)
				} else {
					execute()
				}
				if callErr != nil {
					status = "error"
				}
			}
			write := !readOnly[strings.TrimPrefix(call.Name, "mcp__flow.")] && !strings.HasPrefix(call.Name, "external_")
			rec.finishTool(index, status, callErr, write, loopToolArgs(call))
			if ctx.Err() != nil {
				return ctx.Err()
			}
			content := string(result)
			if callErr != nil {
				content = callErr.Error()
			}
			reply(truncateLoopToolOutput(content, limits.MaxToolOutputBytes), callErr != nil)
		}
		if finished != nil {
			output = strings.TrimSpace(output + "\n\n" + renderLoopSummary(finished))
			rec.setOutputNow(output)
			return nil
		}
	}
}

// loopToolLabel names a tool call the way the run log shows it.
func loopToolLabel(name string, args map[string]any) string {
	name = strings.TrimPrefix(name, "mcp__flow.")
	switch name {
	case webSearchToolName:
		return "Searched the web"
	case fetchURLToolName:
		return "Read web page"
	}
	if strings.HasPrefix(name, "external_") {
		return "Used " + strings.ReplaceAll(strings.TrimPrefix(name, "external_"), "_", " ")
	}
	updating := loopStringArg(args, "id") != ""
	labels := map[string]string{
		"get_issue": "Read issue", "list_issues": "Listed issues", "search_issues": "Searched issues",
		"list_comments": "Read comments", "triage_issue": "Triaged issue", "get_project": "Read project",
		"list_projects": "Listed projects", "list_users": "Listed members", "get_user": "Looked up member",
		"list_teams": "Listed teams", "get_team": "Read team", "list_issue_labels": "Listed labels",
		"list_issue_statuses": "Listed statuses", "list_cycles": "Listed cycles", "list_customers": "Listed customers",
		"save_status_update": "Posted status update", "get_status_updates": "Read status updates",
		"list_issue_history": "Read issue history", "list_project_activity": "Read project activity",
		"list_documents": "Listed documents", "get_document": "Read document", "search_documentation": "Searched documentation",
		"save_reaction": "Reacted", "create_reminder": "Set reminder", "list_diffs": "Listed code changes",
		"get_diff": "Read code change", "get_diff_threads": "Read review threads", "list_initiatives": "Listed initiatives",
		"get_initiative": "Read initiative", "list_releases": "Listed releases", "list_milestones": "Listed milestones",
	}
	if label, ok := labels[name]; ok {
		return label
	}
	switch name {
	case "save_issue":
		if updating {
			return "Updated issue"
		}
		return "Created issue"
	case "save_comment":
		if updating {
			return "Updated comment"
		}
		return "Commented"
	case "save_project":
		if updating {
			return "Updated project"
		}
		return "Created project"
	}
	verb, noun, _ := strings.Cut(name, "_")
	noun = strings.ReplaceAll(noun, "_", " ")
	switch verb {
	case "get":
		return "Read " + noun
	case "list":
		return "Listed " + noun
	case "search":
		return "Searched " + noun
	case "save":
		return "Saved " + noun
	case "delete":
		return "Deleted " + noun
	case "create":
		return "Created " + noun
	}
	return strings.ReplaceAll(name, "_", " ")
}

// loopToolArgsSummary is a short, readable summary of a call's arguments.
func loopToolArgsSummary(args map[string]any) string {
	parts := []string{}
	for _, key := range []string{"id", "issueId", "issue", "identifier", "query", "title", "team", "state", "status", "priority", "assignee", "delegate", "project", "labels", "body", "name", "url"} {
		value, ok := args[key]
		if !ok || value == nil {
			continue
		}
		var text string
		switch typed := value.(type) {
		case string:
			text = strings.TrimSpace(typed)
		case float64:
			text = strconv.FormatFloat(typed, 'f', -1, 64)
			if key == "priority" {
				text = map[int]string{0: "No priority", 1: "Urgent", 2: "High", 3: "Medium", 4: "Low"}[int(typed)]
			}
		case []any:
			items := []string{}
			for _, item := range typed {
				items = append(items, fmt.Sprint(item))
			}
			text = strings.Join(items, ", ")
		case bool:
			continue
		default:
			continue
		}
		if text == "" {
			continue
		}
		if len([]rune(text)) > 60 {
			text = string([]rune(text)[:57]) + "…"
		}
		if key != "id" && key != "issueId" && key != "issue" && key != "identifier" && key != "query" && key != "title" && key != "body" && key != "name" && key != "url" {
			text = key + " " + text
		}
		text = strings.ReplaceAll(text, "\n", " ")
		parts = append(parts, text)
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, " · ")
}

// loopScope enforces a loop's permissions on the tools a run may use.
type loopScope struct {
	s          *server
	workspace  string
	loop       domain.Loop
	trigger    loopTrigger
	entityIDs  []string // IDs and identifiers naming the triggering entity
	teams      map[string]bool
	codeAccess string // disabled | read | readWrite, after the workspace setting
}

func (s *server) newLoopScope(data domain.Bootstrap, loop domain.Loop, trigger loopTrigger) *loopScope {
	scope := &loopScope{s: s, workspace: data.Workspace.URLKey, loop: loop, trigger: trigger, teams: loopTeams(data, loop), codeAccess: "disabled"}
	if data.WorkspaceSettings.FeatureSettings.RepositoryAccess != nil && data.WorkspaceSettings.FeatureSettings.RepositoryAccess.AllowAutomationAccess {
		scope.codeAccess = loopCodeAccess(loop)
	}
	if trigger.EntityID != "" {
		scope.entityIDs = []string{trigger.EntityID}
		if trigger.EntityType == "issue" {
			if issue, err := s.store.IssueRecord(context.Background(), data.Workspace.URLKey, trigger.EntityID); err == nil {
				scope.entityIDs = append(scope.entityIDs, issue.Identifier)
			}
		}
	}
	return scope
}

// loopCodeAccess is the loop's own code access; loops stored before the
// setting existed could read code whenever the workspace allowed it.
func loopCodeAccess(loop domain.Loop) string {
	if loop.CodeAccess == "" {
		return "read"
	}
	return loop.CodeAccess
}

// loopTeams lists the teams whose data a loop may touch: all public teams, or
// the teams selected in its trigger scope.
func loopTeams(data domain.Bootstrap, loop domain.Loop) map[string]bool {
	selected := map[string]bool{}
	for _, id := range loopConfigStrings(loop.TriggerConfig, "teamIds") {
		selected[id] = true
	}
	if loop.TeamID != "" {
		selected[loop.TeamID] = true
	}
	teams := map[string]bool{}
	for _, team := range data.Teams {
		if team.ArchivedAt != nil || team.RetiredAt != nil {
			continue
		}
		settings := data.TeamSettings[team.ID]
		public := !team.Private && !strings.EqualFold(settings.Access, "private") && !strings.EqualFold(settings.Access, "restricted")
		if loop.TeamAccess == "selected" && selected[team.ID] || loop.TeamAccess != "selected" && (public || team.ID == loop.TeamID) {
			teams[team.ID] = true
		}
	}
	return teams
}

func codeIntelligenceTool(name string) bool {
	return strings.Contains(strings.TrimPrefix(name, "mcp__flow."), "diff")
}

// codeWriteTool reports whether a code tool changes code review state.
func codeWriteTool(name string) bool {
	name = strings.TrimPrefix(name, "mcp__flow.")
	return codeIntelligenceTool(name) && !strings.HasPrefix(name, "get_") && !strings.HasPrefix(name, "list_")
}

func (scope *loopScope) offers(tool agentProviderTool) bool {
	return scope.codeToolAllowed(tool.Name)
}

func (scope *loopScope) codeToolAllowed(name string) bool {
	if !codeIntelligenceTool(name) {
		return true
	}
	switch scope.codeAccess {
	case "readWrite":
		return true
	case "read":
		return !codeWriteTool(name)
	}
	return false
}

func loopToolArgs(call domain.AgentToolCall) map[string]any {
	args := map[string]any{}
	_ = json.Unmarshal(call.Arguments, &args)
	return args
}

func loopStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// issueExternallySynced reports whether an issue mirrors an item in another
// tool: a synced link (GitHub/GitLab/Jira sync) or an intake thread such as a
// Slack or email conversation.
func issueExternallySynced(data domain.Bootstrap, issue domain.Issue) bool {
	for _, attachment := range issue.Attachments {
		if attachment.SyncStatus == "synced" {
			return true
		}
	}
	for _, ask := range data.Asks {
		if ask.IssueID == issue.ID && ask.Source != "" && ask.Source != "web" && ask.Source != "loop" && ask.Source != "manual" {
			return true
		}
	}
	return false
}

// check refuses a tool call the loop's permissions do not allow.
func (scope *loopScope) check(ctx context.Context, call domain.AgentToolCall) error {
	name := strings.TrimPrefix(call.Name, "mcp__flow.")
	if codeIntelligenceTool(name) && !scope.codeToolAllowed(name) {
		if scope.codeAccess == "read" {
			return fmt.Errorf("This loop can read code but not change it")
		}
		return fmt.Errorf("Code access is not enabled for this loop")
	}
	if strings.HasPrefix(name, "external_") {
		return nil
	}
	if !scope.s.agentToolRequiresApproval(name) {
		return nil
	}
	args := loopToolArgs(call)
	if teamID := loopStringArg(args, "teamId", "team"); teamID != "" && !scope.teams[teamID] && !scope.teamKeyAllowed(teamID) {
		return fmt.Errorf("This loop cannot access that team")
	}
	target := loopStringArg(args, "issueId", "issue", "id")
	if slices.Contains([]string{"save_issue", "save_comment", "create_attachment", "delete_attachment", "delete_comment", "triage_issue"}, name) && target != "" {
		issue, err := scope.s.store.IssueRecord(ctx, scope.workspace, target)
		if err != nil {
			issue, err = scope.s.issueByIdentifier(ctx, scope.workspace, target)
		}
		if err == nil {
			if !scope.teams[issue.Team.ID] {
				return fmt.Errorf("This loop cannot access that team")
			}
			if !scope.loop.AllowExternalSync {
				if data, ok := scope.s.store.WorkspaceMetadataFields(scope.workspace, "asks"); ok && issueExternallySynced(data, issue) {
					return fmt.Errorf("This loop cannot change externally synced issues or comments")
				}
			}
		}
	}
	if len(scope.entityIDs) == 0 || scope.loop.AllowChangesOutsideTrigger {
		return nil
	}
	if target == "" || !slices.ContainsFunc(scope.entityIDs, func(id string) bool { return strings.EqualFold(id, target) }) {
		return fmt.Errorf("This loop can only change the %s that triggered it", scope.trigger.EntityType)
	}
	return nil
}

func (scope *loopScope) teamKeyAllowed(key string) bool {
	data, ok := scope.s.store.WorkspaceMetadataFields(scope.workspace, "teams")
	if !ok {
		return false
	}
	for _, team := range data.Teams {
		if strings.EqualFold(team.Key, key) || strings.EqualFold(team.Name, key) {
			return scope.teams[team.ID]
		}
	}
	return false
}

func (s *server) issueByIdentifier(ctx context.Context, workspace, identifier string) (domain.Issue, error) {
	// Workspace metadata carries no issues; the identifier index is exact and
	// identifiers are stored upper case.
	identifier = strings.ToUpper(strings.TrimSpace(identifier))
	if identifier == "" {
		return domain.Issue{}, errNotFound
	}
	issue, err := s.store.IssueRecord(ctx, workspace, identifier)
	if err != nil || !strings.EqualFold(issue.Identifier, identifier) {
		return domain.Issue{}, errNotFound
	}
	return issue, nil
}

func (s *server) loopEntityContext(ctx context.Context, workspace string, data domain.Bootstrap, trigger loopTrigger) string {
	if trigger.EntityID == "" {
		return ""
	}
	var value any
	switch trigger.EntityType {
	case "issue":
		issue, err := s.store.IssueRecord(ctx, workspace, trigger.EntityID)
		if err != nil {
			return ""
		}
		labels := []string{}
		for _, label := range issue.Labels {
			labels = append(labels, label.Name)
		}
		assignee := ""
		if issue.Assignee != nil {
			assignee = issue.Assignee.Name
		}
		value = map[string]any{"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "description": truncateSettingsText(issue.Description, 6000), "status": issue.State.Name, "priority": issue.PriorityLabel, "labels": labels, "assignee": assignee, "team": issue.Team.Name}
	case "project":
		for _, project := range data.Projects {
			if project.ID == trigger.EntityID {
				value = map[string]any{"id": project.ID, "name": project.Name, "description": truncateSettingsText(project.Description, 6000)}
			}
		}
	case "initiative":
		for _, initiative := range data.Initiatives {
			if initiative.ID == trigger.EntityID {
				value = map[string]any{"id": initiative.ID, "name": initiative.Name, "description": truncateSettingsText(initiative.Description, 6000)}
			}
		}
	case "cycle":
		for _, cycle := range data.Cycles {
			if cycle.ID == trigger.EntityID {
				value = map[string]any{"id": cycle.ID, "number": cycle.Number, "name": cycle.Name, "teamId": cycle.TeamID}
			}
		}
	case "release":
		for _, release := range data.Releases {
			if release.ID == trigger.EntityID {
				value = map[string]any{"id": release.ID, "name": release.Name, "status": release.Status, "stage": release.Stage}
			}
		}
	case "team":
		for _, team := range data.Teams {
			if team.ID == trigger.EntityID {
				value = map[string]any{"id": team.ID, "key": team.Key, "name": team.Name}
			}
		}
	}
	if value == nil {
		return ""
	}
	raw, _ := json.MarshalIndent(value, "", "  ")
	return string(raw)
}

// loopRunPlan is what the system prompt tells the model about how the run
// works: its tools, budgets and expected outputs.
type loopRunPlan struct {
	Tools   []string
	Limits  loopRunLimits
	Outputs []string
}

var loopOutputPrompts = map[string]string{
	"statusUpdate": "post the status update(s) the instructions ask for with save_status_update",
	"issue":        "create the issue(s) the instructions ask for with save_issue",
	"comment":      "post the comment(s) the instructions ask for with save_comment",
	"document":     "create or update the document(s) the instructions ask for with save_document",
	"change":       "make the change(s) in Flow the instructions ask for",
}

func loopSystemPrompt(data domain.Bootstrap, loop domain.Loop, trigger loopTrigger, entity string, scope *loopScope, webSearch bool, references string, plan loopRunPlan) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "You are running the Flow automation loop %q in the %s workspace. Work autonomously: there is no user to answer questions. Use the Flow tools to act; the run is judged by the changes it makes, not by what it says.\n", loop.Name, data.Workspace.Name)
	prompt.WriteString("\nHow to work:\n")
	prompt.WriteString("1. Plan: call report_progress with a short title for each distinct step (in the same turn as that step's tool calls).\n")
	prompt.WriteString("2. Gather: read only what the step needs. Use filters, a limit and the cursor on list tools; never try to read a whole workspace.\n")
	prompt.WriteString("3. Act: make each change the instructions ask for. After each change, check the tool result: a change only happened when the result has no error. Note the identifier and url it returned.\n")
	prompt.WriteString("4. Self-check before finishing: re-read the instructions and confirm every required output exists (one per item the instructions name), retry or explain any failed change, and list what you could not do.\n")
	fmt.Fprintf(&prompt, "5. Finish: call %s exactly once as your last action with status (done, incomplete or nothing_to_do), summary (under about 150 words; refer to issues by identifier and link them with the url the tools return), done (each change) and notDone (each thing not done and why). Do not end the run with plain text instead.\n", loopFinishToolName)
	if len(plan.Outputs) > 0 {
		wanted := []string{}
		for _, kind := range plan.Outputs {
			wanted = append(wanted, loopOutputPrompts[kind])
		}
		fmt.Fprintf(&prompt, "\nThis loop is expected to produce output: %s. A run that ends without it is flagged for review, so only report nothing_to_do when the instructions truly do not apply.\n", strings.Join(wanted, "; "))
	}
	fmt.Fprintf(&prompt, "\nLimits: at most %d tool turns and %d tool calls; tool results over %d KB are cut off; the run stops after %s.\n", plan.Limits.MaxTurns, plan.Limits.MaxToolCalls, max(plan.Limits.MaxToolOutputBytes>>10, 1), plan.Limits.Timeout.Round(time.Second))
	if len(plan.Tools) > 0 {
		fmt.Fprintf(&prompt, "Available tools: %s.\n", strings.Join(plan.Tools, ", "))
	}
	if guidance := strings.TrimSpace(data.WorkspaceSettings.AgentInstructions); guidance != "" {
		fmt.Fprintf(&prompt, "\nWorkspace guidance:\n%s\n", truncateSettingsText(guidance, 8000))
	}
	fmt.Fprintf(&prompt, "\nLoop instructions:\n%s\n", truncateSettingsText(loopModelInstructions(loop), 12000))
	if references != "" {
		fmt.Fprintf(&prompt, "\nReferenced in the instructions:\n%s", references)
	}
	switch trigger.Kind {
	case "event":
		label := strings.TrimPrefix(trigger.Label, "Triggered by ")
		if label == "" {
			label = fmt.Sprintf("a %s was %s", trigger.EntityType, strings.TrimPrefix(trigger.EventType, trigger.EntityType+"."))
		}
		fmt.Fprintf(&prompt, "\nThis run was triggered by: %s.\n", label)
	case "schedule":
		prompt.WriteString("\nThis run was started by the loop's schedule.\n")
	default:
		if trigger.EntityID != "" {
			fmt.Fprintf(&prompt, "\nThis run was started manually on the %s below.\n", trigger.EntityType)
		} else {
			prompt.WriteString("\nThis run was started manually.\n")
		}
	}
	if entity != "" {
		fmt.Fprintf(&prompt, "\nTriggering %s:\n%s\n", trigger.EntityType, entity)
		if trigger.EntityType == "issue" {
			// Flow has no Triage status: without this the model "accepts" issues by
			// moving them to Todo even when told to leave them in triage.
			prompt.WriteString("\nTriage in Flow: an issue is in its team's triage queue while it has a Backlog-type status and has not been accepted. Changing its status to any other status accepts it out of triage. To leave an issue in triage, do not change its status.\n")
		}
		if !loop.AllowChangesOutsideTrigger {
			fmt.Fprintf(&prompt, "\nOnly change the triggering %s; changes to anything else will be refused.\n", trigger.EntityType)
		}
	}
	if !loop.AllowExternalSync {
		prompt.WriteString("\nDo not change issues or comments that are synced from other tools (Slack, email, GitHub, Jira); such changes will be refused.\n")
	}
	switch scope.codeAccess {
	case "disabled":
		prompt.WriteString("\nThis loop has no access to code.\n")
	case "read":
		prompt.WriteString("\nThis loop can read code changes but not change them.\n")
	}
	if loop.WebSearch && webSearch {
		prompt.WriteString("\nWeb search is enabled: use web_search to find public information and fetch_url to read pages. Cite every web source you use with its URL (as a markdown link) in your summary.\n")
	} else if loop.WebSearch {
		prompt.WriteString("\nWeb search is enabled for this loop, but no web search provider is configured on this server; rely on workspace data and say so if the instructions need the web.\n")
	}
	names := []string{}
	for _, team := range data.Teams {
		if scope.teams[team.ID] {
			names = append(names, team.Name)
		}
	}
	fmt.Fprintf(&prompt, "\nTeams this loop may use: %s.\n", strings.Join(names, ", "))
	fmt.Fprintf(&prompt, "\nToday: %s\n", time.Now().UTC().Format("Monday, 2006-01-02"))
	return prompt.String()
}

// runDueLoops starts scheduled loops whose next run time has passed.
func (s *server) runDueLoops(workspace string, now time.Time) {
	data, ok := s.store.WorkspaceMetadataFields(workspace, "loops")
	if !ok {
		return
	}
	for _, loop := range data.Loops {
		if loop.TriggerType != "schedule" || !loopLive(loop) {
			continue
		}
		next := loop.NextRunAt
		if next == nil {
			computed := nextLoopRun(loop.TriggerConfig, loop.CreatedAt, now)
			s.setLoopNextRun(workspace, loop.ID, computed)
			next = &computed
		}
		if now.Before(*next) {
			continue
		}
		s.setLoopNextRun(workspace, loop.ID, nextLoopRun(loop.TriggerConfig, loop.CreatedAt, now))
		if _, err := s.startLoopRun(workspace, loop.ID, loopTrigger{Kind: "schedule"}, ""); err != nil && !errors.Is(err, errConflict) {
			log.Printf("Scheduled loop workspace=%s loop=%s: %v", workspace, loop.ID, err)
		}
	}
}

func (s *server) setLoopNextRun(workspace, loopID string, next time.Time) {
	// Scheduling only stamps the loop; copy just the loop catalog.
	ctx := store.WithMetadataFields(context.Background(), "loops")
	err := s.store.MutateWorkspace(ctx, workspace, "loop.scheduled", loopID, nil, func(data *domain.Bootstrap) error {
		loop := loopByID(data, loopID)
		if loop == nil {
			return errNotFound
		}
		loop.NextRunAt = &next
		return nil
	})
	if err != nil {
		log.Printf("Loop schedule workspace=%s loop=%s: %v", workspace, loopID, err)
	}
}

func parseLoopDate(value string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(value))
}

func parseLoopClock(value string) (int, int, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, 0, false
	}
	return parsed.Hour(), parsed.Minute(), true
}

// nextLoopRun returns the first scheduled time strictly after `after`. The
// schedule starts on startDate (legacy: starting) at `time` in `timezone` and
// repeats every `interval` hours, days, weeks or months; `weekdays` limits
// daily and weekly schedules to those days.
func nextLoopRun(config map[string]any, created, after time.Time) time.Time {
	config = normalizeLoopTriggerConfig("schedule", config)
	location := time.UTC
	if name, ok := config["timezone"].(string); ok {
		if loaded, err := time.LoadLocation(name); err == nil {
			location = loaded
		}
	}
	hour, minute := 10, 0
	if value, ok := config["time"].(string); ok {
		if h, m, ok := parseLoopClock(value); ok {
			hour, minute = h, m
		}
	}
	start := created.In(location)
	if value, ok := config["startDate"].(string); ok {
		if parsed, err := time.ParseInLocation("2006-01-02", value, location); err == nil {
			start = parsed
		}
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), hour, minute, 0, 0, location)
	interval := 1
	switch value := config["interval"].(type) {
	case float64:
		interval = int(value)
	case int:
		interval = value
	case string:
		interval, _ = strconv.Atoi(value)
	}
	if interval < 1 {
		interval = 1
	}
	unit, _ := config["unit"].(string)
	weekdays := map[time.Weekday]bool{}
	for _, day := range loopConfigStrings(config, "weekdays") {
		if index := slices.Index(loopWeekdays, strings.ToLower(day)); index >= 0 {
			weekdays[time.Weekday(index)] = true
		}
	}
	if unit == "hour" {
		step := time.Duration(interval) * time.Hour
		if start.After(after) {
			return start.UTC()
		}
		periods := int(after.Sub(start)/step) + 1
		next := start.Add(time.Duration(periods) * step)
		for !next.After(after) {
			next = next.Add(step)
		}
		return next.UTC()
	}
	if len(weekdays) > 0 && (unit == "week" || unit == "day" || unit == "") {
		// Walk day by day from the later of start and `after`.
		from := after.In(location)
		if start.After(from) {
			from = start
		}
		weekStart := start.AddDate(0, 0, -int(start.Weekday()))
		for offset := 0; offset <= 7*interval+14 && offset < 800; offset++ {
			day := time.Date(from.Year(), from.Month(), from.Day()+offset, hour, minute, 0, 0, location)
			if day.Before(start) || !day.After(after) || !weekdays[day.Weekday()] {
				continue
			}
			if unit == "week" {
				weeks := int(day.Sub(weekStart).Hours()/24) / 7
				if weeks%interval != 0 {
					continue
				}
			}
			return day.UTC()
		}
	}
	step := func(at time.Time, n int) time.Time {
		switch unit {
		case "week":
			return at.AddDate(0, 0, 7*interval*n)
		case "month":
			return at.AddDate(0, interval*n, 0)
		default:
			return at.AddDate(0, 0, interval*n)
		}
	}
	if start.After(after) {
		return start.UTC()
	}
	// Jump close to `after`, then step forward.
	periods := 0
	switch unit {
	case "week":
		periods = int(after.Sub(start).Hours() / (24 * 7 * float64(interval)))
	case "month":
		periods = ((after.Year()-start.Year())*12 + int(after.Month()) - int(start.Month())) / interval
	default:
		periods = int(after.Sub(start).Hours() / (24 * float64(interval)))
	}
	next := step(start, max(periods-1, 0))
	for !next.After(after) {
		periods++
		next = step(start, periods)
	}
	return next.UTC()
}
