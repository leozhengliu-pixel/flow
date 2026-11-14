package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

func (a *metadataLogicalUnitsArena) removeUnitFacts(ctx context.Context, tx *sql.Tx, trees *metadataLogicalUnitTrees, unit uint64) error {
	lastTree, lastKey := -1, []byte{}
	for {
		rows, err := tx.QueryContext(ctx, `SELECT tree,key FROM logical_unit_facts WHERE unit=? AND (tree>? OR (tree=? AND key>?)) ORDER BY tree,key LIMIT 32`, unit, lastTree, lastTree, lastKey)
		if err != nil {
			return err
		}
		type fact struct {
			tree int
			key  []byte
		}
		facts := make([]fact, 0, 32)
		for rows.Next() {
			var item fact
			if err := rows.Scan(&item.tree, &item.key); err != nil {
				rows.Close()
				return err
			}
			if item.tree < 1 || item.tree > 3 || len(item.key) > sw29LogicalIndexKeyBytes {
				rows.Close()
				return ErrMetadataRecordQuery
			}
			facts = append(facts, item)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(facts) == 0 {
			return nil
		}
		for _, item := range facts {
			trees[item.tree], err = a.index.Delete(ctx, trees[item.tree], item.key)
			if err != nil {
				return err
			}
		}
		tail := facts[len(facts)-1]
		lastTree, lastKey = tail.tree, tail.key
	}
}

func (a *metadataLogicalUnitsArena) dropLeaf(ctx context.Context, tx *sql.Tx, trees *metadataLogicalUnitTrees, field int, ordinal uint64, descriptor *metadataLogicalUnitsField, leaf metadataLogicalUnitsLeaf) error {
	if err := a.removeUnitFacts(ctx, tx, trees, leaf.unit); err != nil {
		return err
	}
	key := metadataLogicalUnitsOrderKey(field, ordinal)
	var err error
	trees[0], err = a.index.Delete(ctx, trees[0], key)
	if err != nil {
		return err
	}
	trees[4], err = a.index.Delete(ctx, trees[4], key)
	if err != nil {
		return err
	}
	trees[5], err = a.index.Delete(ctx, trees[5], metadataLogicalUnitsPhysicalKey(leaf.unit))
	if err != nil {
		return err
	}
	descriptor.count--
	if leaf.unavailable {
		descriptor.unavailableUnits--
	}
	descriptor.cost -= leaf.cost + leaf.keyCost + metadataLogicalUnitsElementOverhead(descriptor.typ)
	descriptor.unavailable = descriptor.unavailableUnits != 0
	return nil
}

func metadataLogicalUnitsElementOverhead(typ reflect.Type) uint64 {
	switch typ.Kind() {
	case reflect.Map:
		return uint64(typ.Elem().Size())*2 + 128
	case reflect.Slice:
		return uint64(typ.Elem().Size())*2 + 64
	}
	return 0
}

func (a *metadataLogicalUnitsArena) needsCompact(ctx context.Context, tx *sql.Tx, field int, descriptor metadataLogicalUnitsField, leaf metadataLogicalUnitsLeaf) (bool, error) {
	name := jsonFieldName(reflect.TypeFor[domain.Bootstrap]().Field(field))
	if name != "importJobs" && name != "migrationJobs" || descriptor.typ.Kind() != reflect.Slice {
		return false, nil
	}
	node, err := a.nodeLocked(ctx, tx, leaf, descriptor.typ.Elem(), false)
	if err != nil {
		return false, err
	}
	excluded := "Rows"
	if name == "migrationJobs" {
		excluded = "Bundle"
	}
	child, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{node}, excluded)
	if err != nil || child == nil {
		return false, err
	}
	nilValue, err := child.(metadataAggregateStoredNode).value.isNil(ctx)
	return !nilValue, err
}

