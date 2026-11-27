package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

var serverWorkerFrames = []string{
	".(*serverBackground).launch.func",
	".(*server).startSocketAccessSweep.func",
	".(*server).startWorkflowScheduler.func",
	".(*server).startDeliveryScheduler.func",
	".(*server).startLoopRunReconciler.func",
	".(*server).startLoopContractMigration.func",
	".(*server).startAgentRunReconciler.func",
	".(*server).startAgentLiveReplySweeper.func",
	".(*server).startCoordination.func",
	".(*server).pollPeerChanges(",
	".(*agentLiveReply).run(",
	".(*chatDMReply).follow(",
	".(*echoProvider).Exec.func",
	".(*serverBackground).afterFunc.func",
	".serveLoadRigMetrics.func",
}

var storeWorkerFrames = []string{
	"store.(*SQLiteStore).runAPIKeyUses(",
	"store.(*SQLiteStore).startRecordJournalMaintenance.func",
	"store.(*SQLiteStore).startMetadataCatalog.func",
	"store.(*SQLiteStore).runContentActivityCycleBackfill(",
	"store.(*metadataResidency).armColdImageTimerLocked.func",
	"store.(*SQLiteStore).joinWorkers(",
}

func workerStacks(frames []string) []string {
	trace := make([]byte, 64<<10)
	for {
		n := runtime.Stack(trace, true)
		if n < len(trace) {
			trace = trace[:n]
			break
		}
		trace = make([]byte, len(trace)*2)
	}
	var found []string
	for _, stack := range strings.Split(string(trace), "\n\n") {
		for _, frame := range frames {
			if strings.Contains(stack, frame) {
				found = append(found, stack)
				break
			}
		}
	}
	return found
}

