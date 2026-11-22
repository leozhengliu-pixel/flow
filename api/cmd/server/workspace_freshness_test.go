package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coordination"

	"github.com/alicebob/miniredis/v2"
)

type reloadWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *reloadWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestBoundedWorkspaceReloadWaitsForTarget(t *testing.T) {
	f, workspace := scaleP21SW550Fixture(t)
	redis := miniredis.RunT(t)
	coordinator, err := coordination.Open(t.Context(), coordination.Config{
		Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: "shared-reload", ConnectTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	s := &server{store: f.probe, coordinator: coordinator}
	for range 2 {
		if _, err := coordinator.CacheIncr(t.Context(), s.workspaceVersionKey(workspace)); err != nil {
			t.Fatal(err)
		}
	}

	older := &workspaceReload{done: make(chan struct{})}
	s.workspaceVersions.loading = map[string]*workspaceReload{workspace: older}
	timeout, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ctx := &reloadWaitContext{Context: timeout, waiting: make(chan struct{})}
	finished := make(chan error, 1)
	go func() { finished <- s.reloadWorkspaceTo(ctx, workspace, 2) }()
	select {
	case <-ctx.waiting:
	case <-timeout.Done():
		t.Fatal("reader did not join the in-flight reload")
	}
	s.workspaceVersions.mu.Lock()
	s.workspaceVersions.applied[workspace] = 1
	delete(s.workspaceVersions.loading, workspace)
	close(older.done)
	s.workspaceVersions.mu.Unlock()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if installed := s.workspaceVersions.current(workspace); installed != 2 {
		t.Fatalf("reader returned at version %d, want at least 2", installed)
	}
}
