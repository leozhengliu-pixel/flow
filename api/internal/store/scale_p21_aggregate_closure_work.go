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
	chunked                    bool
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
	oldProfile, oldPlain, err := metadataAggregateJSONProfile(ctx, plan.before, 0)
	if err != nil {
		return nil, err
	}
	nextProfile, nextPlain, err := metadataAggregateJSONProfile(ctx, plan.after, 0)
	if err != nil {
		return nil, err
	}
	shell := max(oldProfile.shell, nextProfile.shell)
	// Keep the standard encoder's pooled shell finite independently of a
	// legal large string winner. Wider shells retain the existing allowance.
	if oldPlain && nextPlain && shell <= 2<<20 {
		unchanged, err := metadataAggregateSameFrame(ctx, plan.before, plan.after)
		if err != nil {
			return nil, err
		}
		if unchanged {
			plan.previous, plan.before = false, nil
			oldCost, oldProfile = 0, metadataAggregateJSONWork{}
		}
		// Native decoding and proxy normalization precede fixed-size output
		// encoding. Their temporary graphs do not coexist with both final
		// publication envelopes; charge the largest actual lifetime phase.
		normalizeOld := oldCost + oldProfile.generic + 3*oldProfile.shell
		// encoding/json pools its shell buffer. It can retain the larger
		// earlier shell through later phases, even after its raw copy dies.
		shell = max(oldProfile.shell, nextProfile.shell)
		normalizeNext := oldProfile.generic + nextCost + nextProfile.generic + nextProfile.shell + 2*shell
		diff := 2*oldProfile.generic + nextProfile.generic + oldProfile.encoded + 2*shell
		publish := oldProfile.encoded + nextProfile.generic + nextProfile.encoded + 2*shell
		// Packed readers retain two 32 KiB arena cache slots and at most
		// the current SQL chunk's driver/Scan copies. Neither escapes its
		// read, so the same fixed scratch covers FILE and packed sources.
		cost := uint64(2<<20) + payloadWork + max(normalizeOld, normalizeNext, diff, publish)
		if cost > metadataWorkBytes {
			return nil, ErrMetadataWorkBudget
		}
		plan.RequiredBytes = int(cost)
		plan.WinnerBytes = int(max(oldCost, nextCost))
		plan.chunked = true
	} else {
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
	}
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
	p.before, p.after = nil, nil
}

func (p *metadataAggregatePlan) Values(ctx context.Context) (json.RawMessage, any, error) {
	defer p.Close()
	var oldValue any
	var err error
	if p.previous {
		oldValue, err = p.normalized(ctx, p.before)
		if err != nil {
			return nil, nil, err
		}
	}
	nextValue, err := p.normalized(ctx, p.after)
	if err != nil {
		return nil, nil, err
	}
	var previous json.RawMessage
	if p.chunked {
		previous, err = metadataAggregateChunkedDiff(ctx, oldValue, nextValue)
	} else {
		previous = metadataAggregateDiffValues(oldValue, nextValue)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return previous, nextValue, nil
}

func (p *metadataAggregatePlan) normalized(ctx context.Context, node metadataAggregateNode) (any, error) {
	if !p.chunked {
		return metadataAggregateJSONNode(ctx, node, p.WinnerBytes)
	}
	if node == nil {
		return nil, ctx.Err()
	}
	value, err := node.nodeValue(ctx, p.WinnerBytes)
	if err != nil {
		return nil, err
	}
	// Stored node materialization already owns its decoded strings; native
	// callback values still need the legacy normalization's detached copy.
	_, owned := node.(metadataAggregateStoredNode)
	proxy := metadataAggregateJSONProxy{ownedStrings: owned}
	masked, err := proxy.value(ctx, value, 0)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(masked.Interface())
	if cancelled := ctx.Err(); cancelled != nil {
		return nil, cancelled
	}
	if err != nil {
		return nil, nil
	}
	var result any
	if json.Unmarshal(raw, &result) != nil {
		return nil, nil
	}
	return proxy.restore(ctx, result, 0)
}

func (p *metadataAggregatePlan) Realtime(ctx context.Context, payload json.RawMessage, entity any, event string) (json.RawMessage, error) {
	var fields map[string]any
	if len(payload) > 0 && json.Unmarshal(payload, &fields) != nil {
		var input any
		if json.Unmarshal(payload, &input) == nil {
			fields = map[string]any{"input": input}
		}
	}
	if fields == nil {
		fields = map[string]any{}
	}
	if entity != nil {
		fields["entity"] = entity
	}
	if strings.HasSuffix(event, ".deleted") {
		fields["deleted"] = true
	}
	return metadataAggregateMarshalNormalized(ctx, fields)
}

func metadataAggregateChunkedDiff(ctx context.Context, old, next any) (json.RawMessage, error) {
	if old == nil {
		return nil, ctx.Err()
	}
	oldMap, oldOK := old.(map[string]any)
	nextMap, nextOK := next.(map[string]any)
	if next == nil || !oldOK || !nextOK {
		return metadataAggregateMarshalNormalized(ctx, old)
	}
	diff := map[string]any{}
	for key, value := range oldMap {
		if changed, found := nextMap[key]; !found || !reflect.DeepEqual(value, changed) {
			diff[key] = value
		}
	}
	if len(diff) == 0 {
		return nil, ctx.Err()
	}
	return metadataAggregateMarshalNormalized(ctx, diff)
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
