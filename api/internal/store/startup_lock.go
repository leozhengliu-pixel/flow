package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultStartupLockTimeout bounds how long a server waits for another one
// to finish the startup schema work (FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT).
// A long MySQL index build on a large workspace can take minutes.
const DefaultStartupLockTimeout = 10 * time.Minute

// Postgres advisory lock keys for the startup lock: the two-int4 form keeps
// the key readable in pg_locks (classid, objid). Advisory locks are scoped
// to the current database, so two Flow databases on one server do not wait
// for each other.
const (
	postgresStartupLockClass = 0x466c6f77 // "Flow"
	postgresStartupLockID    = 1
)

// ErrStartupLockTimeout is returned when another server held the startup
// lock for longer than the configured timeout.
var ErrStartupLockTimeout = errors.New("timed out waiting for the database startup lock")

// startupLockBackend is one database's mutual-exclusion primitive. tryLock
// never blocks; the wait loop lives in acquireStartupLock so every dialect
// shares the same timeout, logging and keep-alive behaviour.
type startupLockBackend interface {
	tryLock(context.Context) (bool, error)
	// holder describes the current holder for logs (best effort).
	holder(context.Context) string
	// unlock releases the lock; held is false when the lock had already
	// been lost (for example after the lock connection dropped).
	unlock(context.Context) (held bool, err error)
	// ping keeps the lock connection alive and reports when it is gone.
	ping(context.Context) error
	close() error
}

type startupLockOptions struct {
	name      string // what the lock guards, for logs
	timeout   time.Duration
	poll      time.Duration // first retry interval; doubles up to maxPoll
	maxPoll   time.Duration
	logEvery  time.Duration
	keepAlive time.Duration
}

func (o startupLockOptions) withDefaults() startupLockOptions {
	if o.timeout <= 0 {
		o.timeout = DefaultStartupLockTimeout
	}
	if o.poll <= 0 {
		o.poll = 250 * time.Millisecond
	}
	if o.maxPoll <= 0 {
		o.maxPoll = 2 * time.Second
	}
	if o.logEvery <= 0 {
		o.logEvery = 30 * time.Second
	}
	if o.keepAlive <= 0 {
		o.keepAlive = 30 * time.Second
	}
	return o
}

// startupLock is a held lock. release is safe to call more than once.
type startupLock struct {
	backend  startupLockBackend
	name     string
	acquired time.Time
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

// acquireStartupLock waits until backend's lock is free, logging who holds
// it, and fails with ErrStartupLockTimeout after options.timeout.
func acquireStartupLock(ctx context.Context, backend startupLockBackend, options startupLockOptions) (*startupLock, error) {
	options = options.withDefaults()
	started := time.Now()
	deadline := started.Add(options.timeout)
	poll := options.poll
	var lastLog time.Time
	lastHolder := ""
	for {
		acquired, err := backend.tryLock(ctx)
		if err != nil {
			_ = backend.close()
			return nil, fmt.Errorf("acquire %s lock: %w", options.name, err)
		}
		if acquired {
			if waited := time.Since(started); lastLog.IsZero() {
				slog.Debug("acquired startup lock", "lock", options.name)
			} else {
				slog.Info("acquired startup lock", "lock", options.name, "waited_ms", waited.Milliseconds(), "previous_holder", lastHolder)
			}
			lock := &startupLock{backend: backend, name: options.name, acquired: time.Now(), stop: make(chan struct{}), done: make(chan struct{})}
			go lock.keepAlive(options.keepAlive)
			return lock, nil
		}
		now := time.Now()
		if lastLog.IsZero() || now.Sub(lastLog) >= options.logEvery {
			lastHolder = backend.holder(ctx)
			if lastLog.IsZero() {
				slog.Info("another server holds the startup lock; waiting", "lock", options.name, "holder", lastHolder, "timeout", options.timeout.String())
			} else {
				slog.Info("still waiting for the startup lock", "lock", options.name, "holder", lastHolder, "waited_s", int(now.Sub(started).Seconds()))
			}
			lastLog = now
		}
		if !now.Before(deadline) {
			holder := backend.holder(ctx)
			_ = backend.close()
			return nil, fmt.Errorf("%w (%s) after %s; holder: %s. Another Flow server is still running the startup schema work on this database: wait for it to finish or raise FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT. If no other server is starting, end the holder's database session", ErrStartupLockTimeout, options.name, options.timeout, holder)
		}
		wait := min(poll, time.Until(deadline))
		select {
		case <-ctx.Done():
			_ = backend.close()
			return nil, ctx.Err()
		case <-time.After(max(wait, time.Millisecond)):
		}
		poll = min(poll*2, options.maxPoll)
	}
}

func (l *startupLock) keepAlive(interval time.Duration) {
	defer close(l.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := l.backend.ping(ctx)
			cancel()
			if err != nil {
				slog.Error("startup lock connection failed; another server may start the same schema work", "lock", l.name, "error", err)
			}
		}
	}
}

func (l *startupLock) release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		close(l.stop)
		<-l.done
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		held, err := l.backend.unlock(ctx)
		switch {
		case err != nil:
			slog.Warn("release startup lock", "lock", l.name, "error", err)
		case !held:
			slog.Warn("startup lock was lost before release (its database connection dropped)", "lock", l.name)
		}
		_ = l.backend.close()
		slog.Debug("released startup lock", "lock", l.name, "held_ms", time.Since(l.acquired).Milliseconds())
	})
}

// startupProcessName identifies this process to other waiting servers.
func startupProcessName() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("flow %s pid %d", host, os.Getpid())
}

