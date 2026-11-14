package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"reflect"
	"slices"
)

func (s *metadataLogicalSource) transformUnits(ctx context.Context, transform func(string, metadataLogicalNode) (metadataAggregateNode, bool, error)) (*metadataLogicalSource, error) {
	type change struct {
		field   int
		compact string
		node    metadataLogicalNode
		patches []metadataLogicalUnitPatch
	}
	var changes []change
	var staged []*metadataLogicalBuilder
	defer func() {
		for _, builder := range staged {
			builder.Close()
		}
	}()
	if err := s.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name == "-" {
			return nil
		}
		value, changed, err := transform(name, node)
		if err != nil || !changed {
			return err
		}
		field, _, _ := metadataLogicalField(name)
		if patches, ok := metadataLogicalUnitPatches(value, node); ok {
			changes = append(changes, change{field: field, patches: patches})
			return nil
		}
		if compact, ok := value.(metadataLogicalCompactCollection); ok {
			excluded := "Rows"
			if compact.field == "migrationJobs" {
				excluded = "Bundle"
			}
			changes = append(changes, change{field: field, compact: excluded})
			return nil
		}
		if native, ok := value.(metadataAggregateNativeNode); ok {
			// Installed transient collections are normally already nil.
			if (native.value.Kind() == reflect.Slice || native.value.Kind() == reflect.Map) &&
				native.value.IsNil() && s.units.fields[field].nilValue && s.units.fields[field].count == 0 {
				return nil
			}
		}
		// Until a transform has an affected-unit compiler, retain the typed
		// source's streaming whole-field behavior. This fallback bounds memory,
		// but deliberately makes no constant-work claim.
		builder, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return err
		}
		staged = append(staged, builder)
		builder.storage.types = slices.Clone(node.source.types)
		builder.storage.times = slices.Clone(node.source.times)
		if err := builder.inheritLocations(node.source); err != nil {
			return err
		}
		if err := builder.inheritTimestampInputs(ctx); err != nil {
			return err
		}
		written, err := builder.writeAggregateNode(value, 0)
		if err != nil {
			return err
		}
		if written.typ != node.typ {
			return ErrMetadataRecordQuery
		}
		changes = append(changes, change{field: field, node: written})
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.Retain(); err != nil {
		return nil, err
	}
	current := s
	defer func() {
		if current != nil {
			current.Release()
		}
	}()
	generic := false
	for _, change := range changes {
		if len(change.patches) == 0 {
			generic = true
			continue
		}
		next, err := current.patchUnitField(ctx, change.field, change.patches)
		if err != nil {
			return nil, err
		}
		current.Release()
		current = next
	}
	if !generic {
		result := current
		current = nil
		return result, nil
	}
	r, a := current.units, current.units.arena
	root, err := a.change(ctx, r, s.Stamp(), func(tx *sql.Tx, trees *metadataLogicalUnitTrees, fields []metadataLogicalUnitsField) error {
		for _, change := range changes {
			if len(change.patches) != 0 {
				continue
			}
			if change.node.typ != nil {
				if err := a.replaceStreamedField(ctx, tx, trees, change.field, &fields[change.field], change.node); err != nil {
					return err
				}
				continue
			}
			prefix := metadataLogicalUnitsOrderKey(change.field, 0)[:2]
			pendingRoot := trees[4]
			if err := a.index.Range(ctx, pendingRoot, prefix, sw29LogicalPrefixEnd(prefix), func(key, raw []byte) error {
				old, err := metadataLogicalUnitsDecodeLeaf(raw)
				if err != nil {
					return err
				}
				descriptor := &fields[change.field]
				node, err := a.nodeLocked(ctx, tx, old, descriptor.typ.Elem(), false)
				if err != nil {
					return err
				}
				builder, err := newMetadataLogicalBuilder(ctx)
				if err != nil {
					return err
				}
				defer builder.Close()
				builder.storage.types, builder.storage.times = slices.Clone(node.source.types), slices.Clone(node.source.times)
				if err := builder.inheritLocations(node.source); err != nil {
					return err
				}
				if err := builder.inheritTimestampInputs(ctx); err != nil {
					return err
				}
				written, err := builder.writeAggregateNode(metadataLogicalCompactRecord{
					base: metadataAggregateStoredNode{node}, excluded: change.compact,
				}, 0)
				if err != nil {
					return err
				}
				unit, err := a.importLocked(ctx, tx, written)
				if err != nil {
					return err
				}
				ordinal := binary.BigEndian.Uint64(key[2:])
				if err := a.dropLeaf(ctx, tx, trees, change.field, ordinal, descriptor, old); err != nil {
					return err
				}
				leaf := metadataLogicalUnitsLeaf{unit: unit, cost: written.cost, unavailable: written.unavailable, blocked: written.blocked}
				descriptor.count++
				descriptor.cost += leaf.cost + metadataLogicalUnitsElementOverhead(descriptor.typ)
				if leaf.unavailable {
					descriptor.unavailableUnits++
				}
				descriptor.unavailable = descriptor.unavailableUnits != 0
				return a.addLeaf(ctx, tx, trees, change.field, ordinal, *descriptor, leaf)
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result, err := root.source(ctx)
	if err != nil {
		root.Release()
	}
	return result, err
}
