package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// SW551: the per-replica COLD limit holds under concurrent COLD requests, and
// a COLD request never blocks a HOT one.
//
// Twelve COLD requests (a COLD-table route) take their projection slot at
// once on a bounded server whose projection gate has a single slot; each
// holds what it got for 40 ms. At most one COLD slot is ever held, all twelve
// complete (serialized), none holds a projection slot, and HOT requests sent
// meanwhile (a proven-bounded route, which also takes a projection slot)
// never wait for the COLD queue: each completes within a bound far below one
// COLD hold, with no COLD scope. A COLD request whose client leaves while
// queued gives up without the slot, and a write in the COLD scope shares the
// slot its projection took (no second slot, no deadlock).
func TestScaleP21SW551ColdLimitAndHotIsolation(t *testing.T) {
	f, workspace := scaleP21SW550Fixture(t)
	const coldPattern, hotPattern = "GET /api/labels", "GET /api/issue-records"
	if scaleP21RouteHot(coldPattern) || !scaleP21RouteHot(hotPattern) {
		t.Fatalf("SW551 fixture routes changed temperature: %s=%q %s=%q", coldPattern, scaleP21RouteTemperature[coldPattern], hotPattern, scaleP21RouteTemperature[hotPattern])
	}
	s := &server{store: f.probe, projectionGate: newMetadataReadGate(1, 64, 30*time.Second, 0)}
	var holders, maxHolders, coldServed, projectionSlotsByCold atomic.Int64
	hotScoped := atomic.Bool{}
	mux := http.NewServeMux()
	mux.HandleFunc(coldPattern, func(w http.ResponseWriter, r *http.Request) {
		if !acquireProjection(r) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		scope, ok := store.ColdScopeFromContext(r.Context())
		if !ok || !scope.Held() {
			t.Error("SW551 COLD request holds no COLD slot after its projection")
		}
		if hold, _ := r.Context().Value(projectionHoldKey{}).(*projectionHold); hold != nil {
			hold.mu.Lock()
			if hold.held {
				projectionSlotsByCold.Add(1)
			}
			hold.mu.Unlock()
		}
		current := holders.Add(1)
		for {
			seen := maxHolders.Load()
			if current <= seen || maxHolders.CompareAndSwap(seen, current) {
				break
			}
		}
		if held := store.ColdSlotsHeldForTesting(); held != 1 {
			t.Errorf("SW551 %d COLD slots held while a COLD request runs", held)
		}
		time.Sleep(40 * time.Millisecond)
		holders.Add(-1)
		coldServed.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc(hotPattern, func(w http.ResponseWriter, r *http.Request) {
		if _, scoped := store.ColdScopeFromContext(r.Context()); scoped {
			hotScoped.Store(true)
		}
		if !acquireProjection(r) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := s.projectionGate.holdProjections(s.withColdMetadataRouteMux(mux, mux))
	send := func(path string) (int, time.Duration) {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Workspace-Key", workspace)
		recorder := httptest.NewRecorder()
		started := time.Now()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, time.Since(started)
	}
	const cold = 12
	var wait sync.WaitGroup
	started := time.Now()
	for range cold {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if code, _ := send("/api/labels"); code != http.StatusNoContent {
				t.Errorf("SW551 COLD request answered %d", code)
			}
		}()
	}
	// HOT requests while the COLD queue is full.
	time.Sleep(10 * time.Millisecond)
	var slowest time.Duration
	for index := range 20 {
		code, took := send("/api/issue-records")
		if code != http.StatusNoContent {
			t.Fatalf("SW551 HOT request %d answered %d", index, code)
		}
		slowest = max(slowest, took)
		time.Sleep(5 * time.Millisecond)
	}
	wait.Wait()
	elapsed := time.Since(started)
	if coldServed.Load() != cold || maxHolders.Load() != 1 {
		t.Fatalf("SW551 served %d COLD requests with at most %d concurrent, want %d with 1", coldServed.Load(), maxHolders.Load(), cold)
	}
	if elapsed < cold*40*time.Millisecond {
		t.Fatalf("SW551 %d COLD requests of 40 ms finished in %s: not serialized", cold, elapsed)
	}
	if projectionSlotsByCold.Load() != 0 {
		t.Fatalf("SW551 %d COLD requests held a projection slot", projectionSlotsByCold.Load())
	}
	if hotScoped.Load() {
		t.Fatal("SW551 a HOT request ran in a COLD scope")
	}
	if slowest > 30*time.Millisecond {
		t.Fatalf("SW551 a HOT request waited %s while COLD requests queued", slowest)
	}
	if held := store.ColdSlotsHeldForTesting(); held != 0 {
		t.Fatalf("SW551 %d COLD slots leaked", held)
	}
	t.Logf("SW551 %d COLD requests serialized in %s; slowest of 20 concurrent HOT requests %s", cold, elapsed.Round(time.Millisecond), slowest)

	// A queued COLD request whose client leaves gives up without the slot.
	release := make(chan struct{})
	holding := make(chan struct{})
	block := http.NewServeMux()
	block.HandleFunc(coldPattern, func(w http.ResponseWriter, r *http.Request) {
		if !acquireProjection(r) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		select {
		case holding <- struct{}{}:
			<-release
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	blocked := s.projectionGate.holdProjections(s.withColdMetadataRouteMux(block, block))
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/api/labels", nil)
		blocked.ServeHTTP(httptest.NewRecorder(), request)
	}()
	<-holding
	request := httptest.NewRequest(http.MethodGet, "/api/labels", nil)
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Millisecond)
	defer cancel()
	recorder := httptest.NewRecorder()
	blocked.ServeHTTP(recorder, request.WithContext(ctx))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("SW551 a COLD request whose client left answered %d", recorder.Code)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for store.ColdSlotsHeldForTesting() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if held := store.ColdSlotsHeldForTesting(); held != 0 {
		t.Fatalf("SW551 %d COLD slots held after the queue drained", held)
	}

	// A legacy write inside the COLD scope reuses the slot its projection
	// took and publishes to the bounded image.
	write := http.NewServeMux()
	write.HandleFunc(coldPattern, func(w http.ResponseWriter, r *http.Request) {
		if !acquireProjection(r) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		err := f.probe.MutateWorkspace(r.Context(), workspace, "scale_p21_sw551.label", "", nil, func(data *domain.Bootstrap) error {
			if held := store.ColdSlotsHeldForTesting(); held != 1 {
				t.Errorf("SW551 %d COLD slots held during the COLD write", held)
			}
			data.Labels = append(data.Labels, domain.IssueLabel{ID: "sw551-label", Name: "SW551"})
			return nil
		})
		if err != nil {
			t.Error("SW551 COLD write", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	writer := s.projectionGate.holdProjections(s.withColdMetadataRouteMux(write, write))
	request = httptest.NewRequest(http.MethodGet, "/api/labels", nil)
	ctx, cancelWrite := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancelWrite()
	recorder = httptest.NewRecorder()
	writer.ServeHTTP(recorder, request.WithContext(ctx))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("SW551 COLD write answered %d", recorder.Code)
	}
	labels, ok := f.probe.WorkspaceMetadataFields(workspace, "labels")
	if !ok || !slices.ContainsFunc(labels.Labels, func(label domain.IssueLabel) bool { return label.ID == "sw551-label" }) {
		t.Fatal("SW551 the COLD write is missing from the bounded image")
	}
	if held := store.ColdSlotsHeldForTesting(); held != 0 {
		t.Fatalf("SW551 %d COLD slots held after the COLD write", held)
	}
}
