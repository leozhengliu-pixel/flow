package store

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"strconv"
	"syscall"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
)

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
	var pgConnectErr *pgconn.ConnectError
	var sqliteErr *sqlite.Error
	switch {
	case errors.As(err, &mysqlErr), errors.As(err, &pgErr), errors.As(err, &pgConnectErr), errors.As(err, &sqliteErr):
		return true
	case errors.Is(err, driver.ErrBadConn), errors.Is(err, mysql.ErrInvalidConn), errors.Is(err, sql.ErrConnDone), errors.Is(err, sql.ErrTxDone):
		return true
	}
	return false
}

// ErrDatabaseWriteBusy means a database lock conflict prevented a mutation
// from committing. Callers can retry the entire operation.
var ErrDatabaseWriteBusy = errors.New("database write is busy")

// ErrDatabaseUnavailable marks a connection failure in a pre-write read.
// Only the caller that knows the write has not started should wrap it.
var ErrDatabaseUnavailable = errors.New("database temporarily unavailable")

func preWriteDatabaseError(err error) error {
	if !IsDatabaseConnectionError(err) {
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
	var mysqlErr *mysql.MySQLError
	var pgErr *pgconn.PgError
	var networkErr *net.OpError
	switch {
	case errors.Is(err, driver.ErrBadConn):
		return "bad_connection"
	case errors.Is(err, mysql.ErrInvalidConn):
		return "mysql_invalid_connection"
	case errors.Is(err, sql.ErrConnDone):
		return "closed_connection"
	case errors.As(err, &networkErr) && errors.Is(networkErr, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.As(err, &networkErr) && (errors.Is(networkErr, syscall.ECONNRESET) || errors.Is(networkErr, syscall.EPIPE)):
		return "connection_lost"
	case errors.As(err, &mysqlErr) && mysqlErr.Number == 1040:
		return "mysql_1040"
	case errors.As(err, &pgErr) && (pgErr.Code == "53300" || pgErr.Code == "57P01"):
		return "postgres_" + pgErr.Code
	default:
		return ""
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
	var pgConnectErr *pgconn.ConnectError
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
	case errors.As(err, &pgConnectErr):
		return "postgres_connect"
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
