package coding

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// flakyNotifier fails every notification while failing is set.
type flakyNotifier struct {
	recordingNotifier
	failing atomic.Bool
}

var errNotifierDown = errors.New("notifier down")

func (n *flakyNotifier) ApprovalRequested(ctx context.Context, session domain.CodingSession, request TriggerStartRequest) error {
	if n.failing.Load() {
		return errNotifierDown
	}
	return n.recordingNotifier.ApprovalRequested(ctx, session, request)
}

func (n *flakyNotifier) ApprovalResolved(ctx context.Context, session domain.CodingSession, decision, userID string) error {
	if n.failing.Load() {
		return errNotifierDown
	}
	return n.recordingNotifier.ApprovalResolved(ctx, session, decision, userID)
}

func flakyTriggers(store *fakeTriggerStore, notifier *flakyNotifier) *Triggers {
	triggers := newTestTriggers(store, &notifier.recordingNotifier)
	triggers.Notifier = notifier
	return triggers
}

// serviceApprover mimics Service.Approve: one CAS transition out of
// awaitingApproval, ErrConflict for every later call.
func serviceApprover(store *fakeTriggerStore, transitions *atomic.Int32) func(context.Context, string, string, string, bool) (domain.CodingSession, error) {
	return func(ctx context.Context, workspaceKey, sessionID, userID string, reject bool) (domain.CodingSession, error) {
		return store.UpdateCodingSession(ctx, workspaceKey, sessionID, func(session *domain.CodingSession) error {
			if session.Phase != domain.CodingPhaseAwaitingApproval {
				return ErrConflict
			}
			transitions.Add(1)
			session.ApprovedBy = userID
			if reject {
				session.ApprovalState, session.Phase, session.State = ApprovalRejected, domain.CodingPhaseDone, domain.CodingSessionStale
				return nil
			}
			session.ApprovalState, session.Phase, session.State = ApprovalApproved, domain.CodingPhaseQueued, domain.CodingSessionPending
			return nil
		})
	}
}

// A notification that fails after the session was created reports the
// created session (not a failed start); retrying the trigger repairs the
// notification without a second session.
func TestStartNotificationFailureKeepsTheCreatedSession(t *testing.T) {
	ctx := context.Background()
	store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
	notifier := &flakyNotifier{}
	triggers := flakyTriggers(store, notifier)
	trigger := issueTrigger(&IssueContext{ID: "i-ext", Title: "Customer: export broken", Origin: IntegrationOrigin("email")}, "fix the export")
	trigger.AnchorTaskID, trigger.IdempotencyKey = "task-delegation", "delegation:task-delegation"

	notifier.failing.Store(true)
	result, err := triggers.StartForIssue(ctx, trigger)
	var notifyErr *NotificationError
	if !errors.As(err, &notifyErr) || !NotificationFailed(err) {
		t.Fatalf("start with a failing notifier: %v", err)
	}
	if result.Session == nil || !result.Created || !result.NotificationPending || result.Session.Phase != domain.CodingPhaseAwaitingApproval {
		t.Fatalf("result = %+v", result)
	}
	if TriggerErrorCode(err) != "" {
		t.Fatalf("a notification failure must not look like a refused start: %v", err)
	}

	notifier.failing.Store(false)
	retry, err := triggers.StartForIssue(ctx, trigger)
	if err != nil || retry.Session == nil || retry.Session.ID != result.Session.ID || retry.Created || retry.NotificationPending {
		t.Fatalf("retry = %+v %v", retry, err)
	}
	if len(store.sessions) != 1 || len(notifier.approvals) != 1 || len(store.turns(result.Session.ID)) != 1 {
		t.Fatalf("sessions=%d approvals=%v turns=%d", len(store.sessions), notifier.approvals, len(store.turns(result.Session.ID)))
	}
}

