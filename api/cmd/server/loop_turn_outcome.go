package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// Loop turn outcomes: what a model turn the run cannot act on was (cut off at the output limit, reasoning only,
// empty, a stream Flow could not parse, a tool call written as text), the bounded continuation it gets, the
// accurate failure when the continuations run out, and the per-turn diagnostics kept in the run's event log.

type loopTurnOutcome string

const (
	loopTurnToolCalls     loopTurnOutcome = "tool_calls"
	loopTurnAnswer        loopTurnOutcome = "answer"
	loopTurnPromise       loopTurnOutcome = "promise"
	loopTurnTruncated     loopTurnOutcome = "truncated"
	loopTurnReasoningOnly loopTurnOutcome = "reasoning_only"
	loopTurnEmpty         loopTurnOutcome = "empty"
	loopTurnUnreadable    loopTurnOutcome = "unreadable"
	loopTurnToolCallText  loopTurnOutcome = "tool_call_text"
	loopTurnFiltered      loopTurnOutcome = "filtered"
	loopTurnFailed        loopTurnOutcome = "request_failed"
)

// unusable reports whether the run cannot act on a turn with this outcome.
func (outcome loopTurnOutcome) unusable() bool {
	switch outcome {
	case loopTurnTruncated, loopTurnReasoningOnly, loopTurnEmpty, loopTurnUnreadable, loopTurnToolCallText, loopTurnFiltered:
		return true
	}
	return false
}

// loopTurnReading is what a model turn was, as the run reads it.
type loopTurnReading struct {
	outcome loopTurnOutcome
	// detail says what was wrong with an unreadable turn.
	detail string
	// cut marks the tool calls (by position) whose arguments were cut off before they were complete; they are
	// answered with an error and never run.
	cut map[int]bool
}

// loopToolCallMarkers are how models write a tool call as text when the provider did not turn it into one.
var loopToolCallMarkers = []string{"<｜tool▁call", "<｜DSML｜", "<|tool_call", "<tool_call>", "<function_calls>", "<function_call>", "<invoke name="}

// readLoopTurn classifies a model turn after its tool calls were settled (a final turn has none).
func readLoopTurn(turn agentProviderTurn) loopTurnReading {
	reading := loopTurnReading{}
	if len(turn.ToolCalls) > 0 {
		reading.outcome = loopTurnToolCalls
		if turn.truncated() || !turn.Stream.Finished {
			for index, call := range turn.ToolCalls {
				if arguments := strings.TrimSpace(string(call.Arguments)); arguments != "" && !json.Valid([]byte(arguments)) {
					if reading.cut == nil {
						reading.cut = map[int]bool{}
					}
					reading.cut[index] = true
				}
			}
		}
		if len(reading.cut) > 0 {
			reading.outcome = loopTurnTruncated
			if !turn.truncated() {
				reading.outcome, reading.detail = loopTurnUnreadable, "the stream ended in the middle of a tool call"
			}
		}
		return reading
	}
	text := strings.TrimSpace(turn.Text)
	reasoning := strings.TrimSpace(turn.Reasoning) != "" || turn.Usage != nil && turn.Usage.ReasoningTokens > 0
	stop := strings.ToLower(turn.StopReason)
	stream := turn.Stream
	switch {
	case turn.filtered():
		reading.outcome = loopTurnFiltered
	case turn.truncated():
		reading.outcome = loopTurnTruncated
	case text != "" && loopTextHasToolCallMarkup(text):
		reading.outcome = loopTurnToolCallText
	case text != "" && loopTextPromisesMoreWork(text):
		reading.outcome = loopTurnPromise
	case text != "":
		reading.outcome = loopTurnAnswer
	case stop == "tool_calls" || stop == "tool_use" || stop == "function_call":
		reading.outcome, reading.detail = loopTurnUnreadable, fmt.Sprintf("the provider said the model called tools (stop reason %s), but no tool call could be read", agentStopReasonLabel(turn.StopReason))
	case stream.Unreadable > 0:
		reading.outcome, reading.detail = loopTurnUnreadable, fmt.Sprintf("%d of %d stream events were not valid JSON", stream.Unreadable, stream.Events)
	case stream.Unrecognized > 0 && !reasoning:
		reading.outcome, reading.detail = loopTurnUnreadable, fmt.Sprintf("%d of %d stream events were in a shape Flow does not read", stream.Unrecognized, stream.Events)
	case stream.Events == 0 && stream.Bytes > 0:
		reading.outcome, reading.detail = loopTurnUnreadable, fmt.Sprintf("the %d-byte response held no stream events", stream.Bytes)
	case !stream.Finished && stream.Events > 0:
		reading.outcome, reading.detail = loopTurnUnreadable, "the stream ended before the provider finished the response"
	case reasoning:
		reading.outcome = loopTurnReasoningOnly
	default:
		reading.outcome = loopTurnEmpty
	}
	return reading
}

