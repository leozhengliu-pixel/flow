package main

import (
	"context"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// startBoundedLoopRun is startLoopRun on the installed loop run inputs: the
// first loop with the ID, the workspace's loops flag and the entity's label,
// with the loop.run_started stamp written by its closed store transaction.
// The guard, budget reservation, stamp, run record and worker or local
// handoff keep startLoopRun's order and failure unwinding.
func (s *server) startBoundedLoopRun(workspace, loopID string, trigger loopTrigger, actorID string) (domain.LoopRun, error) {
	read, ok, err := s.store.BeginLoopRunStartRead(context.Background(), workspace, loopID)
	if err != nil {
		return domain.LoopRun{}, err
	}
	if !ok {
		return domain.LoopRun{}, errNotFound
	}
	defer read.Close()
	data := read.Data
	workspace = data.Workspace.URLKey
	loop := loopByID(&data, loopID)
	if loop == nil {
		return domain.LoopRun{}, errNotFound
	}
	if err := s.loopRunnable(data, *loop); err != nil {
		return domain.LoopRun{}, err
	}
	guardKey := loopID + "|" + trigger.EntityID
	if !loopGuards.claim(guardKey, trigger.Kind == "event", time.Now()) {
		return domain.LoopRun{}, errConflict
	}
	now := time.Now().UTC()
	run := domain.LoopRun{ID: idgen.New("loop_run_"), LoopID: loopID, Status: "running", Trigger: trigger.Kind, EventType: trigger.EventType, EntityType: trigger.EntityType, EntityID: trigger.EntityID, ActorID: actorID, StartedAt: now}
	if trigger.EntityType == "issue" {
		if issue, err := s.store.IssueRecord(context.Background(), workspace, trigger.EntityID); err == nil {
			run.EntityIdentifier = issue.Identifier
		}
	} else if trigger.EntityID != "" {
		if err := read.LoadEntity(context.Background(), trigger.EntityType, trigger.EntityID); err != nil {
			loopGuards.release(guardKey)
			return domain.LoopRun{}, err
		}
		run.EntityIdentifier = loopEventEntityName(read.Data, loopEvent{EntityType: trigger.EntityType, EntityID: trigger.EntityID})
	}
	run.TriggerLabel = loopTriggerLabel(trigger, run.EntityIdentifier)
	run.TriggerReason, run.TriggerValue = trigger.Reason.Code, trigger.Reason.Value
	run.Occurrence = loopRunOccurrence(*loop, trigger, now)
	trigger.Occurrence = run.Occurrence
	if loop.WebSearch && !s.webSearchAvailable() {
		run.Notices = append(run.Notices, loopWebSearchUnavailable)
	}
	if contract, ok := loopStoredContract(*loop); ok {
		run.ExpectedOutputs = loopContractRequiredKinds(contract)
	}
	limits := s.loopLimits()
	registry := s.loopRunRegistry()
	onWorker := s.loopRunsOnWorker()
	var budgetErr error
	if onWorker {
		if s.workerLoopRunInFlight(context.Background(), workspace, loopID, trigger.EntityID) {
			loopGuards.release(guardKey)
			return domain.LoopRun{}, errConflict
		}
		budgetErr = s.reserveWorkerLoopRun(context.Background(), workspace, limits)
	} else {
		budgetErr = registry.reserve(workspace, limits)
	}
	stampCtx, err := read.MutationContext(context.Background())
	if err == nil {
		err = s.store.MutateLoopRunStart(stampCtx, workspace, loopID, map[string]any{"runId": run.ID, "trigger": trigger.Kind}, func(data *domain.Bootstrap) error {
			item := loopByID(data, loopID)
			if item == nil {
				return errNotFound
			}
			version := ensureLoopVersion(data, item)
			run.VersionID, run.Version = version.ID, version.Version
			item.LastRunAt = &now
			return nil
		})
	}
	if err != nil {
		if budgetErr == nil && !onWorker {
			registry.unreserve(workspace)
		}
		loopGuards.release(guardKey)
		return domain.LoopRun{}, err
	}
	if budgetErr != nil {
		defer loopGuards.release(guardKey)
		run.Status, run.FailureReason, run.Error, run.FinishedAt = "failed", "budget_exhausted", budgetErr.Error(), &now
		if err := s.store.CreateLoopRun(context.Background(), workspace, run, store.LoopRunLease{}); err != nil {
			return domain.LoopRun{}, err
		}
		s.publishLoopRunEvent(workspace, "loop_run.finished", run)
		return run, nil
	}
	if onWorker {
		defer loopGuards.release(guardKey)
		return s.queueLoopRunOnWorker(workspace, *loop, trigger, run, limits)
	}
	// The local run keeps the captured loop, so it keeps the loop's allowance.
	releaseLoop, err := read.ExecutionLease()
	if err != nil {
		registry.unreserve(workspace)
		loopGuards.release(guardKey)
		return domain.LoopRun{}, err
	}
	run.HeartbeatAt = &now
	work := s.registerLoopWork(workspace, run.ID, limits)
	if err := s.store.CreateLoopRun(context.Background(), workspace, run, store.LoopRunLease{Owner: s.loopInstanceID(), ExpiresAt: now.Add(limits.LeaseTTL)}); err != nil {
		releaseLoop()
		work.abandon()
		loopGuards.release(guardKey)
		return domain.LoopRun{}, err
	}
	current := *loop
	go func() {
		defer releaseLoop()
		defer loopGuards.release(guardKey)
		recorder := &loopRunRecorder{s: s, workspace: workspace, run: run}
		s.runLoopWork(work, workspace, limits, func(ctx context.Context) error {
			return s.executeLoopRun(ctx, workspace, current, trigger, recorder)
		}, func(ctx context.Context, runErr error) {
			s.finishLoopRun(ctx, workspace, current, recorder, runErr)
		})
	}()
	return run, nil
}
