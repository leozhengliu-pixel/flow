package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"reflect"

	"flow/api/internal/domain"
)

func (c *sw29LogicalCorpus) locatorBytes() int {
	if c.unitArena != nil {
		return 48
	}
	return 32
}

func (c *sw29LogicalCorpus) unitRange(ctx context.Context, tree int, root uint64, lo, hi []byte, visit func([]byte, []byte) error) error {
	if c.unitRoot != nil && c.index.tx == nil {
		return c.unitRoot.Range(ctx, tree, lo, hi, visit)
	}
	return c.index.Range(ctx, root, lo, hi, visit)
}

func (c *sw29LogicalCorpus) unitLocator(node metadataLogicalNode) ([]byte, error) {
	raw := make([]byte, 48)
	if node.collection != nil {
		raw[0] = 2
		binary.BigEndian.PutUint16(raw[1:3], uint16(node.collection.field))
		return raw, nil
	}
	a := c.unitArena
	if c.index.tx == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
	}
	typ, found := a.typeIDs[node.typ]
	if !found {
		if c.index.tx == nil || len(a.typeIDs) >= len(a.types) {
			return nil, ErrMetadataWorkBudget
		}
		typ = uint32(len(a.typeIDs))
		a.typeIDs[node.typ], a.types[typ] = typ, node.typ
	}
	var unit uint64
	if file, ok := node.source.file.(*metadataLogicalUnitFile); ok && file.path == a.index.stage.path {
		unit = file.unit
	} else {
		if c.index.tx == nil || c.unitContext == nil {
			return nil, ErrMetadataRecordQuery
		}
		var err error
		unit, err = a.importLocked(c.unitContext, c.index.tx, node)
		if err != nil {
			return nil, err
		}
		node.offset = 0
	}
	raw[0] = 1
	binary.BigEndian.PutUint64(raw[1:9], unit)
	binary.BigEndian.PutUint32(raw[9:13], typ)
	if node.unavailable {
		raw[13] |= 1
	}
	if node.blocked {
		raw[13] |= 2
	}
	binary.BigEndian.PutUint64(raw[14:22], uint64(node.offset))
	binary.BigEndian.PutUint64(raw[22:30], uint64(node.size))
	binary.BigEndian.PutUint64(raw[30:38], node.cost)
	return raw, nil
}

