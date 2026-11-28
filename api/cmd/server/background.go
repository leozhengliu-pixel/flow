package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"flow/api/internal/store"
	"flow/api/internal/telemetry"
)

const httpDrainTimeout = 15 * time.Second

var errServerShutdown = errors.New("server shutting down")

type serverBackground struct {
	ctx           context.Context
	cancel        context.CancelCauseFunc
	serving       context.Context
	cancelServing context.CancelFunc
	done          chan struct{}

	mu      sync.Mutex
	running int
	stopped bool
	timers  map[*time.Timer]struct{}
}

// startBackgroundWorkers is called by main after installing the handler's
// sinks. Request-only fixtures leave the periodic workers off.
func (s *server) startBackgroundWorkers() {
	s.startSocketAccessSweep()
	s.startWorkflowScheduler()
	s.startDeliveryScheduler()
	s.startLoopRunReconciler()
	s.startLoopContractMigration()
	s.startAgentRunReconciler()
	s.startAgentLiveReplySweeper()
}

func (s *server) backgroundWork() *serverBackground {
	s.backgroundOnce.Do(func() {
		parent := context.Background()
		if s.telemetry != nil {
			parent = telemetry.WithWork(telemetry.WithRecorder(parent, s.telemetry), telemetry.WorkBackground)
		}
		ctx, cancel := context.WithCancelCause(parent)
		serving, cancelServing := context.WithCancel(ctx)
		b := &serverBackground{ctx: ctx, cancel: cancel, serving: serving, cancelServing: cancelServing, done: make(chan struct{})}
		// A server can share a store with other servers. Closing the store
		// stops all its dependents, while shutting down one server leaves the
		// store and its other users available.
		if s.store != nil && !s.store.RegisterWorkerShutdown(b.stop) {
			b.stop()
		}
		s.background = b
	})
	return s.background
}

// stopServing cancels periodic workers and realtime streams. In-flight HTTP
// requests can still start and use detached work until the drain ends.
func (b *serverBackground) stopServing() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancelServing()
	b.stopTimersLocked()
}

func (b *serverBackground) stop() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.stopped {
		b.stopped = true
		b.cancel(errServerShutdown)
		b.stopTimersLocked()
		if b.running == 0 {
			close(b.done)
		}
	}
	return b.done
}

func (b *serverBackground) stopTimersLocked() {
	for timer := range b.timers {
		if timer.Stop() {
			delete(b.timers, timer)
			b.running--
		}
	}
}

func (b *serverBackground) begin(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	b.running++
	return true
}

func (b *serverBackground) finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running--
	if b.stopped && b.running == 0 {
		close(b.done)
	}
}

func (s *server) goBackground(run func(context.Context)) bool {
	b := s.backgroundWork()
	return b.launch(b.ctx, run)
}

func (s *server) goWorker(run func(context.Context)) bool {
	b := s.backgroundWork()
	return b.launch(b.serving, run)
}

func (b *serverBackground) launch(ctx context.Context, run func(context.Context)) bool {
	if !b.begin(ctx) {
		return false
	}
	go func() {
		defer b.finish()
		run(ctx)
	}()
	return true
}

// workerContext is for work requested by handlers. Periodic workers use the
// context passed to their callback so they stop before the HTTP drain.
func (s *server) workerContext() context.Context {
	return s.coldWorkerContext(s.backgroundWork().ctx)
}

func (s *server) coldWorkerContext(ctx context.Context) context.Context {
	if s.store != nil && s.store.BoundedMetadataEnabled() {
		ctx = store.WithColdWork(ctx, "worker")
	}
	return ctx
}

type serverTimer struct {
	timer *time.Timer
	owner *serverBackground
}

func (b *serverBackground) afterFunc(delay time.Duration, run func()) *serverTimer {
	if b == nil {
		return &serverTimer{timer: time.AfterFunc(delay, run)}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.serving.Err() != nil {
		return nil
	}
	if b.timers == nil {
		b.timers = make(map[*time.Timer]struct{})
	}
	b.running++
	t := &serverTimer{owner: b}
	t.timer = time.AfterFunc(delay, func() {
		b.mu.Lock()
		delete(b.timers, t.timer)
		stopped := b.serving.Err() != nil
		b.mu.Unlock()
		defer b.finish()
		if !stopped {
			run()
		}
	})
	b.timers[t.timer] = struct{}{}
	return t
}

func (t *serverTimer) Stop() {
	if t.owner == nil {
		t.timer.Stop()
		return
	}
	b := t.owner
	b.mu.Lock()
	defer b.mu.Unlock()
	if t.timer.Stop() {
		delete(b.timers, t.timer)
		b.running--
	}
}

// detachedContext keeps the request's values and replaces its cancellation
// with the server's lifetime.
func (s *server) detachedContext(parent context.Context) (context.Context, context.CancelFunc) {
	return s.bindBackground(context.WithoutCancel(parent))
}

// bindBackground cancels the child when either its parent or the server ends.
func (s *server) bindBackground(parent context.Context) (context.Context, context.CancelFunc) {
	return bindLifetime(parent, s.backgroundWork().ctx)
}

func bindLifetime(parent, lifetime context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	stop := context.AfterFunc(lifetime, func() { cancel(context.Cause(lifetime)) })
	if cause := context.Cause(lifetime); cause != nil {
		cancel(cause)
	}
	return ctx, func() {
		stop()
		cancel(nil)
	}
}

func (s *server) drainHTTP(listeners ...*http.Server) {
	b := s.backgroundWork()
	b.stopServing()
	defer b.stop()
	ctx, cancel := context.WithTimeout(context.Background(), httpDrainTimeout)
	defer cancel()
	var drains sync.WaitGroup
	for _, listener := range listeners {
		if listener != nil {
			drains.Go(func() {
				if err := listener.Shutdown(ctx); err != nil {
					_ = listener.Close()
				}
			})
		}
	}
	drains.Wait()
}

// Shutdown cancels and joins work owned by the server. The caller closes
// the HTTP listeners and the store it supplied separately.
func (s *server) Shutdown(ctx context.Context) error {
	done := s.backgroundWork().stop()
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
