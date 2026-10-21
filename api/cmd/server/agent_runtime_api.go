package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The internal runtime API is how agent workers (agent-worker, docs/agent-runtime-design.md §3, §8.1) execute runs
// without database credentials: they claim queued runs (agent_runs row leases), keep them with heartbeats, read
// the run payload and the prompt context Flow renders, mint run tokens for /mcp, post activities into the
// session (stored and fanned out to browsers exactly like the in-process agent's), keep a suspended session's
// serialized harness state, and finish the run.
//
// A worker authenticates with the shared worker secret (FLOW_AGENT_WORKER_SECRET) and names itself in the
// Flow-Worker-Id header; it may act only on runs it holds. A run token also authenticates the routes of its own
// run (except minting tokens), so a harness subprocess holding only the token can post its activities. The API is
// off (404) while no worker secret is configured.

const (
	agentRuntimeLease             = time.Minute
	agentRuntimeHeartbeatInterval = 15 * time.Second
	agentRuntimeMaxClaimWait      = 20 * time.Second
	// A cancel long-poll is answered at once when this process stops a run, and re-checks the database this often
	// for stops made by another API process.
	agentRuntimeMaxCancelWait     = 25 * time.Second
	agentRuntimeCancelRecheck     = time.Second
	agentRuntimeReconcileInterval = 30 * time.Second
	agentRuntimeMaxAttempts       = 2
	agentRuntimeWorkerHeader      = "Flow-Worker-Id"
)

// agentRuntimeWorker reports whether built-in agent runs are meant to execute on the worker runtime
// (FLOW_AGENT_RUNTIME=worker). Nothing enqueues runs yet: the in-process agent still runs every surface.
func (s *server) agentRuntimeWorker() bool {
	return s.agent.Runtime == appconfig.AgentRuntimeWorker
}

func (s *server) agentRuntimeEnabled() bool {
	return s.agent.WorkerSecret != "" && s.store != nil
}

func (s *server) registerAgentRuntimeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/agent-runtime/claim", s.claimAgentRuns)
	mux.HandleFunc("POST /internal/agent-runtime/cancels", s.watchAgentRunCancels)
	mux.HandleFunc("GET /internal/agent-runtime/runs/{id}/payload", s.agentRuntimeRun(true, s.agentRunPayload))
	mux.HandleFunc("POST /internal/agent-runtime/runs/{id}/heartbeat", s.agentRuntimeRun(true, s.heartbeatAgentRun))
	mux.HandleFunc("POST /internal/agent-runtime/runs/{id}/complete", s.agentRuntimeRun(false, s.completeAgentRun))
	mux.HandleFunc("POST /internal/agent-runtime/runs/{id}/token", s.agentRuntimeRun(false, s.createAgentRunToken))
	mux.HandleFunc("POST /internal/agent-runtime/runs/{id}/activities", s.agentRuntimeRun(true, s.postAgentRunActivity))
	mux.HandleFunc("GET /internal/agent-runtime/runs/{id}/changes", s.agentRuntimeRun(true, s.listAgentRunChanges))
	mux.HandleFunc("GET /internal/agent-runtime/runs/{id}/state", s.agentRuntimeRun(true, s.getAgentSessionState))
	mux.HandleFunc("PUT /internal/agent-runtime/runs/{id}/state", s.agentRuntimeRun(true, s.putAgentSessionState))
	mux.HandleFunc("DELETE /internal/agent-runtime/runs/{id}/state", s.agentRuntimeRun(true, s.deleteAgentSessionState))
}

// agentRuntimeCaller is an authenticated worker, or a run token (grant set) acting for its run's worker.
type agentRuntimeCaller struct {
	worker string
	grant  *agentRunGrant
}

