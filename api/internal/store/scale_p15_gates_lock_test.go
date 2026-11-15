package store

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestScaleP15GatesSnapshotDoesNotPinSQLWhileWaitingForStore(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _ := scaleP04OwnedStore(t)
	workspace := repo.Bootstrap().Workspace.URLKey
	if repo.db.Stats().MaxOpenConnections != 1 {
		t.Skip("the deterministic pool-order probe uses the existing single-connection SQLite fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	repo.mu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			repo.mu.Unlock()
		}
	}()
	result := make(chan error, 1)
	go func() {
		_, release, err := repo.BeginWorkspaceRead(ctx, workspace)
		if release != nil {
			release()
		}
		result <- err
	}()
	// Give the reader an opportunity to reach the held memory lock. A
	// database reader must still use the sole connection during that wait.
	for range 100 {
		runtime.Gosched()
	}
	var one int
	if err := repo.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("a reader waiting on the store retained the sole SQL connection: %v", err)
	}
	repo.mu.Unlock()
	unlocked = true
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
