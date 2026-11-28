package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func realtimeEncodingAllocation(f func()) uint64 {
	gc := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(gc)
	runtime.GC()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestCollaborationSyncEncoding(t *testing.T) {
	for _, message := range []collaborationSyncMessage{
		{Type: "document.sync", DocumentID: "d"},
		{Type: "document.sync", DocumentID: "d", Updates: []collaborationSyncUpdate{}},
		{Type: "document.sync", DocumentID: "<>&\xff", ContentState: "\b\x00\u2028\u2029", ReadOnly: true, More: true,
			Updates: []collaborationSyncUpdate{{ID: "a", Data: "\\\"\n"}, {ID: "b"}}},
	} {
		want, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		size := collaborationSyncBytes(message)
		if size != int64(len(want)) || !bytes.Equal(encodeCollaborationSync(message, size), want) {
			t.Fatal("collaboration sync changed the JSON", message)
		}
	}
	for _, n := range []int{8, 1520, 700 << 10} {
		message := collaborationSyncMessage{
			Type: "document.sync", DocumentID: "doc", ContentState: strings.Repeat("<>&", n),
			Updates: []collaborationSyncUpdate{{ID: "update", Data: "text"}},
		}
		var want, raw []byte
		baseline := realtimeEncodingAllocation(func() { want, _ = json.Marshal(message) })
		size := collaborationSyncBytes(message)
		charge := (size+8191)&^8191 + 1024
		allocated := realtimeEncodingAllocation(func() { raw = encodeCollaborationSync(message, size) })
		t.Logf("encoded=%d charge=%d total=%d retained=%d baseline_total=%d", len(raw), charge, allocated, cap(raw), baseline)
		if !bytes.Equal(raw, want) || allocated > uint64(charge) {
			t.Fatalf("sync allocated %d under a %d byte charge or changed its encoding", allocated, charge)
		}
	}
}

func TestScaleEncodedReservationMatchesWire(t *testing.T) {
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

type discardSSE struct{ header http.Header }

func (w *discardSSE) Header() http.Header            { return w.header }
func (w *discardSSE) Write(data []byte) (int, error) { return len(data), nil }
func (w *discardSSE) WriteHeader(int)                {}
func (w *discardSSE) Flush()                         {}

func TestScaleSharedSSEFramesStayChargedThroughFlush(t *testing.T) {
	budget := newRealtimeBudget()
	w := &realtimeBudgetWriter{ResponseWriter: &discardSSE{header: http.Header{}}, budget: budget}
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

type encodingFlushRecorder struct {
	*httptest.ResponseRecorder
	onFlush func()
}

func (r encodingFlushRecorder) Flush() {
	r.onFlush()
	r.ResponseRecorder.Flush()
}

func TestRealtimeSSEEncodingWork(t *testing.T) {
	body := strings.Repeat("<>&", 700<<10)
	for _, fixture := range []struct {
		name, payload string
		noCursor      bool
	}{
		{"single-line", `{"body":"` + body + `", "extra":1.0e-6}`, false},
		{"multiline", "{\n \"body\":\"" + body + "\"\r\n}", false},
		{"no-cursor", `{"body":"` + body + `"}`, true},
		{"empty", "", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			event := domain.RealtimeEvent{
				ID: "evt_1", Type: "document.updated", AggregateID: "doc", Seq: 9,
				Payload: json.RawMessage(fixture.payload), CreatedAt: time.Unix(0, 0).UTC(),
			}
			newline := bytes.ContainsAny(event.Payload, "\r\n")
			var want []byte
			baseline := realtimeEncodingAllocation(func() {
				if fixture.name == "single-line" {
					head := event
					head.Payload = nil
					want, _ = json.Marshal(head)
					want = append(want[:len(want)-1], `,"payload":`...)
					want = append(want, event.Payload...)
					want = append(want, '}')
				} else {
					want, _ = json.Marshal(event)
				}
			})
			prefix := "id: evt_1~node\ndata: "
			if fixture.noCursor {
				prefix = "data: "
			}
			budget := &realtimeBudget{limit: realtimeTransportBytes}
			recorder := encodingFlushRecorder{ResponseRecorder: httptest.NewRecorder(), onFlush: func() {
				if budget.used.Load() <= int64(len(event.Payload)) {
					t.Fatal("frame lease released before flush")
				}
			}}
			recorder.Body.Grow(len(prefix) + len(want) + 2)
			writer := &realtimeBudgetWriter{ResponseWriter: recorder, budget: budget}
			allocated := realtimeEncodingAllocation(func() {
				if !writeRealtimeSSE(writer, event, "node", fixture.noCursor) {
					t.Fatal("SSE write failed")
				}
			})
			wire := recorder.Body.Bytes()
			if !bytes.HasPrefix(wire, []byte(prefix)) || !bytes.HasSuffix(wire, []byte("\n\n")) ||
				!bytes.Equal(wire[len(prefix):len(wire)-2], want) {
				t.Fatal("SSE wire format changed")
			}
			charge := budget.peak.Load() - int64(cap(event.Payload))
			t.Logf("encoded=%d charge=%d total=%d baseline_json_total=%d", len(want), charge, allocated, baseline)
			if allocated > uint64(charge) || !newline && !fixture.noCursor && allocated > 64<<10 {
				t.Fatalf("SSE allocated %d under a %d byte charge", allocated, charge)
			}
			if err := writer.FlushError(); err != nil || !recorder.Flushed || budget.used.Load() != 0 {
				t.Fatal("failed flush or leaked work", err, budget.used.Load())
			}
		})
	}
}
