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

// workflowRunWrite is one MutateWorkflowRun call: its inputs and the event it
// publishes after the attempt that committed.
type workflowRunWrite struct {
	workspace, workflowID    string
	resourceType, resourceID string
	payload                  map[string]string
	webhook                  bool
	callback                 func(*domain.Bootstrap) (string, error)
	event                    domain.DomainEvent
	realtime                 json.RawMessage
	eventOwner               *ResourceRead
	written                  bool
}

func (w *workflowRunWrite) closeEvent() {
	if w.eventOwner != nil {
		w.eventOwner.Close()
		w.eventOwner = nil
	}
}

// MutateWorkflowRun retains the literal workflow callback. A failed action is
// part of its successful failed-run postimage: earlier actions, retry changes,
// the definition and the exact 500-run tail commit in one SQL transaction.
//
// The point attempt (workflowRunPointAttempt) reads and writes only what the
// run touches. The full-image attempt runs the few inputs it cannot reproduce
// exactly (errWorkflowRunFullImage).
func (s *SQLiteStore) MutateWorkflowRun(ctx context.Context, workspace, workflowID, resourceType, resourceID string, payload map[string]string, mutate func(*domain.Bootstrap) error) (err error) {
	if !s.BoundedMetadataEnabled() || mutate == nil || !metadataOutputOwnerPresent(ctx) {
		return ErrBoundedMetadataCoverage
	}
	ctx, trace := startMutationTrace(ctx)
	defer func() { trace.report(workspace, "workflow_run.executed", err) }()
	traceStart(ctx, "workflow")
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	ctx = trackMetadataWorkDemand(ctx)
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
	w := &workflowRunWrite{workspace: workspace, workflowID: workflowID, resourceType: resourceType, resourceID: resourceID, payload: payload}
	if s.webhookConfigured() {
		w.webhook, err = s.residentWebhookNeeded(ctx, workspace)
		if err != nil {
			return err
		}
	}
	defer w.closeEvent()
	ctx, _, w.callback = collectMutationAudit(ctx, workspace, func(data *domain.Bootstrap) (string, error) {
		return workflowID, mutate(data)
	})
	apply := func(ctx context.Context) error {
		w.closeEvent()
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
		err := s.workflowRunPointAttempt(ctx, w)
		if !errors.Is(err, errWorkflowRunFullImage) {
			return err
		}
		traceMark(ctx, "point-refused")
		workflowRunFullImageAttempts.Add(1)
		w.closeEvent()
		return s.workflowRunFullAttempt(ctx, w)
	}
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshWorkflowRunStreamConflict(ctx, workspace))
	if w.written {
		s.dropMetadataCache(ctx, workspace)
	}
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	s.publishMutation(ctx, workspace, w.event, w.realtime)
	return nil
}

// workflowRunFullAttempt runs the callback on the full image (every issue and
// content record) and persists it through the legacy record base. The caller
// holds the write lock.
func (s *SQLiteStore) workflowRunFullAttempt(ctx context.Context, w *workflowRunWrite) error {
	workspace, workflowID := w.workspace, w.workflowID
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
	attempt.inputs, err = s.loadWorkflowRunInputs(ctx, prepared.Source(), workflowID, w.resourceType, w.resourceID, nil)
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
	if err := attempt.admitCallback(ctx); err != nil {
		return err
	}
	if !registerMetadataOutputClose(ctx, attempt.Close) {
		return ErrBoundedMetadataCoverage
	}
	transferred = true
	if err := attempt.runCallback(ctx, w); err != nil {
		return err
	}
	callbackImage, err := attempt.inputs.image(ctx, nil, nil)
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
	payloadRaw, previous, err := s.workflowRunEvent(ctx, w, before, counted)
	if err != nil {
		return err
	}
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
	return s.workflowRunCommit(ctx, w, attempt, tx, stored, stamp, counted, payloadRaw, previous)
}

