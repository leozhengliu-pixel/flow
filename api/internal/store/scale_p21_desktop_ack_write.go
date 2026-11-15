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

type desktopNotificationAcknowledgementKey struct{}

// ContextWithDesktopNotificationAcknowledgement selects only the desktop-ack
// callback contract: delivery predicates and its four assigned fields.
func ContextWithDesktopNotificationAcknowledgement(ctx context.Context) context.Context {
	return context.WithValue(ctx, desktopNotificationAcknowledgementKey{}, true)
}

func desktopNotificationAcknowledgement(ctx context.Context) bool {
	enabled, _ := ctx.Value(desktopNotificationAcknowledgementKey{}).(bool)
	return enabled
}

type legacyDesktopAckAttempt struct {
	owner                *ResourceRead
	base                 *legacyRecordBase
	stored, before, next *metadataLogicalSource
	data                 domain.Bootstrap
	original             domain.Bootstrap
	ordinals             []int
	nativeRole           string
	inputs               *issueHotInputs
	once                 sync.Once
}

func (r *legacyDesktopAckAttempt) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.data, r.original = domain.Bootstrap{}, domain.Bootstrap{}
		r.ordinals = nil
		if r.inputs != nil {
			r.inputs.close()
		}
		if r.before != nil {
			r.before.Release()
		}
		if r.base != nil {
			r.base.Close()
		}
		r.owner.Close()
		r.stored, r.before, r.next, r.inputs = nil, nil, nil, nil
	})
}

func (s *SQLiteStore) prepareLegacyDesktopAck(ctx context.Context, workspace, viewer string, payload any, webhook bool) (_ *legacyDesktopAckAttempt, err error) {
	r := &legacyDesktopAckAttempt{owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err := r.owner.reserve(ctx, 128<<10); err != nil {
		return nil, err
	}
	r.stored, err = s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return nil, err
	}
	r.owner.source = r.stored
	r.base, err = s.newLegacyRecordBase(ctx, r.stored)
	if err != nil {
		return nil, err
	}
	current, err := cloneMetadataLogicalSnapshot(ctx, r.stored)
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
		{"issues", "issue"}, {"", "import"}, {"activities", "activity"},
		{"comments", "comment"}, {"notifications", "notification"}, {"notificationDeliveries", "delivery"},
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
		release()
		if err != nil {
			return nil, err
		}
		current.Release()
		current = frozen
	}
	r.before, current = current, nil
	next := r.before
	if webhook {
		next, err = cloneMetadataLogicalSnapshot(ctx, r.before)
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
	if value, found := actorFromContext(ctx); found {
		actor = &value
	}
	prepared, err := s.prepareLegacyActorImage(ctx, display, actor)
	if err != nil {
		return nil, err
	}
	defer prepared.Close()
	if err := prepared.Source().Retain(); err != nil {
		return nil, err
	}
	r.next = prepared.Source()
	r.inputs = &issueHotInputs{store: s, source: r.next}
	for _, name := range []string{"workspace", "viewer", "viewerRole"} {
		index, _, _ := metadataLogicalField(name)
		value, err := r.owner.node(ctx, r.next.fields[index], false)
		if err != nil {
			return nil, err
		}
		reflect.ValueOf(&r.data).Elem().Field(index).Set(value)
	}
	r.nativeRole = r.data.ViewerRole
	if actor != nil {
		role, teams, apply, err := s.boundedLegacyActorDirectory(ctx, r.data.Workspace.ID, actor.ID, r.owner)
		if err != nil {
			return nil, err
		}
		if apply {
			r.data.ViewerRole, r.data.ViewerTeamIDs = role, teams
		}
	}
	if err := r.base.FingerprintLoaded(ctx, r.next); err != nil {
		return nil, err
	}
	value := reflect.ValueOf(payload)
	if !value.IsValid() || value.Kind() != reflect.Struct || value.NumField() != 1 {
		return nil, ErrBoundedMetadataCoverage
	}
	field := value.FieldByName("NotificationIDs")
	if !field.IsValid() || field.Type() != reflect.TypeFor[[]string]() || !field.CanInterface() {
		return nil, ErrBoundedMetadataCoverage
	}
	ids := field.Interface().([]string)
	budget := &boundedDiscussionAttempt{owner: r.owner}
	if err := budget.reserveValue(ctx, ids); err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if _, found := selected[id]; !found {
			if err := r.inputs.reserve(ctx, uint64(len(id))*8+512); err != nil {
				return nil, err
			}
			selected[id] = true
		}
	}
	index, _, _ := metadataLogicalField("notificationDeliveries")
	err = r.next.fields[index].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		wanted := false
		err := r.owner.withHeader(ctx, node, []string{"RecipientID", "Channel", "NotificationID"}, func(value reflect.Value) error {
			delivery := value.Interface().(domain.NotificationDelivery)
			wanted = delivery.RecipientID == viewer && delivery.Channel == "desktop" && selected[delivery.NotificationID]
			return nil
		})
		if err != nil || !wanted {
			return err
		}
		names := []string{"ID", "RecipientID", "Channel", "NotificationID", "Status", "AcknowledgedAt", "DeliveredAt", "UpdatedAt"}
		cost, err := issueHotProjectionCost(ctx, node, names)
		if err != nil {
			return err
		}
		if err := r.inputs.reserve(ctx, cost+8192); err != nil {
			return err
		}
		value, err := issueHotProjection(ctx, node, names, cost)
		if err != nil {
			return err
		}
		r.data.NotificationDeliveries = append(r.data.NotificationDeliveries, value.Interface().(domain.NotificationDelivery))
		r.ordinals = append(r.ordinals, int(ordinal.Int()))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := budget.reserveCallback(ctx, r.data, payload); err != nil {
		return nil, err
	}
	r.original = cloneBootstrap(r.data)
	r.original.ViewerTeamIDs = cloneSnapshotValue(reflect.ValueOf(r.data.ViewerTeamIDs)).Interface().([]string)
	if r.data.Viewer.Via != nil {
		r.original.Viewer.Via = cloneSnapshotValue(reflect.ValueOf(r.data.Viewer.Via)).Interface().(*domain.ActorBot)
	}
	return r, nil
}

