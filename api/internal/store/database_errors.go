package store

import (
	"database/sql"
	"database/sql/driver"
	"errors"

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

// sqliteBusy reports SQLite's SQLITE_BUSY (any extended code).
func sqliteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 5
}
