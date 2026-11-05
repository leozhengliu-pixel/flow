package main

import (
	"context"
	"errors"
	"log/slog"

	"flow/api/internal/domain"
)

// Inbound diagnostics. Every callback the webhook answers leaves one
// structured log record: "chat callback rejected" (level WARN) with the check
// that refused it, or "chat callback handled" (level INFO) with its outcome:
// queued (reason: the command kind), ignored (reason: why no command), refused
// (a card click answered with an error toast; reason: why), challenge or
// link_preview. A direct message answered without the Flow Agent leaves a
// "chat direct message" record with its reason. Records name the request,
// connection, app, event type and platform message ID, never a token, key,
// signature, member, message text or other payload content.

// chatCallbackLog is what a callback's records say about it.
type chatCallbackLog struct {
	RequestID    string
	Provider     string
	ConnectionID string
	AppID        string
	EventType    string
	Format       string
	Encrypted    bool
	Scheme       string
}

func (d chatCallbackLog) attrs(extra ...any) []any {
	attrs := []any{"request_id", d.RequestID, "provider", d.Provider, "connection_id", d.ConnectionID, "app_id", d.AppID, "event_type", d.EventType, "format", d.Format, "encrypted", d.Encrypted}
	if d.Scheme != "" {
		attrs = append(attrs, "signature_scheme", d.Scheme)
	}
	return append(attrs, extra...)
}

// rejected records the check that refused a callback.
func (d chatCallbackLog) rejected(ctx context.Context, reason string) {
	slog.WarnContext(ctx, "chat callback rejected", d.attrs("reason", reason)...)
}

// handled records what an accepted callback became.
func (d chatCallbackLog) handled(ctx context.Context, outcome, reason string, extra ...any) {
	slog.InfoContext(ctx, "chat callback handled", d.attrs(append([]any{"outcome", outcome, "reason", reason}, extra...)...)...)
}

// chatEnvelopeLogAttrs are the message fields the records may carry: IDs and
// shapes, never content.
func chatEnvelopeLogAttrs(envelope chatWebhookEnvelope) []any {
	message := envelope.Event.Message
	if message.MessageID == "" && envelope.Event.Context.MessageID == "" {
		return nil
	}
	if message.MessageID == "" {
		return []any{"message_id", envelope.Event.Context.MessageID}
	}
	return []any{"message_id", message.MessageID, "chat_type", message.ChatType, "message_type", message.MessageType, "in_thread", message.RootID != "" && message.RootID != message.MessageID, "mentions", len(message.Mentions)}
}

// chatFailureKind classifies a failure for the logs without its text.
func chatFailureKind(err error) string {
	var operation *chatOperationError
	var refusal *chatMutationError
	var platform *chatPlatformError
	switch {
	case errors.As(err, &operation):
		return operation.Reason
	case errors.As(err, &refusal):
		return "flow_refusal"
	case errors.As(err, &platform):
		return "platform"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		return "timeout"
	}
	return "internal"
}

// chatLogDirectMessage records how a direct message was answered when it was
// not by the Flow Agent (or that it was handed to it).
func chatLogDirectMessage(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, outcome string, extra ...any) {
	slog.InfoContext(ctx, "chat direct message", append([]any{"provider", record.Provider, "connection_id", record.ConnectionID, "app_id", record.AppID, "job_id", job.ID, "outcome", outcome}, extra...)...)
}
