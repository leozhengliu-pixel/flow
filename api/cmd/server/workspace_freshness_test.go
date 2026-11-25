package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

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

func TestWorkspaceReloadPanicBecomesRequestError(t *testing.T) {
	f, workspace := scaleP21SW550Fixture(t)
	redis := miniredis.RunT(t)
	coordinator, err := coordination.Open(t.Context(), coordination.Config{
		Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: "reload-panic", ConnectTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	s := &server{
		store:       f.probe,
		coordinator: coordinator,
		reloadWorkspaceFn: func(context.Context, string) error {
			panic("reload failure")
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = s.reloadWorkspaceTo(ctx, workspace, 1)
	if err == nil || err.Error() != "workspace reload failed" {
		t.Fatalf("reload panic returned %v, want a request error", err)
	}
	if got := s.workspaceVersions.current(workspace); got != 0 {
		t.Fatalf("panicking reload advanced version to %d", got)
	}
}

func TestWorkspaceFreshnessDeadlineProbeAcceptsOnlyCurrentState(t *testing.T) {
	f, workspace := scaleP21SW550Fixture(t)
	for _, test := range []struct {
		name        string
		mutate      bool
		wantCurrent bool
		wantErr     error
	}{
		{name: "current", wantCurrent: true, wantErr: nil},
		{name: "stale", mutate: true, wantErr: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.mutate {
				if err := f.repository.MutateWorkspace(t.Context(), workspace, "freshness.stale", "", nil, func(data *domain.Bootstrap) error {
					data.Workspace.Name = "freshness-stale"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			load := &workspaceReload{done: make(chan struct{})}
			s := &server{
				store: f.probe,
				workspaceVersions: workspaceVersions{
					loading: map[string]*workspaceReload{workspace: load},
				},
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
			defer cancel()
			current, err := s.waitForPeerRefresh(ctx, workspace)
			if current != test.wantCurrent {
				t.Fatalf("waitForPeerRefresh current=%t, want %t", current, test.wantCurrent)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("waitForPeerRefresh returned %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestWorkspaceFreshnessDeadlineProbeHandlesCompletedRefreshRace(t *testing.T) {
	for _, test := range []struct {
		name        string
		mutate      bool
		wantCurrent bool
		wantErr     error
	}{
		{name: "current", wantCurrent: true},
		{name: "stale", mutate: true, wantErr: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, workspace := scaleP21SW550Fixture(t)
			if test.mutate {
				if err := f.repository.MutateWorkspace(t.Context(), workspace, "freshness.race", "", nil, func(data *domain.Bootstrap) error {
					data.Workspace.Name = "freshness-race-stale"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			load := &workspaceReload{done: make(chan struct{})}
			close(load.done)
			s := &server{
				store: f.probe,
				workspaceVersions: workspaceVersions{
					loading: map[string]*workspaceReload{workspace: load},
				},
			}
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Millisecond))
			defer cancel()
			current, err := s.waitForPeerRefresh(ctx, workspace)
			if current != test.wantCurrent {
				t.Fatalf("waitForPeerRefresh current=%t, want %t", current, test.wantCurrent)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("waitForPeerRefresh returned %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestLegacyWorkspaceFreshnessDeadlineProbeAcceptsOnlyCurrentState(t *testing.T) {
	for _, test := range []struct {
		name        string
		mutate      bool
		wantCurrent bool
	}{
		{name: "current", wantCurrent: true},
		{name: "stale", mutate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, workspace := scaleP21SW550Fixture(t)
			if test.mutate {
				if err := f.repository.MutateWorkspace(t.Context(), workspace, "freshness.legacy-stale", "", nil, func(data *domain.Bootstrap) error {
					data.Workspace.Name = "freshness-legacy-stale"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			s := &server{store: f.probe}
			err := s.reloadWorkspaceDeadlineResult(workspace, 7, context.DeadlineExceeded)
			if test.wantCurrent {
				if err != nil {
					t.Fatalf("current legacy freshness returned %v", err)
				}
				if got := s.workspaceVersions.current(workspace); got != 7 {
					t.Fatalf("recorded Redis version = %d, want 7", got)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("stale legacy freshness returned %v, want deadline", err)
			}
		})
	}
}
