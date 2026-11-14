package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type sw29LogicalCorpus struct {
	index                        *sw29LogicalIndex
	points, aggregates, barriers uint64
	fields                       map[string]metadataLogicalNode
	types                        []reflect.Type
	stores                       []*metadataLogicalStorage
	literals                     *metadataLogicalBuilder
	buffer                       [32 << 10]byte
	closed                       bool
	unitArena                    *metadataLogicalUnitsArena
	unitRoot                     *metadataLogicalUnitsRoot
	onFact                       func(int, []byte) error
	unitContext                  context.Context
}

const sw29LogicalIndexDescriptorBytes = 1 << 20

func sw29BuildLogicalCorpus(ctx context.Context, source *metadataLogicalSource) (_ *sw29LogicalCorpus, err error) {
	if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, sw29LogicalIndexDescriptorBytes, metadataLogicalDescriptorLimit) {
		return nil, ErrMetadataWorkBudget
	}
	c := &sw29LogicalCorpus{fields: map[string]metadataLogicalNode{}}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()
	c.index, err = sw29NewLogicalIndex(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := c.index.stage.exec(ctx, 4096, `CREATE TABLE logical_physical(node BLOB PRIMARY KEY,field TEXT NOT NULL,key BLOB NOT NULL,physical INTEGER NOT NULL)`); err != nil {
		return nil, err
	}
	defer func() {
		if c.literals != nil {
			c.literals.Close()
			c.literals = nil
		}
	}()
	err = source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name == "-" {
			return nil
		}
		c.fields[name] = node
		if _, err := c.locator(node); err != nil {
			return err
		}
		index, _, ok := metadataLogicalField(name)
		if !ok {
			return ErrMetadataRecordQuery
		}
		rank := sw29LogicalRank(nil, uint64(index+1))
		if node.typ.Kind() == reflect.Slice && node.typ.Elem().Kind() == reflect.Struct {
			err := node.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
				plan := aggregateSearchPlanFor(item.typ, map[reflect.Type]bool{})
				if plan.idIndex == nil {
					return nil
				}
				id, err := metadataAggregateIDNode(ctx, metadataAggregateStoredNode{item}, plan.idIndex)
				if err != nil {
					return err
				}
				stored, ok := id.(metadataAggregateStoredNode)
				if !ok {
					return ErrMetadataRecordQuery
				}
				return c.point(ctx, index, uint64(key.Int()), item, stored.value)
			})
			if err != nil {
				return err
			}
		} else if node.typ.Kind() == reflect.Map && node.typ.Key().Kind() == reflect.String {
			ordinal := uint64(0)
			if err := node.MapEntries(ctx, func(key, item metadataLogicalNode) error {
				err := c.point(ctx, index, ordinal, item, key)
				ordinal++
				return err
			}); err != nil {
				return err
			}
		} else if name == "workspaceSettings" && node.typ.Kind() == reflect.Struct {
			if c.literals == nil {
				c.literals, err = newMetadataLogicalBuilder(ctx)
				if err != nil {
					return err
				}
			}
			if err := node.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
				property, _ := node.typ.FieldByName(key.String())
				id, err := c.literals.writeValue(reflect.ValueOf(jsonFieldName(property)), 0, false)
				if err != nil {
					return err
				}
				return c.point(ctx, index, uint64(property.Index[0]), item, id)
			}); err != nil {
				return err
			}
		}
		return c.compile(ctx, node, rank, name, 0)
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *sw29LogicalCorpus) Close() {
	if c.closed {
		return
	}
	c.closed = true
	if c.index != nil {
		_ = c.index.Close()
		c.index = nil
	}
	for _, storage := range c.stores {
		storage.Release()
	}
	c.stores = nil
	metadataLogicalDescriptorsUsed.Add(-sw29LogicalIndexDescriptorBytes)
}

func sw29LogicalRank(parent []byte, index uint64) []byte {
	result := make([]byte, len(parent)+8)
	copy(result, parent)
	binary.BigEndian.PutUint64(result[len(parent):], index)
	return result
}

