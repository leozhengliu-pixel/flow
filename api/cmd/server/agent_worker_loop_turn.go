package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Worker diagnostics carry only the current turn; the API owns the recovery budget and event log.
type workerLoopModelTurn struct {
	Text       string                   `json:"text"`
	Reasoning  string                   `json:"reasoning"`
	Calls      []agentRunPromptToolCall `json:"calls"`
	StopReason string                   `json:"stopReason"`
	StopDetail string                   `json:"stopDetail"`
	Usage      *agentProviderUsage      `json:"usage"`
	DurationMS int64                    `json:"durationMs"`
	Events     int                      `json:"events"`
	Unreadable int                      `json:"unreadable"`
	Unknown    int                      `json:"unknown"`
	Bytes      int                      `json:"bytes"`
	Finished   bool                     `json:"finished"`
	Model      string                   `json:"model"`
	Protocol   string                   `json:"protocol"`
	Error      string                   `json:"error,omitempty"`
}

func (s *server) workerLoopModelGuard(ctx context.Context, run store.AgentRun, info workerLoopRunInfo, input workerRunGuardRequest) (workerRunGuardResponse, error) {
	response := workerRunGuardResponse{Enabled: true, DecisionRound: -1}
	limits := s.loopLimits()
	if info.MaxContextBytes > 0 {
		limits.MaxContextBytes = info.MaxContextBytes
	}
	if input.Op == "input" {
		if limits.MaxContextBytes > 0 && input.ContextBytes > limits.MaxContextBytes {
			response.Error = fmt.Sprintf("The run's conversation reached %d KB, over its %d KB context budget. Narrow the instructions or the tool queries (filters, smaller limits).", input.ContextBytes>>10, limits.MaxContextBytes>>10)
			s.updateWorkerLoopState(ctx, run, func(state *workerLoopState) { state.FailReason, state.FailMessage = "budget_exhausted", response.Error })
		}
		return response, nil
	}
	if input.ModelTurn == nil {
		return response, store.ErrAgentRunInput
	}
	m := input.ModelTurn
	turn := agentProviderTurn{Text: m.Text, Reasoning: m.Reasoning, StopReason: m.StopReason, StopDetail: m.StopDetail, Usage: m.Usage, Stream: agentStreamStats{Bytes: m.Bytes, Events: m.Events, Finished: m.Finished, Unreadable: m.Unreadable, Unrecognized: m.Unknown}}
	for _, call := range m.Calls {
		turn.ToolCalls = append(turn.ToolCalls, domain.AgentToolCall{ID: call.ID, Name: call.Name, Arguments: []byte(call.Arguments)})
	}
	reading := readLoopTurn(turn)
	rec, err := s.workerLoopRecorder(ctx, run, info)
	if err != nil {
		return response, err
	}
	state := s.workerLoopStateOf(ctx, run.ID)
	duration := time.Duration(m.DurationMS) * time.Millisecond
	continuation := ""
	if reading.outcome.unusable() && !state.Final && m.Error == "" {
		recovery := newLoopTurnRecovery(limits)
		recovery.seen, recovery.used, recovery.total, recovery.wait = state.RecoverySeen, state.RecoveryUsed, state.RecoveryTotal, state.RecoveryWait
		deadline := time.UnixMilli(info.Started).Add(time.Duration(info.TimeoutMS) * time.Millisecond)
		recoveryCtx, cancel := context.WithDeadline(ctx, deadline)
		nothingDone := strings.TrimSpace(state.Output) == "" && rec.toolCount() == 0 && rec.summary() == nil
		allowed := !(reading.outcome == loopTurnEmpty && nothingDone) && recovery.allow(recoveryCtx, reading.outcome, turn, duration)
		cancel()
		state.RecoverySeen, state.RecoveryUsed, state.RecoveryTotal, state.RecoveryWait = recovery.seen, recovery.used, recovery.total, recovery.wait
		if allowed {
			continuation = string(reading.outcome)
			messages := loopRecoveryMessages(reading, agentProviderTurn{Text: m.Text, Reasoning: m.Reasoning}, rec.answering)
			if len(messages) > 0 {
				state.RecoveryNudge = messages[len(messages)-1].Content
				if len(messages) > 1 {
					state.RecoveryNudge += "\n\nLast reply (cut off):\n" + messages[0].Content
				}
			} else {
				state.RecoveryNudge = loopRecoveryEmpty
			}
			// Suppress cut calls before the SDK can execute them. Complete calls in a cut batch are retried too.
			response.Handled = true
		} else {
			state.FailReason, state.FailMessage = loopUnusableEnding(reading, recovery, rec.toolCount() > 0)
			if reading.outcome == loopTurnEmpty && nothingDone {
				state.FailReason, state.FailMessage = "empty_response", "Flow Agent returned an empty response"
			}
			response.Error = state.FailMessage
		}
	}
	record := loopModelTurnRecord{Turn: input.Round + 1, Model: m.Model, Protocol: m.Protocol, Outcome: reading.outcome, Detail: reading.detail, StopReason: agentStopReasonText(m.StopReason), StopDetail: agentStopReasonText(m.StopDetail), DurationMs: m.DurationMS, TextChars: len([]rune(m.Text)), ReasoningChars: len([]rune(m.Reasoning)), ToolCalls: len(m.Calls), CutToolCalls: len(reading.cut), Usage: m.Usage, ResponseBytes: m.Bytes, StreamEvents: m.Events, StreamFinished: m.Finished, Continuation: continuation}
	if m.Error != "" {
		record.Outcome, record.Error = loopTurnFailed, "provider_error"
	}
	record.UnreadableEvents, record.UnknownEvents = m.Unreadable, m.Unknown
	rec.logEvent("model_turn", record)
	state.Draft = ""
	if len(m.Calls) > 0 && !reading.outcome.unusable() && strings.TrimSpace(m.Text) != "" {
		state.Output = strings.TrimSpace(state.Output + "\n\n" + m.Text)
	}
	s.updateWorkerLoopState(ctx, run, func(stored *workerLoopState) { state.Sequence = stored.Sequence; *stored = state })
	rec.save(true)
	return response, nil
}

// Chat's coalescing poster numbers these posts and delivers them serially, including retries with the same seq.
func (s *server) postWorkerLoopActivity(w http.ResponseWriter, r *http.Request, run store.AgentRun, info workerLoopRunInfo) {
	var input struct {
		Seq  int    `json:"seq"`
		Type string `json:"type"`
		Body string `json:"body"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Seq <= 0 {
		writeError(w, http.StatusBadRequest, "seq must be positive")
		return
	}
	rec, err := s.workerLoopRecorder(r.Context(), run, info)
	if err != nil {
		writeError(w, http.StatusConflict, "the loop run is unavailable")
		return
	}
	if !workerLoopRecorderRunning(rec) {
		writeJSON(w, http.StatusOK, map[string]bool{"settled": true})
		return
	}
	publish := false
	now := time.Now().UnixMilli()
	state := s.updateWorkerLoopState(r.Context(), run, func(state *workerLoopState) {
		publish = false
		if input.Seq <= state.Sequence {
			return
		}
		state.Sequence = input.Seq
		if input.Type == "text.delta" && !state.Finished {
			state.Draft = truncateLoopToolOutput(state.Draft+input.Body, loopRunOutputLimit)
			if now-state.OutputSavedAt >= time.Second.Milliseconds() {
				publish, state.OutputSavedAt = true, now
			}
		}
	})
	if publish {
		rec.setOutputNow(strings.TrimSpace(state.Output + "\n\n" + state.Draft))
		rec.save(true)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"settled": false})
}
