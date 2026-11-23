package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDatabaseConnectionErrors(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"driver checkout", driver.ErrBadConn, "bad_connection"},
		{"mysql pooled connection", mysql.ErrInvalidConn, "mysql_invalid_connection"},
		{"mysql connection limit", &mysql.MySQLError{Number: 1040}, "mysql_1040"},
		{"pooled EOF", io.EOF, "connection_lost"},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "connection_refused"},
		{"postgres closed", pgconn.ErrConnClosed, "closed_connection"},
		{"postgres connect", &pgconn.ConnectError{}, "postgres_connect"},
		{"postgres too many connections", &pgconn.PgError{Code: "53300"}, "postgres_53300"},
		{"postgres shutdown", &pgconn.PgError{Code: "57P01"}, "postgres_57P01"},
		{"sqlite closed", db.PingContext(t.Context()), "closed_connection"},
		{"mysql duplicate", &mysql.MySQLError{Number: 1062}, ""},
		{"postgres unique constraint", &pgconn.PgError{Code: "23505"}, ""},
		{"postgres database credentials", &pgconn.PgError{Code: "28P01"}, ""},
		{"finished transaction", sql.ErrTxDone, ""},
		{"no rows", sql.ErrNoRows, ""},
		{"canceled", context.Canceled, ""},
		{"bare deadline", context.DeadlineExceeded, ""},
		{"unrelated message", errors.New("invalid connection"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("store read: %w", tc.err)
			if code := databaseConnectionCode(err); code != tc.code {
				t.Fatalf("connection code = %q, want %q", code, tc.code)
			}
			if tc.code != "" && !IsDatabaseError(err) {
				t.Fatal("connection error was not recognized as a database error")
			}
		})
	}
}
