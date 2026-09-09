package store

import (
	"flow/api/internal/domain"
	"reflect"
)

// Copy mutable containers while sharing immutable strings. JSON round-trips
// duplicate every description and allocate a second serialized workspace.
func cloneBootstrap(data domain.Bootstrap) domain.Bootstrap {
	return cloneSnapshotValue(reflect.ValueOf(data)).Interface().(domain.Bootstrap)
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
		if value.Type().Elem().Kind() == reflect.Uint8 {
			reflect.Copy(copy, value)
		} else {
			for i := 0; i < value.Len(); i++ {
				copy.Index(i).Set(cloneSnapshotValue(value.Index(i)))
			}
		}
		return copy
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copy := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			copy.SetMapIndex(iter.Key(), cloneSnapshotValue(iter.Value()))
		}
		return copy
	case reflect.Struct:
		copy := reflect.New(value.Type()).Elem()
		copy.Set(value)
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.PkgPath != "" {
				continue
			}
			if field.Tag.Get("json") == "-" {
				copy.Field(i).SetZero()
				continue
			}
			copy.Field(i).Set(cloneSnapshotValue(value.Field(i)))
		}
		return copy
	case reflect.Array:
		copy := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			copy.Index(i).Set(cloneSnapshotValue(value.Index(i)))
		}
		return copy
	default:
		return value
	}
}
