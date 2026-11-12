package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"

	"github.com/coder/websocket"
)

const (
	// Together with the 16 MiB replay ring and 32 MiB payload cache this
	// leaves 16 MiB of the 128 MiB envelope for indexes and allocator overhead.
	realtimeTransportBytes = 64 << 20
	realtimeHistoryBytes   = 16 << 20
	realtimeWorkBytes      = 32 << 20
	realtimeWriteTimeout   = 5 * time.Second
)

type realtimeBudget struct {
	limit    int64
	used     atomic.Int64
	peak     atomic.Int64
	mu       sync.Mutex
	changed  chan struct{}
	waiters  atomic.Int32
	eventsMu sync.Mutex
	events   map[realtimeEventKey]*realtimeEventLease
}

type realtimeEventKey struct {
	id, kind string
	payload  unsafe.Pointer
	capacity int
}
type realtimeEventLease struct {
	event domain.RealtimeEvent
	bytes int64
	refs  int
}
type realtimePendingEvent struct {
	key  realtimeEventKey
	size int
}

func eventKey(event domain.RealtimeEvent) realtimeEventKey {
	return realtimeEventKey{event.ID, event.Type, unsafe.Pointer(unsafe.SliceData(event.Payload)), cap(event.Payload)}
}
func (b *realtimeBudget) retainEvent(event domain.RealtimeEvent) bool {
	if b == nil {
		return true
	}
	b.eventsMu.Lock()
	defer b.eventsMu.Unlock()
	key := eventKey(event)
	entry := b.events[key]
	size := int64(256)
	if entry == nil {
		size += int64(realtimeEventBytes(event)) + 512
	}
	if !b.acquire(size) {
		return false
	}
	if entry == nil {
		if b.events == nil {
			b.events = map[realtimeEventKey]*realtimeEventLease{}
		}
		entry = &realtimeEventLease{event: event, bytes: size - 256}
		b.events[key] = entry
	}
	entry.refs++
	return true
}
func (b *realtimeBudget) releaseEvent(key realtimeEventKey) {
	if b == nil {
		return
	}
	b.eventsMu.Lock()
	defer b.eventsMu.Unlock()
	entry := b.events[key]
	if entry == nil {
		return
	}
	entry.refs--
	size := int64(256)
	if entry.refs == 0 {
		size += entry.bytes
		delete(b.events, key)
		if len(b.events) == 0 {
			b.events = nil
		}
	}
	b.release(size)
}

func newRealtimeBudget() *realtimeBudget {
	limit := int64(realtimeTransportBytes)
	// A rollback may reduce this bound, never disable it or increase it past
	// the process envelope. Invalid/zero values keep the safe default.
	if configured, err := strconv.ParseInt(os.Getenv("FLOW_REALTIME_BUDGET_BYTES"), 10, 64); err == nil && configured >= 48<<20 && configured < limit {
		limit = configured
	}
	return &realtimeBudget{limit: limit}
}

func (b *realtimeBudget) acquire(size int64) bool {
	if b == nil {
		return true
	}
	for {
		used := b.used.Load()
		if size < 0 || size > b.limit-used {
			return false
		}
		if b.used.CompareAndSwap(used, used+size) {
			for peak := b.peak.Load(); used+size > peak && !b.peak.CompareAndSwap(peak, used+size); peak = b.peak.Load() {
			}
			return true
		}
	}
}

func (b *realtimeBudget) release(size int64) {
	if b != nil {
		b.used.Add(-size)
		if b.waiters.Load() == 0 {
			return
		}
		b.mu.Lock()
		if b.changed != nil {
			close(b.changed)
			b.changed = nil
		}
		b.mu.Unlock()
	}
}