func (r *legacyDesktopAckAttempt) merge(ctx context.Context) (*metadataLogicalSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	left, right := r.original, r.data
	left.NotificationDeliveries, right.NotificationDeliveries = nil, nil
	if !reflect.DeepEqual(left, right) || len(r.original.NotificationDeliveries) != len(r.data.NotificationDeliveries) {
		return nil, ErrBoundedMetadataCoverage
	}
	for index, before := range r.original.NotificationDeliveries {
		after := r.data.NotificationDeliveries[index]
		before.Status, before.AcknowledgedAt, before.DeliveredAt, before.UpdatedAt = after.Status, after.AcknowledgedAt, after.DeliveredAt, after.UpdatedAt
		if !reflect.DeepEqual(before, after) || after.Status != "delivered" || after.AcknowledgedAt == nil ||
			after.DeliveredAt == nil || !after.AcknowledgedAt.Equal(*after.DeliveredAt) || !after.UpdatedAt.Equal(*after.DeliveredAt) {
			return nil, ErrBoundedMetadataCoverage
		}
	}
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	for _, storage := range r.next.stores {
		if err := frames.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	if err := frames.inheritTimestampInputs(ctx); err != nil {
		return nil, err
	}
	index, _, _ := metadataLogicalField("notificationDeliveries")
	base := r.next.fields[index]
	patch := legacyDiscussionSlicePatch{base: base, shape: base, replacements: map[int]metadataLogicalNode{}, dropped: map[int]bool{}}
	deltas := map[int]int{}
	for selected, ordinal := range r.ordinals {
		deltas[ordinal] = selected
	}
	input := &legacyDiscussionInputs{input: r.inputs}
	err = base.Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		selected, found := deltas[int(ordinal.Int())]
		if !found {
			return nil
		}
		next, err := input.deltaFrame(ctx, frames, node, legacyDiscussionRecordDelta{
			before: reflect.ValueOf(r.original.NotificationDeliveries[selected]), next: reflect.ValueOf(r.data.NotificationDeliveries[selected]),
			allowed: []string{"Status", "AcknowledgedAt", "DeliveredAt", "UpdatedAt"},
		})
		if err == nil {
			patch.replacements[int(ordinal.Int())] = next
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return r.next.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "notificationDeliveries" {
			return patch, true, nil
		}
		return metadataAggregateStoredNode{original}, false, nil
	})
}

