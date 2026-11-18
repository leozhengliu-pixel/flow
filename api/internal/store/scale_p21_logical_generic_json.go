package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
)

const metadataLogicalGenericJSON = uint64(1) << 61
const metadataLogicalCostMask = metadataLogicalGenericJSON - 1

// This representation belongs to the SQL/normalized-JSON boundary. Native
// capture still preserves original Go dynamic types through writeValue.
func metadataGenericJSONType(typ reflect.Type) bool {
	return typ == reflect.TypeFor[map[string]any]() || typ == reflect.TypeFor[[]any]() ||
		typ == reflect.TypeFor[any]() || typ == reflect.TypeFor[string]() ||
		typ == reflect.TypeFor[bool]() || typ == reflect.TypeFor[float64]()
}

func metadataGenericJSONDynamic(prefix byte) reflect.Type {
	switch prefix {
	case '{':
		return reflect.TypeFor[map[string]any]()
	case '[':
		return reflect.TypeFor[[]any]()
	case '"':
		return reflect.TypeFor[string]()
	case 't', 'f':
		return reflect.TypeFor[bool]()
	case 'n':
		return nil
	default:
		return reflect.TypeFor[float64]()
	}
}

func metadataGenericJSONPrefix(ctx context.Context, part legacySessionJSONPart) (byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if part.size <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	var first [1]byte
	_, err := part.input.ReadAt(first[:], part.start)
	return first[0], err
}

func metadataGenericJSONScanner(ctx context.Context, part legacySessionJSONPart) *metadataCollectionScanner {
	return &metadataCollectionScanner{
		ctx: ctx, offset: part.start,
		input: bufio.NewReaderSize(part.reader(), 256),
	}
}

// genericPart distinguishes serialized leaves from borrowed child ranges.
// A child range contains canonical JSON, never a typed frame header.
func (n metadataLogicalNode) genericPart() legacySessionJSONPart {
	if n.genericRange {
		return legacySessionJSONPart{n.source.file, n.offset, n.size}
	}
	return legacySessionJSONPart{n.source.file, n.offset + 16, n.size - 16}
}

func metadataGenericJSONWork(ctx context.Context, store *SQLiteStore, visit func(*metadataAggregateTraversal) error) error {
	if store == nil {
		return ErrBoundedMetadataCoverage
	}
	owner := &ResourceRead{store: store}
	defer owner.Close()
	// Includes the scanner's fixed grammar stack, decoder chunks and reader
	// objects. Recursive generic traversal owns its stack separately.
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return err
	}
	work, err := beginMetadataAggregateTraversal(ctx, func(size uint64) (func(), error) {
		if size > metadataWorkBytes {
			return nil, ErrMetadataWorkBudget
		}
		_, release, err := store.tryAdmitAdditionalMetadataWork(ctx, int(size))
		return release, err
	})
	if err != nil {
		return err
	}
	defer work.Close()
	return visit(work)
}

// Match writeValue's native-storage charge without expanding generic JSON
// into typed frames. Syntax and normalization already belong to the existing
// decoder; this traversal only measures immutable normalized ranges.
//
// The range is read once: every nested value is measured by the scanner that
// reaches it, rather than by rescanning its subtree once per enclosing
// container (which made a chain of depth d cost O(d²) bytes per call, and a
// child-by-child traversal O(d³)).
func metadataGenericJSONCost(ctx context.Context, typ reflect.Type, part legacySessionJSONPart, work *metadataAggregateTraversal) (uint64, error) {
	// The scanner and each string's decoder read small pieces of the same
	// range; one window (within the caller's fixed stream allowance) keeps
	// those re-reads out of the backing file.
	part.input = &metadataGenericJSONWindow{input: part.input}
	measure := metadataGenericJSONMeasure{ctx: ctx, part: part, work: work}
	return measure.value(typ, true)
}

// metadataGenericJSONWindow is an io.ReaderAt that serves reads smaller
// than its buffer from one cached window of the underlying input.
type metadataGenericJSONWindow struct {
	input  io.ReaderAt
	buffer []byte
	start  int64
	size   int
	err    error
}

