package store

import (
	"context"
	"encoding/binary"
	"reflect"
	"slices"
	"time"
)

type metadataLogicalTransformBase interface {
	logicalBase() metadataLogicalNode
}

type metadataLogicalTransformMap interface {
	logicalMapEntries(context.Context, func(metadataAggregateNode, metadataAggregateNode) error) error
}

// Transform preserves unchanged field owners and streams rewritten frame
// paths. Native replacement values are encoded through the same typed codec.
func (s *metadataLogicalSource) Transform(ctx context.Context, transform func(string, metadataLogicalNode) (metadataAggregateNode, bool, error)) (_ *metadataLogicalSource, err error) {
	if err := s.Retain(); err != nil {
		return nil, err
	}
	defer s.Release()
	if s.units != nil {
		return s.transformUnits(ctx, transform)
	}
	result, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			result.Close()
		}
	}()
	err = s.VisitFields(ctx, func(name string, original metadataLogicalNode) error {
		if name == "-" {
			return nil
		}
		index, _, ok := metadataLogicalField(name)
		if !ok {
			return ErrMetadataRecordQuery
		}
		node, changed, err := transform(name, original)
		if err != nil {
			return err
		}
		if !changed {
			if !slices.Contains(result.source.stores, original.source) {
				if err := original.source.Retain(); err != nil {
					return err
				}
				result.source.stores = append(result.source.stores, original.source)
			}
			result.source.fields[index] = original
			return nil
		}
		part, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return err
		}
		defer part.Close()
		part.storage.types = slices.Clone(original.source.types)
		part.storage.times = slices.Clone(original.source.times)
		if err := part.inheritLocations(original.source); err != nil {
			return err
		}
		written, err := part.writeAggregateNode(node, 0)
		if err != nil {
			return err
		}
		if written.typ != original.typ {
			return ErrMetadataRecordQuery
		}
		result.source.fields[index] = written
		result.source.stores = append(result.source.stores, part.storage)
		part.source.stores = nil
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result.Finish(s.Stamp())
}

func (b *metadataLogicalBuilder) writeAggregateNode(value metadataAggregateNode, depth int) (node metadataLogicalNode, err error) {
	if depth >= metadataLogicalDepth || value == nil {
		return node, ErrMetadataWorkBudget
	}
	if stored, ok := value.(metadataAggregateStoredNode); ok {
		// Transform starts with this field's registry. A foreign stored node
		// (for example a directory User) is encoded as an owned replacement.
		if metadataLogicalRegistryContains(b.storage, stored.value.source) {
			return b.copyNode(stored.value)
		}
		return b.importNode(stored.value, depth)
	}
	if native, ok := value.(metadataAggregateNativeNode); ok {
		return b.writeValue(native.value, depth, false)
	}
	t := value.nodeType()
	if t.Kind() == reflect.Pointer {
		node = metadataLogicalNode{source: b.storage, offset: b.offset, cost: uint64(t.Size())*2 + 128, typ: t}
		if err := b.write(make([]byte, 16)); err != nil {
			return node, err
		}
		seen := false
		err = value.nodeChildren(b.ctx, func(_ reflect.Value, child metadataAggregateNode) error {
			if seen || child.nodeType() != t.Elem() {
				return ErrMetadataRecordQuery
			}
			seen = true
			if err := b.write([]byte{1}); err != nil {
				return err
			}
			written, err := b.writeAggregateNode(child, depth+1)
			node.cost += written.cost
			node.unavailable = written.unavailable
			return err
		})
		if err != nil {
			return node, err
		}
		if !seen {
			if err := b.write([]byte{0}); err != nil {
				return node, err
			}
		}
		node.size = b.offset - node.offset
		return node, b.patch(node)
	}
	if t.Kind() != reflect.Struct && t.Kind() != reflect.Slice && t.Kind() != reflect.Map {
		owned, err := value.nodeValue(b.ctx, 2<<20)
		if err != nil {
			return node, err
		}
		return b.writeValue(owned, depth, false)
	}
	node = metadataLogicalNode{source: b.storage, offset: b.offset, cost: uint64(t.Size())*2 + 128, typ: t}
	if err := b.write(make([]byte, 16)); err != nil {
		return node, err
	}
	defer func() {
		if err == nil {
			node.size = b.offset - node.offset
			err = b.patch(node)
		}
	}()
	count, countOffset := uint64(0), int64(0)
	if t.Kind() == reflect.Map || t.Kind() == reflect.Slice {
		base, ok := value.(metadataLogicalTransformBase)
		if !ok {
			return node, ErrMetadataRecordQuery
		}
		nilValue, err := base.logicalBase().isNil(b.ctx)
		if err != nil {
			return node, err
		}
		flag := byte(1)
		if nilValue {
			flag = 0
		}
		if err := b.write([]byte{flag}); err != nil {
			return node, err
		}
		countOffset = b.offset
		if err := b.writeUint(0); err != nil {
			return node, err
		}
	}
	child := func(child metadataAggregateNode) error {
		written, err := b.writeAggregateNode(child, depth+1)
		node.cost += written.cost
		node.unavailable = node.unavailable || written.unavailable
		return err
	}
	if t.Kind() == reflect.Map {
		entries, ok := value.(metadataLogicalTransformMap)
		if !ok {
			return node, ErrMetadataRecordQuery
		}
		err = entries.logicalMapEntries(b.ctx, func(key, item metadataAggregateNode) error {
			if err := child(key); err != nil {
				return err
			}
			node.cost += uint64(t.Elem().Size())*2 + 128
			count++
			return child(item)
		})
	} else {
		err = value.nodeChildren(b.ctx, func(key reflect.Value, item metadataAggregateNode) error {
			if t.Kind() == reflect.Struct {
				if count >= uint64(t.NumField()) || !key.IsValid() || key.String() != t.Field(int(count)).Name || item.nodeType() != t.Field(int(count)).Type {
					return ErrMetadataRecordQuery
				}
			} else {
				node.cost += uint64(t.Elem().Size())*2 + 64
			}
			count++
			return child(item)
		})
	}
	if err != nil {
		return node, err
	}
	if t.Kind() == reflect.Struct {
		if count != uint64(t.NumField()) {
			return node, ErrMetadataRecordQuery
		}
	} else {
		var data [8]byte
		binary.LittleEndian.PutUint64(data[:], count)
		if _, err := b.storage.file.WriteAt(data[:], countOffset); err != nil {
			return node, err
		}
		if count > 0 {
			if _, err := b.storage.file.WriteAt([]byte{1}, countOffset-1); err != nil {
				return node, err
			}
		}
	}
	return node, nil
}

