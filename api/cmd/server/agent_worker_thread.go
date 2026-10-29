package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Built-in agent sessions on the agent worker (FLOW_AGENT_RUNTIME=worker, docs/agent-runtime-design.md §6.7, §7.2):
// a comment-thread mention runs as an agent run of kind thread, a delegated issue as one of kind delegation (its
// Coding transfer through the run guard), instead of executeBuiltinApplication in the API.
// The session, its activities and its delivery are unchanged; only who runs the model loop moves:
//
//   - delivery: the application worker claims the pending task as before, posts the same first activity (the
//     ephemeral look at the resource), and queues a run acting as the built-in application on behalf of the person
//     who asked (principal mode app). The payload's context is the thread prompt executeBuiltinApplication renders
//     (renderAgentRunTaskContext); its `thread` part carries the turn and call budgets and, when the person answered
//     a held call, the decisions the run resumes with. While the run is queued or held the task is not delivered
//     again; a queued run no worker takes within FLOW_AGENT_WORKER_CLAIM_TIMEOUT is cancelled and the session ends
//     with workerChatNoWorker.
//   - activities: the worker posts what the in-process run appends — an action per Flow tool call, streamed text as
//     output, ask_user's question as a select elicitation, held connector calls as one approval elicitation (Flow
//     builds its question and held call, workerThreadElicitation), the response or the error. /mcp posts the
//     connector calls' actions itself, as for an in-process run.
//   - rules: the run guard (agent_runtime_guard.go) gives the in-process run's Coding transfer and connector budget,
//     and for these runs also its tool budget (a spent budget withholds the tools for the last word), the empty-reply
//     and text-approval retries and the outcome check; /mcp counts the calls against the budget.
//   - waiting: a select elicitation ends the run; the person's reply is the next delivery. Held calls suspend the run
//     (RunState in agent_session_states); the person's decision is the next delivery, whose run resumes the state
//     with it. A held call saved without a suspended run (an earlier version, an in-process run) runs in-process.

// workerThreadRunInfo is what a built-in session run's payload says (agent_runs.payload "thread").
type workerThreadRunInfo struct {
	// TaskVersion is the session version the run was queued for.
	TaskVersion int64 `json:"taskVersion"`
	MaxTurns    int   `json:"maxTurns"`
	MaxCalls    int   `json:"maxCalls"`
	// Resume is set on a run that continues a suspended run: the person's decision per held call id.
	Resume *workerChatResume `json:"resume,omitempty"`
	// HostedWebSearch: the run searches with the model provider's hosted search (workerChatRunInfo.HostedWebSearch).
	HostedWebSearch bool `json:"hostedWebSearch,omitempty"`
}

func workerThreadInfo(run store.AgentRun) (workerThreadRunInfo, bool) {
	if run.Kind != store.AgentRunKindThread && run.Kind != store.AgentRunKindDelegation || len(run.Payload) == 0 {
		return workerThreadRunInfo{}, false
	}
	var payload struct {
		Thread *workerThreadRunInfo `json:"thread"`
	}
	if json.Unmarshal(run.Payload, &payload) != nil || payload.Thread == nil {
		return workerThreadRunInfo{}, false
	}
	return *payload.Thread, true
}

// agentTaskOnWorker reports whether a built-in agent session's runs execute on the agent worker: comment-thread
// mentions and delegated issues alike.
func (s *server) agentTaskOnWorker(domain.AgentTask) bool {
	return s.agentRuntimeWorker() && s.agentRuntimeEnabled()
}

// workerTaskRun is the session's latest built-in run on the worker, if any.
func (s *server) workerTaskRun(ctx context.Context, task domain.AgentTask) (store.AgentRun, workerThreadRunInfo, bool) {
	runs, err := s.store.SessionAgentRuns(ctx, task.WorkspaceKey, task.ID, 20)
	if err != nil {
		return store.AgentRun{}, workerThreadRunInfo{}, false
	}
	for _, run := range runs {
		if info, ok := workerThreadInfo(run); ok {
			return run, info, true
		}
	}
	return store.AgentRun{}, workerThreadRunInfo{}, false
}

