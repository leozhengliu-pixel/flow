package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"unicode/utf8"
)

// Recheck this version when rebasing: the current tree's schema versions end
// at 29, including the catalog and record-journal migrations.
const boundedMetadataStagingSchemaVersion = 30

var ErrMetadataSQLStageOpen = errors.New("metadata SQL staging is still open")

func (s *SQLiteStore) createBoundedMetadataStagingSchema(ctx context.Context) error {
	text := "TEXT"
	if s.dialect == "mysql" {
		text = "LONGTEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"
	}
	_, err := s.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS bounded_metadata_write_stage ("+
		"operation_id VARCHAR(64) PRIMARY KEY,data "+text+" NOT NULL,content "+text+" NOT NULL)")
	return err
}

// The installed schema needs only ordinary DML during a write. Each row is
// inserted and removed in the same transaction. An interrupted operation's
// rollback also removes its insert, so committed staging rows never exist.
type metadataSQLWriteStage struct {
	tx                             *sqlTx
	id                             string
	closed                         bool
	target                         metadataSQLStageTarget
	admittedTarget                 metadataSQLStageTarget
	admitted, inserted, firstWrite bool
	written                        [2]int
	consumed                       [2]bool
	statements, inputBytes         int
	// releases return the admitted growth of this stage's writers.
	releases []func()
}

// metadataSQLStageMaxChunkBytes bounds one staged append. Every append
// rewrites the staged value (data || ?), so fixed metadataRecordChunkBytes
// appends made a staged row of n bytes cost O(n²/16 KiB) SQL I/O (a 5 MiB
// row rewrote about 800 MiB). A growing writer doubles its chunk after each
// append while the store admits the larger buffer (at most 3 x 240 KiB, below
// the 1 MiB body-independence margin the streamed writers prove), which cuts
// that rewrite 16-fold.
const metadataSQLStageMaxChunkBytes = 256 << 10

