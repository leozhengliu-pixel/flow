package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// persistMetadataLogicalRefresh writes the old full mutation's display pass
// one record at a time. Callback-owned writes are handled by the scoped delta.
// Inline members use a separately admitted root-field delta. External content
// families still require their domain writer instead of a metadata-row write.
func (s *SQLiteStore) persistMetadataLogicalRefresh(ctx context.Context, tx *sqlTx, workspace string, original, refreshed *metadataLogicalSource, scope MetadataRecordScope, finalDirectory ...*metadataLogicalSource) error {
	if len(finalDirectory) > 1 {
		return ErrMetadataRecordQuery
	}
	directory := refreshed
	if len(finalDirectory) == 1 {
		directory = finalDirectory[0]
	} else if slices.Contains(metadataRecordFields(scope), "users") {
		return fmt.Errorf("%w: reference persistence needs the final user directory", ErrBoundedMetadataCoverage)
	}
	fields := map[string]metadataLogicalNode{}
	var shapes map[string]string
	checkShape := func(name string) error {
		if shapes == nil {
			root, closeRoot, err := s.metadataLogicalRootSnapshot(ctx, tx, workspace)
			if err != nil {
				return err
			}
			shapes = make(map[string]string, len(root.shapes))
			for name, shape := range root.shapes {
				shapes[name] = shape
			}
			closeRoot()
		}
		expected := "array"
		if updateMetadataField(name) {
			expected = "updates"
		}
		if shapes[name] != expected {
			return fmt.Errorf("%w: refreshed %s has no split canonical shape", ErrBoundedMetadataCoverage, name)
		}
		return nil
	}
	if err := refreshed.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		fields[name] = node
		return nil
	}); err != nil {
		return err
	}
	return original.VisitFields(ctx, func(name string, before metadataLogicalNode) error {
		switch name {
		case "members", "projects", "initiatives", "projectUpdates", "initiativeUpdates", "comments", "activities", "notifications":
		default:
			return nil
		}
		if slices.Contains(scope.Fields, name) {
			return nil
		}
		after := fields[name]
		if before.offset == after.offset && before.source == after.source {
			return nil
		}
		pairErr := metadataLogicalPairs(ctx, before, after, func(parentNode metadataLogicalNode, old, next metadataLogicalNode) error {
			changed, err := s.metadataLogicalReferencesChanged(ctx, name, old, next, directory)
			if err != nil || !changed {
				return err
			}
			switch name {
			case "members":
				if err := s.persistMetadataLogicalInlineField(ctx, tx, workspace, name, before, after); err != nil {
					return err
				}
				return errMetadataAggregateStop
			case "comments", "activities", "notifications":
				return fmt.Errorf("%w: refreshed %s needs its content writer", ErrBoundedMetadataCoverage, name)
			}
			if err := checkShape(name); err != nil {
				return err
			}
			idNode, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{old}, "ID")
			if err != nil {
				return err
			}
			if idNode == nil {
				return ErrBoundedMetadataCoverage
			}
			idValue, err := idNode.nodeValue(ctx, 1024)
			if err != nil {
				return err
			}
			id := idValue.String()
			if id == "" || len(id) > 191 {
				return ErrBoundedMetadataCoverage
			}
			if metadataRecordSelected(scope, name, id, true) {
				return nil
			}
			key := id
			parent := ""
			if updateMetadataField(name) {
				if parentNode.cost > metadataWorkBytes/8 {
					return ErrMetadataWorkBudget
				}
				_, releaseParent, err := s.tryAdmitAdditionalMetadataWork(ctx, int(parentNode.cost)*8+1024)
				if err != nil {
					return err
				}
				defer releaseParent()
				parentValue, err := parentNode.Value(ctx, int(parentNode.cost))
				if err != nil {
					return err
				}
				parent = parentValue.String()
				key = updateRecordKey(parent, id)
			}
			return s.persistMetadataLogicalRefreshRecord(ctx, tx, workspace, name, key, parent, old, next)
		})
		if errors.Is(pairErr, errMetadataAggregateStop) {
			return nil
		}
		return pairErr
	})
}

