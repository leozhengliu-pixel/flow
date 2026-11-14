package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// metadataAggregateImage describes the legacy event image, independently of
// the callback's partial Bootstrap. The caller owns Source and admission for
// the returned JSON and its decoded representation until publication ends.
// Selected overlays complete fields and declared points on that exact source
// generation. ExcludedFields describes collections the legacy writer omitted.
type metadataAggregateImage struct {
	Source         *metadataLogicalSource
	Selected       *domain.Bootstrap
	Scope          MetadataRecordScope
	ExcludedFields []string
	// Nil overlays every matching point. Occurrence-aware adapters bind this
	// optional slice filter to the exact source generation before planning.
	PointOccurrence func(field, id string, ordinal uint64) bool
}

type metadataAggregateNode interface {
	nodeType() reflect.Type
	nodeValue(context.Context, int) (reflect.Value, error)
	nodeChildren(context.Context, func(reflect.Value, metadataAggregateNode) error) error
}

// Map keys are irrelevant when descending to children. Keep their framed
// source nodes so a giant unrelated property name never needs decoding.
type metadataAggregateMapNode interface {
	mapValue(context.Context, string) (metadataAggregateNode, error)
	mapValues(context.Context, func(metadataAggregateNode) error) error
}

type metadataAggregateStoredNode struct{ value metadataLogicalNode }

func (n metadataAggregateStoredNode) nodeType() reflect.Type { return n.value.Type() }
func (n metadataAggregateStoredNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return n.value.Value(ctx, limit)
}
func (n metadataAggregateStoredNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.value.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
		return visit(key, metadataAggregateStoredNode{value})
	})
}

