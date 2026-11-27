package store

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type metadataLogicalCursor struct {
	node   metadataLogicalNode
	offset int64
	ctx    context.Context
}

func (c *metadataLogicalCursor) read(p []byte) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if int64(len(p)) > c.node.offset+c.node.size-c.offset {
		return io.ErrUnexpectedEOF
	}
	if c.node.window.read(p, c.node.source, c.offset) {
		c.offset += int64(len(p))
		return nil
	}
	if c.slide(len(p)) && c.node.window.read(p, c.node.source, c.offset) {
		c.offset += int64(len(p))
		return nil
	}
	_, err := c.node.source.ReadAt(p, c.offset)
	c.offset += int64(len(p))
	return err
}

func (c *metadataLogicalCursor) uint() (uint64, error) {
	var p [8]byte
	err := c.read(p[:])
	return binary.LittleEndian.Uint64(p[:]), err
}

func (c *metadataLogicalCursor) byte() (byte, error) {
	var p [1]byte
	err := c.read(p[:])
	return p[0], err
}

func (c *metadataLogicalCursor) child(t reflect.Type) (metadataLogicalNode, error) {
	start := c.offset
	var header [16]byte
	if err := c.read(header[:]); err != nil {
		return metadataLogicalNode{}, err
	}
	size := binary.LittleEndian.Uint64(header[:8])
	cost := binary.LittleEndian.Uint64(header[8:])
	if size < 16 || size > uint64(c.node.offset+c.node.size-start) {
		return metadataLogicalNode{}, io.ErrUnexpectedEOF
	}
	c.offset = start + int64(size)
	window := c.node.window
	if window != nil && (start < window.at || start+int64(size) > window.at+int64(len(window.data))) {
		// A sliding window holds part of the child at most: the child takes
		// its own (windowed).
		window = nil
	}
	if window != nil && window.slid {
		window.lent = true
	}
	return metadataLogicalNode{source: c.node.source, offset: start, size: int64(size), cost: cost & metadataLogicalCostMask, typ: t, unavailable: cost&(uint64(1)<<63) != 0, blocked: cost&(uint64(1)<<62) != 0, generic: cost&metadataLogicalGenericJSON != 0, window: window}, nil
}

// metadataLogicalSlideBytes is the read-ahead of a cursor over a node too
// large for one window (a large collection): its child headers and small
// children are read in chunks of this size instead of one shared-cache block
// (a pread at every miss) per header.
const metadataLogicalSlideBytes = 64 << 10

// slide replaces the cursor's window, for a small read of a node too large
// for one window, by the next chunk of the node from the read's offset. It
// reports whether it read one. Like a window, a chunk is the cursor's own
// copy of bytes the traversal reads anyway, dropped with the cursor; a chunk
// no child was given is refilled in place (metadataLogicalWindow.lent).
func (c *metadataLogicalCursor) slide(size int) bool {
	if c.node.source == nil || c.node.size <= metadataLogicalWindowBytes || size > metadataLogicalSlideBytes/4 {
		return false
	}
	end := min(c.node.offset+c.node.size, c.offset+metadataLogicalSlideBytes)
	if end-c.offset < int64(size) {
		return false
	}
	window := c.node.window
	if window != nil && window.slid && !window.lent && window.storage == c.node.source && int64(cap(window.data)) >= end-c.offset {
		// A refilled chunk no longer holds its old bytes, even when the read
		// fails.
		c.node.window = nil
	} else {
		window = &metadataLogicalWindow{storage: c.node.source, slid: true, data: make([]byte, end-c.offset)}
	}
	data := window.data[:end-c.offset]
	if count, err := c.node.source.ReadAt(data, c.offset); err != nil || count != len(data) {
		return false
	}
	window.at, window.data = c.offset, data
	c.node.window = window
	return true
}

// Value admits the entire decoded object before allocation. The caller must
// hold a work lease until the returned value is no longer retained.
func (n metadataLogicalNode) Value(ctx context.Context, maxBytes int) (reflect.Value, error) {
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, err
	}
	if n.unavailable {
		return reflect.Value{}, fmt.Errorf("%w: installed logical subtree %s is unavailable", ErrBoundedMetadataCoverage, n.typ)
	}
	if maxBytes < 0 || n.cost > uint64(maxBytes) {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := n.source.Retain(); err != nil {
		return reflect.Value{}, err
	}
	defer n.source.Release()
	return n.value(ctx, 0)
}

func (n metadataLogicalNode) value(ctx context.Context, depth int) (reflect.Value, error) {
	return n.materialize(ctx, depth, false)
}

