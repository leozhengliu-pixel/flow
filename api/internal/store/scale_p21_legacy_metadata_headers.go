package store

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
)

func legacyMetadataHeaderLeaseWork() uint64 {
	// ResourceRead retains the lease, sync.Once release guard, closure and
	// old/replacement release-function slots until the row plan closes.
	return uint64(reflect.TypeFor[metadataWorkLease]().Size()+reflect.TypeFor[sync.Once]().Size()) +
		2*uint64(reflect.TypeFor[func()]().Size()) + 64
}

// The native ID survives in the row topology, but normalization and update-key
// encoding are temporary for one occurrence. Keep those lifetimes separate.
func (p *legacyMetadataWritePlan) headerID(ctx context.Context, work *ResourceRead, node metadataLogicalNode, parent string, update bool) (string, string, bool, error) {
	if err := work.reserve(ctx, node.cost); err != nil {
		return "", "", false, err
	}
	value, err := node.Value(ctx, metadataWorkBytes)
	if err != nil {
		return "", "", false, err
	}
	id := value.String()
	encoding, err := metadataAggregateStringEncodingWork(ctx, id)
	if err != nil {
		return "", "", false, err
	}
	// Marshal owns its encoder/replacement/output buffers; Unmarshal owns
	// the normalized string plus an unquote scratch buffer of encoded width.
	if err := work.reserve(ctx, encoding+encoding/4+uint64(len(id))+128); err != nil {
		return "", "", false, err
	}
	encoded, err := json.Marshal(id)
	if err != nil {
		return "", "", false, err
	}
	var normalized string
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return "", "", false, err
	}
	full := id == "" || len(id) > 191 || normalized != id
	key := id
	if update {
		parentEncoding, err := metadataAggregateStringEncodingWork(ctx, parent)
		if err != nil {
			return "", "", false, err
		}
		// updateRecordKey marshals two strings, hashes that output and
		// formats the fixed 32-byte digest into its 64-byte key.
		if err := work.reserve(ctx, parentEncoding+encoding+2*uint64(reflect.TypeFor[string]().Size())+512); err != nil {
			return "", "", false, err
		}
		key = updateRecordKey(parent, id)
	}
	return id, key, full, nil
}

func (p *legacyMetadataWritePlan) retainHeaderRow(ctx context.Context, id, key string, newPosition bool) error {
	// Doubling covers the old and replacement slice backing arrays during
	// append growth. The ID is materialized once and retained by the row.
	cost := uint64(len(id)) + 2*uint64(reflect.TypeFor[legacyMetadataWriteRow]().Size()) + 64 + legacyMetadataHeaderLeaseWork()
	if newPosition {
		// A map bucket holds key/value slots, top-hash/overflow bookkeeping
		// and at most one replacement bucket during growth.
		cost += 2 * (uint64(reflect.TypeFor[string]().Size()+reflect.TypeFor[int]().Size()) + 16)
		cost += uint64(len(key))
	}
	return p.work.reserve(ctx, cost)
}

func (p *legacyMetadataWritePlan) retainHeaderOrder(ctx context.Context) error {
	return p.work.reserve(ctx, 2*uint64(reflect.TypeFor[int]().Size())+32+legacyMetadataHeaderLeaseWork())
}

func (p *legacyMetadataWritePlan) headerParent(ctx context.Context, node metadataLogicalNode, next bool) (string, error) {
	if err := p.work.reserve(ctx, node.cost+legacyMetadataHeaderLeaseWork()); err != nil {
		return "", err
	}
	// Both parent maps own one key and node value. Before also retains the
	// parent order and nil-map entries. Include map growth for each slot.
	cost := 2 * (uint64(reflect.TypeFor[string]().Size()+reflect.TypeFor[metadataLogicalNode]().Size()) + 16)
	if !next {
		cost += 2*(uint64(reflect.TypeFor[string]().Size()+reflect.TypeFor[bool]().Size())+16) +
			2*uint64(reflect.TypeFor[string]().Size()) + 32
	}
	if err := p.work.reserve(ctx, cost+legacyMetadataHeaderLeaseWork()); err != nil {
		return "", err
	}
	key, err := node.Value(ctx, metadataWorkBytes)
	if err != nil {
		return "", err
	}
	return key.String(), nil
}
