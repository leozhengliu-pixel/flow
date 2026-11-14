package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
)

// metadataAggregateRefreshField applies the exact display-reference pass to
// a complete source field before the callback overlay. User lookup keeps the
// last directory entry for duplicate IDs, as newIssueReferences does.
func metadataAggregateRefreshField(source *metadataLogicalSource, field string, node metadataAggregateNode) metadataAggregateNode {
	switch field {
	case "members", "projects", "initiatives", "projectUpdates", "initiativeUpdates", "comments", "activities", "notifications":
		return metadataAggregateRefreshCollection{source: source, field: field, base: node}
	}
	return node
}

type metadataAggregateRefreshCollection struct {
	source *metadataLogicalSource
	field  string
	base   metadataAggregateNode
}

func (n metadataAggregateRefreshCollection) nodeType() reflect.Type { return n.base.nodeType() }
func (n metadataAggregateRefreshCollection) logicalBase() metadataLogicalNode {
	return n.base.(metadataAggregateStoredNode).value
}
func (n metadataAggregateRefreshCollection) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	stored, ok := n.base.(metadataAggregateStoredNode)
	if !ok {
		return ErrMetadataRecordQuery
	}
	return stored.value.MapEntries(ctx, func(key, value metadataLogicalNode) error {
		return visit(metadataAggregateStoredNode{key}, metadataAggregateRefreshCollection{source: n.source, field: n.field, base: metadataAggregateStoredNode{value}})
	})
}
func (n metadataAggregateRefreshCollection) mapValue(ctx context.Context, want string) (metadataAggregateNode, error) {
	var found metadataAggregateNode
	err := n.logicalMapEntries(ctx, func(key, value metadataAggregateNode) error {
		match, err := metadataAggregateStringEqual(ctx, key, want)
		if match {
			found = value
			return errMetadataAggregateStop
		}
		return err
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	return found, err
}
func (n metadataAggregateRefreshCollection) mapValues(ctx context.Context, visit func(metadataAggregateNode) error) error {
	return n.logicalMapEntries(ctx, func(_, value metadataAggregateNode) error { return visit(value) })
}
func (n metadataAggregateRefreshCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		if n.base.nodeType().Kind() == reflect.Map {
			return visit(key, metadataAggregateRefreshCollection{source: n.source, field: n.field, base: child})
		}
		return visit(key, metadataAggregateRefreshRecord{source: n.source, field: n.field, base: child})
	})
}
func (n metadataAggregateRefreshCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}

type metadataAggregateRefreshRecord struct {
	source *metadataLogicalSource
	field  string
	base   metadataAggregateNode
}

func (n metadataAggregateRefreshRecord) nodeType() reflect.Type { return n.base.nodeType() }
func (n metadataAggregateRefreshRecord) logicalBase() metadataLogicalNode {
	return n.base.(metadataAggregateStoredNode).value
}
func (n metadataAggregateRefreshRecord) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		replace := false
		switch n.field {
		case "members", "projectUpdates", "initiativeUpdates", "comments":
			replace = key.String() == "User"
		case "projects":
			replace = key.String() == "Lead" || key.String() == "Creator"
		case "initiatives":
			replace = key.String() == "Creator" || key.String() == "Owner"
		case "activities", "notifications":
			replace = key.String() == "Actor"
		}
		if replace {
			refreshed, err := metadataAggregateRefreshUser(ctx, n.source, child)
			if err != nil {
				return err
			}
			child = refreshed
		}
		return visit(key, child)
	})
}
func (n metadataAggregateRefreshRecord) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}

type metadataAggregatePointerNode struct {
	typ   reflect.Type
	child metadataAggregateNode
}

func (n metadataAggregatePointerNode) nodeType() reflect.Type { return n.typ }
func (n metadataAggregatePointerNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return visit(reflect.Value{}, n.child)
}
func (n metadataAggregatePointerNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	child, err := n.child.nodeValue(ctx, limit)
	if err != nil {
		return reflect.Value{}, err
	}
	value := reflect.New(n.typ.Elem())
	value.Elem().Set(child)
	return value, nil
}

func metadataAggregateRefreshUser(ctx context.Context, source *metadataLogicalSource, original metadataAggregateNode) (metadataAggregateNode, error) {
	unwrapped, err := metadataAggregateUnwrap(ctx, original, true)
	if err != nil || unwrapped == nil {
		return original, err
	}
	id, err := metadataAggregateChild(ctx, unwrapped, "ID")
	if err != nil || id == nil {
		return original, err
	}
	found, err := metadataAggregateLookupUser(ctx, source, id)
	if err != nil || found == nil {
		return original, err
	}
	if original.nodeType().Kind() == reflect.Pointer {
		return metadataAggregatePointerNode{original.nodeType(), found}, nil
	}
	return found, nil
}

