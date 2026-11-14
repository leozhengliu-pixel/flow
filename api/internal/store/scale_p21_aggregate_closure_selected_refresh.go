package store

import (
	"context"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

func metadataAggregateDisplaySource(ctx context.Context, source *metadataLogicalSource, fields []string, fieldScoped bool) (*metadataLogicalSource, error) {
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		base := metadataAggregateNode(metadataAggregateStoredNode{node})
		if fieldScoped && !slices.Contains(fields, name) {
			return base, false, nil
		}
		refreshed := metadataAggregateRefreshField(source, name, base)
		_, changed := refreshed.(metadataAggregateRefreshCollection)
		return refreshed, changed, nil
	})
}

func metadataAggregateDisplayReferences(field string) []string {
	switch field {
	case "members", "projectUpdates", "initiativeUpdates", "comments":
		return []string{"User"}
	case "projects":
		return []string{"Lead", "Creator"}
	case "initiatives":
		return []string{"Creator", "Owner"}
	case "activities", "notifications":
		return []string{"Actor"}
	}
	return nil
}

// Callback inputs need the complete directory's last matching user, but only
// declared records need decoding before a callback that may decline a write.
// The caller keeps the full streamed transform for every successful mutation.
func refreshSelectedMetadataLogicalReferences(ctx context.Context, source *metadataLogicalSource, data *domain.Bootstrap, scope MetadataRecordScope, legacyFields []string, fieldScoped bool, limit int) error {
	fields := metadataRecordFields(scope)
	refresh := false
	for _, field := range fields {
		if len(metadataAggregateDisplayReferences(field)) != 0 && (!fieldScoped || slices.Contains(legacyFields, field)) {
			refresh = true
			break
		}
	}
	if !refresh {
		return ctx.Err()
	}
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(*data), 0)
	if err != nil {
		return err
	}
	if limit < 0 || cost > uint64(limit) {
		return ErrMetadataWorkBudget
	}
	root := reflect.ValueOf(data).Elem()
	userType := reflect.TypeFor[domain.User]()
	for _, field := range fields {
		references := metadataAggregateDisplayReferences(field)
		if len(references) == 0 || fieldScoped && !slices.Contains(legacyFields, field) {
			continue
		}
		index, _, ok := metadataLogicalField(field)
		if !ok {
			return ErrMetadataRecordQuery
		}
		record := func(value reflect.Value) error {
			if value.Kind() != reflect.Struct {
				return ErrMetadataRecordQuery
			}
			for _, name := range references {
				target := value.FieldByName(name)
				if !target.IsValid() {
					return ErrMetadataRecordQuery
				}
				old := target
				if old.Kind() == reflect.Pointer {
					if old.IsNil() {
						continue
					}
					old = old.Elem()
				}
				if old.Type() != userType {
					return ErrMetadataRecordQuery
				}
				id := metadataAggregateNativeNode{old.FieldByName("ID")}
				user, err := metadataAggregateLookupUser(ctx, source, id)
				if err != nil {
					return err
				}
				if user == nil {
					continue
				}
				oldCost, err := metadataAggregateNativeCost(ctx, old, 0)
				if err != nil {
					return err
				}
				nextCost, err := metadataAggregateNodeCost(ctx, user)
				if err != nil {
					return err
				}
				if oldCost > cost || nextCost > uint64(limit) || cost-oldCost > uint64(limit)-nextCost {
					return ErrMetadataWorkBudget
				}
				current, err := user.nodeValue(ctx, limit)
				if err != nil {
					return err
				}
				if current.Type() != userType {
					return ErrMetadataRecordQuery
				}
				cost = cost - oldCost + nextCost
				if target.Kind() == reflect.Pointer {
					pointer := reflect.New(userType)
					pointer.Elem().Set(current)
					target.Set(pointer)
				} else {
					target.Set(current)
				}
			}
			return nil
		}
		collection := root.Field(index)
		slice := func(values reflect.Value) error {
			if values.Kind() != reflect.Slice {
				return ErrMetadataRecordQuery
			}
			for i := 0; i < values.Len(); i++ {
				if err := record(values.Index(i)); err != nil {
					return err
				}
			}
			return nil
		}
		if collection.Kind() == reflect.Map {
			iter := collection.MapRange()
			for iter.Next() {
				if err := slice(iter.Value()); err != nil {
					return err
				}
			}
		} else if err := slice(collection); err != nil {
			return err
		}
	}
	return ctx.Err()
}
