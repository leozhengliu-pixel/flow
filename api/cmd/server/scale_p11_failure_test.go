package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP11CommitBeforePublishKill(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP11NewFixture(t, 1)
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestScaleP11CommitChild$")
	databaseURL := f.config.URL
	if f.config.Driver == "sqlite" {
		databaseURL = f.config.Path
	}
	command.Env = append(os.Environ(), "FLOW_P11_COMMIT_CHILD=1", "FLOW_P11_WORKSPACE="+f.workspace, "FLOW_TEST_DATABASE_DRIVER="+f.config.Driver, "FLOW_TEST_DATABASE_URL="+databaseURL)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "p11 committed" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			_ = command.Wait()
			t.Fatal("child failed before the commit barrier")
		}
	case <-time.After(20 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal("child did not reach the commit barrier")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("writer did not crash")
	}
	if err := f.service.syncPeerWorkspace(t.Context(), f.workspace); err != nil || scaleP11Name(t, f) != "committed before kill" {
		t.Fatalf("peer did not recover the killed writer's committed transaction: %v", err)
	}
}

func TestScaleP11AtomicPageAndStaleVersionFallback(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP11NewFixture(t, 2)
	c := f.service.remoteEvents.consumer(f.workspace)
	before := scaleP11Name(t, f)
	if err := f.writer.MutateWorkspace(t.Context(), f.workspace, "test.p11_atomic", "", nil, func(data *domain.Bootstrap) error {
		data.Projects[0].Name = "both records committed"
		data.Projects[1].Description = strings.Repeat("x", 300<<10)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := f.peer.ReadCommittedChanges(t.Context(), c.workspaceID, fmt.Sprint(c.cursor), 100, peerChangePageBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.peer.ApplyCommittedPage(t.Context(), c.workspaceID, page); !errors.Is(err, store.ErrPeerSnapshotRequired) || scaleP11Name(t, f) != before {
		t.Fatalf("oversized transaction installed a partial page: %v", err)
	}
	if err := f.service.syncPeerWorkspace(t.Context(), f.workspace); err != nil || scaleP11Name(t, f) != "both records committed" {
		t.Fatalf("complete snapshot fallback failed: %v", err)
	}
	scaleP11Rename(t, f, "newer committed version")
	if _, err := f.peer.ApplyCommittedPage(t.Context(), c.workspaceID, page); !errors.Is(err, store.ErrPeerSnapshotRequired) {
		t.Fatalf("stale page was accepted after a newer commit: %v", err)
	}
	if err := f.service.syncPeerWorkspace(t.Context(), f.workspace); err != nil || scaleP11Name(t, f) != "newer committed version" {
		t.Fatal("stale page moved canonical state backwards")
	}
}

func TestScaleP11ConcurrentFreshnessUsesOneConsumer(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP11NewFixture(t, 1)
	scaleP11Rename(t, f, "concurrent requests")
	start := make(chan struct{})
	errors := make(chan error, 32)
	var requests sync.WaitGroup
	for range 32 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			<-start
			errors <- f.service.ensureCurrentWorkspace(t.Context(), f.workspace)
		}()
	}
	close(start)
	requests.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(f.service.remoteEvents.consumers) != 1 || scaleP11Name(t, f) != "concurrent requests" {
		t.Fatal("concurrent requests created multiple consumers or kept stale state")
	}
}

func TestScaleP11CommitChild(t *testing.T) {
	if os.Getenv("FLOW_P11_COMMIT_CHILD") != "1" {
		t.Skip("subprocess commit barrier")
	}
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := store.DatabaseConfig{Driver: os.Getenv("FLOW_TEST_DATABASE_DRIVER"), URL: os.Getenv("FLOW_TEST_DATABASE_URL"), MaxOpenConns: 2}
	if config.Driver == "sqlite" {
		config.Path = config.URL
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	// No realtime sink is registered. The parent kills this process after the
	// SQL commit and before it can publish, close the store, or run cleanups.
	if err := repo.MutateWorkspace(t.Context(), os.Getenv("FLOW_P11_WORKSPACE"), "project.updated", "p11-project-0", map[string]string{"name": "committed before kill"}, func(data *domain.Bootstrap) error {
		data.Projects[0].Name = "committed before kill"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString("p11 committed\n")
	<-t.Context().Done()
}

func TestScaleP11FailedApplyKeepsCursorAndPublishesNothing(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP11NewFixture(t, 1)
	c := f.service.remoteEvents.consumer(f.workspace)
	before := c.cursor
	change := scaleP11Rename(t, f, "retry after database recovery")
	channel, stop := f.service.realtime.subscribe(f.workspace)
	defer stop()
	if err := f.peer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.service.syncPeerWorkspace(t.Context(), f.workspace); err == nil || c.cursor != before || len(channel) != 0 {
		t.Fatal("failed apply advanced its cursor or published before installing")
	}
	reopened, err := store.OpenDatabase(f.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	f.peer, f.service.store = reopened, reopened
	if err := f.service.syncPeerWorkspace(t.Context(), f.workspace); err != nil || scaleP11Name(t, f) != "retry after database recovery" {
		t.Fatalf("consumer did not recover after a failed apply: %v", err)
	}
	select {
	case event := <-channel:
		if event.ID != change.ID && event.Type != "workspace.resync_required" {
			t.Fatal("recovery did not publish the committed change")
		}
	case <-time.After(time.Second):
		t.Fatal("recovery published no event")
	}
}

func TestScaleP11AuthorizationStopsOnFreshnessError(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "shadow")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP11NewFixture(t, 0)
	if err := f.peer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	r.Header.Set("X-Workspace-Key", f.workspace)
	w := httptest.NewRecorder()
	if f.service.authorizeWorkspaceRequest(w, r, f.owner) || w.Code != http.StatusServiceUnavailable {
		t.Fatal("HTTP authorization continued with stale permissions")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.service.ensureCurrentWorkspace(ctx, f.workspace); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled freshness returned %v", err)
	}
}
