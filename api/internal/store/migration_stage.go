package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
)

const (
	sw26MigrationStageChunk    = int64(1 << 20)
	sw26MigrationStagePageSize = int64(4096)
)

type sw26MigrationStage struct {
	db       *sql.Conn
	pool     *sql.DB
	path     string
	reserved int64
	logical  bool
	demand   *atomic.Int64
}

func sw26MigrationReserve(bytes int64) error {
	for {
		used := recordSpoolBytes.Load()
		if bytes < 0 || bytes > recordSpoolMaxBytes()-used {
			return ErrRecordSpoolBudget
		}
		if recordSpoolBytes.CompareAndSwap(used, used+bytes) {
			return nil
		}
	}
}

func sw26NewMigrationStage(ctx context.Context) (_ *sw26MigrationStage, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, _ := ctx.Value(metadataLogicalScratchKey{}).(metadataLogicalScratch)
	stage := &sw26MigrationStage{logical: config.logical, demand: metadataLogicalDiskDemand(ctx)}
	if err := stage.reserve(sw26MigrationStageChunk); err != nil {
		return nil, err
	}
	stage.reserved = sw26MigrationStageChunk
	defer func() {
		if err != nil {
			err = errors.Join(err, stage.Close())
		}
	}()
	file, err := os.CreateTemp(metadataSpoolDirectory(config.directory), "flow-p21-sw26-stage-*.db")
	if err != nil {
		return nil, err
	}
	stage.path = file.Name()
	if err := file.Close(); err != nil {
		return nil, err
	}
	stage.pool, err = sql.Open("sqlite", stage.path)
	if err != nil {
		return nil, err
	}
	stage.db, err = stage.pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	// Only this connection uses the scratch file. Keep the file lock and
	// the connection-local page ceiling across all staging statements.
	for _, query := range []string{
		"PRAGMA locking_mode=EXCLUSIVE", "PRAGMA journal_mode=OFF", "PRAGMA synchronous=OFF",
		"PRAGMA page_size=4096", "PRAGMA cache_size=-512",
		"PRAGMA mmap_size=0", "PRAGMA temp_store=FILE",
	} {
		if _, err := stage.db.ExecContext(ctx, query); err != nil {
			return nil, err
		}
	}
	var pageSize int64
	if err := stage.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return nil, err
	}
	if pageSize != sw26MigrationStagePageSize {
		return nil, ErrRecordSpoolBudget
	}
	if err := stage.setPageLimit(ctx); err != nil {
		return nil, err
	}
	return stage, nil
}

func (s *sw26MigrationStage) setPageLimit(ctx context.Context) error {
	limit := s.reserved / sw26MigrationStagePageSize
	var actual int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", limit)).Scan(&actual); err != nil {
		return err
	}
	if actual != limit {
		return ErrRecordSpoolBudget
	}
	return nil
}

func (s *sw26MigrationStage) exec(ctx context.Context, bytes int64, query string, args ...any) (sql.Result, error) {
	// B-tree page splits and indexes need space beyond encoded values. Reserve
	// before SQLite can grow, and enforce the same physical page ceiling.
	if bytes < 0 || bytes > metadataPolicyBytes {
		return nil, ErrMetadataWorkBudget
	}
	var pages int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return nil, err
	}
	want := pages*sw26MigrationStagePageSize + bytes*4 + 64<<10
	if want > s.reserved {
		grow := ((want - s.reserved + sw26MigrationStageChunk - 1) / sw26MigrationStageChunk) * sw26MigrationStageChunk
		if err := s.reserve(grow); err != nil {
			return nil, err
		}
		s.reserved += grow
		if err := s.setPageLimit(ctx); err != nil {
			return nil, err
		}
	}
	return s.db.ExecContext(ctx, query, args...)
}

func (s *sw26MigrationStage) Close() error {
	if s == nil {
		return nil
	}
	var result error
	if s.db != nil {
		result = s.db.Close()
		s.db = nil
	}
	if s.pool != nil {
		result = errors.Join(result, s.pool.Close())
		s.pool = nil
	}
	if s.path != "" {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Join(result, err)
		}
		s.path = ""
	}
	if s.logical {
		metadataLogicalDiskUsed.Add(-s.reserved)
		if s.demand != nil {
			s.demand.Add(-s.reserved)
		}
	} else {
		recordSpoolBytes.Add(-s.reserved)
	}
	s.reserved = 0
	return result
}

func (s *sw26MigrationStage) reserve(bytes int64) error {
	if !s.logical {
		return sw26MigrationReserve(bytes)
	}
	limit := metadataLogicalDiskCap()
	if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, bytes, limit) {
		if metadataLogicalDiskContended(s.demand, bytes, limit) {
			return ErrRecordSpoolContended
		}
		return ErrRecordSpoolBudget
	}
	if s.demand != nil {
		s.demand.Add(bytes)
	}
	return nil
}

type sw26EventReaderAt struct {
	ctx  context.Context
	tx   *sqlTx
	id   string
	size int64
}

func (r *sw26EventReaderAt) ReadAt(output []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	if len(output) == 0 {
		return 0, nil
	}
	if offset >= r.size {
		return 0, io.EOF
	}
	data := "payload"
	if r.tx.dialect == "sqlite" {
		data = "CAST(payload AS BLOB)"
	}
	n := 0
	for n < len(output) && offset+int64(n) < r.size {
		size := min(int64(metadataRecordChunkBytes), int64(len(output)-n), r.size-offset-int64(n))
		var part []byte
		if err := r.tx.QueryRowContext(r.ctx, `SELECT SUBSTR(`+data+`,?,?) FROM domain_events WHERE id=?`, offset+int64(n)+1, size, r.id).Scan(&part); err != nil {
			return n, err
		}
		if int64(len(part)) != size {
			return n, io.ErrUnexpectedEOF
		}
		n += copy(output[n:], part)
	}
	if n != len(output) {
		return n, io.EOF
	}
	return n, nil
}
