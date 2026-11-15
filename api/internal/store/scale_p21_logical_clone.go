package store

import (
	"context"
	"errors"
	"reflect"
)

// cloneMetadataLogicalSnapshot is cloneBootstrap's immutable-frame boundary.
// Struct values lose exported json:"-" fields; map keys remain exact native
// keys. Sharing unchanged frames is safe because every materialization owns
// its mutable containers. Time and dynamic-type registries remain native.
func cloneMetadataLogicalSnapshot(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	if source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	cloned, err := source.Transform(ctx, func(_ string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		return metadataLogicalSnapshotCloneNode(ctx, node)
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		if cloned != nil {
			cloned.Release()
		}
		return nil, ctxErr
	}
	return cloned, err
}

// Use cloneSnapshotValue's existing struct plan instead of a separate cache.
// Interfaces require an instance visit; map keys are deliberately excluded.
func metadataLogicalSnapshotCloneType(typ reflect.Type, path []reflect.Type) bool {
	if len(path) >= metadataLogicalDepth {
		return true
	}
	for _, parent := range path {
		if parent == typ {
			return false
		}
	}
	path = append(path, typ)
	switch typ.Kind() {
	case reflect.Interface:
		return true
	case reflect.Pointer, reflect.Array, reflect.Slice:
		return metadataLogicalSnapshotCloneType(typ.Elem(), path)
	case reflect.Map:
		return metadataLogicalSnapshotCloneType(typ.Elem(), path)
	case reflect.Struct:
		plan := structPlanFor(typ)
		if len(plan.zero) != 0 {
			return true
		}
		for _, index := range plan.deep {
			if metadataLogicalSnapshotCloneType(typ.Field(index).Type, path) {
				return true
			}
		}
	}
	return false
}

func metadataLogicalSnapshotCloneNode(ctx context.Context, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
	original := metadataAggregateStoredNode{value: node}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if node.typ == nil {
		return nil, false, ErrMetadataRecordQuery
	}
	var path [metadataLogicalDepth]reflect.Type
	if !metadataLogicalSnapshotCloneType(node.typ, path[:0]) {
		return original, false, nil
	}
	if node.blocked {
		return nil, false, ErrBoundedMetadataCoverage
	}
	changed := false
	visit := func(child metadataLogicalNode) error {
		_, childChanged, err := metadataLogicalSnapshotCloneNode(ctx, child)
		if err != nil {
			return err
		}
		if childChanged {
			changed = true
			return ErrMetadataIterationDone
		}
		return nil
	}
	var err error
	if node.typ.Kind() == reflect.Map {
		err = node.MapEntries(ctx, func(_, value metadataLogicalNode) error {
			return visit(value)
		})
	} else {
		err = node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
			if node.typ.Kind() == reflect.Struct {
				field, found := node.typ.FieldByName(key.String())
				if !found {
					return ErrMetadataRecordQuery
				}
				if field.PkgPath == "" && field.Tag.Get("json") == "-" {
					zero, err := metadataLogicalSnapshotCloneZero(ctx, child)
					if err != nil {
						return err
					}
					if !zero {
						changed = true
						return ErrMetadataIterationDone
					}
					return nil
				}
			}
			return visit(child)
		})
	}
	if err != nil && !errors.Is(err, ErrMetadataIterationDone) {
		return nil, false, err
	}
	if changed {
		return metadataLogicalSnapshotCloneNodeView{base: node}, true, nil
	}
	return original, false, nil
}

// Recognize already-zero common dropped fields without decoding containers or
// strings. Other composite values can safely take the streaming rewrite path.
func metadataLogicalSnapshotCloneZero(ctx context.Context, node metadataLogicalNode) (bool, error) {
	if node.blocked {
		return false, ctx.Err()
	}
	switch node.typ.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		return node.isNil(ctx)
	case reflect.String:
		cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
		size, err := cursor.uint()
		return size == 0, err
	case reflect.Struct, reflect.Array:
		return false, ctx.Err()
	default:
		value, err := node.Value(ctx, 4096)
		if err != nil {
			return false, err
		}
		return value.IsZero(), nil
	}
}

type metadataLogicalSnapshotCloneNodeView struct {
	base metadataLogicalNode
}

func (n metadataLogicalSnapshotCloneNodeView) nodeType() reflect.Type {
	return n.base.typ
}

func (n metadataLogicalSnapshotCloneNodeView) logicalBase() metadataLogicalNode {
	return n.base
}

func (n metadataLogicalSnapshotCloneNodeView) nodeValue(context.Context, int) (reflect.Value, error) {
	// This wrapper is a streaming transform, including interfaces and arrays.
	// A codec fallback must not materialize a complete selected body.
	return reflect.Value{}, ErrBoundedMetadataCoverage
}

func (n metadataLogicalSnapshotCloneNodeView) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if n.base.typ.Kind() == reflect.Struct {
			field, found := n.base.typ.FieldByName(key.String())
			if !found {
				return ErrMetadataRecordQuery
			}
			if field.PkgPath == "" && field.Tag.Get("json") == "-" {
				return visit(key, metadataAggregateNativeNode{value: reflect.Zero(field.Type)})
			}
		}
		value, _, err := metadataLogicalSnapshotCloneNode(ctx, child)
		if err != nil {
			return err
		}
		return visit(key, value)
	})
}

func (n metadataLogicalSnapshotCloneNodeView) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	return n.base.MapEntries(ctx, func(key, child metadataLogicalNode) error {
		value, _, err := metadataLogicalSnapshotCloneNode(ctx, child)
		if err != nil {
			return err
		}
		return visit(metadataAggregateStoredNode{value: key}, value)
	})
}
