package main

import (
	"context"
	"errors"
	"log"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) sweepAgentLiveWorkspace(ctx context.Context, workspace string, now time.Time) int {
	swept := 0
	visit := func(session domain.AgentSession) error {
		abandoned := false
		for _, message := range session.Messages {
			if s.agentLiveReplyAbandoned(message, now) {
				abandoned = true
				break
			}
		}
		if !abandoned {
			return nil
		}
		mutationCtx := withAgentReplyFields(store.ContextWithActor(ctx, domain.User{ID: session.UserID}))
		mutate := func(next *domain.Bootstrap) error {
			// The literal callback ignores owner/deletion and continues past a
			// duplicate with no abandoned messages to the first changed one.
			for index := range next.AgentSessions {
				current := &next.AgentSessions[index]
				if current.ID != session.ID {
					continue
				}
				if interruptAgentLiveMessages(next, current, agentReasonReplyAbandoned, func(message domain.AgentMessage) bool {
					return s.agentLiveReplyAbandoned(message, now)
				}) {
					swept++
					return nil
				}
			}
			return errAgentLiveSkip
		}
		var err error
		if s.store.BoundedMetadataEnabled() {
			ownedCtx, closeOutput := beginIndependentMetadataOutputReads(mutationCtx)
			defer closeOutput()
			err = s.store.MutateAgentSessionLifecycle(ownedCtx, workspace, "agent.message_completed", session.ID, store.AgentSessionLifecycleScope{
				ID: session.ID, AppendActivities: true, StreamingMessages: true,
			}, mutate)
		} else {
			err = s.store.MutateWorkspace(mutationCtx, workspace, "agent.message_completed", session.ID, nil, mutate)
		}
		if err != nil && !errors.Is(err, errAgentLiveSkip) && ctx.Err() == nil {
			log.Printf("interrupt agent reply in session %s: %v", session.ID, err)
		}
		return nil
	}
	if !s.store.BoundedMetadataEnabled() {
		data, found := s.store.WorkspaceMetadataFields(workspace, "agentSessions")
		if !found {
			return 0
		}
		for _, session := range data.AgentSessions {
			_ = visit(session)
		}
		return swept
	}
	read, found, err := s.store.BeginAgentSessionRead(ctx, workspace)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("probe agent replies in workspace %s: %v", workspace, err)
		}
		return 0
	}
	if !found {
		return 0
	}
	defer read.Close()
	err = read.VisitHeaders(ctx, store.AgentSessionMatch{}, visit)
	if err != nil && ctx.Err() == nil {
		log.Printf("probe agent replies in workspace %s: %v", workspace, err)
	}
	return swept
}