// Reframe foreign interface/time registry references while copying ordinary
// scalar/string bodies directly. A legal large User name never needs decoding
// merely because its timestamp came from a different field storage owner.
func (b *metadataLogicalBuilder) importNode(original metadataLogicalNode, depth int) (node metadataLogicalNode, err error) {
	if depth >= metadataLogicalDepth {
		return node, ErrMetadataWorkBudget
	}
	t := original.typ
	if original.blocked {
		return b.copyNode(original)
	}
	if t == reflect.TypeFor[time.Time]() {
		if err := b.inheritLocations(original.source); err != nil {
			return node, err
		}
		value, err := original.Value(b.ctx, 4096)
		if err != nil {
			return node, err
		}
		return b.writeValue(value, depth, false)
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Array, reflect.Pointer, reflect.Interface, reflect.Map:
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return b.copyNode(original)
		}
	default:
		return b.copyNode(original)
	}
	node = metadataLogicalNode{source: b.storage, offset: b.offset, cost: uint64(t.Size())*2 + 128, typ: t}
	if err := b.write(make([]byte, 16)); err != nil {
		return node, err
	}
	defer func() {
		if err == nil {
			node.size = b.offset - node.offset
			err = b.patch(node)
		}
	}()
	cursor := metadataLogicalCursor{original, original.offset + 16, b.ctx}
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		flag, err := cursor.byte()
		if err != nil {
			return node, err
		}
		if err := b.write([]byte{flag}); err != nil || flag == 0 {
			return node, err
		}
		if t.Kind() == reflect.Interface {
			index, err := cursor.uint()
			if err != nil || index >= uint64(len(original.source.types)) {
				return node, ErrMetadataRecordQuery
			}
			dynamic := original.source.types[index]
			newIndex := slices.Index(b.storage.types, dynamic)
			if newIndex < 0 {
				if len(b.storage.types) >= metadataLogicalTypes {
					return node, ErrMetadataWorkBudget
				}
				if err := validateMetadataLogicalTypes(append(slices.Clone(b.storage.types), dynamic)); err != nil {
					return node, err
				}
				newIndex = len(b.storage.types)
				b.storage.types = append(b.storage.types, dynamic)
			}
			if err := b.writeUint(uint64(newIndex)); err != nil {
				return node, err
			}
		} else if t.Kind() == reflect.Map || t.Kind() == reflect.Slice {
			length, err := cursor.uint()
			if err != nil || length > original.cost {
				return node, ErrMetadataRecordQuery
			}
			if err := b.writeUint(length); err != nil {
				return node, err
			}
		}
	}
	child := func(value metadataLogicalNode) error {
		written, err := b.importNode(value, depth+1)
		node.cost += written.cost
		node.unavailable = node.unavailable || written.unavailable
		return err
	}
	if t.Kind() == reflect.Map {
		err = original.MapEntries(b.ctx, func(key, value metadataLogicalNode) error {
			if err := child(key); err != nil {
				return err
			}
			node.cost += uint64(t.Elem().Size())*2 + 128
			return child(value)
		})
	} else {
		err = original.Children(b.ctx, func(_ reflect.Value, value metadataLogicalNode) error {
			if t.Kind() == reflect.Slice {
				node.cost += uint64(t.Elem().Size())*2 + 64
			}
			return child(value)
		})
	}
	return node, err
}

func metadataLogicalRegistryContains(target, source *metadataLogicalStorage) bool {
	return len(target.types) >= len(source.types) && len(target.times) >= len(source.times) && len(target.locations) >= len(source.locations) &&
		slices.Equal(target.types[:len(source.types)], source.types) &&
		slices.Equal(target.times[:len(source.times)], source.times) &&
		slices.Equal(target.locations[:len(source.locations)], source.locations)
}
