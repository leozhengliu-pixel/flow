package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type metadataLogicalCursor struct {
	node   metadataLogicalNode
	offset int64
	ctx    context.Context
}

func (c *metadataLogicalCursor) read(p []byte) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if int64(len(p)) > c.node.offset+c.node.size-c.offset {
		return io.ErrUnexpectedEOF
	}
	_, err := c.node.source.file.ReadAt(p, c.offset)
	c.offset += int64(len(p))
	return err
}

func (c *metadataLogicalCursor) uint() (uint64, error) {
	var p [8]byte
	err := c.read(p[:])
	return binary.LittleEndian.Uint64(p[:]), err
}

func (c *metadataLogicalCursor) byte() (byte, error) {
	var p [1]byte
	err := c.read(p[:])
	return p[0], err
}

func (c *metadataLogicalCursor) child(t reflect.Type) (metadataLogicalNode, error) {
	start := c.offset
	size, err := c.uint()
	if err != nil {
		return metadataLogicalNode{}, err
	}
	cost, err := c.uint()
	if err != nil || size < 16 || size > uint64(c.node.offset+c.node.size-start) {
		return metadataLogicalNode{}, io.ErrUnexpectedEOF
	}
	c.offset = start + int64(size)
	return metadataLogicalNode{source: c.node.source, offset: start, size: int64(size), cost: cost & ((uint64(1) << 62) - 1), typ: t, unavailable: cost&(uint64(1)<<63) != 0, blocked: cost&(uint64(1)<<62) != 0}, nil
}

// Value admits the entire decoded object before allocation. The caller must
// hold a work lease until the returned value is no longer retained.
func (n metadataLogicalNode) Value(ctx context.Context, maxBytes int) (reflect.Value, error) {
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, err
	}
	if n.unavailable {
		return reflect.Value{}, fmt.Errorf("%w: installed logical subtree %s is unavailable", ErrBoundedMetadataCoverage, n.typ)
	}
	if maxBytes < 0 || n.cost > uint64(maxBytes) {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := n.source.Retain(); err != nil {
		return reflect.Value{}, err
	}
	defer n.source.Release()
	return n.value(ctx, 0)
}

