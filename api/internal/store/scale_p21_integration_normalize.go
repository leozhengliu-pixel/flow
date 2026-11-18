package store

import (
	"context"
	"reflect"
)

// The legacy normalizer only initializes these two connection slices.
// Preserve every other field as a frame, including large opaque payloads.
type integrationConnectionDefaultsNode struct{ base metadataLogicalNode }

func (n integrationConnectionDefaultsNode) nodeType() reflect.Type { return n.base.typ }
func (n integrationConnectionDefaultsNode) logicalBase() metadataLogicalNode {
	return n.base
}
func (n integrationConnectionDefaultsNode) nodeValue(context.Context, int) (reflect.Value, error) {
	return reflect.Value{}, ErrMetadataRecordQuery
}
func (n integrationConnectionDefaultsNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
		changed := map[string]reflect.Value{}
		for _, name := range []string{"Scopes", "Channels"} {
			field, err := issueHotField(ctx, item, name)
			if err != nil {
				return err
			}
			nilValue, err := field.isNil(ctx)
			if err != nil {
				return err
			}
			if nilValue {
				changed[name] = reflect.ValueOf([]string{})
			}
		}
		if len(changed) == 0 {
			return visit(key, metadataAggregateStoredNode{value: item})
		}
		return visit(key, legacyDiscussionNativeDelta{base: item, changed: changed})
	})
}
