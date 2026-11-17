package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// One refresh owns these frames until Transform imports the selected nodes.
// The original byte patches and their small-record callers remain unchanged.
type metadataLogicalLargeRetry struct {
	store  *SQLiteStore
	frames *metadataLogicalBuilder
	rows   map[metadataRecordKey]metadataLogicalNode
}

func newMetadataLogicalLargeRetry(ctx context.Context, s *SQLiteStore, source *metadataLogicalSource) (*metadataLogicalLargeRetry, error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil {
		return nil, ErrMetadataRecordQuery
	}
	frames, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	return &metadataLogicalLargeRetry{store: s, frames: frames, rows: map[metadataRecordKey]metadataLogicalNode{}}, nil
}

func (r *metadataLogicalLargeRetry) Close() {
	if r != nil && r.frames != nil {
		r.frames.Close()
		r.frames = nil
		r.rows = nil
	}
}

func metadataLogicalLargeRetryField(field string) bool {
	return field == "documents" || field == "projects"
}

func (r *metadataLogicalLargeRetry) check(ctx context.Context) error {
	if r == nil || r.frames == nil || r.frames.closed || r.store == nil {
		return errMetadataLogicalClosed
	}
	return ctx.Err()
}

func (r *metadataLogicalLargeRetry) captureRaw(ctx context.Context, key metadataRecordKey, raw []byte) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	node, err := r.capturePart(ctx, key.field, legacySessionJSONPart{bytes.NewReader(raw), 0, int64(len(raw))})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.rows[key] = node
	return nil
}

