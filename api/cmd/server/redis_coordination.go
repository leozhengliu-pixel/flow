package main

import (
	"context"
	"errors"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func (s *server) publishRealtime(workspace string, event domain.RealtimeEvent) {
	if domain.RoutineCredentialEvent(event.Type) {
		return
	}
	s.stampChangeCursor(&event)
	if recordPeerSyncMode() == "off" {
		s.realtime.publish(workspace, event)
		s.queueSocketAccessCheck(workspace, event)
	} else if s.publishPeerEvent(workspace, event) {
		s.queueSocketAccessCheck(workspace, event)
	}
	if s.coordinator == nil {
		return
	}
	if recordPeerSyncMode() != "on" && !remoteEventKeepsState(event.Type) && !ephemeralRealtimeEvent(event.Type) {
		// Before the event (and the writer's response): a request on another
		// server then reloads the workspace instead of using its old copy.
		// Signals (agent streaming, notifications) change no workspace state.
		s.bumpWorkspaceVersion(workspace)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if recordPeerSyncMode() == "on" && event.Seq > 0 {
		// Shadow preserves the old envelope for mixed-version readers. Once
		// all peers consume SQL, Redis carries only the durable head hint.
		if raw, err := s.store.RecordJournalEventSeq(ctx, event.ID); err == nil {
			if head, err := strconv.ParseInt(raw, 10, 64); err == nil && head > 0 {
				event = domain.RealtimeEvent{ID: event.ID, Seq: head, Type: peerHeadEventType, ClientID: event.ClientID, CreatedAt: event.CreatedAt}
			}
		}
	}
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
	listenCtx, stop := context.WithCancel(listenCtx)
	context.AfterFunc(s.store.WorkerContext(), stop)
	if recordPeerSyncMode() != "off" {
		go s.pollPeerChanges(listenCtx)
	}
	go func() {
		defer stop()
		subscribedBefore := false
		for listenCtx.Err() == nil {
			err := s.coordinator.Listen(listenCtx, s.receiveRemoteEvent, func(resubscribed bool) {
				if recordPeerSyncMode() != "off" {
					for _, workspace := range s.store.WorkspaceKeys() {
						s.remoteEvents.wake(workspace, s.applyRemoteEvents)
					}
					if resubscribed || subscribedBefore {
						s.realtime.resyncAll()
						s.queueAllSocketAccessChecks()
					}
					subscribedBefore = true
					return
				}
				// The first subscription loads every workspace; after any
				// reconnect, events published while this instance was not
				// subscribed are gone, so reload and resync instead.
				missed := resubscribed || subscribedBefore
				subscribedBefore = true
				ctx, cancel := context.WithTimeout(s.store.WorkerContext(), 10*time.Second)
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
			select {
			case <-listenCtx.Done():
				return
			case <-time.After(time.Second):
			}
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

// remoteEventQueue coalesces durable heads and bounds side-channel envelopes.
// Each workspace has one worker; SQL supplies durable order and payloads.
type remoteEventQueue struct {
	mu           sync.Mutex
	pending      map[string][]domain.RealtimeEvent
	running      map[string]bool
	bytes        map[string]int
	heads        map[string]int64
	waking       map[string]bool
	overflow     map[string]bool
	consumers    map[string]*peerChangeConsumer
	clients      map[string]map[string]string
	clientBytes  map[string]int
	hintOverflow map[string]bool
}

const remoteEventQueueMaxBytes = 256 << 10
const remoteEventQueueMaxEvents = 64
const peerHeadEventType = "__peer_head"
const peerClientHintMaxBytes = 64 << 10
const peerClientHintMaxEvents = 256

func (q *remoteEventQueue) wake(workspace string, apply func(string)) {
	q.push(workspace, domain.RealtimeEvent{Type: peerHeadEventType}, apply)
}

func (q *remoteEventQueue) push(workspace string, event domain.RealtimeEvent, apply func(string)) {
	q.mu.Lock()
	if q.pending == nil {
		q.pending, q.running = map[string][]domain.RealtimeEvent{}, map[string]bool{}
		q.bytes, q.heads, q.waking, q.overflow = map[string]int{}, map[string]int64{}, map[string]bool{}, map[string]bool{}
	}
	if event.Type == peerHeadEventType || recordPeerSyncMode() != "off" && event.Seq > 0 {
		q.noteClientLocked(workspace, event)
		q.heads[workspace] = max(q.heads[workspace], event.Seq)
		q.waking[workspace] = true
	} else if !q.overflow[workspace] {
		size := realtimeEventBytes(event)
		if len(q.pending[workspace]) >= remoteEventQueueMaxEvents || size > remoteEventQueueMaxBytes-q.bytes[workspace] {
			q.pending[workspace] = []domain.RealtimeEvent{{ID: idgen.New("resync_"), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()}}
			q.bytes[workspace] = realtimeEventBytes(q.pending[workspace][0])
			q.overflow[workspace] = true
		} else {
			q.pending[workspace] = append(q.pending[workspace], event)
			q.bytes[workspace] += size
		}
	}
	start := !q.running[workspace]
	q.running[workspace] = true
	q.mu.Unlock()
	if start {
		go apply(workspace)
	}
}

func (q *remoteEventQueue) noteClientLocked(workspace string, event domain.RealtimeEvent) {
	if event.ID == "" {
		return
	}
	if q.clients == nil {
		q.clients, q.clientBytes, q.hintOverflow = map[string]map[string]string{}, map[string]int{}, map[string]bool{}
	}
	if q.hintOverflow[workspace] {
		return
	}
	if q.clients[workspace] == nil {
		q.clients[workspace] = map[string]string{}
	}
	old, exists := q.clients[workspace][event.ID]
	size := q.clientBytes[workspace] + len(event.ID) + len(event.ClientID)
	if exists {
		size -= len(event.ID) + len(old)
	}
	if size > peerClientHintMaxBytes || !exists && len(q.clients[workspace]) >= peerClientHintMaxEvents {
		delete(q.clients, workspace)
		delete(q.clientBytes, workspace)
		q.hintOverflow[workspace] = true
		return
	}
	q.clients[workspace][event.ID] = event.ClientID
	q.clientBytes[workspace] = size
}

func (q *remoteEventQueue) takeClient(workspace, id string) (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	client, exists := q.clients[workspace][id]
	if exists {
		delete(q.clients[workspace], id)
		q.clientBytes[workspace] -= len(id) + len(client)
		if len(q.clients[workspace]) == 0 {
			delete(q.clients, workspace)
			delete(q.clientBytes, workspace)
		}
	}
	return client, exists
}

func (q *remoteEventQueue) clearClients(workspace string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.clients, workspace)
	delete(q.clientBytes, workspace)
	delete(q.hintOverflow, workspace)
}

// take returns the workspace's waiting events, or none once its worker should
// stop.
func (q *remoteEventQueue) take(workspace string) []domain.RealtimeEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	batch := q.pending[workspace]
	if q.waking[workspace] {
		batch = append(batch, domain.RealtimeEvent{Type: peerHeadEventType, Seq: q.heads[workspace]})
	}
	delete(q.pending, workspace)
	delete(q.bytes, workspace)
	delete(q.heads, workspace)
	delete(q.waking, workspace)
	delete(q.overflow, workspace)
	if len(batch) == 0 {
		delete(q.running, workspace)
	}
	return batch
}

// applyRemoteEvents installs durable transactions before publishing their SQL
// events. Non-journal signals retain the compatibility reload path.
func (s *server) applyRemoteEvents(workspace string) {
	if recordPeerSyncMode() == "off" {
		s.applyLegacyRemoteEvents(workspace)
		return
	}
	for {
		batch := s.remoteEvents.take(workspace)
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(s.store.WorkerContext(), workspaceReloadTimeout)
		err := s.syncPeerWorkspace(ctx, workspace)
		if err == nil {
			for _, event := range batch {
				if event.Type == peerHeadEventType {
					continue
				}
				if event.Type == "workspace.resync_required" || strings.HasPrefix(event.Type, "workspace.") {
					err = s.store.ReloadAllWorkspaces(ctx)
				} else if !remoteEventKeepsState(event.Type) {
					err = s.store.ReloadWorkspace(ctx, workspace)
				}
				if err != nil {
					break
				}
				s.realtime.publish(workspace, event)
				s.queueSocketAccessCheck(workspace, event)
			}
		}
		cancel()
		if err != nil {
			log.Printf("Peer apply workspace=%s: %v", workspace, err)
			s.peerResync(workspace)
		}
	}
}

func (s *server) applyLegacyRemoteEvents(workspace string) {
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
		// The reload runs in the background, so it gets the reload budget, not a
		// request's: a large workspace can take longer than 5s to decode under
		// load, and a failed reload makes every client here resync (refetch
		// the bootstrap) at once, which adds the load that slowed it down. A
		// closed store (shutdown) ends it.
		ctx, cancel := context.WithTimeout(s.store.WorkerContext(), workspaceReloadTimeout)
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
			s.realtime.publish(workspace, domain.RealtimeEvent{ID: idgen.New("resync_"), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()})
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
