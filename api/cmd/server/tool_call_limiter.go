package main

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Every Flow tool call (MCP clients, agent run tokens, the in-process agent) starts from a private copy of the
// workspace metadata (mcpWorkspaceData → PagedWorkspaceMetadata → cloneBootstrap), about 16 MB on the dev
// database, and holds it until the call returns. Without a bound, the API's heap grew with the number of calls
// in flight: 400 concurrent agent-worker sessions queued hundreds of calls behind SQLite's single writer and the
// process reached 7.7 GB (docs/agent-runtime-design.md §16.3). Calls beyond the bound now wait — holding only
// their parsed arguments — for a slot, so the clones in memory are at most toolCallSlots × ~16 MB. The work is
// CPU-bound cloning plus serialized SQLite writes, so a small multiple of the cores loses no throughput.
//
// FLOW_TOOL_CALL_CONCURRENCY sets the bound (default 2 per CPU, at least 4; 0 or a negative value disables it).

type toolCallLimiter struct {
	slots chan struct{}
}

type toolCallSlotKey struct{}

func newToolCallLimiter(limit int) *toolCallLimiter {
	if limit <= 0 {
		return &toolCallLimiter{}
	}
	return &toolCallLimiter{slots: make(chan struct{}, limit)}
}

func toolCallConcurrencyFromEnv() int {
	limit := max(4, 2*runtime.GOMAXPROCS(0))
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("FLOW_TOOL_CALL_CONCURRENCY"))); err == nil {
		limit = value
	}
	return limit
}

// acquire waits for a slot (or for ctx to end) and returns the context the call runs under and its release. A
// call made while the context already holds a slot (a tool that calls another tool) does not wait again, so nested
// calls cannot deadlock.
func (l *toolCallLimiter) acquire(ctx context.Context) (context.Context, func(), error) {
	if l == nil || l.slots == nil || ctx.Value(toolCallSlotKey{}) != nil {
		return ctx, func() {}, nil
	}
	select {
	case l.slots <- struct{}{}:
		return context.WithValue(ctx, toolCallSlotKey{}, true), func() { <-l.slots }, nil
	case <-ctx.Done():
		return ctx, func() {}, ctx.Err()
	}
}

func (s *server) toolCalls() *toolCallLimiter {
	s.toolCallLimiterOnce.Do(func() {
		if s.toolCallLimiter == nil {
			s.toolCallLimiter = newToolCallLimiter(toolCallConcurrencyFromEnv())
		}
	})
	return s.toolCallLimiter
}
