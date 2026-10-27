package main

import (
	"context"
	"errors"
	"time"
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
	return errors.Join(s.queueCommittedChatEvents(ctx, workspace), s.processChatJobs(ctx, workspace))
}

func (s *server) queueCommittedChatEvents(ctx context.Context, workspace string) error {
	records, err := s.store.ChatDomainEventPage(ctx, workspace, 200)
	if err != nil {
		return err
	}
	for _, record := range records {
		switch record.Event.Type {
		case "issue.created", "issue.updated", "comment.created", "project.update_created":
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
