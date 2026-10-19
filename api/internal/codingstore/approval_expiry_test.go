package codingstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// The session reconciler is the one owner of the approval expiry; the
// ApprovalExpired hook (track L's notification) fires exactly once, after
// the committed transition, however often and by however many replicas the
// session is reconciled afterwards.
func TestApprovalExpiryNotifiesOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	envs := fakeEnvs{envs: []domain.CodingEnvironment{{ID: "env1", Name: "App", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app", DefaultBranch: "main"}}}
	h.svc = coding.NewService(coding.Options{Config: h.cfg, Store: h.store, Anchors: h.store, Workspaces: harnessWorkspaces{h}, Environments: envs, Hosts: map[string]codehost.Host{domain.CodeHostGitHub: h.host}, Now: clock})
	var notified []domain.CodingSession
	h.svc.ApprovalExpired = func(_ context.Context, session domain.CodingSession) {
		notified = append(notified, session)
	}

	session, created, err := h.svc.Start(ctx, coding.StartInput{WorkspaceKey: workspace, Source: domain.CodingSourceExternal, Prompt: "From email", CreatorID: "user1", RequireApproval: true, IdempotencyKey: "approval-expiry"})
	if err != nil || !created || session.Phase != domain.CodingPhaseAwaitingApproval {
		t.Fatalf("start: %+v %v %v", session, created, err)
	}
	reconcileAll := func(owners ...string) {
		for _, owner := range owners {
			h.exec(`UPDATE coding_sessions SET next_reconcile_at=0 WHERE id=? AND lease_until=0`, session.ID)
			h.reconciler(owner).RunOnce(ctx)
		}
	}

	// Before the deadline nothing happens.
	reconcileAll("a")
	if got := h.session(session.ID); got.Phase != domain.CodingPhaseAwaitingApproval || len(notified) != 0 {
		t.Fatalf("before the deadline: %s notified=%d", got.Phase, len(notified))
	}

	// Past the deadline the reconciler ends the session and notifies once.
	mu.Lock()
	now = now.Add(h.cfg.Timeouts.Approval + time.Second)
	mu.Unlock()
	reconcileAll("a", "b", "a", "b")
	got := h.session(session.ID)
	if got.Phase != domain.CodingPhaseDone || got.ErrorCode != domain.CodingErrApprovalExpired || got.State != domain.CodingSessionError {
		t.Fatalf("expired session: %s %s %s", got.Phase, got.ErrorCode, got.State)
	}
	if len(notified) != 1 || notified[0].ID != session.ID || notified[0].ErrorCode != domain.CodingErrApprovalExpired {
		t.Fatalf("notified = %+v", notified)
	}

	// A late decision is refused and does not notify again.
	if _, err := h.svc.Approve(ctx, workspace, session.ID, "user2", false); err == nil {
		t.Fatal("approving an expired session must fail")
	}
	reconcileAll("a")
	if len(notified) != 1 {
		t.Fatalf("notified again: %d", len(notified))
	}
}

// Other endings of an awaitingApproval session (a rejection) are not expiry.
func TestApprovalRejectionDoesNotNotifyExpiry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	calls := 0
	h.svc.ApprovalExpired = func(context.Context, domain.CodingSession) { calls++ }
	session, _, err := h.svc.Start(ctx, coding.StartInput{WorkspaceKey: workspace, Source: domain.CodingSourceExternal, Prompt: "Reject me", CreatorID: "user1", RequireApproval: true, IdempotencyKey: "approval-reject"})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := h.svc.Approve(ctx, workspace, session.ID, "user2", true)
	if err != nil || rejected.Phase != domain.CodingPhaseDone {
		t.Fatalf("reject: %+v %v", rejected, err)
	}
	if calls != 0 {
		t.Fatalf("expiry hook called on rejection: %d", calls)
	}
}
