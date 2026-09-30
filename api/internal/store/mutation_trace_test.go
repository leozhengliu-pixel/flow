package store

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestSlowMutationTraceLogsStages(t *testing.T) {
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, nil)))
	defer slog.SetDefault(previous)
	ctx, trace := startMutationTrace(context.Background())
	traceStart(ctx, "full")
	traceLocked(ctx)
	traceMark(ctx, "clone")
	traceUnlocked(ctx)
	trace.report("acme", "team.archived", nil)
	if buffer.Len() != 0 {
		t.Fatalf("fast mutation was logged: %s", buffer.String())
	}
	trace.start = trace.start.Add(-2 * slowMutationThreshold())
	trace.report("acme", "team.archived", nil)
	line := buffer.String()
	for _, want := range []string{"slow workspace mutation", "event=team.archived", "workspace=acme", "path=full", "lock_wait=", "clone="} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q misses %q", line, want)
		}
	}
}
