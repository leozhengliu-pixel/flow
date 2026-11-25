package store

import (
	"context"
	"time"
)

// WorkspaceReloadTrace measures a single reload for the load rig.
type WorkspaceReloadTrace struct {
	DeltaAttempts, DeltaRaces, FullAttempts          int
	DeltaBuild, FullBuild, InstallWait, LockedReload time.Duration
}

type workspaceReloadTraceKey struct{}

func WithWorkspaceReloadTrace(ctx context.Context, trace *WorkspaceReloadTrace) context.Context {
	return context.WithValue(ctx, workspaceReloadTraceKey{}, trace)
}

func workspaceReloadTrace(ctx context.Context) *WorkspaceReloadTrace {
	trace, _ := ctx.Value(workspaceReloadTraceKey{}).(*WorkspaceReloadTrace)
	return trace
}
