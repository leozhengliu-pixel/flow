package store

import (
	"context"
	"errors"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"
)

func holdShutdownWorker(ctx context.Context, started chan<- struct{}, release <-chan struct{}) {
	pprof.Do(ctx, pprof.Labels("test-label", "test-request-value"), func(context.Context) {
		close(started)
		<-release
	})
}

func TestStoreCloseBoundsWorkerJoin(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "shutdown.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	repo.workers.Go(func() { holdShutdownWorker(t.Context(), started, release) })
	<-started
	stopped := make(chan struct{})
	if !repo.RegisterWorkerShutdown(func() <-chan struct{} {
		close(stopped)
		return release
	}) {
		t.Fatal("store refused a worker before closing")
	}

	logs := captureSlog(t)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- repo.closeWithWorkerDeadline(ctx) }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close with stuck workers = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked behind workers that ignored cancellation")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("store did not signal its registered workers")
	}
	if !errors.Is(repo.WorkerContext().Err(), context.Canceled) {
		t.Fatal("store did not cancel its own workers")
	}
	if err := repo.CheckDatabaseConnection(t.Context()); !databasePoolClosed(err) {
		t.Fatalf("SQL pool after Close = %v", err)
	}
	stacks := logs.String()
	if !strings.Contains(stacks, "store shutdown timed out waiting for workers") ||
		!strings.Contains(stacks, "store.holdShutdownWorker") ||
		strings.Contains(stacks, "test-label") || strings.Contains(stacks, "test-request-value") {
		t.Fatal("shutdown warning should include worker frames without request labels")
	}
	unblock()
	repo.workers.Wait()
}
