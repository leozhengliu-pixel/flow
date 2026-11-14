package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// The corpus borrows the image's immutable root. No lookup builds a second
// index or retains descriptors after its caller releases the source.
func metadataAggregateResolveIndexed(ctx context.Context, image metadataAggregateImage, id string) (metadataAggregateNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	corpus := image.Source.unitCorpus(ctx)
	if image.Selected == nil && len(image.ExcludedFields) == 0 {
		found, err := corpus.Resolve(ctx, id)
		if errors.Is(err, ErrBoundedMetadataCoverage) {
			return metadataAggregateResolveWalk(ctx, image, id)
		}
		return found, err
	}
	field, terminal := metadataAggregatePrefixField(id)
	var found metadataAggregateNode
	find := func(name string, node metadataAggregateNode, direct bool) error {
		var err error
		found, err = metadataAggregateIndexedField(ctx, corpus, name, node, id, direct)
		if err != nil {
			return err
		}
		if found != nil {
			return errMetadataAggregateStop
		}
		return nil
	}
	if field != "" {
		err := image.visit(ctx, func(name string, node metadataAggregateNode) error {
			if name != field {
				return nil
			}
			return find(name, node, true)
		})
		if errors.Is(err, errMetadataAggregateStop) {
			err = nil
		}
		if err != nil || found != nil || terminal {
			return found, err
		}
	}
	err := image.visit(ctx, func(name string, node metadataAggregateNode) error { return find(name, node, false) })
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	return found, err
}

