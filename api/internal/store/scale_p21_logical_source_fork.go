package store

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

func (b *metadataLogicalBuilder) copyNode(node metadataLogicalNode) (metadataLogicalNode, error) {
	start := b.offset
	count, err := io.CopyBuffer(metadataLogicalWriter{b}, io.NewSectionReader(node.source.file, node.offset, node.size), b.buffer[:])
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if count != node.size {
		return metadataLogicalNode{}, io.ErrUnexpectedEOF
	}
	node.source, node.offset = b.storage, start
	return node, nil
}

// Fork copies untouched typed frames without decoding them. Only selected
// replacements are encoded, preserving warm values and collection order.
func (s *metadataLogicalSource) Fork(ctx context.Context, before, next domain.Bootstrap, scope MetadataRecordScope, stamp copyStamp, suppliedOrders ...map[metadataRecordKey]int) (_ *metadataLogicalSource, err error) {
	if err := s.Retain(); err != nil {
		return nil, err
	}
	defer s.Release()
	b, err := newMetadataLogicalBuilder(ctx)
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
			part, err := newMetadataLogicalBuilder(ctx)
			if err != nil {
				return err
			}
			b.source.stores = append(b.source.stores, part.storage)
			b.storage, b.offset = part.storage, 0
			part.source.stores = nil
			part.source.Release()
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
		err = node.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			id := ""
			selected := false
			if err := item.Children(ctx, func(field reflect.Value, child metadataLogicalNode) error {
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
			copy, err := b.copyNode(item)
			b.cost += copy.cost + uint64(item.typ.Size())*2
			b.unavailable = b.unavailable || copy.unavailable
			b.count++
			return err
		})
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
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	if n.typ.Kind() == reflect.Slice && n.typ.Elem().Kind() == reflect.Uint8 {
		present, err := c.byte()
		if err != nil {
			return err
		}
		if present == 0 {
			return visit(io.LimitReader(&metadataLogicalByteReader{ctx, n.source.file, c.offset}, 0))
		}
	} else if n.typ.Kind() != reflect.String {
		return ErrMetadataRecordQuery
	}
	size, err := c.uint()
	if err != nil || size > uint64(n.size) {
		return io.ErrUnexpectedEOF
	}
	return visit(io.LimitReader(&metadataLogicalByteReader{ctx, n.source.file, c.offset}, int64(size)))
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
