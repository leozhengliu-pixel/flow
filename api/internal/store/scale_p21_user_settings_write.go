package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// userSettingsUsernameScope is the callback input of a personal settings
// write that names a username: usernameTaken derives every member's handle
// from the users directory, the member list and every member's settings.
var userSettingsUsernameScope = MetadataRecordScope{Fields: []string{"users", "members", "userSettings"}}

// MutateUserSettings is the personal settings writer (event
// user_settings.updated, aggregate the member's user ID). Flag off it is the
// literal MutateWorkspace. Bounded, a write that does not name a username
// runs the literal point writer's transaction (mutateUserSettingsRecord) on
// the member's settings record alone; one that names a username runs on the
// scoped writer with the complete users, members and userSettings fields,
// as the literal metadata-only full writer gives them to the callback.
func (s *SQLiteStore) MutateUserSettings(ctx context.Context, workspace, aggregate string, payload any, mutate func(*domain.Bootstrap) error) error {
	const eventType = "user_settings.updated"
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspace(ctx, workspace, eventType, aggregate, payload, mutate)
	}
	if !userSettingsRecordMutation(eventType, payload) {
		return s.MutateWorkspaceScoped(ctx, workspace, eventType, aggregate, payload, userSettingsUsernameScope, mutate)
	}
	return s.mutateBoundedUserSettingsRecord(withMutationAggregateID(ctx, aggregate), workspace, eventType, aggregate, payload, mutate)
}

