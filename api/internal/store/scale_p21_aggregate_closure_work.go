package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
)

// metadataAggregatePlan resolves both images before event allocation. Its
// reservation covers decoded winners, JSON escaping and encoder growth,
// normalized maps, the previous-values diff, and final realtime/webhook bytes.
// The caller acquires RequiredBytes without waiting inside its transaction
// and holds that allowance until its synchronous publication sinks return.
type metadataAggregatePlan struct {
	RequiredBytes, WinnerBytes int
	before, after              metadataAggregateNode
	previous                   bool
	sources                    []*metadataLogicalSource
}

func planMetadataAggregate(ctx context.Context, before, after metadataAggregateImage, id string, previous bool, payloadBytes int) (*metadataAggregatePlan, error) {
	if payloadBytes < 0 || payloadBytes > metadataWorkBytes/16 {
		return nil, ErrMetadataWorkBudget
	}
	return planMetadataAggregateWork(ctx, before, after, id, previous, uint64(payloadBytes)*16)
}

func planMetadataAggregatePayload(ctx context.Context, before, after metadataAggregateImage, id string, previous bool, payload []byte) (*metadataAggregatePlan, error) {
	payloadWork, err := metadataAggregatePayloadJSONWork(ctx, payload)
	if err != nil {
		return nil, err
	}
	return planMetadataAggregateWork(ctx, before, after, id, previous, payloadWork)
}

func planMetadataAggregateWork(ctx context.Context, before, after metadataAggregateImage, id string, previous bool, payloadWork uint64) (*metadataAggregatePlan, error) {
	plan := &metadataAggregatePlan{previous: previous && strings.TrimSpace(id) != ""}
	var err error
	if plan.previous {
		plan.before, err = metadataAggregateResolve(ctx, before, id)
		if err != nil {
			return nil, err
		}
	}
	plan.after, err = metadataAggregateResolve(ctx, after, id)
	if err != nil {
		return nil, err
	}
	oldCost, err := metadataAggregateNodeCost(ctx, plan.before)
	if err != nil {
		return nil, err
	}
	nextCost, err := metadataAggregateNodeCost(ctx, plan.after)
	if err != nil {
		return nil, err
	}
	// Source costs include doubled string/container storage plus per-node
	// overhead. Sixteen copies of that conservative charge cover the maximum
	// six-byte JSON escape expansion, marshal buffer growth, generic JSON
	// decode, diff maps and the separately encoded publication envelopes.
	const copies = uint64(16)
	base := uint64(128 << 10)
	if oldCost > metadataWorkBytes || nextCost > metadataWorkBytes || payloadWork > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	cost := base + (oldCost+nextCost)*copies + payloadWork
	if cost > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	plan.RequiredBytes = int(cost)
	// This is a materialization limit backed by the larger reservation,
	// rather than the former fixed encoded page limit.
	plan.WinnerBytes = int(max(oldCost, nextCost)) * 8
	for _, image := range []struct {
		node   metadataAggregateNode
		source *metadataLogicalSource
	}{{plan.before, before.Source}, {plan.after, after.Source}} {
		if image.node == nil || image.source == nil {
			continue
		}
		duplicate := false
		for _, source := range plan.sources {
			duplicate = duplicate || source == image.source
		}
		if duplicate {
			continue
		}
		if err := image.source.Retain(); err != nil {
			plan.Close()
			return nil, err
		}
		plan.sources = append(plan.sources, image.source)
	}
	return plan, nil
}

func (p *metadataAggregatePlan) Close() {
	for _, source := range p.sources {
		source.Release()
	}
	p.sources = nil
}

func (p *metadataAggregatePlan) Values(ctx context.Context) (json.RawMessage, any, error) {
	defer p.Close()
	var oldValue any
	var err error
	if p.previous {
		oldValue, err = metadataAggregateJSONNode(ctx, p.before, p.WinnerBytes)
		if err != nil {
			return nil, nil, err
		}
	}
	nextValue, err := metadataAggregateJSONNode(ctx, p.after, p.WinnerBytes)
	if err != nil {
		return nil, nil, err
	}
	previous := metadataAggregateDiffValues(oldValue, nextValue)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return previous, nextValue, nil
}

