package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// A finished chat reply records how its run ended (domain.AgentReplyOutcome) and the activity logged with it says the
// same, so a reply that was saved after a failure, an interruption or a stop never reads as a run that succeeded.
// Both runtimes (in-process agent_stream.go, agent worker agent_worker_chat.go) and the live-reply sweeper set it.
//
//	outcome      activity type   activity status   message
//	completed    response        completed         the answer; without one, reason no_reply_text (the work ran)
//	                                               or empty_response (nothing came back), shown as nothing
//	failed       error           failed            the error part names the cause
//	interrupted  error           interrupted       the error part says it was interrupted
//	canceled     canceled        canceled          "Generation stopped" (the owner pressed Stop; the web
//	                                               keeps only the work, with no badge or Retry)
//
// An empty answer never fails a run that ended normally. Replies saved before outcomes existed keep no outcome; they
// are never rewritten.

const (
	agentOutcomeCompleted   = "completed"
	agentOutcomeFailed      = "failed"
	agentOutcomeInterrupted = "interrupted"
	agentOutcomeCanceled    = "canceled"
)

// Outcome reasons (machine-readable; the reply's error part carries the readable text).
const (
	// agentReasonNoReplyText qualifies a completed reply whose tool work ran but that wrote no answer.
	agentReasonNoReplyText = "no_reply_text"
	// agentReasonEmptyResponse qualifies a completed reply that came back with nothing (no answer and no work).
	agentReasonEmptyResponse = "empty_response"
	agentReasonOutputLimit   = "output_limit"
	agentReasonProviderError = "provider_error"
	agentReasonTimeout       = "timeout"
	agentReasonTextApproval  = "text_approval"
	// agentReasonRunFailed: the run failed outside the model call (setup, an approval that could not resume, a
	// worker run that ended failed).
	agentReasonRunFailed         = "run_failed"
	agentReasonWorkerUnavailable = "worker_unavailable"
	agentReasonUserStop          = "user_stop"
	// agentReasonReplyAbandoned: no server is writing the reply any more (a restart, a peer that went away).
	agentReasonReplyAbandoned = "reply_abandoned"
	agentReasonWorkerLost     = "worker_lost"
	agentReasonConnectionLost = "connection_lost"
)

// errAgentReplyStopped is the cancel cause of an in-process reply its owner stopped (POST …/stop). It is a
// context.Canceled, so every check for a canceled run still sees one.
var errAgentReplyStopped = fmt.Errorf("agent reply stopped by its owner: %w", context.Canceled)

// withAgentReplyFields scopes a metadata-only mutation that finishes replies to the chats and the activity log: a
// mutation scoped to agentSessions alone drops the activity it appends.
func withAgentReplyFields(ctx context.Context) context.Context {
	return store.WithMetadataFields(ctx, "agentSessions", "agentActivities")
}

type agentReplyRunKey struct{}

// withAgentRunContext keeps run (the reply's cancelable context) reachable from contexts derived from it, also
// through context.WithoutCancel, so a failure can tell a stop from another cancellation.
func withAgentRunContext(run context.Context) context.Context {
	return context.WithValue(run, agentReplyRunKey{}, run)
}

// agentReplyStoppedByOwner reports whether ctx belongs to a reply its owner stopped.
func agentReplyStoppedByOwner(ctx context.Context) bool {
	run, ok := ctx.Value(agentReplyRunKey{}).(context.Context)
	return ok && errors.Is(context.Cause(run), errAgentReplyStopped)
}

// agentTurnFailureReason names a failed model turn.
func agentTurnFailureReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return agentReasonTimeout
	}
	return agentReasonProviderError
}

// agentReplyDidWork reports whether a reply's tool calls ran (a reply that did its work and wrote no answer is not
// an empty response).
func agentReplyDidWork(parts []domain.AgentMessagePart) bool {
	for _, part := range parts {
		if part.ToolCall != nil && part.ToolCall.Status == "completed" && part.ToolCall.Error == "" {
			return true
		}
	}
	return false
}

// agentReplyOutcomeOf is a finished reply's outcome; replies without one (saved before outcomes existed) count as
// completed unless their error part says otherwise.
func agentReplyOutcomeOf(message domain.AgentMessage) domain.AgentReplyOutcome {
	if message.Outcome != nil && message.Outcome.Status != "" {
		return *message.Outcome
	}
	if text, ok := agentReplyErrorText(message); ok {
		switch text {
		case "Generation stopped":
			return domain.AgentReplyOutcome{Status: agentOutcomeCanceled, Reason: agentReasonUserStop}
		case agentReplyInterrupted:
			return domain.AgentReplyOutcome{Status: agentOutcomeInterrupted}
		}
		return domain.AgentReplyOutcome{Status: agentOutcomeFailed}
	}
	return domain.AgentReplyOutcome{Status: agentOutcomeCompleted}
}

// agentReplyErrorText is the reply's closing error part ("<message id>_error"), if any.
func agentReplyErrorText(message domain.AgentMessage) (string, bool) {
	for _, part := range message.Parts {
		if part.Type == "error" && part.ID == message.ID+"_error" {
			return part.Text, true
		}
	}
	return "", false
}

// agentReplyActivity is the activity logged when a reply finishes; its type and status follow the reply's outcome.
func agentReplyActivity(sessionID string, message domain.AgentMessage, metadata map[string]any) domain.AgentActivity {
	outcome := agentReplyOutcomeOf(message)
	kind, body := "response", message.Content
	switch outcome.Status {
	case agentOutcomeFailed, agentOutcomeInterrupted:
		kind = "error"
		if text, ok := agentReplyErrorText(message); ok {
			body = text
		}
	case agentOutcomeCanceled:
		kind, body = "canceled", "Generation stopped"
	}
	values := map[string]any{"durationMs": message.DurationMS, "partCount": len(message.Parts), "messageId": message.ID, "outcome": outcome.Status}
	if outcome.Reason != "" {
		values["reason"] = outcome.Reason
	}
	if kind != "response" && strings.TrimSpace(message.Content) != "" {
		// What the reply had written before it ended stays on the message.
		values["partialContent"] = true
	}
	for key, value := range metadata {
		values[key] = value
	}
	at := message.CreatedAt
	return domain.AgentActivity{ID: idgen.New("agent_activity_"), SessionID: sessionID, Type: kind, Status: outcome.Status, Body: body, Metadata: values, CreatedAt: at, UpdatedAt: at}
}
