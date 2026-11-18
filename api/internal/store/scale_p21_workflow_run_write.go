package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type workflowRunAttempt struct {
	owner   *ResourceRead
	inputs  *workflowRunInputs
	lookups *issueEditorLookups
	once    sync.Once
}

func (r *workflowRunAttempt) Close() {
	if r != nil {
		r.once.Do(func() {
			if r.lookups != nil {
				r.lookups.close()
				r.lookups = nil
			}
			if r.inputs != nil {
				r.inputs.close()
				r.inputs = nil
			}
			r.owner.Close()
		})
	}
}

// MutateWorkflowRun retains the literal workflow callback. A failed action is
// part of its successful failed-run postimage: earlier actions, retry changes,
// the definition and the exact 500-run tail commit in one SQL transaction.
func (s *SQLiteStore) MutateWorkflowRun(ctx context.Context, workspace, workflowID, resourceType, resourceID string, payload map[string]string, mutate func(*domain.Bootstrap) error) (err error) {
	if !s.BoundedMetadataEnabled() || mutate == nil || !metadataOutputOwnerPresent(ctx) {
		return ErrBoundedMetadataCoverage
	}
	ctx, trace := startMutationTrace(ctx)
	defer func() { trace.report(workspace, "workflow_run.executed", err) }()
	traceStart(ctx, "workflow")
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	ctx, finish, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	webhook := false
	if s.webhookConfigured() {
		webhook, err = s.residentWebhookNeeded(ctx, workspace)
		if err != nil {
			return err
		}
	}
	var event domain.DomainEvent
	var realtime json.RawMessage
	var eventOwner *ResourceRead
	defer func() {
		if eventOwner != nil {
			eventOwner.Close()
		}
	}()
	ctx, _, callback := collectMutationAudit(ctx, workspace, func(data *domain.Bootstrap) (string, error) {
		return workflowID, mutate(data)
	})
	written := false
	apply := func(ctx context.Context) error {
		if eventOwner != nil {
			eventOwner.Close()
			eventOwner = nil
		}
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		traceLocked(ctx)
		defer traceUnlocked(ctx)
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
				return err
			}
		}
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace %q: not found", workspace)
		}
		unbind, err := s.bindMetadataTimestampInputs(ctx)
		if err != nil {
			return err
		}
		defer unbind()
		attempt := &workflowRunAttempt{owner: &ResourceRead{store: s}}
		transferred := false
		defer func() {
			if !transferred {
				attempt.Close()
			}
		}()
		if err := attempt.owner.reserve(ctx, 256<<10); err != nil {
			return err
		}
		stored, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		defer stored.Release()
		base, err := s.newLegacyRecordBase(ctx, stored)
		if err != nil {
			return err
		}
		defer base.Close()
		before, err := s.workflowRunFullImage(contextWithLegacyRecordBase(ctx, base), workspace, stored)
		if err != nil {
			return err
		}
		defer before.Release()
		display, err := s.refreshLegacyDisplayImage(ctx, before)
		if err != nil {
			return err
		}
		defer display.Release()
		var actor *domain.User
		if value, found := actorFromContext(ctx); found {
			actor = &value
		}
		prepared, err := s.prepareLegacyActorImage(ctx, display, actor)
		if err != nil {
			return err
		}
		defer prepared.Close()
		if err := base.FingerprintLoaded(ctx, prepared.Source()); err != nil {
			return err
		}
		traceMark(ctx, "image")
		attempt.inputs, err = s.loadWorkflowRunInputs(ctx, prepared.Source(), workflowID, resourceType, resourceID)
		if err != nil {
			return err
		}
		data := &attempt.inputs.data
		attempt.lookups, err = newLegacyIssueEditorLookups(ctx, attempt.inputs.issueHotInputs, workspace, data.Workspace.ID, domain.Issue{})
		if err != nil {
			return err
		}
		attempt.lookups.install(data)
		traceMark(ctx, "inputs")
		// Every action can prepend one issue and append one notification.
		// The selected config strings are borrowed under the input owner.
		actions := 0
		if len(data.WorkflowDefinitions) != 0 {
			actions = len(data.WorkflowDefinitions[0].Actions)
		}
		if uint64(actions) > metadataWorkBytes/(16<<10) {
			return ErrMetadataWorkBudget
		}
		if err := attempt.owner.reserve(ctx, uint64(actions)*(16<<10)+64<<10); err != nil {
			return err
		}
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		if _, err := callback(data); err != nil {
			return err
		}
		data.FormerMembers, data.CollaborationUpdates, data.CollaborationWork = nil, nil, nil
		if err := attempt.lookups.check(); err != nil {
			return err
		}
		traceMark(ctx, "callback")
		// Hold all callback-native results until the containing request/job
		// releases them; temporary per-row encodings remain separately scoped.
		for _, value := range []any{data.Issues, data.WorkflowRuns, data.Notifications} {
			cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(value), 0)
			if err != nil {
				return err
			}
			if err := attempt.owner.reserve(ctx, cost); err != nil {
				return err
			}
		}
		traceMark(ctx, "callback-admission")
		callbackImage, err := attempt.inputs.image(ctx)
		if err != nil {
			return err
		}
		defer callbackImage.Release()
		traceMark(ctx, "callback-image")
		counted, err := s.refreshLegacyResourceCountsFromImage(ctx, callbackImage)
		if err != nil {
			return err
		}
		defer counted.Release()
		traceMark(ctx, "counts")
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		plan, err := planMetadataAggregatePayload(ctx, metadataAggregateImage{Source: before},
			metadataAggregateImage{Source: counted}, workflowID, webhook, payloadRaw)
		if err != nil {
			return err
		}
		eventOwner = &ResourceRead{store: s}
		if err := eventOwner.reserve(ctx, uint64(plan.RequiredBytes)); err != nil {
			return err
		}
		previous, entity, err := plan.Values(ctx)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: "workflow_run.executed", AggregateID: workflowID,
			Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
		realtime = enrichRealtimePayload(payloadRaw, entity, event.Type)
		traceMark(ctx, "payload")
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				return err
			}
		}
		stamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return err
		}
		if stamp.workspaceID != stored.Stamp().workspaceID {
			return ErrResourceAccessChanged
		}
		if err := base.LockForPersistence(ctx, tx, workspace, counted); err != nil {
			return err
		}
		traceMark(ctx, "record-lock")
		if err := s.persistLegacyImportInputs(ctx, tx, workspace, counted); err != nil {
			return err
		}
		traceMark(ctx, "imports")
		if err := base.Persist(ctx, tx, workspace, counted); err != nil {
			return err
		}
		traceMark(ctx, "records")
		persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"workflowDefinitions", "workflowRuns"})
		persistCtx = context.WithValue(persistCtx, workflowRunStreamKey{}, true)
		merged, err := s.persistLegacyMetadataImage(persistCtx, tx, workspace, stored, counted)
		if err != nil {
			return err
		}
		defer merged.Release()
		traceMark(ctx, "metadata")
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, stamp, stored.Stamp() == stamp, true)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		s.mu.RLock()
		viewer := s.viewer
		s.mu.RUnlock()
		viewerWork, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(viewer), 0)
		if err != nil {
			return err
		}
		if err := attempt.owner.reserve(ctx, uint64(viewerWork)); err != nil {
			return err
		}
		raw, err := json.Marshal(viewer)
		if err != nil {
			return err
		}
		if len(raw) == 0 || string(raw) == "{}" {
			raw, err = json.Marshal(data.Viewer)
			if err != nil {
				return err
			}
		}
		if err := writeAccountMetadata(ctx, tx, workspace, raw); err != nil {
			return err
		}
		storedPrevious, err := storedEventPreviousValues(ctx, tx, workspace, event.Type, workflowID, previous)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: workflowID,
			Payload: payloadRaw, PreviousValues: storedPrevious, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano),
			WorkspaceID: stamp.workspaceID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		traceMark(ctx, "commit")
		written = true
		return publication.Install()
	}
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshWorkflowRunStreamConflict(ctx, workspace))
	if written {
		s.dropMetadataCache(ctx, workspace)
	}
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	s.publishMutation(ctx, workspace, event, realtime)
	return nil
}

