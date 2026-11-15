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

type legacyDiscussionAttempt struct {
	Owner      *ResourceRead
	Data       *domain.Bootstrap
	Stored     *metadataLogicalSource
	Before     *metadataLogicalSource
	NativeRole string
	inputs     *legacyDiscussionInputs
	lookups    *issueEditorLookups
	base       *legacyRecordBase
	ctx        context.Context
	workspace  string
	once       sync.Once
}

func (r *legacyDiscussionAttempt) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.Data != nil {
			*r.Data = domain.Bootstrap{}
			r.Data = nil
		}
		if r.lookups != nil {
			r.lookups.close()
			r.lookups = nil
		}
		if r.inputs != nil {
			r.inputs.Close()
			r.inputs = nil
		}
		if r.base != nil {
			r.base.Close()
			r.base = nil
		}
		if r.Before != nil {
			r.Before.Release()
			r.Before = nil
		}
		r.Owner.Close()
		r.Stored, r.ctx = nil, nil
	})
}

// Scope and occurrence validation is performed by MergeCallback immediately
// afterward, against the immutable prepared source.
func (r *legacyDiscussionAttempt) CheckCallback() error {
	if r == nil || r.Data == nil || r.inputs == nil || r.lookups == nil {
		return errMetadataLogicalClosed
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	return r.lookups.check()
}

func (r *legacyDiscussionAttempt) MergeCallback(ctx context.Context) (*metadataLogicalSource, error) {
	if err := r.CheckCallback(); err != nil {
		return nil, err
	}
	return r.inputs.MergeCallback(ctx)
}

func (r *legacyDiscussionAttempt) PersistExternal(ctx context.Context, tx *sqlTx, source *metadataLogicalSource) error {
	if r == nil || r.base == nil || r.inputs == nil {
		return errMetadataLogicalClosed
	}
	return r.base.Persist(ctx, tx, r.workspace, source)
}

// The literal count pass mutates existing label slices. Callback results may
// retain those slices, so update their selected native occurrences in place.
func (r *legacyDiscussionAttempt) applyCallbackCounts(ctx context.Context, source *metadataLogicalSource) error {
	if err := r.Owner.reserve(ctx, uint64(len(r.Data.Labels))*512+64<<10); err != nil {
		return err
	}
	wanted := make(map[string][]int, len(r.Data.Labels))
	for index := range r.Data.Labels {
		id := r.Data.Labels[index].ID
		wanted[id] = append(wanted[id], index)
	}
	labelIndex, _, _ := metadataLogicalField("labels")
	if err := source.fields[labelIndex].Children(ctx, func(_ reflect.Value, label metadataLogicalNode) error {
		work := &ResourceRead{store: r.Owner.store}
		defer work.Close()
		value, err := work.projectNode(ctx, label, "ID", "IssueCount")
		if err != nil {
			return err
		}
		item := value.Interface().(domain.IssueLabel)
		for _, index := range wanted[item.ID] {
			r.Data.Labels[index].IssueCount = item.IssueCount
		}
		return nil
	}); err != nil {
		return err
	}
	issueIndex, _, _ := metadataLogicalField("issues")
	selected := 0
	err := source.fields[issueIndex].Children(ctx, func(index reflect.Value, issue metadataLogicalNode) error {
		if selected == len(r.inputs.IssueOrdinals) || int(index.Int()) != r.inputs.IssueOrdinals[selected] {
			return nil
		}
		target := &r.Data.Issues[selected]
		labels, err := issueHotField(ctx, issue, "Labels")
		if err != nil {
			return err
		}
		seen := 0
		if err := labels.Children(ctx, func(index reflect.Value, label metadataLogicalNode) error {
			if int(index.Int()) != seen || seen >= len(target.Labels) {
				return ErrMetadataRecordQuery
			}
			value, err := r.Owner.node(ctx, label, false)
			if err != nil {
				return err
			}
			target.Labels[seen] = value.Interface().(domain.IssueLabel)
			seen++
			return nil
		}); err != nil {
			return err
		}
		if seen != len(target.Labels) {
			return ErrMetadataRecordQuery
		}
		selected++
		return nil
	})
	if err != nil {
		return err
	}
	if selected != len(r.inputs.IssueOrdinals) {
		return ErrMetadataRecordQuery
	}
	return ctx.Err()
}

// Each missing family uses a separate owned snapshot or borrows the caller's
// existing read snapshot. Returned frames retain no owned SQL transaction.
func (s *SQLiteStore) prepareLegacyDiscussionAttempt(ctx context.Context, workspace, issueID string, webhookEnabled bool) (_ *legacyDiscussionAttempt, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || issueID == "" {
		return nil, ErrBoundedMetadataCoverage
	}
	r := &legacyDiscussionAttempt{Owner: &ResourceRead{store: s}, ctx: ctx, workspace: workspace}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err := r.Owner.reserve(ctx, 128<<10); err != nil {
		return nil, err
	}
	r.Stored, err = s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return nil, err
	}
	r.Owner.source = r.Stored
	r.base, err = s.newLegacyRecordBase(ctx, r.Stored)
	if err != nil {
		return nil, err
	}
	current, err := cloneMetadataLogicalSnapshot(ctx, r.Stored)
	if err != nil {
		return nil, err
	}
	defer func() {
		if current != nil {
			current.Release()
		}
	}()
	capture := contextWithLegacyRecordBase(ctx, r.base)
	for _, family := range []struct{ field, kind string }{
		{"issues", "issue"},
		{"", "import"},
		{"activities", "activity"},
		{"comments", "comment"},
		{"notifications", "notification"},
		{"notificationDeliveries", "delivery"},
	} {
		if family.kind == "import" {
			frozen, err := s.prepareLegacyImportImage(capture, workspace, current)
			if err != nil {
				return nil, err
			}
			current.Release()
			current = frozen
			continue
		}
		index, _, _ := metadataLogicalField(family.field)
		missing, err := current.fields[index].isNil(capture)
		if err != nil {
			return nil, err
		}
		if !missing {
			continue
		}
		var tx *sqlTx
		release := func() {}
		if snapshot, ok := capture.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
			tx = snapshot.reader
		} else {
			tx, err = s.db.BeginTx(capture, metadataReadOptions(s.dialect))
			if err != nil {
				return nil, err
			}
			release = func() { _ = tx.Rollback() }
		}
		var frozen *metadataLogicalSource
		if family.kind == "issue" {
			frozen, err = s.prepareLegacyIssueImage(capture, tx, workspace, current)
		} else {
			frozen, err = s.prepareLegacyContentImage(capture, tx, workspace, current, family.kind)
		}
		// Transform has consumed every frame and raw-row receipt by now.
		release()
		if err != nil {
			return nil, err
		}
		current.Release()
		current = frozen
	}
	r.Before, current = current, nil
	next := r.Before
	if webhookEnabled {
		next, err = cloneMetadataLogicalSnapshot(ctx, r.Before)
	} else {
		err = next.Retain()
	}
	if err != nil {
		return nil, err
	}
	defer next.Release()
	display, err := s.refreshLegacyDisplayImage(ctx, next)
	if err != nil {
		return nil, err
	}
	defer display.Release()
	var actor *domain.User
	if value, ok := actorFromContext(ctx); ok {
		actor = &value
	}
	prepared, err := s.prepareLegacyActorImage(ctx, display, actor)
	if err != nil {
		return nil, err
	}
	defer prepared.Close()
	var role string
	var teams []string
	applyRole := false
	if actor != nil {
		index, _, _ := metadataLogicalField("workspace")
		workspaceValue, err := r.Owner.projectNode(ctx, prepared.Source().fields[index], "ID")
		if err != nil {
			return nil, err
		}
		role, teams, applyRole, err = s.boundedLegacyActorDirectory(ctx, workspaceValue.Interface().(domain.Workspace).ID, actor.ID, r.Owner)
		if err != nil {
			return nil, err
		}
	}
	if err := r.base.FingerprintLoaded(ctx, prepared.Source()); err != nil {
		return nil, err
	}
	r.inputs, err = s.loadLegacyDiscussionInputs(ctx, prepared.Source(), issueID)
	if err != nil {
		return nil, err
	}
	r.Data = r.inputs.Data
	r.NativeRole = r.Data.ViewerRole
	if applyRole {
		r.Data.ViewerRole = role
		r.Data.ViewerTeamIDs = teams
	}
	var target domain.Issue
	for _, issue := range r.Data.Issues {
		if issue.ID == issueID {
			target = issue
			break
		}
	}
	r.lookups, err = newLegacyIssueEditorLookups(ctx, r.inputs.input, workspace, r.Data.Workspace.ID, target)
	if err != nil {
		return nil, err
	}
	r.Data.FormerMembers = r.lookups.formerMembers
	r.Data.CollaborationUpdates = r.lookups.collaborationUpdates
	return r, nil
}

