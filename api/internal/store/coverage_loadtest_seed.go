//go:build loadtest

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// seedSyntheticBatchBounded is the bounded writer of one SeedSyntheticMetadata
// batch, built only with -tags loadtest. The callback appends new records to a
// single collection (a record-backed metadata list or notifications) of an
// otherwise empty image holding the resident workspace and viewer; no stored
// collection is read or decoded. In one transaction it writes what the legacy
// full mutation writes for such an append:
//   - each new metadata record through the canonical row writer, ordered after
//     the stored last record (the legacy tail append of writeArrayFieldDelta;
//     an empty collection starts at 0 as the legacy record sync does);
//   - each notification through the content record writer (syncContentRecords);
//   - the same issue cache revision, domain event, account viewer row and
//     publication.
//
// The resident image is reinstalled by the caller once all batches commit.
func (s *SQLiteStore) seedSyntheticBatchBounded(ctx context.Context, workspace, field string, mutate func(*domain.Bootstrap) (string, error)) error {
	const eventType = "loadtest.seeded"
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	metadata, ok := field != "notifications", true
	var index metadataField
	if metadata {
		index, ok = metadataFieldByName(field)
		if !ok || metadataResidencyFields()[field] != "records" || reflect.TypeFor[domain.Bootstrap]().Field(index.index).Type.Kind() != reflect.Slice {
			return fmt.Errorf("%w: synthetic seed collection %s", ErrBoundedMetadataCoverage, field)
		}
	}
	webhookEnabled := false
	if s.webhookConfigured() {
		var err error
		if webhookEnabled, err = s.residentWebhookNeeded(ctx, workspace); err != nil {
			return err
		}
	}
	var event domain.DomainEvent
	var realtime json.RawMessage
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		resident, found := s.storedWorkspace(workspace)
		if !found {
			return fmt.Errorf("workspace %q: %w", workspace, errors.New("not found"))
		}
		data := domain.Bootstrap{Workspace: resident.Workspace, Viewer: resident.Viewer}
		aggregate, err := mutate(&data)
		if err != nil {
			return err
		}
		// Only the declared collection may change.
		expected := domain.Bootstrap{Workspace: resident.Workspace, Viewer: resident.Viewer}
		target := reflect.ValueOf(&expected).Elem()
		if metadata {
			target.Field(index.index).Set(reflect.ValueOf(data).Field(index.index))
		} else {
			expected.Notifications = data.Notifications
		}
		if !reflect.DeepEqual(expected, data) {
			return fmt.Errorf("%w: synthetic seed changed more than %s", ErrBoundedMetadataCoverage, field)
		}
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if tx.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var workspaceID string
		var rootBytes int64
		length := (&ResourceRead{store: s}).byteLength("data")
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id,`+length+` FROM workspace_states WHERE workspace_key=?`+lock, workspace).Scan(&workspaceID, &rootBytes); err != nil {
			return err
		}
		if workspaceID != resident.Workspace.ID {
			return ErrResourceAccessChanged
		}
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				return err
			}
		}
		if metadata {
			if err := s.seedSyntheticRecords(ctx, tx, workspace, index, reflect.ValueOf(data).Field(index.index), lock, rootBytes > MetadataRecordPageMaxBytes); err != nil {
				return err
			}
		} else {
			for resource, items := range notificationRecords(data.Notifications) {
				for _, item := range items {
					if err := writeContentRecord(ctx, tx, workspace, "notification", resource, item); err != nil {
						return err
					}
				}
			}
		}
		// The legacy full mutation rewrites the issue rows it loaded, which
		// advances the issue cache revision once per batch.
		queueIssueCountRevision(ctx, tx, workspace)
		viewerRaw, _ := json.Marshal(s.viewer)
		if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
			viewerRaw, _ = json.Marshal(data.Viewer)
		}
		if err := writeAccountMetadata(ctx, tx, workspace, viewerRaw); err != nil {
			return err
		}
		payloadRaw, err := json.Marshal(nil)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		if webhookEnabled {
			event.PreviousValues = aggregatePreviousValues(expectedWithout(expected, index, metadata), data, aggregate)
		}
		realtime = enrichRealtimePayload(payloadRaw, aggregateJSONValue(data, aggregate), eventType)
		previous, err := storedEventPreviousValues(ctx, tx, workspace, event.Type, event.AggregateID, event.PreviousValues)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, Payload: payloadRaw, PreviousValues: previous, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: workspaceID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		return tx.Commit()
	}
	err := mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, nil)
	if err != nil {
		return err
	}
	s.dropMetadataCache(ctx, workspace)
	s.publishMutation(ctx, workspace, event, realtime)
	return nil
}

// expectedWithout is the callback input: the image before the append.
func expectedWithout(data domain.Bootstrap, index metadataField, metadata bool) domain.Bootstrap {
	if metadata {
		value := reflect.ValueOf(&data).Elem().Field(index.index)
		value.Set(reflect.Zero(value.Type()))
	} else {
		data.Notifications = nil
	}
	return data
}

// seedSyntheticRecords appends items after the stored last record of field.
// An existing ID or an inline collection (ensureScopedMetadataShape) is
// refused rather than rewritten; the legacy full sync would replace them.
func (s *SQLiteStore) seedSyntheticRecords(ctx context.Context, tx *sqlTx, workspace string, field metadataField, items reflect.Value, lock string, streamed bool) error {
	if items.Len() == 0 {
		return nil
	}
	if err := s.ensureScopedMetadataShape(ctx, tx, workspace, field.name, "array", lock, streamed); err != nil {
		return err
	}
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(collection_order) FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, workspace, field.name).Scan(&last); err != nil {
		return err
	}
	next := 0
	if last.Valid {
		next = int(last.Int64) + 1
	}
	seen := map[string]bool{}
	for i := 0; i < items.Len(); i++ {
		id, valid := metadataRecordValueKey(items.Index(i))
		if !valid || id == "" || len(id) > 191 || seen[id] {
			return fmt.Errorf("%w: synthetic %s record %q", ErrBoundedMetadataCoverage, field.name, id)
		}
		seen[id] = true
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field.name, id).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return fmt.Errorf("%w: synthetic %s record %q exists", ErrBoundedMetadataCoverage, field.name, id)
		}
		raw, err := json.Marshal(items.Index(i).Interface())
		if err != nil {
			return err
		}
		if err := writeMetadataRecordRow(ctx, tx, workspace, metadataRecordKey{field.name, id}, metadataRecord{data: raw, order: next + i}, nil); err != nil {
			return err
		}
	}
	return nil
}

// workspaceStateBytesBounded is WorkspaceStateBytes on a bounded store: the
// stored root's byte length and, per measured collection, the length of the
// JSON array legacy encodes from its metadata copy, summed one decoded record
// at a time from the installed logical generation (record-backed or inline).
func (s *SQLiteStore) workspaceStateBytesBounded(ctx context.Context, workspace string) (map[string]int, error) {
	var stateRow int
	length := (&ResourceRead{store: s}).byteLength("data")
	if err := s.db.QueryRowContext(ctx, `SELECT `+length+` FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&stateRow); err != nil {
		return nil, err
	}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	if _, ok := s.storedWorkspace(workspace); !ok {
		return nil, ErrAuthForbidden
	}
	source, err := s.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		return nil, err
	}
	defer source.Release()
	sizes := map[string]int{"stateRow": stateRow}
	for name, field := range map[string]string{"projects": "projects", "trash": "trash", "documents": "documents", "conversations": "aiConversations"} {
		index, _, ok := metadataLogicalField(field)
		if !ok {
			return nil, ErrBoundedMetadataCoverage
		}
		node := source.fields[index]
		null, err := node.isNil(ctx)
		if err != nil {
			return nil, err
		}
		if null {
			sizes[name] = len("null")
			continue
		}
		total, count := 2, 0
		err = node.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			cost := int(min(record.cost, uint64(metadataWorkBytes)))
			recordCtx, release, err := s.admitMetadataWork(ctx, max(cost, 64<<10))
			if err != nil {
				return err
			}
			defer release()
			value, err := record.Value(recordCtx, max(cost, 64<<10))
			if err != nil {
				return err
			}
			raw, err := json.Marshal(value.Interface())
			if err != nil {
				return err
			}
			total += len(raw)
			count++
			return nil
		})
		if err != nil {
			return nil, err
		}
		if count > 1 {
			total += count - 1
		}
		sizes[name] = total
	}
	return sizes, nil
}