// mutateBoundedUserSettingsRecord is mutateUserSettingsRecord for a bounded
// store. The literal writer starts the member's entry from its stored row
// (as stored, without the load's defaults) or else from the installed entry,
// writes only that record (rebased onto the row, skipped when the row
// vanished under an installed entry), records the event without previous
// values and installs the written entry as written. Here the installed entry
// comes from the installed logical source, the same transaction runs under
// the bounded writers' lock and stamp protocol, and the installed source is
// patched with exactly the entry the literal writer installs. The realtime
// entity is the literal one: the aggregate's users entry (a usr_ ID), else
// the first provider job, workspace or viewer (the acting member) with that
// ID, all read from the installed source.
func (s *SQLiteStore) mutateBoundedUserSettingsRecord(ctx context.Context, workspace, eventType, aggregate string, payload any, mutate func(*domain.Bootstrap) error) error {
	ctx = metadataLogicalUnpinnedContext(ctx)
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
	var realtimePayload json.RawMessage
	apply := func(ctx context.Context) error {
		event, realtimePayload = domain.DomainEvent{}, nil
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
		actor, hasActor := actorFromContext(ctx)
		role := ""
		if hasActor {
			if value, status, err := s.WorkspaceRole(ctx, workspaceID, actor.ID); err == nil && status == "active" {
				role = value
			}
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
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		// A map stored inline (an over-long key) has no rows: the literal
		// writer writes the whole installed map, so it is read whole.
		inline, err := s.inlineMetadataFields(ctx, tx, workspace, "userSettings")
		if err != nil {
			return err
		}
		scope := MetadataRecordScope{Read: []RecordKey{{Kind: "providerJobs", ID: aggregate}, {Kind: "users", ID: aggregate}, {Kind: "userSettings", ID: aggregate}}}
		if len(inline) > 0 {
			scope.Fields = []string{"userSettings"}
		}
		current, err := source.Select(ctx, scope, 8<<20)
		if err != nil {
			return err
		}
		next := domain.Bootstrap{Workspace: current.Workspace, Viewer: current.Viewer, ViewerRole: current.ViewerRole, UserSettings: map[string]domain.UserSettings{}}
		for key, value := range current.UserSettings {
			next.UserSettings[key] = value
		}
		// Installed defaults aren't stored records. Use the locked row as
		// the merge base when one exists and allow the first settings write.
		storedAggregate := false
		if aggregate != "" {
			var raw []byte
			err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='userSettings' AND record_key=?`+lock, workspace, aggregate).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				var stored domain.UserSettings
				if err := json.Unmarshal(raw, &stored); err != nil {
					return err
				}
				next.UserSettings[aggregate] = stored
				storedAggregate = true
			}
		}
		before := map[string]domain.UserSettings{}
		for key, value := range next.UserSettings {
			before[key] = value
		}
		if hasActor {
			next.Viewer = actor
			if role != "" {
				next.ViewerRole = role
			}
		}
		if err := mutate(&next); err != nil {
			return err
		}
		refs := newIssueReferences(domain.Bootstrap{Users: current.Users})
		installed := map[string]domain.UserSettings{}
		for key, value := range next.UserSettings {
			installed[key] = value
		}
		deleted := map[string]bool{}
		rowKeys, removed := sortedKeys(next.UserSettings), before
		if len(inline) > 0 {
			written := domain.Bootstrap{Workspace: current.Workspace, UserSettings: next.UserSettings}
			if err := s.writeInlineMetadataFields(ctx, tx, workspace, written, inline); err != nil {
				return err
			}
			rowKeys, removed = nil, nil
		}
		for _, key := range rowKeys {
			value := next.UserSettings[key]
			previous, existed := before[key]
			if existed && reflect.DeepEqual(previous, value) {
				continue
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			var base []byte
			stored := existed && (key != aggregate || storedAggregate)
			if stored {
				if base, err = json.Marshal(previous); err != nil {
					return err
				}
			}
			rebased, write, err := rebaseMetadataRecordWrite(ctx, tx, workspace, refs, "userSettings", key, base, raw)
			if err != nil {
				return err
			}
			if !write {
				delete(installed, key)
				deleted[key] = true
				continue
			}
			if !bytes.Equal(rebased, raw) {
				var merged domain.UserSettings
				if err := json.Unmarshal(rebased, &merged); err != nil {
					return err
				}
				installed[key], raw = merged, rebased
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'userSettings',?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`, workspace, key, raw); err != nil {
				return err
			}
			if !stored {
				if err := ensureOAuthMetadataShape(ctx, tx, workspace, "userSettings", "map", lock); err != nil {
					return err
				}
			}
			if err := writeCustomerFilterRecord(ctx, tx, workspace, "userSettings", key, raw); err != nil {
				return err
			}
			if err := syncMetadataSearchDocument(ctx, tx, workspace, "userSettings", key, raw); err != nil {
				return err
			}
		}
		for key := range removed {
			if _, ok := installed[key]; !ok {
				if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='userSettings' AND record_key=?`, workspace, key); err != nil {
					return err
				}
			}
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate, Payload: payloadRaw, CreatedAt: now}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, Payload: []byte(event.Payload), PreviousValues: []byte(nil), CreatedAt: now.Format(time.RFC3339Nano), WorkspaceID: workspaceID}); err != nil {
			return err
		}
		// The literal entity lookup runs over the installed snapshot with the
		// acting member as viewer: the aggregate's users entry, else the
		// first provider job, workspace member or viewer with that ID.
		entityImage := domain.Bootstrap{ProviderJobs: current.ProviderJobs, Workspace: current.Workspace, Viewer: next.Viewer, Users: current.Users}
		realtimePayload = enrichRealtimePayload(payloadRaw, aggregateJSONValue(entityImage, aggregate), eventType)
		// The installed source changes exactly where the literal writer's
		// installed map differs from the one it started from.
		var patches []metadataLogicalRecordPatch
		for _, key := range sortedKeys(installed) {
			value := installed[key]
			if previous, existed := current.UserSettings[key]; existed && reflect.DeepEqual(previous, value) {
				continue
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			patches = append(patches, metadataLogicalRecordPatch{key: metadataRecordKey{"userSettings", key}, raw: raw})
		}
		for _, key := range sortedKeys(current.UserSettings) {
			if _, ok := installed[key]; !ok {
				patches = append(patches, metadataLogicalRecordPatch{key: metadataRecordKey{"userSettings", key}})
			}
		}
		if len(patches) == 0 {
			// The installed entries are unchanged, but the commit's stamp
			// still installs on a source of its own.
			raw, err := json.Marshal(installed[aggregate])
			if err != nil {
				return err
			}
			patches = append(patches, metadataLogicalRecordPatch{key: metadataRecordKey{"userSettings", aggregate}, raw: raw})
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
		if errors.Is(err, ErrNoMutation) {
			return nil
		}
		return err
	}
	s.dropMetadataCache(ctx, workspace)
	s.publishMutation(ctx, workspace, event, realtimePayload)
	return nil
}