func beginMetadataSQLWriteStage(ctx context.Context, tx *sqlTx, target metadataSQLStageTarget) (*metadataSQLWriteStage, error) {
	if tx == nil || !target.valid() {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	stage := &metadataSQLWriteStage{
		tx: tx, id: hex.EncodeToString(nonce[:]), target: target,
		firstWrite: tx.writtenBytes == 0,
	}
	if tx.metadataSQLStageOwners == nil {
		tx.metadataSQLStageOwners = map[string]*metadataSQLWriteStage{}
	}
	tx.metadataSQLStageOwners[stage.id] = stage
	tx.metadataSQLStages++
	if err := stage.admitTarget(); err != nil {
		delete(tx.metadataSQLStageOwners, stage.id)
		tx.metadataSQLStages--
		stage.closed = true
		return nil, err
	}
	if _, err := stage.exec(ctx, metadataSQLStageInsert, stage.id); err != nil {
		_ = stage.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	stage.inserted = true
	return stage, nil
}

// Close errors must be returned on the successful mutation path. Otherwise a
// caller could commit a staging row after a failed delete. Error paths also
// defer Close with an uncancelled context before rolling back the mutation.
func (s *metadataSQLWriteStage) Close(ctx context.Context) error {
	if s == nil || s.closed {
		return nil
	}
	// A writer's buffer is unreachable once its stage closes or fails.
	for _, release := range s.releases {
		release()
	}
	s.releases = nil
	if _, err := s.exec(ctx, metadataSQLStageDelete, s.id); err != nil {
		return err
	}
	s.closed = true
	delete(s.tx.metadataSQLStageOwners, s.id)
	s.tx.metadataSQLStages--
	return nil
}

func metadataSQLStageColumn(column string) bool {
	return column == "data" || column == "content"
}

func (s *metadataSQLWriteStage) writer(ctx context.Context, column string) (*metadataSQLStageWriter, error) {
	if !s.open() || !s.inserted || !metadataSQLStageColumn(column) || column == "content" && s.target.searchBytes < 0 {
		return nil, ErrMetadataRecordQuery
	}
	return &metadataSQLStageWriter{
		ctx: ctx, stage: s, column: column, chunk: metadataRecordChunkBytes,
		buffer: make([]byte, 0, metadataRecordChunkBytes),
	}, nil
}

// growingWriter is writer whose appends grow geometrically up to
// metadataSQLStageMaxChunkBytes while store admits each larger buffer (the
// buffer, its SQL argument copy and the driver's bound copy). Without
// admission it keeps its current chunk; the staged bytes are the same.
func (s *metadataSQLWriteStage) growingWriter(ctx context.Context, column string, store *SQLiteStore) (*metadataSQLStageWriter, error) {
	writer, err := s.writer(ctx, column)
	if err == nil && store != nil {
		writer.admit = func(size int) error {
			_, release, err := store.tryAdmitAdditionalMetadataWork(ctx, size)
			if err == nil {
				s.releases = append(s.releases, release)
			}
			return err
		}
	}
	return writer, err
}

// valueSQL has exactly one operation-ID placeholder. The caller binds s.id
// at that position. Column names are restricted to this internal schema.
func (s *metadataSQLWriteStage) valueSQL(column string, asBytes bool) (string, error) {
	if !s.open() || !metadataSQLStageColumn(column) {
		return "", ErrMetadataRecordQuery
	}
	value := column
	if asBytes {
		switch s.tx.dialect {
		case "sqlite":
			value = "CAST(" + column + " AS BLOB)"
		case "mysql":
			value = "CAST(" + column + " AS BINARY)"
		case "postgres":
			value = "convert_to(" + column + ",'UTF8')"
		default:
			return "", ErrBoundedMetadataCoverage
		}
	}
	return "(SELECT " + value + " FROM bounded_metadata_write_stage WHERE operation_id=?)", nil
}

type metadataSQLStageWriter struct {
	ctx    context.Context
	stage  *metadataSQLWriteStage
	column string
	buffer []byte
	chunk  int
	admit  func(int) error
}

func (w *metadataSQLStageWriter) grow() {
	if w.admit == nil || w.chunk >= metadataSQLStageMaxChunkBytes {
		return
	}
	next := min(w.chunk*2, metadataSQLStageMaxChunkBytes)
	if w.admit((next-w.chunk)*3) != nil {
		w.admit = nil
		return
	}
	buffer := make([]byte, len(w.buffer), next)
	copy(buffer, w.buffer)
	w.buffer, w.chunk = buffer, next
}

func (w *metadataSQLStageWriter) Write(part []byte) (int, error) {
	if w == nil || !w.stage.open() || !metadataSQLStageColumn(w.column) {
		return 0, ErrMetadataRecordQuery
	}
	column := 0
	if w.column == "content" {
		column = 1
	}
	if w.stage.consumed[column] || len(part) > w.stage.expectedBytes(column)-w.stage.written[column]-len(w.buffer) {
		return 0, ErrBoundedMetadataCoverage
	}
	original := len(part)
	for len(part) > 0 {
		take := min(len(part), w.chunk-len(w.buffer))
		w.buffer = append(w.buffer, part[:take]...)
		part = part[take:]
		if len(w.buffer) == w.chunk {
			if err := w.flush(false); err != nil {
				return original - len(part), err
			}
			w.grow()
		}
	}
	return original, nil
}

func (w *metadataSQLStageWriter) flush(final bool) error {
	if w == nil || !w.stage.open() || !metadataSQLStageColumn(w.column) {
		return ErrMetadataRecordQuery
	}
	end := len(w.buffer)
	if !final {
		for end > 0 && !utf8.Valid(w.buffer[:end]) && len(w.buffer)-end < utf8.UTFMax {
			end--
		}
	}
	if !utf8.Valid(w.buffer[:end]) || end == 0 && final && len(w.buffer) != 0 {
		return ErrMetadataRecordQuery
	}
	if end == 0 {
		return w.ctx.Err()
	}
	operation, column := metadataSQLStageAppendData, 0
	if w.column == "content" {
		operation, column = metadataSQLStageAppendSearch, 1
	}
	result, err := w.stage.exec(w.ctx, operation, string(w.buffer[:end]), w.stage.id)
	if err != nil {
		return err
	}
	w.stage.written[column] += end
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		if err != nil {
			return err
		}
		return ErrConcurrentWrite
	}
	copy(w.buffer, w.buffer[end:])
	w.buffer = w.buffer[:len(w.buffer)-end]
	return nil
}
