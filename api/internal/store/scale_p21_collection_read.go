package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"flow/api/internal/domain"
)

var ErrMetadataIterationDone = errors.New("metadata iteration completed")
var ErrMetadataArrayMerge = errors.New("repeated metadata array fields require typed occurrence merging")
var errMetadataCollectionJSON = errors.New("invalid metadata collection JSON")

// MetadataArrayShape describes storage before normalizeStoredMetadata. Present
// distinguishes an omitted field from null; Nil distinguishes null from [].
type MetadataArrayShape struct {
	WorkspaceFound bool
	FieldPresent   bool
	Nil            bool
	// encoding/json reuses existing slice elements across repeated root
	// fields. Raw final-record visitation cannot silently discard that state.
	RequiresTypedMerge bool
}

type MetadataArrayRecord struct {
	Ordinal      int64
	EncodedBytes int64
	Key          RecordKey
	// Reader also implements io.ReaderAt and io.ReadSeeker, relative to
	// this record. All cursors share the callback's source lifetime.
	Reader io.Reader
}

// MetadataArrayOccurrence exposes every inline input, including overwritten
// inputs, in JSON order. Begin starts an array without clearing its backing
// elements; Null drops the slice; End sets its length to Ordinal (and an empty
// array also discards backing elements). Record events decode into the existing
// element at Ordinal. Append events are fresh canonical elements after inline
// decoding. The caller owns the typed merge state and its memory admission.
type MetadataArrayOccurrence struct {
	Begin, Null, End, Append bool
	MetadataArrayRecord
}

// MetadataMapOccurrence is an event in encoding/json map decoding order. Reset
// clears the caller's map to nil. BeginObject creates an empty map only if nil.
// Other events replace one whole value. Inline keys are quoted JSON in RawKey;
// canonical keys are already decoded in Key. Empty string is a valid map key.
type MetadataMapOccurrence struct {
	Reset, BeginObject, CanonicalKey bool
	Key                              string
	RawKey                           io.Reader
	EncodedKeyBytes, EncodedBytes    int64
	Reader                           io.Reader
}

type metadataCollectionRange struct{ offset, size int64 }

type metadataDetachedCollection struct {
	shape        MetadataArrayShape
	array        bool
	inline       metadataCollectionRange
	inlineNil    bool
	manifest     string
	manifestSeen bool
	index        *RecordSpool
	count        int64
	occurrences  int
}

// MetadataCollectionRead either owns detached bytes and a parser lease or
// borrows a caller-owned transaction and lease. BeginMetadataCollectionRead
// releases SQL before returning and keeps its lease out of callback contexts.
// MetadataCollectionsInSnapshot retains the caller's context and transaction.
// Typed decoding and retained output need separate size-aware admission.
//
// Readers are valid only in their callback. Callers must not retain readers,
// and may retain owned decoded data only under their own admission. This is a
// trusted storage primitive; callers still authorize, redact, normalize and
// revalidate live access before publication.
type MetadataCollectionRead struct {
	ctx         context.Context
	stamp       copyStamp
	root, data  *RecordSpool
	rootSize    int64
	collections map[string]*metadataDetachedCollection
	release     func()
	closed      bool
	snapshot    *metadataCollectionSnapshot
}

type MetadataArrayRead struct {
	Shape MetadataArrayShape
	read  *MetadataCollectionRead
	kind  string
}

func (s *SQLiteStore) BeginMetadataArrayRead(ctx context.Context, workspace, kind string) (*MetadataArrayRead, error) {
	read, err := s.BeginMetadataCollectionRead(ctx, workspace, kind)
	if err != nil {
		return nil, err
	}
	field := read.collections[kind]
	if !field.array {
		read.Close()
		return nil, ErrMetadataRecordQuery
	}
	return &MetadataArrayRead{Shape: field.shape, read: read, kind: kind}, nil
}

func (r *MetadataArrayRead) Visit(visit func(context.Context, MetadataArrayRecord) error) error {
	return r.read.VisitArray(r.kind, visit)
}

