package store

import (
	"context"
	"testing"
)

// accessChangedReadAttempts bounds the snapshots a P19 summary read takes
// while the workspace's metadata keeps moving past the installed copy
// (ErrResourceAccessChanged). One background write can retire the copy twice:
// its commit advances the stored revision, and its later install under the
// write lock leaves the copy's revision unknown (setWorkspaceLocked), so a
// few back-to-back reruns can all land inside one write. The reruns after the
// first wait as a write that lost a race does (concurrentWriteBackoff): the
// eight attempts span about 300ms of waiting before the read gives up.
const accessChangedReadAttempts = 8

type accessChangedReadHookKey struct{}

// WithAccessChangedReadHookForTesting runs hook in every attempt of a P19
// summary read made with ctx, after the attempt read the stored metadata
// revision and before it checks the installed copy against it: a write the
// hook commits then retires the snapshot. attempt counts from 1.
func WithAccessChangedReadHookForTesting(ctx context.Context, hook func(attempt int)) context.Context {
	if !testing.Testing() {
		return ctx
	}
	return context.WithValue(ctx, accessChangedReadHookKey{}, hook)
}

func accessChangedReadHook(ctx context.Context, attempt int) {
	if hook, ok := ctx.Value(accessChangedReadHookKey{}).(func(int)); ok {
		hook(attempt)
	}
}

// accessChangedReread prepares the attempt after a snapshot attempt that
// found the installed copy behind its SQL revision: past the bound it answers
// ErrResourceAccessChanged, as the read always did when the metadata kept
// changing; otherwise it waits (from the second rerun on) and reloads the
// copy. A cancelled wait also answers ErrResourceAccessChanged.
func (s *SQLiteStore) accessChangedReread(ctx context.Context, workspace string, attempt int) error {
	if attempt >= accessChangedReadAttempts {
		return ErrResourceAccessChanged
	}
	if attempt > 1 {
		if err := concurrentWriteBackoff(ctx, attempt-1); err != nil {
			return ErrResourceAccessChanged
		}
	}
	return s.ReloadWorkspace(ctx, workspace)
}
