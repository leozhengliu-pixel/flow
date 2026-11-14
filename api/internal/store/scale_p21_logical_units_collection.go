package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"sync"
)

type metadataLogicalUnitsField struct {
	typ              reflect.Type
	cost             uint64
	count            uint64
	nilValue         bool
	unavailable      bool
	blocked          bool
	single           metadataLogicalUnitsLeaf
	nextOrder        uint64
	unavailableUnits uint64
}

type metadataLogicalUnitsCollection struct {
	root  *metadataLogicalUnitsRoot
	field int
	metadataLogicalUnitsField
}

type metadataLogicalUnitsLeaf struct {
	unit                   uint64
	keySize, keyCost, cost uint64
	unavailable, blocked   bool
	pending                bool
}

func (v metadataLogicalUnitsLeaf) encode() []byte {
	raw := make([]byte, 34)
	raw[0] = 1
	binary.BigEndian.PutUint64(raw[1:9], v.unit)
	binary.BigEndian.PutUint64(raw[9:17], v.keySize)
	binary.BigEndian.PutUint64(raw[17:25], v.keyCost)
	binary.BigEndian.PutUint64(raw[25:33], v.cost)
	if v.unavailable {
		raw[33] |= 1
	}
	if v.blocked {
		raw[33] |= 2
	}
	if v.pending {
		raw[33] |= 4
	}
	return raw
}

func metadataLogicalUnitsDecodeLeaf(raw []byte) (metadataLogicalUnitsLeaf, error) {
	if len(raw) != 34 || raw[0] != 1 {
		return metadataLogicalUnitsLeaf{}, ErrMetadataRecordQuery
	}
	return metadataLogicalUnitsLeaf{
		unit: binary.BigEndian.Uint64(raw[1:9]), keySize: binary.BigEndian.Uint64(raw[9:17]),
		keyCost: binary.BigEndian.Uint64(raw[17:25]), cost: binary.BigEndian.Uint64(raw[25:33]),
		unavailable: raw[33]&1 != 0, blocked: raw[33]&2 != 0,
		pending: raw[33]&4 != 0,
	}, nil
}

func metadataLogicalUnitsOrderKey(field int, ordinal uint64) []byte {
	key := make([]byte, 10)
	binary.BigEndian.PutUint16(key[:2], uint16(field))
	binary.BigEndian.PutUint64(key[2:], ordinal)
	return key
}

func (r *metadataLogicalUnitsRoot) Range(ctx context.Context, tree int, lo, hi []byte, visit func([]byte, []byte) error) error {
	if err := r.Retain(); err != nil {
		return err
	}
	defer r.Release()
	for {
		var key, value []byte
		a := r.arena
		a.mu.Lock()
		err := a.index.Range(ctx, r.trees[tree], lo, hi, func(k, v []byte) error {
			key, value = append([]byte(nil), k...), append([]byte(nil), v...)
			return sw29LogicalIndexStop
		})
		a.mu.Unlock()
		if err != nil || key == nil {
			return err
		}
		if err := visit(key, value); err != nil {
			if errors.Is(err, sw29LogicalIndexStop) {
				return nil
			}
			return err
		}
		lo = append(key, 0)
	}
}

