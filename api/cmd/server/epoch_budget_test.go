package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP12RealtimeBurstWaitsForProjectionBudget(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_REALTIME_BUDGET_BYTES", "50331648")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := newScaleP12Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			event := domain.RealtimeEvent{ID: "p12-budget-burst", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{}`)}
			// Warm the source and policy caches, leaving the projection cold.
			authority, err := f.service.readEventAuthority(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			source, err := f.service.eventSourceForAuthority(t.Context(), workspace, event, authority)
			if err != nil {
				t.Fatal(err)
			}
			budget := source.budget
			baseline := budget.used.Load()
			scratch := int64(4<<10 + source.policy.Bytes())
			room := scratch + int64(source.bytes*4+16<<10)
			held := budget.limit - baseline - room
			if !budget.acquire(held) {
				t.Fatal("could not constrain the budget to one cold projection")
			}
			defer budget.release(held)
			if !budget.acquire(room) {
				t.Fatal("could not fill the budget")
			}
			releaseBurst := sync.OnceFunc(func() { budget.release(room) })
			request := scaleP12Request(workspace, f.data.Viewer)
			ctx, cancel := context.WithCancel(request.Context())
			request = request.WithContext(ctx)
			const deliveries = 32
			results := make(chan error, deliveries+1)
			var workers sync.WaitGroup
			defer func() {
				cancel()
				releaseBurst()
				workers.Wait()
			}()
			for range deliveries {
				workers.Go(func() {
					frame, handled, err := f.service.sharedEventFrame(request, event)
					defer frame.Close()
					if err != nil {
						results <- err
					} else {
						projected, err := frame.Event()
						switch {
						case err != nil:
							results <- err
						case !handled || !frame.Visible() || projected.Type != event.Type || projected.AggregateID != event.AggregateID:
							results <- errors.New("expected the original issue update in a visible shared frame")
						default:
							results <- nil
						}
					}
				})
			}
			cancelCtx, cancelDelivery := context.WithCancel(ctx)
			defer cancelDelivery()
			canceled := make(chan error, 1)
			workers.Go(func() {
				frame, _, err := f.service.sharedEventFrame(request.WithContext(cancelCtx), event)
				frame.Close()
				canceled <- err
			})
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			timeout := time.NewTimer(realtimeWriteTimeout)
			defer timeout.Stop()
			for budget.waiters.Load() != deliveries+1 {
				select {
				case err := <-results:
					t.Fatalf("delivery finished before projection budget was available: %v", err)
				case <-timeout.C:
					t.Fatalf("only %d deliveries waited for the budget", budget.waiters.Load())
				case <-tick.C:
				}
			}
			cancelDelivery()
			select {
			case err := <-canceled:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled delivery: %v", err)
				}
			case <-timeout.C:
				t.Fatal("canceled delivery remained blocked on the budget")
			}
			releaseBurst()
			for range deliveries {
				if err := <-results; err != nil {
					t.Errorf("burst delivery: %v", err)
				}
			}
			workers.Wait()
			if used, peak := budget.used.Load(), budget.peak.Load(); used != baseline+held || peak > budget.limit {
				t.Fatalf("projection budget used=%d want=%d peak=%d limit=%d", used, baseline+held, peak, budget.limit)
			}

			// Force a projection that cannot fit, even with an empty budget.
			// The already cached source and authority are still usable.
			source.budget = &realtimeBudget{limit: scratch - 1}
			frame, handled, err := f.service.sharedEventFrame(request, event)
			defer frame.Close()
			if err != nil || !handled || !frame.Visible() || !frame.noCursor {
				t.Fatalf("expected a resync frame without cursor advance, handled=%t visible=%t err=%v", handled, frame.Visible(), err)
			}
			projected, err := frame.Event()
			if err != nil || projected.Type != "workspace.resync_required" || projected.AggregateID != "" {
				t.Fatalf("resync frame: type=%s aggregate=%s err=%v", projected.Type, projected.AggregateID, err)
			}
			request.Header.Set("X-Flow-Sync", "summary")
			frame, handled, err = f.service.sharedEventFrame(request, event)
			defer frame.Close()
			if err != nil || !handled || !frame.Visible() {
				t.Fatalf("expected summary invalidation, handled=%t visible=%t err=%v", handled, frame.Visible(), err)
			}
			projected, err = frame.Event()
			var payload struct{ Transaction syncV2Transaction }
			if err != nil || projected.Type != "sync.delta" || json.Unmarshal(projected.Payload, &payload) != nil {
				t.Fatalf("summary invalidation: type=%s err=%v", projected.Type, err)
			}
			tx := payload.Transaction
			if len(tx.Upserts) != 0 || len(tx.Removals) != 0 || len(tx.Invalidations) != 1 || tx.Invalidations[0] != "issues" {
				t.Fatalf("expected only an issues invalidation, got %+v", tx)
			}
		})
	}
}

func TestScaleP12CachedFrameDoesNotReserveAnotherEncoding(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		event := domain.RealtimeEvent{ID: "p12-cache-budget", Type: "issue.updated", AggregateID: f.open.ID, Payload: mustJSON(t, map[string]string{"note": strings.Repeat("x", 8192)})}
		source, err := f.service.BuildEventSource(t.Context(), f.data.Workspace.URLKey, event)
		if err != nil {
			t.Fatal(err)
		}
		access, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := ProjectEvent(t.Context(), source, access)
		if err != nil || !frame.Visible() {
			t.Fatalf("initial frame: %v", err)
		}
		frame.Close()
		source.budget = &realtimeBudget{limit: 32 << 10}
		frame, err = ProjectEvent(t.Context(), source, access)
		if err != nil || !frame.Visible() {
			t.Fatalf("cached frame reserved another encoding workload: %v", err)
		}
		frame.Close()
		if source.budget.used.Load() != 0 {
			t.Fatal("cached frame retained work or bytes after close")
		}
		source.frames = nil
		if frame, err = ProjectEvent(t.Context(), source, access); !errors.Is(err, store.ErrEventSourceBudget) {
			frame.Close()
			t.Fatalf("uncached encoding bypassed the work budget: %v", err)
		}
	}
}

func TestScaleP12SharedEncodingFailureIsNotAnInvisibleFrame(t *testing.T) {
	var cache realtimePayloadCache
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cache.loadResult("p12-failed-encoding", func() (json.RawMessage, error) {
			close(entered)
			<-release
			return nil, store.ErrEventSourceBudget
		})
	}()
	<-entered
	cache.mu.Lock()
	entry := cache.entries["p12-failed-encoding"]
	cache.mu.Unlock()
	close(release)
	<-entry.ready
	<-done
	if !errors.Is(entry.err, store.ErrEventSourceBudget) {
		t.Fatal("shared encoding failure became a hidden event")
	}
	if _, err := cache.loadResult("p12-failed-encoding", func() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }); err != nil {
		t.Fatalf("failed encoding poisoned the next delivery: %v", err)
	}
}
