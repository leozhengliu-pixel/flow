package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// SW590: a writer that runs its SQL in one transaction under the store lock
// (lockWorkspaceWritesTx) takes the database connection before the store
// lock. A read snapshot holds the connection (SQLite has one) and then waits
// for the store's read lock (ResolveAccess, a resource read); a writer that
// waited for the connection while holding the store lock deadlocked with it
// until a request timed out (the load rig's 30-second label.delete).
func TestScaleP21SW590WriterTakesConnectionBeforeStoreLock(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if open := repo.db.Stats().MaxOpenConnections; open != 1 {
		t.Fatalf("SW590 SQLite pool has %d connections, want 1", open)
	}
	label := addDeletionLabel(t, repo, "project")

	// A reader holds the only connection in a read transaction.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	reader, err := repo.db.BeginTx(ctx, metadataReadOptions(repo.dialect))
	if err != nil {
		t.Fatal(err)
	}
	var stamp int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_states`).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() {
		written <- repo.MutateLabelDeletion(ctx, "test-workspace", "label.deleted", label.ID, false, removeTestLabel(label.ID))
	}()
	// Let the writer queue for the connection.
	time.Sleep(100 * time.Millisecond)
	// The reader then takes the store's read lock, as ResolveAccess does.
	locked := make(chan struct{})
	go func() {
		repo.mu.RLock()
		_ = repo.workspaces["test-workspace"]
		repo.mu.RUnlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("SW590 a reader holding the connection waited for the store lock behind a writer waiting for the connection")
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SW590 the writer did not finish once the connection was free")
	}
	metadata, _ := repo.WorkspaceMetadata("test-workspace")
	if slices.ContainsFunc(metadata.Labels, func(value domain.IssueLabel) bool { return value.ID == label.ID }) {
		t.Fatal("SW590 deleted label remained in the image")
	}
}