func (b *realtimeBudget) wait(ctx context.Context, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.acquire(size) {
		return nil
	}
	b.waiters.Add(1)
	defer b.waiters.Add(-1)
	ctx, cancel := context.WithTimeout(ctx, realtimeWriteTimeout)
	defer cancel()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b.mu.Lock()
		if b.acquire(size) {
			b.mu.Unlock()
			return nil
		}
		if b.changed == nil {
			b.changed = make(chan struct{})
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

type realtimeSubscriber struct {
	mu       sync.Mutex
	channel  chan domain.RealtimeEvent
	budget   *realtimeBudget
	base     int64
	retained int64
	managed  bool
	closed   bool
	// Only unmanaged callers receive directly without acknowledging a batch.
	pending []realtimePendingEvent
	held    map[realtimeEventKey]int
}

func realtimeEventBytes(event domain.RealtimeEvent) int {
	return cap(event.Payload) + len(event.ID) + len(event.Type) + len(event.AggregateID) + len(event.ActorID) + len(event.ClientID) + 256
}

func (s *realtimeSubscriber) reconcileLocked() {
	if s.managed {
		return
	}
	consumed := max(0, len(s.pending)-len(s.channel))
	for _, item := range s.pending[:consumed] {
		s.releaseLocked(item.key, item.size)
	}
	s.pending = s.pending[consumed:]
}

func (s *realtimeSubscriber) releaseLocked(key realtimeEventKey, size int) {
	if s.held[key] == 0 {
		return
	}
	s.held[key]--
	if s.held[key] == 0 {
		delete(s.held, key)
	}
	s.retained -= int64(size)
	s.budget.releaseEvent(key)
}

func (s *realtimeSubscriber) push(event domain.RealtimeEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked()
	size := int64(realtimeEventBytes(event))
	if s.closed || s.retained+size > realtimeSubscriberQueueBytes || !s.budget.retainEvent(event) {
		return false
	}
	select {
	case s.channel <- event:
		s.retained += size
		key := eventKey(event)
		if s.held == nil {
			s.held = map[realtimeEventKey]int{}
		}
		s.held[key]++
		if !s.managed {
			s.pending = append(s.pending, realtimePendingEvent{key, int(size)})
		}
		return true
	default:
		s.budget.releaseEvent(eventKey(event))
		return false
	}
}

func (s *realtimeSubscriber) releaseEvent(event domain.RealtimeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseLocked(eventKey(event), realtimeEventBytes(event))
}

func (s *realtimeSubscriber) collapse(event domain.RealtimeEvent) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.reconcileLocked()
drain:
	for {
		select {
		case queued := <-s.channel:
			s.releaseLocked(eventKey(queued), realtimeEventBytes(queued))
		default:
			break drain
		}
	}
	s.pending = nil
	s.mu.Unlock()
	if !s.push(domain.RealtimeEvent{ID: event.ID, Type: "workspace.resync_required", CreatedAt: event.CreatedAt}) {
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.channel)
		}
		s.mu.Unlock()
	}
}

func (s *realtimeSubscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.channel)
	}
	for range s.channel {
	}
	for key, count := range s.held {
		for range count {
			s.budget.releaseEvent(key)
		}
	}
	s.budget.release(s.base)
	s.retained, s.base, s.pending, s.held = 0, 0, nil, nil
}

func (h *realtimeHub) subscribeTransport(workspace, since string, durable bool) (*realtimeSubscriber, func()) {
	if durable {
		since = ""
	}
	return h.subscribeLockedMode(workspace, since, true)
}

// A lease survives projection, Write and Flush. ResponseController unwraps
// middleware, so the deadline covers a blocked socket flush as well.
type realtimeBudgetWriter struct {
	http.ResponseWriter
	budget       *realtimeBudget
	frames       []realtimeEventKey
	encodedBytes int64
}

func (w *realtimeBudgetWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *realtimeBudgetWriter) Flush() {
	_ = w.FlushError()
}

func (w *realtimeBudgetWriter) FlushError() error {
	defer w.releaseFrames()
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *realtimeBudgetWriter) releaseFrames() {
	for _, key := range w.frames {
		w.budget.releaseEvent(key)
	}
	w.frames = nil
	w.budget.release(w.encodedBytes)
	w.encodedBytes = 0
}
func (w *realtimeBudgetWriter) reserveEncoding(bytes int64) bool {
	if !w.budget.acquire(bytes) {
		return false
	}
	w.encodedBytes += bytes
	return true
}