func loopTextHasToolCallMarkup(text string) bool {
	for _, marker := range loopToolCallMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// Continuation budget for unusable turns: one targeted continuation per cause (two for output truncation, which
// a shorter retry often fixes), at most three per run, and none once unusable turns have cost loopRecoveryMaxWait
// of model time or the run is about to time out.
var loopRecoveryPerCause = map[loopTurnOutcome]int{loopTurnTruncated: 2, loopTurnReasoningOnly: 1, loopTurnEmpty: 1, loopTurnUnreadable: 1, loopTurnToolCallText: 1}

const (
	loopRecoveryTotal        = 3
	loopRecoveryMinRemaining = 30 * time.Second
	// loopRecoveryEchoBytes is how much of a cut-off reply is sent back so the model can continue it.
	loopRecoveryEchoBytes = 2 << 10
)

var loopRecoveryMaxWait = 3 * time.Minute

// loopTurnRecovery tracks a run's unusable turns and the continuations they got.
type loopTurnRecovery struct {
	seen  map[loopTurnOutcome]int
	used  map[loopTurnOutcome]int
	total int
	wait  time.Duration
	// lastStop is the stop reason of the latest unusable turn, for the failure message.
	lastStop string
	// minRemaining is the run time a continuation needs left (see newLoopTurnRecovery).
	minRemaining time.Duration
}

// newLoopTurnRecovery is the continuation budget of a run with these limits: no continuation starts in the last
// fifth of the run's time limit (at most loopRecoveryMinRemaining).
func newLoopTurnRecovery(limits loopRunLimits) *loopTurnRecovery {
	return &loopTurnRecovery{minRemaining: min(loopRecoveryMinRemaining, limits.Timeout/5)}
}

// allow records an unusable turn and reports whether it gets a continuation.
func (recovery *loopTurnRecovery) allow(ctx context.Context, outcome loopTurnOutcome, turn agentProviderTurn, wait time.Duration) bool {
	if recovery.seen == nil {
		recovery.seen, recovery.used = map[loopTurnOutcome]int{}, map[loopTurnOutcome]int{}
	}
	recovery.seen[outcome]++
	recovery.wait += wait
	recovery.lastStop = turn.StopReason
	if recovery.used[outcome] >= loopRecoveryPerCause[outcome] || recovery.total >= loopRecoveryTotal || recovery.wait >= loopRecoveryMaxWait || ctx.Err() != nil {
		return false
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < recovery.minRemaining {
		return false
	}
	recovery.used[outcome]++
	recovery.total++
	return true
}

const (
	loopRecoveryTruncatedText = "Your last reply was cut off at the output limit. Continue from where it stopped without repeating what you already wrote, and keep it shorter: summarize instead of quoting, and write long documents in parts (create the document, then add the rest with patch). Do not repeat changes that already succeeded."
	loopRecoveryTruncated     = "Your last reply used the whole output limit before it produced an answer or a tool call, so nothing happened. Think less and take the next step now with a tool call. Do not repeat changes that already succeeded."
	loopRecoveryReasoningOnly = "Your last reply contained only reasoning, with no answer and no tool call, so nothing happened. Act on it now: call the tool for the next step. Keep your reasoning brief. Do not repeat changes that already succeeded."
	loopRecoveryEmpty         = "Your last reply was empty: no text and no tool call. Continue from the tool results above: call the tool for the next step. Do not repeat changes that already succeeded."
	loopRecoveryToolCallText  = "Your last reply wrote a tool call as text, which Flow cannot run. Call the tool through the tool-calling interface instead, with JSON arguments. Do not repeat changes that already succeeded."
	loopRecoveryRunFinish     = " If everything the instructions ask for is done, call finish_run with your report instead."
	loopRecoveryReplyFinish   = " If everything is done, answer in plain text instead."
	loopCutToolCallResult     = "This call was cut off at the model's output limit before its arguments were complete, so it was not run. Send it again with less content: write long documents in parts (create the document, then add the rest with patch)."
	loopUnfinishedToolCall    = "The model's response ended before this call's arguments were complete, so it was not run. Send it again."
)

// loopRecoveryMessages are what the conversation gets before the continuation of an unusable turn. A turn with
// tool calls continues through their results (the cut ones answered with loopCutToolCallResult); an unreadable
// stream is asked again as it was.
func loopRecoveryMessages(reading loopTurnReading, turn agentProviderTurn, answering bool) []agentProviderMessage {
	if len(turn.ToolCalls) > 0 || reading.outcome == loopTurnUnreadable {
		return nil
	}
	finish := loopRecoveryRunFinish
	if answering {
		finish = loopRecoveryReplyFinish
	}
	switch reading.outcome {
	case loopTurnTruncated:
		text := strings.TrimSpace(turn.Text)
		if text == "" {
			return []agentProviderMessage{{Role: "user", Content: loopRecoveryTruncated + finish}}
		}
		if len(text) > loopRecoveryEchoBytes {
			text = "[…] " + strings.ToValidUTF8(text[len(text)-loopRecoveryEchoBytes:], "")
		}
		return []agentProviderMessage{{Role: "assistant", Content: text}, {Role: "user", Content: loopRecoveryTruncatedText + finish}}
	case loopTurnReasoningOnly:
		return []agentProviderMessage{{Role: "user", Content: loopRecoveryReasoningOnly + finish}}
	case loopTurnToolCallText:
		return []agentProviderMessage{{Role: "user", Content: loopRecoveryToolCallText + finish}}
	}
	return []agentProviderMessage{{Role: "user", Content: loopRecoveryEmpty + finish}}
}

// loopUnusableEnding is the failure reason and message of a run that stopped on an unusable turn. The messages
// are fixed templates the web app translates (FAILURE_MESSAGES in web/src/components/loops/loop-run-labels.ts).
func loopUnusableEnding(reading loopTurnReading, recovery *loopTurnRecovery, afterTools bool) (string, string) {
	count := max(recovery.seen[reading.outcome], 1)
	stop := agentStopReasonLabel(recovery.lastStop)
	switch reading.outcome {
	case loopTurnTruncated:
		return "output_truncated", fmt.Sprintf("Model turns cut off at the output limit: %d (stop reason %s). The run stopped before it finished.", count, stop)
	case loopTurnReasoningOnly:
		return "reasoning_only", fmt.Sprintf("Model turns with only reasoning and no answer or tool call: %d (stop reason %s).", count, stop)
	case loopTurnUnreadable:
		return "stream_error", "The model stream could not be parsed: " + reading.detail
	case loopTurnToolCallText:
		return "stream_error", fmt.Sprintf("Model turns that wrote a tool call as text, which Flow cannot run: %d.", count)
	case loopTurnFiltered:
		return "provider_error", fmt.Sprintf("The model provider withheld the output (stop reason %s).", stop)
	}
	if afterTools {
		return "empty_response", fmt.Sprintf("Model turns with no text and no tool call after the run's tool calls: %d (stop reason %s).", count, stop)
	}
	return "empty_response", fmt.Sprintf("Model turns with no text and no tool call: %d (stop reason %s).", count, stop)
}

// endUnusableTurn ends the work on an unusable turn whose continuations ran out. Work that already succeeded is
// kept; a run that did nothing yet fails, otherwise it is saved as unfinished for review.
func (rec *loopRunRecorder) endUnusableTurn(reading loopTurnReading, recovery *loopTurnRecovery, turn agentProviderTurn, output string, nothingDone bool) error {
	if text := strings.TrimSpace(turn.Text); reading.outcome == loopTurnTruncated && text != "" && len(turn.ToolCalls) == 0 {
		// The cut-off reply is what the model had written; the failure reason says it is incomplete.
		output = strings.TrimSpace(output + "\n\n" + text)
	}
	rec.setOutputNow(strings.TrimSpace(output))
	if reading.outcome == loopTurnEmpty && nothingDone {
		return &loopRunError{reason: "empty_response", message: "Flow Agent returned an empty response"}
	}
	reason, message := loopUnusableEnding(reading, recovery, rec.toolCount() > 0)
	if nothingDone {
		return &loopRunError{reason: reason, message: message}
	}
	rec.endUnfinished(reason, message, "")
	return nil
}

// loopModelTurnRecord is one model turn in the run's event log ("model_turn"): sizes, counts and the provider's
// stop reason, never the text, the reasoning or the tool arguments.
type loopModelTurnRecord struct {
	Turn             int                 `json:"turn"`
	Model            string              `json:"model"`
	Protocol         string              `json:"protocol"`
	Outcome          loopTurnOutcome     `json:"outcome"`
	Detail           string              `json:"detail,omitempty"`
	StopReason       string              `json:"stopReason,omitempty"`
	StopDetail       string              `json:"stopDetail,omitempty"`
	DurationMs       int64               `json:"durationMs"`
	TextChars        int                 `json:"textChars"`
	ReasoningChars   int                 `json:"reasoningChars"`
	ToolCalls        int                 `json:"toolCalls"`
	CutToolCalls     int                 `json:"cutToolCalls,omitempty"`
	Usage            *agentProviderUsage `json:"usage,omitempty"`
	ResponseBytes    int                 `json:"responseBytes"`
	StreamEvents     int                 `json:"streamEvents"`
	UnreadableEvents int                 `json:"unreadableEvents,omitempty"`
	UnknownEvents    int                 `json:"unknownEvents,omitempty"`
	StreamFinished   bool                `json:"streamFinished"`
	Retries          int                 `json:"retries,omitempty"`
	// Error is the failure class of a request that failed (provider_error, provider_timeout, stream_error).
	Error string `json:"error,omitempty"`
	// Continuation names the continuation the run sent after this turn.
	Continuation string `json:"continuation,omitempty"`
}

// recordModelTurn logs a model turn in the run's event log and the server log.
func (rec *loopRunRecorder) recordModelTurn(index int, turn agentProviderTurn, reading loopTurnReading, duration time.Duration, retries int, requestErr error, continuation string) {
	record := loopModelTurnRecord{
		Turn: index + 1, Outcome: reading.outcome, Detail: reading.detail,
		StopReason: agentStopReasonText(turn.StopReason), StopDetail: agentStopReasonText(turn.StopDetail),
		DurationMs: duration.Milliseconds(), TextChars: len([]rune(turn.Text)), ReasoningChars: len([]rune(turn.Reasoning)),
		ToolCalls: len(turn.ToolCalls), CutToolCalls: len(reading.cut), Usage: turn.Usage,
		ResponseBytes: turn.Stream.Bytes, StreamEvents: turn.Stream.Events, UnreadableEvents: turn.Stream.Unreadable, UnknownEvents: turn.Stream.Unrecognized, StreamFinished: turn.Stream.Finished,
		Retries: retries, Continuation: continuation,
	}
	if rec.s != nil {
		record.Model, record.Protocol = rec.s.agent.Model, firstNonEmpty(rec.s.agent.Protocol, "openai-chat-completions")
	}
	if requestErr != nil {
		record.Outcome = loopTurnFailed
		var runErr *loopRunError
		if errors.As(loopProviderFailure(requestErr), &runErr) {
			record.Error = runErr.reason
		}
	}
	rec.logEvent("model_turn", record)
	usage := agentProviderUsage{}
	if turn.Usage != nil {
		usage = *turn.Usage
	}
	log.Printf("Loop model turn workspace=%s run=%s turn=%d model=%s outcome=%s stop=%s stop_detail=%s duration_ms=%d text_chars=%d reasoning_chars=%d tool_calls=%d cut_tool_calls=%d input_tokens=%d output_tokens=%d reasoning_tokens=%d cached_tokens=%d response_bytes=%d events=%d unreadable=%d unknown=%d finished=%t retries=%d error=%s continuation=%s",
		rec.workspace, rec.run.ID, record.Turn, record.Model, record.Outcome, firstNonEmpty(record.StopReason, "none"), record.StopDetail, record.DurationMs, record.TextChars, record.ReasoningChars, record.ToolCalls, record.CutToolCalls,
		usage.InputTokens, usage.OutputTokens, usage.ReasoningTokens, usage.CachedTokens, record.ResponseBytes, record.StreamEvents, record.UnreadableEvents, record.UnknownEvents, record.StreamFinished, record.Retries, record.Error, record.Continuation)
}

// agentStopReasonText is a provider stop reason fit for the logs (see agentStopReasonLabel), empty when there was none.
func agentStopReasonText(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return agentStopReasonLabel(reason)
}
