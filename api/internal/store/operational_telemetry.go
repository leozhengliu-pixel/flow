package store

import (
	"context"
	"database/sql"
	"errors"

	"flow/api/internal/telemetry"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func (b *metadataWorkBudget) telemetryStats() telemetry.Budget {
	b.mu.Lock()
	defer b.mu.Unlock()
	return telemetry.Budget{Used: int64(b.used), Limit: int64(b.limit), Peak: int64(b.peak), Waiting: int64(b.waiting)}
}

// TelemetryStats reports process totals without reading workspace data. The fast path
// only takes the small work-budget lock.
func (s *SQLiteStore) TelemetryStats(full bool) (out telemetry.LocalStats) {
	if r := s.metadataResidency; r != nil {
		out.Work = r.work.telemetryStats()
		if full {
			r.mu.Lock()
			out.Policy = telemetry.Budget{Used: int64(r.policyBytes), Limit: int64(r.policyLimit)}
			out.Rows = telemetry.Budget{Used: int64(r.cacheBytes), Limit: int64(r.cacheLimit)}
			r.mu.Unlock()
			out.Images = telemetry.Budget{Used: coldDecodeCachedBytes.Load(), Limit: coldDecodeCacheBudget()}
			out.Sources = telemetry.Budget{Used: metadataLogicalDescriptorsUsed.Load(), Limit: metadataLogicalDescriptorCap()}
			out.Spool = telemetry.Budget{Used: metadataLogicalDiskUsed.Load(), Limit: metadataLogicalDiskCap()}
		}
	}
	if full && s.db != nil {
		for i, db := range [...]*sql.DB{s.db.DB, s.db.snapshots, s.db.sqliteReads} {
			if db != nil {
				reads := db.Stats()
				out.DB[i] = telemetry.Pool{
					Open: int64(reads.OpenConnections), InUse: int64(reads.InUse), Idle: int64(reads.Idle),
					MaxOpen: int64(reads.MaxOpenConnections), WaitCount: reads.WaitCount, WaitNanos: int64(reads.WaitDuration),
				}
			}
		}
	}
	return
}

func recordTransactionConflict(ctx context.Context, backend string, err error, retry bool) {
	if err == nil || !telemetry.Enabled() {
		return
	}
	if backend == "" {
		var my *mysql.MySQLError
		var pg *pgconn.PgError
		switch {
		case errors.As(err, &my):
			backend = "mysql"
		case errors.As(err, &pg):
			backend = "postgres"
		case sqliteBusy(err):
			backend = "sqlite"
		}
	}
	reason := ""
	switch {
	case databaseDeadlock(err):
		reason = "deadlock"
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "40001" {
			reason = "serialization"
		}
	case databaseLockWait(err):
		reason = "lock_wait"
	case sqliteBusy(err):
		reason = "busy"
	case errors.Is(err, ErrConcurrentWrite):
		reason = "concurrent_write"
	}
	if reason != "" {
		telemetry.DatabaseRetry(ctx, backend, reason, retry)
	}
}