// deliverBuiltinTaskToWorker is deliverApplicationTask for a built-in session whose runs execute on the worker. It
// reports false when the delivery must run in-process instead (an answered held call without a suspended run).
func (s *server) deliverBuiltinTaskToWorker(ctx context.Context, task domain.AgentTask, data domain.Bootstrap) bool {
	if run, _, ok := s.workerTaskRun(ctx, task); ok {
		switch run.Status {
		case store.AgentRunQueued:
			if canceled, expired, err := s.store.CancelUnclaimedAgentRun(ctx, run.ID, workerChatNoWorker, time.Now().Add(-s.agentWorkerClaimTimeout())); err == nil && expired {
				_ = s.store.DeleteAgentSessionState(ctx, canceled.WorkspaceKey, canceled.SessionID)
				_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "error", Body: workerChatNoWorker})
				return true
			}
			fallthrough
		case store.AgentRunLeased, store.AgentRunRunning:
			// The run is on its way: look again in a minute (its activities keep the session's lease meanwhile).
			_ = s.store.FinishApplicationDelivery(ctx, task.ID, task.Version, false)
			return true
		}
	}
	if task.Status == "active" {
		// Mid-run with no run left to finish it (its worker went away): as in-process after a crash.
		_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "error", Body: "Execution was interrupted. Inspect previous tool results before retrying."})
		return true
	}
	info := workerThreadRunInfo{TaskVersion: task.Version, MaxTurns: s.agentMaxToolTurns(), MaxCalls: s.agentMaxToolCalls(), HostedWebSearch: s.agentRunHostedWebSearch(agentWebAccess(data.WorkspaceSettings))}
	if task.PendingTool != nil {
		// The person answered held calls: the suspended run continues with the decision, when there is one.
		state, err := s.store.AgentSessionStateFor(ctx, task.WorkspaceKey, task.ID)
		if err != nil || state.RunID == "" {
			return false
		}
		decisions := map[string]string{}
		for _, call := range agentApprovalBatchCalls(*task.PendingTool) {
			decisions[call.ID] = map[bool]string{true: "approved", false: "rejected"}[task.PendingTool.Status == "approved"]
		}
		info.Resume = &workerChatResume{Decisions: decisions}
	}
	kind := store.AgentRunKindThread
	if task.Trigger == "delegation" {
		kind = store.AgentRunKindDelegation
	}
	principal, _ := json.Marshal(agentRunPrincipal{Mode: agentRunModeApp, UserID: task.CreatorID, AppUserID: task.AppUserID, TeamID: task.TeamID})
	payload, _ := json.Marshal(map[string]any{"thread": info})
	version := task.Version
	// Instant feedback (AIG): the session shows the agent at work before the run is claimed.
	task = s.acknowledgeBuiltinTask(ctx, task, data)
	if task.PendingTool != nil {
		_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "thought", Body: "Continuing…", Ephemeral: true})
	}
	if _, _, err := s.store.EnqueueAgentRun(context.WithoutCancel(ctx), store.AgentRunInput{WorkspaceKey: task.WorkspaceKey, SessionID: task.ID, Kind: kind, IdempotencyKey: "task:" + task.ID + ":" + strconv.FormatInt(version, 10), Principal: principal, Payload: payload}); err != nil {
		log.Printf("queue built-in agent run for %s: %v", task.ID, err)
		_, _ = s.store.AppendAgentActivity(ctx, task.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{ActorID: task.AppUserID, Type: "error", Body: "Agent execution failed. Check the provider configuration and retry."})
	}
	return true
}

