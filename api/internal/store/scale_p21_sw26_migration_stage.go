package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
)

const sw26MigrationStageChunk = int64(1 << 20)

type sw26MigrationStage struct {
	db       *sql.DB
	path     string
	reserved int64
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
	if err := sw26MigrationReserve(sw26MigrationStageChunk); err != nil {
		return nil, err
	}
	stage := &sw26MigrationStage{reserved: sw26MigrationStageChunk}
	defer func() {
		if err != nil {
			err = errors.Join(err, stage.Close())
		}
	}()
	file, err := os.CreateTemp("", "flow-p21-sw26-stage-*.db")
	if err != nil {
		return nil, err
	}
	stage.path = file.Name()
	if err := file.Close(); err != nil {
		return nil, err
	}
	stage.db, err = sql.Open("sqlite", stage.path)
	if err != nil {
		return nil, err
	}
	stage.db.SetMaxOpenConns(1)
	for _, query := range []string{
		"PRAGMA journal_mode=OFF", "PRAGMA synchronous=OFF",
		"PRAGMA page_size=4096", "PRAGMA cache_size=-512",
		"PRAGMA mmap_size=0", "PRAGMA temp_store=FILE",
		fmt.Sprintf("PRAGMA max_page_count=%d", stage.reserved/4096),
	} {
		if _, err := stage.db.ExecContext(ctx, query); err != nil {
			return nil, err
		}
	}
	return stage, nil
}

func (s *sw26MigrationStage) exec(ctx context.Context, bytes int64, query string, args ...any) (sql.Result, error) {
	var pages, pageSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return nil, err
	}
	// B-tree page splits and indexes need space beyond encoded values. Reserve
	// before SQLite can grow, and enforce the same physical page ceiling.
	if bytes < 0 || bytes > metadataPolicyBytes {
		return nil, ErrMetadataWorkBudget
	}
	want := pages*pageSize + bytes*4 + 64<<10
	if want > s.reserved {
		grow := ((want - s.reserved + sw26MigrationStageChunk - 1) / sw26MigrationStageChunk) * sw26MigrationStageChunk
		if err := sw26MigrationReserve(grow); err != nil {
			return nil, err
		}
		s.reserved += grow
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", s.reserved/pageSize)); err != nil {
			return nil, err
		}
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return nil, err
	}
	if pages*pageSize > s.reserved {
		return nil, ErrRecordSpoolBudget
	}
	return result, nil
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
	if s.path != "" {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Join(result, err)
		}
		s.path = ""
	}
	recordSpoolBytes.Add(-s.reserved)
	s.reserved = 0
	return result
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