func (c *sw29LogicalCorpus) hash(ctx context.Context, id metadataLogicalNode) ([32]byte, error) {
	var result [32]byte
	h := sha256.New()
	err := id.StreamBytes(ctx, func(reader io.Reader) error {
		_, err := io.CopyBuffer(h, reader, c.buffer[:])
		return err
	})
	copy(result[:], h.Sum(nil))
	return result, err
}

func (c *sw29LogicalCorpus) locator(node metadataLogicalNode) ([]byte, error) {
	if c.unitArena != nil {
		return c.unitLocator(node)
	}
	storage := slices.Index(c.stores, node.source)
	if storage < 0 {
		if len(c.stores) >= 512 {
			return nil, ErrMetadataWorkBudget
		}
		if err := node.source.Retain(); err != nil {
			return nil, err
		}
		storage = len(c.stores)
		c.stores = append(c.stores, node.source)
	}
	typ := slices.Index(c.types, node.typ)
	if typ < 0 {
		if len(c.types) >= 4096 {
			return nil, ErrMetadataWorkBudget
		}
		typ = len(c.types)
		c.types = append(c.types, node.typ)
	}
	result := make([]byte, 32)
	binary.BigEndian.PutUint16(result[:2], uint16(storage))
	binary.BigEndian.PutUint16(result[2:4], uint16(typ))
	if node.unavailable {
		result[4] |= 1
	}
	if node.blocked {
		result[4] |= 2
	}
	binary.BigEndian.PutUint64(result[8:16], uint64(node.offset))
	binary.BigEndian.PutUint64(result[16:24], uint64(node.size))
	binary.BigEndian.PutUint64(result[24:32], node.cost)
	return result, nil
}

