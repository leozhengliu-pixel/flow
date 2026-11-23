package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"flow/api/internal/domain"
)

// metadataLogicalCopyBuffers are the buffers of large frame copies (1 MiB,
// pooled: a copy holds one only while it runs).
var metadataLogicalCopyBuffers = sync.Pool{New: func() any {
	buffer := make([]byte, 1<<20)
	return &buffer
}}

func (b *metadataLogicalBuilder) copyNode(node metadataLogicalNode) (metadataLogicalNode, error) {
	if b.storage.genericStore == nil {
		b.storage.genericStore = node.source.genericStore
	}
	if node.generic && node.genericRange {
		return b.writeGenericJSONPart(node.typ, node.genericPart(), node.cost)
	}
	start := b.offset
	buffer := b.buffer[:]
	if node.size > int64(len(buffer)) {
		// A large frame (a document body, a whole collection) is copied
		// in large reads: each read of the copy is one pread.
		pooled := metadataLogicalCopyBuffers.Get().(*[]byte)
		defer metadataLogicalCopyBuffers.Put(pooled)
		buffer = *pooled
	}
	count, err := io.CopyBuffer(metadataLogicalWriter{b}, io.NewSectionReader(node.source, node.offset, node.size), buffer)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if count != node.size {
		return metadataLogicalNode{}, io.ErrUnexpectedEOF
	}
	b.storage.recordCopy(start, metadataLogicalFrameOrigin(node))
	b.storage.copyChildIndex(node.source, node.offset, start, node.size)
	node.source, node.offset = b.storage, start
	return node, nil
}

// metadataLogicalCopyRun copies consecutive frames of one storage that a
// collection keeps verbatim (its unchanged records) as one range: a record
// by record copy of a large collection was one read and about one write per
// record, most of a project or document edit. Each frame of the run gets
// exactly what copyNode gives it (offset, origin, child index); the bytes are
// written when the run is flushed. The builder writes nothing else while a
// run is open: its owner flushes before any other write to the builder and
// when the collection's children end.
type metadataLogicalCopyRun struct {
	b        *metadataLogicalBuilder
	source   *metadataLogicalStorage
	from, to int64
	start    int64
	nodes    []metadataLogicalNode
}

// metadataLogicalCopyRunFrames counts frames copied through runs and the
// runs themselves (tests and load measurement).
var metadataLogicalCopyRunFrames, metadataLogicalCopyRuns atomic.Int64

// add takes node into the run and returns its copy as copyNode would return
// it. ok is false (nothing taken) for a frame copyNode does not copy byte for
// byte.
func (r *metadataLogicalCopyRun) add(node metadataLogicalNode) (_ metadataLogicalNode, ok bool, err error) {
	if node.generic && node.genericRange || node.source == nil || node.size <= 0 {
		return node, false, nil
	}
	if len(r.nodes) != 0 && (node.source != r.source || node.offset != r.to) {
		if err := r.flush(); err != nil {
			return node, false, err
		}
	}
	if len(r.nodes) == 0 {
		// A node may be visited from a storage that lives only while its
		// visit runs: the run keeps it until it is flushed.
		if err := node.source.Retain(); err != nil {
			return node, false, nil
		}
		r.source, r.from, r.to, r.start = node.source, node.offset, node.offset, r.b.offset
	}
	if r.b.storage.genericStore == nil {
		r.b.storage.genericStore = node.source.genericStore
	}
	r.nodes = append(r.nodes, node)
	r.to += node.size
	copied := node
	copied.source, copied.offset = r.b.storage, r.start+(node.offset-r.from)
	return copied, true, nil
}

// abandon drops an open run without writing it (its builder failed).
func (r *metadataLogicalCopyRun) abandon() {
	if r != nil && len(r.nodes) != 0 {
		r.source.Release()
		clear(r.nodes)
		r.nodes = r.nodes[:0]
	}
}

// flush writes the run's bytes and records each frame's origin and child
// index as copyNode does.
func (r *metadataLogicalCopyRun) flush() error {
	if r == nil || len(r.nodes) == 0 {
		return nil
	}
	defer func() {
		r.source.Release()
		clear(r.nodes)
		r.nodes = r.nodes[:0]
	}()
	if r.b.offset != r.start {
		// Something wrote to the builder while the run was open.
		return ErrMetadataRecordQuery
	}
	size := r.to - r.from
	// A large range of a settled storage is borrowed, not copied
	// (scale_p21_logical_borrowed_range.go).
	if borrowed, err := r.b.borrowRange(r.source, r.from, size); err != nil {
		return err
	} else if !borrowed {
		pooled := metadataLogicalCopyBuffers.Get().(*[]byte)
		defer metadataLogicalCopyBuffers.Put(pooled)
		count, err := io.CopyBuffer(metadataLogicalWriter{r.b}, io.NewSectionReader(r.source, r.from, size), *pooled)
		if err != nil {
			return err
		}
		if count != size {
			return io.ErrUnexpectedEOF
		}
	}
	for _, node := range r.nodes {
		at := r.start + (node.offset - r.from)
		r.b.storage.recordCopy(at, metadataLogicalFrameOrigin(node))
		r.b.storage.copyChildIndex(node.source, node.offset, at, node.size)
	}
	metadataLogicalCopyRuns.Add(1)
	metadataLogicalCopyRunFrames.Add(int64(len(r.nodes)))
	return nil
}

