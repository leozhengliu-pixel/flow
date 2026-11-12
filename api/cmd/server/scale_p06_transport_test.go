package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/coder/websocket"
)

func TestScaleP06ReplayAndDequeuedBatchStayBudgeted(t *testing.T) {
	h := newRealtimeHub()
	h.publish("w", domain.RealtimeEvent{ID: "cursor"})
	payload := json.RawMessage(`{"body":"` + strings.Repeat("a", 200<<10) + `"}`)
	for i := range 10 {
		h.publish("w", domain.RealtimeEvent{ID: fmt.Sprint(i), Type: "document.updated", Payload: payload})
	}
	subscriber, stop := h.subscribeTransport("w", "cursor", false)
	if len(subscriber.channel) != 1 || (<-subscriber.channel).Type != "workspace.resync_required" {
		t.Fatal("oversized replay must collapse atomically to a resync")
	}
	stop()
	if got := h.budget.used.Load(); got != 0 {
		t.Fatalf("replay leaked %d bytes", got)
	}

	subscriber, stop = h.subscribeTransport("w", "", false)
	h.publish("w", domain.RealtimeEvent{ID: "live", Payload: payload})
	before := h.budget.used.Load()
	event := <-subscriber.channel
	if got := h.budget.used.Load(); got != before {
		t.Fatal("dequeue released the in-flight batch")
	}
	h.publish("w", domain.RealtimeEvent{ID: "concurrent", Payload: payload})
	if subscriber.retained < int64(2*realtimeEventBytes(event)) {
		t.Fatal("in-flight and queued events must both count")
	}
	subscriber.releaseEvent(event)
	stop()
	if h.budget.used.Load() != 0 {
		t.Fatal("unsubscribe leaked batch/queue storage")
	}
}

func TestScaleP06SharedCollaborationAndInFlightAccounting(t *testing.T) {
	h := newRealtimeHub()
	first := h.addSocket("w", "first", func() {})
	second := h.addSocket("w", "second", func() {})
	h.joinDocument(first, "document")
	h.joinDocument(second, "document")
	data := []byte{1, 2, 3}
	h.broadcastDocument("w", "document", 0, realtimeSocketMessage{binary: true, data: data})
	data[0] = 9
	before := h.budget.used.Load()
	a, b := <-first.send, <-second.send
	if a.data[0] != 1 || &a.data[0] != &b.data[0] {
		t.Fatal("broadcast must own one immutable payload shared by its recipients")
	}
	if h.budget.used.Load() != before || first.queuedBytes.Load() != 3 {
		t.Fatal("dequeue released the frame before its write completed")
	}
	a.release(first)
	if second.queuedBytes.Load() != 3 {
		t.Fatal("first writer released another writer's frame")
	}
	b.release(second)
	h.removeSocket(first)
	h.removeSocket(second)
	if h.budget.used.Load() != 0 {
		t.Fatal("socket teardown leaked reservations")
	}
}

func TestScaleP06TwoThousandSlowConnectionsBounded(t *testing.T) {
	h := newRealtimeHub()
	stops := make([]func(), 0, 2000)
	for i := range 1000 {
		subscriber, stop := h.subscribeTransport("w", "", false)
		if subscriber.closed {
			t.Fatal("idle SSE connection rejected")
		}
		stops = append(stops, stop)
		client := h.addSocket("w", fmt.Sprint(i), func() {})
		if client == nil {
			t.Fatal("idle collaboration connection rejected")
		}
		h.joinDocument(client, "document")
		stops = append(stops, func() { h.removeSocket(client) })
	}
	for i := range 100 {
		payload := bytes.Repeat([]byte{byte(i)}, 64<<10)
		h.broadcastDocument("w", "document", 0, realtimeSocketMessage{binary: true, data: payload})
		h.publish("w", domain.RealtimeEvent{ID: fmt.Sprint(i), Type: "issue.updated", Payload: payload})
	}
	total := h.budget.peak.Load() + int64(h.historyTotal) + realtimePayloadCacheBytes
	if total > 128<<20 || h.budget.used.Load() > h.budget.limit {
		t.Fatalf("transport + ring + cache = %d, want <= 128 MiB", total)
	}
	t.Logf("2000 mixed slow connections: peak_transport=%d history=%d cache_ceiling=%d total_ceiling=%d", h.budget.peak.Load(), h.historyTotal, realtimePayloadCacheBytes, total)
	for _, stop := range stops {
		stop()
	}
	if h.budget.used.Load() != 0 {
		t.Fatalf("teardown leaked %d bytes", h.budget.used.Load())
	}
}