func (w *metadataGenericJSONWindow) ReadAt(p []byte, offset int64) (int, error) {
	if w.buffer == nil {
		w.buffer = make([]byte, 16<<10)
	}
	if len(p) > len(w.buffer) {
		return w.input.ReadAt(p, offset)
	}
	if offset < w.start || offset+int64(len(p)) > w.start+int64(w.size) {
		w.size, w.err = w.input.ReadAt(w.buffer, offset)
		w.start = offset
		if w.size < len(p) && w.err == nil {
			w.err = io.ErrUnexpectedEOF
		}
	}
	count := copy(p, w.buffer[min(int(offset-w.start), w.size):w.size])
	if count < len(p) {
		return count, w.err
	}
	return count, nil
}

type metadataGenericJSONMeasure struct {
	ctx     context.Context
	part    legacySessionJSONPart
	work    *metadataAggregateTraversal
	scanner *metadataCollectionScanner
}

// value measures the value at the scanner's position and consumes it. The
// root (top) reads its prefix directly from the range, exactly as a fresh
// range measurement does, and is never consumed past what its kind needs.
func (m *metadataGenericJSONMeasure) value(typ reflect.Type, top bool) (uint64, error) {
	ctx := m.ctx
	if !metadataGenericJSONType(typ) {
		return 0, ErrBoundedMetadataCoverage
	}
	if err := m.work.enter(); err != nil {
		return 0, err
	}
	defer m.work.leave()
	cost := uint64(typ.Size())*2 + 128
	add := func(size uint64) error {
		if size > metadataWorkBytes || cost > metadataWorkBytes-size {
			return ErrMetadataWorkBudget
		}
		cost += size
		return nil
	}
	var prefix byte
	var err error
	if top {
		prefix, err = metadataGenericJSONPrefix(ctx, m.part)
	} else {
		prefix, err = m.peek()
	}
	if err != nil {
		return cost, err
	}
	if prefix == 'n' {
		if !top {
			_, err = m.scanner.value(0)
		}
		return cost, err
	}
	if typ.Kind() == reflect.Interface {
		child, err := m.value(metadataGenericJSONDynamic(prefix), top)
		if err != nil {
			return 0, err
		}
		if err := add(child); err != nil {
			return 0, err
		}
		return cost, nil
	}
	if metadataGenericJSONDynamic(prefix) != typ {
		return 0, ErrMetadataRecordQuery
	}
	if m.scanner == nil {
		m.scanner = metadataGenericJSONScanner(ctx, m.part)
	}
	scanner := m.scanner
	switch typ.Kind() {
	case reflect.String:
		if err := add(64); err != nil {
			return 0, err
		}
		input := m.part
		if !top {
			var value metadataCollectionRange
			if value, err = scanner.value(0); err != nil {
				return cost, err
			}
			input = legacySessionJSONPart{m.part.input, value.offset, value.size}
		}
		_, err = VisitMetadataJSONStringChunks(ctx, input.reader(), func(value []byte) error {
			return add(uint64(len(value)) * 2)
		})
	case reflect.Map:
		err = m.members('{', '}', func() error {
			if err := add(uint64(typ.Elem().Size())*2 + 64); err != nil {
				return err
			}
			size, err := m.value(typ.Key(), false)
			if err != nil {
				return err
			}
			if err := add(size); err != nil {
				return err
			}
			if err := scanner.spaces(); err != nil {
				return err
			}
			if err := scanner.expect(':'); err != nil {
				return err
			}
			if size, err = m.value(typ.Elem(), false); err != nil {
				return err
			}
			return add(size)
		})
	case reflect.Slice:
		err = m.members('[', ']', func() error {
			if err := add(uint64(typ.Elem().Size())*2 + 64); err != nil {
				return err
			}
			size, err := m.value(typ.Elem(), false)
			if err != nil {
				return err
			}
			return add(size)
		})
	default:
		if !top {
			_, err = scanner.value(0)
		}
	}
	return cost, err
}

func (m *metadataGenericJSONMeasure) peek() (byte, error) {
	if err := m.ctx.Err(); err != nil {
		return 0, err
	}
	if err := m.scanner.spaces(); err != nil {
		return 0, err
	}
	prefix, err := m.scanner.peek()
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return prefix, err
}

