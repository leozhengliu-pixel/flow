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
	appendChunkBytes               int
	admittedAppendChunkBytes       int
	appendOwner                    *integrationDeliveryPageOwner
}

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
	if chunk < 0 || chunk > metadataSQLIntegrationOwnedChunkBytes {
		// admitTarget accepts only the fixed append sizes; the buffer never
		// exceeds the largest of them.
		return nil, ErrBoundedMetadataCoverage
	}
	return &metadataSQLStageWriter{
		ctx: ctx, stage: s, column: column,
		buffer: make([]byte, 0, chunk),
	}, nil
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
		take := min(len(part), cap(w.buffer)-len(w.buffer))
		w.buffer = append(w.buffer, part[:take]...)
		part = part[take:]
		if len(w.buffer) == cap(w.buffer) {
			if err := w.flush(false); err != nil {
				return original - len(part), err
			}
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
