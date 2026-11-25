package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"time"
)

// SQLite snapshot pool (bounded metadata on).
//
// SQLite runs on one connection (writes serialize there). With bounded
// metadata on, read-only transactions are long: a workspace reload after a
// COLD legacy write reads every metadata record and builds and normalizes
// its spools inside one, a COLD facade or legacy view decodes from one,
// authorization and record reads keep one open while they resolve. On the
// one connection each of them stopped every other statement of the replica
// (sign-in, issue reads, writes) for its whole duration; the load rig showed
// HOT requests queued behind them for about a second each.
//
// Read-only transactions (sql.TxOptions.ReadOnly: metadataReadOptions and
// the other read snapshots) therefore run on a separate pool of
// FLOW_SQLITE_SNAPSHOT_CONNS (default 2) connections to the same file, with
// the same connection settings. One additional connection serves standalone
// SELECTs, such as live authorization, independently of those snapshots.
// In WAL mode a read transaction sees the commits made before its first read, on
// whichever connection: the same snapshot it saw on the main connection,
// only no longer excluding other statements while it runs. Writes stay on
// the main connection, one at a time. The pool is used only for a
// file-backed database in WAL mode and only with bounded metadata on; flag
// off, and for in-memory databases, nothing changes.

func (d *sqlDatabase) openSQLiteSnapshotPool(sqlDriver, dsn, path string, mainConns int) error {
	if mainConns != 1 || strings.Contains(path, ":memory:") || strings.Contains(path, "mode=memory") {
		return nil
	}
	conns := envPositiveInt("FLOW_SQLITE_SNAPSHOT_CONNS", 2)
	if raw := strings.TrimSpace(strings.ToLower(os.Getenv("FLOW_SQLITE_SNAPSHOT_POOL"))); raw == "off" || raw == "0" || raw == "false" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mode string
	if err := d.DB.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return err
	}
	if !strings.EqualFold(mode, "wal") {
		return nil
	}
	pool, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return err
	}
	pool.SetMaxOpenConns(conns)
	pool.SetMaxIdleConns(conns)
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return err
	}
	reads, err := sql.Open(sqlDriver, dsn+"&_pragma=query_only(1)")
	if err != nil {
		pool.Close()
		return err
	}
	reads.SetMaxOpenConns(1)
	reads.SetMaxIdleConns(1)
	if err := reads.PingContext(ctx); err != nil {
		reads.Close()
		pool.Close()
		return err
	}
	d.snapshots = pool
	d.sqliteReads = reads
	return nil
}
