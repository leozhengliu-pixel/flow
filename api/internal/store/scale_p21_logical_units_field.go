package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"reflect"

	"flow/api/internal/domain"
)

func (a *metadataLogicalUnitsArena) replaceField(ctx context.Context, tx *sql.Tx, trees *metadataLogicalUnitTrees, field int, descriptor *metadataLogicalUnitsField, value reflect.Value) error {
	if descriptor.typ.Kind() != reflect.Struct && descriptor.count > MetadataRecordBatchMaxKeys {
		return ErrMetadataPageBudget
	}
	prefix := metadataLogicalUnitsOrderKey(field, 0)[:2]
	type oldUnit struct {
		ordinal uint64
		leaf    metadataLogicalUnitsLeaf
	}
	var old []oldUnit
	if err := a.index.Range(ctx, trees[0], prefix, sw29LogicalPrefixEnd(prefix), func(key, raw []byte) error {
		leaf, err := metadataLogicalUnitsDecodeLeaf(raw)
		if err == nil {
			old = append(old, oldUnit{binary.BigEndian.Uint64(key[2:]), leaf})
		}
		return err
	}); err != nil {
		return err
	}
	for _, unit := range old {
		if err := a.dropLeaf(ctx, tx, trees, field, unit.ordinal, descriptor, unit.leaf); err != nil {
			return err
		}
	}
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return err
	}
	defer builder.Close()
	for _, unit := range old {
		node, err := a.nodeLocked(ctx, tx, unit.leaf, descriptor.typ, false)
		if err != nil {
			return err
		}
		if err := builder.inheritLocations(node.source); err != nil {
			return err
		}
	}
	if err := builder.inheritTimestampInputs(ctx); err != nil {
		return err
	}
	name := jsonFieldName(reflect.TypeFor[domain.Bootstrap]().Field(field))
	if err := builder.Field(name, value); err != nil {
		return err
	}
	node := builder.source.fields[field]
	if node.unavailable {
		return ErrBoundedMetadataCoverage
	}
	return a.appendStreamedField(ctx, tx, trees, field, descriptor, node)
}

// The typed-source compatibility path can rewrite an arbitrarily large field
// without materializing its collection or retaining a slice of old unit IDs.
func (a *metadataLogicalUnitsArena) replaceStreamedField(ctx context.Context, tx *sql.Tx, trees *metadataLogicalUnitTrees, field int, descriptor *metadataLogicalUnitsField, node metadataLogicalNode) error {
	prefix := metadataLogicalUnitsOrderKey(field, 0)[:2]
	oldRoot := trees[0]
	if err := a.index.Range(ctx, oldRoot, prefix, sw29LogicalPrefixEnd(prefix), func(key, raw []byte) error {
		leaf, err := metadataLogicalUnitsDecodeLeaf(raw)
		if err != nil {
			return err
		}
		return a.dropLeaf(ctx, tx, trees, field, binary.BigEndian.Uint64(key[2:]), descriptor, leaf)
	}); err != nil {
		return err
	}
	return a.appendStreamedField(ctx, tx, trees, field, descriptor, node)
}

func (a *metadataLogicalUnitsArena) appendStreamedField(ctx context.Context, tx *sql.Tx, trees *metadataLogicalUnitTrees, field int, descriptor *metadataLogicalUnitsField, node metadataLogicalNode) error {
	*descriptor = metadataLogicalUnitsField{
		typ: node.typ, cost: node.cost, unavailable: node.unavailable, blocked: node.blocked,
	}
	var err error
	add := func(ordinal uint64, key *metadataLogicalNode, child metadataLogicalNode) error {
		leaf := metadataLogicalUnitsLeaf{cost: child.cost, unavailable: child.unavailable, blocked: child.blocked}
		frame := child
		if key != nil {
			leaf.keySize, leaf.keyCost = uint64(key.size), key.cost
			frame.offset, frame.size = key.offset, key.size+child.size
		}
		leaf.unit, err = a.importLocked(ctx, tx, frame)
		if err != nil {
			return err
		}
		descriptor.count++
		descriptor.nextOrder = max(descriptor.nextOrder, ordinal+1)
		if leaf.unavailable {
			descriptor.unavailableUnits++
		}
		descriptor.single = leaf
		return a.addLeaf(ctx, tx, trees, field, ordinal, *descriptor, leaf)
	}
	switch {
	case node.typ.Kind() == reflect.Map && !node.blocked:
		var ordinal uint64
		err = node.MapEntries(ctx, func(key, child metadataLogicalNode) error {
			err := add(ordinal, &key, child)
			ordinal++
			return err
		})
		if err == nil {
			descriptor.nilValue, err = node.isNil(ctx)
		}
	case node.typ.Kind() == reflect.Slice && node.typ.Elem().Kind() != reflect.Uint8 && !node.blocked,
		node.typ.Kind() == reflect.Struct && jsonFieldName(reflect.TypeFor[domain.Bootstrap]().Field(field)) == "workspaceSettings" && !node.blocked:
		err = node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
			ordinal := uint64(0)
			if node.typ.Kind() == reflect.Struct {
				field, _ := node.typ.FieldByName(key.String())
				ordinal = uint64(field.Index[0])
			} else {
				ordinal = uint64(key.Int())
			}
			return add(ordinal, nil, child)
		})
		if err == nil {
			descriptor.nilValue, err = node.isNil(ctx)
		}
	default:
		err = add(0, nil, node)
	}
	return err
}

func (s *metadataLogicalSource) unitCorpus(ctx context.Context) *sw29LogicalCorpus {
	r := s.units
	fields := map[string]metadataLogicalNode{}
	for i, field := range r.fields {
		if field.typ != nil {
			fields[jsonFieldName(reflect.TypeFor[domain.Bootstrap]().Field(i))] = s.fields[i]
		}
	}
	return &sw29LogicalCorpus{
		index:  &sw29LogicalIndex{stage: r.arena.index.stage, seed: r.arena.index.seed},
		fields: fields, points: r.trees[1], aggregates: r.trees[2], barriers: r.trees[3],
		unitRoot: r, unitArena: r.arena, unitContext: ctx,
	}
}