func (s *SQLiteStore) mutateBoundedDesktopAck(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if s == nil || !s.BoundedMetadataEnabled() || !desktopNotificationAcknowledgement(ctx) ||
		eventType != "notification.desktop_acknowledged" || mutate == nil || UsesIssueRecordMutations(ctx) || !metadataOutputOwnerPresent(ctx) {
		return ErrBoundedMetadataCoverage
	}
	viewer := mutationAggregateID(ctx)
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	webhook := false
	if s.webhookConfigured() {
		var err error
		webhook, err = s.boundedLegacyWebhookNeeded(ctx, workspace)
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
	ctx, finish, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finish()
	var event domain.DomainEvent
	var realtime json.RawMessage
	written := false
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), workspace); err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
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
		attempt, err := s.prepareLegacyDesktopAck(ctx, workspace, viewer, payload, webhook)
		if err != nil {
			return err
		}
		transferred := false
		defer func() {
			if !transferred {
				attempt.Close()
			}
		}()
		defer attempt.base.Close()
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		aggregate, err := mutate(&attempt.data)
		if err != nil {
			return err
		}
		if aggregate != viewer {
			return ErrBoundedMetadataCoverage
		}
		callback, err := attempt.merge(ctx)
		if err != nil {
			return err
		}
		defer callback.Release()
		attempt.data.ViewerRole, attempt.data.ViewerTeamIDs = attempt.nativeRole, nil
		var previous json.RawMessage
		if webhook {
			plan, err := planMetadataAggregatePayload(ctx, metadataAggregateImage{Source: attempt.before}, metadataAggregateImage{Source: callback}, aggregate, true, nil)
			if err != nil {
				return err
			}
			if err := attempt.owner.reserve(ctx, uint64(plan.RequiredBytes)); err != nil {
				return err
			}
			previous, _, err = plan.Values(ctx)
			if err != nil {
				return err
			}
		}
		counted, err := s.refreshLegacyResourceCountsFromImage(ctx, callback)
		if err != nil {
			return err
		}
		defer counted.Release()
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		nextEvent := domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate, Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
		plan, err := planMetadataAggregatePayload(ctx, metadataAggregateImage{Source: attempt.before}, metadataAggregateImage{Source: counted}, aggregate, false, payloadRaw)
		if err != nil {
			return err
		}
		if err := attempt.owner.reserve(ctx, uint64(plan.RequiredBytes)); err != nil {
			return err
		}
		_, entity, err := plan.Values(ctx)
		if err != nil {
			return err
		}
		nextRealtime := enrichRealtimePayload(payloadRaw, entity, eventType)
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
		if stamp.workspaceID == "" || stamp.workspaceID != attempt.stored.Stamp().workspaceID {
			return ErrResourceAccessChanged
		}
		if err := attempt.base.LockForPersistence(ctx, tx, workspace, counted); err != nil {
			return err
		}
		if err := s.persistLegacyImportInputs(ctx, tx, workspace, counted); err != nil {
			return err
		}
		if err := attempt.base.Persist(ctx, tx, workspace, counted); err != nil {
			return err
		}
		merged, err := s.persistLegacyMetadataImage(ctx, tx, workspace, attempt.stored, counted)
		if err != nil {
			return err
		}
		defer merged.Release()
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, stamp, attempt.stored.Stamp() == stamp, true)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		budget := &boundedDiscussionAttempt{owner: attempt.owner}
		if err := budget.reserveValue(ctx, s.viewer); err != nil {
			return err
		}
		viewerRaw, _ := json.Marshal(s.viewer)
		if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
			index, _, _ := metadataLogicalField("viewer")
			value, err := attempt.owner.node(ctx, merged.fields[index], false)
			if err != nil {
				return err
			}
			viewerRaw, _ = json.Marshal(value.Interface())
		}
		if err := writeAccountMetadata(ctx, tx, workspace, viewerRaw); err != nil {
			return err
		}
		storedPrevious, err := storedEventPreviousValues(ctx, tx, workspace, eventType, aggregate, previous)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: nextEvent.ID, Type: eventType, AggregateID: aggregate, Payload: payloadRaw, PreviousValues: storedPrevious, CreatedAt: nextEvent.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: stamp.workspaceID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		written = true
		if err := publication.Install(); err != nil {
			return err
		}
		event, realtime = nextEvent, nextRealtime
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
	s.publishMutation(ctx, workspace, event, realtime)
	return nil
}