func (a *metadataLogicalUnitsArena) addLeaf(ctx context.Context, tx *sql.Tx, trees *metadataLogicalUnitTrees, field int, ordinal uint64, descriptor metadataLogicalUnitsField, leaf metadataLogicalUnitsLeaf) error {
	pending, err := a.needsCompact(ctx, tx, field, descriptor, leaf)
	if err != nil {
		return err
	}
	leaf.pending = pending
	key := metadataLogicalUnitsOrderKey(field, ordinal)
	trees[0], err = a.index.Put(ctx, trees[0], key, leaf.encode())
	if err != nil {
		return err
	}
	if pending {
		trees[4], err = a.index.Put(ctx, trees[4], key, leaf.encode())
		if err != nil {
			return err
		}
	}
	return a.compileUnit(ctx, tx, trees, field, ordinal, descriptor, leaf)
}

func (a *metadataLogicalUnitsArena) encodeLeaf(ctx context.Context, tx *sql.Tx, key reflect.Value, value reflect.Value, origin *metadataLogicalStorage) (metadataLogicalUnitsLeaf, error) {
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return metadataLogicalUnitsLeaf{}, err
	}
	defer builder.Close()
	if origin != nil {
		builder.storage.types, builder.storage.times = slices.Clone(origin.types), slices.Clone(origin.times)
		if err := builder.inheritLocations(origin); err != nil {
			return metadataLogicalUnitsLeaf{}, err
		}
	}
	if err := builder.inheritTimestampInputs(ctx); err != nil {
		return metadataLogicalUnitsLeaf{}, err
	}
	var keyNode metadataLogicalNode
	if key.IsValid() {
		keyNode, err = builder.writeValue(key, 0, false)
		if err != nil {
			return metadataLogicalUnitsLeaf{}, err
		}
	}
	node, err := builder.writeValue(value, 0, false)
	if err != nil {
		return metadataLogicalUnitsLeaf{}, err
	}
	if node.unavailable || keyNode.unavailable {
		return metadataLogicalUnitsLeaf{}, ErrBoundedMetadataCoverage
	}
	leaf := metadataLogicalUnitsLeaf{cost: node.cost, unavailable: node.unavailable, blocked: node.blocked}
	if key.IsValid() {
		leaf.keySize, leaf.keyCost = uint64(keyNode.size), keyNode.cost
		node.offset, node.size = keyNode.offset, keyNode.size+node.size
	}
	leaf.unit, err = a.importLocked(ctx, tx, node)
	return leaf, err
}

func metadataLogicalUnitsSelected(value reflect.Value, key string) (reflect.Value, bool, error) {
	switch value.Kind() {
	case reflect.Map:
		item := value.MapIndex(reflect.ValueOf(key).Convert(value.Type().Key()))
		return item, item.IsValid(), nil
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if jsonFieldName(value.Type().Field(i)) == key {
				return value.Field(i), true, nil
			}
		}
	case reflect.Slice:
		var selected reflect.Value
		for i := 0; i < value.Len(); i++ {
			if id, ok := metadataRecordValueKey(value.Index(i)); ok && id == key {
				if selected.IsValid() {
					return reflect.Value{}, false, ErrMetadataRecordQuery
				}
				selected = value.Index(i)
			}
		}
		return selected, selected.IsValid(), nil
	}
	return reflect.Value{}, false, nil
}

