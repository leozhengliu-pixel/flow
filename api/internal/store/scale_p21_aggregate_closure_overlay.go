package store

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"slices"
	"strings"
)

type metadataAggregateOverlayNode struct {
	base            metadataAggregateNode
	selected        reflect.Value
	field           string
	scope           MetadataRecordScope
	pointOccurrence func(field, id string, ordinal uint64) bool
}

func (n metadataAggregateOverlayNode) nodeType() reflect.Type { return n.base.nodeType() }

func (n metadataAggregateOverlayNode) selectedRecord(id string) (reflect.Value, bool) {
	if n.selected.Kind() == reflect.Map {
		value := n.selected.MapIndex(reflect.ValueOf(id).Convert(n.selected.Type().Key()))
		return value, value.IsValid()
	}
	for i := 0; i < n.selected.Len(); i++ {
		value := n.selected.Index(i)
		key, ok := metadataRecordValueKey(value)
		if ok && key == id {
			return value, true
		}
	}
	return reflect.Value{}, false
}

func (n metadataAggregateOverlayNode) mapValue(ctx context.Context, id string) (metadataAggregateNode, error) {
	if metadataRecordSelected(n.scope, n.field, id, false) {
		if value, ok := n.selectedRecord(id); ok {
			return metadataAggregateNativeNode{value}, nil
		}
		return nil, nil
	}
	if lazy, ok := n.base.(metadataAggregateMapNode); ok {
		return lazy.mapValue(ctx, id)
	}
	return metadataAggregateChild(ctx, n.base, id)
}

func (n metadataAggregateOverlayNode) mapValues(ctx context.Context, visit func(metadataAggregateNode) error) error {
	stored, ok := n.base.(metadataAggregateStoredNode)
	if !ok {
		return n.nodeChildren(ctx, func(_ reflect.Value, child metadataAggregateNode) error { return visit(child) })
	}
	seen := map[string]bool{}
	err := stored.value.MapEntries(ctx, func(key, value metadataLogicalNode) error {
		for _, point := range append(slices.Clone(n.scope.Read), n.scope.Write...) {
			if point.Kind != n.field {
				continue
			}
			match, err := key.StringEqual(ctx, point.ID)
			if err != nil {
				return err
			}
			if !match {
				continue
			}
			seen[point.ID] = true
			if replacement, exists := n.selectedRecord(point.ID); exists {
				return visit(metadataAggregateNativeNode{replacement})
			}
			return nil
		}
		return visit(metadataAggregateStoredNode{value})
	})
	if err != nil {
		return err
	}
	iter := n.selected.MapRange()
	for iter.Next() {
		id := iter.Key().String()
		if !seen[id] && metadataRecordSelected(n.scope, n.field, id, false) {
			if err := visit(metadataAggregateNativeNode{iter.Value()}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n metadataAggregateOverlayNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	if n.selected.Kind() == reflect.Struct && n.field == "workspaceSettings" {
		return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
			field, ok := n.selected.Type().FieldByName(key.String())
			if ok {
				name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if metadataRecordSelected(n.scope, n.field, name, false) {
					child = metadataAggregateNativeNode{n.selected.FieldByIndex(field.Index)}
				}
			}
			return visit(key, child)
		})
	}
	seen := map[string]bool{}
	nextOrdinal := 0
	err := n.baseChildren(ctx, func(key reflect.Value, child metadataAggregateNode, ordinal uint64) error {
		var id string
		matched := false
		if n.selected.Kind() == reflect.Map {
			id = key.String()
			matched = metadataRecordSelected(n.scope, n.field, id, false)
		} else {
			nextOrdinal++
			field, ok := child.nodeType().FieldByName("ID")
			if !ok {
				return visit(key, child)
			}
			idNode, err := metadataAggregateIDNode(ctx, child, field.Index)
			if err != nil {
				return err
			}
			// At most the declared point keys can replace an array element;
			// compare them without retaining IDs from unrelated source rows.
			for _, point := range append(slices.Clone(n.scope.Read), n.scope.Write...) {
				if point.Kind != n.field {
					continue
				}
				match, err := metadataAggregateStringEqual(ctx, idNode, point.ID)
				if err != nil {
					return err
				}
				if match {
					id = point.ID
					matched = true
					break
				}
			}
		}
		if !matched {
			return visit(key, child)
		}
		seen[id] = true
		if n.selected.Kind() == reflect.Slice && n.pointOccurrence != nil && !n.pointOccurrence(n.field, id, ordinal) {
			return visit(key, child)
		}
		value, exists := n.selectedRecord(id)
		if !exists {
			// An absent selected write is a tombstone, including deletion of
			// the last point; it never means an empty complete collection.
			return nil
		}
		return visit(key, metadataAggregateNativeNode{value})
	})
	if err != nil {
		return err
	}
	appendRecord := func(id string, key reflect.Value, value reflect.Value) error {
		if seen[id] || !metadataRecordSelected(n.scope, n.field, id, false) {
			return nil
		}
		seen[id] = true
		return visit(key, metadataAggregateNativeNode{value})
	}
	if n.selected.Kind() == reflect.Map {
		iter := n.selected.MapRange()
		for iter.Next() {
			if err := appendRecord(iter.Key().String(), iter.Key(), iter.Value()); err != nil {
				return err
			}
		}
	} else {
		for i := 0; i < n.selected.Len(); i++ {
			value := n.selected.Index(i)
			id, ok := metadataRecordValueKey(value)
			if !ok {
				continue
			}
			if err := appendRecord(id, reflect.ValueOf(nextOrdinal), value); err != nil {
				return err
			}
			nextOrdinal++
		}
	}
	return nil
}

func (n metadataAggregateOverlayNode) baseChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode, uint64) error) error {
	stored, ok := n.base.(metadataAggregateStoredNode)
	if n.pointOccurrence == nil || n.selected.Kind() != reflect.Slice || !ok || stored.value.collection == nil {
		return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
			var ordinal uint64
			if key.IsValid() && key.Kind() == reflect.Int {
				ordinal = uint64(key.Int())
			}
			return visit(key, child, ordinal)
		})
	}
	collection := stored.value.collection
	prefix := metadataLogicalUnitsOrderKey(collection.field, 0)[:2]
	index := 0
	return collection.root.Range(ctx, 0, prefix, sw29LogicalPrefixEnd(prefix), func(key, raw []byte) error {
		leaf, err := metadataLogicalUnitsDecodeLeaf(raw)
		if err != nil {
			return err
		}
		child, err := collection.root.node(ctx, leaf, collection.typ.Elem(), false)
		if err != nil {
			return err
		}
		dense := index
		index++
		return visit(reflect.ValueOf(dense), metadataAggregateStoredNode{child}, binary.BigEndian.Uint64(key[2:]))
	})
}