func flushRealtime(w http.ResponseWriter) bool {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(realtimeWriteTimeout))
	return controller.Flush() == nil
}

func (s *server) writeRealtimeBatch(w http.ResponseWriter, r *http.Request, subscriber *realtimeSubscriber, batch []domain.RealtimeEvent, durable bool, replaySeq int64) bool {
	defer func() {
		for _, event := range batch {
			subscriber.releaseEvent(event)
		}
	}()
	for index, event := range batch {
		if durable && event.Seq > 0 && event.Seq <= replaySeq {
			continue
		}
		if event.Type == "presence.updated" {
			superseded := false
			for _, later := range batch[index+1:] {
				superseded = superseded || later.Type == event.Type
			}
			if superseded {
				continue
			}
		}
		if !s.writeProjectedRealtime(w, r, event) {
			return false
		}
	}
	return flushRealtime(w)
}

func (s *server) writeProjectedRealtime(w http.ResponseWriter, r *http.Request, event domain.RealtimeEvent) bool {
	release, err := acquireRealtimeWork(r.Context(), s.realtime.budget, 8<<10)
	if err != nil {
		return false
	}
	defer release()
	projected, visible, err := s.pagedRealtimeEvent(r, event)
	if err != nil {
		return false
	}
	if !visible {
		return true
	}
	if realtimeEventBytes(projected) > realtimeWorkBytes/2 {
		projected = domain.RealtimeEvent{ID: event.ID, Type: "workspace.resync_required", CreatedAt: time.Now().UTC()}
	}
	return writeSSE(w, projected, s.realtime.node) && flushRealtime(w)
}

func (s *server) replayBudgetedChanges(w http.ResponseWriter, r *http.Request, workspaceID string, since int64) (int64, bool) {
	cursor := since
	window, err := s.store.OpenRealtimeReplay(r.Context(), workspaceID, since)
	if err != nil {
		return cursor, false
	}
	for range (maxReplayPages*maxChangesLimit + 63) / 64 {
		next, more, ok := s.replayBudgetedPage(w, r, window, cursor)
		cursor = next
		if !ok || !more {
			return cursor, ok
		}
	}
	return cursor, writeSSE(w, domain.RealtimeEvent{ID: idgen.New("resync_"), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()}, s.realtime.node) && flushRealtime(w)
}

func (s *server) replayBudgetedRecord(w http.ResponseWriter, r *http.Request, workspaceID string, cursor int64) (int64, bool, bool) {
	window, err := s.store.OpenRealtimeReplay(r.Context(), workspaceID, cursor)
	if err != nil {
		return cursor, false, false
	}
	return s.replayBudgetedPage(w, r, window, cursor)
}

func (s *server) replayBudgetedPage(w http.ResponseWriter, r *http.Request, window store.RealtimeReplayWindow, cursor int64) (int64, bool, bool) {
	release, err := acquireRealtimeWork(r.Context(), s.realtime.budget, 9<<20)
	if err != nil {
		return cursor, false, false
	}
	defer release()
	page, err := s.store.ReadRealtimeReplayPage(r.Context(), window, cursor, 1<<20)
	if err != nil {
		return cursor, false, false
	}
	if page.ResyncRequired {
		return max(cursor, page.Head), false, writeSSE(w, domain.RealtimeEvent{ID: idgen.New("resync_"), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()}, s.realtime.node) && flushRealtime(w)
	}
	for _, record := range page.Records {
		cursor = record.Seq
		event := domain.RealtimeEvent{ID: record.Event.ID, Seq: record.Seq, Type: record.Event.Type, AggregateID: record.Event.AggregateID, ActorID: record.ActorID, Payload: record.Event.Payload, CreatedAt: record.Event.CreatedAt}
		if !s.writeProjectedRealtime(w, r, event) {
			return cursor, false, false
		}
	}
	return cursor, page.HasMore, true
}