func (s *SQLiteStore) metadataLogicalReferencesChanged(ctx context.Context, field string, before, after metadataLogicalNode, finalDirectory *metadataLogicalSource) (bool, error) {
	changed := false
	err := before.Children(ctx, func(key reflect.Value, old metadataLogicalNode) error {
		reference := false
		switch field {
		case "members", "projectUpdates", "initiativeUpdates", "comments":
			reference = key.String() == "User"
		case "projects":
			reference = key.String() == "Lead" || key.String() == "Creator"
		case "initiatives":
			reference = key.String() == "Creator" || key.String() == "Owner"
		case "activities", "notifications":
			reference = key.String() == "Actor"
		}
		if !reference {
			return nil
		}
		next, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{after}, key.String())
		if err != nil {
			return err
		}
		// Normal record persistence canonicalizes display fields against the
		// final directory. When that directory still owns this ID, both
		// display versions compare equal and the old writer skips the row.
		if field != "members" {
			user, err := metadataAggregateUnwrap(ctx, next, true)
			if err != nil {
				return err
			}
			if user != nil {
				id, err := metadataAggregateChild(ctx, user, "ID")
				if err != nil {
					return err
				}
				current, err := metadataAggregateLookupUser(ctx, finalDirectory, id)
				if err != nil {
					return err
				}
				if current != nil {
					return nil
				}
			}
		}
		cost, err := metadataAggregateNodeCost(ctx, next)
		if err != nil {
			return err
		}
		required := (old.cost+cost)*8 + 32<<10
		if required > metadataWorkBytes {
			return ErrMetadataWorkBudget
		}
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(required))
		if err != nil {
			return err
		}
		defer release()
		a, err := old.Value(ctx, int(required))
		if err != nil {
			return err
		}
		b, err := next.nodeValue(ctx, int(required))
		if err != nil {
			return err
		}
		aRaw, err := json.Marshal(a.Interface())
		if err != nil {
			return err
		}
		bRaw, err := json.Marshal(b.Interface())
		if err != nil {
			return err
		}
		changed = changed || !bytes.Equal(StripJSONNUL(aRaw), StripJSONNUL(bRaw))
		return nil
	})
	return changed, err
}

func (s *SQLiteStore) persistMetadataLogicalRefreshRecord(ctx context.Context, tx *sqlTx, workspace, field, key, parent string, before, after metadataLogicalNode) error {
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	where := s.metadataRecordKeyEquals("workspace_key", "?") + " AND " +
		s.metadataRecordKeyEquals("field", "?") + " AND " + s.metadataRecordKeyEquals("record_key", "?")
	var matches int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE "+where, workspace, field, key).Scan(&matches); err != nil {
		return err
	}
	if matches != 1 {
		return fmt.Errorf("%w: refreshed %s has an ambiguous canonical locator", ErrBoundedMetadataCoverage, field)
	}
	if tx.dialect == "sqlite" {
		var storage string
		if err := tx.QueryRowContext(ctx, "SELECT typeof(record_key) FROM workspace_metadata_records WHERE "+where, workspace, field, key).Scan(&storage); err != nil {
			return err
		}
		if storage != "text" {
			return fmt.Errorf("%w: refreshed %s requires its physical row locator", ErrBoundedMetadataCoverage, field)
		}
	}
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT LENGTH(data) FROM workspace_metadata_records WHERE "+where+lock, workspace, field, key).Scan(&size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: refreshed %s has no exact canonical locator", ErrBoundedMetadataCoverage, field)
		}
		return err
	}
	if size < 0 || size > metadataWorkBytes || before.cost+after.cost > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	required := (before.cost+after.cost+uint64(size))*16 + 128<<10
	if required > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(required))
	if err != nil {
		return err
	}
	defer release()
	var canonical []byte
	if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE "+where+lock, size, workspace, field, key).Scan(&canonical); err != nil {
		return err
	}
	if canonical == nil || int64(len(canonical)) != size {
		return ErrConcurrentWrite
	}
	a, err := before.Value(ctx, int(required))
	if err != nil {
		return err
	}
	b, err := after.Value(ctx, int(required))
	if err != nil {
		return err
	}
	oldRaw, err := json.Marshal(a.Interface())
	if err != nil {
		return err
	}
	nextRaw, err := json.Marshal(b.Interface())
	if err != nil {
		return err
	}
	if updateMetadataField(field) {
		oldRaw, err = json.Marshal(metadataUpdate{Parent: parent, Item: oldRaw})
		if err != nil {
			return err
		}
		nextRaw, err = json.Marshal(metadataUpdate{Parent: parent, Item: nextRaw})
		if err != nil {
			return err
		}
	}
	oldRaw, nextRaw = StripJSONNUL(oldRaw), StripJSONNUL(nextRaw)
	merged, conflict, err := mergeMetadataRecord(oldRaw, nextRaw, canonical, canonical)
	if err != nil {
		return err
	}
	if conflict {
		return ErrConcurrentWrite
	}
	if bytes.Equal(merged, canonical) {
		return nil
	}
	changes := []metadataRecordChange{{field: field, key: key, base: canonical, raw: merged}}
	return scaleP16WriteChanges(ctx, tx, workspace, newIssueReferences(domain.Bootstrap{}), changes)
}