func metadataAggregateIndexedDirect(ctx context.Context, node metadataAggregateNode, id string) (metadataAggregateNode, error) {
	var found metadataAggregateNode
	err := node.nodeChildren(ctx, func(_ reflect.Value, child metadataAggregateNode) error {
		field, ok := child.nodeType().FieldByName("ID")
		if !ok || field.Type.Kind() != reflect.String {
			return nil
		}
		idNode, err := metadataAggregateIDNode(ctx, child, field.Index)
		if err != nil {
			return err
		}
		match, err := metadataAggregateStringEqual(ctx, idNode, id)
		if match {
			found = child
			return errMetadataAggregateStop
		}
		return err
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	return found, err
}

func metadataAggregateIndexedField(ctx context.Context, corpus *sw29LogicalCorpus, name string, node metadataAggregateNode, id string, direct bool) (metadataAggregateNode, error) {
	if !direct && !aggregateSearchPlanFor(node.nodeType(), map[reflect.Type]bool{}).mayContain {
		return nil, nil
	}
	if compact, ok := node.(metadataAggregateCompactCollection); ok {
		found, err := metadataAggregateIndexedField(ctx, corpus, name, compact.base, id, direct)
		return metadataAggregateIndexedCompact(found), err
	}
	overlay, partial := node.(metadataAggregateOverlayNode)
	if _, stored := node.(metadataAggregateStoredNode); !stored && !partial {
		if direct {
			return metadataAggregateIndexedDirect(ctx, node, id)
		}
		return metadataAggregateFind(ctx, node, id)
	}
	if direct && !partial {
		var found metadataAggregateNode
		err := corpus.Point(ctx, name, id, func(value metadataLogicalNode) error {
			found = metadataAggregateStoredNode{value}
			return sw29LogicalIndexStop
		})
		return found, err
	}
	field, _, ok := metadataLogicalField(name)
	if !ok {
		return nil, ErrMetadataRecordQuery
	}
	fieldRank := sw29LogicalRank(nil, uint64(field+1))
	var best metadataAggregateNode
	var bestRank []byte
	offer := func(rank []byte, value metadataAggregateNode) {
		if value != nil && (best == nil || bytes.Compare(rank, bestRank) < 0) {
			best, bestRank = value, slices.Clone(rank)
		}
	}
	if partial {
		// Replacements inherit each original occurrence's position. New points
		// follow the base in the callback's selected collection order.
		points := append(slices.Clone(overlay.scope.Read), overlay.scope.Write...)
		seen := map[string]bool{}
		for _, point := range points {
			if point.Kind != name || seen[point.ID] {
				continue
			}
			seen[point.ID] = true
			value, exists := metadataAggregateIndexedSelectedRecord(overlay, point.ID)
			if !exists {
				continue
			}
			native := metadataAggregateNativeNode{value}
			var match metadataAggregateNode
			var err error
			if direct {
				idField, ok := value.Type().FieldByName("ID")
				if ok && idField.Type.Kind() == reflect.String && value.FieldByIndex(idField.Index).String() == id {
					match = native
				}
			} else {
				match, err = metadataAggregateFind(ctx, native, id)
			}
			if err != nil {
				return nil, err
			}
			if match == nil {
				continue
			}
			occurrences := 0
			err = corpus.pointRecords(ctx, name, point.ID, func(order uint64, _ metadataLogicalNode) error {
				occurrences++
				if overlay.selected.Kind() == reflect.Slice && overlay.pointOccurrence != nil && !overlay.pointOccurrence(name, point.ID, order) {
					return nil
				}
				offer(sw29LogicalRank(fieldRank, order+1), match)
				return nil
			})
			if err != nil {
				return nil, err
			}
			if occurrences == 0 {
				collection := corpus.fields[name].collection
				if collection == nil {
					return metadataAggregateFind(ctx, node, id)
				}
				order := uint64(0)
				if value.Kind() == reflect.Struct && overlay.selected.Kind() == reflect.Slice {
					for i := 0; i < overlay.selected.Len(); i++ {
						key, ok := metadataRecordValueKey(overlay.selected.Index(i))
						if ok && key == point.ID {
							order = uint64(i)
							break
						}
					}
				} else {
					order = uint64(slices.IndexFunc(points, func(key RecordKey) bool { return key == point }))
				}
				offer(sw29LogicalRank(fieldRank, collection.nextOrder+order+1), match)
			}
		}
		if !direct && overlay.selected.Kind() == reflect.Map && metadataRecordSelected(overlay.scope, name, "id", false) {
			idNode, err := overlay.mapValue(ctx, "id")
			if err != nil {
				return nil, err
			}
			idNode, err = metadataAggregateUnwrap(ctx, idNode, false)
			if err != nil {
				return nil, err
			}
			match, err := metadataAggregateStringEqual(ctx, idNode, id)
			if err != nil {
				return nil, err
			}
			if match {
				offer(fieldRank, overlay)
			}
		}
	}
	if direct {
		err := corpus.pointRecords(ctx, name, id, func(order uint64, value metadataLogicalNode) error {
			rank := sw29LogicalRank(fieldRank, order+1)
			masked, err := metadataAggregateIndexedMasked(ctx, corpus, name, rank, overlay)
			if err != nil {
				return err
			}
			if !masked {
				offer(rank, metadataAggregateStoredNode{value})
				return sw29LogicalIndexStop
			}
			return nil
		})
		return best, err
	}
	hash := corpus.hashString(id)
	lo := append(slices.Clone(hash[:]), fieldRank...)
	hi := append(slices.Clone(hash[:]), sw29LogicalPrefixEnd(fieldRank)...)
	err := corpus.unitRange(ctx, 2, corpus.aggregates, lo, hi, func(key, value []byte) error {
		rank := key[32:]
		if best != nil && bytes.Compare(rank, bestRank) >= 0 {
			return sw29LogicalIndexStop
		}
		masked, err := metadataAggregateIndexedMasked(ctx, corpus, name, rank, overlay)
		if err != nil || masked {
			return err
		}
		idNode, err := corpus.decode(value[corpus.locatorBytes():])
		if err != nil {
			return err
		}
		match, err := idNode.StringEqual(ctx, id)
		if err != nil || !match {
			return err
		}
		stored, err := corpus.decode(value)
		if err != nil {
			return err
		}
		var winner metadataAggregateNode = metadataAggregateStoredNode{stored}
		if partial && len(rank) == len(fieldRank) {
			winner = overlay
		}
		offer(rank, winner)
		return sw29LogicalIndexStop
	})
	if err != nil {
		return nil, err
	}
	// Opaque native nodes keep the original typed-source search behavior.
	// An index never converts an unsupported layout into a new failure.
	barrier := false
	err = corpus.unitRange(ctx, 3, corpus.barriers, fieldRank, sw29LogicalPrefixEnd(fieldRank), func(rank, _ []byte) error {
		if best != nil && bytes.Compare(rank, bestRank) > 0 {
			return sw29LogicalIndexStop
		}
		masked := false
		var err error
		if len(rank) > len(fieldRank) {
			masked, err = metadataAggregateIndexedMasked(ctx, corpus, name, rank, overlay)
		}
		if err != nil {
			return err
		}
		if !masked {
			barrier = true
			return sw29LogicalIndexStop
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if barrier {
		return metadataAggregateFind(ctx, node, id)
	}
	return metadataAggregateIndexedCompact(best), nil
}

func metadataAggregateIndexedMasked(ctx context.Context, corpus *sw29LogicalCorpus, name string, rank []byte, overlay metadataAggregateOverlayNode) (bool, error) {
	if overlay.base == nil {
		return false, nil
	}
	if len(rank) == 8 {
		return overlay.selected.Kind() == reflect.Map && metadataRecordSelected(overlay.scope, name, "id", false), nil
	}
	if len(rank) < 16 {
		return false, ErrMetadataRecordQuery
	}
	ordinal := binary.BigEndian.Uint64(rank[8:16])
	if ordinal == 0 {
		return false, ErrMetadataRecordQuery
	}
	ordinal--
	collection := corpus.fields[name].collection
	if collection == nil {
		return false, ErrMetadataRecordQuery
	}
	var matched bool
	key := metadataLogicalUnitsOrderKey(collection.field, ordinal)
	err := collection.root.Range(ctx, 0, key, sw29LogicalPrefixEnd(key), func(_, raw []byte) error {
		leaf, err := metadataLogicalUnitsDecodeLeaf(raw)
		if err != nil {
			return err
		}
		var id metadataLogicalNode
		switch collection.typ.Kind() {
		case reflect.Map:
			id, err = collection.root.node(ctx, leaf, collection.typ.Key(), true)
		case reflect.Slice:
			value, readErr := collection.root.node(ctx, leaf, collection.typ.Elem(), false)
			if readErr != nil {
				return readErr
			}
			field, ok := value.typ.FieldByName("ID")
			if !ok {
				return nil
			}
			idNode, readErr := metadataAggregateIDNode(ctx, metadataAggregateStoredNode{value}, field.Index)
			if readErr != nil {
				return readErr
			}
			if idNode == nil {
				return nil
			}
			id = idNode.(metadataAggregateStoredNode).value
		case reflect.Struct:
			if ordinal >= uint64(collection.typ.NumField()) {
				return ErrMetadataRecordQuery
			}
			matched = metadataRecordSelected(overlay.scope, name, jsonFieldName(collection.typ.Field(int(ordinal))), false)
			return nil
		default:
			return ErrMetadataRecordQuery
		}
		if err != nil {
			return err
		}
		for _, point := range append(slices.Clone(overlay.scope.Read), overlay.scope.Write...) {
			if point.Kind != name {
				continue
			}
			equal, err := id.StringEqual(ctx, point.ID)
			if err != nil {
				return err
			}
			if equal {
				matched = overlay.selected.Kind() != reflect.Slice || overlay.pointOccurrence == nil || overlay.pointOccurrence(name, point.ID, ordinal)
				break
			}
		}
		return nil
	})
	return matched, err
}

func metadataAggregateIndexedCompact(node metadataAggregateNode) metadataAggregateNode {
	if node == nil {
		return nil
	}
	if _, compact := node.(metadataAggregateCompactNode); compact {
		return node
	}
	if node.nodeType() == reflect.TypeFor[domain.ImportJob]() {
		return metadataAggregateCompactNode{base: node, excluded: "Rows"}
	}
	if node.nodeType() == reflect.TypeFor[domain.MigrationJob]() {
		return metadataAggregateCompactNode{base: node, excluded: "Bundle"}
	}
	return node
}

func metadataAggregateIndexedSelectedRecord(overlay metadataAggregateOverlayNode, id string) (reflect.Value, bool) {
	if overlay.selected.Kind() == reflect.Struct {
		for i := 0; i < overlay.selected.NumField(); i++ {
			if jsonFieldName(overlay.selected.Type().Field(i)) == id {
				return overlay.selected.Field(i), true
			}
		}
		return reflect.Value{}, false
	}
	return overlay.selectedRecord(id)
}

// The display pass uses the last duplicate user. Hash stored ID frames
// directly so even a giant ID never needs a newly allocated Go string.
func metadataAggregateIndexedLookupUser(ctx context.Context, corpus *sw29LogicalCorpus, id metadataAggregateNode) (metadataAggregateNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var hash [32]byte
	switch value := id.(type) {
	case metadataAggregateStoredNode:
		var err error
		hash, err = corpus.hash(ctx, value.value)
		if err != nil {
			return nil, err
		}
	case metadataAggregateNativeNode:
		hash = corpus.hashString(value.value.String())
	default:
		return nil, ErrMetadataRecordQuery
	}
	field, _, _ := metadataLogicalField("users")
	prefix := make([]byte, 34)
	binary.BigEndian.PutUint16(prefix[:2], uint16(field))
	copy(prefix[2:], hash[:])
	var found metadataAggregateNode
	err := corpus.unitRange(ctx, 1, corpus.points, prefix, sw29LogicalPrefixEnd(prefix), func(_, value []byte) error {
		candidateID, err := corpus.decode(value[corpus.locatorBytes():])
		if err != nil {
			return err
		}
		equal, err := metadataAggregateStringsEqual(ctx, id, metadataAggregateStoredNode{candidateID})
		if err != nil || !equal {
			return err
		}
		candidate, err := corpus.decode(value)
		if err == nil {
			found = metadataAggregateStoredNode{candidate}
		}
		return err
	})
	return found, err
}
