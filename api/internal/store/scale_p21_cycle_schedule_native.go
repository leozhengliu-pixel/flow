package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

func cycleScheduleNativeValue(ctx context.Context, owner *ResourceRead, node metadataLogicalNode) (reflect.Value, error) {
	if node.cost > metadataWorkBytes {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, node.cost); err != nil {
		return reflect.Value{}, err
	}
	return node.Value(ctx, int(node.cost))
}

func cycleScheduleProjectNode(ctx context.Context, owner *ResourceRead, node metadataLogicalNode, names ...string) (reflect.Value, error) {
	selected := map[string]bool{}
	for _, name := range names {
		selected[name] = true
	}
	cost := uint64(node.typ.Size())*2 + 128
	if err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if selected[key.String()] {
			if child.cost > metadataWorkBytes-cost {
				return ErrMetadataWorkBudget
			}
			cost += child.cost
		}
		return nil
	}); err != nil {
		return reflect.Value{}, err
	}
	if err := owner.reserve(ctx, cost); err != nil {
		return reflect.Value{}, err
	}
	value := reflect.New(node.typ).Elem()
	err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if !selected[key.String()] {
			return nil
		}
		field, err := child.Value(ctx, int(child.cost))
		if err == nil {
			value.FieldByName(key.String()).Set(field)
		}
		return err
	})
	return value, err
}

// Match cloneSnapshotValue and cloneInPlace: mutable containers are copied,
// while strings and flat values share their immutable contents.
func cycleScheduleCloneValueWork(ctx context.Context, value reflect.Value, inPlace bool, depth int) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !value.IsValid() {
		return 0, nil
	}
	if depth >= metadataLogicalDepth {
		return 0, ErrMetadataWorkBudget
	}
	cost := uint64(0)
	add := func(part uint64) error {
		if part > metadataWorkBytes || cost > metadataWorkBytes-part {
			return ErrMetadataWorkBudget
		}
		cost += part
		return nil
	}
	child := func(value reflect.Value, place bool) error {
		part, err := cycleScheduleCloneValueWork(ctx, value, place, depth+1)
		if err != nil {
			return err
		}
		return add(part)
	}
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return 0, nil
		}
		if err := add(uint64(value.Type().Elem().Size())*2 + 128); err != nil {
			return 0, err
		}
		if err := child(value.Elem(), false); err != nil {
			return 0, err
		}
	case reflect.Interface:
		if value.IsNil() {
			return 0, nil
		}
		if err := add(uint64(value.Type().Size())*2 + uint64(value.Elem().Type().Size()) + 128); err != nil {
			return 0, err
		}
		if err := child(value.Elem(), false); err != nil {
			return 0, err
		}
	case reflect.Slice:
		if value.IsNil() {
			return 0, nil
		}
		unit := uint64(value.Type().Elem().Size()) * 2
		if unit != 0 && uint64(value.Len()) > (metadataWorkBytes-128)/unit {
			return 0, ErrMetadataWorkBudget
		}
		if err := add(uint64(value.Len())*unit + 128); err != nil {
			return 0, err
		}
		if !typeCloneFlat(value.Type().Elem()) {
			for index := 0; index < value.Len(); index++ {
				if err := child(value.Index(index), true); err != nil {
					return 0, err
				}
			}
		}
	case reflect.Map:
		if value.IsNil() {
			return 0, nil
		}
		unit := (uint64(value.Type().Key().Size()) + uint64(value.Type().Elem().Size()) + 64) * 4
		if uint64(value.Len()) > (metadataWorkBytes-512)/unit {
			return 0, ErrMetadataWorkBudget
		}
		if err := add(uint64(value.Len())*unit + 512); err != nil {
			return 0, err
		}
		if !typeCloneFlat(value.Type().Elem()) {
			iter := value.MapRange()
			for iter.Next() {
				if err := child(iter.Value(), false); err != nil {
					return 0, err
				}
			}
		}
	case reflect.Struct:
		plan := structPlanFor(value.Type())
		if !plan.flat {
			if !inPlace {
				if err := add(uint64(value.Type().Size())*2 + 128); err != nil {
					return 0, err
				}
			}
			for _, index := range plan.deep {
				if err := child(value.Field(index), true); err != nil {
					return 0, err
				}
			}
		}
	case reflect.Array:
		if !typeCloneFlat(value.Type().Elem()) {
			if !inPlace {
				if err := add(uint64(value.Type().Size())*2 + 128); err != nil {
					return 0, err
				}
			}
			for index := 0; index < value.Len(); index++ {
				if err := child(value.Index(index), true); err != nil {
					return 0, err
				}
			}
		}
	}
	return cost, nil
}

func reserveCycleScheduleClone(ctx context.Context, owner *ResourceRead, data domain.Bootstrap) error {
	cost, err := cycleScheduleCloneValueWork(ctx, reflect.ValueOf(data), false, 0)
	if err != nil {
		return err
	}
	add := func(value reflect.Value) error {
		part, err := cycleScheduleCloneValueWork(ctx, value, false, 0)
		if err != nil {
			return err
		}
		if part > metadataWorkBytes-cost {
			return ErrMetadataWorkBudget
		}
		cost += part
		return nil
	}
	if err := add(reflect.ValueOf(data.ActivityHistoryHeads)); err != nil {
		return err
	}
	if err := add(reflect.ValueOf(data.ViewerTeamIDs)); err != nil {
		return err
	}
	if narrowMutationInputs() {
		if err := add(reflect.ValueOf(data.Viewer)); err != nil {
			return err
		}
		if err := add(reflect.ValueOf(data.Viewer.Via)); err != nil {
			return err
		}
		for _, user := range data.Users {
			if user.Via != nil {
				if err := add(reflect.ValueOf(user)); err != nil {
					return err
				}
				if err := add(reflect.ValueOf(user.Via)); err != nil {
					return err
				}
			}
		}
	}
	if cost > metadataWorkBytes-(64<<10) {
		return ErrMetadataWorkBudget
	}
	return owner.reserve(ctx, cost+64<<10)
}
