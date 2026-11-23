package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// A mutation attempt the database rolled back to break a deadlock (MySQL
// 1213, PostgreSQL 40P01 or 40001) runs again; other database errors do
// not, and the attempts stay bounded.
func TestMutationAttemptsRetryDatabaseDeadlocks(t *testing.T) {
	for _, deadlock := range []error{
		&mysql.MySQLError{Number: 1213, Message: "Deadlock found when trying to get lock"},
		&pgconn.PgError{Code: "40P01"},
		&pgconn.PgError{Code: "40001"},
	} {
		wrapped := fmt.Errorf("scoped metadata: %w", deadlock)
		if !databaseDeadlock(wrapped) {
			t.Fatalf("%v is not a deadlock", wrapped)
		}
		calls := 0
		err := mutationAttempts(context.Background(), 3, func(context.Context) error {
			calls++
			if calls < 3 {
				return wrapped
			}
			return nil
		}, nil)
		if err != nil || calls != 3 {
			t.Fatalf("%v: err %v after %d attempts", deadlock, err, calls)
		}
		calls = 0
		err = mutationAttempts(context.Background(), 2, func(context.Context) error { calls++; return wrapped }, nil)
		if !errors.Is(err, deadlock) || !errors.Is(err, ErrDatabaseWriteBusy) || calls != 2 {
			t.Fatalf("%v: bounded attempts: err %v after %d", deadlock, err, calls)
		}
	}
	for _, lock := range []error{&mysql.MySQLError{Number: 1205}, &pgconn.PgError{Code: "55P03"}} {
		calls := 0
		err := mutationAttempts(t.Context(), 3, func(context.Context) error { calls++; return lock }, nil)
		if !errors.Is(err, lock) || !errors.Is(err, ErrDatabaseWriteBusy) || calls != 1 {
			t.Fatal("a rolled back lock wait must return a classified refusal")
		}
	}
	other := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}
	if databaseDeadlock(other) {
		t.Fatal("a duplicate key is not a deadlock")
	}
	calls := 0
	if err := mutationAttempts(context.Background(), 3, func(context.Context) error { calls++; return other }, nil); !errors.Is(err, other) || errors.Is(err, ErrDatabaseWriteBusy) || calls != 1 {
		t.Fatalf("other errors run once: err %v after %d", err, calls)
	}
}
