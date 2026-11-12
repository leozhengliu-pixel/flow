package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type syncV2StreamKey struct{}
type syncV2SidechannelKey struct{}
type syncV2Stream struct {
	workspaceID string
	cursor      int64
}

func syncResourceEvent(kind string) bool {
	if strings.HasPrefix(kind, "document.draft_") && kind != "document.draft_published" {
		return false
	}
	return syncCollection(kind) != "*"
}

func syncTransactionFrame(tx syncV2Transaction, epoch string) (EventFrame, error) {
	encode := func() (json.RawMessage, error) {
		payload, err := json.Marshal(struct {
			Schema      int               `json:"schema"`
			AuthzEpoch  string            `json:"authzEpoch"`
			Transaction syncV2Transaction `json:"transaction"`
		}{2, epoch, tx})
		if err != nil {
			return nil, err
		}
		return json.Marshal(domain.RealtimeEvent{ID: tx.TxID, Type: "sync.delta", Payload: payload})
	}
	raw, err := encode()
	if err != nil {
		return EventFrame{}, err
	}
	if len(raw)+len(tx.Seq)+128 > syncV2FrameBytes {
		change := store.CommittedChange{TxID: tx.TxID, Seq: tx.Seq, Invalidations: tx.Invalidations}
		for _, item := range tx.Upserts {
			change.Records = append(change.Records, store.RecordDelta{Key: store.RecordKey{Kind: item.Key.Kind}})
		}
		for _, item := range tx.Removals {
			change.Records = append(change.Records, store.RecordDelta{Key: store.RecordKey{Kind: item.Key.Kind}})
		}
		tx = invalidateCommittedChange(change)
		raw, err = encode()
		if err != nil {
			return EventFrame{}, err
		}
	}
	if len(raw)+len(tx.Seq)+128 > syncV2FrameBytes {
		return EventFrame{}, store.ErrEventSourceBudget
	}
	return EventFrame{encoded: raw, id: tx.Seq, visible: true, durableCursor: true}, nil
}

func (s *server) writeSyncJournalPage(w http.ResponseWriter, r *http.Request, journal committedChangeReader, cursor int64) (int64, bool, bool) {
	release, err := acquireRealtimeWork(r.Context(), s.realtime.budget, 1<<20)
	if err != nil {
		return cursor, false, false
	}
	defer release()
	access, err := s.resolveSyncAccess(r)
	if err != nil {
		return cursor, false, false
	}
	if state, ok := r.Context().Value(syncV2StreamKey{}).(*syncV2Stream); ok && state.workspaceID != access.WorkspaceID {
		return cursor, false, false
	}
	page, err := s.collectSyncChanges(r, access, journal, strconv.FormatInt(cursor, 10), maxChangesLimit)
	if err != nil {
		return cursor, false, false
	}
	if page.ResyncRequired {
		return cursor, false, writeSSE(w, domain.RealtimeEvent{ID: idgen.New("resync_"), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()}, s.realtime.node) && flushRealtime(w)
	}
	for _, tx := range page.Transactions {
		frame, err := syncTransactionFrame(tx, page.AuthzEpoch)
		if err != nil {
			return cursor, false, false
		}
		frame.validate = access.Validate
		if !frame.WriteSSE(w, s.realtime.node) || !flushRealtime(w) {
			return cursor, false, false
		}
	}
	next, err := strconv.ParseInt(page.Next, 10, 64)
	if err != nil || next < cursor || page.HasMore && next == cursor {
		return cursor, false, false
	}
	return next, page.HasMore, true
}

func (s *server) replaySyncJournal(w http.ResponseWriter, r *http.Request, journal committedChangeReader, cursor int64) (int64, bool) {
	for range maxReplayPages {
		next, more, ok := s.writeSyncJournalPage(w, r, journal, cursor)
		cursor = next
		if !ok || !more {
			return cursor, ok
		}
	}
	return cursor, writeSSE(w, domain.RealtimeEvent{ID: idgen.New("resync_"), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()}, s.realtime.node) && flushRealtime(w)
}

func (s *server) writeSyncV2Batch(w http.ResponseWriter, r *http.Request, state *syncV2Stream, batch []domain.RealtimeEvent) bool {
	journal, ok := s.syncV2Journal()
	if !ok {
		return false
	}
	hasChanges := false
	sidechannelRequest := r.WithContext(context.WithValue(r.Context(), syncV2SidechannelKey{}, true))
	for _, event := range batch {
		if syncResourceEvent(event.Type) {
			hasChanges = true
		}
	}
	if hasChanges {
		next, ok := s.replaySyncJournal(w, r, journal, state.cursor)
		if !ok || next < state.cursor {
			return false
		}
		state.cursor = next
	}
	for _, event := range batch {
		if event.Seq > 0 && syncResourceEvent(event.Type) {
			continue
		}
		event.Seq = 0
		if !s.writeProjectedRealtime(w, sidechannelRequest, event) {
			return false
		}
	}
	return flushRealtime(w)
}

func (s *realtimeSubscriber) setSyncV2QueueLimit() {
	s.mu.Lock()
	s.maxBytes = 32 << 10
	overflow := s.retained > s.maxBytes
	s.mu.Unlock()
	if overflow {
		s.collapse(domain.RealtimeEvent{ID: idgen.New("resync_"), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()})
	}
}

func (s *server) attachSyncStream(r *http.Request, workspaceID string, cursor int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), syncV2StreamKey{}, &syncV2Stream{workspaceID: workspaceID, cursor: cursor}))
}