func (n metadataAggregateOverlayNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	// Whole-container matches are possible for string-key maps carrying an
	// "id" member. Materialize only that winning container, under the same
	// caller-owned size admission as every other winning subtree.
	base, err := n.base.nodeValue(ctx, limit)
	if err != nil {
		return reflect.Value{}, err
	}
	// The stored source decoder returned a fresh owned container; changing
	// its selected entries cannot change the immutable source generation.
	value := base
	if value.Kind() == reflect.Struct {
		for i := 0; i < value.NumField(); i++ {
			name, _, _ := strings.Cut(value.Type().Field(i).Tag.Get("json"), ",")
			if metadataRecordSelected(n.scope, n.field, name, false) {
				value.Field(i).Set(n.selected.Field(i))
			}
		}
		return value, nil
	}
	if value.Kind() != reflect.Map {
		return reflect.Value{}, ErrMetadataRecordQuery
	}
	if value.IsNil() && !n.selected.IsNil() {
		value = reflect.MakeMap(value.Type())
	}
	for _, point := range append(slices.Clone(n.scope.Read), n.scope.Write...) {
		if point.Kind != n.field {
			continue
		}
		key := reflect.ValueOf(point.ID).Convert(value.Type().Key())
		value.SetMapIndex(key, n.selected.MapIndex(key))
	}
	return value, nil
}

// compactImportInputs removes uploaded input payloads before both lookup and
// encoding. These wrappers apply that same transformation lazily to each job.
type metadataAggregateCompactCollection struct {
	base  metadataAggregateNode
	field string
}

func (n metadataAggregateCompactCollection) nodeType() reflect.Type { return n.base.nodeType() }
func (n metadataAggregateCompactCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return n.base.nodeValue(ctx, limit)
}
func (n metadataAggregateCompactCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		field := "Rows"
		if n.field == "migrationJobs" {
			field = "Bundle"
		}
		return visit(key, metadataAggregateCompactNode{base: child, excluded: field})
	})
}

type metadataAggregateCompactNode struct {
	base     metadataAggregateNode
	excluded string
}

func (n metadataAggregateCompactNode) nodeType() reflect.Type { return n.base.nodeType() }
func (n metadataAggregateCompactNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		if key.IsValid() && key.Kind() == reflect.String && key.String() == n.excluded {
			return nil
		}
		return visit(key, child)
	})
}
func (n metadataAggregateCompactNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	kind := n.base.nodeType()
	if field, ok := kind.FieldByName(n.excluded); !ok || field.PkgPath != "" {
		return reflect.Value{}, errors.New("metadata aggregate input compaction type mismatch")
	}
	if native, ok := n.base.(metadataAggregateNativeNode); ok {
		cost, err := metadataAggregateNativeStructCost(ctx, native.value, n.excluded, 0)
		if err != nil {
			return reflect.Value{}, err
		}
		if limit < 0 || cost > uint64(limit) {
			return reflect.Value{}, ErrMetadataWorkBudget
		}
		value := reflect.New(kind).Elem()
		value.Set(native.value)
		value.FieldByName(n.excluded).SetZero()
		return value, ctx.Err()
	}
	if stored, ok := n.base.(metadataAggregateStoredNode); ok {
		excluded, err := metadataAggregateChild(ctx, n.base, n.excluded)
		if err != nil {
			return reflect.Value{}, err
		}
		cost := stored.value.cost
		if child, ok := excluded.(metadataAggregateStoredNode); ok {
			cost -= child.value.cost
		}
		if limit < 0 || cost > uint64(limit) {
			return reflect.Value{}, ErrMetadataWorkBudget
		}
	}
	value := reflect.New(kind).Elem()
	err := n.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		part, err := child.nodeValue(ctx, limit)
		if err != nil {
			return err
		}
		value.FieldByName(key.String()).Set(part)
		return nil
	})
	return value, err
}
