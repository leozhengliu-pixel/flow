package main

import (
	"errors"
	"log"
	"sync"

	"flow/api/internal/store"
)

// With bounded metadata on, a workspace the store cannot serve (its installed
// image refused by the coverage check) fails every read of it with
// ErrBoundedMetadataCoverage. A background pass over all workspaces skips such
// a workspace and carries on with the others, as the legacy passes skip a
// workspace they cannot read: one workspace never stops the pass for the
// rest. The failure is logged once per pass and workspace (not on every
// tick) until the workspace passes again.
type uncoveredWorkspaceLog struct {
	mu     sync.Mutex
	logged map[[2]string]bool
}

// uncoveredWorkspaceLogs holds each server's log (*server ->
// *uncoveredWorkspaceLog), created on its first skipped workspace.
var uncoveredWorkspaceLogs sync.Map

// skipUncoveredWorkspace reports whether err is a bounded coverage failure of
// workspace that pass skips; it logs the first one.
func (s *server) skipUncoveredWorkspace(pass, workspace string, err error) bool {
	if err == nil || s.store == nil || !s.store.BoundedMetadataEnabled() || !errors.Is(err, store.ErrBoundedMetadataCoverage) {
		return false
	}
	value, _ := uncoveredWorkspaceLogs.LoadOrStore(s, &uncoveredWorkspaceLog{})
	l := value.(*uncoveredWorkspaceLog)
	l.mu.Lock()
	first := !l.logged[[2]string{pass, workspace}]
	if first {
		if l.logged == nil {
			l.logged = map[[2]string]bool{}
		}
		l.logged[[2]string{pass, workspace}] = true
	}
	l.mu.Unlock()
	if first {
		log.Printf("%s: skipping workspace=%s until it is covered: %v", pass, workspace, err)
	}
	return true
}

// coveredWorkspacePassed records that pass served workspace again, so a later
// coverage failure is logged again.
func (s *server) coveredWorkspacePassed(pass, workspace string) {
	value, ok := uncoveredWorkspaceLogs.Load(s)
	if !ok {
		return
	}
	l := value.(*uncoveredWorkspaceLog)
	l.mu.Lock()
	if len(l.logged) != 0 {
		delete(l.logged, [2]string{pass, workspace})
	}
	l.mu.Unlock()
}
