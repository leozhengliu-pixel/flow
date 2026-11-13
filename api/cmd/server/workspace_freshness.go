package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
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
// SQL revisions, authorization epochs and durable heads are authoritative in
// shadow/on mode. Redis only wakes the consumer. Requests wait for a shared
// install when the copy is behind, and return an error if freshness cannot be
// established. Off retains the Redis version/reload flow, with the same error
// handling and shared background reload lifetime.

type workspaceVersions struct {
	mu      sync.Mutex
	applied map[string]int64
	loading map[string]*workspaceReload
}

type workspaceReload struct {
	done chan struct{}
	err  error
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
func (s *server) ensureCurrentWorkspace(ctx context.Context, workspace string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.coordinator == nil || workspace == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if s.recordPeerSyncMode() != "off" {
		for {
			state, err := s.store.WorkspacePeerState(ctx, workspace)
			if err != nil {
				return err
			}
			if state.Current {
				return nil
			}
			if err := s.waitForPeerRefresh(ctx, workspace); err != nil {
				return err
			}
		}
	}
	shared, err := s.sharedWorkspaceVersion(ctx, workspace)
	if err != nil {
		// Preserve the legacy Redis outage path: load only missing workspaces.
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
		return nil
	}
	return s.reloadWorkspaceTo(ctx, workspace, shared)
}

// A request's deadline stops its wait, not the shared install. Otherwise a
// large fallback snapshot can be decoded and discarded on every retry.
func (s *server) waitForPeerRefresh(ctx context.Context, workspace string) error {
	versions := &s.workspaceVersions
	versions.mu.Lock()
	if versions.loading == nil {
		versions.loading = map[string]*workspaceReload{}
	}
	load := versions.loading[workspace]
	if load == nil {
		load = &workspaceReload{done: make(chan struct{})}
		versions.loading[workspace] = load
		go func() {
			workerCtx, cancel := context.WithTimeout(s.store.WorkerContext(), workspaceReloadTimeout)
			defer cancel()
			err := s.syncPeerWorkspace(workerCtx, workspace)
			versions.mu.Lock()
			load.err = err
			delete(versions.loading, workspace)
			close(load.done)
			versions.mu.Unlock()
		}()
	}
	versions.mu.Unlock()
	select {
	case <-load.done:
		return load.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *server) requireCurrentWorkspace(w http.ResponseWriter, r *http.Request, workspace string) bool {
	if err := s.ensureCurrentWorkspace(r.Context(), workspace); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "workspace not found")
		} else {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "Workspace is temporarily unavailable")
		}
		return false
	}
	return true
}

// reloadWorkspaceTo reloads the workspace unless a reload in flight or done
// meanwhile already brought it to version target.
func (s *server) reloadWorkspaceTo(ctx context.Context, workspace string, target int64) error {
	versions := &s.workspaceVersions
	for {
		versions.mu.Lock()
		if versions.applied == nil {
			versions.applied = map[string]int64{}
		}
		if versions.loading == nil {
			versions.loading = map[string]*workspaceReload{}
		}
		if target > 0 && versions.applied[workspace] >= target && s.store.HasWorkspace(workspace) {
			versions.mu.Unlock()
			return nil
		}
		if inFlight := versions.loading[workspace]; inFlight != nil {
			versions.mu.Unlock()
			select {
			case <-inFlight.done:
				return inFlight.err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		load := &workspaceReload{done: make(chan struct{})}
		versions.loading[workspace] = load
		versions.mu.Unlock()

		// The reload outlives the request that started it. A large workspace
		// can take longer to decode than a request waits (3s); cancelling it
		// then threw the decoded copy away without recording the version, so
		// the next request decoded the whole workspace again, and again, while
		// the server was already short of memory. The requester still stops
		// waiting at its own deadline and returns an error. It does
		// not outlive the store: closing it (shutdown) ends the reload, which
		// then decodes nothing more.
		go func() {
			reloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspaceReloadTimeout)
			defer cancel()
			defer context.AfterFunc(s.store.WorkerContext(), cancel)()
			started := time.Now()
			// Read the version first: the reload then includes every change it
			// counts (a writer commits before it bumps the version).
			version, versionErr := s.sharedWorkspaceVersion(reloadCtx, workspace)
			err := s.store.ReloadWorkspace(reloadCtx, workspace)
			if elapsed := time.Since(started); err != nil {
				log.Printf("Reload workspace=%s after %s: %v", workspace, elapsed.Round(time.Millisecond), err)
			} else if elapsed > time.Second {
				log.Printf("Reload workspace=%s took %s", workspace, elapsed.Round(time.Millisecond))
			}
			versions.mu.Lock()
			delete(versions.loading, workspace)
			load.err = err
			if err == nil && versionErr == nil && version > versions.applied[workspace] {
				versions.applied[workspace] = version
			}
			close(load.done)
			versions.mu.Unlock()
		}()
		select {
		case <-load.done:
			return load.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// workspaceReloadTimeout bounds one shared reload of a workspace's metadata.
const workspaceReloadTimeout = 2 * time.Minute