func (r *metadataLogicalUnitsRoot) node(ctx context.Context, leaf metadataLogicalUnitsLeaf, typ reflect.Type, key bool) (metadataLogicalNode, error) {
	a := r.arena
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || r.refs == 0 {
		return metadataLogicalNode{}, errMetadataLogicalClosed
	}
	var registryID, size uint64
	if err := a.index.stage.db.QueryRowContext(ctx, `SELECT registry,size FROM logical_units WHERE id=?`, leaf.unit).Scan(&registryID, &size); err != nil {
		return metadataLogicalNode{}, err
	}
	registry := a.registries[registryID]
	if registry == nil || leaf.keySize > size {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	storage := &metadataLogicalStorage{
		file:     &metadataLogicalUnitFile{db: a.index.stage.db, path: a.index.stage.path, unit: leaf.unit, size: int64(size), cache: &a.chunks},
		unitRoot: r, types: registry.types, times: registry.times, locations: registry.locations,
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

func (n *metadataLogicalUnitsCollection) MapEntries(ctx context.Context, visit func(metadataLogicalNode, metadataLogicalNode) error) error {
	if n.typ.Kind() != reflect.Map || n.blocked {
		return ErrMetadataRecordQuery
	}
	prefix := metadataLogicalUnitsOrderKey(n.field, 0)[:2]
	return n.root.Range(ctx, 0, prefix, sw29LogicalPrefixEnd(prefix), func(_ []byte, raw []byte) error {
		leaf, err := metadataLogicalUnitsDecodeLeaf(raw)
		if err != nil {
			return err
		}
		key, err := n.root.node(ctx, leaf, n.typ.Key(), true)
		if err != nil {
			return err
		}
		value, err := n.root.node(ctx, leaf, n.typ.Elem(), false)
		if err != nil {
			return err
		}
		return visit(key, value)
	})
}

func (n *metadataLogicalUnitsCollection) Children(ctx context.Context, visit func(reflect.Value, metadataLogicalNode) error) error {
	if n.blocked {
		return ErrBoundedMetadataCoverage
	}
	if n.typ.Kind() == reflect.Map {
		return n.MapEntries(ctx, func(key, value metadataLogicalNode) error {
			owned, err := key.Value(ctx, 2<<20)
			if err != nil {
				return err
			}
			return visit(owned, value)
		})
	}
	prefix := metadataLogicalUnitsOrderKey(n.field, 0)[:2]
	index := 0
	return n.root.Range(ctx, 0, prefix, sw29LogicalPrefixEnd(prefix), func(key []byte, raw []byte) error {
		leaf, err := metadataLogicalUnitsDecodeLeaf(raw)
		if err != nil {
			return err
		}
		var typ reflect.Type
		var childKey reflect.Value
		if n.typ.Kind() == reflect.Struct {
			ordinal := binary.BigEndian.Uint64(key[2:])
			if ordinal >= uint64(n.typ.NumField()) {
				return ErrMetadataRecordQuery
			}
			field := n.typ.Field(int(ordinal))
			typ, childKey = field.Type, reflect.ValueOf(field.Name)
		} else {
			typ, childKey = n.typ.Elem(), reflect.ValueOf(index)
		}
		value, err := n.root.node(ctx, leaf, typ, false)
		if err != nil {
			return err
		}
		index++
		return visit(childKey, value)
	})
}

func (n *metadataLogicalUnitsCollection) Value(ctx context.Context, maxBytes int) (reflect.Value, error) {
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, err
	}
	if n.unavailable {
		return reflect.Value{}, ErrBoundedMetadataCoverage
	}
	if maxBytes < 0 || n.cost > uint64(maxBytes) {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	value := reflect.New(n.typ).Elem()
	switch n.typ.Kind() {
	case reflect.Map:
		if !n.nilValue {
			value.Set(reflect.MakeMapWithSize(n.typ, int(n.count)))
		}
		err := n.MapEntries(ctx, func(key, item metadataLogicalNode) error {
			k, err := key.Value(ctx, maxBytes)
			if err != nil {
				return err
			}
			v, err := item.Value(ctx, maxBytes)
			if err != nil {
				return err
			}
			value.SetMapIndex(k, v)
			return nil
		})
		return value, err
	case reflect.Slice:
		if !n.nilValue {
			value.Set(reflect.MakeSlice(n.typ, 0, int(n.count)))
		}
		err := n.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			v, err := item.Value(ctx, maxBytes)
			if err == nil {
				value.Set(reflect.Append(value, v))
			}
			return err
		})
		return value, err
	case reflect.Struct:
		err := n.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
			v, err := item.Value(ctx, maxBytes)
			if err == nil {
				value.FieldByName(key.String()).Set(v)
			}
			return err
		})
		return value, err
	}
	return reflect.Value{}, ErrMetadataRecordQuery
}

