package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type metadataLogicalUnitPatch struct {
	key   string
	value metadataAggregateNode
	shape metadataLogicalNode
}

func metadataLogicalPatchUnitRecords(ctx context.Context, source *metadataLogicalSource, patches []metadataLogicalRecordPatch) (_ *metadataLogicalSource, err error) {
	if err := source.Retain(); err != nil {
		return nil, err
	}
	current := source
	defer func() {
		if current != nil {
			current.Release()
		}
	}()
	builder, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	defer builder.Close()
	if err := source.VisitFields(ctx, func(name string, _ metadataLogicalNode) error {
		index, _, _ := metadataLogicalField(name)
		_, kind, ok := metadataRecordType(name)
		if !ok {
			return nil
		}
		for _, patch := range patches {
			if patch.key.field != name {
				continue
			}
			node := current.fields[index]
			var value metadataAggregateNode
			if patch.raw != nil {
				old, err := metadataLogicalFirstRecord(ctx, metadataAggregateStoredNode{node}, patch.key.key)
				if err != nil {
					return err
				}
				value, err = metadataLogicalRetryRecord(ctx, builder, name, kind, patch.raw, old)
				if err != nil {
					return err
				}
			}
			shape := node
			if node.typ.Kind() == reflect.Map {
				shape, err = builder.writeValue(reflect.MakeMap(node.typ), 0, false)
				if err != nil {
					return err
				}
			}
			next, err := current.patchUnitRecord(ctx, index, metadataLogicalUnitPatch{patch.key.key, value, shape})
			if err != nil {
				return err
			}
			current.Release()
			current = next
		}
		return nil
	}); err != nil {
		return nil, err
	}
	result := current
	current = nil
	return result, nil
}

func metadataLogicalUnitPatches(value metadataAggregateNode, original metadataLogicalNode) ([]metadataLogicalUnitPatch, bool) {
	var reversed []metadataLogicalUnitPatch
	for {
		patch, ok := value.(metadataLogicalPatchedCollection)
		if !ok {
			break
		}
		if len(reversed) == MetadataRecordBatchMaxKeys {
			return nil, false
		}
		if patch.shape.typ != original.typ {
			return nil, false
		}
		reversed = append(reversed, metadataLogicalUnitPatch{patch.key, patch.value, patch.shape})
		value = patch.base
	}
	base, ok := value.(metadataAggregateStoredNode)
	if !ok || base.value.collection == nil || base.value.collection != original.collection {
		return nil, false
	}
	if original.typ.Kind() != reflect.Map && original.typ.Kind() != reflect.Slice {
		return nil, false
	}
	slices.Reverse(reversed)
	return reversed, len(reversed) != 0
}

func (s *metadataLogicalSource) patchUnitField(ctx context.Context, field int, patches []metadataLogicalUnitPatch) (*metadataLogicalSource, error) {
	if err := s.Retain(); err != nil {
		return nil, err
	}
	current := s
	defer func() {
		if current != nil {
			current.Release()
		}
	}()
	for _, patch := range patches {
		next, err := current.patchUnitRecord(ctx, field, patch)
		if err != nil {
			return nil, err
		}
		current.Release()
		current = next
	}
	result := current
	current = nil
	return result, nil
}