func (n metadataLogicalNode) materialize(ctx context.Context, depth int, wire bool) (reflect.Value, error) {
	if n.generic {
		return n.genericValue(ctx)
	}
	if wire && n.unavailable && metadataLogicalWireCustomMarshaler(n.typ) {
		return reflect.Value{}, ErrBoundedMetadataCoverage
	}
	if wire && n.blocked && n.typ != reflect.TypeFor[time.Time]() {
		return reflect.Value{}, fmt.Errorf("%w: opaque wire subtree %s", ErrBoundedMetadataCoverage, n.typ)
	}
	if depth >= metadataLogicalDepth {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	n = n.windowed()
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	v := reflect.New(n.typ).Elem()
	if n.typ == reflect.TypeFor[time.Time]() {
		mode, err := c.byte()
		if err != nil {
			return v, err
		}
		if mode == metadataLogicalWireTime {
			if !wire {
				return v, ErrBoundedMetadataCoverage
			}
			return n.wireTimeValue(&c)
		}
		first, err := c.uint()
		if err != nil {
			return v, err
		}
		second, err := c.uint()
		if err != nil {
			return v, err
		}
		var result time.Time
		if mode == 0 {
			local, err := c.uint()
			if err != nil {
				return v, err
			}
			result = time.Unix(int64(first), int64(second)).UTC()
			if local == 1 {
				result = result.In(metadataLogicalProcessLocal)
			} else if local >= 2 && local-2 < uint64(len(n.source.locations)) {
				result = result.In(n.source.locations[local-2])
			} else if local != 0 {
				return v, ErrMetadataRecordQuery
			}
		} else if mode == 1 && first < uint64(len(n.source.times)) {
			result = n.source.times[first].Add(time.Duration(second))
		} else if mode == metadataLogicalOffsetTime {
			offset, err := c.uint()
			if err != nil {
				return v, err
			}
			if result, err = metadataLogicalOffsetValue(first, second, offset); err != nil {
				return v, err
			}
		} else {
			return v, ErrMetadataRecordQuery
		}
		v.Set(reflect.ValueOf(result))
		return v, nil
	}
	switch n.typ.Kind() {
	case reflect.Bool:
		value, err := c.byte()
		v.SetBool(value != 0)
		return v, err
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, err := c.uint()
		v.SetInt(int64(value))
		return v, err
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value, err := c.uint()
		v.SetUint(value)
		return v, err
	case reflect.Float32, reflect.Float64:
		value, err := c.uint()
		v.SetFloat(math.Float64frombits(value))
		return v, err
	case reflect.Complex64, reflect.Complex128:
		r, err := c.uint()
		if err != nil {
			return v, err
		}
		i, err := c.uint()
		v.SetComplex(complex(math.Float64frombits(r), math.Float64frombits(i)))
		return v, err
	case reflect.String:
		length, err := c.uint()
		if err != nil {
			return v, err
		}
		if length > uint64(n.size) {
			return v, io.ErrUnexpectedEOF
		}
		data := make([]byte, int(length))
		if err := c.read(data); err != nil {
			return v, err
		}
		v.SetString(string(data))
		return v, nil
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		present, err := c.byte()
		if err != nil || present == 0 {
			return v, err
		}
		if n.typ.Kind() == reflect.Pointer || n.typ.Kind() == reflect.Interface {
			var t reflect.Type
			if n.typ.Kind() == reflect.Interface {
				index, err := c.uint()
				if err != nil {
					return v, err
				}
				if index >= uint64(len(n.source.types)) {
					return v, ErrMetadataRecordQuery
				}
				t = n.source.types[index]
			} else {
				t = n.typ.Elem()
			}
			node, err := c.child(t)
			if err != nil {
				return v, err
			}
			owned, err := node.materialize(ctx, depth+1, wire)
			if err != nil {
				return v, err
			}
			if n.typ.Kind() == reflect.Pointer {
				result := reflect.New(t)
				result.Elem().Set(owned)
				return result, nil
			}
			v.Set(owned)
			return v, nil
		}
		length, err := c.uint()
		if err != nil {
			return v, err
		}
		if length > uint64(n.cost) {
			return v, io.ErrUnexpectedEOF
		}
		if n.typ.Kind() == reflect.Slice {
			v.Set(reflect.MakeSlice(n.typ, int(length), int(length)))
			if n.typ.Elem().Kind() == reflect.Uint8 {
				for i := 0; i < int(length); i += 32 << 10 {
					data := make([]byte, min(32<<10, int(length)-i))
					if err := c.read(data); err != nil {
						return v, err
					}
					for j, value := range data {
						v.Index(i + j).SetUint(uint64(value))
					}
				}
				return v, nil
			}
		} else {
			v.Set(reflect.MakeMapWithSize(n.typ, int(length)))
		}
		for i := 0; i < int(length); i++ {
			var key reflect.Value
			if n.typ.Kind() == reflect.Map {
				node, err := c.child(n.typ.Key())
				if err != nil {
					return v, err
				}
				// Detached wire times must not change native map-key identity.
				if wire && node.unavailable {
					return v, ErrBoundedMetadataCoverage
				}
				key, err = node.materialize(ctx, depth+1, wire)
				if err != nil {
					return v, err
				}
			}
			node, err := c.child(n.typ.Elem())
			if err != nil {
				return v, err
			}
			owned, err := node.materialize(ctx, depth+1, wire)
			if err != nil {
				return v, err
			}
			if n.typ.Kind() == reflect.Map {
				v.SetMapIndex(key, owned)
			} else {
				v.Index(i).Set(owned)
			}
		}
	case reflect.Struct, reflect.Array:
		length := n.typ.NumField
		t := func(i int) reflect.Type { return n.typ.Field(i).Type }
		at := v.Field
		if n.typ.Kind() == reflect.Array {
			length = n.typ.Len
			t = func(int) reflect.Type { return n.typ.Elem() }
			at = v.Index
		}
		for i := 0; i < length(); i++ {
			node, err := c.child(t(i))
			if err != nil {
				return v, err
			}
			owned, err := node.materialize(ctx, depth+1, wire)
			if err != nil {
				return v, err
			}
			at(i).Set(owned)
		}
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
	default:
		return v, ErrMetadataRecordQuery
	}
	return v, nil
}

func metadataLogicalCollectionSize(ctx context.Context, node metadataLogicalNode) (int, bool, error) {
	if node.generic {
		return node.genericCollectionSize(ctx)
	}
	cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
	present, err := cursor.byte()
	if err != nil || present == 0 {
		return 0, false, err
	}
	count, err := cursor.uint()
	if err != nil || count > node.cost || count > uint64(^uint(0)>>1) {
		return 0, false, errors.Join(err, ErrMetadataRecordQuery)
	}
	return int(count), true, nil
}

// Children borrows framed child nodes without decoding their bodies. Map keys
// are decoded under a fixed bound; captures reject unsupported key graphs.
func (n metadataLogicalNode) Children(ctx context.Context, visit func(reflect.Value, metadataLogicalNode) error) error {
	return n.children(ctx, true, visit)
}

func (n metadataLogicalNode) children(ctx context.Context, index bool, visit func(reflect.Value, metadataLogicalNode) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n.blocked {
		return fmt.Errorf("%w: opaque installed logical subtree %s", ErrBoundedMetadataCoverage, n.typ)
	}
	if err := n.source.Retain(); err != nil {
		return err
	}
	defer n.source.Release()
	if n.generic {
		return n.genericChildren(ctx, visit)
	}
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	if n.typ == reflect.TypeFor[time.Time]() {
		return nil
	}
	length := 0
	switch n.typ.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		present, err := c.byte()
		if err != nil || present == 0 {
			return err
		}
		if n.typ.Kind() == reflect.Pointer || n.typ.Kind() == reflect.Interface {
			var t reflect.Type
			if n.typ.Kind() == reflect.Interface {
				index, err := c.uint()
				if err != nil {
					return err
				}
				if index >= uint64(len(n.source.types)) {
					return ErrMetadataRecordQuery
				}
				t = n.source.types[index]
			} else {
				t = n.typ.Elem()
			}
			node, err := c.child(t)
			if err != nil {
				return err
			}
			return visit(reflect.Value{}, node)
		}
		if n.typ.Kind() == reflect.Slice && n.typ.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		count, err := c.uint()
		if err != nil {
			return err
		}
		if count > uint64(n.cost) {
			return ErrMetadataRecordQuery
		}
		length = int(count)
	case reflect.Array:
		length = n.typ.Len()
	case reflect.Struct:
		length = n.typ.NumField()
	default:
		return nil
	}
	if index && (n.typ.Kind() == reflect.Slice || n.typ.Kind() == reflect.Array) && length >= metadataLogicalIndexMinimum {
		return n.indexedChildren(ctx, &c, length, visit)
	}
	var fields []metadataStructField
	if n.typ.Kind() == reflect.Struct {
		fields = metadataStructFields(n.typ)
	}
	for i := 0; i < length; i++ {
		key := reflect.ValueOf(i)
		t := n.typ.Elem
		if n.typ.Kind() == reflect.Struct {
			f := fields[i]
			key, t = reflect.ValueOf(f.name), func() reflect.Type { return f.typ }
		} else if n.typ.Kind() == reflect.Map {
			node, err := c.child(n.typ.Key())
			if err != nil {
				return err
			}
			key, err = node.Value(ctx, 256<<10)
			if err != nil {
				return err
			}
		}
		node, err := c.child(t())
		if err != nil {
			return err
		}
		if err := visit(key, node); err != nil {
			return err
		}
	}
	return nil
}

