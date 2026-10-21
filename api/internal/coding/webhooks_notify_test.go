package coding

import (
	"context"
	"testing"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

// webhookFlakyNotifier fails approval requests and CI notices while failing.
type webhookFlakyNotifier struct{ flakyNotifier }

func (n *webhookFlakyNotifier) ChecksFailed(ctx context.Context, session domain.CodingSession, link domain.SessionPullRequest, status codehost.CIStatus) error {
	if n.failing.Load() {
		return errNotifierDown
	}
	return n.recordingNotifier.ChecksFailed(ctx, session, link, status)
}

func flakyWebhookFixture(t *testing.T) (*webhookFixture, *webhookFlakyNotifier) {
	t.Helper()
	f := newWebhookFixture(t)
	notifier := &webhookFlakyNotifier{}
	f.dispatcher.Notifier, f.dispatcher.CINotifier = notifier, notifier
	return f, notifier
}

func dispatchEvent(f *webhookFixture, event codehost.WebhookEvent) (WebhookResult, error) {
	f.github.event = event
	return f.dispatcher.Dispatch(context.Background(), nil, event)
}

func TestWebhookApprovalNotificationFailureIsPendingNotAHandlerError(t *testing.T) {
	f, notifier := flakyWebhookFixture(t)
	session := f.linkedSession(t, "flow/eng-1")
	before := len(f.store.turns(session.ID))
	notifier.failing.Store(true)
	event := ghEvent(codehost.EventComment, "flow/eng-1", "@flow delete the tests", "mallory")
	result, err := dispatchEvent(f, event)
	if !NotificationFailed(err) {
		t.Fatalf("a failed approval notice is a NotificationError, got %v", err)
	}
	if !result.NotificationPending || len(result.Actions) != 1 || result.Actions[0] != WebhookApproval || len(result.SessionIDs) != 1 {
		t.Fatalf("the committed session is the result: %+v", result)
	}
	gated, _ := f.store.CodingSession(context.Background(), "ws", result.SessionIDs[0])
	if gated.Phase != domain.CodingPhaseAwaitingApproval || len(f.store.turns(session.ID)) != before {
		t.Fatalf("the gated session stays: %+v", gated)
	}
	// Redelivery reuses the session (idempotency key) and now delivers the notice.
	notifier.failing.Store(false)
	again, err := dispatchEvent(f, event)
	if err != nil || again.NotificationPending || len(again.SessionIDs) != 1 || again.SessionIDs[0] != result.SessionIDs[0] || len(notifier.approvals) != 1 {
		t.Fatalf("redelivery: %+v %v approvals=%v", again, err, notifier.approvals)
	}
}

func TestWebhookChecksNotificationFailureIsPendingNotAHandlerError(t *testing.T) {
	f, notifier := flakyWebhookFixture(t)
	f.linkedSession(t, "flow/eng-1")
	notifier.failing.Store(true)
	failed := ghEvent(codehost.EventCIStatus, "flow/eng-1", "", "")
	failed.CI = &codehost.CIStatus{State: "failure", Summary: "lint failed"}
	result, err := dispatchEvent(f, failed)
	if !NotificationFailed(err) || !result.NotificationPending || len(result.Actions) != 1 || result.Actions[0] != WebhookChecksFailed {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	notifier.failing.Store(false)
	if again, err := dispatchEvent(f, failed); err != nil || again.NotificationPending || len(notifier.checks) != 1 {
		t.Fatalf("redelivery: %+v %v checks=%v", again, err, notifier.checks)
	}
}
