//go:build integration

package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"flow/api/internal/idgen"
)

func TestExternalMutationLockWait(t *testing.T) {
	driver := os.Getenv("FLOW_TEST_DATABASE_DRIVER")
	if driver != "mysql" && driver != "postgres" && driver != "postgresql" {
		t.Skip("requires a MySQL or PostgreSQL test database")
	}
	pair := peerStoreSets(t)[driver]
	owner, writer := pair[0], pair[1]
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	data, err := owner.CreateWorkspace(ctx, "Lock wait test", idgen.New("lock-wait-"), "us")
	if err != nil {
		t.Fatal("could not create the lock wait fixture")
	}
	key, aggregate := data.Workspace.URLKey, data.Workspace.ID
	t.Cleanup(func() {
		if err := owner.DeleteWorkspace(context.Background(), key); err != nil {
			t.Error("could not remove the lock wait fixture")
		}
	})
	held, err := owner.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not open the lock holder")
	}
	defer held.Rollback()
	var locked string
	if err := held.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=? FOR UPDATE`, key).Scan(&locked); err != nil {
		t.Fatal("could not hold the workspace row")
	}
	write := func(ctx context.Context) error {
		tx, err := writer.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		timeout := `SET LOCAL lock_timeout='100ms'`
		if driver == "mysql" {
			timeout = `SET SESSION innodb_lock_wait_timeout=1`
		}
		if _, err := tx.ExecContext(ctx, timeout); err != nil {
			return err
		}
		var workspaceID string
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=? FOR UPDATE`, key).Scan(&workspaceID); err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{
			ID: idgen.New("evt-"), Type: "test.lock_wait", AggregateID: aggregate, WorkspaceID: workspaceID,
			Payload: []byte(`{}`), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			return err
		}
		return tx.Commit()
	}
	events := func() int {
		t.Helper()
		rows, err := owner.Events(ctx, aggregate)
		if err != nil {
			t.Fatal("could not read lock wait test events")
		}
		count := 0
		for _, row := range rows {
			if row.Type == "test.lock_wait" {
				count++
			}
		}
		return count
	}
	before := events()
	if err := mutationAttempts(ctx, fullMutationAttempts, write, nil); !errors.Is(err, ErrDatabaseWriteBusy) || events() != before {
		t.Fatal("a held database lock must return a retryable error with no event")
	}
	if err := held.Rollback(); err != nil {
		t.Fatal("could not release the workspace row")
	}
	if err := mutationAttempts(ctx, fullMutationAttempts, write, nil); err != nil || events() != before+1 {
		t.Fatal("the released database lock must allow one event to commit")
	}
}
