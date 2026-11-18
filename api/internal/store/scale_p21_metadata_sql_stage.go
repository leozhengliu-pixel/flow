package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Recheck this version when rebasing: the current tree's schema versions end
// at 29, including the catalog and record-journal migrations.
const boundedMetadataStagingSchemaVersion = 30

const metadataSQLIntegrationChunkBytes = MetadataRecordPageMaxBytes
const metadataSQLIntegrationOwnedChunkBytes = 1 << 20

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

// Staged input is kept as one row per appended chunk (operation, column,
// sequence) and assembled once by the statement that consumes it. Appending
// to a single staged value rewrote the whole growing value per chunk (and,
// on MySQL, its undo log), so a staged row of n bytes cost O(n^2 / chunk).
const boundedMetadataStagingChunksSchemaVersion = 31

func (s *SQLiteStore) createBoundedMetadataStagingChunksSchema(ctx context.Context) error {
	text := "TEXT"
	if s.dialect == "mysql" {
		text = "LONGTEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"
	}
	_, err := s.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS bounded_metadata_write_chunks ("+
		"operation_id VARCHAR(64) NOT NULL,part INTEGER NOT NULL,seq INTEGER NOT NULL,chunk "+text+" NOT NULL,"+
		"PRIMARY KEY(operation_id,part,seq))")
	return err
}

// metadataSQLStageConcatFanIn stays below PostgreSQL's 100-argument function
// limit and SQLite's default 127.
const metadataSQLStageConcatFanIn = 64

// metadataSQLStageConcat is one SQL expression joining the parts in order:
// a single concat() (no aggregate, no session limit) per at most 64 parts,
// nested for more. An empty list is the empty string.
func metadataSQLStageConcat(parts []string) string {
	if len(parts) == 0 {
		return "''"
	}
	for len(parts) > 1 {
		var next []string
		for start := 0; start < len(parts); start += metadataSQLStageConcatFanIn {
			group := parts[start:min(start+metadataSQLStageConcatFanIn, len(parts))]
			if len(group) == 1 {
				next = append(next, group[0])
			} else {
				next = append(next, "concat("+strings.Join(group, ",")+")")
			}
		}
		parts = next
	}
	return parts[0]
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
	// chunks counts the chunk rows staged per column (data, content).
	chunks                   [2]int
	statements, inputBytes   int
	appendChunkBytes         int
	admittedAppendChunkBytes int
	appendOwner              *integrationDeliveryPageOwner
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
	ownedChunk, err := integrationDeliverySQLChunkBytes(ctx, tx)
	if err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	stage := &metadataSQLWriteStage{
		tx: tx, id: hex.EncodeToString(nonce[:]), target: target,
		firstWrite:       tx.writtenBytes == 0,
		appendChunkBytes: metadataRecordChunkBytes,
	}
	event := integrationDeliveryStreamEvent(ctx)
	if (event == "integration_delivery.claimed" || event == "integration_delivery.retry") &&
		(target.workspaceState || target.field == "integrationDeliveries" || target.field == "integrationConnections") {
		// Event-only callers retain their existing 2 MiB scratch bound.
		// The private live owner additionally admits a 1 MiB SQL append.
		stage.appendChunkBytes = metadataSQLIntegrationChunkBytes
		if ownedChunk != 0 {
			stage.appendChunkBytes = ownedChunk
			stage.appendOwner, _ = ctx.Value(integrationDeliveryPagesKey{}).(*integrationDeliveryPageOwner)
		}
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
	if _, err := s.exec(ctx, metadataSQLStageDeleteChunks, s.id); err != nil {
		return err
	}
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
	if err := s.checkAppendOwner(ctx); err != nil {
		return nil, err
	}
	columnIndex := 0
	if column == "content" {
		columnIndex = 1
	}
	chunk := min(s.appendChunkBytes, s.expectedBytes(columnIndex))
	return &metadataSQLStageWriter{
		ctx: ctx, stage: s, column: column, chunk: chunk,
		buffer: make([]byte, 0, chunk),
	}, nil
}

// growingWriter is writer whose appends grow geometrically up to
// metadataSQLStageMaxChunkBytes while store admits each larger buffer (the
// buffer, its SQL argument copy and the driver's bound copy). Without
// admission it keeps its current chunk; the staged bytes are the same.
func (s *metadataSQLWriteStage) growingWriter(ctx context.Context, column string, store *SQLiteStore) (*metadataSQLStageWriter, error) {
	writer, err := s.writer(ctx, column)
	// Integration delivery stages keep their own fixed append size.
	if err == nil && store != nil && s.appendChunkBytes == metadataRecordChunkBytes {
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
	// The staged chunks of this operation's column, in order, each a primary
	// key lookup correlated with the operation's stage row.
	part := 0
	if column == "content" {
		part = 1
	}
	pieces := make([]string, s.chunks[part])
	for seq := range pieces {
		pieces[seq] = "(SELECT chunk FROM bounded_metadata_write_chunks WHERE operation_id=bounded_metadata_write_stage.operation_id" +
			" AND part=" + strconv.Itoa(part) + " AND seq=" + strconv.Itoa(seq) + ")"
	}
	assembled := metadataSQLStageConcat(pieces)
	value := assembled
	if asBytes {
		switch s.tx.dialect {
		case "sqlite":
			value = "CAST(" + assembled + " AS BLOB)"
		case "mysql":
			value = "CAST(" + assembled + " AS BINARY)"
		case "postgres":
			value = "convert_to(" + assembled + ",'UTF8')"
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
	if err := w.stage.checkAppendOwner(w.ctx); err != nil {
		w.buffer = nil
		return 0, err
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
	result, err := w.stage.exec(w.ctx, operation, w.stage.id, w.stage.chunks[column], string(w.buffer[:end]))
	if err != nil {
		return err
	}
	w.stage.chunks[column]++
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