func metadataAggregateLookupUser(ctx context.Context, source *metadataLogicalSource, id metadataAggregateNode) (metadataAggregateNode, error) {
	if source.units != nil && id != nil && id.nodeType().Kind() == reflect.String {
		field, _, _ := metadataLogicalField("users")
		if !source.fields[field].blocked {
			return metadataAggregateIndexedLookupUser(ctx, source.unitCorpus(ctx), id)
		}
	}
	var found metadataAggregateNode
	err := source.VisitFields(ctx, func(name string, users metadataLogicalNode) error {
		if name != "users" {
			return nil
		}
		return users.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
			candidate := metadataAggregateStoredNode{user}
			userID, err := metadataAggregateChild(ctx, candidate, "ID")
			if err != nil {
				return err
			}
			equal, err := metadataAggregateStringsEqual(ctx, id, userID)
			if equal {
				found = candidate
			}
			return err
		})
	})
	return found, err
}

func metadataAggregateStringsEqual(ctx context.Context, left, right metadataAggregateNode) (bool, error) {
	if left == nil || right == nil || left.nodeType().Kind() != reflect.String || right.nodeType().Kind() != reflect.String {
		return false, nil
	}
	if native, ok := left.(metadataAggregateNativeNode); ok {
		return metadataAggregateStringEqual(ctx, right, native.value.String())
	}
	if native, ok := right.(metadataAggregateNativeNode); ok {
		return metadataAggregateStringEqual(ctx, left, native.value.String())
	}
	a, aOK := left.(metadataAggregateStoredNode)
	b, bOK := right.(metadataAggregateStoredNode)
	if !aOK || !bOK {
		return false, ErrMetadataRecordQuery
	}
	equal := true
	err := a.value.StreamBytes(ctx, func(a io.Reader) error {
		return b.value.StreamBytes(ctx, func(b io.Reader) error {
			var x, y [4096]byte
			for {
				n, errA := io.ReadFull(a, x[:])
				m, errB := io.ReadFull(b, y[:])
				if errA != nil && !errors.Is(errA, io.EOF) && !errors.Is(errA, io.ErrUnexpectedEOF) {
					return errA
				}
				if errB != nil && !errors.Is(errB, io.EOF) && !errors.Is(errB, io.ErrUnexpectedEOF) {
					return errB
				}
				if n != m || !bytes.Equal(x[:n], y[:m]) {
					equal = false
					return nil
				}
				if errA != nil || errB != nil {
					equal = (errA != nil) == (errB != nil)
					return nil
				}
			}
		})
	})
	return equal, err
}

func metadataAggregateMaterializeChildren(ctx context.Context, node metadataAggregateNode, limit int) (reflect.Value, error) {
	cost, err := metadataAggregateNodeCost(ctx, node)
	if err != nil {
		return reflect.Value{}, err
	}
	if limit < 0 || cost > uint64(limit) {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	kind := node.nodeType()
	result := reflect.New(kind).Elem()
	if kind.Kind() == reflect.Map || kind.Kind() == reflect.Slice {
		nilValue := false
		if base, ok := node.(metadataLogicalTransformBase); ok {
			nilValue, err = base.logicalBase().isNil(ctx)
			if err != nil {
				return reflect.Value{}, err
			}
		}
		if !nilValue {
			if kind.Kind() == reflect.Map {
				result.Set(reflect.MakeMap(kind))
			} else {
				result.Set(reflect.MakeSlice(kind, 0, 0))
			}
		}
	}
	err = node.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		if kind.Kind() == reflect.Struct {
			if field, ok := kind.FieldByName(key.String()); ok && field.Tag.Get("json") == "-" {
				return nil
			}
		}
		value, err := child.nodeValue(ctx, limit)
		if err != nil {
			return err
		}
		switch kind.Kind() {
		case reflect.Struct:
			result.FieldByName(key.String()).Set(value)
		case reflect.Map:
			result.SetMapIndex(key, value)
		case reflect.Slice:
			result.Set(reflect.Append(result, value))
		default:
			return ErrMetadataRecordQuery
		}
		return nil
	})
	return result, err
}
