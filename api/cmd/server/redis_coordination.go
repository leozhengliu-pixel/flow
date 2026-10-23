package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
)

func (s *server) publishRealtime(workspace string, event domain.RealtimeEvent) {
	if domain.RoutineCredentialEvent(event.Type) {
		return
	}
	s.stampChangeCursor(&event)
	s.realtime.publish(workspace, event)
	s.queueSocketAccessCheck(workspace, event)
	if s.coordinator == nil {
		return
	}
	if !remoteEventKeepsState(event.Type) && !ephemeralRealtimeEvent(event.Type) {
		// Before the event (and the writer's response): a request on another
		// server then reloads the workspace instead of using its old copy.
		// Signals (agent streaming, notifications) change no workspace state.
		s.bumpWorkspaceVersion(workspace)
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
	listenCtx := s.coordinationContext
	if listenCtx == nil {
		listenCtx = context.Background()
	}
	go func() {
		subscribedBefore := false
		for listenCtx.Err() == nil {
			err := s.coordinator.Listen(listenCtx, s.receiveRemoteEvent, func(resubscribed bool) {
				// The first subscription loads every workspace; after any
				// reconnect, events published while this instance was not
				// subscribed are gone, so reload and resync instead.
				missed := resubscribed || subscribedBefore
				subscribedBefore = true
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := s.store.ReloadAllWorkspaces(ctx)
				cancel()
				if err != nil {
					log.Printf("Redis coordination refresh: %v", err)
				}
				if missed {
					log.Printf("Redis event subscription restored; resyncing realtime subscribers")
					s.realtime.resyncAll()
					// Access changes may be among the lost events.
					s.queueAllSocketAccessChecks()
				}
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("Redis event subscriber: %v; reconnecting", err)
			}
			time.Sleep(time.Second)
		}
	}()
}

// receiveRemoteEvent takes a peer's event off the Redis subscription. It must
// not block: the subscription buffers a limited number of messages.
func (s *server) receiveRemoteEvent(envelope coordination.EventEnvelope) {
	if domain.RoutineCredentialEvent(envelope.Event.Type) {
		return
	}
	if envelope.Event.Type == agentAnswerEventType {
		// An answer for a chat reply running here; never shown to browsers (agent_live_answer.go).
		go s.receiveForwardedAgentAnswer(envelope.Workspace, envelope.Event)
		return
	}
	if s.receiveCollaborationEvent(envelope.Workspace, envelope.Event) {
		return
	}
	s.remoteEvents.push(envelope.Workspace, envelope.Event, s.applyRemoteEvents)
}

// remoteEventQueue holds peer events per workspace until a worker applies
// them. Each workspace has at most one worker, so its events keep their
// order, and a burst shares one reload instead of one per event.
type remoteEventQueue struct {
	mu      sync.Mutex
	pending map[string][]domain.RealtimeEvent
	running map[string]bool
}

func (q *remoteEventQueue) push(workspace string, event domain.RealtimeEvent, apply func(string)) {
	q.mu.Lock()
	if q.pending == nil {
		q.pending, q.running = map[string][]domain.RealtimeEvent{}, map[string]bool{}
	}
	q.pending[workspace] = append(q.pending[workspace], event)
	start := !q.running[workspace]
	q.running[workspace] = true
	q.mu.Unlock()
	if start {
		go apply(workspace)
	}
}

// take returns the workspace's waiting events, or none once its worker should
// stop.
func (q *remoteEventQueue) take(workspace string) []domain.RealtimeEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	batch := q.pending[workspace]
	delete(q.pending, workspace)
	if len(batch) == 0 {
		delete(q.running, workspace)
	}
	return batch
}

// applyRemoteEvents reloads the workspace once for the waiting peer events,
// then publishes them to this instance's subscribers.
func (s *server) applyRemoteEvents(workspace string) {
	for {
		batch := s.remoteEvents.take(workspace)
		if len(batch) == 0 {
			return
		}
		all, one := false, false
		for _, event := range batch {
			switch {
			case slices.Contains([]string{"workspace.created", "workspace.updated", "workspace.deleted"}, event.Type):
				all = true
			case !remoteEventKeepsState(event.Type):
				one = true
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var err error
		// Read before reloading: the reload includes every change it counts.
		version, versionErr := int64(0), error(nil)
		if all || one {
			version, versionErr = s.sharedWorkspaceVersion(ctx, workspace)
		}
		if one && !all && versionErr == nil && version > 0 && s.workspaceVersions.current(workspace) >= version && s.store.HasWorkspace(workspace) {
			// A request already reloaded past these changes (ensureCurrentWorkspace).
			one = false
		}
		if all {
			err = s.store.ReloadAllWorkspaces(ctx)
		} else if one {
			err = s.store.ReloadWorkspace(ctx, workspace)
		}
		cancel()
		if err == nil && versionErr == nil && version > 0 {
			s.workspaceVersions.record(workspace, version)
		}
		if err != nil {
			// Publishing would let subscribers here fetch the stale copy as if
			// it had the change; the next applied batch reloads it, and the
			// resync makes them refetch then.
			log.Printf("Redis reload workspace=%s events=%d: %v", workspace, len(batch), err)
			s.realtime.publish(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("resync_%d", time.Now().UnixNano()), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()})
			continue
		}
		for _, event := range batch {
			s.realtime.publish(workspace, event)
			// After the reload: the check reads the changed access.
			s.queueSocketAccessCheck(workspace, event)
		}
	}
}

// remoteEventKeepsState reports peer events that change nothing this instance
// holds in memory, so they need no reload: loop run signals and presence (the
// roster lives in Redis and travels in the payload).
func remoteEventKeepsState(eventType string) bool {
	return strings.HasPrefix(eventType, "loop_run.") || eventType == "presence.updated"
}

// realtimeNodeTag is the short instance tag on stream ids (realtimeHub.node).
func realtimeNodeTag(instanceID string) string {
	if len(instanceID) > 12 {
		return instanceID[:12]
	}
	return instanceID
}
