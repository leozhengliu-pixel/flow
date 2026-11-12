package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func (s *SQLiteStore) NotificationRecord(ctx context.Context, workspace, userID, id string) (domain.Notification, error) {
	var raw []byte
	var result domain.Notification
	err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND id=? AND owner_id=?`, workspace, id, userID).Scan(&raw)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if result.RecipientID != userID {
		return domain.Notification{}, sql.ErrNoRows
	}
	refs, err := s.readIssueReferences(ctx, workspace, []string{result.Actor.ID})
	if err != nil {
		return result, err
	}
	if actor, ok := refs.users[result.Actor.ID]; ok {
		result.Actor = actor
	}
	return result, nil
}

// NotificationStateChanges includes both owners on reassignment and the old
// owner on removal. P09 uses it with the records captured by its write journal.
func NotificationStateChanges(workspace string, before, after *domain.Notification) []NotificationStateChange {
	if before == nil && after == nil {
		return nil
	}
	if before != nil && after != nil {
		oldJSON, oldErr := json.Marshal(before)
		newJSON, newErr := json.Marshal(after)
		if oldErr == nil && newErr == nil && bytes.Equal(oldJSON, newJSON) {
			return nil
		}
	}
	var changes []NotificationStateChange
	for _, item := range []*domain.Notification{before, after} {
		if item != nil && item.RecipientID != "" {
			changes = append(changes, NotificationStateChange{WorkspaceKey: workspace, RecipientID: item.RecipientID, ChangedIDs: []string{item.ID}})
		}
	}
	return changes
}

func (s *SQLiteStore) UpdateNotificationRecord(ctx context.Context, workspace, userID, id, eventType string, input any, mutate func(*domain.Notification)) (domain.Notification, error) {
	var result domain.Notification
	var event domain.DomainEvent
	var realtime json.RawMessage
	err := func() error {
		s.lockWorkspaceWrites()
		defer s.unlockWorkspaceWrites()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var raw []byte
		var resource string
		if err := tx.QueryRowContext(ctx, `SELECT resource_id,data FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND id=? AND owner_id=?`+lock, workspace, id, userID).Scan(&resource, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		if result.RecipientID != userID {
			return sql.ErrNoRows
		}
		previous, err := json.Marshal(result)
		if err != nil {
			return err
		}
		stamped := result.UpdatedAt
		mutate(&result)
		unstamped := result
		unstamped.UpdatedAt = stamped
		if next, err := json.Marshal(unstamped); err == nil && bytes.Equal(previous, next) {
			// An update to the current values (its updatedAt aside) writes
			// nothing and keeps the stored record.
			result = unstamped
			return ErrNoMutation
		}
		if err := writeContentRecord(ctx, tx, workspace, "notification", resource, result); err != nil {
			return err
		}
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: id, Payload: payload, CreatedAt: time.Now().UTC()}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: id, Payload: payload, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceKey: workspace}); err != nil {
			return err
		}
		realtime = enrichRealtimePayload(payload, result, eventType)
		return tx.Commit()
	}()
	if errors.Is(err, ErrNoMutation) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if sink := s.webhook(); sink != nil {
		sink(workspace, webhookEvent(ctx, event, realtime))
	}
	if sink := s.realtime(); sink != nil {
		actor, _ := actorFromContext(ctx)
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: id, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: realtime, CreatedAt: event.CreatedAt})
	}
	return result, nil
}