// members consumes one object or array, calling member at the start of each
// member; member consumes that member's value (and an object's key/colon).
func (m *metadataGenericJSONMeasure) members(open, close byte, member func() error) error {
	scanner := m.scanner
	if err := scanner.expect(open); err != nil {
		return err
	}
	if err := scanner.spaces(); err != nil {
		return err
	}
	if b, _ := scanner.peek(); b == close {
		return scanner.expect(close)
	}
	for {
		if err := scanner.spaces(); err != nil {
			return err
		}
		if err := member(); err != nil {
			return err
		}
		if err := scanner.spaces(); err != nil {
			return err
		}
		b, err := scanner.byte()
		if err != nil {
			return err
		}
		if b == close {
			return nil
		}
		if b != ',' {
			return errMetadataCollectionJSON
		}
	}
}

// The metered decoder intentionally exposes one invalid-JSON boundary.
// Preserve the SQL typed loader's numeric error family using the existing
// scanner and standard scalar decoder, without allocating another graph.
func metadataGenericJSONDecodeError(ctx context.Context, store *SQLiteStore, part legacySessionJSONPart) error {
	err := metadataGenericJSONWork(ctx, store, func(work *metadataAggregateTraversal) error {
		var validate func(legacySessionJSONPart) error
		validate = func(part legacySessionJSONPart) error {
			if err := work.enter(); err != nil {
				return err
			}
			defer work.leave()
			scanner := metadataGenericJSONScanner(ctx, part)
			if err := scanner.spaces(); err != nil {
				return err
			}
			first, err := scanner.peek()
			if err != nil {
				return err
			}
			switch first {
			case '{':
				err = scanner.object(func(_, value metadataCollectionRange) error {
					return validate(legacySessionJSONPart{part.input, value.offset, value.size})
				}, 0)
			case '[':
				err = scanner.array(func(value metadataCollectionRange) error {
					return validate(legacySessionJSONPart{part.input, value.offset, value.size})
				}, 0)
			case '"':
				_, err = VisitMetadataJSONStringChunks(ctx, part.reader(), func([]byte) error { return nil })
				return err
			default:
				owner, err := store.BeginMetadataJSONRead(ctx)
				if err != nil {
					return err
				}
				defer owner.Close()
				if part.size < 0 || part.size > metadataWorkBytes/4 {
					return ErrMetadataWorkBudget
				}
				if err := owner.ReserveEncoded(ctx, int(part.size)); err != nil {
					return err
				}
				raw := make([]byte, int(part.size))
				if _, err := io.ReadFull(part.reader(), raw); err != nil {
					return err
				}
				var value any
				return json.Unmarshal(raw, &value)
			}
			if err != nil {
				return err
			}
			return scanner.end()
		}
		return validate(part)
	})
	if err == nil {
		return ErrMetadataRecordQuery
	}
	return err
}