func TestScaleP06QueuedCollaborationRevocationFailsClosed(t *testing.T) {
	for _, scenario := range []string{"removed", "credential", "permission error", "still allowed"} {
		t.Run(scenario, func(t *testing.T) {
			s := &server{realtime: newRealtimeHub()}
			client := s.realtime.addSocket("w", "c", func() {})
			defer s.realtime.removeSocket(client)
			s.realtime.joinDocument(client, "doc")
			s.realtime.broadcastDocument("w", "doc", 0, realtimeSocketMessage{binary: true, data: []byte("secret")})
			message := <-client.send
			defer message.release(client)
			client.checkCredential = func() bool { return scenario != "credential" }
			client.checkDocument = func(string) (string, bool, error) {
				if scenario == "permission error" {
					return "", false, errors.New("authority unavailable")
				}
				if scenario == "removed" {
					return "none", false, nil
				}
				return "viewer", false, nil
			}
			if got := s.socketMessageAllowed(client, message); got != (scenario == "still allowed") {
				t.Fatalf("send allowed = %v", got)
			}
		})
	}
}

type scaleP06DeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
	flushed  bool
}

func (w *scaleP06DeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func (w *scaleP06DeadlineWriter) FlushError() error {
	w.flushed = true
	if w.deadline.IsZero() {
		return errors.New("unbounded flush")
	}
	return context.DeadlineExceeded
}

func TestScaleP06WriteAndFlushDeadline(t *testing.T) {
	w := &scaleP06DeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	if !writeSSE(w, domain.RealtimeEvent{ID: "event", Payload: json.RawMessage(`{"x":1}`)}, "") {
		t.Fatal("frame write failed")
	}
	if w.deadline.IsZero() || time.Until(w.deadline) > realtimeWriteTimeout {
		t.Fatal("missing bounded write deadline")
	}
	if flushRealtime(w) || !w.flushed {
		t.Fatal("flush timeout must stop the stream")
	}
}

func scaleP06Repository(t *testing.T) *store.SQLiteStore {
	t.Helper()
	var repo *store.SQLiteStore
	var err error
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		repo, err = store.OpenDatabase(store.DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
	} else {
		repo, err = store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "p06.db"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

func TestScaleP06LargestLegalCollaborationUpdateReconnect(t *testing.T) {
	repo := scaleP06Repository(t)
	srv := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(srv)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	updateID := newCollaborationID()
	workspaceKey := "p06-" + updateID[len(updateID)-16:]
	workspace := requestJSON[domain.Bootstrap](t, handler, http.MethodPost, "/api/workspaces", map[string]any{"name": "P06 recovery", "urlKey": workspaceKey, "region": "us", "tutorial": false}, http.StatusCreated)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := repo.DeleteWorkspace(ctx, workspaceKey); err != nil {
			t.Error("could not clean up the recovery test workspace")
		}
	})
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace="+workspaceKey, map[string]any{"title": "P06 large recovery", "teamId": workspace.Teams[0].ID}, http.StatusCreated)
	documentID := "document_content_" + issue.ID
	secondUpdateID := updateID[:len(updateID)-1] + "a"
	if secondUpdateID == updateID {
		secondUpdateID = updateID[:len(updateID)-1] + "b"
	}
	if issue.DocumentContent != nil && issue.DocumentContent.ID != "" {
		documentID = issue.DocumentContent.ID
	}
	headerSize := len(encodeCollaborationFrame(collaborationUpdateFrame, documentID, updateID, nil))
	payload := bytes.Repeat([]byte{42}, maxCollaborationFrameSize-headerSize)
	frame := encodeCollaborationFrame(collaborationUpdateFrame, documentID, updateID, payload)
	if len(frame) != maxCollaborationFrameSize {
		t.Fatal("fixture is not the largest legal frame")
	}
	dial := func(clientID string) *websocket.Conn {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		connection, _, err := websocket.Dial(ctx, strings.Replace(host.URL, "http://", "ws://", 1)+"/api/realtime/socket?workspace="+workspaceKey+"&clientId="+clientID, nil)
		if err != nil {
			t.Fatal(err)
		}
		connection.SetReadLimit(4 << 20)
		return connection
	}
	first := dial("large-sender")
	defer first.CloseNow()
	joinDocument(t, first, issue.ID, documentID)
	slowContext, cancelSlow := context.WithCancel(t.Context())
	defer cancelSlow()
	slow := srv.realtime.addSocket(workspaceKey, "slow-before-recovery", cancelSlow)
	srv.realtime.joinDocument(slow, documentID)
	for range 4 {
		if !slow.enqueue(realtimeSocketMessage{data: make([]byte, maxCollaborationFrameSize)}, false) {
			t.Fatal("failed to fill slow connection before recovery test")
		}
	}
	writeSocket(t, first, websocket.MessageBinary, frame)
	assertUpdateFrame(t, first, documentID, updateID, payload)
	if slowContext.Err() == nil {
		t.Fatal("the largest legal committed update did not disconnect the overflowing slow connection")
	}
	srv.realtime.removeSocket(slow)
	writeSocket(t, first, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, documentID, secondUpdateID, payload))
	assertUpdateFrame(t, first, documentID, secondUpdateID, payload)
	first.CloseNow()

	second := dial("large-reconnected")
	defer second.CloseNow()
	raw, _ := json.Marshal(map[string]string{"type": "document.join", "issueId": issue.ID, "documentId": documentID})
	writeSocket(t, second, websocket.MessageText, raw)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	recoveredIDs := map[string]bool{}
	for {
		_, recovered, err := second.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var message collaborationSyncMessage
		if json.Unmarshal(recovered, &message) != nil || message.Type != "document.sync" {
			t.Fatal("reconnect did not recover the committed updates")
		}
		for _, update := range message.Updates {
			if update.ID != updateID && update.ID != secondUpdateID {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(update.Data)
			if err != nil || !bytes.Equal(decoded, payload) {
				t.Fatal("largest legal update was lost or truncated")
			}
			recoveredIDs[update.ID] = true
		}
		if !message.More {
			break
		}
	}
	if len(recoveredIDs) != 2 || !recoveredIDs[updateID] || !recoveredIDs[secondUpdateID] {
		t.Fatal("multi-chunk recovery lost an update")
	}
}

func TestScaleP06BudgetConcurrentCancelAndRollback(t *testing.T) {
	t.Setenv("FLOW_REALTIME_BUDGET_BYTES", "0")
	if newRealtimeBudget().limit != realtimeTransportBytes {
		t.Fatal("zero must not disable the memory bound")
	}
	t.Setenv("FLOW_REALTIME_BUDGET_BYTES", "50331648")
	budget := newRealtimeBudget()
	if budget.limit != 48<<20 {
		t.Fatal("conservative rollback ignored")
	}
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			for range 1000 {
				if budget.acquire(8192) {
					budget.release(8192)
				}
			}
		})
	}
	wg.Wait()
	if budget.used.Load() != 0 || budget.peak.Load() > budget.limit {
		t.Fatal("concurrent reservation lost its bound")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := acquireRealtimeWork(ctx, budget, 1); err == nil {
		t.Fatal("cancelled work was admitted")
	}
}