func (n metadataAggregateStoredNode) mapValue(ctx context.Context, want string) (metadataAggregateNode, error) {
	var found metadataAggregateNode
	err := n.value.MapEntries(ctx, func(key, value metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, want)
		if err != nil {
			return err
		}
		if match {
			found = metadataAggregateStoredNode{value}
			return errMetadataAggregateStop
		}
		return nil
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	return found, err
}

func (n metadataAggregateStoredNode) mapValues(ctx context.Context, visit func(metadataAggregateNode) error) error {
	return n.value.MapEntries(ctx, func(_, value metadataLogicalNode) error {
		return visit(metadataAggregateStoredNode{value})
	})
}

type metadataAggregateNativeNode struct{ value reflect.Value }

func (n metadataAggregateNativeNode) nodeType() reflect.Type { return n.value.Type() }
func (n metadataAggregateNativeNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	cost, err := metadataAggregateNativeCost(ctx, n.value, 0)
	if err != nil {
		return reflect.Value{}, err
	}
	if limit < 0 || cost > uint64(limit) {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	return n.value, nil
}
func (n metadataAggregateNativeNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value := n.value
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			return visit(reflect.Value{}, metadataAggregateNativeNode{value.Elem()})
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.PkgPath == "" {
				if err := visit(reflect.ValueOf(field.Name), metadataAggregateNativeNode{value.Field(i)}); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if err := visit(iter.Key(), metadataAggregateNativeNode{iter.Value()}); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := visit(reflect.ValueOf(i), metadataAggregateNativeNode{value.Index(i)}); err != nil {
				return err
			}
		}
	}
	return nil
}

var errMetadataAggregateStop = errors.New("metadata aggregate found")

func metadataAggregateChild(ctx context.Context, node metadataAggregateNode, name string) (metadataAggregateNode, error) {
	var found metadataAggregateNode
	err := node.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		if key.IsValid() && key.Kind() == reflect.String && key.String() == name {
			found = child
			return errMetadataAggregateStop
		}
		return nil
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	return found, err
}

func metadataAggregateUnwrap(ctx context.Context, node metadataAggregateNode, pointers bool) (metadataAggregateNode, error) {
	for node != nil && (node.nodeType().Kind() == reflect.Interface || pointers && node.nodeType().Kind() == reflect.Pointer) {
		var child metadataAggregateNode
		err := node.nodeChildren(ctx, func(_ reflect.Value, value metadataAggregateNode) error {
			child = value
			return errMetadataAggregateStop
		})
		if err != nil && !errors.Is(err, errMetadataAggregateStop) {
			return nil, err
		}
		node = child
	}
	return node, nil
}

func metadataAggregateIDNode(ctx context.Context, node metadataAggregateNode, path []int) (metadataAggregateNode, error) {
	for _, index := range path {
		var err error
		node, err = metadataAggregateUnwrap(ctx, node, true)
		if err != nil || node == nil {
			return nil, err
		}
		node, err = metadataAggregateChild(ctx, node, node.nodeType().Field(index).Name)
		if err != nil || node == nil {
			return nil, err
		}
	}
	return node, nil
}

func metadataAggregateStringEqual(ctx context.Context, node metadataAggregateNode, id string) (bool, error) {
	if node == nil || node.nodeType().Kind() != reflect.String {
		return false, nil
	}
	if stored, ok := node.(metadataAggregateStoredNode); ok {
		return stored.value.StringEqual(ctx, id)
	}
	if native, ok := node.(metadataAggregateNativeNode); ok {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return native.value.String() == id, nil
	}
	value, err := node.nodeValue(ctx, len(id)*2+1024)
	if err != nil {
		return false, err
	}
	return value.String() == id, nil
}

func metadataAggregateFind(ctx context.Context, node metadataAggregateNode, id string) (metadataAggregateNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var err error
	node, err = metadataAggregateUnwrap(ctx, node, true)
	if err != nil || node == nil || id == "" {
		return nil, err
	}
	kind := node.nodeType()
	plan := aggregateSearchPlanFor(kind, map[reflect.Type]bool{})
	if !plan.mayContain {
		return nil, nil
	}
	switch kind.Kind() {
	case reflect.Struct:
		if plan.idIndex != nil {
			idNode, err := metadataAggregateIDNode(ctx, node, plan.idIndex)
			if err != nil {
				return nil, err
			}
			match, err := metadataAggregateStringEqual(ctx, idNode, id)
			if err != nil || match {
				if match {
					return node, nil
				}
				return nil, err
			}
		}
		for _, index := range plan.fields {
			child, err := metadataAggregateChild(ctx, node, kind.Field(index).Name)
			if err != nil {
				return nil, err
			}
			found, err := metadataAggregateFind(ctx, child, id)
			if err != nil || found != nil {
				return found, err
			}
		}
		return nil, nil
	case reflect.Map:
		if kind.Key().Kind() == reflect.String {
			var idNode metadataAggregateNode
			var err error
			if lazy, ok := node.(metadataAggregateMapNode); ok {
				idNode, err = lazy.mapValue(ctx, "id")
			} else {
				idNode, err = metadataAggregateChild(ctx, node, "id")
			}
			if err != nil {
				return nil, err
			}
			idNode, err = metadataAggregateUnwrap(ctx, idNode, false)
			if err != nil {
				return nil, err
			}
			match, err := metadataAggregateStringEqual(ctx, idNode, id)
			if err != nil || match {
				if match {
					return node, nil
				}
				return nil, err
			}
		}
		if !aggregateSearchPlanFor(kind.Elem(), map[reflect.Type]bool{}).mayContain {
			return nil, nil
		}
	}
	var found metadataAggregateNode
	visit := func(child metadataAggregateNode) error {
		var err error
		found, err = metadataAggregateFind(ctx, child, id)
		if err != nil {
			return err
		}
		if found != nil {
			return errMetadataAggregateStop
		}
		return nil
	}
	if lazy, ok := node.(metadataAggregateMapNode); ok && kind.Kind() == reflect.Map {
		err = lazy.mapValues(ctx, visit)
	} else {
		err = node.nodeChildren(ctx, func(_ reflect.Value, child metadataAggregateNode) error {
			return visit(child)
		})
	}
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	return found, err
}

func (image metadataAggregateImage) visit(ctx context.Context, visit func(string, metadataAggregateNode) error) error {
	if image.Source == nil {
		return ErrBoundedMetadataCoverage
	}
	return image.Source.VisitFields(ctx, func(name string, stored metadataLogicalNode) error {
		if name == "-" || slices.Contains(image.ExcludedFields, name) {
			return nil
		}
		var node metadataAggregateNode = metadataAggregateStoredNode{stored}
		if image.Selected != nil {
			field, ok := metadataFieldByName(name)
			if ok {
				selected := reflect.ValueOf(image.Selected).Elem().Field(field.index)
				if name == "workspace" || name == "viewer" || name == "viewerRole" || slices.Contains(image.Scope.Fields, name) {
					node = metadataAggregateNativeNode{selected}
				} else if slices.Contains(metadataRecordFields(image.Scope), name) {
					node = metadataAggregateOverlayNode{base: node, selected: selected, field: name, scope: image.Scope, pointOccurrence: image.PointOccurrence}
				}
			}
		}
		if name == "importJobs" || name == "migrationJobs" {
			node = metadataAggregateCompactCollection{base: node, field: name}
		}
		return visit(name, node)
	})
}

func metadataAggregatePrefixField(id string) (string, bool) {
	for _, entry := range []struct{ prefix, field string }{
		{"team_resource_section_", "teamResourceSections"},
		{"team_resource_", "teamPinnedResources"},
		{"team_", "teams"}, {"state_", "states"}, {"usr_", "users"},
		{"label_", "labels"}, {"issue_", "issues"}, {"project_", "projects"}, {"cycle_", "cycles"},
	} {
		if strings.HasPrefix(id, entry.prefix) {
			return entry.field, entry.prefix == "team_resource_section_" || entry.prefix == "team_resource_"
		}
	}
	return "", false
}

func metadataAggregateResolve(ctx context.Context, image metadataAggregateImage, id string) (metadataAggregateNode, error) {
	if err := validateMetadataRecordScope(image.Scope); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ctx.Err()
	}
	if image.Source != nil && image.Source.units != nil {
		return metadataAggregateResolveIndexed(ctx, image, id)
	}
	return metadataAggregateResolveWalk(ctx, image, id)
}

