package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// mutateBoundedLabelDeletion is MutateLabelDeletion for a bounded store: the
// same transaction (labelDeletionWrite's label cascade over the one-record
// projections it selects in SQL, and the label.deleted or
// label_group.deleted event without previous values), under the bounded
// writers' lock and stamp protocol. The legacy writer replaced the changed
// records in its full in-memory snapshot; here the installed logical source
// is patched with exactly the metadata records the transaction wrote or
// deleted (as a reload decodes them) and installed at the commit's stamp, so
// no workspace image is decoded. Issue rows are not part of the installed
// image. Publication is the legacy writer's: the committed event to the
// webhook sink and its payload to the realtime sink.
func (s *SQLiteStore) mutateBoundedLabelDeletion(ctx context.Context, workspace, eventType, id string, group bool, mutate func(*domain.Bootstrap) error) error {
	ctx = trackMetadataWorkDemand(metadataLogicalUnpinnedContext(ctx))
	ctx, release, err := s.admitMetadataWork(ctx, 8<<20+metadataPolicyBytes)
	if err != nil {
		return err
	}
	defer release()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
				return err
			}
		}
		source, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		defer source.Release()
		workspaceID, err := s.WorkspaceIDForKey(ctx, workspace)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
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
		if baseStamp.workspaceID != workspaceID {
			return ErrResourceAccessChanged
		}
		exactSource := baseStamp == source.Stamp()
		if s.coordinator != nil && !exactSource {
			return ErrConcurrentWrite
		}
		ids, changes, err := s.labelDeletionWrite(ctx, tx, workspace, id, group, mutate)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		payload, err := json.Marshal(map[string]any{"labelIds": ids})
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: id, Payload: payload, CreatedAt: now}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: id, Payload: []byte(payload), PreviousValues: []byte(nil), CreatedAt: now.Format(time.RFC3339Nano), WorkspaceID: workspaceID}); err != nil {
			return err
		}
		var patches []metadataLogicalRecordPatch
		fields := make([]string, 0, len(changes))
		for field := range changes {
			fields = append(fields, field)
		}
		slices.Sort(fields)
		for _, field := range fields {
			keys := make([]string, 0, len(changes[field]))
			for key := range changes[field] {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				raw := changes[field][key]
				if len(raw) == 0 {
					raw = nil
				}
				patches = append(patches, metadataLogicalRecordPatch{key: metadataRecordKey{field, key}, raw: raw})
			}
		}
		candidate, err := metadataLogicalPatchRecords(ctx, source, patches)
		if err != nil {
			return err
		}
		defer func() {
			if candidate != nil {
				candidate.Release()
			}
		}()
		capture := tx.captureMetadataCommitStampFrom(workspace, baseStamp)
		policy, cost, err := s.metadataLogicalPolicy(ctx, candidate)
		if err != nil {
			return err
		}
		s.metadataResidency.mu.Lock()
		total := cost
		for key, bytes := range s.metadataResidency.policy {
			if key != workspace {
				total += bytes
			}
		}
		limit := s.metadataResidency.policyLimit
		s.metadataResidency.mu.Unlock()
		if total > limit {
			return ErrMetadataWorkBudget
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		finalStamp, ready := capture.result()
		if !ready {
			return ErrBoundedMetadataCoverage
		}
		sourceStamp := copyStamp{workspaceID: finalStamp.workspaceID, revision: -1}
		if exactSource {
			sourceStamp = finalStamp
		}
		if err := candidate.setStamp(sourceStamp); err != nil {
			return err
		}
		if err := s.installResidentPolicySource(workspace, policy, finalStamp, 0, cost, true, candidate); err != nil {
			return err
		}
		candidate = nil
		return nil
	}
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if err != nil {
		return err
	}
	s.dropMetadataCache(ctx, workspace)
	if sink := s.webhook(); sink != nil {
		sink(workspace, event)
	}
	if sink := s.realtime(); sink != nil {
		actor, _ := actorFromContext(ctx)
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: id, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: event.Payload, CreatedAt: event.CreatedAt})
	}
	return nil
}
