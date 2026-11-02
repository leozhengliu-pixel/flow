package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Contract extraction runs outside the write lock. Its callback must check the
// instructions and person choices against these freshly locked records, not a
// snapshot from before the model request. No issue or discussion data is needed.
func (s *SQLiteStore) mutateLoopContract(ctx context.Context, workspace string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	loopID := mutationAggregateID(ctx)
	if loopID == "" {
		return errors.New("loop contract mutation requires a loop ID")
	}
	traceStart(ctx, "loop_contract")
	webhookEnabled := s.webhookConfigured() && s.webhookNeeded(ctx, workspace)
	var event domain.DomainEvent
	var realtimePayload json.RawMessage
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		traceLocked(ctx)
		defer traceUnlocked(ctx)
		event, realtimePayload = domain.DomainEvent{}, nil
		current, ok := s.storedWorkspace(workspace)
		if !ok {
			return fmt.Errorf("workspace %q: not found", workspace)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		locked, err := lockMetadataRecords(ctx, tx, workspace, "loops", []string{loopID})
		if err != nil {
			return err
		}
		before := map[metadataRecordKey][]byte{}
		if raw, exists := locked[loopID]; exists {
			before[metadataRecordKey{"loops", loopID}] = raw
		}
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		rows, err := tx.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field='loopVersions' AND `+s.jsonText("data", "loopId")+`=? ORDER BY record_key`+lock, workspace, loopID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key string
			var raw []byte
			if err := rows.Scan(&key, &raw); err != nil {
				rows.Close()
				return err
			}
			before[metadataRecordKey{"loopVersions", key}] = raw
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		next := domain.Bootstrap{Workspace: current.Workspace}
		if err := setMetadataRecords(&next, before); err != nil {
			return err
		}
		previous := cloneBootstrap(next)
		traceMark(ctx, "load")
		aggregateID, err := mutate(&next)
		traceMark(ctx, "mutate")
		if err != nil {
			return err
		}
		for _, field := range changedMetadataFields(previous, next) {
			if field.name != "loops" && field.name != "loopVersions" {
				return errors.New("loop contract mutation changed unrelated metadata")
			}
		}
		encoded, err := json.Marshal(struct {
			Loops        []domain.Loop        `json:"loops"`
			LoopVersions []domain.LoopVersion `json:"loopVersions"`
		}{next.Loops, next.LoopVersions})
		if err != nil {
			return err
		}
		_, _, after, err := splitWorkspaceMetadata(encoded)
		if err != nil {
			return err
		}
		changes := []metadataRecordChange{}
		for key, record := range after {
			base, ok := before[key]
			if !ok {
				return errors.New("loop contract mutation changed unrelated metadata")
			}
			if !jsonEqual(base, record.data) {
				changes = append(changes, metadataRecordChange{field: key.field, key: key.key, raw: record.data})
			}
		}
		if len(after) != len(before) {
			return errors.New("loop contract mutation removed metadata")
		}
		if len(changes) == 0 {
			return nil
		}
		sort.Slice(changes, func(i, j int) bool {
			if changes[i].field != changes[j].field {
				return changes[i].field < changes[j].field
			}
			return changes[i].key < changes[j].key
		})
		if err := writeMetadataRecordUpserts(ctx, tx, workspace, changes); err != nil {
			return err
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: "loop.output_contract", AggregateID: aggregateID, Payload: raw, CreatedAt: time.Now().UTC()}
		if webhookEnabled {
			event.PreviousValues = aggregatePreviousValues(previous, next, aggregateID)
		}
		realtimePayload = enrichRealtimePayload(raw, aggregateJSONValue(next, aggregateID), event.Type)
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, Payload: event.Payload, PreviousValues: event.PreviousValues, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: current.Workspace.ID}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		traceMark(ctx, "persist")
		// Keep unrelated cached metadata as-is; readers never share these slices.
		current.Loops = slices.Clone(current.Loops)
		current.LoopVersions = slices.Clone(current.LoopVersions)
		for _, change := range changes {
			before[metadataRecordKey{change.field, change.key}] = change.raw
		}
		if err := setMetadataRecords(&current, before); err != nil {
			return err
		}
		s.installWorkspace(workspace, current, true)
		return nil
	}
	err := mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, nil)
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	if event.ID != "" {
		s.publishMutation(ctx, workspace, event, realtimePayload)
	}
	return nil
}
