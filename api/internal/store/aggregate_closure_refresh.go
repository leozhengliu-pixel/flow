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
		if metadataAggregateRefreshReplaces(n.field, key.String()) {
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
	var found metadataAggregateNode
	err := source.VisitFields(ctx, func(name string, users metadataLogicalNode) error {
		if name != "users" {
			return nil
		}
		if want, plain, err := metadataAggregatePlainString(ctx, id); err != nil {
			return err
		} else if plain {
			// The scan's result from the users frame's ID index.
			index, ok, err := metadataAggregateUserIndexOf(ctx, users)
			if err != nil {
				return err
			}
			if ok {
				if users := index.users[want]; len(users) != 0 {
					found = metadataAggregateStoredNode{users[len(users)-1].node}
				}
				return nil
			}
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

// metadataAggregateRefreshReplaces reports whether the display refresh of a
// field's record substitutes its member name with the directory's user.
func metadataAggregateRefreshReplaces(field, name string) bool {
	switch field {
	case "members", "projectUpdates", "initiativeUpdates", "comments":
		return name == "User"
	case "projects":
		return name == "Lead" || name == "Creator"
	case "initiatives":
		return name == "Creator" || name == "Owner"
	case "activities", "notifications":
		return name == "Actor"
	}
	return false
}

// unchanged reports whether refreshing the record changes nothing: every
// user it substitutes is (strictly, frame by frame) the user the record
// already holds, so the refreshed record decodes exactly as the stored one.
// stored is that record. A false answer only means the record is written
// as before.
func (n metadataAggregateRefreshRecord) unchanged(ctx context.Context) (metadataAggregateStoredNode, bool, error) {
	stored, ok := n.base.(metadataAggregateStoredNode)
	if !ok || stored.value.typ.Kind() != reflect.Struct || stored.value.blocked || stored.value.unavailable || stored.value.generic {
		return stored, false, nil
	}
	same := true
	err := stored.value.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if !same || !metadataAggregateRefreshReplaces(n.field, key.String()) {
			return nil
		}
		original := metadataAggregateStoredNode{child}
		refreshed, err := metadataAggregateRefreshUser(ctx, n.source, original)
		if err != nil {
			return err
		}
		if kept, ok := refreshed.(metadataAggregateStoredNode); ok && kept.value == child {
			// Nothing substituted (no user, or none found).
			return nil
		}
		held, found := original.value, metadataAggregateNode(nil)
		switch value := refreshed.(type) {
		case metadataAggregatePointerNode:
			// The record's pointer holds the user the directory replaces.
			pointee, err := metadataAggregateUnwrap(ctx, original, true)
			if err != nil {
				return err
			}
			stored, ok := pointee.(metadataAggregateStoredNode)
			if !ok {
				same = false
				return nil
			}
			held, found = stored.value, value.child
		default:
			found = value
		}
		user, ok := found.(metadataAggregateStoredNode)
		if !ok {
			same = false
			return nil
		}
		equal, err := legacyMetadataWriteEqualMode(ctx, held, user.value, 0, true)
		if err != nil && ctx.Err() == nil {
			// Frames the comparison does not read (opaque, unavailable):
			// the record is written as before.
			equal, err = false, nil
		}
		same = equal
		return err
	})
	if err != nil {
		return stored, false, err
	}
	return stored, same, nil
}

// unchanged reports whether no record of the collection changes when
// refreshed (unchanged records): the display pass then keeps the stored
// collection instead of rewriting it.
func (n metadataAggregateRefreshCollection) unchanged(ctx context.Context) (bool, error) {
	stored, ok := n.base.(metadataAggregateStoredNode)
	if !ok || stored.value.blocked || stored.value.unavailable || stored.value.generic {
		return false, nil
	}
	same := true
	err := n.nodeChildren(ctx, func(_ reflect.Value, child metadataAggregateNode) error {
		if !same {
			return errMetadataAggregateStop
		}
		var err error
		switch value := child.(type) {
		case metadataAggregateRefreshRecord:
			_, same, err = value.unchanged(ctx)
		case metadataAggregateRefreshCollection:
			same, err = value.unchanged(ctx)
		default:
			same = false
		}
		return err
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	if err != nil {
		return false, err
	}
	return same, nil
}