func boundedLegacyIssueEvent(event string) bool {
	return boundedIssueDiscussionEvent(event) || event == "issue.reaction_toggled"
}

func (s *SQLiteStore) mutateBoundedLegacyDiscussion(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	issueID := mutationAggregateID(ctx)
	if !s.BoundedMetadataEnabled() || !boundedLegacyIssueEvent(eventType) || issueID == "" ||
		mutate == nil || !metadataOutputOwnerPresent(ctx) || UsesIssueRecordMutations(ctx) {
		return ErrBoundedMetadataCoverage
	}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	webhookEnabled := false
	if s.webhookConfigured() {
		var err error
		webhookEnabled, err = s.boundedLegacyWebhookNeeded(ctx, workspace)
		if err != nil {
			return err
		}
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, metadataTimestampInputBytes)
	if err != nil {
		return err
	}
	defer release()
	ctx, finishInputs, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finishInputs()
	var committed domain.DomainEvent
	var realtime json.RawMessage
	written := false
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		traceLocked(ctx)
		defer traceUnlocked(ctx)
		traceStart(ctx, "full")
		if s.coordinator != nil {
			reload := context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
			if err := s.reloadResidentWorkspace(reload, workspace); err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
			}
			traceMark(ctx, "reload")
		}
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace %q: not found", workspace)
		}
		unbind, err := s.bindMetadataTimestampInputs(ctx)
		if err != nil {
			return err
		}
		defer unbind()
		attempt, err := s.prepareLegacyDiscussionAttempt(ctx, workspace, issueID, webhookEnabled)
		if err != nil {
			return err
		}
		attempt.inputs.issueReaction = eventType == "issue.reaction_toggled"
		transferred := false
		defer func() {
			if !transferred {
				attempt.Close()
			}
		}()
		// The fingerprint stage need not survive SQL commit/rollback, while
		// callback-captured native results survive until response release.
		defer attempt.base.Close()
		budget := &boundedDiscussionAttempt{owner: attempt.Owner}
		if err := budget.reserveCallback(ctx, *attempt.Data, payload); err != nil {
			return err
		}
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		traceMark(ctx, "load")
		aggregate, callbackErr := mutate(attempt.Data)
		attempt.Data.FormerMembers, attempt.Data.CollaborationUpdates = nil, nil
		attempt.Data.ViewerRole, attempt.Data.ViewerTeamIDs = attempt.NativeRole, nil
		traceMark(ctx, "mutate")
		if callbackErr != nil {
			return callbackErr
		}
		if aggregate != issueID {
			return fmt.Errorf("%w: legacy discussion replaced its aggregate", ErrBoundedMetadataCoverage)
		}
		if err := attempt.CheckCallback(); err != nil {
			return err
		}
		if err := budget.reserveContent(ctx, *attempt.Data, payload); err != nil {
			return err
		}
		callback, err := attempt.MergeCallback(ctx)
		if err != nil {
			return err
		}
		defer callback.Release()
		var previous json.RawMessage
		if webhookEnabled {
			plan, err := planMetadataAggregatePayload(ctx, metadataAggregateImage{Source: attempt.Before}, metadataAggregateImage{Source: callback}, aggregate, true, nil)
			if err != nil {
				return err
			}
			if err := attempt.Owner.reserve(ctx, uint64(plan.RequiredBytes)); err != nil {
				return err
			}
			previous, _, err = plan.Values(ctx)
			if err != nil {
				return err
			}
		}
		var progress *legacyProgressImage
		if eventType == "issue.reaction_toggled" {
			progress, err = s.prepareLegacyProgressImage(ctx, callback, time.Now().UTC())
			if err != nil {
				return err
			}
			defer progress.Close()
		}
		counted, err := s.refreshLegacyResourceCountsFromImage(ctx, callback)
		if err != nil {
			return err
		}
		defer counted.Release()
		if err := attempt.applyCallbackCounts(ctx, counted); err != nil {
			return err
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event := domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate,
			Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
		plan, err := planMetadataAggregatePayload(ctx, metadataAggregateImage{Source: attempt.Before}, metadataAggregateImage{Source: counted}, aggregate, false, payloadRaw)
		if err != nil {
			return err
		}
		if err := attempt.Owner.reserve(ctx, uint64(plan.RequiredBytes)); err != nil {
			return err
		}
		_, entity, err := plan.Values(ctx)
		if err != nil {
			return err
		}
		nextRealtime := enrichRealtimePayload(payloadRaw, entity, eventType)
		// The literal full writer opens its default transaction only after
		// callback, progress classification, count and realtime planning.
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
		baseStamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return err
		}
		if baseStamp.workspaceID == "" || baseStamp.workspaceID != attempt.Stored.Stamp().workspaceID {
			return ErrResourceAccessChanged
		}
		exact := attempt.Stored.Stamp() == baseStamp
		if err := attempt.base.LockForPersistence(ctx, tx, workspace, counted); err != nil {
			return err
		}
		if err := s.persistLegacyImportInputs(ctx, tx, workspace, counted); err != nil {
			return err
		}
		if err := attempt.PersistExternal(ctx, tx, counted); err != nil {
			return err
		}
		var progressPlan legacyMetadataProgress
		if progress != nil {
			progressPlan = progress
		}
		merged, err := s.persistLegacyMetadataImageWithProgress(ctx, tx, workspace, attempt.Stored, counted, progressPlan)
		if err != nil {
			return err
		}
		defer merged.Release()
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, baseStamp, exact, true)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		if err := budget.reserveValue(ctx, s.viewer); err != nil {
			return err
		}
		viewerRaw, _ := json.Marshal(s.viewer)
		if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
			index, _, _ := metadataLogicalField("viewer")
			viewer, err := attempt.Owner.node(ctx, merged.fields[index], false)
			if err != nil {
				return err
			}
			if err := budget.reserveValue(ctx, viewer.Interface()); err != nil {
				return err
			}
			viewerRaw, _ = json.Marshal(viewer.Interface())
		}
		if err := writeAccountMetadata(ctx, tx, workspace, viewerRaw); err != nil {
			return err
		}
		storedPrevious, err := storedEventPreviousValues(ctx, tx, workspace, event.Type, event.AggregateID, event.PreviousValues)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type,
			AggregateID: aggregate, Payload: payloadRaw, PreviousValues: storedPrevious,
			CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: baseStamp.workspaceID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		written = true
		if err := publication.Install(); err != nil {
			return err
		}
		committed, realtime = event, nextRealtime
		traceMark(ctx, "persist")
		return nil
	}
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if written {
		s.dropMetadataCache(ctx, workspace)
	}
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	s.publishMutation(ctx, workspace, committed, realtime)
	traceMark(ctx, "publish")
	return nil
}
