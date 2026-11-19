package store

import (
	"context"
	"reflect"
)

// ID indexes of installed collection frames.
//
// The display refresh (refreshLegacyDisplayImage) replaces every embedded
// user of a project, member, update, comment, activity and notification by
// the users field's record with the same ID. Each lookup scanned the users
// field and compared every ID: a refreshed projects collection cost
// projects x users comparisons, on each HOT write that reads it. An issue
// edit likewise scanned every project to select the issue's project. The
// frames of an installed generation do not change, so a collection's IDs are
// indexed once per frame (the storage's frame memo): a lookup is a map read
// with the scan's result (the user lookup's last match; a selection's
// matches in collection order).

// metadataLogicalUserIndexLimit bounds the records an index holds (larger
// collections are scanned as before).
const metadataLogicalUserIndexLimit = 1 << 16

// metadataLogicalIndexed is one record of an indexed collection and its
// position.
type metadataLogicalIndexed struct {
	position int
	node     metadataLogicalNode
}

// metadataLogicalUserIndex maps a collection frame's IDs to its records with
// that ID, in collection order; complete reports whether every ID was a
// plain string (else the lookup scans).
type metadataLogicalUserIndex struct {
	size     int64
	typ      reflect.Type
	complete bool
	users    map[string][]metadataLogicalIndexed
}

func (s *metadataLogicalStorage) userIndex(node metadataLogicalNode) (metadataLogicalUserIndex, bool) {
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	index, found := s.userIndexes[node.offset]
	if !found || index.size != node.size || index.typ != node.typ {
		return metadataLogicalUserIndex{}, false
	}
	return index, true
}

func (s *metadataLogicalStorage) storeUserIndex(node metadataLogicalNode, index metadataLogicalUserIndex) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	if s.closed || node.offset+node.size > s.logicalSizeLocked() {
		return
	}
	if s.userIndexes == nil {
		s.userIndexes = map[int64]metadataLogicalUserIndex{}
	}
	s.userIndexes[node.offset] = index
}

// metadataLogicalPlainString reads a stored string node's value when it is
// framed as a plain string (not generic JSON, available, not opaque).
func metadataLogicalPlainString(ctx context.Context, n metadataLogicalNode) (string, bool, error) {
	if n.typ == nil || n.typ.Kind() != reflect.String || n.generic || n.unavailable || n.blocked || n.source == nil {
		return "", false, nil
	}
	if err := n.source.Retain(); err != nil {
		return "", false, err
	}
	defer n.source.Release()
	c := metadataLogicalCursor{n, n.offset + 16, ctx}
	length, err := c.uint()
	if err != nil {
		return "", false, err
	}
	if length > uint64(n.size) {
		return "", false, nil
	}
	data := make([]byte, int(length))
	if err := c.read(data); err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// metadataAggregatePlainString is the string an ID node holds when it is
// native or a plain stored string.
func metadataAggregatePlainString(ctx context.Context, node metadataAggregateNode) (string, bool, error) {
	if node == nil || node.nodeType().Kind() != reflect.String {
		return "", false, nil
	}
	switch value := node.(type) {
	case metadataAggregateNativeNode:
		return value.value.String(), true, ctx.Err()
	case metadataAggregateStoredNode:
		return metadataLogicalPlainString(ctx, value.value)
	}
	return "", false, nil
}

// metadataAggregateUserIndexOf returns the ID index of a collection frame of
// records with an ID (users, projects), built on first use; ok is false when
// the lookup must scan.
func metadataAggregateUserIndexOf(ctx context.Context, users metadataLogicalNode) (metadataLogicalUserIndex, bool, error) {
	if users.source == nil || users.generic || users.blocked || users.typ == nil || users.typ.Kind() != reflect.Slice {
		return metadataLogicalUserIndex{}, false, nil
	}
	if index, found := users.source.userIndex(users); found {
		return index, index.complete, nil
	}
	index := metadataLogicalUserIndex{size: users.size, typ: users.typ, complete: true, users: map[string][]metadataLogicalIndexed{}}
	count := 0
	err := users.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
		position := count
		count++
		if count > metadataLogicalUserIndexLimit {
			index.complete = false
			return errMetadataAggregateStop
		}
		id, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{user}, "ID")
		if err != nil {
			return err
		}
		if id == nil || id.nodeType().Kind() != reflect.String {
			// The scan never matches such a user.
			return nil
		}
		value, ok, err := metadataAggregatePlainString(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			index.complete = false
			return errMetadataAggregateStop
		}
		user.window = nil // (the memo keeps no read chunk alive)
		index.users[value] = append(index.users[value], metadataLogicalIndexed{position: position, node: user})
		return nil
	})
	if err != nil && err != errMetadataAggregateStop {
		return metadataLogicalUserIndex{}, false, err
	}
	if !index.complete {
		index.users = nil
	}
	users.source.storeUserIndex(users, index)
	return index, index.complete, nil
}
