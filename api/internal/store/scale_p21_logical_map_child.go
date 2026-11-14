package store

import (
	"context"
	"errors"
	"reflect"

	"flow/api/internal/domain"
)

// MapChild returns a borrowed node under the caller's source pin. Top-level
// packed string maps use their exact native-key index. Typed FILE and nested
// maps preserve the existing bounded-memory scan behavior.
func (n metadataLogicalNode) MapChild(ctx context.Context, key string) (metadataLogicalNode, bool, error) {
	if err := ctx.Err(); err != nil {
		return metadataLogicalNode{}, false, err
	}
	if n.typ == nil || n.typ.Kind() != reflect.Map || n.typ.Key().Kind() != reflect.String {
		return metadataLogicalNode{}, false, ErrMetadataRecordQuery
	}
	if err := n.source.Retain(); err != nil {
		return metadataLogicalNode{}, false, err
	}
	defer n.source.Release()
	var result metadataLogicalNode
	found := false
	if n.collection != nil {
		r := n.collection.root
		name := jsonFieldName(reflect.TypeFor[domain.Bootstrap]().Field(n.collection.field))
		c := &sw29LogicalCorpus{
			index:  &sw29LogicalIndex{stage: r.arena.index.stage, seed: r.arena.index.seed},
			fields: map[string]metadataLogicalNode{name: n}, points: r.trees[1],
			unitRoot: r, unitArena: r.arena, unitContext: ctx,
		}
		err := c.Point(ctx, name, key, func(value metadataLogicalNode) error {
			if found {
				return ErrMetadataRecordQuery
			}
			result, found = value, true
			return nil
		})
		if err != nil {
			return metadataLogicalNode{}, false, err
		}
		return result, found, err
	}
	err := n.MapEntries(ctx, func(candidate, value metadataLogicalNode) error {
		match, err := candidate.StringEqual(ctx, key)
		if err != nil || !match {
			return err
		}
		result, found = value, true
		return errMetadataAggregateStop
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	if err != nil {
		return metadataLogicalNode{}, false, err
	}
	return result, found, err
}
