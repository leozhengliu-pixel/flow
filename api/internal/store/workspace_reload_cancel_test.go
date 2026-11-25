package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type reloadCancelTestKey struct{}

func TestWorkspaceMutationReloadCanceledAtCommit(t *testing.T) {
	for dialect, pair := range peerStoreSets(t) {
		t.Run(dialect, func(t *testing.T) {
			writer := pair[1]
			workspace := writer.Bootstrap().Workspace.URLKey
			writer.mu.Lock()
			writer.forgetCopyRevisionLocked(workspace)
			writer.mu.Unlock()

			// With one connection, a successful ping after cancellation
			// proves database/sql already rolled the read transaction back.
			writer.db.SetMaxOpenConns(1)
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), reloadCancelTestKey{}, true))
			defer cancel()
			entered, resume := make(chan struct{}), make(chan struct{})
			defer close(resume)
			writer.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
				if tx.context().Value(reloadCancelTestKey{}) == true {
					close(entered)
					<-resume
				}
			}})
			defer writer.db.commitHooks.Store(nil)

			done := make(chan error, 1)
			go func() {
				done <- writer.MutateWorkspaceWithAggregate(ctx, workspace, "label.created", nil, func(*domain.Bootstrap) (string, error) {
					return "", errors.New("mutation reached its callback after reload failed")
				})
			}()
			select {
			case err := <-done:
				t.Fatalf("mutation returned before reloading: %v", err)
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("the workspace reload did not reach commit")
			}
			cancel()
			pingCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			if err := writer.db.PingContext(pingCtx); err != nil {
				t.Fatalf("canceled reload did not return its connection: %v", err)
			}
			resume <- struct{}{}
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("reload returned %v, want context.Canceled", err)
			}
			if _, current := writer.copyRevisionOf(workspace); current {
				t.Fatal("canceled reload installed a snapshot")
			}
		})
	}
}