// indexedChildren visits a large array or slice from its storage's header index,
// reading and admitting the index on the first complete scan.
func (n metadataLogicalNode) indexedChildren(ctx context.Context, c *metadataLogicalCursor, length int, visit func(reflect.Value, metadataLogicalNode) error) error {
	t := n.typ.Elem()
	storage := n.source
	if headers, found := storage.childIndex(n.offset, n.size, c.offset, length); found {
		for i, header := range headers {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := header.node(n.source, t)
			// As from the cursor (child): a child inside the node's
			// window reads from it rather than taking its own.
			if window := n.window; window != nil && !window.slid && window.storage == n.source &&
				child.offset >= window.at && child.offset+child.size <= window.at+int64(len(window.data)) {
				child.window = window
			}
			if err := visit(reflect.ValueOf(i), child); err != nil {
				return err
			}
		}
		return nil
	}
	start := c.offset
	var headers []metadataLogicalChildHeader
	if reserveMetadataLogicalChildIndex(length) {
		headers = make([]metadataLogicalChildHeader, 0, length)
	}
	for i := 0; i < length; i++ {
		node, err := c.child(t)
		if err == nil && headers != nil {
			headers = append(headers, metadataLogicalHeaderOf(node))
		}
		if err == nil {
			err = visit(reflect.ValueOf(i), node)
		}
		if err != nil {
			if headers != nil {
				metadataLogicalIndexBytes.Add(-metadataLogicalChildIndexBytes(cap(headers)))
			}
			return err
		}
	}
	if headers != nil {
		storage.storeChildIndex(n.offset, n.size, start, headers)
	}
	return nil
}

