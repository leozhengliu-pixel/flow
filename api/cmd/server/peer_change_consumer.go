package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

const peerChangePollInterval = 350 * time.Millisecond
const peerChangePageBytes = 256 << 10

func recordPeerSyncMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_RECORD_PEER_SYNC"))) {
	case "off", "0", "false":
		return "off"
	case "on":
		return "on"
	default:
		return "shadow"
	}
}

// The gate serializes requests and the background worker for one workspace.
// Redis heads never become applied cursors: only successful SQL installs do.
type peerChangeConsumer struct {
	gate        chan struct{}
	workspaceID string
	authzEpoch  string
	cursor      int64
	eventCursor int64
	initialized bool
	users       int // guarded by remoteEventQueue.mu, including gate waiters
}

func (q *remoteEventQueue) consumer(workspace string) *peerChangeConsumer {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.consumerLocked(workspace)
}

func (q *remoteEventQueue) consumerLocked(workspace string) *peerChangeConsumer {
	if q.consumers == nil {
		q.consumers = map[string]*peerChangeConsumer{}
	}
	c := q.consumers[workspace]
	if c == nil {
		c = &peerChangeConsumer{gate: make(chan struct{}, 1)}
		q.consumers[workspace] = c
	}
	return c
}

func (s *server) syncPeerWorkspace(ctx context.Context, workspace string) error {
	s.remoteEvents.mu.Lock()
	c := s.remoteEvents.consumerLocked(workspace)
	c.users++
	s.remoteEvents.mu.Unlock()
	defer func() {
		s.remoteEvents.mu.Lock()
		c.users--
		s.remoteEvents.mu.Unlock()
	}()
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	state, err := s.store.WorkspacePeerState(ctx, workspace)
	if errors.Is(err, sql.ErrNoRows) {
		// ReloadWorkspace cannot remove a deleted or renamed key.
		if err := s.store.ReloadAllWorkspaces(ctx); err != nil {
			return err
		}
		s.remoteEvents.clearClients(workspace)
		s.peerResync(workspace)
		return nil
	}
	if err != nil {
		return err
	}
	if !c.initialized || c.workspaceID != state.WorkspaceID {
		// Capture H0 BEFORE loading the snapshot. Commits racing that load
		// are replayed after H0 rather than skipped by a later head read.
		if err := s.store.ReloadWorkspace(ctx, workspace); err != nil {
			return err
		}
		c.workspaceID, c.cursor, c.initialized = state.WorkspaceID, state.Head, true
		c.eventCursor = state.EventHead
		c.authzEpoch = state.AuthzEpoch
		baseline := store.CommittedPage{Next: strconv.FormatInt(state.Head, 10), Head: strconv.FormatInt(state.Head, 10)}
		_, _ = s.store.ApplyCommittedPage(ctx, state.WorkspaceID, baseline)
		s.remoteEvents.clearClients(workspace)
		if !state.Current {
			s.peerResync(workspace)
		}
		return nil
	}
	authzChanged := c.authzEpoch != state.AuthzEpoch
	if state.Current && state.Head == c.cursor && state.EventHead == c.eventCursor && !state.Pending && !authzChanged {
		return nil
	}
	page, readErr := s.store.ReadCommittedChanges(ctx, c.workspaceID, strconv.FormatInt(c.cursor, 10), 100, peerChangePageBytes)
	if readErr != nil && !errors.Is(readErr, store.ErrRecordJournalUnavailable) && !errors.Is(readErr, store.ErrRecordJournalCoverage) {
		return readErr
	}
	through := state.Head
	if readErr == nil && !page.ResyncRequired && !page.HasMore {
		through, err = strconv.ParseInt(page.Next, 10, 64)
		if err != nil || through < c.cursor {
			return store.ErrMetadataCursor
		}
		if recordPeerSyncMode() == "on" {
			_, err = s.store.ApplyCommittedPage(ctx, c.workspaceID, page)
		} else {
			var equivalent bool
			equivalent, err = s.store.ShadowCommittedPage(ctx, c.workspaceID, page)
			if err == nil && !equivalent {
				log.Printf("Peer journal shadow differs workspace=%s after=%d next=%s", workspace, c.cursor, page.Next)
			}
		}
		if err != nil && !errors.Is(err, store.ErrPeerSnapshotRequired) && !errors.Is(err, store.ErrRecordJournalCoverage) {
			return err
		}
	} else {
		err = store.ErrPeerSnapshotRequired
	}
	if err != nil {
		// Old writers, retention, large transactions and unsupported shapes
		// retain the complete compatibility snapshot. Never apply half a tx.
		through = state.Head
		if err := s.store.ReloadWorkspace(ctx, workspace); err != nil {
			return err
		}
		baseline := store.CommittedPage{Next: strconv.FormatInt(through, 10), Head: strconv.FormatInt(through, 10)}
		_, _ = s.store.ApplyCommittedPage(ctx, c.workspaceID, baseline)
	}
	if through < c.cursor {
		s.peerResync(workspace)
		c.cursor = through
		c.eventCursor = state.EventHead
		return nil
	}
	journalChanged := through != c.cursor
	c.cursor = through
	if authzChanged {
		s.queuePeerSocketAccessCheck(workspace)
	}
	c.authzEpoch = state.AuthzEpoch
	through = state.EventHead
	if through < c.eventCursor {
		s.peerResync(workspace)
		c.eventCursor = through
		return nil
	}
	if through == c.eventCursor {
		if !state.Current || authzChanged || journalChanged {
			// SQL-only changes and commits awaiting event sequencing have no
			// legacy event to publish. Invalidate clients after installation.
			s.peerResync(workspace)
		}
		return nil
	}
	// The full state is installed before the first event is visible. Replay is
	// byte bounded and sparse/oversized logs recover through a resync marker.
	window := store.RealtimeReplayWindow{WorkspaceID: c.workspaceID, Head: through, MaxRecordBytes: peerChangePageBytes}
	for c.eventCursor < through {
		events, err := s.store.ReadRealtimeReplayPage(ctx, window, c.eventCursor, peerChangePageBytes)
		if err != nil {
			return err
		}
		if events.ResyncRequired || len(events.Records) == 0 {
			s.peerResync(workspace)
			c.eventCursor = through
			return nil
		}
		for _, record := range events.Records {
			if domain.RoutineCredentialEvent(record.Event.Type) {
				c.eventCursor = record.Seq
				continue
			}
			event := domain.RealtimeEvent{ID: record.Event.ID, Seq: record.Seq, Type: record.Event.Type, AggregateID: record.Event.AggregateID, ActorID: record.ActorID, Payload: record.Event.Payload, CreatedAt: record.Event.CreatedAt}
			client, signaled := s.remoteEvents.takeClient(workspace, event.ID)
			if !signaled && !s.peerEventPublished(workspace, event.ID) {
				// The legacy live envelope's client correlation was lost. Its
				// existing reconnect contract is a resync, not an own-write echo.
				s.remoteEvents.clearClients(workspace)
				s.peerResync(workspace)
				c.eventCursor = through
				return nil
			}
			event.ClientID = client
			if s.publishPeerEvent(workspace, event) {
				s.queueSocketAccessCheck(workspace, event)
			}
			c.eventCursor = record.Seq
		}
	}
	return nil
}