// writeGenericJSON accepts only the exact generic types listed above. It is
// never an implicit conversion of a warm native graph or custom marshaler.
func (b *metadataLogicalBuilder) writeGenericJSON(ctx context.Context, s *SQLiteStore, typ reflect.Type, part legacySessionJSONPart) (metadataLogicalNode, error) {
	if !metadataGenericJSONType(typ) {
		return metadataLogicalNode{}, ErrBoundedMetadataCoverage
	}
	input, err := s.BeginMetadataJSONRead(ctx)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	defer input.Close()
	if part.size < 0 || part.size > metadataWorkBytes/4 {
		return metadataLogicalNode{}, ErrMetadataWorkBudget
	}
	if err := input.ReserveEncoded(ctx, int(part.size)); err != nil {
		return metadataLogicalNode{}, err
	}
	raw := make([]byte, int(part.size))
	if _, err := io.ReadFull(part.reader(), raw); err != nil {
		return metadataLogicalNode{}, err
	}
	decode, err := s.BeginMetadataJSONRead(ctx)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	defer decode.Close()
	value, valid, err := decode.DecodeGeneric(ctx, raw)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if !valid {
		return metadataLogicalNode{}, metadataGenericJSONDecodeError(ctx, s, part)
	}
	native := reflect.New(typ).Elem()
	if value != nil {
		dynamic := reflect.ValueOf(value)
		if !dynamic.Type().AssignableTo(typ) {
			kind := dynamic.Kind().String()
			if dynamic.Kind() == reflect.Float64 {
				kind = "number"
			}
			return metadataLogicalNode{}, &json.UnmarshalTypeError{Value: kind, Type: typ}
		}
		native.Set(dynamic)
	}
	raw = nil
	input.Close()
	encode, err := s.BeginMetadataJSONRead(ctx)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	defer encode.Close()
	canonical, valid, err := encode.EncodeGeneric(ctx, native.Interface())
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if !valid {
		return metadataLogicalNode{}, ErrBoundedMetadataCoverage
	}
	value, native = nil, reflect.Value{}
	decode.Close()
	part = legacySessionJSONPart{bytes.NewReader(canonical), 0, int64(len(canonical))}
	var cost uint64
	err = metadataGenericJSONWork(ctx, s, func(work *metadataAggregateTraversal) error {
		var err error
		cost, err = metadataGenericJSONCost(ctx, typ, part, work)
		return err
	})
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if b.storage.genericStore == nil {
		b.storage.genericStore = s
	}
	node, err := b.writeGenericJSONPart(typ, part, cost)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := ctx.Err(); err != nil {
		return metadataLogicalNode{}, err
	}
	return node, nil
}

