package store

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Slow workspace writes are logged with their stage timings so a regression
// (a write that scales with the workspace instead of the change) is visible
// in production logs. FLOW_SLOW_MUTATION_MS sets the threshold (default 500;
// 0 disables the log).
var slowMutationThreshold = sync.OnceValue(func() time.Duration {
	value := strings.TrimSpace(os.Getenv("FLOW_SLOW_MUTATION_MS"))
	if value == "" {
		return 500 * time.Millisecond
	}
	ms, err := strconv.Atoi(value)
	if err != nil || ms < 0 {
		return 500 * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
})

type mutationTraceKey struct{}

type mutationStage struct {
	name     string
	duration time.Duration
}

type mutationTrace struct {
	mu       sync.Mutex
	path     string
	start    time.Time
	last     time.Time
	locked   time.Time
	lockHeld time.Duration
	stages   []mutationStage
}

func startMutationTrace(ctx context.Context) (context.Context, *mutationTrace) {
	now := time.Now()
	trace := &mutationTrace{start: now, last: now}
	return context.WithValue(ctx, mutationTraceKey{}, trace), trace
}

func mutationTraceFrom(ctx context.Context) *mutationTrace {
	trace, _ := ctx.Value(mutationTraceKey{}).(*mutationTrace)
	return trace
}

// traceMark ends the current stage of the mutation traced by ctx.
func traceMark(ctx context.Context, stage string) {
	if trace := mutationTraceFrom(ctx); trace != nil {
		trace.mark(stage)
	}
}

func traceStart(ctx context.Context, path string) {
	if trace := mutationTraceFrom(ctx); trace != nil {
		trace.mu.Lock()
		trace.path = path
		trace.mu.Unlock()
	}
}

// traceLocked records that the workspace lock was acquired (the time since
// the previous mark is lock wait); traceUnlocked accumulates the hold time.
func traceLocked(ctx context.Context) {
	if trace := mutationTraceFrom(ctx); trace != nil {
		trace.mark("lock_wait")
		trace.mu.Lock()
		trace.locked = time.Now()
		trace.mu.Unlock()
	}
}

func traceUnlocked(ctx context.Context) {
	if trace := mutationTraceFrom(ctx); trace != nil {
		trace.mu.Lock()
		if !trace.locked.IsZero() {
			trace.lockHeld += time.Since(trace.locked)
			trace.locked = time.Time{}
		}
		trace.mu.Unlock()
	}
}

func (t *mutationTrace) mark(stage string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.stages = append(t.stages, mutationStage{stage, now.Sub(t.last)})
	t.last = now
}

func (t *mutationTrace) report(workspace, eventType string, err error) {
	threshold := slowMutationThreshold()
	if threshold <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	total := time.Since(t.start)
	if total < threshold && t.lockHeld < threshold {
		return
	}
	stages := make([]string, 0, len(t.stages)+1)
	for _, stage := range t.stages {
		stages = append(stages, stage.name+"="+strconv.FormatInt(stage.duration.Milliseconds(), 10)+"ms")
	}
	if rest := time.Since(t.last); rest >= time.Millisecond {
		stages = append(stages, "rest="+strconv.FormatInt(rest.Milliseconds(), 10)+"ms")
	}
	attrs := []any{"event", eventType, "workspace", workspace, "path", t.path, "total_ms", total.Milliseconds(), "lock_held_ms", t.lockHeld.Milliseconds(), "stages", strings.Join(stages, " ")}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	slog.Warn("slow workspace mutation", attrs...)
}