func (s *server) authorizeAgentRuntime(w http.ResponseWriter, r *http.Request) (agentRuntimeCaller, bool) {
	if !s.agentRuntimeEnabled() {
		writeError(w, http.StatusNotFound, "not found")
		return agentRuntimeCaller{}, false
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		writeError(w, http.StatusUnauthorized, "Worker credentials are required")
		return agentRuntimeCaller{}, false
	}
	token := strings.TrimSpace(header[len("Bearer "):])
	if strings.HasPrefix(token, agentRunTokenPrefix) {
		grant, err := s.authenticateAgentRunToken(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Run token is invalid, expired, or its run has ended")
			return agentRuntimeCaller{}, false
		}
		return agentRuntimeCaller{worker: grant.run.LeaseOwner, grant: grant}, true
	}
	given, want := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(s.agent.WorkerSecret))
	if subtle.ConstantTimeCompare(given[:], want[:]) != 1 {
		writeError(w, http.StatusUnauthorized, "Worker credentials are invalid")
		return agentRuntimeCaller{}, false
	}
	worker := strings.TrimSpace(r.Header.Get(agentRuntimeWorkerHeader))
	if worker == "" || len(worker) > 128 || strings.ContainsFunc(worker, func(r rune) bool { return r < 0x21 || r > 0x7e }) {
		writeError(w, http.StatusBadRequest, agentRuntimeWorkerHeader+" must name the worker (1-128 visible ASCII characters)")
		return agentRuntimeCaller{}, false
	}
	return agentRuntimeCaller{worker: worker}, true
}

// agentRuntimeRun authorizes a route of one run: the caller must hold the run (or present its token, when
// allowToken).
func (s *server) agentRuntimeRun(allowToken bool, handler func(http.ResponseWriter, *http.Request, agentRuntimeCaller, store.AgentRun)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := s.authorizeAgentRuntime(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if caller.grant != nil && (!allowToken || caller.grant.run.ID != id) {
			writeError(w, http.StatusForbidden, "This run token cannot be used here")
			return
		}
		run, err := s.store.AgentRun(r.Context(), id)
		if errors.Is(err, store.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "agent run not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "agent run could not be read")
			return
		}
		if run.LeaseOwner != caller.worker || !store.AgentRunHeld(run.Status) {
			writeError(w, http.StatusConflict, "This worker does not hold the run")
			return
		}
		handler(w, r, caller, run)
	}
}

// agentRuntimeLanes maps a claim's free slots by run kind to priority lanes.
var agentRuntimeLanes = map[string]int{"chat": 0, "thread": 1, "delegation": 1, "loop": 2, "background": 3}