func TestScaleP06WorkReservationsReleaseOnPanic(t *testing.T) {
	s := &server{realtime: newRealtimeHub()}
	r := httptest.NewRequest("GET", "/api/realtime/events?workspace=missing", nil)
	assertPanicReleases := func(run func()) {
		t.Helper()
		panicked := false
		func() {
			defer func() { panicked = recover() != nil }()
			run()
		}()
		if !panicked || s.realtime.budget.used.Load() != 0 {
			t.Fatal("panic leaked work reservation")
		}
	}
	assertPanicReleases(func() { s.replayBudgetedRecord(httptest.NewRecorder(), r, "missing", 0) })
	assertPanicReleases(func() {
		s.writeProjectedRealtime(httptest.NewRecorder(), r, domain.RealtimeEvent{Type: "issue.updated"})
	})
}

func TestScaleP06ReorderingUsesTransportBudget(t *testing.T) {
	h := newRealtimeHub()
	h.ordered = true
	h.publish("w", domain.RealtimeEvent{ID: "first", Seq: 1})
	payload := bytes.Repeat([]byte{1}, 256<<10)
	for i := range 300 {
		h.publish("w", domain.RealtimeEvent{ID: fmt.Sprint(i), Seq: int64(i + 3), Payload: payload})
	}
	if h.budget.peak.Load() > h.budget.limit {
		t.Fatal("reordering escaped the process budget")
	}
	h.resyncAll()
	if h.budget.used.Load() != 0 {
		t.Fatal("resync leaked pending events")
	}
}

func TestScaleP06GenerationConflictNoticeStillReachesOldEditors(t *testing.T) {
	s := &server{realtime: newRealtimeHub()}
	client := s.realtime.addSocket("w", "editor", func() {})
	defer s.realtime.removeSocket(client)
	s.realtime.joinDocument(client, "old-generation")
	client.checkDocument = func(string) (string, bool, error) { return "none", true, nil }
	s.realtime.broadcastDocumentConflict("w", "old-generation")
	notice := <-client.send
	defer notice.release(client)
	if !s.socketMessageAllowed(client, notice) || !bytes.Contains(notice.data, []byte(`"type":"document.conflict"`)) {
		t.Fatal("generation replacement lost its control notice")
	}
}