func (b *metadataLogicalBuilder) writeGenericJSONPart(typ reflect.Type, part legacySessionJSONPart, cost uint64) (metadataLogicalNode, error) {
	node := metadataLogicalNode{source: b.storage, offset: b.offset, typ: typ, cost: cost, generic: true}
	if !metadataGenericJSONType(typ) || cost > metadataWorkBytes || part.size < 0 {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	if err := b.write(make([]byte, 16)); err != nil {
		return metadataLogicalNode{}, err
	}
	count, err := io.CopyBuffer(metadataLogicalWriter{b}, part.reader(), b.buffer[:])
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if count != part.size {
		return metadataLogicalNode{}, io.ErrUnexpectedEOF
	}
	node.size = b.offset - node.offset
	return node, b.patch(node)
}

func (n metadataLogicalNode) genericValue(ctx context.Context) (reflect.Value, error) {
	if value, cold, err := n.coldGenericValue(ctx); cold {
		return value, err
	}
	part := n.genericPart()
	input, err := n.source.genericStore.BeginMetadataJSONRead(ctx)
	if err != nil {
		return reflect.Value{}, err
	}
	defer input.Close()
	if part.size < 0 || part.size > metadataWorkBytes/4 {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := input.ReserveEncoded(ctx, int(part.size)); err != nil {
		return reflect.Value{}, err
	}
	raw := make([]byte, int(part.size))
	if _, err := io.ReadFull(part.reader(), raw); err != nil {
		return reflect.Value{}, err
	}
	decode, err := n.source.genericStore.BeginMetadataJSONRead(ctx)
	if err != nil {
		return reflect.Value{}, err
	}
	defer decode.Close()
	value, valid, err := decode.DecodeGeneric(ctx, raw)
	if err != nil {
		return reflect.Value{}, err
	}
	if !valid {
		return reflect.Value{}, ErrMetadataRecordQuery
	}
	result := reflect.New(n.typ).Elem()
	if value != nil {
		dynamic := reflect.ValueOf(value)
		if !dynamic.Type().AssignableTo(n.typ) {
			return reflect.Value{}, ErrMetadataRecordQuery
		}
		result.Set(dynamic)
	}
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, err
	}
	// Value/WireValue's caller already owns n.cost until the result dies.
	// The decoder's additional reservation covers its parsing and temporary
	// high-water; closing it transfers the live native graph to that caller.
	return result, nil
}

func (n metadataLogicalNode) genericChild(ctx context.Context, typ reflect.Type, part legacySessionJSONPart, work *metadataAggregateTraversal) (metadataLogicalNode, error) {
	cost, err := metadataGenericJSONCost(ctx, typ, part, work)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	return metadataLogicalNode{
		source: n.source, offset: part.start, size: part.size, typ: typ, cost: cost,
		generic: true, genericRange: true,
	}, nil
}

// Parser scratch expires before a borrowed child callback. Otherwise nested
// callbacks would retain one complete grammar stack per JSON container.
type metadataGenericJSONScanWork struct {
	ctx     context.Context
	store   *SQLiteStore
	release func()
}

func (w *metadataGenericJSONScanWork) resume() error {
	_, release, err := w.store.tryAdmitAdditionalMetadataWork(w.ctx, logicalJSONStreamWork)
	if err == nil {
		w.release = release
	}
	return err
}

func (w *metadataGenericJSONScanWork) suspend() {
	if w.release != nil {
		w.release()
		w.release = nil
	}
}

func (w *metadataGenericJSONScanWork) child(visit func() error) error {
	w.suspend()
	if err := visit(); err != nil {
		return err
	}
	return w.resume()
}

func (n metadataLogicalNode) genericMapEntries(ctx context.Context, visit func(metadataLogicalNode, metadataLogicalNode) error) error {
	part := n.genericPart()
	owner := &ResourceRead{store: n.source.genericStore}
	defer owner.Close()
	if err := owner.reserve(ctx, metadataAggregateTraversalFrame); err != nil {
		return err
	}
	prefix, err := metadataGenericJSONPrefix(ctx, part)
	if err != nil || prefix == 'n' {
		return err
	}
	if n.typ != reflect.TypeFor[map[string]any]() || prefix != '{' {
		return ErrMetadataRecordQuery
	}
	scratch := metadataGenericJSONScanWork{ctx: ctx, store: n.source.genericStore}
	if err := scratch.resume(); err != nil {
		return err
	}
	defer scratch.suspend()
	return metadataGenericJSONScanner(ctx, part).object(func(key, value metadataCollectionRange) error {
		var a, b metadataLogicalNode
		err := metadataGenericJSONWork(ctx, n.source.genericStore, func(work *metadataAggregateTraversal) error {
			var err error
			a, err = n.genericChild(ctx, n.typ.Key(), legacySessionJSONPart{part.input, key.offset, key.size}, work)
			if err == nil {
				b, err = n.genericChild(ctx, n.typ.Elem(), legacySessionJSONPart{part.input, value.offset, value.size}, work)
			}
			return err
		})
		if err != nil {
			return err
		}
		return scratch.child(func() error { return visit(a, b) })
	}, 0)
}

func (n metadataLogicalNode) genericChildren(ctx context.Context, visit func(reflect.Value, metadataLogicalNode) error) error {
	if n.typ.Kind() == reflect.Map {
		return n.genericMapEntries(ctx, func(key, child metadataLogicalNode) error {
			owner := &ResourceRead{store: n.source.genericStore}
			defer owner.Close()
			if key.cost > 256<<10 {
				return ErrMetadataWorkBudget
			}
			if err := owner.reserve(ctx, key.cost); err != nil {
				return err
			}
			value, err := key.Value(ctx, 256<<10)
			if err != nil {
				return err
			}
			return visit(value, child)
		})
	}
	part := n.genericPart()
	owner := &ResourceRead{store: n.source.genericStore}
	defer owner.Close()
	if err := owner.reserve(ctx, metadataAggregateTraversalFrame); err != nil {
		return err
	}
	prefix, err := metadataGenericJSONPrefix(ctx, part)
	if err != nil || prefix == 'n' {
		return err
	}
	if n.typ.Kind() == reflect.Interface {
		var child metadataLogicalNode
		err := metadataGenericJSONWork(ctx, n.source.genericStore, func(work *metadataAggregateTraversal) error {
			var err error
			child, err = n.genericChild(ctx, metadataGenericJSONDynamic(prefix), part, work)
			return err
		})
		if err != nil {
			return err
		}
		return visit(reflect.Value{}, child)
	}
	if n.typ.Kind() != reflect.Slice {
		return nil
	}
	scratch := metadataGenericJSONScanWork{ctx: ctx, store: n.source.genericStore}
	if err := scratch.resume(); err != nil {
		return err
	}
	defer scratch.suspend()
	index := 0
	return metadataGenericJSONScanner(ctx, part).array(func(value metadataCollectionRange) error {
		var child metadataLogicalNode
		err := metadataGenericJSONWork(ctx, n.source.genericStore, func(work *metadataAggregateTraversal) error {
			var err error
			child, err = n.genericChild(ctx, n.typ.Elem(), legacySessionJSONPart{part.input, value.offset, value.size}, work)
			return err
		})
		if err != nil {
			return err
		}
		err = scratch.child(func() error { return visit(reflect.ValueOf(index), child) })
		index++
		return err
	}, 0)
}

func (n metadataLogicalNode) genericStringEqual(ctx context.Context, want string) (bool, error) {
	position, equal := 0, true
	err := metadataGenericJSONWork(ctx, n.source.genericStore, func(*metadataAggregateTraversal) error {
		_, err := VisitMetadataJSONStringChunks(ctx, n.genericPart().reader(), func(value []byte) error {
			if len(value) > len(want)-position {
				equal = false
				return nil
			}
			if !bytes.Equal(value, []byte(want[position:position+len(value)])) {
				equal = false
			}
			position += len(value)
			return nil
		})
		return err
	})
	return equal && position == len(want), err
}

func (n metadataLogicalNode) genericStreamBytes(ctx context.Context, visit func(io.Reader) error) error {
	if n.typ.Kind() != reflect.String {
		return ErrMetadataRecordQuery
	}
	return metadataGenericJSONWork(ctx, n.source.genericStore, func(*metadataAggregateTraversal) error {
		builder, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return err
		}
		defer builder.Close()
		decoded, err := legacyProjectFrameString(ctx, builder, n.genericPart().reader(), metadataLogicalNode{})
		if err != nil {
			return err
		}
		return decoded.StreamBytes(ctx, visit)
	})
}