func (n metadataLogicalNode) value(ctx context.Context, depth int) (reflect.Value, error) {
	if depth >= metadataLogicalDepth {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	v := reflect.New(n.typ).Elem()
	if n.typ == reflect.TypeFor[time.Time]() {
		mode, err := c.byte()
		if err != nil {
			return v, err
		}
		first, err := c.uint()
		if err != nil {
			return v, err
		}
		second, err := c.uint()
		if err != nil {
			return v, err
		}
		var result time.Time
		if mode == 0 {
			local, err := c.uint()
			if err != nil {
				return v, err
			}
			result = time.Unix(int64(first), int64(second)).UTC()
			if local == 1 {
				result = result.In(metadataLogicalProcessLocal)
			} else if local >= 2 && local-2 < uint64(len(n.source.locations)) {
				result = result.In(n.source.locations[local-2])
			} else if local != 0 {
				return v, ErrMetadataRecordQuery
			}
		} else if mode == 1 && first < uint64(len(n.source.times)) {
			result = n.source.times[first].Add(time.Duration(second))
		} else {
			return v, ErrMetadataRecordQuery
		}
		v.Set(reflect.ValueOf(result))
		return v, nil
	}
	switch n.typ.Kind() {
	case reflect.Bool:
		value, err := c.byte()
		v.SetBool(value != 0)
		return v, err
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, err := c.uint()
		v.SetInt(int64(value))
		return v, err
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value, err := c.uint()
		v.SetUint(value)
		return v, err
	case reflect.Float32, reflect.Float64:
		value, err := c.uint()
		v.SetFloat(math.Float64frombits(value))
		return v, err
	case reflect.Complex64, reflect.Complex128:
		r, err := c.uint()
		if err != nil {
			return v, err
		}
		i, err := c.uint()
		v.SetComplex(complex(math.Float64frombits(r), math.Float64frombits(i)))
		return v, err
	case reflect.String:
		length, err := c.uint()
		if err != nil || length > uint64(n.size) {
			return v, io.ErrUnexpectedEOF
		}
		data := make([]byte, int(length))
		if err := c.read(data); err != nil {
			return v, err
		}
		v.SetString(string(data))
		return v, nil
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		present, err := c.byte()
		if err != nil || present == 0 {
			return v, err
		}
		if n.typ.Kind() == reflect.Pointer || n.typ.Kind() == reflect.Interface {
			var t reflect.Type
			if n.typ.Kind() == reflect.Interface {
				index, err := c.uint()
				if err != nil || index >= uint64(len(n.source.types)) {
					return v, ErrMetadataRecordQuery
				}
				t = n.source.types[index]
			} else {
				t = n.typ.Elem()
			}
			node, err := c.child(t)
			if err != nil {
				return v, err
			}
			owned, err := node.value(ctx, depth+1)
			if err != nil {
				return v, err
			}
			if n.typ.Kind() == reflect.Pointer {
				result := reflect.New(t)
				result.Elem().Set(owned)
				return result, nil
			}
			v.Set(owned)
			return v, nil
		}
		length, err := c.uint()
		if err != nil || length > uint64(n.cost) {
			return v, io.ErrUnexpectedEOF
		}
		if n.typ.Kind() == reflect.Slice {
			v.Set(reflect.MakeSlice(n.typ, int(length), int(length)))
			if n.typ.Elem().Kind() == reflect.Uint8 {
				for i := 0; i < int(length); i += 32 << 10 {
					data := make([]byte, min(32<<10, int(length)-i))
					if err := c.read(data); err != nil {
						return v, err
					}
					for j, value := range data {
						v.Index(i + j).SetUint(uint64(value))
					}
				}
				return v, nil
			}
		} else {
			v.Set(reflect.MakeMapWithSize(n.typ, int(length)))
		}
		for i := 0; i < int(length); i++ {
			var key reflect.Value
			if n.typ.Kind() == reflect.Map {
				node, err := c.child(n.typ.Key())
				if err != nil {
					return v, err
				}
				key, err = node.value(ctx, depth+1)
				if err != nil {
					return v, err
				}
			}
			node, err := c.child(n.typ.Elem())
			if err != nil {
				return v, err
			}
			owned, err := node.value(ctx, depth+1)
			if err != nil {
				return v, err
			}
			if n.typ.Kind() == reflect.Map {
				v.SetMapIndex(key, owned)
			} else {
				v.Index(i).Set(owned)
			}
		}
	case reflect.Struct, reflect.Array:
		length := n.typ.NumField
		t := func(i int) reflect.Type { return n.typ.Field(i).Type }
		at := v.Field
		if n.typ.Kind() == reflect.Array {
			length = n.typ.Len
			t = func(int) reflect.Type { return n.typ.Elem() }
			at = v.Index
		}
		for i := 0; i < length(); i++ {
			node, err := c.child(t(i))
			if err != nil {
				return v, err
			}
			owned, err := node.value(ctx, depth+1)
			if err != nil {
				return v, err
			}
			at(i).Set(owned)
		}
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
	default:
		return v, ErrMetadataRecordQuery
	}
	return v, nil
}

// Children borrows framed child nodes without decoding their bodies. Map keys
// are decoded under a fixed bound; captures reject unsupported key graphs.
func (n metadataLogicalNode) Children(ctx context.Context, visit func(reflect.Value, metadataLogicalNode) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n.blocked {
		return fmt.Errorf("%w: opaque installed logical subtree %s", ErrBoundedMetadataCoverage, n.typ)
	}
	if err := n.source.Retain(); err != nil {
		return err
	}
	defer n.source.Release()
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	if n.typ == reflect.TypeFor[time.Time]() {
		return nil
	}
	length := 0
	switch n.typ.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		present, err := c.byte()
		if err != nil || present == 0 {
			return err
		}
		if n.typ.Kind() == reflect.Pointer || n.typ.Kind() == reflect.Interface {
			var t reflect.Type
			if n.typ.Kind() == reflect.Interface {
				index, err := c.uint()
				if err != nil || index >= uint64(len(n.source.types)) {
					return ErrMetadataRecordQuery
				}
				t = n.source.types[index]
			} else {
				t = n.typ.Elem()
			}
			node, err := c.child(t)
			if err != nil {
				return err
			}
			return visit(reflect.Value{}, node)
		}
		if n.typ.Kind() == reflect.Slice && n.typ.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		count, err := c.uint()
		if err != nil || count > uint64(n.cost) {
			return ErrMetadataRecordQuery
		}
		length = int(count)
	case reflect.Array:
		length = n.typ.Len()
	case reflect.Struct:
		length = n.typ.NumField()
	default:
		return nil
	}
	for i := 0; i < length; i++ {
		key := reflect.ValueOf(i)
		t := n.typ.Elem
		if n.typ.Kind() == reflect.Struct {
			f := n.typ.Field(i)
			key, t = reflect.ValueOf(f.Name), func() reflect.Type { return f.Type }
		} else if n.typ.Kind() == reflect.Map {
			node, err := c.child(n.typ.Key())
			if err != nil {
				return err
			}
			key, err = node.Value(ctx, 256<<10)
			if err != nil {
				return err
			}
		}
		node, err := c.child(t())
		if err != nil {
			return err
		}
		if err := visit(key, node); err != nil {
			return err
		}
	}
	return nil
}

func (n metadataLogicalNode) StringEqual(ctx context.Context, want string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if n.typ.Kind() != reflect.String {
		return false, nil
	}
	if err := n.source.Retain(); err != nil {
		return false, err
	}
	defer n.source.Release()
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	length, err := c.uint()
	if err != nil || length != uint64(len(want)) {
		return false, err
	}
	var data [4096]byte
	for offset := 0; offset < len(want); offset += len(data) {
		size := min(len(data), len(want)-offset)
		if err := c.read(data[:size]); err != nil {
			return false, err
		}
		if string(data[:size]) != want[offset:offset+size] {
			return false, nil
		}
	}
	return true, nil
}