// Fork copies untouched typed frames without decoding them. Only selected
// replacements are encoded, preserving warm values and collection order.
func (s *metadataLogicalSource) Fork(ctx context.Context, before, next domain.Bootstrap, scope MetadataRecordScope, stamp copyStamp, suppliedOrders ...map[metadataRecordKey]int) (_ *metadataLogicalSource, err error) {
	if err := s.Retain(); err != nil {
		return nil, err
	}
	defer s.Release()
	b, err := newMetadataLogicalBuilderIn(ctx, s.spoolGroup())
	if err != nil {
		return nil, err
	}
	// Rewritten fields import their source storage registry lazily. An entire
	// unchanged field shares its existing owner instead.
	defer func() {
		if err != nil {
			b.source.Release()
		}
	}()
	old, changed := reflect.ValueOf(before), reflect.ValueOf(next)
	appendOrders, _ := ctx.Value(metadataAppendOrdersKey{}).(map[metadataRecordKey]int)
	if len(suppliedOrders) > 1 {
		return nil, ErrMetadataRecordQuery
	}
	if len(suppliedOrders) == 1 {
		appendOrders = suppliedOrders[0]
	}
	err = s.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		index, _, ok := metadataLogicalField(name)
		if !ok || name == "-" {
			return nil
		}
		value := changed.Field(index)
		complete := slices.Contains(scope.Fields, name) ||
			(name == "workspace" || name == "viewer" || name == "viewerRole") && !reflect.DeepEqual(old.Field(index).Interface(), value.Interface())
		writes := []RecordKey{}
		for _, key := range scope.Write {
			if key.Kind == name && !slices.Contains(writes, key) {
				writes = append(writes, key)
			}
		}
		if complete || len(writes) > 0 {
			// Each rewritten field has its own registry. Unchanged typed frames
			// can then be copied without remapping dynamic interface/time IDs.
			if err := b.startPart(ctx); err != nil {
				return err
			}
			b.storage.types, b.storage.times = slices.Clone(node.source.types), slices.Clone(node.source.times)
			if err := b.inheritLocations(node.source); err != nil {
				return err
			}
			if err := b.inheritTimestampInputs(ctx); err != nil {
				return err
			}
		}
		if complete {
			return b.fieldAvailableNative(name, value)
		}
		if len(writes) == 0 {
			if !slices.Contains(b.source.stores, node.source) {
				if err := node.source.Retain(); err != nil {
					return err
				}
				b.source.stores = append(b.source.stores, node.source)
			}
			b.source.fields[index] = node
			return nil
		}
		if node.typ.Kind() == reflect.Struct && name == "workspaceSettings" {
			source, err := node.Value(ctx, 2<<20)
			if err != nil {
				return err
			}
			for _, key := range writes {
				for i := 0; i < source.NumField(); i++ {
					if jsonFieldName(source.Type().Field(i)) == key.ID {
						source.Field(i).Set(value.Field(i))
					}
				}
			}
			return b.fieldAvailableNative(name, source)
		}
		if node.typ.Kind() != reflect.Map && node.typ.Kind() != reflect.Slice {
			return ErrMetadataRecordQuery
		}
		if node.typ.Kind() == reflect.Map {
			return b.forkMap(node, name, value, writes)
		}
		nilValue, err := node.isNil(ctx)
		if err != nil {
			return err
		}
		if err := b.BeginCollection(name, nilValue); err != nil {
			return err
		}
		seen := map[string]bool{}
		ids := make([]string, 0, len(writes))
		for _, write := range writes {
			ids = append(ids, write.ID)
		}
		hinted, _ := residentSelectedOrdinals(ctx, name, node, ids)
		run := &metadataLogicalCopyRun{b: b}
		defer run.abandon()
		if slices.Contains(metadataAppendPrependKinds(ctx), name) {
			var prepended []reflect.Value
			for i := 0; i < value.Len(); i++ {
				id, ok := metadataRecordValueKey(value.Index(i))
				if !ok {
					continue
				}
				if _, allowed := appendOrders[metadataRecordKey{field: name, key: id}]; !allowed {
					continue
				}
				prepended = append(prepended, value.Index(i))
			}
			slices.SortStableFunc(prepended, func(a, b reflect.Value) int {
				aID, _ := metadataRecordValueKey(a)
				bID, _ := metadataRecordValueKey(b)
				return appendOrders[metadataRecordKey{field: name, key: aID}] -
					appendOrders[metadataRecordKey{field: name, key: bID}]
			})
			for _, item := range prepended {
				id, _ := metadataRecordValueKey(item)
				if seen[id] {
					return fmt.Errorf("%w: duplicate logical append key in %s", ErrMetadataRecordQuery, name)
				}
				seen[id] = true
				if err := b.appendAvailableNative(reflect.Value{}, item); err != nil {
					return err
				}
			}
		}
		err = node.Children(ctx, func(ordinal reflect.Value, item metadataLogicalNode) error {
			id := ""
			selected := false
			if hinted != nil && !hinted[int(ordinal.Int())] {
				// The resident image of this frame holds no written ID at
				// this ordinal: copied without reading its ID.
			} else if err := item.Children(ctx, func(field reflect.Value, child metadataLogicalNode) error {
				if field.String() != "ID" {
					return nil
				}
				for _, write := range writes {
					equal, err := child.StringEqual(ctx, write.ID)
					if err != nil {
						return err
					}
					if equal {
						id, selected = write.ID, true
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if selected {
				if seen[id] {
					return fmt.Errorf("%w: duplicate logical write key in %s", ErrMetadataRecordQuery, name)
				}
				seen[id] = true
				if err := run.flush(); err != nil {
					return err
				}
				found := false
				for i := 0; i < value.Len(); i++ {
					candidateID, ok := metadataRecordValueKey(value.Index(i))
					if ok && candidateID == id {
						if found {
							return ErrMetadataRecordQuery
						}
						found = true
						if err := b.appendAvailableNative(reflect.Value{}, value.Index(i)); err != nil {
							return err
						}
					}
				}
				return nil
			}
			copy, batched, err := run.add(item)
			if err == nil && !batched {
				if err = run.flush(); err == nil {
					copy, err = b.copyNode(item)
				}
			}
			if err == nil {
				b.collectionIndex.add(copy)
			}
			b.cost += copy.cost + uint64(item.typ.Size())*2
			b.unavailable = b.unavailable || copy.unavailable
			b.count++
			return err
		})
		if err == nil {
			err = run.flush()
		}
		if err != nil {
			return err
		}
		appended := []reflect.Value{}
		appendedIDs := map[string]bool{}
		for i := 0; i < value.Len(); i++ {
			id, ok := metadataRecordValueKey(value.Index(i))
			if appendedIDs[id] {
				return fmt.Errorf("%w: duplicate logical append key in %s", ErrMetadataRecordQuery, name)
			}
			if !ok || seen[id] || !slices.Contains(writes, RecordKey{Kind: name, ID: id}) {
				continue
			}
			_, allowed := appendOrders[metadataRecordKey{field: name, key: id}]
			actor, hasActor := actorFromContext(ctx)
			if !allowed && !(name == "users" && hasActor && actor.ID == id) {
				return fmt.Errorf("%w: logical array creation requires a complete field or declared append", ErrMetadataRecordQuery)
			}
			seen[id] = true
			appendedIDs[id] = true
			appended = append(appended, value.Index(i))
		}
		slices.SortStableFunc(appended, func(a, b reflect.Value) int {
			aID, _ := metadataRecordValueKey(a)
			bID, _ := metadataRecordValueKey(b)
			aOrder := appendOrders[metadataRecordKey{field: name, key: aID}]
			bOrder := appendOrders[metadataRecordKey{field: name, key: bID}]
			if aOrder < bOrder {
				return -1
			}
			if aOrder > bOrder {
				return 1
			}
			return 0
		})
		for _, value := range appended {
			if err := b.appendAvailableNative(reflect.Value{}, value); err != nil {
				return err
			}
		}
		return b.EndCollection()
	})
	if err != nil {
		return nil, err
	}
	return b.Finish(stamp)
}

func (b *metadataLogicalBuilder) forkMap(node metadataLogicalNode, name string, next reflect.Value, writes []RecordKey) error {
	nilValue, err := node.isNil(b.ctx)
	if err != nil {
		return err
	}
	if err := b.BeginCollection(name, nilValue); err != nil {
		return err
	}
	seen := map[string]bool{}
	err = node.MapEntries(b.ctx, func(key, item metadataLogicalNode) error {
		for _, write := range writes {
			equal, err := key.StringEqual(b.ctx, write.ID)
			if err != nil {
				return err
			}
			if equal {
				seen[write.ID] = true
				k := reflect.ValueOf(write.ID).Convert(node.typ.Key())
				if replacement := next.MapIndex(k); replacement.IsValid() {
					return b.appendAvailableNative(k, replacement)
				}
				return nil
			}
		}
		copiedKey, err := b.copyNode(key)
		if err != nil {
			return err
		}
		copiedValue, err := b.copyNode(item)
		b.cost += copiedKey.cost + copiedValue.cost + uint64(item.typ.Size())*2 + 128
		b.unavailable = b.unavailable || copiedKey.unavailable || copiedValue.unavailable
		b.count++
		return err
	})
	if err != nil {
		return err
	}
	for _, write := range writes {
		if seen[write.ID] {
			continue
		}
		key := reflect.ValueOf(write.ID).Convert(node.typ.Key())
		if replacement := next.MapIndex(key); replacement.IsValid() {
			if err := b.appendAvailableNative(key, replacement); err != nil {
				return err
			}
		}
	}
	return b.EndCollection()
}

func jsonFieldName(field reflect.StructField) string {
	name := field.Tag.Get("json")
	for i := range name {
		if name[i] == ',' {
			return name[:i]
		}
	}
	return name
}

// New callback values must be readable before their transaction may commit.
// Unavailable untouched siblings remain copied opaque frames.
func (b *metadataLogicalBuilder) fieldAvailableNative(name string, value reflect.Value) error {
	if err := b.Field(name, value); err != nil {
		return err
	}
	index, _, _ := metadataLogicalField(name)
	if b.source.fields[index].unavailable {
		return ErrBoundedMetadataCoverage
	}
	return nil
}

func (b *metadataLogicalBuilder) appendAvailableNative(key, value reflect.Value) error {
	prior := b.unavailable
	b.unavailable = false
	err := b.Append(key, value)
	unavailable := b.unavailable
	b.unavailable = prior || unavailable
	if err == nil && unavailable {
		return ErrBoundedMetadataCoverage
	}
	return err
}

func (n metadataLogicalNode) isNil(ctx context.Context) (bool, error) {
	if n.typ.Kind() != reflect.Slice && n.typ.Kind() != reflect.Map && n.typ.Kind() != reflect.Pointer && n.typ.Kind() != reflect.Interface {
		return false, nil
	}
	if n.generic {
		prefix, err := metadataGenericJSONPrefix(ctx, n.genericPart())
		return prefix == 'n', err
	}
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	present, err := c.byte()
	return present == 0, err
}

// setStamp is only legal on an unpublished candidate owned by its writer.
func (s *metadataLogicalSource) setStamp(stamp copyStamp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs != 1 {
		return ErrMetadataRecordQuery
	}
	s.stamp = stamp
	return nil
}

// StreamBytes borrows the complete bytes of a typed string or byte slice. The
// reader remains valid only until visit returns and honors cancellation.
func (n metadataLogicalNode) StreamBytes(ctx context.Context, visit func(io.Reader) error) error {
	if err := n.source.Retain(); err != nil {
		return err
	}
	defer n.source.Release()
	if n.generic {
		return n.genericStreamBytes(ctx, visit)
	}
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	if n.typ.Kind() == reflect.Slice && n.typ.Elem().Kind() == reflect.Uint8 {
		present, err := c.byte()
		if err != nil {
			return err
		}
		if present == 0 {
			return visit(io.LimitReader(&metadataLogicalByteReader{ctx, n.source, c.offset}, 0))
		}
	} else if n.typ.Kind() != reflect.String {
		return ErrMetadataRecordQuery
	}
	size, err := c.uint()
	if err != nil || size > uint64(n.size) {
		return io.ErrUnexpectedEOF
	}
	// A node read in a window streams its bytes from it. The reader stays a
	// LimitedReader over the declared size, as from the source below: codec
	// validation tells truncated JSON (the declared bytes exhausted) from a
	// short backing read by its remaining count.
	if window := c.node.window; window != nil && window.storage == n.source && c.offset >= window.at && c.offset+int64(size) <= window.at+int64(len(window.data)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return visit(io.LimitReader(bytes.NewReader(window.data[c.offset-window.at:c.offset-window.at+int64(size)]), int64(size)))
	}
	return visit(io.LimitReader(&metadataLogicalByteReader{ctx, n.source, c.offset}, int64(size)))
}

type metadataLogicalByteReader struct {
	ctx    context.Context
	source io.ReaderAt
	offset int64
}

func (r *metadataLogicalByteReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := r.source.ReadAt(p, r.offset)
	r.offset += int64(count)
	return count, err
}
