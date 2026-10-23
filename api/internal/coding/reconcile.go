package coding

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// The session reconciler (design §5.1). Every interval, and when woken, it
// leases due sessions (LeaseCodingSessions: FOR UPDATE SKIP LOCKED on
// PostgreSQL and MySQL, a CAS on SQLite), runs Step on each, executes the
// idempotent actions, writes the result with a version CAS and releases the
// lease with the next reconcile time. It only writes the desired state of
// coding_sandboxes rows and reads their observed state; the sandbox
// reconciler (track A) talks to the provider. Nothing is kept in memory
// between passes, so any replica can pick a session up after a restart.

// Reconciler drives sessions.
type Reconciler struct {
	Service *Service
	// Owner is this process's lease owner name.
	Owner string
	// Meter bills session sandboxes' runtime (nil skips billing).
	Meter        *Meter
	Lease        time.Duration
	Limit        int
	ProcessStart time.Time
}

// Run reconciles until ctx ends.
func (r *Reconciler) Run(ctx context.Context) {
	if r.ProcessStart.IsZero() {
		r.ProcessStart = r.Service.now()
	}
	ticker := time.NewTicker(r.Service.step.ReconcileInterval)
	defer ticker.Stop()
	for {
		r.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.Service.wakeCh:
		}
	}
}

// RunOnce leases and reconciles the due sessions once; it returns how many it handled.
func (r *Reconciler) RunOnce(ctx context.Context) int {
	started := time.Now()
	lease, limit := r.Lease, r.Limit
	if lease <= 0 {
		lease = 30 * time.Second
	}
	if limit <= 0 {
		limit = 50
	}
	sessions, err := r.Service.store.LeaseCodingSessions(ctx, r.Owner, lease, limit)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("coding session lease failed", "error", err)
		}
		return 0
	}
	for _, session := range sessions {
		next := r.reconcile(ctx, session)
		if err := r.Service.store.ReleaseCodingSession(ctx, r.Owner, session.ID, next); err != nil && !errors.Is(err, ErrLeaseLost) {
			slog.Warn("coding session release failed", "session", session.ID, "error", err)
		}
	}
	r.Service.metrics.reconciled(ctx, time.Since(started))
	return len(sessions)
}

// reconcile handles one leased session and returns its next reconcile time.
func (r *Reconciler) reconcile(ctx context.Context, session domain.CodingSession) time.Time {
	svc := r.Service
	now := svc.now().UTC()
	retry := now.Add(svc.step.ReconcileInterval)
	sandboxes, err := svc.store.CodingSandboxes(ctx, session.WorkspaceKey, session.ID)
	if err != nil {
		slog.Warn("coding session sandboxes", "session", session.ID, "error", err)
		return retry
	}
	r.bill(ctx, session, sandboxes)
	obs := Observation{Now: now, ProcessStart: r.ProcessStart, ProviderEnabled: svc.ready(), Sandboxes: sandboxes}
	if session.Phase == domain.CodingPhaseQueued {
		workspace, total, err := svc.store.CountActiveCodingSessions(ctx, session.WorkspaceKey)
		if err != nil {
			return retry
		}
		obs.ActiveInWorkspace, obs.ActiveTotal = workspace, total
		if svc.guard != nil {
			obs.SpendErr = svc.guard.Check(ctx, session.WorkspaceKey, session.CreatorID, session.LoopID)
			if obs.SpendErr != nil && !errors.Is(obs.SpendErr, ErrQuotaExceeded) {
				slog.Warn("coding spend check failed", "session", session.ID, "error", obs.SpendErr)
				return retry
			}
		}
	}
	if session.Phase == domain.CodingPhaseIdle || session.Phase == domain.CodingPhaseAwaitingUser {
		if obs.PendingTurns, err = svc.store.PendingCodingTurns(ctx, session.ID); err != nil {
			return retry
		}
	}
	decision := svc.step.Step(cloneSession(session), obs)
	var selection []domain.CodingEnvironment
	if decision.Has(ActProvision) {
		outcome := svc.provision(ctx, decision.Session, sandboxes)
		selection = outcome.Selection
		decision.Actions = withoutAction(decision.Actions, ActProvision)
		svc.step.AfterProvision(&decision, sandboxes, outcome, now)
	}
	if decision.Has(ActPublish) {
		outcome := svc.publish(ctx, decision.Session)
		decision.Actions = withoutAction(decision.Actions, ActPublish)
		svc.step.AfterPublish(&decision, sandboxes, outcome, now)
	}
	svc.runSandboxActions(ctx, decision)
	saved, err := svc.commit(ctx, session, decision.Session)
	if errors.Is(err, ErrVersionConflict) {
		// A runtime call changed the session meanwhile; look again at once.
		return now
	}
	if err != nil {
		slog.Warn("coding session update failed", "session", session.ID, "error", err)
		return retry
	}
	if len(selection) > 0 && saved.Phase == domain.CodingPhaseAwaitingUser {
		svc.askForEnvironment(ctx, saved, selection)
	}
	if saved.Phase == domain.CodingPhaseDone {
		return now // not leased again: done is excluded from leasing
	}
	next := decision.NextIn
	if next <= 0 {
		next = svc.step.ReconcileInterval
	}
	return now.Add(next)
}

func (r *Reconciler) bill(ctx context.Context, session domain.CodingSession, sandboxes []sandbox.Record) {
	if r.Meter == nil {
		return
	}
	for _, record := range sandboxes {
		if err := r.Meter.BillSandbox(ctx, record, session.CreatorID, session.LoopID); err != nil {
			slog.Warn("coding runtime billing failed", "sandbox", record.ID, "error", err)
		}
	}
}

func withoutAction(actions []Action, kind ActionKind) []Action {
	result := actions[:0:0]
	for _, action := range actions {
		if action.Kind != kind {
			result = append(result, action)
		}
	}
	return result
}

// askForEnvironment posts the repository selection elicitation (the reference select signal).
func (s *Service) askForEnvironment(ctx context.Context, session domain.CodingSession, envs []domain.CodingEnvironment) {
	options := []map[string]any{}
	metadata := map[string]any{}
	for _, env := range envs {
		option := map[string]any{"value": env.ID, "label": env.Name + " (" + env.Repository + ")"}
		if env.ID == session.PreferredEnvironmentID {
			// The trigger's ranking had a clear leader (track L): preselect it.
			option["recommended"] = true
			metadata["selected"] = env.ID
		}
		options = append(options, option)
	}
	metadata["options"] = options
	s.anchorActivity(ctx, session, domain.AgentActivity{Type: "elicitation", Body: "Which repository should I work in?", Signal: "select", SignalMetadata: metadata})
}
