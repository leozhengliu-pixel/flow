package main

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
)

func (s *server) publishRealtime(workspace string, event domain.RealtimeEvent) {
	s.realtime.publish(workspace, event)
	if s.coordinator == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.coordinator.Publish(ctx, workspace, event); err != nil {
		log.Printf("Redis publish workspace=%s event=%s: %v", workspace, event.Type, err)
	}
}

func (s *server) startCoordination() {
	if s.coordinator == nil || !s.coordinationStarted.CompareAndSwap(false, true) {
		return
	}
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := s.store.ReloadAllWorkspaces(ctx); err != nil {
				log.Printf("Redis coordination initial refresh: %v", err)
			}
			cancel()
			err := s.coordinator.Listen(context.Background(), func(envelope coordination.EventEnvelope) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				var err error
				if slices.Contains([]string{"workspace.created", "workspace.updated", "workspace.deleted"}, envelope.Event.Type) {
					err = s.store.ReloadAllWorkspaces(ctx)
				} else {
					err = s.store.ReloadWorkspace(ctx, envelope.Workspace)
				}
				cancel()
				if err != nil {
					log.Printf("Redis reload workspace=%s event=%s: %v", envelope.Workspace, envelope.Event.Type, err)
					return
				}
				s.realtime.publish(envelope.Workspace, envelope.Event)
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("Redis event subscriber: %v; reconnecting", err)
			}
			time.Sleep(time.Second)
		}
	}()
}
