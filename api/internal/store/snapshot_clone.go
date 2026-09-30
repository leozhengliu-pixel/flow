package store

import (
	"flow/api/internal/domain"
	"reflect"
	"sync"
)

// Copy mutable containers while sharing immutable strings. JSON round-trips
// duplicate every description and allocate a second serialized workspace.
func cloneBootstrap(data domain.Bootstrap) domain.Bootstrap {
	return cloneSnapshotValue(reflect.ValueOf(data)).Interface().(domain.Bootstrap)
}

// structClonePlan caches, per struct type, which exported fields need a deep
// copy and which are dropped (json:"-"). A type is flat when a plain value
// copy already is a deep clone (no pointers, slices, maps or interfaces in
// exported fields). Large metadata collections are mostly flat or nearly
// flat, so cloning them is a memmove plus a few targeted field copies instead
// of a reflective walk (and struct tag parse) of every field of every row.
type structClonePlan struct {
	flat bool
	deep []int
	zero []int
}

var structClonePlans sync.Map

func structPlanFor(t reflect.Type) *structClonePlan {
	if cached, ok := structClonePlans.Load(t); ok {
		return cached.(*structClonePlan)
	}
	plan := &structClonePlan{flat: true}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if field.Tag.Get("json") == "-" {
			plan.zero = append(plan.zero, i)
			plan.flat = false
			continue
		}
		if !typeCloneFlat(field.Type) {
			plan.deep = append(plan.deep, i)
			plan.flat = false
		}
	}
	actual, _ := structClonePlans.LoadOrStore(t, plan)
	return actual.(*structClonePlan)
}

// typeCloneFlat reports whether cloneSnapshotValue(v) is equivalent to v.
// Reference kinds always allocate, so recursion only descends into value
// types and terminates without cycle tracking.
func typeCloneFlat(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		return false
	case reflect.Struct:
		return structPlanFor(t).flat
	case reflect.Array:
		return typeCloneFlat(t.Elem())
	default:
		return true
	}
}

func cloneSnapshotValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copy := reflect.New(value.Type().Elem())
		copy.Elem().Set(cloneSnapshotValue(value.Elem()))
		return copy
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copy := reflect.New(value.Type()).Elem()
		copy.Set(cloneSnapshotValue(value.Elem()))
		return copy
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copy := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		reflect.Copy(copy, value)
		if !typeCloneFlat(value.Type().Elem()) {
			for i := 0; i < copy.Len(); i++ {
				cloneInPlace(copy.Index(i))
			}
		}
		return copy
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copy := reflect.MakeMapWithSize(value.Type(), value.Len())
		flat := typeCloneFlat(value.Type().Elem())
		iter := value.MapRange()
		for iter.Next() {
			if flat {
				copy.SetMapIndex(iter.Key(), iter.Value())
			} else {
				copy.SetMapIndex(iter.Key(), cloneSnapshotValue(iter.Value()))
			}
		}
		return copy
	case reflect.Struct:
		plan := structPlanFor(value.Type())
		if plan.flat {
			return value
		}
		copy := reflect.New(value.Type()).Elem()
		copy.Set(value)
		cloneStructInPlace(copy, plan)
		return copy
	case reflect.Array:
		if typeCloneFlat(value.Type().Elem()) {
			return value
		}
		copy := reflect.New(value.Type()).Elem()
		copy.Set(value)
		for i := 0; i < copy.Len(); i++ {
			cloneInPlace(copy.Index(i))
		}
		return copy
	default:
		return value
	}
}

// cloneInPlace replaces the references held by an addressable value (which
// still shares them with its source) with deep copies.
func cloneInPlace(target reflect.Value) {
	switch target.Kind() {
	case reflect.Struct:
		if plan := structPlanFor(target.Type()); !plan.flat {
			cloneStructInPlace(target, plan)
		}
	case reflect.Array:
		if !typeCloneFlat(target.Type().Elem()) {
			for i := 0; i < target.Len(); i++ {
				cloneInPlace(target.Index(i))
			}
		}
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		target.Set(cloneSnapshotValue(target))
	}
}

func cloneStructInPlace(target reflect.Value, plan *structClonePlan) {
	for _, index := range plan.zero {
		target.Field(index).SetZero()
	}
	for _, index := range plan.deep {
		cloneInPlace(target.Field(index))
	}
}
