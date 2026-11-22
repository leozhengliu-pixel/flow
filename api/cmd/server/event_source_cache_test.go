package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func TestEventSourceCacheConcurrentBuilds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		var cache eventSourceCache
		warm := &EventSource{bytes: 1}
		if _, err := cache.load(ctx, "warm", func() (*EventSource, error) { return warm, nil }); err != nil {
			t.Fatal(err)
		}
		type result struct {
			source *EventSource
			err    error
		}
		load := func(ctx context.Context, key string, build func() (*EventSource, error)) <-chan result {
			out := make(chan result, 1)
			go func() {
				source, err := cache.load(ctx, key, build)
				out <- result{source, err}
			}()
			return out
		}
		requireResult := func(out <-chan result, source *EventSource, err error) {
			t.Helper()
			select {
			case got := <-out:
				if got.source != source || !errors.Is(got.err, err) {
					t.Fatalf("load returned source %p and %v, want %p and %v", got.source, got.err, source, err)
				}
			default:
				t.Fatal("load is still waiting")
			}
		}
		unexpectedBuild := func() (*EventSource, error) {
			return nil, errors.New("unexpected build")
		}
		hold, entered := make(chan struct{}), make(chan struct{}, 2)
		defer func() {
			if hold != nil {
				close(hold)
			}
		}()
		a, b := &EventSource{bytes: 1}, &EventSource{bytes: 1}
		start := func(key string, source *EventSource, release <-chan struct{}) <-chan result {
			return load(ctx, key, func() (*EventSource, error) {
				entered <- struct{}{}
				<-release
				return source, nil
			})
		}
		first := start("a", a, hold)
		synctest.Wait()
		duplicate := load(ctx, "a", unexpectedBuild)
		other := start("b", b, hold)
		hit := load(ctx, "warm", unexpectedBuild)
		synctest.Wait()
		if len(entered) != 2 {
			t.Fatal("distinct builds did not both start")
		}
		requireResult(hit, warm, nil)

		waitingCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		excess := load(waitingCtx, "c", unexpectedBuild)
		sameKeyWait := load(waitingCtx, "b", unexpectedBuild)
		synctest.Wait()
		cancel()
		synctest.Wait()
		requireResult(excess, nil, context.Canceled)
		requireResult(sameKeyWait, nil, context.Canceled)

		close(hold)
		hold = nil
		synctest.Wait()
		requireResult(first, a, nil)
		requireResult(duplicate, a, nil)
		requireResult(other, b, nil)
	})
}

func TestEventSourceCacheSharesOnlyStaleBuilds(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		err   error
		calls int32
	}{
		{"obsolete live source", errLiveEventSourceStale, 1},
		{"legacy snapshot mismatch", errRealtimeProjectionStale, 2},
		{"builder canceled", context.Canceled, 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var cache eventSourceCache
				var calls atomic.Int32
				var allowFresh atomic.Bool
				release := make(chan struct{})
				fresh := &EventSource{bytes: 1}
				build := func() (*EventSource, error) {
					if calls.Add(1) == 1 {
						<-release
						return nil, scenario.err
					}
					if scenario.err == errLiveEventSourceStale && !allowFresh.Load() {
						return nil, scenario.err
					}
					return fresh, nil
				}
				load := func(wantSource *EventSource, wantErr error) {
					source, err := cache.load(t.Context(), "key", build)
					if source != wantSource || !errors.Is(err, wantErr) {
						t.Errorf("source=%p err=%v, want source=%p err=%v", source, err, wantSource, wantErr)
					}
				}
				go load(nil, scenario.err)
				synctest.Wait()
				for range 8 {
					if scenario.err == errLiveEventSourceStale {
						go load(nil, scenario.err)
					} else {
						go load(fresh, nil)
					}
				}
				synctest.Wait()
				close(release)
				synctest.Wait()
				if count := calls.Load(); count != scenario.calls {
					t.Errorf("started %d builds, want %d", count, scenario.calls)
				}
				// A later caller can build after a rejected flight.
				allowFresh.Store(true)
				load(fresh, nil)
			})
		})
	}
}