func metadataAggregateNodeCost(ctx context.Context, node metadataAggregateNode) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	switch value := node.(type) {
	case nil:
		return 0, nil
	case metadataAggregateStoredNode:
		return value.value.cost, nil
	case metadataAggregateNativeNode:
		return metadataAggregateNativeCost(ctx, value.value, 0)
	case metadataAggregateOverlayNode:
		cost, err := metadataAggregateNodeCost(ctx, value.base)
		if err != nil {
			return 0, err
		}
		selected, err := metadataAggregateNativeCost(ctx, value.selected, 0)
		return cost + selected, err
	case metadataAggregateRefreshCollection:
		return metadataAggregateChildrenCost(ctx, value)
	case metadataAggregateRefreshRecord:
		return metadataAggregateChildrenCost(ctx, value)
	case metadataAggregatePointerNode:
		child, err := metadataAggregateNodeCost(ctx, value.child)
		return uint64(value.typ.Size())*2 + 128 + child, err
	case metadataAggregateCompactNode:
		if stored, ok := value.base.(metadataAggregateStoredNode); ok {
			child, err := metadataAggregateChild(ctx, value.base, value.excluded)
			if err != nil {
				return 0, err
			}
			cost := stored.value.cost
			if excluded, ok := child.(metadataAggregateStoredNode); ok {
				cost -= excluded.value.cost
			}
			return cost, nil
		}
		if native, ok := value.base.(metadataAggregateNativeNode); ok {
			return metadataAggregateNativeStructCost(ctx, native.value, value.excluded, 0)
		}
	}
	return 0, ErrMetadataRecordQuery
}

func metadataAggregateChildrenCost(ctx context.Context, node metadataAggregateNode) (uint64, error) {
	cost := uint64(node.nodeType().Size())*2 + 128
	visit := func(child metadataAggregateNode) error {
		part, err := metadataAggregateNodeCost(ctx, child)
		if err != nil {
			return err
		}
		if part > metadataWorkBytes || cost > metadataWorkBytes-part {
			return ErrMetadataWorkBudget
		}
		cost += part
		return nil
	}
	var err error
	if collection, ok := node.(metadataAggregateRefreshCollection); ok && node.nodeType().Kind() == reflect.Map {
		err = collection.logicalMapEntries(ctx, func(key, value metadataAggregateNode) error {
			if err := visit(key); err != nil {
				return err
			}
			return visit(value)
		})
	} else {
		err = node.nodeChildren(ctx, func(_ reflect.Value, child metadataAggregateNode) error { return visit(child) })
	}
	return cost, err
}

func metadataAggregateNativeCost(ctx context.Context, value reflect.Value, depth int) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if depth >= metadataLogicalDepth {
		return 0, ErrMetadataWorkBudget
	}
	cost := uint64(value.Type().Size())*2 + 128
	add := func(child reflect.Value) error {
		part, err := metadataAggregateNativeCost(ctx, child, depth+1)
		if part > metadataWorkBytes || cost > metadataWorkBytes-part {
			return ErrMetadataWorkBudget
		}
		cost += part
		return err
	}
	switch value.Kind() {
	case reflect.String:
		cost += uint64(value.Len())*2 + 64
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			if err := add(value.Elem()); err != nil {
				return 0, err
			}
		}
	case reflect.Map:
		cost += uint64(value.Len()) * (uint64(value.Type().Elem().Size())*2 + 64)
		iter := value.MapRange()
		for iter.Next() {
			if err := add(iter.Key()); err != nil {
				return 0, err
			}
			if err := add(iter.Value()); err != nil {
				return 0, err
			}
		}
	case reflect.Slice, reflect.Array:
		cost += uint64(value.Len()) * (uint64(value.Type().Elem().Size())*2 + 64)
		if value.Type().Elem().Kind() != reflect.Uint8 {
			for i := 0; i < value.Len(); i++ {
				if err := add(value.Index(i)); err != nil {
					return 0, err
				}
			}
		}
	case reflect.Struct:
		return metadataAggregateNativeStructCost(ctx, value, "", depth)
	}
	if cost > metadataWorkBytes {
		return 0, ErrMetadataWorkBudget
	}
	return cost, nil
}

func metadataAggregateNativeStructCost(ctx context.Context, value reflect.Value, excluded string, depth int) (uint64, error) {
	cost := uint64(value.Type().Size())*2 + 128
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if field.PkgPath != "" || field.Tag.Get("json") == "-" || field.Name == excluded {
			continue
		}
		part, err := metadataAggregateNativeCost(ctx, value.Field(i), depth+1)
		if err != nil {
			return 0, err
		}
		if part > metadataWorkBytes || cost > metadataWorkBytes-part {
			return 0, ErrMetadataWorkBudget
		}
		cost += part
	}
	return cost, nil
}
