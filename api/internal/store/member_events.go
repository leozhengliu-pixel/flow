package store

import (
	"context"
	"encoding/json"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

var memberStatusEvents = map[string]string{
	"suspended": "workspace_member.suspended",
	"active":    "workspace_member.resumed",
	"removed":   "workspace_member.removed",
}

// recordMemberStatusTx keeps the event and the membership change atomic.
func recordMemberStatusTx(ctx context.Context, tx *sqlTx, workspaceID, userID, status string) (domain.DomainEvent, error) {
	payload, err := json.Marshal(map[string]string{"userId": userID, "status": status})
	if err != nil {
		return domain.DomainEvent{}, err
	}
	event := domain.DomainEvent{ID: idgen.New("evt_"), Type: memberStatusEvents[status], AggregateID: userID, Payload: payload, CreatedAt: time.Now().UTC()}
	_, err = insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: userID, Payload: payload, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: workspaceID})
	return event, err
}

func (s *SQLiteStore) publishMemberStatus(ctx context.Context, workspaceID string, event domain.DomainEvent) {
	if _, workspace, ok := s.workspaceByID(workspaceID); ok {
		s.publishMutation(ctx, workspace, event, event.Payload)
	}
}

// PublishMemberStatus records a status changed by SCIM and fans it out through
// the same durable event, webhook and realtime paths as other mutations.
func (s *SQLiteStore) PublishMemberStatus(ctx context.Context, workspaceID, userID, status string) error {
	if memberStatusEvents[status] == "" || userID == "" {
		return ErrAuthForbidden
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	event, err := recordMemberStatusTx(ctx, tx, workspaceID, userID, status)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishMemberStatus(ctx, workspaceID, event)
	return nil
}
