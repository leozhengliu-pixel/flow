package store

import (
	"context"
	"reflect"
)

// Transient issue/content inputs are removed at the same boundary as
// collectionMetadata. Import input bodies are also request-only storage.
func metadataLogicalInstalledCandidate(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch name {
		case "issues", "comments", "activities", "notifications", "notificationDeliveries":
			return metadataAggregateNativeNode{reflect.Zero(node.typ)}, true, nil
		case "importJobs", "migrationJobs":
			return metadataLogicalCompactCollection{base: metadataAggregateStoredNode{node}, field: name}, true, nil
		default:
			return metadataAggregateStoredNode{node}, false, nil
		}
	})
}

type metadataLogicalCompactCollection struct {
	base  metadataAggregateNode
	field string
}

func (n metadataLogicalCompactCollection) nodeType() reflect.Type { return n.base.nodeType() }
func (n metadataLogicalCompactCollection) logicalBase() metadataLogicalNode {
	return n.base.(metadataAggregateStoredNode).value
}
func (n metadataLogicalCompactCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n metadataLogicalCompactCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		excluded := "Rows"
		if n.field == "migrationJobs" {
			excluded = "Bundle"
		}
		return visit(key, metadataLogicalCompactRecord{base: child, excluded: excluded})
	})
}

type metadataLogicalCompactRecord struct {
	base     metadataAggregateNode
	excluded string
}

func (n metadataLogicalCompactRecord) nodeType() reflect.Type { return n.base.nodeType() }
func (n metadataLogicalCompactRecord) logicalBase() metadataLogicalNode {
	return n.base.(metadataAggregateStoredNode).value
}
func (n metadataLogicalCompactRecord) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n metadataLogicalCompactRecord) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		if key.String() == n.excluded {
			child = metadataAggregateNativeNode{reflect.Zero(child.nodeType())}
		}
		return visit(key, child)
	})
}
