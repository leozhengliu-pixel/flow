package store

import (
	"context"
	"encoding/json"
	"reflect"

	"flow/api/internal/domain"
)

func selectedIssueMetadataChanges(before, after domain.Bootstrap, keys []RecordKey) ([]metadataRecordChange, error) {
	type pair struct{ old, next metadataCollection }
	fields := map[string]pair{}
	var result []metadataRecordChange
	for _, key := range keys {
		collections, found := fields[key.Kind]
		if !found {
			field, _, ok := metadataRecordType(key.Kind)
			if !ok {
				return nil, ErrMetadataRecordQuery
			}
			collections = pair{
				newMetadataCollection(field, reflect.ValueOf(before).Field(field.index)),
				newMetadataCollection(field, reflect.ValueOf(after).Field(field.index)),
			}
			fields[key.Kind] = collections
		}
		a, existed := collections.old.record(key.ID)
		b, exists := collections.next.record(key.ID)
		if !existed || !exists || metadataValueEqual(a, b) {
			continue
		}
		base, err := json.Marshal(a.Interface())
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(b.Interface())
		if err != nil {
			return nil, err
		}
		result = append(result, metadataRecordChange{field: key.Kind, key: key.ID, base: base, raw: raw})
	}
	return result, nil
}

// Selected records retain their native ordinals. Canonical rebase can remove
// the first occurrence while later duplicate IDs remain in the inline field.
type issueMetadataPointCollection struct {
	base, next metadataLogicalNode
	before     reflect.Value
	keys       []string
}

func (n issueMetadataPointCollection) nodeType() reflect.Type           { return n.base.typ }
func (n issueMetadataPointCollection) logicalBase() metadataLogicalNode { return n.base }
func (n issueMetadataPointCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}

func (n issueMetadataPointCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	oldCounts := map[string]int{}
	replacements := map[string][]metadataLogicalNode{}
	seen := map[string]int{}
	for _, key := range n.keys {
		for index := 0; index < n.before.Len(); index++ {
			if id, _ := metadataRecordValueKey(n.before.Index(index)); id == key {
				oldCounts[key]++
			}
		}
		if err := n.next.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			id, err := issueHotField(ctx, record, "ID")
			if err != nil {
				return err
			}
			match, err := id.StringEqual(ctx, key)
			if err != nil {
				return err
			}
			if match {
				replacements[key] = append(replacements[key], record)
			}
			return nil
		}); err != nil {
			return err
		}
		if len(replacements[key]) > oldCounts[key] {
			return ErrMetadataRecordQuery
		}
	}
	ordinal := 0
	return n.base.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		id, err := issueHotField(ctx, record, "ID")
		if err != nil {
			return err
		}
		child := metadataAggregateNode(metadataAggregateStoredNode{record})
		for _, key := range n.keys {
			match, err := id.StringEqual(ctx, key)
			if err != nil {
				return err
			}
			if !match {
				continue
			}
			position := seen[key]
			seen[key]++
			position -= oldCounts[key] - len(replacements[key])
			if position < 0 {
				return nil
			}
			if position >= len(replacements[key]) {
				return ErrMetadataRecordQuery
			}
			child = metadataAggregateStoredNode{replacements[key][position]}
			break
		}
		err = visit(reflect.ValueOf(ordinal), child)
		ordinal++
		return err
	})
}

func selectedIssueMetadataPostimage(ctx context.Context, source *metadataLogicalSource, before, after domain.Bootstrap, keys []RecordKey) (*metadataLogicalSource, error) {
	left, right := reflect.ValueOf(before), reflect.ValueOf(after)
	builder, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	defer builder.Close()
	prepared := map[string]metadataLogicalNode{}
	for _, name := range []string{"projects", "documents"} {
		index, _, _ := metadataLogicalField(name)
		value := right.Field(index)
		// Selected native inputs were validated by Value. Rebase adds only
		// standard-JSON times; progress adds UTC grid points.
		if err := builder.auditJSONLocations(value); err != nil {
			return nil, err
		}
		node, err := builder.writeValue(value, 0, false)
		if err != nil {
			return nil, err
		}
		prepared[name] = node
	}
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		var ids []string
		for _, key := range keys {
			if key.Kind == name {
				ids = append(ids, key.ID)
			}
		}
		if len(ids) == 0 {
			return metadataAggregateStoredNode{node}, false, nil
		}
		index, _, _ := metadataLogicalField(name)
		return issueMetadataPointCollection{base: node, before: left.Field(index), next: prepared[name], keys: ids}, true, nil
	})
}