// MapEntries leaves keys in their framed representation, so an unrelated
// legal large property name can be skipped without allocating it.
func (n metadataLogicalNode) MapEntries(ctx context.Context, visit func(metadataLogicalNode, metadataLogicalNode) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n.blocked {
		return ErrBoundedMetadataCoverage
	}
	if n.typ.Kind() != reflect.Map {
		return ErrMetadataRecordQuery
	}
	if err := n.source.Retain(); err != nil {
		return err
	}
	defer n.source.Release()
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	present, err := c.byte()
	if err != nil || present == 0 {
		return err
	}
	length, err := c.uint()
	if err != nil || length > n.cost {
		return ErrMetadataRecordQuery
	}
	for i := uint64(0); i < length; i++ {
		key, err := c.child(n.typ.Key())
		if err != nil {
			return err
		}
		value, err := c.child(n.typ.Elem())
		if err != nil {
			return err
		}
		if err := visit(key, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *metadataLogicalSource) Select(ctx context.Context, scope MetadataRecordScope, maxBytes int) (domain.Bootstrap, error) {
	var data domain.Bootstrap
	result := reflect.ValueOf(&data).Elem()
	remaining := maxBytes
	err := s.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		core := name == "workspace" || name == "viewer" || name == "viewerRole"
		complete := slices.Contains(scope.Fields, name)
		if core || complete {
			value, err := node.Value(ctx, remaining)
			if err != nil {
				return err
			}
			remaining -= int(node.cost)
			index, _, _ := metadataLogicalField(name)
			result.Field(index).Set(value)
			return nil
		}
		if !slices.Contains(metadataRecordFields(scope), name) {
			return nil
		}
		index, _, _ := metadataLogicalField(name)
		target := result.Field(index)
		switch node.Type().Kind() {
		case reflect.Slice:
			if node.typ.Elem().Kind() != reflect.Struct {
				return ErrMetadataRecordQuery
			}
			target.Set(reflect.MakeSlice(node.typ, 0, 0))
		case reflect.Map:
			target.Set(reflect.MakeMap(node.typ))
		case reflect.Struct:
			if name != "workspaceSettings" {
				return ErrMetadataRecordQuery
			}
		default:
			return ErrMetadataRecordQuery
		}
		seen := map[string]bool{}
		if node.typ.Kind() == reflect.Map {
			return node.MapEntries(ctx, func(key, item metadataLogicalNode) error {
				for _, selected := range append(slices.Clone(scope.Read), scope.Write...) {
					if selected.Kind != name {
						continue
					}
					equal, err := key.StringEqual(ctx, selected.ID)
					if err != nil {
						return err
					}
					if !equal || seen[selected.ID] {
						continue
					}
					owned, err := item.Value(ctx, remaining)
					if err != nil {
						return err
					}
					remaining -= int(item.cost)
					seen[selected.ID] = true
					target.SetMapIndex(reflect.ValueOf(selected.ID).Convert(node.typ.Key()), owned)
				}
				return nil
			})
		}
		return node.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
			id := ""
			matched := false
			if node.typ.Kind() == reflect.Map {
				if key.Kind() != reflect.String {
					return ErrMetadataRecordQuery
				}
				id = key.String()
				matched = true
			} else if node.typ.Kind() == reflect.Struct {
				f, _ := node.typ.FieldByName(key.String())
				id = strings.Split(f.Tag.Get("json"), ",")[0]
				matched = true
			} else {
				if err := item.Children(ctx, func(field reflect.Value, value metadataLogicalNode) error {
					if field.String() == "ID" {
						for _, selected := range append(slices.Clone(scope.Read), scope.Write...) {
							if selected.Kind != name {
								continue
							}
							equal, err := value.StringEqual(ctx, selected.ID)
							if err != nil {
								return err
							}
							if equal {
								id = selected.ID
								matched = true
								break
							}
						}
					}
					return nil
				}); err != nil {
					return err
				}
			}
			if !matched || !metadataRecordSelected(scope, name, id, false) {
				return nil
			}
			if seen[id] {
				return fmt.Errorf("%w: ambiguous logical record in %s", ErrMetadataRecordQuery, name)
			}
			seen[id] = true
			owned, err := item.Value(ctx, remaining)
			if err != nil {
				return err
			}
			remaining -= int(item.cost)
			switch node.typ.Kind() {
			case reflect.Map:
				target.SetMapIndex(key, owned)
			case reflect.Struct:
				target.FieldByName(key.String()).Set(owned)
			default:
				target.Set(reflect.Append(target, owned))
			}
			return nil
		})
	})
	return data, err
}