func (n metadataLogicalNode) genericEmpty(ctx context.Context, zero bool) (bool, error) {
	part := n.genericPart()
	prefix, err := metadataGenericJSONPrefix(ctx, part)
	if err != nil || prefix == 'n' {
		return prefix == 'n', err
	}
	switch n.typ.Kind() {
	case reflect.Interface:
		return false, nil
	case reflect.Map, reflect.Slice:
		return !zero && part.size == 2, nil
	case reflect.String:
		return part.size == 2, nil
	default:
		value, err := n.Value(ctx, 4096)
		if err != nil {
			return false, err
		}
		return value.IsZero(), nil
	}
}

func (n metadataLogicalNode) genericCollectionSize(ctx context.Context) (count int, present bool, err error) {
	part := n.genericPart()
	err = metadataGenericJSONWork(ctx, n.source.genericStore, func(*metadataAggregateTraversal) error {
		prefix, err := metadataGenericJSONPrefix(ctx, part)
		if err != nil || prefix == 'n' {
			return err
		}
		present = true
		scanner := metadataGenericJSONScanner(ctx, part)
		switch n.typ.Kind() {
		case reflect.Map:
			return scanner.object(func(_, _ metadataCollectionRange) error { count++; return nil }, 0)
		case reflect.Slice:
			return scanner.array(func(_ metadataCollectionRange) error { count++; return nil }, 0)
		default:
			return ErrMetadataRecordQuery
		}
	})
	return count, present, err
}