func (s *server) claimAgentRuns(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.authorizeAgentRuntime(w, r)
	if !ok {
		return
	}
	if caller.grant != nil {
		writeError(w, http.StatusForbidden, "This run token cannot be used here")
		return
	}
	var input struct {
		// Free is how many more runs the worker can take, by kind: chat, thread (delegation too), loop, background.
		Free map[string]int `json:"free"`
		// WaitMS holds the request up to this long (at most 20 s) until a run is claimable.
		WaitMS int `json:"waitMs"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	free := map[int]int{}
	for kind, slots := range input.Free {
		lane, known := agentRuntimeLanes[kind]
		if !known || slots < 0 || slots > 256 {
			writeError(w, http.StatusBadRequest, "free must count 0-256 slots for chat, thread, loop or background")
			return
		}
		free[lane] += slots
	}
	perWorkspace := s.agent.WorkspaceRunConcurrency
	if perWorkspace <= 0 {
		perWorkspace = 4
	}
	wait := min(max(time.Duration(input.WaitMS)*time.Millisecond, 0), agentRuntimeMaxClaimWait)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		// Taken before the claim, so a run enqueued between the claim and the wait still wakes this request.
		wake := s.store.AgentRunWake()
		runs, err := s.store.ClaimAgentRuns(r.Context(), store.AgentRunClaim{Owner: caller.worker, Free: free, MaxPerWorkspace: perWorkspace, Lease: agentRuntimeLease, Now: time.Now()})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "agent runs could not be claimed")
			return
		}
		if len(runs) > 0 || wait == 0 {
			if runs == nil {
				runs = []store.AgentRun{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "leaseMs": agentRuntimeLease.Milliseconds(), "heartbeatMs": agentRuntimeHeartbeatInterval.Milliseconds()})
			return
		}
		select {
		case <-wake:
		case <-deadline.C:
			wait = 0
		case <-r.Context().Done():
			return
		}
	}
}

// watchAgentRunCancels is the push path for stops (§8.1): a worker keeps one long-poll open and is answered as soon
// as a run it holds should stop — a cancel was requested or the person stopped the session — instead of learning it
// on the run's next heartbeat (every 15 s, which stays the fallback). Ignore lists runs the worker is already
// stopping, so the answer is not repeated while they finish.
func (s *server) watchAgentRunCancels(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.authorizeAgentRuntime(w, r)
	if !ok {
		return
	}
	if caller.grant != nil {
		writeError(w, http.StatusForbidden, "This run token cannot be used here")
		return
	}
	var input struct {
		// WaitMS holds the request up to this long (at most 25 s) until a held run should stop.
		WaitMS int      `json:"waitMs"`
		Ignore []string `json:"ignore"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	wait := min(max(time.Duration(input.WaitMS)*time.Millisecond, 0), agentRuntimeMaxCancelWait)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	recheck := time.NewTicker(agentRuntimeCancelRecheck)
	defer recheck.Stop()
	for {
		// Taken before the query, so a stop between the query and the wait still wakes this request.
		wake := s.store.AgentRunCancelWake()
		ids, err := s.store.AgentRunsToCancel(r.Context(), caller.worker)
		if err != nil {
			if r.Context().Err() == nil {
				writeError(w, http.StatusInternalServerError, "canceled runs could not be read")
			}
			return
		}
		ids = slices.DeleteFunc(ids, func(id string) bool { return slices.Contains(input.Ignore, id) })
		if len(ids) > 0 || wait == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"cancel": ids})
			return
		}
		select {
		case <-wake:
		case <-recheck.C:
		case <-deadline.C:
			wait = 0
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) heartbeatAgentRun(w http.ResponseWriter, r *http.Request, caller agentRuntimeCaller, run store.AgentRun) {
	run, err := s.store.HeartbeatAgentRun(r.Context(), run.ID, caller.worker, agentRuntimeLease, time.Now())
	if errors.Is(err, store.ErrAgentRunLeaseLost) {
		writeError(w, http.StatusConflict, "This worker does not hold the run")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "heartbeat failed")
		return
	}
	cancel := run.CancelRequested
	if !cancel {
		// A person stopped the session (a stop signal in the thread): the run should stop too.
		if task, err := s.store.AgentTask(r.Context(), run.WorkspaceKey, run.SessionID); err == nil && task.Status == "canceled" {
			cancel = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"leaseUntil": run.LeaseUntil, "cancel": cancel})
}