// workerThreadElicitation is the elicitation a built-in session run posts when it stops for the person, as
// executeBuiltinApplication appends it: ask_user's question with its options (the turn's text before it), or the
// calls a connector set to always ask holds, as one approval with its held call (Approve / Reject).
func (s *server) workerThreadElicitation(ctx context.Context, run store.AgentRun, body string, elicitation *workerChatElicitation) (domain.AgentActivity, error) {
	activity := domain.AgentActivity{Type: "elicitation", Signal: "select"}
	switch elicitation.Kind {
	case "select":
		question, options := strings.TrimSpace(elicitation.Body), elicitation.Options
		call := domain.AgentToolCall{ID: elicitation.CallID, Name: askUserTool, Arguments: workerChatArguments(json.RawMessage(elicitation.Arguments))}
		if parsed, parsedOptions, err := parseAskUser(call); err == nil {
			question, options = parsed, parsedOptions.Options
		}
		choices := make([]any, 0, len(options))
		for _, option := range options {
			choices = append(choices, map[string]any{"label": option.Label, "value": option.Value})
		}
		activity.Body = strings.TrimSpace(strings.TrimSpace(body) + "\n\n" + question)
		activity.SignalMetadata = map[string]any{"options": choices}
	case "approval":
		approval := domain.AgentApproval{Kind: "connector"}
		for _, held := range elicitation.Calls {
			approval.Calls = append(approval.Calls, domain.AgentToolCall{ID: held.CallID, Name: agentToolBaseName(held.Name), Arguments: workerChatArguments(json.RawMessage(held.Arguments))})
		}
		if len(approval.Calls) == 0 {
			return activity, errors.New("an approval holds calls")
		}
		// Held connector calls are named by their connector and tool, as in-process.
		if tools, err := s.agentRunConnectorTools(ctx, s.agentRunGrantOf(run)); err == nil {
			ctx = context.WithValue(ctx, connectorToolsKey{}, tools)
		}
		pending := agentApprovalBatchCall(approval)
		activity.Body = strings.TrimSpace(strings.TrimSpace(body) + "\n\nAllow " + agentHeldCallNames(ctx, approval.Calls) + "?")
		metadata := map[string]any{"options": []any{map[string]any{"label": "Approve", "value": agentApprovalApprove}, map[string]any{"label": "Reject", "value": agentApprovalReject}}}
		if raw, err := json.Marshal(agentApprovalSummary(approval)); err == nil {
			var summary map[string]any
			if json.Unmarshal(raw, &summary) == nil {
				metadata["approval"] = summary
			}
		}
		activity.SignalMetadata, activity.ToolCall = metadata, &pending
	default:
		return activity, store.ErrAgentRunInput
	}
	return activity, nil
}

// agentRunGrantOf is the grant a run's own token carries.
func (s *server) agentRunGrantOf(run store.AgentRun) *agentRunGrant {
	principal, _ := decodeAgentRunPrincipal(run)
	return &agentRunGrant{run: run, principal: principal}
}

// workerRunBudgetMax is a worker run's tool-call budget (chat and built-in session runs); ok is false for runs
// without one.
func (s *server) workerRunBudgetMax(run store.AgentRun) (int, bool) {
	if info, ok := workerChatInfo(run); ok {
		return max(info.MaxCalls, 0), true
	}
	if info, ok := workerThreadInfo(run); ok {
		return max(info.MaxCalls, 0), true
	}
	return 0, false
}

// workerThreadBudgetTurn applies a built-in session run's tool budget at a model turn, as executeBuiltinApplication
// ends its tool turns: at the turn budget or once the call budget is spent the turn is offered no tools and the
// last tool result carries the budget nudge (its last word). It reports whether tools are withheld and the nudge.
func (s *server) workerThreadBudgetTurn(ctx context.Context, run store.AgentRun, info workerThreadRunInfo, round int) (withhold bool, nudge string) {
	maxTurns := info.MaxTurns
	if maxTurns <= 0 {
		maxTurns = s.agentMaxToolTurns()
	}
	s.updateWorkerChatBudget(ctx, run, func(budget *agentToolBudget, state *workerChatBudgetState) {
		withhold, nudge = false, ""
		if round > 0 {
			budget.endTurn()
		}
		if budget.stop == "" && round >= maxTurns {
			budget.stop = agentBudgetTurns
		}
		state.Turns = max(state.Turns, round+1)
		if budget.stop != "" {
			withhold = true
			if !state.Nudged {
				nudge, state.Nudged = "\n\n"+agentToolBudgetNudge(budget.stop, maxTurns, info.MaxCalls), true
			}
		}
	})
	return withhold, nudge
}

// workerThreadLastWord is a built-in session run's reply once its tool budget is spent, as executeBuiltinApplication
// gives it: the last turn's text, or Flow's summary of what was done and what is left.
func workerThreadLastWord(stopped, text string, maxTurns, maxCalls int, chinese bool) string {
	if reply := strings.TrimSpace(text); reply != "" {
		return reply
	}
	return agentToolBudgetFallback(stopped, maxTurns, maxCalls, chinese, -1, false)
}

// workerRunTextApproval reports whether a reply without tool calls asks the person to allow a tool the run is
// offered (agentTextApprovalRequest: Flow's tools and the run's connector tools).
func (s *server) workerRunTextApproval(ctx context.Context, run store.AgentRun, text string) bool {
	if tools, err := s.agentRunConnectorTools(ctx, s.agentRunGrantOf(run)); err == nil {
		ctx = context.WithValue(ctx, connectorToolsKey{}, tools)
	}
	return s.agentTextApprovalRequest(ctx, agentProviderTurn{Text: text})
}
