package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// legacyActorImage owns a callback-native actor projection, not a persisted
// workspace. In particular Viewer/User Via remains available to the callback.
type legacyActorImage struct {
	owner *ResourceRead
}

// Source is borrowed until Close. A caller extending that lifetime must retain
// its own source reference before closing this image.
func (r *legacyActorImage) Source() *metadataLogicalSource {
	if r == nil || r.owner == nil {
		return nil
	}
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	if r.owner.closed {
		return nil
	}
	return r.owner.source
}

func (r *legacyActorImage) Close() {
	if r != nil {
		r.owner.Close()
	}
}

// prepareLegacyActorImage reproduces mutateFull's actor metadata step. The
// supplied source is authoritative; incidental context pins never select it.
func (s *SQLiteStore) prepareLegacyActorImage(ctx context.Context, source *metadataLogicalSource, actor *domain.User) (_ *legacyActorImage, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	result := &legacyActorImage{owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			result.Close()
		}
	}()
	if err := result.owner.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	result.owner.source = source
	if actor == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result, nil
	}
	// Only the selected actor enters native work. The user directory is
	// traversed as typed frames; unmatched strings and bodies are not decoded.
	for _, value := range []reflect.Value{reflect.ValueOf(*actor), reflect.ValueOf(actor.Via)} {
		cost, err := metadataAggregatePayloadEncodingWork(ctx, value, 0)
		if err != nil {
			return nil, err
		}
		if uint64(cost) > (metadataWorkBytes-4096)/2 {
			return nil, ErrMetadataWorkBudget
		}
		if err := result.owner.reserve(ctx, uint64(cost)*2+4096); err != nil {
			return nil, err
		}
	}
	scratch := metadataLogicalSourceScratchContext(ctx, source)
	selected, err := newMetadataLogicalBuilder(scratch)
	if err != nil {
		return nil, err
	}
	defer selected.Close()
	for _, storage := range source.stores {
		if err := selected.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	if err := selected.inheritTimestampInputs(ctx); err != nil {
		return nil, err
	}
	actorNode, err := selected.writeAggregateNode(legacyActorUserNode{value: reflect.ValueOf(*actor)}, 0)
	if err != nil {
		return nil, err
	}
	if actorNode.unavailable || actorNode.blocked {
		return nil, ErrBoundedMetadataCoverage
	}
	replacement := metadataAggregateStoredNode{value: actorNode}
	candidate, err := source.Transform(scratch, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch name {
		case "viewer":
			return replacement, true, nil
		case "users":
			return legacyActorUsersNode{base: original, actorID: actor.ID, actor: replacement}, true, nil
		default:
			return metadataAggregateStoredNode{value: original}, false, nil
		}
	})
	if err != nil {
		return nil, err
	}
	result.owner.source.Release()
	result.owner.source = candidate
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Native User encoding strips json:"-" fields. Enumerating the typed fields
// keeps Via in this temporary callback image without changing wire JSON.
type legacyActorUserNode struct {
	value reflect.Value
}

func (n legacyActorUserNode) nodeType() reflect.Type { return n.value.Type() }
func (n legacyActorUserNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyActorUserNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	for index := 0; index < n.value.NumField(); index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(reflect.ValueOf(n.value.Type().Field(index).Name), metadataAggregateNativeNode{value: n.value.Field(index)}); err != nil {
			return err
		}
	}
	return nil
}

type legacyActorUsersNode struct {
	base    metadataLogicalNode
	actorID string
	actor   metadataAggregateNode
}

func (n legacyActorUsersNode) nodeType() reflect.Type           { return n.base.typ }
func (n legacyActorUsersNode) logicalBase() metadataLogicalNode { return n.base }
func (n legacyActorUsersNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyActorUsersNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	replaced, ordinal := false, 0
	err := n.base.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
		var child metadataAggregateNode = metadataAggregateStoredNode{value: user}
		if !replaced {
			id, err := issueHotField(ctx, user, "ID")
			if err != nil {
				return err
			}
			equal, err := id.StringEqual(ctx, n.actorID)
			if err != nil {
				return err
			}
			if equal {
				replaced, child = true, n.actor
			}
		}
		if err := visit(reflect.ValueOf(ordinal), child); err != nil {
			return err
		}
		ordinal++
		return nil
	})
	if err != nil || replaced {
		return err
	}
	return visit(reflect.ValueOf(ordinal), n.actor)
}