func (s *metadataLogicalSource) forkUnits(ctx context.Context, before, next domain.Bootstrap, scope MetadataRecordScope, stamp copyStamp, suppliedOrders ...map[metadataRecordKey]int) (*metadataLogicalSource, error) {
	if len(suppliedOrders) > 1 {
		return nil, ErrMetadataRecordQuery
	}
	orders, _ := ctx.Value(metadataAppendOrdersKey{}).(map[metadataRecordKey]int)
	if len(suppliedOrders) == 1 {
		orders = suppliedOrders[0]
	}
	root, a := s.units, s.units.arena
	old, changed := reflect.ValueOf(before), reflect.ValueOf(next)
	candidate, err := a.change(ctx, root, stamp, func(tx *sql.Tx, trees *metadataLogicalUnitTrees, fields []metadataLogicalUnitsField) error {
		for field, descriptor := range fields {
			if descriptor.typ == nil {
				continue
			}
			name := jsonFieldName(reflect.TypeFor[domain.Bootstrap]().Field(field))
			core := name == "workspace" || name == "viewer" || name == "viewerRole"
			complete := slices.Contains(scope.Fields, name) || core && !reflect.DeepEqual(old.Field(field).Interface(), changed.Field(field).Interface())
			if complete {
				if err := a.replaceField(ctx, tx, trees, field, &fields[field], changed.Field(field)); err != nil {
					return err
				}
				continue
			}
			writes := []RecordKey{}
			for _, key := range scope.Write {
				if key.Kind == name && !slices.Contains(writes, key) {
					writes = append(writes, key)
				}
			}
			if descriptor.typ.Kind() == reflect.Slice {
				slices.SortStableFunc(writes, func(x, y RecordKey) int {
					return orders[metadataRecordKey{field: name, key: x.ID}] - orders[metadataRecordKey{field: name, key: y.ID}]
				})
			}
			for _, key := range writes {
				c := &sw29LogicalCorpus{
					index: a.index, points: trees[1], unitArena: a, unitContext: ctx,
					fields: map[string]metadataLogicalNode{name: {typ: descriptor.typ}},
				}
				var ordinal uint64
				found := false
				var origin *metadataLogicalStorage
				var physical []byte
				if err := c.pointRecords(ctx, name, key.ID, func(order uint64, node metadataLogicalNode) error {
					if found {
						return ErrMetadataRecordQuery
					}
					found, ordinal = true, order
					origin = node.source
					return nil
				}); err != nil {
					return err
				}
				replacement, exists, err := metadataLogicalUnitsSelected(changed.Field(field), key.ID)
				if err != nil {
					return err
				}
				if found {
					raw, _, err := a.index.Get(ctx, trees[0], metadataLogicalUnitsOrderKey(field, ordinal))
					if err != nil {
						return err
					}
					leaf, err := metadataLogicalUnitsDecodeLeaf(raw)
					if err != nil {
						return err
					}
					physical, _, err = a.index.Get(ctx, trees[5], metadataLogicalUnitsPhysicalKey(leaf.unit))
					if err != nil {
						return err
					}
					if err := a.dropLeaf(ctx, tx, trees, field, ordinal, &descriptor, leaf); err != nil {
						return err
					}
				} else if exists {
					switch descriptor.typ.Kind() {
					case reflect.Slice:
						_, allowed := orders[metadataRecordKey{field: name, key: key.ID}]
						actor, hasActor := actorFromContext(ctx)
						if !allowed && !(name == "users" && hasActor && actor.ID == key.ID) {
							return ErrMetadataRecordQuery
						}
						ordinal = descriptor.nextOrder
					case reflect.Map:
						ordinal = descriptor.nextOrder
					case reflect.Struct:
						for i := 0; i < descriptor.typ.NumField(); i++ {
							if jsonFieldName(descriptor.typ.Field(i)) == key.ID {
								ordinal = uint64(i)
							}
						}
					default:
						return ErrMetadataRecordQuery
					}
				}
				if exists {
					var mapKey reflect.Value
					if descriptor.typ.Kind() == reflect.Map {
						mapKey = reflect.ValueOf(key.ID).Convert(descriptor.typ.Key())
					}
					leaf, err := a.encodeLeaf(ctx, tx, mapKey, replacement, origin)
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
					if err := a.addLeaf(ctx, tx, trees, field, ordinal, descriptor, leaf); err != nil {
						return err
					}
					if physical != nil {
						if _, _, err := metadataLogicalUnitsDecodePhysical(physical); err != nil {
							return err
						}
						binary.BigEndian.PutUint64(physical[1:9], leaf.unit)
						trees[5], err = a.index.Put(ctx, trees[5], metadataLogicalUnitsPhysicalKey(leaf.unit), physical)
						if err != nil {
							return err
						}
					}
				}
			}
			fields[field] = descriptor
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result, err := candidate.source(ctx)
	if err != nil {
		candidate.Release()
	}
	return result, err
}