// Once the initial capped SELECT discovers a large supported row, recapture
// the whole requested field in one repeatable snapshot. Mixing small bytes
// from the discovery SELECT with later body chunks would mix generations.
func (r *metadataLogicalLargeRetry) captureField(ctx context.Context, workspace, field string, ids []string) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if !metadataLogicalLargeRetryField(field) || len(ids) > MetadataRecordBatchMaxKeys {
		return ErrMetadataRecordQuery
	}
	owner := &ResourceRead{store: r.store}
	defer owner.Close()
	// Bound every SQL Scan before it can copy a physical key or small body.
	// The query refuses to return either larger than the existing page size.
	if err := owner.reserve(ctx, MetadataRecordPageMaxBytes*12+64<<10); err != nil {
		return err
	}
	tx, err := r.store.db.BeginTx(ctx, metadataReadOptions(r.store.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	clause, args := bindList("record_key", ids)
	keyLength, dataLength := owner.byteLength("record_key"), owner.byteLength("data")
	blob := "0"
	if r.store.dialect == "sqlite" {
		blob = "CASE WHEN typeof(record_key)='blob' THEN 1 ELSE 0 END"
	}
	query := "SELECT " + keyLength + ",CASE WHEN " + keyLength + "<=? THEN record_key ELSE NULL END," +
		dataLength + ",CASE WHEN " + dataLength + "<=? THEN data ELSE NULL END," + blob + " FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND " + clause
	rows, err := tx.QueryContext(ctx, query,
		append([]any{MetadataRecordPageMaxBytes, MetadataRecordPageMaxBytes, workspace, field}, args...)...)
	if err != nil {
		return err
	}
	// Capture headers first, then close rows before using the same transaction
	// for body chunks (the database may allow only one active result set).
	type header struct {
		key  string
		size int64
		raw  []byte
		blob bool
	}
	headers := make([]header, 0, len(ids))
	for rows.Next() {
		var keySize, size int64
		var physical sql.NullString
		var raw []byte
		var blob bool
		if err := rows.Scan(&keySize, &physical, &size, &raw, &blob); err != nil {
			rows.Close()
			return err
		}
		if keySize < 0 || size < 0 {
			rows.Close()
			return ErrMetadataRecordQuery
		}
		if !physical.Valid || !slices.Contains(ids, physical.String) {
			continue
		}
		if size > metadataLogicalDiskBytes {
			rows.Close()
			return ErrMetadataWorkBudget
		}
		// Fund retained SQL byte copies separately from the reusable Scan
		// envelope. Each field has at most the original bounded key count.
		if uint64(len(raw)) > (metadataWorkBytes-512)/2 {
			rows.Close()
			return ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, uint64(len(raw))*2+uint64(len(physical.String))*2+512); err != nil {
			rows.Close()
			return err
		}
		headers = append(headers, header{physical.String, size, raw, blob})
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		r.rows[metadataRecordKey{field, id}] = metadataLogicalNode{}
	}
	for index := range headers {
		header := &headers[index]
		key := metadataRecordKey{field, header.key}
		if header.raw != nil {
			if int64(len(header.raw)) != header.size {
				return io.ErrUnexpectedEOF
			}
			if err := r.captureRaw(ctx, key, header.raw); err != nil {
				return err
			}
			header.raw = nil
			continue
		}
		spool, err := newMetadataLogicalRecordSpool(metadataLogicalSourceScratchContext(ctx, r.frames.source))
		if err != nil {
			return err
		}
		err = func() error {
			defer spool.Close()
			if !spool.CanAppend(header.size) {
				return ErrRecordSpoolBudget
			}
			reader := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: RecordKey{field, header.key}, size: header.size}
			var input io.Reader = reader
			if header.blob {
				input = &metadataRetryBlobReader{base: reader}
			}
			count, err := io.CopyBuffer(spool, input, make([]byte, metadataRecordChunkBytes))
			if err != nil {
				return err
			}
			if count != header.size {
				return io.ErrUnexpectedEOF
			}
			node, err := r.capturePart(ctx, field, legacySessionSpoolPart(spool))
			if err != nil {
				return err
			}
			r.rows[key] = node
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// The existing canonical reader binds string physical keys. SQLite can also
// return a BLOB locator; retain that binding type rather than rereading a TEXT
// alias. Chunk size, SQL byte projection and ownership are otherwise literal.
type metadataRetryBlobReader struct{ base *canonicalMetadataReader }

func (r *metadataRetryBlobReader) Read(output []byte) (int, error) {
	base := r.base
	if err := base.ctx.Err(); err != nil {
		return 0, err
	}
	if len(base.buffer) == 0 {
		if base.offset >= base.size {
			return 0, io.EOF
		}
		count := min(int64(metadataRecordChunkBytes), base.size-base.offset)
		if err := base.tx.QueryRowContext(base.ctx,
			`SELECT SUBSTR(CAST(data AS BLOB),?,?) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`,
			base.offset+1, count, base.workspace, base.key.Kind, []byte(base.key.ID)).Scan(&base.buffer); err != nil {
			return 0, err
		}
		if len(base.buffer) == 0 {
			return 0, io.ErrUnexpectedEOF
		}
		base.offset += int64(len(base.buffer))
	}
	count := copy(output, base.buffer)
	base.buffer = base.buffer[count:]
	return count, nil
}

func (r *metadataLogicalLargeRetry) capturePart(ctx context.Context, field string, part legacySessionJSONPart) (metadataLogicalNode, error) {
	if err := r.check(ctx); err != nil {
		return metadataLogicalNode{}, err
	}
	ctx = metadataLogicalSourceScratchContext(ctx, r.frames.source)
	switch field {
	case "projects":
		return metadataRetryProjectCanonicalRecord(ctx, r.store, r.frames, part)
	case "documents":
		resolved, close, err := metadataRetryResolveDocument(ctx, r.store, part)
		if err != nil {
			return metadataLogicalNode{}, err
		}
		defer close()
		return metadataRetryDocumentCanonicalRecord(ctx, r.store, r.frames, resolved)
	default:
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
}

func metadataRetryProjectCanonicalRecord(ctx context.Context, s *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart) (metadataLogicalNode, error) {
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return metadataLogicalNode{}, err
	}
	header, body, err := legacyProjectCanonicalHeader(ctx, owner, frames, io.NewSectionReader(part.input, part.start, part.size), part.size)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	var project domain.Project
	if err := json.Unmarshal(header, &project); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := frames.auditJSONLocations(reflect.ValueOf(project)); err != nil {
		return metadataLogicalNode{}, err
	}
	node, err := frames.writeValue(reflect.ValueOf(project), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	result, err := frames.writeAggregateNode(legacyProjectProgressNode{header: node, description: body}, 0)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := ctx.Err(); err != nil {
		return metadataLogicalNode{}, err
	}
	return result, nil
}

// This is only the original raw substring trigger, not another JSON parser.
func metadataRetryHasDocumentMarker(ctx context.Context, part legacySessionJSONPart) (bool, error) {
	reader := bufio.NewReaderSize(part.reader(), metadataRecordChunkBytes)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		prefix, err := reader.Peek(len(documentMarkerBytes))
		if bytes.Equal(prefix, documentMarkerBytes) {
			return true, nil
		}
		if _, readErr := reader.ReadByte(); readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return false, nil
			}
			return false, readErr
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
	}
}

// Preserve the literal marker resolver. Its raw maps and expanded output
// must be admitted before allocation; the 2 MiB typed-frame shortcut is not
// a limit on supported current-document revision expansion.
func metadataRetryResolveDocument(ctx context.Context, s *SQLiteStore, part legacySessionJSONPart) (_ legacySessionJSONPart, close func(), err error) {
	owner := &ResourceRead{store: s}
	close = owner.Close
	defer func() {
		if err != nil {
			owner.Close()
		}
	}()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	found, err := metadataRetryHasDocumentMarker(ctx, part)
	if err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	if !found {
		return part, close, nil
	}
	if part.size < 0 || uint64(part.size) > (metadataWorkBytes-(64<<10))/2 {
		return legacySessionJSONPart{}, nil, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(part.size)*2+64<<10); err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	raw := make([]byte, int(part.size))
	if _, err := io.ReadFull(part.reader(), raw); err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	work, err := metadataAggregatePayloadJSONWork(ctx, raw)
	if err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	preflight := &ResourceRead{store: s}
	defer preflight.Close()
	if err := preflight.reserve(ctx, work+64<<10); err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	var revisions []map[string]json.RawMessage
	// The literal resolver ignores a malformed revision list, then the
	// ordinary typed Document decoder supplies its original type error.
	_ = json.Unmarshal(fields["revisions"], &revisions)
	bodyBytes := uint64(0)
	for _, name := range revisionBodyFields {
		bodyBytes += uint64(len(fields[name]) + len(name) + 4)
	}
	expanded := uint64(len(raw))
	for _, revision := range revisions {
		var marker map[string]json.RawMessage
		if json.Unmarshal(revision[offloadMarker], &marker) != nil || jsonString(marker["body"]) != "document" {
			continue
		}
		if bodyBytes > metadataWorkBytes || expanded > metadataWorkBytes-bodyBytes {
			return legacySessionJSONPart{}, nil, ErrMetadataWorkBudget
		}
		expanded += bodyBytes
	}
	if expanded > (metadataWorkBytes-(64<<10))/16 {
		return legacySessionJSONPart{}, nil, ErrMetadataWorkBudget
	}
	fields, revisions = nil, nil
	preflight.Close()
	// Reuse the existing raw/generic publication envelope factor for the
	// resolver's map copies, repeated body RawMessages and encoder output.
	if err := owner.reserve(ctx, expanded*16+64<<10); err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	resolved, err := resolveDocumentMarkers("documents", raw)
	if err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return legacySessionJSONPart{}, nil, err
	}
	return legacySessionJSONPart{bytes.NewReader(resolved), 0, int64(len(resolved))}, close, nil
}

func metadataLogicalRetryFramedRecord(ctx context.Context, fresh metadataLogicalNode, kind reflect.Type, old metadataAggregateNode) (metadataAggregateNode, error) {
	if fresh.typ != kind {
		return nil, ErrMetadataRecordQuery
	}
	keep := map[string]metadataAggregateNode{}
	if old != nil && kind.Kind() == reflect.Struct {
		if err := old.nodeChildren(ctx, func(key reflect.Value, value metadataAggregateNode) error {
			field, exists := kind.FieldByName(key.String())
			if exists && field.PkgPath == "" && field.Tag.Get("json") == "-" {
				keep[field.Name] = value
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if len(keep) == 0 {
		return metadataAggregateStoredNode{value: fresh}, nil
	}
	return metadataLogicalRetainedRecord{fresh: fresh, keep: keep}, nil
}
