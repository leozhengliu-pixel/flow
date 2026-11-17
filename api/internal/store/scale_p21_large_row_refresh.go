package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
)

// largeRowRefresh is a conflicted canonical row too large for the page-sized
// refresh batch: it is streamed into a spool and decoded with its string
// members framed.
type largeRowRefresh struct {
	store *SQLiteStore
	spool *RecordSpool
	size  int64
}

// readLargeRowRefresh streams one canonical row in a read-only snapshot
// transaction (a chunked read must not mix two committed versions). found is
// false when the row is gone by then: the refresh drops the record, as it
// does for a row missing from its batch query.
func (s *SQLiteStore) readLargeRowRefresh(ctx context.Context, workspace string, key metadataRecordKey) (_ *largeRowRefresh, found bool, err error) {
	options := &sql.TxOptions{ReadOnly: true}
	if s.dialect != "sqlite" {
		options.Isolation = sql.LevelRepeatableRead
	}
	tx, err := s.db.BeginTx(ctx, options)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var size int64
	length := (&ResourceRead{store: s}).byteLength("data")
	err = tx.QueryRowContext(ctx, "SELECT "+length+" FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?",
		workspace, key.field, key.key).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if size < 0 || size > metadataLogicalDiskBytes {
		return nil, false, ErrMetadataWorkBudget
	}
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return nil, false, err
	}
	if !spool.CanAppend(size) {
		spool.Close()
		return nil, false, ErrRecordSpoolBudget
	}
	reader := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: RecordKey{key.field, key.key}, size: size}
	if copied, err := io.CopyBuffer(spool, reader, make([]byte, metadataRecordChunkBytes)); err != nil || copied != size {
		spool.Close()
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, false, err
	}
	return &largeRowRefresh{spool: spool, size: size}, true, nil
}

func largeRowContains(ctx context.Context, input io.Reader, needle []byte) (bool, error) {
	reader := bufio.NewReaderSize(input, metadataRecordChunkBytes)
	window := make([]byte, 0, metadataRecordChunkBytes+len(needle))
	buffer := make([]byte, metadataRecordChunkBytes)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, err := reader.Read(buffer)
		window = append(window, buffer[:n]...)
		if bytes.Contains(window, needle) {
			return true, nil
		}
		if keep := len(needle) - 1; len(window) > keep {
			window = append(window[:0], window[len(window)-keep:]...)
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}

// largeRowRetryStrings are kind's plain top-level string fields: framing them
// is the typed decoder's string assignment with its repeated-member rules.
func largeRowRetryStrings(kind reflect.Type) []largeRowBody {
	if kind.Kind() != reflect.Struct {
		return nil
	}
	var bodies []largeRowBody
	for i := 0; i < kind.NumField(); i++ {
		info := kind.Field(i)
		if info.Anonymous {
			return nil
		}
		if info.PkgPath != "" || info.Type != reflect.TypeFor[string]() {
			continue
		}
		tag := info.Tag.Get("json")
		if tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		skip := false
		for _, option := range parts[1:] {
			skip = skip || option == "string"
		}
		if skip {
			continue
		}
		name := parts[0]
		if name == "" {
			name = info.Name
		}
		bodies = append(bodies, largeRowBody{field: info.Name, key: name})
	}
	return bodies
}

// metadataLogicalRetryLargeRecord is metadataLogicalRetryRecord for a large
// canonical row: the typed header is decoded natively and every top-level
// string is framed from its token. Document marker expansion copies the
// document's content into its versions, so such a row is resolved in memory
// under an admission sized to it.
func metadataLogicalRetryLargeRecord(ctx context.Context, builder *metadataLogicalBuilder, field string, kind reflect.Type, row *largeRowRefresh, old metadataAggregateNode) (metadataAggregateNode, error) {
	owner := &ResourceRead{store: row.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 512<<10); err != nil {
		return nil, err
	}
	part := legacySessionSpoolPart(row.spool)
	if field == "documents" || field == "trash" {
		marked, err := largeRowContains(ctx, part.reader(), documentMarkerBytes)
		if err != nil {
			return nil, err
		}
		if marked {
			raw, err := largeRowPartBytes(ctx, owner, part, 1)
			if err != nil {
				return nil, err
			}
			// Each current-document reference expands to a copy of the
			// content; the resolver's maps, the expansion and the typed
			// decode are admitted together.
			expanded := uint64(bytes.Count(raw, documentMarkerBytes)+1) * uint64(len(raw))
			if expanded > metadataWorkBytes/4 {
				return nil, ErrMetadataWorkBudget
			}
			if err := owner.reserve(ctx, expanded*4+4096); err != nil {
				return nil, err
			}
			return metadataLogicalRetryRecordLimit(ctx, builder, field, kind, raw, old, int(expanded), true)
		}
	}
	bodies := largeRowRetryStrings(kind)
	header, framed, _, err := largeRowCanonicalHeader(ctx, owner, builder, row.spool, row.size, kind, bodies, largeRowPlaceholders[0])
	if err != nil {
		return nil, err
	}
	record, err := decodeMetadataRecord(kind, header, reflect.Value{})
	if err != nil {
		return nil, err
	}
	keep, err := metadataLogicalRetryKeep(ctx, field, kind, record, old)
	if err != nil {
		return nil, err
	}
	if err := builder.auditJSONLocations(record); err != nil {
		return nil, err
	}
	fresh, err := builder.writeValue(record, 0, false)
	if err != nil {
		return nil, err
	}
	if len(bodies) != 0 {
		replaced := make(map[string]metadataLogicalNode, len(bodies))
		for index, body := range bodies {
			replaced[body.field] = framed[index]
		}
		if fresh, err = builder.writeAggregateNode(largeRowNode{fresh, replaced}, 0); err != nil {
			return nil, err
		}
	}
	if len(keep) == 0 {
		return metadataAggregateStoredNode{fresh}, nil
	}
	return metadataLogicalRetainedRecord{fresh, keep}, nil
}