type metadataLogicalPairCursor struct {
	node  metadataLogicalNode
	read  metadataLogicalCursor
	count uint64
	last  []byte
	root  *metadataLogicalUnitsRoot
}

func newMetadataLogicalPairCursor(ctx context.Context, node metadataLogicalNode) (*metadataLogicalPairCursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if node.collection != nil {
		if err := node.collection.root.Retain(); err != nil {
			return nil, err
		}
		return &metadataLogicalPairCursor{node: node, count: node.collection.count, root: node.collection.root}, nil
	}
	c := &metadataLogicalPairCursor{node: node, read: metadataLogicalCursor{node: node, offset: node.offset + 16, ctx: ctx}}
	present, err := c.read.byte()
	if err != nil || present == 0 {
		return c, err
	}
	c.count, err = c.read.uint()
	return c, err
}

func (c *metadataLogicalPairCursor) Close() {
	if c.root != nil {
		c.root.Release()
		c.root = nil
	}
}

func (c *metadataLogicalPairCursor) next(ctx context.Context) (metadataLogicalNode, metadataLogicalNode, error) {
	if c.node.collection == nil {
		var key metadataLogicalNode
		var err error
		if c.node.typ.Kind() == reflect.Map {
			key, err = c.read.child(c.node.typ.Key())
			if err != nil {
				return key, metadataLogicalNode{}, err
			}
		}
		value, err := c.read.child(c.node.typ.Elem())
		return key, value, err
	}
	collection := c.node.collection
	prefix := metadataLogicalUnitsOrderKey(collection.field, 0)[:2]
	lo := prefix
	if c.last != nil {
		lo = append(slices.Clone(c.last), 0)
	}
	var raw []byte
	err := c.root.Range(ctx, 0, lo, sw29LogicalPrefixEnd(prefix), func(key, value []byte) error {
		c.last, raw = slices.Clone(key), slices.Clone(value)
		return sw29LogicalIndexStop
	})
	if err != nil {
		return metadataLogicalNode{}, metadataLogicalNode{}, err
	}
	if raw == nil {
		return metadataLogicalNode{}, metadataLogicalNode{}, io.ErrUnexpectedEOF
	}
	leaf, err := metadataLogicalUnitsDecodeLeaf(raw)
	if err != nil {
		return metadataLogicalNode{}, metadataLogicalNode{}, err
	}
	var key metadataLogicalNode
	if c.node.typ.Kind() == reflect.Map {
		key, err = c.root.node(ctx, leaf, c.node.typ.Key(), true)
		if err != nil {
			return key, metadataLogicalNode{}, err
		}
	}
	value, err := c.root.node(ctx, leaf, c.node.typ.Elem(), false)
	return key, value, err
}

func metadataLogicalPairs(ctx context.Context, before, after metadataLogicalNode, visit func(metadataLogicalNode, metadataLogicalNode, metadataLogicalNode) error) error {
	if before.typ != after.typ || before.typ.Kind() != reflect.Map && before.typ.Kind() != reflect.Slice {
		return ErrMetadataRecordQuery
	}
	a, err := newMetadataLogicalPairCursor(ctx, before)
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := newMetadataLogicalPairCursor(ctx, after)
	if err != nil {
		return err
	}
	defer b.Close()
	if a.count != b.count {
		return ErrMetadataRecordQuery
	}
	for range a.count {
		parent, old, err := a.next(ctx)
		if err != nil {
			return err
		}
		nextKey, next, err := b.next(ctx)
		if err != nil {
			return err
		}
		if before.typ.Kind() == reflect.Map {
			equal, err := metadataAggregateStringsEqual(ctx, metadataAggregateStoredNode{parent}, metadataAggregateStoredNode{nextKey})
			if err != nil {
				return err
			}
			if !equal {
				return ErrMetadataRecordQuery
			}
		}
		if before.typ.Kind() == reflect.Map {
			if err := metadataLogicalPairs(ctx, old, next, func(_ metadataLogicalNode, old, next metadataLogicalNode) error { return visit(parent, old, next) }); err != nil {
				return err
			}
		} else if err := visit(parent, old, next); err != nil {
			return err
		}
	}
	return nil
}