func (s *server) peerEventPublished(workspace, id string) bool {
	s.realtime.mu.Lock()
	defer s.realtime.mu.Unlock()
	for _, event := range s.realtime.history[workspace] {
		if event.ID == id {
			return true
		}
	}
	return false
}

func (s *server) publishPeerEvent(workspace string, event domain.RealtimeEvent) bool {
	if domain.RoutineCredentialEvent(event.Type) {
		return false
	}
	if event.Seq == 0 {
		s.realtime.publish(workspace, event)
		return true
	}
	s.realtime.mu.Lock()
	defer s.realtime.mu.Unlock()
	for _, delivered := range s.realtime.history[workspace] {
		if delivered.ID == event.ID {
			return false
		}
	}
	if s.realtime.ordered {
		s.realtime.publishInOrderLocked(workspace, event)
	} else {
		s.realtime.deliverLocked(workspace, event)
	}
	return true
}

func (s *server) peerResync(workspace string) {
	s.realtime.publish(workspace, domain.RealtimeEvent{ID: idgen.New("resync_"), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()})
	s.queuePeerSocketAccessCheck(workspace)
}

func (s *server) queuePeerSocketAccessCheck(workspace string) {
	if !s.realtime.hasSockets(workspace) {
		return
	}
	scope := socketAccessScope{all: true}
	s.realtime.socketAccessWork(workspace, scope)
	s.socketAccess.push(workspace, scope, s.runSocketAccessChecks)
}

func (s *server) pollPeerChanges(ctx context.Context) {
	ticker := time.NewTicker(peerChangePollInterval)
	defer ticker.Stop()
	for {
		workspaces := s.store.WorkspaceKeys()
		for _, workspace := range workspaces {
			s.remoteEvents.wake(workspace, s.applyRemoteEvents)
		}
		s.remoteEvents.pruneConsumers(workspaces)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (q *remoteEventQueue) pruneConsumers(workspaces []string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.consumers) <= len(workspaces)+8 {
		return
	}
	present := make(map[string]bool, len(workspaces))
	for _, key := range workspaces {
		present[key] = true
	}
	for key, consumer := range q.consumers {
		if !present[key] && !q.running[key] && consumer.users == 0 {
			delete(q.consumers, key)
			delete(q.clients, key)
			delete(q.clientBytes, key)
			delete(q.hintOverflow, key)
		}
	}
}
