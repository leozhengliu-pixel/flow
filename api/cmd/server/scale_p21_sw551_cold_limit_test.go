package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// SW551: the per-replica COLD limit holds under concurrent COLD requests, COLD
// requests are served fairly and refused with 503 + Retry-After when the wait
// is too long, a long COLD request does not hold the COLD slot between its
// operations, and a COLD request never blocks a HOT one.
func TestScaleP21SW551ColdLimitAndHotIsolation(t *testing.T) {
	f, workspace := scaleP21SW550Fixture(t)
	const coldPattern, hotPattern = "GET /api/labels", "GET /api/issue-records"
	if scaleP21RouteHot(coldPattern) || !scaleP21RouteHot(hotPattern) {
		t.Fatalf("SW551 fixture routes changed temperature: %s=%q %s=%q", coldPattern, scaleP21RouteTemperature[coldPattern], hotPattern, scaleP21RouteTemperature[hotPattern])
	}
	newServer := func(coldGate *metadataReadGate) *server {
		return &server{store: f.probe, projectionGate: newMetadataReadGate(1, 64, 30*time.Second, 0), coldProjectionGate: coldGate}
	}
	send := func(handler http.Handler, path string, ctx context.Context) (*httptest.ResponseRecorder, time.Duration) {
		request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		request.Header.Set("X-Workspace-Key", workspace)
		recorder := httptest.NewRecorder()
		started := time.Now()
		handler.ServeHTTP(recorder, request)
		return recorder, time.Since(started)
	}
	// coldWrite is a COLD legacy write (no migrated writer serves the event)
	// whose callback runs while it holds the COLD slot.
	coldWrite := func(ctx context.Context, name string, inside func()) error {
		return f.probe.MutateWorkspace(ctx, workspace, "scale_p21_sw551.label", "", nil, func(data *domain.Bootstrap) error {
			if inside != nil {
				inside()
			}
			data.Labels = append(data.Labels, domain.IssueLabel{ID: "sw551-" + name, Name: name})
			return nil
		})
	}
	work := store.WithColdWork(context.Background(), "sw551")

	t.Run("cold-projections-serialize-hot-never-waits", func(t *testing.T) {
		s := newServer(newMetadataReadGate(1, 64, 30*time.Second, 0))
		var holders, maxHolders, served, hotHeldCold atomic.Int64
		var hotScoped atomic.Bool
		mux := http.NewServeMux()
		mux.HandleFunc(coldPattern, func(w http.ResponseWriter, r *http.Request) {
			if !acquireProjection(r) {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if _, scoped := store.ColdScopeFromContext(r.Context()); !scoped {
				t.Error("SW551 COLD request without a COLD scope")
			}
			current := holders.Add(1)
			for seen := maxHolders.Load(); current > seen && !maxHolders.CompareAndSwap(seen, current); seen = maxHolders.Load() {
			}
			if len(s.projectionGate.slots) != 0 {
				hotHeldCold.Add(1)
			}
			time.Sleep(40 * time.Millisecond)
			holders.Add(-1)
			served.Add(1)
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
		const cold = 12
		var wait sync.WaitGroup
		started := time.Now()
		for range cold {
			wait.Add(1)
			go func() {
				defer wait.Done()
				if w, _ := send(handler, "/api/labels", t.Context()); w.Code != http.StatusNoContent {
					t.Errorf("SW551 COLD request answered %d", w.Code)
				}
			}()
		}
		time.Sleep(10 * time.Millisecond)
		var slowest time.Duration
		for index := range 20 {
			w, took := send(handler, "/api/issue-records", t.Context())
			if w.Code != http.StatusNoContent {
				t.Fatalf("SW551 HOT request %d answered %d", index, w.Code)
			}
			slowest = max(slowest, took)
			time.Sleep(5 * time.Millisecond)
		}
		wait.Wait()
		elapsed := time.Since(started)
		if served.Load() != cold || maxHolders.Load() != 1 || elapsed < cold*40*time.Millisecond {
			t.Fatalf("SW551 served %d COLD projections, at most %d at once, in %s", served.Load(), maxHolders.Load(), elapsed)
		}
		if hotHeldCold.Load() != 0 || hotScoped.Load() {
			t.Fatal("SW551 a COLD request held a HOT projection slot or a HOT request ran COLD")
		}
		if slowest > 30*time.Millisecond {
			t.Fatalf("SW551 a HOT request waited %s while COLD requests queued", slowest)
		}
		t.Logf("SW551 %d COLD projections serialized in %s; slowest of 20 concurrent HOT requests %s", cold, elapsed.Round(time.Millisecond), slowest)
	})

	t.Run("cold-projection-queue-answers-503", func(t *testing.T) {
		s := newServer(newMetadataReadGate(1, 0, 30*time.Millisecond, 0))
		release, holding := make(chan struct{}), make(chan struct{})
		mux := http.NewServeMux()
		mux.HandleFunc(coldPattern, func(w http.ResponseWriter, r *http.Request) {
			if !acquireProjection(r) {
				if retryAfter, refused := projectionRefused(r); refused {
					writeProjectionBusy(w, retryAfter)
				}
				return
			}
			if r.URL.Query().Get("hold") != "" {
				close(holding)
				<-release
			}
			w.WriteHeader(http.StatusNoContent)
		})
		handler := s.projectionGate.holdProjections(s.withColdMetadataRouteMux(mux, mux))
		go send(handler, "/api/labels?hold=1", t.Context())
		<-holding
		w, _ := send(handler, "/api/labels", t.Context())
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Fatalf("SW551 a COLD read over the queue answered %d (Retry-After %q)", w.Code, w.Header().Get("Retry-After"))
		}
		close(release)
	})

	t.Run("cold-slot-is-fifo-and-one-at-a-time", func(t *testing.T) {
		f.probe.SetColdQueueForTesting(64, 10*time.Second)
		var order []string
		var mu sync.Mutex
		var holders, maxHolders atomic.Int64
		release := make(chan struct{})
		var wait sync.WaitGroup
		first := make(chan struct{})
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := coldWrite(work, "w0", func() {
				close(first)
				<-release
			}); err != nil {
				t.Error("SW551 first COLD write", err)
			}
		}()
		<-first
		const writers = 6
		for index := 1; index <= writers; index++ {
			name := "w" + strconv.Itoa(index)
			wait.Add(1)
			go func() {
				defer wait.Done()
				if err := coldWrite(work, name, func() {
					current := holders.Add(1)
					for seen := maxHolders.Load(); current > seen && !maxHolders.CompareAndSwap(seen, current); seen = maxHolders.Load() {
					}
					mu.Lock()
					order = append(order, name)
					mu.Unlock()
					time.Sleep(5 * time.Millisecond)
					holders.Add(-1)
				}); err != nil {
					t.Error("SW551 queued COLD write", name, err)
				}
			}()
			// Each writer queues before the next one arrives.
			deadline := time.Now().Add(5 * time.Second)
			for coldWaiting(f.probe) < index && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			time.Sleep(3 * time.Millisecond)
		}
		close(release)
		wait.Wait()
		want := []string{"w1", "w2", "w3", "w4", "w5", "w6"}
		if !slices.Equal(order, want) || maxHolders.Load() != 1 {
			t.Fatalf("SW551 COLD writes ran in order %v with at most %d at once, want %v one at a time", order, maxHolders.Load(), want)
		}
		labels, _ := f.probe.WorkspaceMetadataFields(workspace, "labels")
		for _, name := range append([]string{"w0"}, want...) {
			if !slices.ContainsFunc(labels.Labels, func(label domain.IssueLabel) bool { return label.ID == "sw551-"+name }) {
				t.Fatalf("SW551 COLD write %s is missing from the bounded image", name)
			}
		}
	})

	t.Run("cold-slot-wait-is-bounded", func(t *testing.T) {
		f.probe.SetColdQueueForTesting(1, 50*time.Millisecond)
		defer f.probe.SetColdQueueForTesting(64, 15*time.Second)
		release, holding := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- coldWrite(work, "holder", func() {
				close(holding)
				<-release
			})
		}()
		<-holding
		queued := make(chan error, 1)
		go func() { queued <- coldWrite(work, "timed-out", nil) }()
		deadline := time.Now().Add(5 * time.Second)
		for coldWaiting(f.probe) < 1 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		// The queue holds one waiter: the next is refused at once.
		if err := coldWrite(work, "refused", nil); !errors.Is(err, store.ErrColdMetadataBusy) {
			t.Fatalf("SW551 a COLD write beyond the queue limit returned %v", err)
		}
		err := <-queued
		if !errors.Is(err, store.ErrColdMetadataBusy) {
			t.Fatalf("SW551 a COLD write that waited past the timeout returned %v", err)
		}
		w := httptest.NewRecorder()
		respondMutation(w, err, http.StatusOK, nil)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" {
			t.Fatalf("SW551 a busy COLD write answered %d (Retry-After %q)", w.Code, w.Header().Get("Retry-After"))
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("long-cold-request-does-not-hold-the-slot", func(t *testing.T) {
		s := newServer(newMetadataReadGate(2, 64, 30*time.Second, 0))
		streaming, stop := make(chan struct{}), make(chan struct{})
		mux := http.NewServeMux()
		mux.HandleFunc(coldPattern, func(w http.ResponseWriter, r *http.Request) {
			if err := coldWrite(r.Context(), "stream", nil); err != nil {
				t.Error("SW551 stream write", err)
			}
			close(streaming)
			<-stop
			w.WriteHeader(http.StatusNoContent)
		})
		handler := s.projectionGate.holdProjections(s.withColdMetadataRouteMux(mux, mux))
		go send(handler, "/api/labels", t.Context())
		<-streaming
		if held := store.ColdSlotsHeldForTesting(); held != 0 {
			t.Fatalf("SW551 a streaming COLD request holds %d COLD slots between operations", held)
		}
		ctx, cancel := context.WithTimeout(work, 2*time.Second)
		defer cancel()
		if err := coldWrite(ctx, "beside-stream", nil); err != nil {
			t.Fatal("SW551 a COLD write waited for a streaming COLD request", err)
		}
		close(stop)
	})

	deadline := time.Now().Add(5 * time.Second)
	for store.ColdSlotsHeldForTesting() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if held := store.ColdSlotsHeldForTesting(); held != 0 {
		t.Fatalf("SW551 %d COLD slots leaked", held)
	}
}

func coldWaiting(repo *store.SQLiteStore) int {
	return int(repo.ColdSlotWaitingForTesting())
}