// An approval that committed reports the decided session when its
// notification fails; repeating the decision repairs the notification.
// Concurrent approvals make one transition and every caller sees success.
func TestDecideApprovalAfterCommit(t *testing.T) {
	ctx := context.Background()
	for _, withApprover := range []bool{false, true} {
		name := map[bool]string{false: "store", true: "session core"}[withApprover]
		t.Run(name+": notification failure after approve", func(t *testing.T) {
			store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
			notifier := &flakyNotifier{}
			triggers := flakyTriggers(store, notifier)
			var transitions atomic.Int32
			if withApprover {
				triggers.Approver = serviceApprover(store, &transitions)
			}
			session := startExternal(t, triggers, nil)
			notifier.failing.Store(true)
			approved, err := triggers.DecideApproval(ctx, "ws", session.ID, "u2", true, DecisionApprove)
			if !NotificationFailed(err) || approved.Phase != domain.CodingPhaseQueued || approved.ApprovedBy != "u2" {
				t.Fatalf("approve with a failing notifier: %+v %v", approved, err)
			}
			if stored, _ := store.CodingSession(ctx, "ws", session.ID); stored.Phase != domain.CodingPhaseQueued {
				t.Fatalf("the approval did not persist: %+v", stored)
			}
			notifier.failing.Store(false)
			again, err := triggers.DecideApproval(ctx, "ws", session.ID, "u2", true, DecisionApprove)
			if err != nil || again.Phase != domain.CodingPhaseQueued || again.ApprovedBy != "u2" {
				t.Fatalf("retried approve: %+v %v", again, err)
			}
			if len(notifier.resolved) != 1 || notifier.resolved[0] != session.ID+":approved" {
				t.Fatalf("resolved = %v", notifier.resolved)
			}
			if withApprover && transitions.Load() != 1 {
				t.Fatalf("transitions = %d", transitions.Load())
			}
		})
	}
	t.Run("expired request with a failing notifier", func(t *testing.T) {
		store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
		notifier := &flakyNotifier{}
		triggers := flakyTriggers(store, notifier)
		session := startExternal(t, triggers, nil)
		triggers.Config.Timeouts.Approval = time.Minute
		triggers.Now = func() time.Time { return fixedNow.Add(time.Hour) }
		notifier.failing.Store(true)
		expired, err := triggers.DecideApproval(ctx, "ws", session.ID, "u2", true, DecisionApprove)
		if TriggerErrorCode(err) != domain.CodingErrApprovalExpired || expired.ErrorCode != domain.CodingErrApprovalExpired {
			t.Fatalf("expired = %+v %v", expired, err)
		}
	})
	t.Run("concurrent approvals", func(t *testing.T) {
		store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
		notifier := &flakyNotifier{}
		triggers := flakyTriggers(store, notifier)
		var transitions atomic.Int32
		approve := serviceApprover(store, &transitions)
		errs := make([]error, 8)
		// Every member read the session while it still waited for approval
		// before any of them approves.
		var arrived sync.WaitGroup
		arrived.Add(len(errs))
		triggers.Approver = func(ctx context.Context, workspaceKey, sessionID, userID string, reject bool) (domain.CodingSession, error) {
			arrived.Done()
			arrived.Wait()
			return approve(ctx, workspaceKey, sessionID, userID, reject)
		}
		session := startExternal(t, triggers, nil)
		var wg sync.WaitGroup
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = triggers.DecideApproval(ctx, "ws", session.ID, "member"+string(rune('a'+i)), true, DecisionApprove)
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("approval %d: %v", i, err)
			}
		}
		stored, _ := store.CodingSession(ctx, "ws", session.ID)
		if transitions.Load() != 1 || stored.Phase != domain.CodingPhaseQueued || stored.ApprovedBy == "" {
			t.Fatalf("transitions=%d session=%+v", transitions.Load(), stored)
		}
		// A conflicting decision after the approval is still refused.
		triggers.Approver = approve
		if _, err := triggers.DecideApproval(ctx, "ws", session.ID, "u3", true, DecisionReject); TriggerErrorCode(err) != domain.CodingErrPermissionDenied {
			t.Fatalf("late reject: %v", err)
		}
	})
}
