package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"strconv"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
)

func (s *SQLiteStore) CheckDatabaseConnection(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// CloseDatabasePoolForTesting simulates loss of SQL without shutting down
// the store's workers or releasing installed workspace metadata.
func (s *SQLiteStore) CloseDatabasePoolForTesting() error {
	if !testing.Testing() {
		return errors.New("closing the live database pool requires a test")
	}
	if s.db.snapshots != nil {
		return errors.Join(s.db.snapshots.Close(), s.db.DB.Close())
	}
	return s.db.DB.Close()
}

// IsDatabaseError reports an error that came from the database or its driver
// (a MySQL, Postgres or SQLite error, a lost connection, a finished
// transaction), however deeply the store wrapped it with %w. Its text names
// tables, columns and SQL states, so callers log it and show a generic
// message instead of passing it to a client or an agent model.
func IsDatabaseError(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysql.MySQLError
	var pgErr *pgconn.PgError
	var sqliteErr *sqlite.Error
	switch {
	case errors.As(err, &mysqlErr), errors.As(err, &pgErr), errors.As(err, &sqliteErr):
		return true
	case IsDatabaseConnectionError(err), errors.Is(err, sql.ErrTxDone):
		return true
	}
	return false
}

// ErrDatabaseWriteBusy means a database lock conflict prevented a mutation
// from committing. Callers can retry the entire operation.
var ErrDatabaseWriteBusy = errors.New("database write is busy")

// ErrDatabaseUnavailable marks a connection failure or deadline in a
// pre-write read. Only callers that know the write has not started wrap it.
var ErrDatabaseUnavailable = errors.New("database temporarily unavailable")

// PreWriteDatabaseError marks retryable SQL read errors before a write starts.
func PreWriteDatabaseError(err error) error {
	if errors.Is(err, ErrDatabaseUnavailable) || (!errors.Is(err, context.DeadlineExceeded) && !IsDatabaseConnectionError(err)) {
		return err
	}
	return errors.Join(ErrDatabaseUnavailable, err)
}

// IsDatabaseConnectionError recognizes typed transport and connection-limit
// errors. A connection failure by itself says nothing about a write's outcome.
func IsDatabaseConnectionError(err error) bool {
	return databaseConnectionCode(err) != ""
}

func databaseConnectionCode(err error) string {
	if err == nil {
		return ""
	}
	var mysqlErr *mysql.MySQLError
	var pgErr *pgconn.PgError
	var pgConnectErr *pgconn.ConnectError
	var networkErr *net.OpError
	switch {
	case errors.Is(err, driver.ErrBadConn):
		return "bad_connection"
	case errors.Is(err, mysql.ErrInvalidConn):
		return "mysql_invalid_connection"
	case errors.Is(err, sql.ErrConnDone), errors.Is(err, pgconn.ErrConnClosed):
		return "closed_connection"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, io.EOF),
		errors.As(err, &networkErr) && errors.Is(networkErr, io.ErrUnexpectedEOF):
		return "connection_lost"
	case pgconn.Timeout(err), errors.As(err, &networkErr) && networkErr.Timeout():
		return "connection_timeout"
	case errors.As(err, &mysqlErr) && (mysqlErr.Number == 1040 || mysqlErr.Number == 2006 || mysqlErr.Number == 2013):
		return "mysql_" + strconv.Itoa(int(mysqlErr.Number))
	case errors.As(err, &pgErr):
		switch pgErr.Code {
		case "08000", "08001", "08003", "08004", "08006", "08007", "08P01", "53300", "57P01", "57P02", "57P03":
			return "postgres_" + pgErr.Code
		}
		// ConnectError can wrap a server rejection such as invalid credentials.
		return ""
	case errors.As(err, &pgConnectErr):
		return "postgres_connect"
	case databasePoolClosed(err):
		return "closed_connection"
	}
	return ""
}

// database/sql does not export the sentinel it returns after DB.Close.
func databasePoolClosed(err error) bool {
	switch err := err.(type) {
	case nil:
		return false
	case interface{ Unwrap() []error }:
		for _, cause := range err.Unwrap() {
			if databasePoolClosed(cause) {
				return true
			}
		}
		return false
	case interface{ Unwrap() error }:
		return databasePoolClosed(err.Unwrap())
	default:
		return err.Error() == "sql: database is closed"
	}
}

// DatabaseErrorCode returns a diagnostic code without SQL text, parameter
// values, hostnames or error messages.
func DatabaseErrorCode(err error) string {
	if code := databaseConnectionCode(err); code != "" {
		return code
	}
	var mysqlErr *mysql.MySQLError
	var pgErr *pgconn.PgError
	var sqliteErr *sqlite.Error
	switch {
	case errors.As(err, &mysqlErr):
		return "mysql_" + strconv.Itoa(int(mysqlErr.Number))
	case errors.As(err, &pgErr):
		if len(pgErr.Code) == 5 {
			for _, c := range pgErr.Code {
				if (c < '0' || c > '9') && (c < 'A' || c > 'Z') {
					return "postgres_error"
				}
			}
			return "postgres_" + pgErr.Code
		}
		return "postgres_error"
	case errors.As(err, &sqliteErr):
		return "sqlite_" + strconv.Itoa(sqliteErr.Code())
	case errors.Is(err, sql.ErrTxDone):
		return "transaction_ended"
	case errors.Is(err, sql.ErrNoRows):
		return "no_rows"
	default:
		return ""
	}
}

// databaseLockWait matches server replies that explicitly refuse a lock,
// excluding connection failures and client-side commit timeouts.
func databaseLockWait(err error) bool {
	var mysqlErr *mysql.MySQLError
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &mysqlErr):
		return mysqlErr.Number == 1205
	case errors.As(err, &pgErr):
		return pgErr.Code == "55P03"
	}
	return false
}

// sqliteBusy reports SQLite's SQLITE_BUSY (any extended code).
func sqliteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 5
}

// databaseDeadlock reports a transaction the database rolled back to break a
// lock cycle or a serialization conflict (MySQL 1213, PostgreSQL 40P01 and
// 40001): nothing of it was written, so the whole attempt can run again.
func databaseDeadlock(err error) bool {
	var mysqlErr *mysql.MySQLError
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &mysqlErr):
		return mysqlErr.Number == 1213
	case errors.As(err, &pgErr):
		return pgErr.Code == "40P01" || pgErr.Code == "40001"
	}
	return false
}