// admitCallback charges the callback's possible results: every action can
// prepend one issue and append one notification. The selected config strings
// are borrowed under the input owner.
func (r *workflowRunAttempt) admitCallback(ctx context.Context) error {
	data := &r.inputs.data
	actions := 0
	if len(data.WorkflowDefinitions) != 0 {
		actions = len(data.WorkflowDefinitions[0].Actions)
	}
	if uint64(actions) > uint64(metadataWorkBytes())/(16<<10) {
		return ErrMetadataWorkBudget
	}
	return r.owner.reserve(ctx, uint64(actions)*(16<<10)+64<<10)
}

// runCallback runs the literal callback on the selected inputs and holds its
// native results until the containing request or job releases them;
// temporary per-row encodings remain separately scoped.
func (r *workflowRunAttempt) runCallback(ctx context.Context, w *workflowRunWrite) error {
	data := &r.inputs.data
	if _, err := w.callback(data); err != nil {
		return err
	}
	data.FormerMembers, data.CollaborationUpdates, data.CollaborationWork = nil, nil, nil
	if err := r.lookups.check(); err != nil {
		return err
	}
	traceMark(ctx, "callback")
	for _, value := range []any{data.Issues, data.WorkflowRuns, data.Notifications} {
		cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(value), 0)
		if err != nil {
			return err
		}
		if err := r.owner.reserve(ctx, cost); err != nil {
			return err
		}
	}
	traceMark(ctx, "callback-admission")
	return nil
}

// workflowRunEvent plans the workflow_run.executed event: its payload, the
// definition's previous values for webhooks and the realtime entity.
func (s *SQLiteStore) workflowRunEvent(ctx context.Context, w *workflowRunWrite, before, after *metadataLogicalSource) (json.RawMessage, json.RawMessage, error) {
	payloadRaw, err := json.Marshal(w.payload)
	if err != nil {
		return nil, nil, err
	}
	plan, err := planMetadataAggregatePayload(ctx, metadataAggregateImage{Source: before},
		metadataAggregateImage{Source: after}, w.workflowID, w.webhook, payloadRaw)
	if err != nil {
		return nil, nil, err
	}
	w.eventOwner = &ResourceRead{store: s}
	if err := w.eventOwner.reserve(ctx, uint64(plan.RequiredBytes)); err != nil {
		return nil, nil, err
	}
	previous, entity, err := plan.Values(ctx)
	if err != nil {
		return nil, nil, err
	}
	w.event = domain.DomainEvent{ID: idgen.New("evt_"), Type: "workflow_run.executed", AggregateID: w.workflowID,
		Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
	w.realtime = enrichRealtimePayload(payloadRaw, entity, w.event.Type)
	traceMark(ctx, "payload")
	return payloadRaw, previous, nil
}

// workflowRunCommit persists the metadata image, account row and event, then
// commits and installs the image. Both attempts end here.
func (s *SQLiteStore) workflowRunCommit(ctx context.Context, w *workflowRunWrite, attempt *workflowRunAttempt, tx *sqlTx,
	stored *metadataLogicalSource, stamp copyStamp, target *metadataLogicalSource, payloadRaw, previous json.RawMessage) error {
	workspace := w.workspace
	persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"workflowDefinitions", "workflowRuns"})
	persistCtx = context.WithValue(persistCtx, workflowRunStreamKey{}, true)
	merged, err := s.persistLegacyMetadataImage(persistCtx, tx, workspace, stored, target)
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
		raw, err = json.Marshal(attempt.inputs.data.Viewer)
		if err != nil {
			return err
		}
	}
	if err := writeAccountMetadata(ctx, tx, workspace, raw); err != nil {
		return err
	}
	storedPrevious, err := storedEventPreviousValues(ctx, tx, workspace, w.event.Type, w.workflowID, previous)
	if err != nil {
		return err
	}
	if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: w.event.ID, Type: w.event.Type, AggregateID: w.workflowID,
		Payload: payloadRaw, PreviousValues: storedPrevious, CreatedAt: w.event.CreatedAt.Format(time.RFC3339Nano),
		WorkspaceID: stamp.workspaceID, WorkspaceKey: workspace}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	traceMark(ctx, "commit")
	w.written = true
	return publication.Install()
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