func (s *metadataLogicalSource) patchUnitRecord(ctx context.Context, field int, patch metadataLogicalUnitPatch) (_ *metadataLogicalSource, err error) {
	r, a := s.units, s.units.arena
	descriptor := r.fields[field]
	nilValue := descriptor.nilValue
	if descriptor.typ.Kind() == reflect.Map {
		nilValue, err = patch.shape.isNil(ctx)
		if err != nil {
			return nil, err
		}
	}
	name := jsonFieldName(reflect.TypeFor[domain.Bootstrap]().Field(field))
	var old metadataLogicalNode
	var ordinal uint64
	found := false
	if err := s.unitCorpus(ctx).pointRecords(ctx, name, patch.key, func(order uint64, node metadataLogicalNode) error {
		old, ordinal, found = node, order, true
		return sw29LogicalIndexStop
	}); err != nil {
		return nil, err
	}
	if !found {
		ordinal = descriptor.nextOrder
	}
	var frame metadataLogicalNode
	var leaf metadataLogicalUnitsLeaf
	if patch.value != nil {
		builder, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, s))
		if err != nil {
			return nil, err
		}
		defer builder.Close()
		if found {
			builder.storage.types, builder.storage.times = slices.Clone(old.source.types), slices.Clone(old.source.times)
			if err := builder.inheritLocations(old.source); err != nil {
				return nil, err
			}
		}
		if err := builder.inheritTimestampInputs(ctx); err != nil {
			return nil, err
		}
		if descriptor.typ.Kind() == reflect.Map {
			var keyNode metadataLogicalNode
			if found {
				index := &sw29LogicalIndex{stage: a.index.stage, seed: a.index.seed}
				raw, ok, err := index.Get(ctx, r.trees[0], metadataLogicalUnitsOrderKey(field, ordinal))
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, ErrMetadataRecordQuery
				}
				oldLeaf, err := metadataLogicalUnitsDecodeLeaf(raw)
				if err != nil {
					return nil, err
				}
				key, err := r.node(ctx, oldLeaf, descriptor.typ.Key(), true)
				if err != nil {
					return nil, err
				}
				keyNode, err = builder.importNode(key, 0)
				if err != nil {
					return nil, err
				}
			} else {
				keyNode, err = builder.writeValue(reflect.ValueOf(patch.key).Convert(descriptor.typ.Key()), 0, false)
				if err != nil {
					return nil, err
				}
			}
			leaf.keySize, leaf.keyCost = uint64(keyNode.size), keyNode.cost
		}
		written, err := builder.writeAggregateNode(patch.value, 0)
		if err != nil {
			return nil, err
		}
		if written.typ != descriptor.typ.Elem() {
			return nil, ErrMetadataRecordQuery
		}
		leaf.cost, leaf.unavailable, leaf.blocked = written.cost, written.unavailable, written.blocked
		frame = written
		if descriptor.typ.Kind() == reflect.Map {
			frame.offset, frame.size = 0, builder.offset
		}
		// Import while the staged builder and every retained native subtree
		// are live; no decoded-record or native location re-encoding occurs.
		return s.commitUnitPatch(ctx, field, ordinal, found, false, nilValue, frame, leaf)
	}
	return s.commitUnitPatch(ctx, field, ordinal, found, true, nilValue, frame, leaf)
}

func (s *metadataLogicalSource) commitUnitPatch(ctx context.Context, field int, ordinal uint64, found, deleted, nilValue bool, frame metadataLogicalNode, leaf metadataLogicalUnitsLeaf) (_ *metadataLogicalSource, err error) {
	a := s.units.arena
	root, err := a.change(ctx, s.units, s.Stamp(), func(tx *sql.Tx, trees *metadataLogicalUnitTrees, fields []metadataLogicalUnitsField) error {
		descriptor := &fields[field]
		var physical []byte
		if found {
			raw, ok, err := a.index.Get(ctx, trees[0], metadataLogicalUnitsOrderKey(field, ordinal))
			if err != nil {
				return err
			}
			if !ok {
				return ErrMetadataRecordQuery
			}
			old, err := metadataLogicalUnitsDecodeLeaf(raw)
			if err != nil {
				return err
			}
			physical, _, err = a.index.Get(ctx, trees[5], metadataLogicalUnitsPhysicalKey(old.unit))
			if err != nil {
				return err
			}
			if err := a.dropLeaf(ctx, tx, trees, field, ordinal, descriptor, old); err != nil {
				return err
			}
		}
		if deleted {
			descriptor.nilValue = nilValue && descriptor.count == 0
			return nil
		}
		leaf.unit, err = a.importLocked(ctx, tx, frame)
		if err != nil {
			return err
		}
		descriptor.count++
		descriptor.nextOrder = max(descriptor.nextOrder, ordinal+1)
		descriptor.cost += leaf.cost + leaf.keyCost + metadataLogicalUnitsElementOverhead(descriptor.typ)
		if leaf.unavailable {
			descriptor.unavailableUnits++
		}
		descriptor.unavailable = descriptor.unavailableUnits != 0
		descriptor.nilValue = false
		if err := a.addLeaf(ctx, tx, trees, field, ordinal, *descriptor, leaf); err != nil {
			return err
		}
		if physical != nil {
			if _, _, err := metadataLogicalUnitsDecodePhysical(physical); err != nil {
				return err
			}
			binary.BigEndian.PutUint64(physical[1:9], leaf.unit)
			trees[5], err = a.index.Put(ctx, trees[5], metadataLogicalUnitsPhysicalKey(leaf.unit), physical)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	source, err := root.source(ctx)
	if err != nil {
		root.Release()
	}
	return source, err
}
