package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP06EncodedReservationMatchesWire(t *testing.T) {
	empty := collaborationSyncMessage{Type: "document.sync", DocumentID: "doc"}
	rawEmpty, _ := json.Marshal(empty)
	if collaborationSyncBytes(empty) != int64(len(rawEmpty)) {
		t.Fatal("nil update list size changed")
	}
	for _, state := range []string{"", "AQID", "quote\"slash\\\n\x00<&>", "\u754c\u2028\u2029", string([]byte{255}), strings.Repeat("A", 2<<20)} {
		message := collaborationSyncMessage{Type: "document.sync", DocumentID: "doc", ContentState: state, Updates: []collaborationSyncUpdate{{ID: "collab_\"", Data: "AQID"}}, More: true, ReadOnly: true}
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if got := collaborationSyncBytes(message); got != int64(len(raw)) {
			t.Fatalf("reservation=%d wire=%d", got, len(raw))
		}
	}
}

type p06DiscardSSE struct{ header http.Header }

func (w *p06DiscardSSE) Header() http.Header            { return w.header }
func (w *p06DiscardSSE) Write(data []byte) (int, error) { return len(data), nil }
func (w *p06DiscardSSE) WriteHeader(int)                {}
func (w *p06DiscardSSE) Flush()                         {}

func TestScaleP06SharedSSEFramesStayChargedThroughFlush(t *testing.T) {
	budget := newRealtimeBudget()
	w := &realtimeBudgetWriter{ResponseWriter: &p06DiscardSSE{header: http.Header{}}, budget: budget}
	event := domain.RealtimeEvent{ID: "shared", Type: "presence.updated", Payload: json.RawMessage(`{"data":"` + strings.Repeat("x", 64<<10) + `"}`)}
	for range 2000 {
		if !writeSSE(w, event, "") {
			t.Fatal("shared frame was charged once per recipient")
		}
	}
	if budget.used.Load() == 0 || budget.used.Load() > 4<<20 {
		t.Fatalf("in-flight frames charged %d", budget.used.Load())
	}
	if !flushRealtime(w) || budget.used.Load() != 0 {
		t.Fatal("flush did not release all frame leases")
	}
}