func (s *server) completeAgentRun(w http.ResponseWriter, r *http.Request, caller agentRuntimeCaller, run store.AgentRun) {
	var input struct {
		Status string          `json:"status"`
		Error  string          `json:"error"`
		Usage  json.RawMessage `json:"usage"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	run, err := s.store.FinishAgentRun(r.Context(), run.ID, caller.worker, store.AgentRunResult{Status: input.Status, Error: input.Error, Usage: input.Usage}, time.Now())
	if err == nil && (input.Status == "done" || input.Status == "failed") {
		// A finished run that should have entered Coding and did not is moved into it (or reports the blocker).
		s.ensureWorkerRunCoding(r, run)
	}
	switch {
	case errors.Is(err, store.ErrAgentRunInput):
		writeError(w, http.StatusBadRequest, "status must be done, failed, canceled, awaiting or interrupted, and usage a JSON value under 64 KB")
	case errors.Is(err, store.ErrAgentRunLeaseLost):
		writeError(w, http.StatusConflict, "This worker does not hold the run")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "the run could not be finished")
	default:
		writeJSON(w, http.StatusOK, run)
	}
}

func (s *server) createAgentRunToken(w http.ResponseWriter, r *http.Request, _ agentRuntimeCaller, run store.AgentRun) {
	token, expires, err := s.mintAgentRunToken(r.Context(), run)
	if err != nil {
		writeError(w, http.StatusConflict, "a token cannot be issued for this run")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expiresAt": expires.UnixMilli(), "mcpPath": "/mcp"})
}

// agentRunPromptMessage is one message of the conversation a run starts from.
type agentRunPromptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// agentRunPromptContext is the prompt context Flow renders for a run, as the in-process agent builds it.
type agentRunPromptContext struct {
	// System is the system prompt: rules, workspace / team / personal guidance, the session's resources and the
	// issues the request names (loaded up front, so "introduce DEV-16" needs no lookup).
	System string `json:"system"`
	// Messages are the task prompt and the thread so far, oldest first.
	Messages []agentRunPromptMessage `json:"messages"`
	// PendingTool is the approval the session is answering (held calls and the person's decision).
	PendingTool *domain.AgentToolCall `json:"pendingTool,omitempty"`
	// LatestPrompt is the person's latest message (approval answers and publish requests are read from it).
	LatestPrompt string `json:"latestPrompt"`
	WebAccess    bool   `json:"webAccess"`
	// ReferencedIssues lists the identifiers of the issues loaded into System beyond the session's own.
	ReferencedIssues []string `json:"referencedIssues,omitempty"`
}

func (s *server) agentRunPayload(w http.ResponseWriter, r *http.Request, _ agentRuntimeCaller, run store.AgentRun) {
	principal, err := decodeAgentRunPrincipal(run)
	if err != nil {
		writeError(w, http.StatusConflict, "the run has no valid principal")
		return
	}
	response := map[string]any{"run": run, "principal": map[string]any{"mode": principal.Mode, "userId": principal.UserID, "appUserId": principal.AppUserID, "tools": principal.Tools}}
	task, err := s.store.AgentTask(r.Context(), run.WorkspaceKey, run.SessionID)
	if err == nil {
		task.DeriveTimedState(time.Now())
		response["session"] = task
		prompt, err := s.renderAgentRunTaskContext(r, task)
		if err != nil {
			writeError(w, http.StatusConflict, "the run's application or requester no longer has access to the session")
			return
		}
		response["context"] = prompt
	} else if !store.IsAgentTaskMissing(err) {
		writeError(w, http.StatusInternalServerError, "the run's session could not be read")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// renderAgentRunTaskContext renders the prompt context of a built-in agent session (comment thread or
// delegation) as executeBuiltinApplication builds it, plus the issues the request names.
func (s *server) renderAgentRunTaskContext(r *http.Request, task domain.AgentTask) (*agentRunPromptContext, error) {
	request, data, appUser, key, err := s.builtinRunRequest(r, task)
	if err != nil {
		return nil, err
	}
	ctx := request.Context()
	invoker, _ := request.Context().Value(builtinInvokerContextKey{}).(domain.User)
	actor := mcpActor{WorkspaceKey: task.WorkspaceKey, User: appUser, APIKey: key, AuthorizationUserID: invoker.ID}
	refs := findAgentMessageReferences(data, s.builtinTaskPrompt(ctx, task, data)+"\n"+task.Prompt)
	extra := s.agentRunReferencedIssues(ctx, actor, refs.IssueIdentifiers, data.Issues)
	messages := s.builtinThreadMessages(request, task, data, append(slices.Clone(data.Issues), extra...))
	prompt := &agentRunPromptContext{System: messages[0].Content, PendingTool: task.PendingTool, LatestPrompt: task.Prompt, WebAccess: agentWebAccess(data.WorkspaceSettings)}
	for _, message := range messages[1:] {
		prompt.Messages = append(prompt.Messages, agentRunPromptMessage{Role: message.Role, Content: message.Content})
	}
	for _, issue := range extra {
		prompt.ReferencedIssues = append(prompt.ReferencedIssues, issue.Identifier)
	}
	return prompt, nil
}

// builtinRunRequest is a request acting as a built-in agent session's application (its scopes and teams, the
// person who delegated or mentioned it) with the session's context, as executeBuiltinApplication runs it.
func (s *server) builtinRunRequest(r *http.Request, task domain.AgentTask) (*http.Request, domain.Bootstrap, domain.User, domain.APIKey, error) {
	ctx := r.Context()
	app, err := s.store.ApplicationByUser(ctx, task.AppUserID)
	if err != nil || !app.Active || !app.Builtin || !slices.Contains(app.AccessTeamIDs(), task.TeamID) {
		return nil, domain.Bootstrap{}, domain.User{}, domain.APIKey{}, store.ErrAuthForbidden
	}
	appUser := app.User()
	key := domain.APIKey{CreatorID: appUser.ID, SubjectUserID: appUser.ID, Actor: "app", Scopes: app.Scopes, TeamRestriction: "selected", TeamIDs: app.AccessTeamIDs(), OAuthClientID: app.ClientID}
	ctx = context.WithValue(ctx, authUserContextKey{}, appUser)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, task.WorkspaceKey)
	ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
	ctx = store.ContextWithActor(ctx, appUser)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://flow.internal/api/agent-tasks/"+task.ID, nil)
	if err != nil {
		return nil, domain.Bootstrap{}, domain.User{}, domain.APIKey{}, err
	}
	data, err := s.applicationTaskContext(request, task, false)
	if err != nil {
		return nil, domain.Bootstrap{}, domain.User{}, domain.APIKey{}, err
	}
	data.Viewer, data.ViewerRole = appUser, "app"
	request, data, err = s.builtinApplicationContext(request, app, task)
	if err != nil {
		return nil, domain.Bootstrap{}, domain.User{}, domain.APIKey{}, err
	}
	return request, data, appUser, key, nil
}

// ensureWorkerRunCoding gives a worker run the in-process agent's Coding guarantee. The worker runs its own model
// loop (agent-worker), so Flow cannot bound its connector reads or step in at its forced-decision turn; it checks
// when the run finishes instead: a delegation whose request clearly asks for code changes, with Coding available
// and no coding session yet, is moved into Coding through the same tool path (or ends with the environment
// blocker), and the thread gets the action and the session's state. Mentions and replies are left to the worker:
// they share the delegation's session (issueCodingIdempotencyKey) only when the issue is delegated.
func (s *server) ensureWorkerRunCoding(r *http.Request, run store.AgentRun) {
	task, err := s.store.AgentTask(r.Context(), run.WorkspaceKey, run.SessionID)
	if err != nil || task.Trigger != "delegation" || task.CodingSessionID != "" {
		return
	}
	request, data, _, _, err := s.builtinRunRequest(r, task)
	if err != nil {
		return
	}
	guard := newBuiltinRunGuard(s.builtinCodingContextFor(request.Context(), task, data))
	if !guard.codingDue() {
		return
	}
	appendActivity := func(activity domain.AgentActivity) error {
		activity.ActorID = task.AppUserID
		next, err := appendBuiltinActivity(request.Context(), s.store, task, activity)
		if err == nil {
			task = next
		}
		return err
	}
	execute := func(call domain.AgentToolCall) ([]byte, error) { return s.executeAgentTool(request, data, call) }
	issueTitle := ""
	if len(data.Issues) == 1 {
		issueTitle = data.Issues[0].Title
	}
	state, ok := s.builtinCodingTransfer(request.Context(), task, guard, issueTitle, "the run finished without starting Coding", execute, appendActivity)
	if ok {
		_ = appendActivity(domain.AgentActivity{Type: "response", Body: state})
	}
}

// agentRunReferencedIssues loads the issues named by identifier that the actor can see and that are not loaded
// already (agentReferencedIssues for a session acting through the application).
func (s *server) agentRunReferencedIssues(ctx context.Context, actor mcpActor, identifiers []string, loaded []domain.Issue) []domain.Issue {
	identifiers = slices.DeleteFunc(slices.Clone(identifiers), func(identifier string) bool {
		return slices.ContainsFunc(loaded, func(issue domain.Issue) bool { return strings.EqualFold(issue.Identifier, identifier) })
	})
	if len(identifiers) == 0 {
		return nil
	}
	q, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return nil
	}
	q.Filter = store.IssueFilter{Field: "identifier", Values: identifiers}
	q.Limit = agentReferencedIssueLimit
	page, err := s.store.QueryIssueRecords(ctx, q)
	if err != nil {
		return nil
	}
	for index := range page.Items {
		page.Items[index].Description = truncateSettingsText(strings.TrimSpace(page.Items[index].Description), agentReferencedDescriptionRunes)
	}
	return page.Items
}

// agentRunActivityTypes are the activities a run may post to its session.
var agentRunActivityTypes = []string{"thought", "action", "output", "response", "error", "elicitation"}

func (s *server) postAgentRunActivity(w http.ResponseWriter, r *http.Request, _ agentRuntimeCaller, run store.AgentRun) {
	var input struct {
		// ActivityID is the worker's id for the activity: posting it again returns the first result.
		ActivityID     string                `json:"activityId"`
		Type           string                `json:"type"`
		Body           string                `json:"body"`
		Ephemeral      bool                  `json:"ephemeral"`
		Signal         string                `json:"signal"`
		SignalMetadata map[string]any        `json:"signalMetadata"`
		ToolCall       *domain.AgentToolCall `json:"toolCall"`
		Content        *struct {
			Action    string `json:"action"`
			Parameter string `json:"parameter"`
			Result    string `json:"result"`
		} `json:"content"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.ActivityID) > 128 {
		writeError(w, http.StatusBadRequest, "activityId is too long")
		return
	}
	if !slices.Contains(agentRunActivityTypes, input.Type) {
		writeError(w, http.StatusBadRequest, "type must be thought, action, output, response, error or elicitation")
		return
	}
	if err := validateApplicationSignal(input.Type, input.Signal, input.SignalMetadata, true); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Ephemeral && input.Type != "thought" && input.Type != "action" {
		writeError(w, http.StatusBadRequest, "Only thought and action activities can be ephemeral")
		return
	}
	if input.ToolCall != nil && input.Type != "elicitation" && input.Type != "action" {
		writeError(w, http.StatusBadRequest, "Only action and elicitation activities carry a tool call")
		return
	}
	key := ""
	if input.ActivityID != "" {
		key = "activity:" + input.ActivityID
		if cached, found, err := s.store.AgentRunKey(r.Context(), run.ID, key); err == nil && found {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(cached)
			return
		}
	}
	task, err := s.store.AgentTask(r.Context(), run.WorkspaceKey, run.SessionID)
	if err != nil {
		writeError(w, http.StatusConflict, "This run's session takes no activities (chat sessions move to activities later)")
		return
	}
	app, err := s.store.ApplicationByUser(r.Context(), task.AppUserID)
	if err != nil || !app.Active || !slices.Contains(app.AccessTeamIDs(), task.TeamID) {
		writeError(w, http.StatusForbidden, "The application no longer has access to this session")
		return
	}
	activity := domain.AgentActivity{ActorID: task.AppUserID, Type: input.Type, Body: input.Body, Signal: input.Signal, SignalMetadata: input.SignalMetadata, Ephemeral: input.Ephemeral, ToolCall: input.ToolCall}
	if input.Content != nil && input.Type == "action" {
		activity.Metadata = map[string]any{"action": input.Content.Action, "parameter": input.Content.Parameter, "result": input.Content.Result}
	}
	// The session's version moves with every activity (a person may reply meanwhile): retry on a fresh read.
	for attempt := 0; ; attempt++ {
		updated, err := s.store.AppendAgentActivity(r.Context(), task.WorkspaceKey, task.ID, task.Version, activity)
		if errors.Is(err, store.ErrIssueVersion) && attempt < 3 {
			current, readErr := s.store.AgentTask(r.Context(), task.WorkspaceKey, task.ID)
			if readErr == nil && current.Version != task.Version {
				task = current
				continue
			}
		}
		switch {
		case errors.Is(err, store.ErrIssueQuery):
			writeError(w, http.StatusBadRequest, "Invalid activity")
		case errors.Is(err, store.ErrIssueVersion):
			writeError(w, http.StatusConflict, "The session no longer accepts this activity (it was stopped or is waiting for the person)")
		case err != nil:
			writeError(w, http.StatusInternalServerError, "the activity could not be saved")
		default:
			body, _ := json.Marshal(map[string]any{"activityId": fmt.Sprintf("%020d", updated.Version), "session": updated})
			if key != "" {
				_ = s.store.SaveAgentRunKey(r.Context(), run, key, body)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		}
		return
	}
}

func (s *server) listAgentRunChanges(w http.ResponseWriter, r *http.Request, _ agentRuntimeCaller, run store.AgentRun) {
	changes, err := s.store.AgentRunChanges(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the run's changes could not be read")
		return
	}
	items := make([]map[string]any, 0, len(changes))
	for _, item := range changes {
		items = append(items, map[string]any{"ref": newAgentRunChangeRef(run.ID, item.Seq, item.Change), "tool": item.Tool, "change": item.Change})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getAgentSessionState(w http.ResponseWriter, r *http.Request, _ agentRuntimeCaller, run store.AgentRun) {
	state, err := s.store.AgentSessionStateFor(r.Context(), run.WorkspaceKey, run.SessionID)
	if errors.Is(err, store.ErrAgentRunNotFound) {
		writeError(w, http.StatusNotFound, "the session has no stored state")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the session state could not be read")
		return
	}
	w.Header().Set("Content-Type", state.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Flow-State-Run-Id", state.RunID)
	w.Header().Set("Flow-State-Updated-At", strconv.FormatInt(state.UpdatedAt, 10))
	_, _ = w.Write(state.Data)
}

func (s *server) putAgentSessionState(w http.ResponseWriter, r *http.Request, _ agentRuntimeCaller, run store.AgentRun) {
	r.Body = http.MaxBytesReader(w, r.Body, store.AgentSessionStateMaxBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("session state is limited to %d bytes", store.AgentSessionStateMaxBytes))
		return
	}
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := s.store.SaveAgentSessionState(r.Context(), store.AgentSessionState{WorkspaceKey: run.WorkspaceKey, SessionID: run.SessionID, RunID: run.ID, ContentType: contentType, Data: data}); err != nil {
		if errors.Is(err, store.ErrAgentRunInput) {
			writeError(w, http.StatusBadRequest, "invalid session state")
			return
		}
		writeError(w, http.StatusInternalServerError, "the session state could not be saved")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deleteAgentSessionState(w http.ResponseWriter, r *http.Request, _ agentRuntimeCaller, run store.AgentRun) {
	if err := s.store.DeleteAgentSessionState(r.Context(), run.WorkspaceKey, run.SessionID); err != nil {
		writeError(w, http.StatusInternalServerError, "the session state could not be deleted")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// startAgentRunReconciler recovers runs whose worker stopped heartbeating (§11): every 30 s, expired leases go back
// to the queue when the run wrote nothing, and otherwise the run is interrupted and its session told so.
func (s *server) startAgentRunReconciler() {
	if !s.agentRuntimeEnabled() || !s.agentRunReconcilerStarted.CompareAndSwap(false, true) {
		return
	}
	go func() {
		ctx := s.store.WorkerContext()
		ticker := time.NewTicker(agentRuntimeReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileAgentRuns(ctx, time.Now())
			}
		}
	}()
}

// reconcileAgentRuns releases expired leases once and posts an error activity to the sessions of interrupted runs.
func (s *server) reconcileAgentRuns(ctx context.Context, now time.Time) (requeued, interrupted int) {
	retried, stopped, err := s.store.ReleaseExpiredAgentRuns(ctx, now, agentRuntimeMaxAttempts)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Agent run reconcile: %v", err)
		}
		return len(retried), len(stopped)
	}
	for _, run := range stopped {
		task, err := s.store.AgentTask(ctx, run.WorkspaceKey, run.SessionID)
		if err != nil || task.Status == "complete" || task.Status == "error" || task.Status == "canceled" {
			continue
		}
		// Like the in-process agent after a crash: no silent repeat of writes, the person decides to retry.
		_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "error", Body: "Execution was interrupted. Inspect previous tool results before retrying."})
	}
	return len(retried), len(stopped)
}