func metadataAggregateResolveWalk(ctx context.Context, image metadataAggregateImage, id string) (metadataAggregateNode, error) {
	field, terminal := metadataAggregatePrefixField(id)
	var found metadataAggregateNode
	if field != "" {
		err := image.visit(ctx, func(name string, node metadataAggregateNode) error {
			if name != field {
				return nil
			}
			return node.nodeChildren(ctx, func(_ reflect.Value, child metadataAggregateNode) error {
				path, ok := child.nodeType().FieldByName("ID")
				if !ok || path.Type.Kind() != reflect.String {
					return nil
				}
				idNode, err := metadataAggregateIDNode(ctx, child, path.Index)
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
		})
		if errors.Is(err, errMetadataAggregateStop) {
			err = nil
		}
		if err != nil || found != nil || terminal {
			return found, err
		}
	}
	err := image.visit(ctx, func(_ string, node metadataAggregateNode) error {
		var err error
		found, err = metadataAggregateFind(ctx, node, id)
		if err != nil {
			return err
		}
		if found != nil {
			return errMetadataAggregateStop
		}
		return nil
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	return found, err
}

// metadataAggregateJSONValue retains the legacy typed-search then JSON
// normalization contract. Search traverses source descriptors, so rejected
// large records are not decoded. A winning subtree alone is materialized.
func metadataAggregateJSONValue(ctx context.Context, image metadataAggregateImage, id string, maxBytes int) (any, error) {
	node, err := metadataAggregateResolve(ctx, image, id)
	if err != nil || node == nil {
		return nil, err
	}
	return metadataAggregateJSONNode(ctx, node, maxBytes)
}

func metadataAggregateJSONNode(ctx context.Context, node metadataAggregateNode, maxBytes int) (any, error) {
	if node == nil {
		return nil, ctx.Err()
	}
	value, err := node.nodeValue(ctx, maxBytes)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value.Interface())
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		// The original aggregate accessor treats unencodable entities as
		// absent; a successful mutation does not become an encoding error.
		return nil, nil
	}
	if len(raw) > maxBytes {
		return nil, ErrMetadataWorkBudget
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func metadataAggregatePreviousValues(ctx context.Context, before, after metadataAggregateImage, id string, maxBytes int) (json.RawMessage, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	oldValue, err := metadataAggregateJSONValue(ctx, before, id, maxBytes)
	if err != nil || oldValue == nil {
		return nil, err
	}
	nextValue, err := metadataAggregateJSONValue(ctx, after, id, maxBytes)
	if err != nil {
		return nil, err
	}
	return metadataAggregateDiffValues(oldValue, nextValue), nil
}

func metadataAggregateDiffValues(oldValue, nextValue any) json.RawMessage {
	if oldValue == nil {
		return nil
	}
	if nextValue == nil {
		raw, _ := json.Marshal(oldValue)
		return raw
	}
	oldMap, oldOK := oldValue.(map[string]any)
	nextMap, nextOK := nextValue.(map[string]any)
	if !oldOK || !nextOK {
		raw, _ := json.Marshal(oldValue)
		return raw
	}
	diff := map[string]any{}
	for key, value := range oldMap {
		if next, ok := nextMap[key]; !ok || !reflect.DeepEqual(value, next) {
			diff[key] = value
		}
	}
	if len(diff) == 0 {
		return nil
	}
	raw, _ := json.Marshal(diff)
	return raw
}