func remainingWorkers(frames []string) []string {
	// A joined worker can still be returning from the defer that signalled
	// completion when the first stack snapshot is taken.
	deadline := time.Now().Add(time.Second)
	for {
		found := workerStacks(frames)
		if len(found) == 0 || !time.Now().Before(deadline) {
			return found
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBackgroundWorkersStopAfterCleanup(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	t.Run("store", func(t *testing.T) {
		repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "workers.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := repo.Close(); err != nil {
				t.Error(err)
			}
		})
		redis := miniredis.RunT(t)
		coord, err := coordination.Open(t.Context(), coordination.Config{
			Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: "worker-cleanup", ConnectTimeout: time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = coord.Close() })
		if err := repo.RecordAPIKeyUse(t.Context(), repo.Bootstrap().Workspace.URLKey, "test-worker-key", ""); err != nil {
			t.Fatal(err)
		}
		t.Run("server", func(t *testing.T) {
			s := &server{store: repo, coordinator: coord, deliverySchedulerInterval: time.Hour}
			newTestHandler(t, s)
			s.startBackgroundWorkers()
			for _, frame := range []string{
				".(*server).startSocketAccessSweep.func",
				".(*server).startWorkflowScheduler.func",
				".(*server).startDeliveryScheduler.func",
				".(*server).startCoordination.func",
				".(*server).pollPeerChanges(",
				"store.(*SQLiteStore).runAPIKeyUses(",
				"store.(*SQLiteStore).startRecordJournalMaintenance.func",
			} {
				deadline := time.Now().Add(2 * time.Second)
				for len(workerStacks([]string{frame})) == 0 {
					if !time.Now().Before(deadline) {
						t.Fatalf("worker never started: %s\n%s", frame, strings.Join(workerStacks(append(serverWorkerFrames, storeWorkerFrames...)), "\n\n"))
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			s.backgroundWork().afterFunc(time.Hour, func() { t.Error("shutdown timer fired") })
		})
		if found := remainingWorkers(serverWorkerFrames); len(found) != 0 {
			t.Fatalf("server workers after cleanup:\n%s", strings.Join(found, "\n\n"))
		}
		if repo.WorkerContext().Err() != nil {
			t.Fatal("server shutdown closed its store")
		}
	})
	if found := remainingWorkers(storeWorkerFrames); len(found) != 0 {
		t.Fatalf("store workers after cleanup:\n%s", strings.Join(found, "\n\n"))
	}
}

func TestStoreCloseJoinsServerWork(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "workers.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), "test-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := view.RecordAPIKeyUse(t.Context(), "test-workspace", "test-worker-key", ""); err != nil {
		t.Fatal(err)
	}
	s := &server{store: view}
	cleanupTestServer(t, s)
	finished := make(chan struct{})
	if !s.goBackground(func(ctx context.Context) {
		<-ctx.Done()
		close(finished)
	}) {
		t.Fatal("server refused work before shutdown")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("store closed before the server worker finished")
	}
	if s.goBackground(func(context.Context) { t.Error("worker started after Close") }) {
		t.Fatal("store admitted server work after Close")
	}
	if err := view.RecordAPIKeyUse(t.Context(), "test-workspace", "test-worker-key", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("RecordAPIKeyUse after Close = %v", err)
	}
}

func waitForShutdownPhase(t *testing.T, done <-chan struct{}, phase string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", phase)
	}
}

func TestHTTPDrainPreservesDetachedWork(t *testing.T) {
	s := &server{}
	cleanupTestServer(t, s)
	workerDone := make(chan struct{})
	if !s.goWorker(func(ctx context.Context) {
		<-ctx.Done()
		close(workerDone)
	}) {
		t.Fatal("periodic worker did not start")
	}
	public := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(public.Close)
	entered, allowWork := make(chan struct{}), make(chan struct{})
	allow := sync.OnceFunc(func() { close(allowWork) })
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-allowWork
		parent, cancel := s.detachedContext(r.Context())
		ready := make(chan error, 1)
		if !s.goBackground(func(ctx context.Context) {
			defer cancel()
			ready <- parent.Err()
			<-ctx.Done()
		}) {
			cancel()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if err := <-ready; err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(runtime.Close)
	defer allow()

	response := make(chan int, 1)
	go func() {
		reply, err := runtime.Client().Get(runtime.URL)
		if err != nil {
			response <- 0
			return
		}
		_ = reply.Body.Close()
		response <- reply.StatusCode
	}()
	waitForShutdownPhase(t, entered, "in-flight runtime request")
	publicClosing, runtimeClosing, drained := make(chan struct{}), make(chan struct{}), make(chan struct{})
	public.Config.RegisterOnShutdown(func() { close(publicClosing) })
	runtime.Config.RegisterOnShutdown(func() { close(runtimeClosing) })
	go func() {
		s.drainHTTP(public.Config, runtime.Config)
		close(drained)
	}()
	waitForShutdownPhase(t, workerDone, "periodic worker to stop")
	waitForShutdownPhase(t, publicClosing, "public HTTP drain")
	waitForShutdownPhase(t, runtimeClosing, "coding runtime HTTP drain")
	if err := s.backgroundWork().ctx.Err(); err != nil {
		t.Fatalf("detached work cancelled during drain: %v", err)
	}
	allow()
	select {
	case status := <-response:
		if status != http.StatusNoContent {
			t.Fatalf("request could not start detached work during drain: HTTP %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request did not finish")
	}
	waitForShutdownPhase(t, drained, "HTTP drain")
	if cause := context.Cause(s.backgroundWork().ctx); !errors.Is(cause, errServerShutdown) {
		t.Fatalf("detached work cancellation cause after drain = %v", cause)
	}
}

func TestShutdownDropsPendingQueues(t *testing.T) {
	s := &server{}
	const workspace = "test-workspace"
	s.backgroundWork().stopServing()
	s.socketAccess.push(workspace, socketAccessScope{all: true}, s.scheduleSocketAccessChecks)
	if s.socketAccess.running[workspace] || s.socketAccess.pending[workspace] != nil {
		t.Fatal("socket checks retained after realtime shutdown")
	}
	s.backgroundWork().stop()
	s.remoteEvents.push(workspace, domain.RealtimeEvent{ID: "test-event", Seq: 1}, true, s.scheduleRemoteEvents)
	if s.remoteEvents.running[workspace] || s.remoteEvents.waking[workspace] || s.remoteEvents.clients[workspace] != nil {
		t.Fatal("peer refresh left scheduled after server shutdown")
	}
}

func TestCollaborationShutdownSkipsCancelledPublishLog(t *testing.T) {
	redis := miniredis.RunT(t)
	coord, err := coordination.Open(t.Context(), coordination.Config{
		Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: "publish-cleanup", ConnectTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coord.Close() })
	s := &server{coordinator: coord}
	cleanupTestServer(t, s)
	s.backgroundWork().stopServing()
	output := &lockedBuffer{}
	original := log.Writer()
	log.SetOutput(output)
	defer log.SetOutput(original)
	s.publishCollaborationEvent("workspace", "document.update", collaborationEventPayload{})
	if strings.Contains(output.String(), "Redis publish collaboration") {
		t.Fatal("collaboration logged an expected shutdown cancellation")
	}
}