// Reuse the full writer's frozen physical row receipts. Only its selected
// callback values become Go objects; unselected records remain framed.
func (s *SQLiteStore) workflowRunFullImage(ctx context.Context, workspace string, source *metadataLogicalSource) (_ *metadataLogicalSource, err error) {
	current, err := cloneMetadataLogicalSnapshot(ctx, source)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			current.Release()
		}
	}()
	for _, family := range []struct{ field, kind string }{
		{"issues", "issue"}, {"", "import"}, {"activities", "activity"}, {"comments", "comment"},
		{"notifications", "notification"}, {"notificationDeliveries", "delivery"},
	} {
		if family.kind == "import" {
			next, err := s.prepareLegacyImportImage(ctx, workspace, current)
			if err != nil {
				return nil, err
			}
			current.Release()
			current = next
			continue
		}
		index, _, _ := metadataLogicalField(family.field)
		missing, err := current.fields[index].isNil(ctx)
		if err != nil {
			return nil, err
		}
		if !missing {
			continue
		}
		var tx *sqlTx
		release := func() {}
		if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
			tx = snapshot.reader
		} else {
			tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
			if err != nil {
				return nil, err
			}
			release = func() { _ = tx.Rollback() }
		}
		var next *metadataLogicalSource
		if family.kind == "issue" {
			next, err = s.prepareLegacyIssueImage(ctx, tx, workspace, current)
		} else {
			next, err = s.prepareLegacyContentImage(ctx, tx, workspace, current, family.kind)
		}
		release()
		if err != nil {
			return nil, err
		}
		current.Release()
		current = next
	}
	return current, nil
}