func (s *logicalJSONStream) genericWrite(node metadataLogicalNode) error {
	copyRaw := func(node metadataLogicalNode) error {
		part := node.genericPart()
		input := io.LimitReader(&metadataLogicalByteReader{s.ctx, part.input, part.start}, part.size)
		_, err := io.CopyBuffer(s.out, input, s.buffer[:])
		if err != nil {
			return err
		}
		return s.ctx.Err()
	}
	// With no nested transformation, this is a single immutable typed leaf.
	if s.rewrite == nil && s.replace == nil {
		return copyRaw(node)
	}
	return metadataGenericJSONWork(s.ctx, node.source.genericStore, func(work *metadataAggregateTraversal) error {
		var write func(metadataLogicalNode, bool) error
		write = func(node metadataLogicalNode, root bool) error {
			if err := work.enter(); err != nil {
				return err
			}
			defer work.leave()
			if !root && s.replace != nil {
				var err error
				node, err = s.replace(node)
				if err != nil {
					return err
				}
			}
			if !node.generic {
				return s.write(webhookJSONValue{node: node}, 0)
			}
			absent, err := node.isNil(s.ctx)
			if err != nil {
				return err
			}
			if absent {
				return s.text("null")
			}
			switch node.typ.Kind() {
			case reflect.Interface:
				return node.Children(s.ctx, func(_ reflect.Value, child metadataLogicalNode) error {
					return write(child, false)
				})
			case reflect.Map:
				if err := s.text("{"); err != nil {
					return err
				}
				first := true
				err := node.MapEntries(s.ctx, func(key, child metadataLogicalNode) error {
					if !first {
						if err := s.text(","); err != nil {
							return err
						}
					}
					first = false
					if err := copyRaw(key); err != nil {
						return err
					}
					if err := s.text(":"); err != nil {
						return err
					}
					return write(child, false)
				})
				if err != nil {
					return err
				}
				return s.text("}")
			case reflect.Slice:
				if err := s.text("["); err != nil {
					return err
				}
				first := true
				err := node.Children(s.ctx, func(_ reflect.Value, child metadataLogicalNode) error {
					if !first {
						if err := s.text(","); err != nil {
							return err
						}
					}
					first = false
					return write(child, false)
				})
				if err != nil {
					return err
				}
				return s.text("]")
			case reflect.String:
				if s.rewrite != nil {
					if handled, err := s.rewriteNode(node); handled || err != nil {
						return err
					}
				}
			}
			return copyRaw(node)
		}
		return write(node, true)
	})
}

// A fresh key view preserves the legacy accessor's Go-map traversal rather
// than replacing ambiguous nested-ID traversal with canonical key order.
func (n metadataLogicalNode) genericMapValues(ctx context.Context, visit func(metadataAggregateNode) error) error {
	owner := &ResourceRead{store: n.source.genericStore}
	defer owner.Close()
	count := 0
	if err := n.MapEntries(ctx, func(_, _ metadataLogicalNode) error { count++; return nil }); err != nil {
		return err
	}
	slot := uint64(reflect.TypeFor[metadataLogicalNode]().Size()) * 2
	if uint64(count) > (metadataWorkBytes-1024)/(512+slot) {
		return ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(count)*(512+slot)+metadataAggregateTraversalFrame*3); err != nil {
		return err
	}
	order := make(map[string]int, count)
	values := make([]metadataLogicalNode, 0, count)
	err := n.MapEntries(ctx, func(key, value metadataLogicalNode) error {
		if err := owner.reserve(ctx, key.cost); err != nil {
			return err
		}
		name, err := key.Value(ctx, metadataWorkBytes)
		if err != nil {
			return err
		}
		order[name.String()] = len(values)
		values = append(values, value)
		return nil
	})
	if err != nil {
		return err
	}
	for _, index := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(metadataAggregateStoredNode{values[index]}); err != nil {
			return err
		}
	}
	return nil
}

