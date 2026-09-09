package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"flow/api/internal/domain"
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

func (s *SQLiteStore) UpdateNotificationRecord(ctx context.Context, workspace, userID, id, eventType string, input any, mutate func(*domain.Notification)) (domain.Notification, error) {
	var result domain.Notification
	var event domain.DomainEvent
	var realtime json.RawMessage
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
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
		mutate(&result)
		if err := writeContentRecord(ctx, tx, workspace, "notification", resource, result); err != nil {
			return err
		}
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: eventType, AggregateID: id, Payload: payload, CreatedAt: time.Now().UTC()}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, event.ID, event.Type, id, payload, event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		realtime = enrichRealtimePayload(payload, result, eventType)
		return tx.Commit()
	}()
	if err != nil {
		return result, err
	}
	if sink := s.webhook(); sink != nil {
		sink(workspace, event)
	}
	if sink := s.realtime(); sink != nil {
		actor, _ := actorFromContext(ctx)
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: id, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: realtime, CreatedAt: event.CreatedAt})
	}
	return result, nil
}
