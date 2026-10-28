package main

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP elicitation on worker runs. A connector may ask the person for input in the middle of a call (MCP
// elicitation/create). Flow is the connector's MCP client for worker runs too (callMCPRunConnector), so it answers
// the request the way the in-process chat agent does (requestAgentElicitation):
//
//   - only a run with a person to ask advertises elicitation to its connectors: a chat run acting as its user. A
//     thread, delegation, loop or background run does not, like the in-process thread and loop agents, so a
//     connector there gets no elicitation capability and proceeds (or fails) without asking;
//   - the question is held in this process for up to 5 minutes (the connector call waits; the worker gives
//     connector calls 390 s) and is answered only through POST /api/agent/sessions/{id}/elicitations/{eid}: the
//     chat session's owner, who must be the run's acting user (resolveAgentElicitation checks both). Form answers
//     go to the connector, never into the stored conversation;
//   - the worker learns of the question (and of its resolution) from
//     GET /internal/agent-runtime/runs/{id}/elicitations, a long poll it keeps while a connector call is in flight,
//     and shows it to the person as the in-process chat shows its elicitation part.
//
// Like the in-process chat (agentApprovals / agentElicitations), the pending question lives in the API process
// that serves the connector call: the answer and the worker's poll must reach that process.

// agentRunElicitationKeep is how many of a run's questions the log keeps.
const agentRunElicitationKeep = 20

// agentRunElicitationIdle is how long an untouched log is kept (a run that never completed).
const agentRunElicitationIdle = time.Hour

// agentRunElicitationItem is one question of a run as the worker sees it.
type agentRunElicitationItem struct {
	domain.AgentElicitation
	// Status is pending while the connector waits, completed once answered, error when cancelled or timed out.
	Status string `json:"status"`
	// AnswerPath is where the person answers (the in-process chat's route).
	AnswerPath string `json:"answerPath"`
}

type agentRunElicitationLog struct {
	// ask holds one question at a time, as a chat turn asks them.
	ask     sync.Mutex
	mu      sync.Mutex
	version int64
	items   []agentRunElicitationItem
	wake    chan struct{}
	touched time.Time
}

// agentRunElicitationLogs holds each run's questions (run id → *agentRunElicitationLog).
var agentRunElicitationLogs sync.Map

func agentRunElicitations(runID string) *agentRunElicitationLog {
	if value, ok := agentRunElicitationLogs.Load(runID); ok {
		return value.(*agentRunElicitationLog)
	}
	agentRunElicitationLogs.Range(func(key, value any) bool {
		entry := value.(*agentRunElicitationLog)
		entry.mu.Lock()
		stale := time.Since(entry.touched) > agentRunElicitationIdle
		entry.mu.Unlock()
		if stale {
			agentRunElicitationLogs.CompareAndDelete(key, entry)
		}
		return true
	})
	value, _ := agentRunElicitationLogs.LoadOrStore(runID, &agentRunElicitationLog{wake: make(chan struct{}), touched: time.Now()})
	return value.(*agentRunElicitationLog)
}

func (l *agentRunElicitationLog) record(part domain.AgentMessagePart) {
	if part.Elicitation == nil {
		return
	}
	item := agentRunElicitationItem{AgentElicitation: *part.Elicitation, Status: part.Status, AnswerPath: "/api/agent/sessions/" + part.Elicitation.SessionID + "/elicitations/" + part.Elicitation.ID}
	l.mu.Lock()
	defer l.mu.Unlock()
	if index := slices.IndexFunc(l.items, func(existing agentRunElicitationItem) bool { return existing.ID == item.ID }); index >= 0 {
		l.items[index] = item
	} else {
		l.items = append(l.items, item)
		if len(l.items) > agentRunElicitationKeep {
			l.items = slices.Clone(l.items[len(l.items)-agentRunElicitationKeep:])
		}
	}
	l.version++
	l.touched = time.Now()
	close(l.wake)
	l.wake = make(chan struct{})
}

func (l *agentRunElicitationLog) snapshot() (int64, []agentRunElicitationItem, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = time.Now()
	return l.version, slices.Clone(l.items), l.wake
}

// agentRunAsksPerson reports whether a run has a person to ask mid-call: a chat run acting as its user.
func agentRunAsksPerson(grant *agentRunGrant) bool {
	return grant.run.Kind == store.AgentRunKindChat && grant.principal.Mode == agentRunModeUser
}

// agentRunElicit is the connector elicitation handler of a run's connector calls; nil when the run has no person
// to ask (its connectors are then not offered elicitation, as for an in-process thread run).
func (s *server) agentRunElicit(grant *agentRunGrant) func(context.Context, applicationPolicy, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
	if !agentRunAsksPerson(grant) {
		return nil
	}
	log := agentRunElicitations(grant.run.ID)
	return func(ctx context.Context, item applicationPolicy, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		log.ask.Lock()
		defer log.ask.Unlock()
		// The MCP SDK hands the handler its connection's context, which a Stop does not end: the question also
		// waits on the run, so stopping the reply cancels it (as agentElicitationContext does in-process).
		ctx, cancel := s.agentRunStopContext(ctx, grant.run.ID)
		defer cancel()
		return s.requestAgentElicitation(ctx, grant.run.WorkspaceKey, grant.run.SessionID, grant.principal.UserID, item, params, func(part domain.AgentMessagePart, kind string) error {
			log.record(part)
			// The question is shown on the chat reply the run writes (agent_worker_chat.go).
			s.workerChatElicitationPart(context.WithoutCancel(ctx), grant.run, part, kind)
			return nil
		})
	}
}

// watchAgentRunElicitations answers the worker's long poll for a run's connector questions: the run's questions
// once its log moved past `after` (or at once with waitMs 0). `supported` is false for a run without a person to
// ask, which never has any.
func (s *server) watchAgentRunElicitations(w http.ResponseWriter, r *http.Request, _ agentRuntimeCaller, run store.AgentRun) {
	principal, err := decodeAgentRunPrincipal(run)
	if err != nil {
		writeError(w, http.StatusConflict, "the run has no valid principal")
		return
	}
	grant := &agentRunGrant{run: run, principal: principal}
	if !agentRunAsksPerson(grant) {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false, "version": 0, "items": []agentRunElicitationItem{}})
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	waitMs, _ := strconv.Atoi(r.URL.Query().Get("waitMs"))
	wait := min(time.Duration(max(waitMs, 0))*time.Millisecond, agentRuntimeMaxClaimWait)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	log := agentRunElicitations(run.ID)
	for {
		version, items, wake := log.snapshot()
		if version > after || wait == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"supported": true, "version": version, "items": items})
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