func (c *sw29LogicalCorpus) decode(raw []byte) (metadataLogicalNode, error) {
	if c.unitArena != nil {
		return c.unitDecode(raw)
	}
	if len(raw) < 32 {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	storage, typ := int(binary.BigEndian.Uint16(raw[:2])), int(binary.BigEndian.Uint16(raw[2:4]))
	if storage >= len(c.stores) || typ >= len(c.types) {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	return metadataLogicalNode{
		source: c.stores[storage], typ: c.types[typ], unavailable: raw[4]&1 != 0, blocked: raw[4]&2 != 0,
		offset: int64(binary.BigEndian.Uint64(raw[8:16])), size: int64(binary.BigEndian.Uint64(raw[16:24])), cost: binary.BigEndian.Uint64(raw[24:32]),
	}, nil
}

func (c *sw29LogicalCorpus) pair(node, id metadataLogicalNode) ([]byte, error) {
	a, err := c.locator(node)
	if err != nil {
		return nil, err
	}
	b, err := c.locator(id)
	if err != nil {
		return nil, err
	}
	return append(a, b...), nil
}

func (c *sw29LogicalCorpus) point(ctx context.Context, field int, ordinal uint64, node, id metadataLogicalNode) error {
	hash, err := c.hash(ctx, id)
	if err != nil {
		return err
	}
	key := make([]byte, 42)
	binary.BigEndian.PutUint16(key[:2], uint16(field))
	copy(key[2:34], hash[:])
	binary.BigEndian.PutUint64(key[34:], ordinal)
	value, err := c.pair(node, id)
	if err != nil {
		return err
	}
	c.points, err = c.index.Put(ctx, c.points, key, value)
	if err == nil && c.onFact != nil {
		err = c.onFact(1, key)
	}
	return err
}

func (c *sw29LogicalCorpus) fact(ctx context.Context, node, id metadataLogicalNode, rank []byte) error {
	empty, err := id.StringEqual(ctx, "")
	if err != nil || empty {
		return err
	}
	hash, err := c.hash(ctx, id)
	if err != nil {
		return err
	}
	key := append(append([]byte(nil), hash[:]...), rank...)
	value, err := c.pair(node, id)
	if err != nil {
		return err
	}
	c.aggregates, err = c.index.Put(ctx, c.aggregates, key, value)
	if err == nil && c.onFact != nil {
		err = c.onFact(2, key)
	}
	return err
}

func (c *sw29LogicalCorpus) compile(ctx context.Context, node metadataLogicalNode, rank []byte, field string, depth int) error {
	if depth >= metadataLogicalDepth {
		return ErrMetadataWorkBudget
	}
	if node.blocked {
		if !aggregateSearchPlanFor(node.typ, map[reflect.Type]bool{}).mayContain {
			return nil
		}
		value, err := c.locator(node)
		if err != nil {
			return err
		}
		c.barriers, err = c.index.Put(ctx, c.barriers, rank, value)
		if err == nil && c.onFact != nil {
			err = c.onFact(3, rank)
		}
		return err
	}
	unwrapped, err := metadataAggregateUnwrap(ctx, metadataAggregateStoredNode{node}, true)
	if err != nil {
		return err
	}
	if unwrapped == nil {
		return nil
	}
	node = unwrapped.(metadataAggregateStoredNode).value
	plan := aggregateSearchPlanFor(node.typ, map[reflect.Type]bool{})
	if !plan.mayContain {
		return nil
	}
	if node.blocked {
		value, err := c.locator(node)
		if err != nil {
			return err
		}
		c.barriers, err = c.index.Put(ctx, c.barriers, rank, value)
		if err == nil && c.onFact != nil {
			err = c.onFact(3, rank)
		}
		return err
	}
	switch node.typ.Kind() {
	case reflect.Struct:
		if plan.idIndex != nil {
			id, err := metadataAggregateIDNode(ctx, metadataAggregateStoredNode{node}, plan.idIndex)
			if err != nil {
				return err
			}
			if id != nil {
				if err := c.fact(ctx, node, id.(metadataAggregateStoredNode).value, rank); err != nil {
					return err
				}
			}
		}
		return node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
			entry, ok := node.typ.FieldByName(key.String())
			if !ok || !slices.Contains(plan.fields, entry.Index[0]) {
				return nil
			}
			if node.typ == reflect.TypeFor[domain.ImportJob]() && key.String() == "Rows" ||
				node.typ == reflect.TypeFor[domain.MigrationJob]() && key.String() == "Bundle" {
				return nil
			}
			return c.compile(ctx, child, sw29LogicalRank(rank, uint64(entry.Index[0]+1)), field, depth+1)
		})
	case reflect.Slice, reflect.Array:
		return node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
			return c.compile(ctx, child, sw29LogicalRank(rank, uint64(key.Int()+1)), field, depth+1)
		})
	case reflect.Map:
		if node.typ.Key().Kind() == reflect.String {
			if err := node.MapEntries(ctx, func(key, value metadataLogicalNode) error {
				match, err := key.StringEqual(ctx, "id")
				if err != nil || !match {
					return err
				}
				unwrapped, err := metadataAggregateUnwrap(ctx, metadataAggregateStoredNode{value}, false)
				if err != nil {
					return err
				}
				if unwrapped != nil && unwrapped.nodeType().Kind() == reflect.String {
					return c.fact(ctx, node, unwrapped.(metadataAggregateStoredNode).value, rank)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if !aggregateSearchPlanFor(node.typ.Elem(), map[reflect.Type]bool{}).mayContain {
			return nil
		}
		ordinal := uint64(0)
		return node.MapEntries(ctx, func(_ metadataLogicalNode, child metadataLogicalNode) error {
			ordinal++
			return c.compile(ctx, child, sw29LogicalRank(rank, ordinal), field, depth+1)
		})
	}
	return nil
}

func sw29LogicalPrefixEnd(prefix []byte) []byte {
	result := append([]byte(nil), prefix...)
	for i := len(result) - 1; i >= 0; i-- {
		if result[i] != 255 {
			result[i]++
			return result[:i+1]
		}
	}
	return nil
}

func (c *sw29LogicalCorpus) Point(ctx context.Context, field, id string, visit func(metadataLogicalNode) error) error {
	return c.pointRecords(ctx, field, id, func(_ uint64, node metadataLogicalNode) error { return visit(node) })
}

func (c *sw29LogicalCorpus) pointRecords(ctx context.Context, field, id string, visit func(uint64, metadataLogicalNode) error) error {
	index, _, ok := metadataLogicalField(field)
	if !ok {
		return ErrMetadataRecordQuery
	}
	node := c.fields[field]
	if node.typ.Kind() != reflect.Map && node.typ.Kind() != reflect.Slice && field != "workspaceSettings" {
		return ErrMetadataRecordQuery
	}
	hash := c.hashString(id)
	prefix := make([]byte, 34)
	binary.BigEndian.PutUint16(prefix[:2], uint16(index))
	copy(prefix[2:], hash[:])
	return c.unitRange(ctx, 1, c.points, prefix, sw29LogicalPrefixEnd(prefix), func(key []byte, value []byte) error {
		idNode, err := c.decode(value[c.locatorBytes():])
		if err != nil {
			return err
		}
		match, err := idNode.StringEqual(ctx, id)
		if err != nil || !match {
			return err
		}
		record, err := c.decode(value)
		if err != nil {
			return err
		}
		return visit(binary.BigEndian.Uint64(key[34:]), record)
	})
}

func (c *sw29LogicalCorpus) Select(ctx context.Context, scope MetadataRecordScope, maxBytes int) (domain.Bootstrap, error) {
	var result domain.Bootstrap
	if len(scope.Fields)+len(scope.Read)+len(scope.Write) > 50 {
		return result, ErrMetadataWorkBudget
	}
	target := reflect.ValueOf(&result).Elem()
	left := maxBytes
	bootstrap := reflect.TypeFor[domain.Bootstrap]()
	for index := 0; index < bootstrap.NumField(); index++ {
		name := jsonFieldName(bootstrap.Field(index))
		if name == "-" {
			continue
		}
		node := c.fields[name]
		value := target.Field(index)
		if name == "workspace" || name == "viewer" || name == "viewerRole" || slices.Contains(scope.Fields, name) {
			owned, err := node.Value(ctx, left)
			if err != nil {
				return result, err
			}
			left -= int(node.cost)
			value.Set(owned)
			continue
		}
		if !slices.Contains(metadataRecordFields(scope), name) {
			continue
		}
		if node.typ.Kind() == reflect.Map {
			value.Set(reflect.MakeMap(node.typ))
		} else if node.typ.Kind() == reflect.Slice {
			if node.typ.Elem().Kind() != reflect.Struct {
				return result, ErrMetadataRecordQuery
			}
			value.Set(reflect.MakeSlice(node.typ, 0, 0))
		} else if name != "workspaceSettings" {
			return result, ErrMetadataRecordQuery
		}
		type selection struct {
			order uint64
			key   string
			node  metadataLogicalNode
		}
		selected := []selection{}
		for _, key := range append(slices.Clone(scope.Read), scope.Write...) {
			if key.Kind != name || slices.ContainsFunc(selected, func(s selection) bool { return s.key == key.ID }) {
				continue
			}
			matches := 0
			err := c.pointRecords(ctx, name, key.ID, func(order uint64, item metadataLogicalNode) error {
				matches++
				if matches > 1 {
					return ErrMetadataRecordQuery
				}
				selected = append(selected, selection{order, key.ID, item})
				return nil
			})
			if err != nil {
				return result, err
			}
		}
		slices.SortStableFunc(selected, func(a, b selection) int {
			if a.order < b.order {
				return -1
			}
			if a.order > b.order {
				return 1
			}
			return 0
		})
		for _, item := range selected {
			owned, err := item.node.Value(ctx, left)
			if err != nil {
				return result, err
			}
			left -= int(item.node.cost)
			switch node.typ.Kind() {
			case reflect.Map:
				value.SetMapIndex(reflect.ValueOf(item.key).Convert(node.typ.Key()), owned)
			case reflect.Slice:
				value.Set(reflect.Append(value, owned))
			default:
				value.Field(int(item.order)).Set(owned)
			}
		}
	}
	return result, nil
}

func (c *sw29LogicalCorpus) Resolve(ctx context.Context, id string) (metadataAggregateNode, error) {
	if id == "" {
		return nil, nil
	}
	field, terminal := metadataAggregatePrefixField(id)
	var found metadataLogicalNode
	if field != "" {
		err := c.Point(ctx, field, id, func(node metadataLogicalNode) error {
			found = node
			return sw29LogicalIndexStop
		})
		if err != nil {
			return nil, err
		}
		if found.typ != nil {
			return metadataAggregateStoredNode{found}, nil
		}
		if terminal {
			return nil, nil
		}
	}
	hash := c.hashString(id)
	var winnerRank []byte
	err := c.unitRange(ctx, 2, c.aggregates, hash[:], sw29LogicalPrefixEnd(hash[:]), func(key, value []byte) error {
		idNode, err := c.decode(value[c.locatorBytes():])
		if err != nil {
			return err
		}
		match, err := idNode.StringEqual(ctx, id)
		if err != nil || !match {
			return err
		}
		found, err = c.decode(value)
		winnerRank = append([]byte(nil), key[32:]...)
		if err != nil {
			return err
		}
		return sw29LogicalIndexStop
	})
	if err != nil {
		return nil, err
	}
	var barrier []byte
	err = c.unitRange(ctx, 3, c.barriers, nil, nil, func(key, _ []byte) error {
		barrier = append([]byte(nil), key...)
		return sw29LogicalIndexStop
	})
	if err != nil {
		return nil, err
	}
	if barrier != nil && (found.typ == nil || bytes.Compare(barrier, winnerRank) <= 0) {
		return nil, ErrBoundedMetadataCoverage
	}
	if found.typ == nil {
		return nil, nil
	}
	var result metadataAggregateNode = metadataAggregateStoredNode{found}
	if found.typ == reflect.TypeFor[domain.ImportJob]() {
		result = metadataAggregateCompactNode{base: result, excluded: "Rows"}
	} else if found.typ == reflect.TypeFor[domain.MigrationJob]() {
		result = metadataAggregateCompactNode{base: result, excluded: "Bundle"}
	}
	return result, nil
}

func (c *sw29LogicalCorpus) hashString(value string) [32]byte {
	h := sha256.New()
	var buffer [4096]byte
	for len(value) > 0 {
		n := copy(buffer[:], value)
		h.Write(buffer[:n])
		value = value[n:]
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// Only a producer that observed the actual canonical row can supply this
// binding. Logical body IDs are intentionally never used to infer its key.
func (c *sw29LogicalCorpus) BindCanonical(ctx context.Context, node metadataLogicalNode, key RecordKey, physical uint8) error {
	if len(key.Kind) > 191 || len(key.ID) > 191 || physical > 1 {
		return ErrMetadataRecordQuery
	}
	descriptor, err := c.locator(node)
	if err != nil {
		return err
	}
	if _, err := c.index.stage.exec(ctx, int64(len(key.Kind)+len(key.ID)+128), `INSERT INTO logical_physical(node,field,key,physical) VALUES(?,?,?,?) ON CONFLICT(node) DO NOTHING`, descriptor, key.Kind, []byte(key.ID), physical); err != nil {
		return err
	}
	var kind string
	var id []byte
	var existing uint8
	if err := c.index.stage.db.QueryRowContext(ctx, `SELECT field,key,physical FROM logical_physical WHERE node=?`, descriptor).Scan(&kind, &id, &existing); err != nil {
		return err
	}
	if kind != key.Kind || string(id) != key.ID || existing != physical {
		return ErrMetadataRecordQuery
	}
	return nil
}

func (c *sw29LogicalCorpus) Canonical(ctx context.Context, node metadataLogicalNode) (RecordKey, uint8, error) {
	descriptor, err := c.locator(node)
	if err != nil {
		return RecordKey{}, 0, err
	}
	var result RecordKey
	var id []byte
	var physical uint8
	err = c.index.stage.db.QueryRowContext(ctx, `SELECT field,key,physical FROM logical_physical WHERE node=?`, descriptor).Scan(&result.Kind, &id, &physical)
	result.ID = string(id)
	return result, physical, err
}