func (n metadataLogicalNode) StringEqual(ctx context.Context, want string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if n.typ.Kind() != reflect.String {
		return false, nil
	}
	if n.generic {
		if err := n.source.Retain(); err != nil {
			return false, err
		}
		defer n.source.Release()
		return n.genericStringEqual(ctx, want)
	}
	if err := n.source.Retain(); err != nil {
		return false, err
	}
	defer n.source.Release()
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	length, err := c.uint()
	if err != nil || length != uint64(len(want)) {
		return false, err
	}
	var data [4096]byte
	for offset := 0; offset < len(want); offset += len(data) {
		size := min(len(data), len(want)-offset)
		if err := c.read(data[:size]); err != nil {
			return false, err
		}
		if string(data[:size]) != want[offset:offset+size] {
			return false, nil
		}
	}
	return true, nil
}

// MapEntries leaves keys in their framed representation, so an unrelated
// legal large property name can be skipped without allocating it.
func (n metadataLogicalNode) MapEntries(ctx context.Context, visit func(metadataLogicalNode, metadataLogicalNode) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n.blocked {
		return ErrBoundedMetadataCoverage
	}
	if n.typ.Kind() != reflect.Map {
		return ErrMetadataRecordQuery
	}
	if err := n.source.Retain(); err != nil {
		return err
	}
	defer n.source.Release()
	if n.generic {
		return n.genericMapEntries(ctx, visit)
	}
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	present, err := c.byte()
	if err != nil || present == 0 {
		return err
	}
	length, err := c.uint()
	if err != nil {
		return err
	}
	if length > n.cost {
		return ErrMetadataRecordQuery
	}
	for i := uint64(0); i < length; i++ {
		key, err := c.child(n.typ.Key())
		if err != nil {
			return err
		}
		value, err := c.child(n.typ.Elem())
		if err != nil {
			return err
		}
		if err := visit(key, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *metadataLogicalSource) Select(ctx context.Context, scope MetadataRecordScope, maxBytes int) (domain.Bootstrap, error) {
	return s.selectMetadata(ctx, scope, maxBytes, nil)
}

// selectMetadata can copy values from an exact immutable image while using
// the source's selection order and frame costs for admission.
func (s *metadataLogicalSource) selectMetadata(ctx context.Context, scope MetadataRecordScope, maxBytes int, image *coldDecodedImage) (domain.Bootstrap, error) {
	var data domain.Bootstrap
	result := reflect.ValueOf(&data).Elem()
	remaining := maxBytes
	valueOf := func(node metadataLogicalNode, decoded reflect.Value) (reflect.Value, error) {
		if decoded.IsValid() && !node.unavailable {
			if err := ctx.Err(); err != nil {
				return reflect.Value{}, err
			}
			if remaining < 0 || node.cost > uint64(remaining) {
				return reflect.Value{}, ErrMetadataWorkBudget
			}
			return exactCopyValue(decoded), nil
		}
		return node.Value(ctx, remaining)
	}
	err := s.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		core := name == "workspace" || name == "viewer" || name == "viewerRole"
		complete := slices.Contains(scope.Fields, name)
		index, _, _ := metadataLogicalField(name)
		var decoded reflect.Value
		if image != nil {
			decoded = image.fields[index].value
		}
		if core || complete {
			value, err := valueOf(node, decoded)
			if err != nil {
				return err
			}
			remaining -= int(node.cost)
			result.Field(index).Set(value)
			return nil
		}
		if !slices.Contains(metadataRecordFields(scope), name) {
			return nil
		}
		target := result.Field(index)
		switch node.Type().Kind() {
		case reflect.Slice:
			if node.typ.Elem().Kind() != reflect.Struct {
				return ErrMetadataRecordQuery
			}
			target.Set(reflect.MakeSlice(node.typ, 0, 0))
		case reflect.Map:
			target.Set(reflect.MakeMap(node.typ))
		case reflect.Struct:
			if name != "workspaceSettings" {
				return ErrMetadataRecordQuery
			}
		default:
			return ErrMetadataRecordQuery
		}
		seen := map[string]bool{}
		if node.typ.Kind() == reflect.Map {
			return node.MapEntries(ctx, func(key, item metadataLogicalNode) error {
				for _, selected := range append(slices.Clone(scope.Read), scope.Write...) {
					if selected.Kind != name {
						continue
					}
					equal, err := key.StringEqual(ctx, selected.ID)
					if err != nil {
						return err
					}
					if !equal || seen[selected.ID] {
						continue
					}
					owned, err := item.Value(ctx, remaining)
					if err != nil {
						return err
					}
					remaining -= int(item.cost)
					seen[selected.ID] = true
					target.SetMapIndex(reflect.ValueOf(selected.ID).Convert(node.typ.Key()), owned)
				}
				return nil
			})
		}
		var hinted map[int]bool
		if node.typ.Kind() == reflect.Slice {
			ids := []string{}
			for _, selected := range append(slices.Clone(scope.Read), scope.Write...) {
				if selected.Kind == name {
					ids = append(ids, selected.ID)
				}
			}
			hinted, _ = residentSelectedOrdinals(ctx, name, node, ids)
		}
		return node.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
			if hinted != nil && !hinted[int(key.Int())] {
				// The resident image of this frame holds none of the
				// selected IDs at this ordinal.
				return nil
			}
			id := ""
			matched := false
			if node.typ.Kind() == reflect.Map {
				if key.Kind() != reflect.String {
					return ErrMetadataRecordQuery
				}
				id = key.String()
				matched = true
			} else if node.typ.Kind() == reflect.Struct {
				f, _ := node.typ.FieldByName(key.String())
				id = strings.Split(f.Tag.Get("json"), ",")[0]
				matched = true
			} else {
				if err := item.Children(ctx, func(field reflect.Value, value metadataLogicalNode) error {
					if field.String() == "ID" {
						for _, selected := range append(slices.Clone(scope.Read), scope.Write...) {
							if selected.Kind != name {
								continue
							}
							equal, err := value.StringEqual(ctx, selected.ID)
							if err != nil {
								return err
							}
							if equal {
								id = selected.ID
								matched = true
								break
							}
						}
					}
					return nil
				}); err != nil {
					return err
				}
			}
			if !matched || !metadataRecordSelected(scope, name, id, false) {
				return nil
			}
			if seen[id] {
				return fmt.Errorf("%w: ambiguous logical record in %s", ErrMetadataRecordQuery, name)
			}
			seen[id] = true
			var record reflect.Value
			if hinted != nil && decoded.IsValid() && decoded.Kind() == reflect.Slice {
				record = decoded.Index(int(key.Int()))
			}
			owned, err := valueOf(item, record)
			if err != nil {
				return err
			}
			remaining -= int(item.cost)
			switch node.typ.Kind() {
			case reflect.Map:
				target.SetMapIndex(key, owned)
			case reflect.Struct:
				target.FieldByName(key.String()).Set(owned)
			default:
				target.Set(reflect.Append(target, owned))
			}
			return nil
		})
	})
	return data, err
}