func (s *server) handleBudgetedCollaboration(ctx context.Context, r *http.Request, client *realtimeSocketClient, kind websocket.MessageType, message []byte) error {
	workBytes := int64(len(message)*8 + 64<<10)
	if kind == websocket.MessageText {
		workBytes = int64(len(message)*2 + 4096)
	}
	release, err := acquireRealtimeWork(ctx, client.budget, workBytes)
	if err != nil {
		return fmt.Errorf("%w: %v", errRealtimeBudget, err)
	}
	defer release()
	switch kind {
	case websocket.MessageText:
		return s.handleCollaborationCommand(r, client, message)
	case websocket.MessageBinary:
		return s.handleCollaborationFrame(r, client, message)
	}
	return nil
}

type sharedSocketPayload struct {
	data     []byte
	budget   *realtimeBudget
	refs     atomic.Int64
	reserved int64
}

func (p *sharedSocketPayload) release() {
	if p != nil && p.refs.Add(-1) == 0 {
		p.budget.release(p.reserved)
	}
}

func (message realtimeSocketMessage) release(client *realtimeSocketClient) {
	client.queuedBytes.Add(-int64(len(message.data)))
	client.budget.release(message.reserved)
	message.shared.release()
}

func subscriberBaseBytes(capacity int) int64 {
	return int64(capacity)*int64(unsafe.Sizeof(domain.RealtimeEvent{})+8) + 2048
}

// Authorize after dequeue as well: a revocation can leave older frames in
// the send channel even after the socket has left the document.
func (s *server) socketMessageAllowed(client *realtimeSocketClient, message realtimeSocketMessage) bool {
	if message.documentID == "" {
		return true
	}
	h := s.realtime
	h.mu.Lock()
	_, joined := client.documents[message.documentID]
	issueID := client.issueDocuments[message.documentID]
	revoked := client.credentialRevoked
	h.mu.Unlock()
	if !joined || revoked || client.checkCredential != nil && !client.checkCredential() {
		return false
	}
	if issueID != "" && client.checkIssue != nil {
		allowed, err := s.recheckIssueDocument(client, message.documentID, issueID)
		if err != nil {
			client.cancel()
		}
		return allowed && err == nil
	}
	if client.checkDocument != nil {
		role, conflict, err := client.checkDocument(message.documentID)
		if err != nil || conflict {
			client.cancel()
			return false
		}
		if role == "none" {
			h.revokeDocument(client, message.documentID, "")
			return false
		}
		h.setEditAccess(client, message.documentID, canEditDocument(role))
		return h.joinedDocument(client, message.documentID)
	}
	return true
}

var errRealtimeBudget = errors.New("realtime transport budget exceeded")

func acquireRealtimeWork(ctx context.Context, budget *realtimeBudget, size int64) (func(), error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := budget.wait(ctx, size); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { budget.release(size) }) }, nil
}

// Grow only after reserving the new allocation. Idle readers hold no full
// frame reservation; 2,000 open sockets must still be able to wait for edits.
func readBudgetedCollaboration(ctx context.Context, connection *websocket.Conn, budget *realtimeBudget) (websocket.MessageType, []byte, func(), error) {
	kind, reader, err := connection.Reader(ctx)
	if err != nil {
		return kind, nil, func() {}, err
	}
	var raw []byte
	var held int64
	release := func() { budget.release(held) }
	for {
		if len(raw) == cap(raw) {
			if len(raw) == maxCollaborationFrameSize {
				var extra [1]byte
				n, err := reader.Read(extra[:])
				if n == 0 && err == io.EOF {
					return kind, raw, release, nil
				}
				release()
				return kind, nil, func() {}, errors.New("collaboration frame exceeded")
			}
			capacity := min(maxCollaborationFrameSize, max(4096, cap(raw)*2))
			if !budget.acquire(int64(capacity)) {
				release()
				return kind, nil, func() {}, errRealtimeBudget
			}
			next := make([]byte, len(raw), capacity)
			copy(next, raw)
			budget.release(held)
			raw, held = next, int64(capacity)
		}
		n, err := reader.Read(raw[len(raw):cap(raw)])
		raw = raw[:len(raw)+n]
		if err == io.EOF {
			return kind, raw, release, nil
		}
		if err != nil {
			release()
			return kind, nil, func() {}, err
		}
	}
}
