package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"flow/api/internal/domain"
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
	event := domain.DomainEvent{ID: fmt.Sprintf("evt_%d", now.UnixNano()), Type: eventType, AggregateID: aggregateID, Payload: payloadRaw, CreatedAt: now}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at,workspace_id) VALUES(?,?,?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(event.Payload), []byte(nil), now.Format(time.RFC3339Nano), workspaceID); err != nil {
		return err
	}
	s.publishMutation(ctx, workspace, event, enrichRealtimePayload(payloadRaw, entity, eventType))
	return nil
}