type metadataLogicalUnitFile struct {
	db    *sql.DB
	tx    *sql.Tx
	path  string
	unit  uint64
	size  int64
	cache *metadataLogicalUnitChunkCache
}

// These two chunks are included in the arena's fixed descriptor/cache charge.
// Unit IDs never recur after commit; transaction-local candidate reads bypass
// this cache because rolled-back SQLite row IDs may be reused.
type metadataLogicalUnitChunkCache struct {
	mu    sync.Mutex
	next  int
	slots [2]struct {
		unit  uint64
		chunk int64
		size  int
		data  [metadataLogicalUnitChunk]byte
	}
}

func (c *metadataLogicalUnitChunkCache) read(ctx context.Context, f *metadataLogicalUnitFile, output []byte, chunk, within, size int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	slot := -1
	for i := range c.slots {
		if c.slots[i].unit == f.unit && c.slots[i].chunk == chunk {
			slot = i
			break
		}
	}
	if slot == -1 {
		var raw []byte
		if err := f.db.QueryRowContext(ctx, `SELECT data FROM logical_unit_chunks WHERE unit=? AND chunk=?`, f.unit, chunk).Scan(&raw); err != nil {
			return 0, err
		}
		if len(raw) != int(min(metadataLogicalUnitChunk, f.size-chunk*metadataLogicalUnitChunk)) {
			return 0, io.ErrUnexpectedEOF
		}
		slot = c.next
		c.next = (c.next + 1) % len(c.slots)
		c.slots[slot].unit, c.slots[slot].chunk, c.slots[slot].size = f.unit, chunk, len(raw)
		copy(c.slots[slot].data[:], raw)
	}
	if within < 0 || size < 0 || within+size > int64(c.slots[slot].size) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(output, c.slots[slot].data[within:within+size]), nil
}

func (f *metadataLogicalUnitFile) Name() string              { return f.path }
func (f *metadataLogicalUnitFile) Close() error              { return nil }
func (f *metadataLogicalUnitFile) Write([]byte) (int, error) { return 0, ErrMetadataRecordQuery }
func (f *metadataLogicalUnitFile) WriteAt([]byte, int64) (int, error) {
	return 0, ErrMetadataRecordQuery
}
func (f *metadataLogicalUnitFile) ReadAt([]byte, int64) (int, error) {
	return 0, ErrMetadataRecordQuery
}
func (f *metadataLogicalUnitFile) ReadAtContext(ctx context.Context, output []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	if len(output) == 0 {
		return 0, nil
	}
	n := 0
	for n < len(output) && offset+int64(n) < f.size {
		position := offset + int64(n)
		chunk, within := position/metadataLogicalUnitChunk, position%metadataLogicalUnitChunk
		size := min(int64(len(output)-n), metadataLogicalUnitChunk-within, f.size-position)
		if f.tx == nil && f.cache != nil {
			copied, err := f.cache.read(ctx, f, output[n:], chunk, within, size)
			n += copied
			if err != nil {
				return n, err
			}
			continue
		}
		var raw []byte
		var row *sql.Row
		if f.tx != nil {
			row = f.tx.QueryRowContext(ctx, `SELECT SUBSTR(data,?,?) FROM logical_unit_chunks WHERE unit=? AND chunk=?`, within+1, size, f.unit, chunk)
		} else {
			row = f.db.QueryRowContext(ctx, `SELECT SUBSTR(data,?,?) FROM logical_unit_chunks WHERE unit=? AND chunk=?`, within+1, size, f.unit, chunk)
		}
		if err := row.Scan(&raw); err != nil {
			return n, err
		}
		if int64(len(raw)) != size {
			return n, io.ErrUnexpectedEOF
		}
		n += copy(output[n:], raw)
	}
	if n < len(output) {
		return n, io.EOF
	}
	return n, nil
}
