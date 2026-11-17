package store

import (
	"context"
	"reflect"
)

// largeRowRecordValue decodes one native record a callback receives whole.
// A record at or below largeRowNodeCost keeps ResourceRead.node (eight
// native copies admitted). A larger record is decoded member by member as
// largeRowHeader decodes a header, each member admitted at its own native
// cost: a 15 MiB string is one native copy, not eight copies of the row.
func largeRowRecordValue(ctx context.Context, owner *ResourceRead, node metadataLogicalNode) (reflect.Value, error) {
	if node.cost <= largeRowNodeCost || node.typ == nil || node.typ.Kind() != reflect.Struct || node.generic || node.blocked || node.unavailable {
		return owner.node(ctx, node, false)
	}
	for index := range node.typ.NumField() {
		if field := node.typ.Field(index); field.Anonymous || field.PkgPath != "" {
			return owner.node(ctx, node, false)
		}
	}
	value, _, err := largeRowHeader(ctx, owner, node, nil, "")
	return value, err
}

// largeRowSharedStrings is the native cost of value's top-level string
// members above largeRowBodyCost. Snapshot clones and callback copies share
// a Go string's bytes, so admissions for further copies of value exclude it.
func largeRowSharedStrings(value reflect.Value) uint64 {
	if value.Kind() != reflect.Struct {
		return 0
	}
	shared := uint64(0)
	for index := range value.NumField() {
		field := value.Field(index)
		if value.Type().Field(index).PkgPath != "" || field.Kind() != reflect.String {
			continue
		}
		if cost := uint64(field.Len()) * 2; cost > largeRowBodyCost {
			shared += cost
		}
	}
	return shared
}

// largeRowCloneBytes bounds what cloneSnapshotValue allocates for a copy of
// value: slice and array backing stores, map entries and pointed-to or
// boxed values. Strings share their bytes with the original, so a release
// with 300,000 linked issue IDs clones 16 bytes per ID, not the IDs.
func largeRowCloneBytes(ctx context.Context, value reflect.Value, depth int) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if depth >= metadataLogicalDepth {
		return 0, ErrMetadataWorkBudget
	}
	total := uint64(0)
	add := func(child reflect.Value, own uint64) error {
		part, err := largeRowCloneBytes(ctx, child, depth+1)
		if err != nil {
			return err
		}
		if part > metadataWorkBytes || own > metadataWorkBytes || total > metadataWorkBytes-part-own {
			return ErrMetadataWorkBudget
		}
		total += part + own
		return nil
	}
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			return total, add(value.Elem(), uint64(value.Type().Elem().Size())+16)
		}
	case reflect.Interface:
		if !value.IsNil() {
			return total, add(value.Elem(), uint64(value.Elem().Type().Size())+16)
		}
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && !value.IsNil() {
			total = uint64(value.Len())*uint64(value.Type().Elem().Size()) + 16
		}
		if value.Type().Elem().Kind() == reflect.Uint8 || value.Type().Elem().Kind() == reflect.String {
			return total, nil
		}
		for index := range value.Len() {
			if err := add(value.Index(index), 0); err != nil {
				return 0, err
			}
		}
	case reflect.Map:
		if !value.IsNil() {
			total = uint64(value.Len())*(uint64(value.Type().Key().Size()+value.Type().Elem().Size())*2+16) + 64
			iter := value.MapRange()
			for iter.Next() {
				if err := add(iter.Key(), 0); err != nil {
					return 0, err
				}
				if err := add(iter.Value(), 0); err != nil {
					return 0, err
				}
			}
		}
	case reflect.Struct:
		for index := range value.NumField() {
			if value.Type().Field(index).PkgPath != "" {
				continue
			}
			if err := add(value.Field(index), 0); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}
