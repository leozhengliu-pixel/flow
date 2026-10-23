package main

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"
)

// Read-after-write across servers.
//
// Each server keeps workspace metadata (teams, members, settings, API keys…)
// in memory and applies another server's change when its peer event arrives:
// asynchronously, batched per workspace. A request that reached this server
// right after the change was committed elsewhere used to see the old copy
// (a workspace created a moment ago was "not found", a new team "invalid").
//
// With Redis coordination every state change bumps a per-workspace version
// in Redis before its event goes out (so before the writer's response). A
// request names its workspace; when Redis holds a newer version than this
// server has applied, or the workspace is not loaded here at all, the server
// reloads it from the database before authorizing the request. Concurrent
// requests share one reload.

type workspaceVersions struct {
	mu      sync.Mutex
	applied map[string]int64
	loading map[string]chan struct{}
}

// advanceOwn records this server's own change n when it already held every
// earlier one (its copy then includes n); otherwise a request reloads.
func (v *workspaceVersions) advanceOwn(workspace string, n int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.applied == nil {
		v.applied = map[string]int64{}
	}
	if v.applied[workspace] == n-1 {
		v.applied[workspace] = n
	}
}

// record notes that this server's copy includes every change up to n.
func (v *workspaceVersions) record(workspace string, n int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.applied == nil {
		v.applied = map[string]int64{}
	}
	if n > v.applied[workspace] {
		v.applied[workspace] = n
	}
}

func (v *workspaceVersions) current(workspace string) int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.applied[workspace]
}

func (s *server) workspaceVersionKey(workspace string) string {
	return s.coordinator.CacheKey(workspace, "statever", "")
}

// bumpWorkspaceVersion marks a committed change to a workspace's state for
// every server. It runs before the change's peer event is published.
func (s *server) bumpWorkspaceVersion(workspace string) {
	if s.coordinator == nil || workspace == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n, err := s.coordinator.CacheIncr(ctx, s.workspaceVersionKey(workspace))
	if err != nil {
		log.Printf("Redis workspace version workspace=%s: %v", workspace, err)
		return
	}
	s.workspaceVersions.advanceOwn(workspace, n)
}

// sharedWorkspaceVersion is the newest committed change version in Redis.
func (s *server) sharedWorkspaceVersion(ctx context.Context, workspace string) (int64, error) {
	raw, err := s.coordinator.CacheGet(ctx, s.workspaceVersionKey(workspace))
	if err != nil || len(raw) == 0 {
		return 0, err
	}
	return strconv.ParseInt(string(raw), 10, 64)
}

// ensureCurrentWorkspace makes this server's copy of the workspace include
// every change committed by any server before the call (see above).
func (s *server) ensureCurrentWorkspace(ctx context.Context, workspace string) {
	if s.coordinator == nil || workspace == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	shared, err := s.sharedWorkspaceVersion(ctx, workspace)
	if err != nil {
		// Redis is unavailable: only a workspace missing here is loaded.
		shared = 0
	}
	present := s.store.HasWorkspace(workspace)
	applied := s.workspaceVersions.current(workspace)
	if present && shared <= applied {
		if shared < applied && err == nil {
			// Redis lost the counter (flushed or replaced): start over from it.
			s.workspaceVersions.mu.Lock()
			s.workspaceVersions.applied[workspace] = shared
			s.workspaceVersions.mu.Unlock()
		}
		return
	}
	s.reloadWorkspaceTo(ctx, workspace, shared)
}

// reloadWorkspaceTo reloads the workspace unless a reload in flight or done
// meanwhile already brought it to version target.
func (s *server) reloadWorkspaceTo(ctx context.Context, workspace string, target int64) {
	versions := &s.workspaceVersions
	for {
		versions.mu.Lock()
		if versions.applied == nil {
			versions.applied, versions.loading = map[string]int64{}, map[string]chan struct{}{}
		}
		if versions.loading == nil {
			versions.loading = map[string]chan struct{}{}
		}
		if target > 0 && versions.applied[workspace] >= target && s.store.HasWorkspace(workspace) {
			versions.mu.Unlock()
			return
		}
		if inFlight := versions.loading[workspace]; inFlight != nil {
			versions.mu.Unlock()
			select {
			case <-inFlight:
				if target == 0 {
					return
				}
				continue
			case <-ctx.Done():
				return
			}
		}
		done := make(chan struct{})
		versions.loading[workspace] = done
		versions.mu.Unlock()

		// Read the version first: the reload then includes every change it
		// counts (a writer commits before it bumps the version).
		version, versionErr := s.sharedWorkspaceVersion(ctx, workspace)
		err := s.store.ReloadWorkspace(ctx, workspace)
		versions.mu.Lock()
		delete(versions.loading, workspace)
		close(done)
		if err == nil && versionErr == nil && version > versions.applied[workspace] {
			versions.applied[workspace] = version
		}
		versions.mu.Unlock()
		return
	}
}
