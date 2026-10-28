package store

import (
	"context"
	"encoding/json"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// recordMetadataEvent records and publishes an event whose mutation changed
// nothing stored (for example a member cleanup with no affected issues). It
// delivers the same domain event, webhook and realtime envelope the generic
// mutation path would, without cloning or rewriting the workspace.
func (s *SQLiteStore) recordMetadataEvent(ctx context.Context, workspace, eventType, aggregateID string, payload any) error {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var entity any
	workspaceID := ""
	s.mu.RLock()
	if data, ok := s.workspaces[workspace]; ok {
		entity = teamMutationEntity(&data, aggregateID, len(data.Teams))
		workspaceID = data.Workspace.ID
	}
	s.mu.RUnlock()
	now := time.Now().UTC()
	event := domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregateID, Payload: payloadRaw, CreatedAt: now}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, Payload: []byte(event.Payload), PreviousValues: []byte(nil), CreatedAt: now.Format(time.RFC3339Nano), WorkspaceID: workspaceID}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishMutation(ctx, workspace, event, enrichRealtimePayload(payloadRaw, entity, eventType))
	return nil
}
