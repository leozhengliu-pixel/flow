package main

import (
	"context"
	"errors"
	"log"
	"time"

	"flow/api/internal/store"
)

// The persisted commit-ordered log closes the gap between a business commit
// and an in-memory notification callback. Outbox inserts are idempotent, and a
// failed insert never advances this consumer's cursor.
func (s *server) processChatIntegrationTick(ctx context.Context, workspace string) error {
	lease, claimed, err := s.store.ClaimChatWorker(ctx, workspace, time.Now(), time.Minute)
	if err != nil || !claimed {
		return err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.store.ReleaseChatWorker(releaseCtx, workspace, lease)
	}()
	return errors.Join(s.recheckChatTenants(ctx, workspace), s.queueCommittedChatEvents(ctx, workspace), s.processChatJobs(ctx, workspace))
}

func (s *server) queueCommittedChatEvents(ctx context.Context, workspace string) error {
	records, err := s.store.ChatDomainEventPage(ctx, workspace, 200)
	if errors.Is(err, store.ErrChatDomainCursorExpired) {
		from, to, resetErr := s.store.ResetChatDomainCursor(ctx, workspace)
		if resetErr != nil {
			return resetErr
		}
		log.Printf("Chat integration workspace=%s: notification cursor %d is outside the retained event log; resuming at %d", workspace, from, to)
		return nil
	}
	if err != nil {
		return err
	}
	for _, record := range records {
		switch record.Event.Type {
		case "issue.created", "issue.updated", "comment.created", "comment.updated", "comment.deleted", "project.update_created", "initiative.update_created", "ask.decided":
			if err := s.queueChatDomainEvent(ctx, workspace, record.Event); err != nil {
				return err
			}
		}
		if err := s.store.AdvanceChatDomainCursor(ctx, workspace, record.Seq); err != nil {
			return err
		}
	}
	return nil
}
