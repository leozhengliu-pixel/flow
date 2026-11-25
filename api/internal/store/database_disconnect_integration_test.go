//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestExternalDatabaseDisconnectBeforeWorkspaceMutation(t *testing.T) {
	dialect := os.Getenv("FLOW_TEST_DATABASE_DRIVER")
	if dialect != "mysql" && dialect != "postgres" {
		t.Skip("requires a MySQL or PostgreSQL test database")
	}
	pair := peerStoreSets(t)[dialect]
	owner, writer := pair[0], pair[1]
	workspace := writer.Bootstrap().Workspace.URLKey
	writer.mu.Lock()
	writer.forgetCopyRevisionLocked(workspace)
	writer.mu.Unlock()
	writer.db.SetMaxOpenConns(1)
	writer.db.SetMaxIdleConns(1)

	pidQuery := `SELECT pg_backend_pid()`
	if dialect == "mysql" {
		pidQuery = `SELECT CONNECTION_ID()`
	}
	var backend int
	if err := writer.db.QueryRowContext(t.Context(), pidQuery).Scan(&backend); err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	defer close(resume)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, ",data FROM workspace_states") {
			close(entered)
			<-resume
		}
	})
	done := make(chan error, 1)
	go func() {
		done <- writer.MutateWorkspaceWithAggregate(ctx, workspace, "label.created", nil, func(*domain.Bootstrap) (string, error) {
			return "", errors.New("mutation reached its callback after the connection was lost")
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("mutation returned before reloading: %v", err)
	case <-ctx.Done():
		t.Fatal("reload did not start")
	case <-entered:
	}

	// The connection is in an active read transaction, so the driver's
	// idle checkout check cannot hide this disconnect.
	if dialect == "mysql" {
		if _, err := owner.db.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", backend)); err != nil {
			t.Fatal(err)
		}
	} else {
		var terminated bool
		if err := owner.db.QueryRowContext(ctx, `SELECT pg_terminate_backend(?, 5000)`, backend).Scan(&terminated); err != nil || !terminated {
			t.Fatalf("could not terminate test backend: %v", err)
		}
	}
	resume <- struct{}{}
	if err := <-done; !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("pre-write reload returned %v, want ErrDatabaseUnavailable", err)
	}
	if _, current := writer.copyRevisionOf(workspace); current {
		t.Fatal("interrupted reload installed a snapshot")
	}
	if err := writer.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatalf("reload after reconnect: %v", err)
	}
}