// openStartupLockBackend returns the cross-process lock for a database, or
// nil for an in-memory SQLite database (nothing else can open it).
func openStartupLockBackend(ctx context.Context, dialect, sqlDriver, dsn, sqlitePath string) (startupLockBackend, error) {
	switch dialect {
	case "postgres", "mysql":
		// A dedicated single-connection pool: session-level locks stay with
		// that session, and the store's own pool (which may be sized to one
		// connection) stays free for the migration itself.
		db, err := sql.Open(sqlDriver, dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		conn, err := db.Conn(ctx)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("open startup lock connection: %w", err)
		}
		if dialect == "postgres" {
			// Shown in pg_stat_activity so waiting servers can name the holder.
			_, _ = conn.ExecContext(ctx, `SELECT set_config('application_name', $1, false)`, truncate(startupProcessName(), 63))
			return &postgresStartupLock{db: db, conn: conn}, nil
		}
		var database sql.NullString
		if err := conn.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&database); err != nil {
			conn.Close()
			db.Close()
			return nil, fmt.Errorf("read current database: %w", err)
		}
		return &mysqlStartupLock{db: db, conn: conn, name: mysqlStartupLockName(database.String)}, nil
	case "sqlite":
		path := sqliteLockPath(sqlitePath)
		if path == "" {
			return nil, nil
		}
		return newFileStartupLock(path), nil
	}
	return nil, nil
}

func truncate(value string, limit int) string {
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

// mysqlStartupLockName scopes the server-wide MySQL user lock to one
// database (GET_LOCK names are global to the server and at most 64 chars).
func mysqlStartupLockName(database string) string {
	name := "flow_migrate:" + database
	if len(name) <= 64 {
		return name
	}
	sum := sha256.Sum256([]byte(database))
	return "flow_migrate:" + hex.EncodeToString(sum[:])[:40]
}

// sqliteLockPath is the lock file beside a SQLite database, or "" for an
// in-memory database.
func sqliteLockPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || strings.Contains(path, ":memory:") || strings.Contains(path, "mode=memory") || strings.HasPrefix(path, "file:") {
		return ""
	}
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	return path + ".startup-lock"
}

type postgresStartupLock struct {
	db   *sql.DB
	conn *sql.Conn
}

func (l *postgresStartupLock) tryLock(ctx context.Context) (bool, error) {
	var acquired bool
	err := l.conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, postgresStartupLockClass, postgresStartupLockID).Scan(&acquired)
	return acquired, err
}

func (l *postgresStartupLock) holder(ctx context.Context) string {
	var pid int64
	var application, client string
	err := l.conn.QueryRowContext(ctx, `SELECT a.pid, COALESCE(a.application_name, ''), COALESCE(host(a.client_addr), 'local')
FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
WHERE l.locktype = 'advisory' AND l.granted AND l.classid::bigint = $1 AND l.objid::bigint = $2 AND l.objsubid = 2
AND l.database = (SELECT oid FROM pg_database WHERE datname = current_database()) LIMIT 1`, postgresStartupLockClass, postgresStartupLockID).Scan(&pid, &application, &client)
	if err != nil {
		return "unknown"
	}
	return fmt.Sprintf("backend pid %d (%s) from %s", pid, application, client)
}

func (l *postgresStartupLock) unlock(ctx context.Context) (bool, error) {
	var held bool
	err := l.conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1, $2)`, postgresStartupLockClass, postgresStartupLockID).Scan(&held)
	return held, err
}

func (l *postgresStartupLock) ping(ctx context.Context) error { return l.conn.PingContext(ctx) }

func (l *postgresStartupLock) close() error {
	// Closing the session also releases a lock still held after a failed
	// unlock.
	return errors.Join(l.conn.Close(), l.db.Close())
}

type mysqlStartupLock struct {
	db   *sql.DB
	conn *sql.Conn
	name string
}

func (l *mysqlStartupLock) tryLock(ctx context.Context) (bool, error) {
	var result sql.NullInt64
	if err := l.conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, l.name).Scan(&result); err != nil {
		return false, err
	}
	if !result.Valid {
		return false, fmt.Errorf("GET_LOCK(%q) failed", l.name)
	}
	return result.Int64 == 1, nil
}

func (l *mysqlStartupLock) holder(ctx context.Context) string {
	var id sql.NullInt64
	if err := l.conn.QueryRowContext(ctx, `SELECT IS_USED_LOCK(?)`, l.name).Scan(&id); err != nil || !id.Valid {
		return "unknown"
	}
	var host sql.NullString
	var seconds sql.NullInt64
	if err := l.conn.QueryRowContext(ctx, `SELECT HOST, TIME FROM information_schema.PROCESSLIST WHERE ID = ?`, id.Int64).Scan(&host, &seconds); err != nil {
		return fmt.Sprintf("connection %d", id.Int64)
	}
	return fmt.Sprintf("connection %d from %s (connected %ds)", id.Int64, host.String, seconds.Int64)
}

func (l *mysqlStartupLock) unlock(ctx context.Context) (bool, error) {
	var result sql.NullInt64
	if err := l.conn.QueryRowContext(ctx, `SELECT RELEASE_LOCK(?)`, l.name).Scan(&result); err != nil {
		return false, err
	}
	return result.Valid && result.Int64 == 1, nil
}

func (l *mysqlStartupLock) ping(ctx context.Context) error { return l.conn.PingContext(ctx) }

func (l *mysqlStartupLock) close() error { return errors.Join(l.conn.Close(), l.db.Close()) }

// withStartupLock runs action while holding the database's startup lock.
func withStartupLock(ctx context.Context, backend startupLockBackend, options startupLockOptions, action func() error) error {
	if backend == nil {
		return action()
	}
	lock, err := acquireStartupLock(ctx, backend, options)
	if err != nil {
		return err
	}
	defer lock.release()
	return action()
}