func (r *MetadataArrayRead) VisitOccurrences(visit func(context.Context, MetadataArrayOccurrence) error) error {
	return r.read.VisitArrayOccurrences(r.kind, visit)
}

func (r *MetadataArrayRead) Close() error { return r.read.Close() }

func (s *SQLiteStore) StreamMetadataArray(ctx context.Context, workspace, kind string, visit func(context.Context, MetadataArrayRecord) error) (MetadataArrayShape, error) {
	read, err := s.BeginMetadataArrayRead(ctx, workspace, kind)
	if err != nil {
		return MetadataArrayShape{}, err
	}
	defer read.Close()
	return read.Shape, read.Visit(visit)
}

// BeginMetadataCollectionRead detaches up to 50 selected arrays/maps in one SQL
// snapshot. Inline fields and split rows share that snapshot. Disk admission is
// strict: ErrRecordSpoolBudget is returned without a full-metadata fallback.
// Maps expose decoding occurrences, not deduplicated/sorted final entries.
// workspaceSettings also exposes object occurrences; its caller must apply
// struct decoding semantics, including ignoring null rather than clearing it.
func (s *SQLiteStore) BeginMetadataCollectionRead(ctx context.Context, workspace string, kinds ...string) (_ *MetadataCollectionRead, err error) {
	if workspace == "" || len(kinds) == 0 || len(kinds) > 50 {
		return nil, ErrMetadataRecordQuery
	}
	r := &MetadataCollectionRead{ctx: ctx, collections: map[string]*metadataDetachedCollection{}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	for _, kind := range kinds {
		field, ok := metadataFieldByName(kind)
		if !ok || !validMetadataKey(RecordKey{Kind: kind}) {
			return nil, ErrMetadataRecordQuery
		}
		t := reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type
		object := t.Kind() == reflect.Map && t.Key().Kind() == reflect.String ||
			kind == "workspaceSettings" && t.Kind() == reflect.Struct
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 || t.Kind() != reflect.Slice && !object {
			return nil, ErrMetadataRecordQuery
		}
		r.collections[kind] = &metadataDetachedCollection{array: t.Kind() == reflect.Slice, inlineNil: true, shape: MetadataArrayShape{Nil: true}}
	}
	if s.dialect == "postgres" && strings.ContainsRune(workspace, '\x00') {
		return r, nil
	}
	workCtx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return nil, err
	}
	r.release = release
	if r.root, err = NewRecordSpool(); err != nil {
		return nil, err
	}
	if r.data, err = NewRecordSpool(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(workCtx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rootLength := "LENGTH(data)"
	if s.dialect == "sqlite" {
		rootLength = "LENGTH(CAST(data AS BLOB))"
	}
	err = tx.QueryRowContext(workCtx, `SELECT `+rootLength+` FROM workspace_states WHERE `+s.metadataRecordKeyEquals("workspace_key", "?"), workspace).Scan(&r.rootSize)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	r.stamp, err = readMetadataStamp(workCtx, tx, workspace)
	if err != nil {
		return nil, err
	}
	for _, field := range r.collections {
		field.shape.WorkspaceFound = true
	}
	if !r.root.CanAppend(r.rootSize) {
		return nil, ErrRecordSpoolBudget
	}
	if _, err = io.CopyBuffer(r.root, &metadataRootChunkReader{ctx: workCtx, tx: tx, workspace: workspace, size: r.rootSize}, make([]byte, metadataRecordChunkBytes)); err != nil {
		return nil, err
	}
	if err = r.scanRoot(nil); err != nil {
		return nil, err
	}
	for kind, field := range r.collections {
		if !field.manifestSeen {
			continue
		}
		if field.index, err = NewRecordSpool(); err != nil {
			return nil, err
		}
		keyLength := "LENGTH(record_key)"
		if s.dialect == "sqlite" {
			keyLength = "LENGTH(CAST(record_key AS BLOB))"
		} else if s.dialect == "postgres" {
			keyLength = "OCTET_LENGTH(record_key)"
		}
		var invalidKey int
		err = tx.QueryRowContext(workCtx, `SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+keyLength+`>191 LIMIT 1`, workspace, kind).Scan(&invalidKey)
		if err == nil {
			return nil, ErrMetadataRecordQuery
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		err = scanMetadataHeaders(workCtx, tx, workspace, kind, func(key RecordKey, size int64) error {
			if len(key.ID) > 191 || size < 0 {
				return ErrMetadataRecordQuery
			}
			if field.manifest != "array" && field.manifest != "map" ||
				field.array != (field.manifest == "array") {
				return fmt.Errorf("%w: collection %s has shape %q", ErrMetadataRecordQuery, kind, field.manifest)
			}
			offset, err := r.data.Seek(0, io.SeekEnd)
			if err != nil {
				return err
			}
			if !r.data.CanAppend(size) {
				return ErrRecordSpoolBudget
			}
			n, err := io.CopyBuffer(r.data, &canonicalMetadataReader{ctx: workCtx, tx: tx, workspace: workspace, key: key, size: size}, make([]byte, metadataRecordChunkBytes))
			if err != nil {
				return err
			}
			if n != size {
				return io.ErrUnexpectedEOF
			}
			if err := validateMetadataCollectionJSON(workCtx, io.NewSectionReader(r.data, offset, size)); err != nil {
				return err
			}
			var header [26]byte
			binary.BigEndian.PutUint64(header[:8], uint64(offset))
			binary.BigEndian.PutUint64(header[8:16], uint64(size))
			binary.BigEndian.PutUint64(header[16:24], uint64(field.count))
			binary.BigEndian.PutUint16(header[24:], uint16(len(key.ID)))
			if _, err = field.index.Write(header[:]); err != nil {
				return err
			}
			if _, err = io.WriteString(field.index, key.ID); err != nil {
				return err
			}
			field.count++
			return nil
		})
		if err != nil {
			return nil, err
		}
		if field.count > 0 {
			field.shape.FieldPresent, field.shape.Nil = true, false
		}
	}
	// Read-only rollback ends the snapshot before any user callback can block
	// on a response writer or use the only database connection.
	if err = tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return nil, err
	}
	return r, nil
}

func (r *MetadataCollectionRead) Shape(kind string) (MetadataArrayShape, error) {
	field, ok := r.collections[kind]
	if !ok || r.closed {
		return MetadataArrayShape{}, ErrMetadataRecordQuery
	}
	return field.shape, nil
}

// VisitRootFieldOccurrences exposes a persisted core field from this same
// snapshot, including repeated aliases and null. Decode each occurrence into
// the same admitted typed destination to preserve struct merge semantics.
// It does not assemble split rows; use the collection visitors for collections.
func (r *MetadataCollectionRead) VisitRootFieldOccurrences(kind string, visit func(context.Context, int64, io.Reader) error) (bool, error) {
	if _, ok := metadataFieldByName(kind); !ok || visit == nil || r.closed {
		return false, ErrMetadataRecordQuery
	}
	if err := r.ctx.Err(); err != nil {
		return false, err
	}
	if r.rootSize == 0 {
		return false, nil
	}
	scanner := r.scanner(metadataCollectionRange{size: r.rootSize})
	if err := scanner.spaces(); err != nil {
		return false, err
	}
	if b, _ := scanner.peek(); b == 'n' {
		return false, nil
	}
	found := false
	err := scanner.object(func(key, value metadataCollectionRange) error {
		name, err := r.shortString(key)
		if err != nil {
			return err
		}
		if !strings.EqualFold(name, kind) {
			return nil
		}
		found = true
		return visit(r.ctx, value.size, io.NewSectionReader(r.rootSource(), value.offset, value.size))
	}, 0)
	if errors.Is(err, ErrMetadataIterationDone) {
		err = nil
	}
	return found, err
}

func (r *MetadataCollectionRead) VisitArray(kind string, visit func(context.Context, MetadataArrayRecord) error) error {
	field, ok := r.collections[kind]
	if !ok || !field.array || visit == nil || r.closed {
		return ErrMetadataRecordQuery
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if field.shape.RequiresTypedMerge {
		return ErrMetadataArrayMerge
	}
	err := func() error {
		ordinal := int64(0)
		if !field.inlineNil {
			scanner := r.scanner(field.inline)
			if err := scanner.array(func(part metadataCollectionRange) error {
				err := visit(r.ctx, MetadataArrayRecord{Ordinal: ordinal, EncodedBytes: part.size, Key: RecordKey{Kind: kind}, Reader: io.NewSectionReader(r.rootSource(), part.offset, part.size)})
				ordinal++
				return err
			}, 0); err != nil {
				return err
			}
		}
		if field.count > 0 {
			return r.visitRows(kind, func(key string, part metadataCollectionRange, _ int64) error {
				err := visit(r.ctx, MetadataArrayRecord{Ordinal: ordinal, EncodedBytes: part.size, Key: RecordKey{Kind: kind, ID: key}, Reader: r.rowSource(kind, key, part)})
				ordinal++
				return err
			})
		}
		return nil
	}()
	if errors.Is(err, ErrMetadataIterationDone) {
		return nil
	}
	return err
}

// VisitArrayOccurrences is required when Shape.RequiresTypedMerge is true.
// It also exposes discarded records so a typed caller can preserve the first
// encoding/json type error rather than accepting only the final occurrence.
// This method bounds source memory, not the caller's retained merge state.
func (r *MetadataCollectionRead) VisitArrayOccurrences(kind string, visit func(context.Context, MetadataArrayOccurrence) error) error {
	field, ok := r.collections[kind]
	if !ok || !field.array || visit == nil || r.closed {
		return ErrMetadataRecordQuery
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if !field.shape.WorkspaceFound {
		return nil
	}
	var finalCount int64
	err := r.scanRoot(func(name string, part metadataCollectionRange, null bool) error {
		if name != kind {
			return nil
		}
		finalCount = 0
		if null {
			return visit(r.ctx, MetadataArrayOccurrence{Null: true})
		}
		if err := visit(r.ctx, MetadataArrayOccurrence{Begin: true}); err != nil {
			return err
		}
		scanner := r.scanner(part)
		if err := scanner.array(func(part metadataCollectionRange) error {
			err := visit(r.ctx, MetadataArrayOccurrence{MetadataArrayRecord: MetadataArrayRecord{
				Ordinal: finalCount, EncodedBytes: part.size, Key: RecordKey{Kind: kind},
				Reader: io.NewSectionReader(r.rootSource(), part.offset, part.size),
			}})
			finalCount++
			return err
		}, 0); err != nil {
			return err
		}
		return visit(r.ctx, MetadataArrayOccurrence{End: true, MetadataArrayRecord: MetadataArrayRecord{Ordinal: finalCount}})
	})
	if err == nil {
		err = r.visitRows(kind, func(key string, part metadataCollectionRange, _ int64) error {
			err := visit(r.ctx, MetadataArrayOccurrence{Append: true, MetadataArrayRecord: MetadataArrayRecord{
				Ordinal: finalCount, EncodedBytes: part.size, Key: RecordKey{Kind: kind, ID: key},
				Reader: r.rowSource(kind, key, part),
			}})
			finalCount++
			return err
		})
	}
	if errors.Is(err, ErrMetadataIterationDone) {
		return nil
	}
	return err
}

func (r *MetadataCollectionRead) VisitMap(kind string, visit func(context.Context, MetadataMapOccurrence) error) error {
	field, ok := r.collections[kind]
	if !ok || field.array || visit == nil || r.closed {
		return ErrMetadataRecordQuery
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if !field.shape.WorkspaceFound {
		return nil
	}
	err := r.scanRoot(func(name string, part metadataCollectionRange, null bool) error {
		if name != kind {
			return nil
		}
		if null {
			return visit(r.ctx, MetadataMapOccurrence{Reset: true})
		}
		if err := visit(r.ctx, MetadataMapOccurrence{BeginObject: true}); err != nil {
			return err
		}
		scanner := r.scanner(part)
		return scanner.object(func(key, value metadataCollectionRange) error {
			return visit(r.ctx, MetadataMapOccurrence{RawKey: io.NewSectionReader(r.rootSource(), key.offset, key.size), EncodedKeyBytes: key.size, EncodedBytes: value.size, Reader: io.NewSectionReader(r.rootSource(), value.offset, value.size)})
		}, 0)
	})
	if err == nil && field.count > 0 {
		if err = visit(r.ctx, MetadataMapOccurrence{BeginObject: true}); err == nil {
			err = r.visitRows(kind, func(key string, part metadataCollectionRange, _ int64) error {
				return visit(r.ctx, MetadataMapOccurrence{CanonicalKey: true, Key: key, EncodedBytes: part.size, Reader: r.rowSource(kind, key, part)})
			})
		}
	}
	if errors.Is(err, ErrMetadataIterationDone) {
		return nil
	}
	return err
}

func (r *MetadataCollectionRead) visitRows(kind string, visit func(string, metadataCollectionRange, int64) error) error {
	field := r.collections[kind]
	if field.count == 0 {
		return nil
	}
	if r.snapshot != nil {
		ordinal := int64(0)
		return r.snapshot.scanHeaders(r.ctx, kind, func(key RecordKey, size int64) error {
			err := visit(key.ID, metadataCollectionRange{size: size}, ordinal)
			ordinal++
			return err
		})
	}
	info, err := field.index.Stat()
	if err != nil {
		return err
	}
	index := io.NewSectionReader(field.index, 0, info.Size())
	for range field.count {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		var header [26]byte
		if _, err := io.ReadFull(index, header[:]); err != nil {
			return err
		}
		key := make([]byte, int(binary.BigEndian.Uint16(header[24:])))
		if _, err := io.ReadFull(index, key); err != nil {
			return err
		}
		part := metadataCollectionRange{int64(binary.BigEndian.Uint64(header[:8])), int64(binary.BigEndian.Uint64(header[8:16]))}
		if err := visit(string(key), part, int64(binary.BigEndian.Uint64(header[16:24]))); err != nil {
			return err
		}
	}
	return nil
}

func (r *MetadataCollectionRead) Close() error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	var result error
	for _, field := range r.collections {
		result = errors.Join(result, field.index.Close())
	}
	result = errors.Join(result, r.root.Close(), r.data.Close())
	if r.release != nil {
		r.release()
	}
	return result
}

func (r *MetadataCollectionRead) scanner(part metadataCollectionRange) *metadataCollectionScanner {
	return &metadataCollectionScanner{ctx: r.ctx, input: bufio.NewReaderSize(io.NewSectionReader(r.rootSource(), part.offset, part.size), metadataRecordChunkBytes), offset: part.offset}
}

// scanRoot validates the entire JSON structure with constant token storage.
// Only selected field ranges and manifest values are retained. It does not
// decode/validate domain record fields: that is the consumer's responsibility.
func (r *MetadataCollectionRead) scanRoot(visit func(string, metadataCollectionRange, bool) error) error {
	scanner := r.scanner(metadataCollectionRange{size: r.rootSize})
	if err := scanner.spaces(); err != nil {
		return err
	}
	if b, _ := scanner.peek(); b == 'n' {
		if err := scanner.literal("null"); err != nil {
			return err
		}
		return scanner.end()
	}
	err := scanner.object(func(key, value metadataCollectionRange) error {
		name, err := r.shortString(key)
		if err != nil {
			return err
		}
		if strings.EqualFold(name, metadataCollectionsKey) {
			if visit == nil {
				return r.readManifest(value)
			}
			return nil
		}
		for kind, field := range r.collections {
			if !strings.EqualFold(name, kind) {
				continue
			}
			input := r.scanner(value)
			b, err := input.peek()
			if err != nil {
				return err
			}
			null := b == 'n'
			if !null && (field.array && b != '[' || !field.array && b != '{') {
				return fmt.Errorf("%w: invalid inline %s", ErrMetadataRecordQuery, kind)
			}
			if visit != nil {
				return visit(kind, value, null)
			}
			field.shape.FieldPresent, field.shape.Nil = true, null
			field.inline = value
			field.inlineNil = null
			if field.array {
				field.occurrences++
				field.shape.RequiresTypedMerge = field.occurrences > 1
			}
			break
		}
		return nil
	}, 0)
	if err != nil {
		return err
	}
	return scanner.end()
}

func (r *MetadataCollectionRead) shortString(part metadataCollectionRange) (string, error) {
	// All recognized Bootstrap tags are far shorter even with \u escapes.
	if part.size > 4096 {
		return "", nil
	}
	raw := make([]byte, int(part.size))
	if _, err := r.rootSource().ReadAt(raw, part.offset); err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func (r *MetadataCollectionRead) readManifest(part metadataCollectionRange) error {
	scanner := r.scanner(part)
	b, err := scanner.peek()
	if err != nil {
		return err
	}
	if b == 'n' {
		for _, field := range r.collections {
			field.manifestSeen, field.manifest = false, ""
		}
		return nil
	}
	return scanner.object(func(key, value metadataCollectionRange) error {
		name, err := r.shortString(key)
		if err != nil {
			return err
		}
		input := r.scanner(value)
		b, err := input.peek()
		if err != nil {
			return err
		}
		var shape string
		if b != 'n' {
			if b != '"' {
				return errMetadataCollectionJSON
			}
			shape, err = r.shortString(value)
			if err != nil {
				return err
			}
		}
		if field := r.collections[name]; field != nil {
			field.manifestSeen, field.manifest = true, shape
		}
		return nil
	}, 0)
}

type metadataRootChunkReader struct {
	ctx          context.Context
	tx           *sqlTx
	workspace    string
	size, offset int64
	buffer       []byte
}

func (r *metadataRootChunkReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.buffer) == 0 {
		if r.offset >= r.size {
			return 0, io.EOF
		}
		data := "data"
		if r.tx.dialect == "sqlite" {
			data = "CAST(data AS BLOB)"
		}
		if err := r.tx.QueryRowContext(r.ctx, `SELECT SUBSTR(`+data+`,?,?) FROM workspace_states WHERE workspace_key=?`, r.offset+1, min(int64(metadataRecordChunkBytes), r.size-r.offset), r.workspace).Scan(&r.buffer); err != nil {
			return 0, err
		}
		if len(r.buffer) == 0 {
			return 0, io.ErrUnexpectedEOF
		}
		r.offset += int64(len(r.buffer))
	}
	n := copy(p, r.buffer)
	r.buffer = r.buffer[n:]
	return n, nil
}

// This lexer validates JSON without retaining arbitrary strings/numbers. Its
// depth bound matches encoding/json's maximum nesting. Range callbacks refer
// to the immutable spool, never to the parser's fixed input buffer.
type metadataCollectionScanner struct {
	ctx    context.Context
	input  *bufio.Reader
	offset int64
}

func validateMetadataCollectionJSON(ctx context.Context, input io.Reader) error {
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(input, metadataRecordChunkBytes)}
	if _, err := scanner.value(0); err != nil {
		return err
	}
	return scanner.end()
}

func (s *metadataCollectionScanner) byte() (byte, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	b, err := s.input.ReadByte()
	if err == nil {
		s.offset++
	} else if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return b, err
}

func (s *metadataCollectionScanner) peek() (byte, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	raw, err := s.input.Peek(1)
	if err != nil {
		return 0, err
	}
	return raw[0], nil
}

func (s *metadataCollectionScanner) spaces() error {
	for {
		b, err := s.peek()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return nil
		}
		if _, err := s.byte(); err != nil {
			return err
		}
	}
}

func (s *metadataCollectionScanner) expect(want byte) error {
	b, err := s.byte()
	if err != nil {
		return err
	}
	if b != want {
		return errMetadataCollectionJSON
	}
	return nil
}

func (s *metadataCollectionScanner) end() error {
	if err := s.spaces(); err != nil {
		return err
	}
	if _, err := s.peek(); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errMetadataCollectionJSON
	}
	return nil
}

func (s *metadataCollectionScanner) literal(value string) error {
	for i := range value {
		if err := s.expect(value[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *metadataCollectionScanner) string() error {
	if err := s.expect('"'); err != nil {
		return err
	}
	for {
		b, err := s.byte()
		if err != nil {
			return err
		}
		if b == '"' {
			return nil
		}
		if b < 0x20 {
			return errMetadataCollectionJSON
		}
		if b != '\\' {
			continue
		}
		b, err = s.byte()
		if err != nil {
			return err
		}
		switch b {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			for range 4 {
				b, err = s.byte()
				if err != nil {
					return err
				}
				if !(b >= '0' && b <= '9' || b >= 'A' && b <= 'F' || b >= 'a' && b <= 'f') {
					return errMetadataCollectionJSON
				}
			}
		default:
			return errMetadataCollectionJSON
		}
	}
}

func (s *metadataCollectionScanner) number() error {
	b, err := s.peek()
	if err != nil {
		return err
	}
	if b == '-' {
		s.byte()
		b, err = s.peek()
		if err != nil {
			return err
		}
	}
	if b == '0' {
		s.byte()
	} else {
		if b < '1' || b > '9' {
			return errMetadataCollectionJSON
		}
		if err := s.digits(); err != nil {
			return err
		}
	}
	b, err = s.peek()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if b == '.' {
		s.byte()
		if err := s.digits(); err != nil {
			return err
		}
		b, err = s.peek()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	if b == 'e' || b == 'E' {
		s.byte()
		b, err = s.peek()
		if err != nil {
			return err
		}
		if b == '+' || b == '-' {
			s.byte()
		}
		if err := s.digits(); err != nil {
			return err
		}
	}
	return nil
}

func (s *metadataCollectionScanner) digits() error {
	count := 0
	for {
		b, err := s.peek()
		if errors.Is(err, io.EOF) && count > 0 {
			return nil
		}
		if err != nil {
			return err
		}
		if b < '0' || b > '9' {
			if count == 0 {
				return errMetadataCollectionJSON
			}
			return nil
		}
		if _, err := s.byte(); err != nil {
			return err
		}
		count++
	}
}

func (s *metadataCollectionScanner) value(depth int) (metadataCollectionRange, error) {
	if depth > 10000 {
		return metadataCollectionRange{}, errMetadataCollectionJSON
	}
	if err := s.spaces(); err != nil {
		return metadataCollectionRange{}, err
	}
	start := s.offset
	b, err := s.peek()
	if err != nil {
		return metadataCollectionRange{}, err
	}
	if depth >= 10000 && (b == '{' || b == '[') {
		return metadataCollectionRange{}, errMetadataCollectionJSON
	}
	switch b {
	case '{', '[':
		err = s.container(depth)
	case '"':
		err = s.string()
	case 'n':
		err = s.literal("null")
	case 't':
		err = s.literal("true")
	case 'f':
		err = s.literal("false")
	default:
		err = s.number()
	}
	return metadataCollectionRange{start, s.offset - start}, err
}

// Nested ignored values use a fixed grammar stack instead of growing the Go
// call stack with attacker-controlled JSON depth.
func (s *metadataCollectionScanner) container(base int) error {
	const (
		objectKeyOrEnd byte = iota
		objectKey
		objectColon
		objectValue
		objectComma
		arrayValueOrEnd
		arrayValue
		arrayComma
	)
	var stack [10000]byte
	depth := 0
	consume := func() error {
		if err := s.spaces(); err != nil {
			return err
		}
		b, err := s.peek()
		if err != nil {
			return err
		}
		switch b {
		case '{', '[':
			if base+depth >= len(stack) {
				return errMetadataCollectionJSON
			}
			stack[depth] = objectKeyOrEnd
			if b == '[' {
				stack[depth] = arrayValueOrEnd
			}
			depth++
			return s.expect(b)
		case '"':
			return s.string()
		case 'n':
			return s.literal("null")
		case 't':
			return s.literal("true")
		case 'f':
			return s.literal("false")
		default:
			return s.number()
		}
	}
	if err := consume(); err != nil {
		return err
	}
	for depth > 0 {
		if err := s.spaces(); err != nil {
			return err
		}
		index := depth - 1
		switch stack[index] {
		case objectKeyOrEnd, objectKey:
			b, err := s.peek()
			if err != nil {
				return err
			}
			if b == '}' && stack[index] == objectKeyOrEnd {
				if err := s.expect('}'); err != nil {
					return err
				}
				depth--
				continue
			}
			if err := s.string(); err != nil {
				return err
			}
			stack[index] = objectColon
		case objectColon:
			if err := s.expect(':'); err != nil {
				return err
			}
			stack[index] = objectValue
		case objectValue:
			stack[index] = objectComma
			if err := consume(); err != nil {
				return err
			}
		case objectComma:
			b, err := s.byte()
			if err != nil {
				return err
			}
			if b == '}' {
				depth--
			} else if b == ',' {
				stack[index] = objectKey
			} else {
				return errMetadataCollectionJSON
			}
		case arrayValueOrEnd, arrayValue:
			b, err := s.peek()
			if err != nil {
				return err
			}
			if b == ']' && stack[index] == arrayValueOrEnd {
				if err := s.expect(']'); err != nil {
					return err
				}
				depth--
				continue
			}
			stack[index] = arrayComma
			if err := consume(); err != nil {
				return err
			}
		case arrayComma:
			b, err := s.byte()
			if err != nil {
				return err
			}
			if b == ']' {
				depth--
			} else if b == ',' {
				stack[index] = arrayValue
			} else {
				return errMetadataCollectionJSON
			}
		}
	}
	return nil
}

func (s *metadataCollectionScanner) array(visit func(metadataCollectionRange) error, depth int) error {
	if err := s.expect('['); err != nil {
		return err
	}
	if err := s.spaces(); err != nil {
		return err
	}
	if b, _ := s.peek(); b == ']' {
		return s.expect(']')
	}
	for {
		part, err := s.value(depth + 1)
		if err != nil {
			return err
		}
		if visit != nil {
			if err := visit(part); err != nil {
				return err
			}
		}
		if err := s.spaces(); err != nil {
			return err
		}
		b, err := s.byte()
		if err != nil {
			return err
		}
		if b == ']' {
			return nil
		}
		if b != ',' {
			return errMetadataCollectionJSON
		}
	}
}

func (s *metadataCollectionScanner) object(visit func(metadataCollectionRange, metadataCollectionRange) error, depth int) error {
	if err := s.expect('{'); err != nil {
		return err
	}
	if err := s.spaces(); err != nil {
		return err
	}
	if b, _ := s.peek(); b == '}' {
		return s.expect('}')
	}
	for {
		if err := s.spaces(); err != nil {
			return err
		}
		start := s.offset
		if err := s.string(); err != nil {
			return err
		}
		key := metadataCollectionRange{start, s.offset - start}
		if err := s.spaces(); err != nil {
			return err
		}
		if err := s.expect(':'); err != nil {
			return err
		}
		value, err := s.value(depth + 1)
		if err != nil {
			return err
		}
		if visit != nil {
			if err := visit(key, value); err != nil {
				return err
			}
		}
		if err := s.spaces(); err != nil {
			return err
		}
		b, err := s.byte()
		if err != nil {
			return err
		}
		if b == '}' {
			return nil
		}
		if b != ',' {
			return errMetadataCollectionJSON
		}
	}
}