func metadataGenericJSONEqual(ctx context.Context, a, b metadataLogicalNode) (bool, error) {
	store := a.source.genericStore
	if store == nil {
		store = b.source.genericStore
	}
	equal := false
	err := metadataGenericJSONWork(ctx, store, func(work *metadataAggregateTraversal) error {
		var compare func(metadataLogicalNode, metadataLogicalNode) (bool, error)
		compare = func(a, b metadataLogicalNode) (bool, error) {
			if err := work.enter(); err != nil {
				return false, err
			}
			defer work.leave()
			if a.typ != b.typ {
				return false, nil
			}
			kind := a.typ.Kind()
			if !metadataGenericJSONType(a.typ) {
				return legacyMetadataWriteEqualMode(ctx, a, b, 0, true)
			}
			if kind == reflect.Map || kind == reflect.Slice || kind == reflect.Interface {
				leftNil, err := a.isNil(ctx)
				if err != nil {
					return false, err
				}
				rightNil, err := b.isNil(ctx)
				if err != nil || leftNil != rightNil || leftNil {
					return leftNil == rightNil, err
				}
			}
			if kind == reflect.String {
				return metadataGenericJSONStringsEqual(ctx, a, b)
			}
			if kind == reflect.Bool || kind == reflect.Float64 {
				x, err := a.Value(ctx, 4096)
				if err != nil {
					return false, err
				}
				y, err := b.Value(ctx, 4096)
				if err != nil {
					return false, err
				}
				return reflect.DeepEqual(x.Interface(), y.Interface()), nil
			}
			if kind == reflect.Interface {
				var left, right metadataLogicalNode
				if err := a.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error { left = child; return nil }); err != nil {
					return false, err
				}
				if err := b.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error { right = child; return nil }); err != nil {
					return false, err
				}
				return compare(left, right)
			}
			leftCount, rightCount := 0, 0
			if kind == reflect.Map {
				if err := a.MapEntries(ctx, func(_, _ metadataLogicalNode) error { leftCount++; return nil }); err != nil {
					return false, err
				}
				if err := b.MapEntries(ctx, func(_, _ metadataLogicalNode) error { rightCount++; return nil }); err != nil {
					return false, err
				}
				if leftCount != rightCount {
					return false, nil
				}
				matched := true
				err := a.MapEntries(ctx, func(key, value metadataLogicalNode) error {
					found := false
					err := b.MapEntries(ctx, func(otherKey, otherValue metadataLogicalNode) error {
						same, err := compare(key, otherKey)
						if err != nil || !same {
							return err
						}
						found = true
						matched, err = compare(value, otherValue)
						if err != nil {
							return err
						}
						return errMetadataAggregateStop
					})
					if err != nil && !errors.Is(err, errMetadataAggregateStop) {
						return err
					}
					if !found {
						matched = false
					}
					if !matched {
						return errMetadataAggregateStop
					}
					return nil
				})
				if errors.Is(err, errMetadataAggregateStop) {
					err = nil
				}
				return matched, err
			}
			if err := a.Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error { leftCount++; return nil }); err != nil {
				return false, err
			}
			if err := b.Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error { rightCount++; return nil }); err != nil {
				return false, err
			}
			if leftCount != rightCount {
				return false, nil
			}
			matched := true
			err := a.Children(ctx, func(index reflect.Value, child metadataLogicalNode) error {
				var other metadataLogicalNode
				err := b.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
					if key.Int() == index.Int() {
						other = value
						return errMetadataAggregateStop
					}
					return nil
				})
				if err != nil && !errors.Is(err, errMetadataAggregateStop) {
					return err
				}
				matched, err = compare(child, other)
				if err != nil {
					return err
				}
				if !matched {
					return errMetadataAggregateStop
				}
				return nil
			})
			if errors.Is(err, errMetadataAggregateStop) {
				err = nil
			}
			return matched, err
		}
		var err error
		equal, err = compare(a, b)
		return err
	})
	return equal, err
}

func metadataGenericJSONStringsEqual(ctx context.Context, a, b metadataLogicalNode) (bool, error) {
	equal := true
	err := a.StreamBytes(ctx, func(left io.Reader) error {
		return b.StreamBytes(ctx, func(right io.Reader) error {
			var x, y [4096]byte
			for {
				n, leftErr := io.ReadFull(left, x[:])
				m, rightErr := io.ReadFull(right, y[:])
				if leftErr != nil && !errors.Is(leftErr, io.EOF) && !errors.Is(leftErr, io.ErrUnexpectedEOF) {
					return leftErr
				}
				if rightErr != nil && !errors.Is(rightErr, io.EOF) && !errors.Is(rightErr, io.ErrUnexpectedEOF) {
					return rightErr
				}
				if n != m || !bytes.Equal(x[:n], y[:m]) {
					equal = false
					return nil
				}
				if leftErr != nil || rightErr != nil {
					equal = (leftErr != nil) == (rightErr != nil)
					return nil
				}
			}
		})
	})
	return equal, err
}