func (c *sw29LogicalCorpus) unitDecode(raw []byte) (metadataLogicalNode, error) {
	if len(raw) < 48 {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	if raw[0] == 2 {
		if c.unitRoot == nil {
			return metadataLogicalNode{}, ErrMetadataRecordQuery
		}
		return c.unitRoot.fieldNode(c.unitContext, int(binary.BigEndian.Uint16(raw[1:3])))
	}
	if raw[0] != 1 {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	typ := binary.BigEndian.Uint32(raw[9:13])
	if typ >= uint32(len(c.unitArena.types)) || c.unitArena.types[typ] == nil {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	unit, offset := binary.BigEndian.Uint64(raw[1:9]), binary.BigEndian.Uint64(raw[14:22])
	size, cost := binary.BigEndian.Uint64(raw[22:30]), binary.BigEndian.Uint64(raw[30:38])
	leaf := metadataLogicalUnitsLeaf{unit: unit, keySize: offset, cost: cost, unavailable: raw[13]&1 != 0, blocked: raw[13]&2 != 0}
	var node metadataLogicalNode
	var err error
	if c.index.tx != nil {
		node, err = c.unitArena.nodeLocked(c.unitContext, c.index.tx, leaf, c.unitArena.types[typ], false)
	} else if c.unitRoot != nil {
		node, err = c.unitRoot.node(c.unitContext, leaf, c.unitArena.types[typ], false)
	} else {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	if err != nil {
		return node, err
	}
	if size > uint64(node.size) {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	node.size = int64(size)
	return node, nil
}

func (a *metadataLogicalUnitsArena) nodeLocked(ctx context.Context, tx *sql.Tx, leaf metadataLogicalUnitsLeaf, typ reflect.Type, key bool) (metadataLogicalNode, error) {
	var registryID, size uint64
	if err := tx.QueryRowContext(ctx, `SELECT registry,size FROM logical_units WHERE id=?`, leaf.unit).Scan(&registryID, &size); err != nil {
		return metadataLogicalNode{}, err
	}
	registry := a.registries[registryID]
	if registry == nil || leaf.keySize > size {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	storage := &metadataLogicalStorage{
		refs: 1, file: &metadataLogicalUnitFile{tx: tx, path: a.index.stage.path, unit: leaf.unit, size: int64(size)},
		types: registry.types, times: registry.times, locations: registry.locations,
	}
	node := metadataLogicalNode{
		source: storage, typ: typ, offset: int64(leaf.keySize), size: int64(size - leaf.keySize),
		cost: leaf.cost, unavailable: leaf.unavailable, blocked: leaf.blocked,
	}
	if key {
		node.offset, node.size, node.cost = 0, int64(leaf.keySize), leaf.keyCost
		node.unavailable, node.blocked = false, false
	}
	return node, nil
}

func (a *metadataLogicalUnitsArena) compileUnit(ctx context.Context, tx *sql.Tx, trees *metadataLogicalUnitTrees, field int, ordinal uint64, descriptor metadataLogicalUnitsField, leaf metadataLogicalUnitsLeaf) error {
	c := &sw29LogicalCorpus{
		index: a.index, points: trees[1], aggregates: trees[2], barriers: trees[3],
		unitArena: a, unitContext: ctx,
	}
	defer func() {
		if c.literals != nil {
			c.literals.Close()
		}
	}()
	c.onFact = func(tree int, key []byte) error {
		_, err := a.execLocked(ctx, tx, int64(len(key)+64), `INSERT OR REPLACE INTO logical_unit_facts(unit,tree,key) VALUES(?,?,?)`, leaf.unit, tree, key)
		return err
	}
	name := jsonFieldName(reflect.TypeFor[domain.Bootstrap]().Field(field))
	rank := sw29LogicalRank(nil, uint64(field+1))
	typ := descriptor.typ
	childType := typ
	collection := !descriptor.blocked && (typ.Kind() == reflect.Map ||
		typ.Kind() == reflect.Slice && typ.Elem().Kind() != reflect.Uint8 ||
		typ.Kind() == reflect.Struct && name == "workspaceSettings")
	if collection && (typ.Kind() == reflect.Map || typ.Kind() == reflect.Slice) {
		childType = typ.Elem()
	} else if collection && typ.Kind() == reflect.Struct {
		childType = typ.Field(int(ordinal)).Type
	}
	value, err := a.nodeLocked(ctx, tx, leaf, childType, false)
	if err != nil {
		return err
	}
	if collection {
		rank = sw29LogicalRank(rank, ordinal+1)
	}
	if collection && typ.Kind() == reflect.Map {
		key, err := a.nodeLocked(ctx, tx, leaf, typ.Key(), true)
		if err != nil {
			return err
		}
		if typ.Key().Kind() == reflect.String {
			if err := c.point(ctx, field, ordinal, value, key); err != nil {
				return err
			}
			own, err := key.StringEqual(ctx, "id")
			if err != nil {
				return err
			}
			if own {
				id, err := metadataAggregateUnwrap(ctx, metadataAggregateStoredNode{value}, false)
				if err != nil {
					return err
				}
				if id != nil && id.nodeType().Kind() == reflect.String {
					virtual := metadataLogicalNode{typ: typ, collection: &metadataLogicalUnitsCollection{field: field}}
					if err := c.fact(ctx, virtual, id.(metadataAggregateStoredNode).value, sw29LogicalRank(nil, uint64(field+1))); err != nil {
						return err
					}
				}
			}
		}
	} else if collection && typ.Kind() == reflect.Slice && childType.Kind() == reflect.Struct {
		plan := aggregateSearchPlanFor(childType, map[reflect.Type]bool{})
		if plan.idIndex != nil {
			id, err := metadataAggregateIDNode(ctx, metadataAggregateStoredNode{value}, plan.idIndex)
			if err != nil {
				return err
			}
			if id != nil {
				if err := c.point(ctx, field, ordinal, value, id.(metadataAggregateStoredNode).value); err != nil {
					return err
				}
			}
		}
	}
	if collection && name == "workspaceSettings" {
		c.literals, err = newMetadataLogicalBuilder(ctx)
		if err != nil {
			return err
		}
		id, err := c.literals.writeValue(reflect.ValueOf(jsonFieldName(typ.Field(int(ordinal)))), 0, false)
		if err != nil {
			return err
		}
		if err := c.point(ctx, field, ordinal, value, id); err != nil {
			return err
		}
	}
	if err := c.compile(ctx, value, rank, "", 0); err != nil {
		return err
	}
	trees[1], trees[2], trees[3] = c.points, c.aggregates, c.barriers
	return nil
}
