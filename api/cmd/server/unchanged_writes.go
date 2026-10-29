package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// A REST write whose effective change set is empty writes nothing: its
// mutation callback returns store.ErrNoMutation, so the store commits no
// domain event, history entry, realtime push, webhook or notification, and
// the resource keeps its version and updatedAt. The handler answers 200 with
// the resource as it is. Optimistic locking is checked before the comparison;
// issues allow stale versions only when the patched fields stayed unchanged.

// bookkeepingFields are set by every write itself, so they never make an edit
// a change.
var bookkeepingFields = []string{"Version", "UpdatedAt"}

// unchangedEdit reports whether after equals before, ignoring the top-level
// bookkeeping fields (Version, UpdatedAt) and any extra field names given.
// Nil and empty slices or maps compare equal and times compare by instant, so
// a value read from storage and the same value set from a request are equal.
func unchangedEdit[T any](before, after T, ignore ...string) bool {
	left, right := reflect.ValueOf(&before).Elem(), reflect.ValueOf(&after).Elem()
	if left.Kind() != reflect.Struct {
		return sameValue(left, right)
	}
	for index := 0; index < left.NumField(); index++ {
		name := left.Type().Field(index).Name
		if slices.Contains(bookkeepingFields, name) || slices.Contains(ignore, name) {
			continue
		}
		if !sameValue(left.Field(index), right.Field(index)) {
			return false
		}
	}
	return true
}

// jsonSnapshot captures a value as JSON, so a record that an edit changes in
// place (a settings map, a shared slice) can still be compared with what it
// was: unchangedJSON(snapshot, after).
func jsonSnapshot(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return raw
}

// unchangedJSON reports whether after marshals to the same JSON as the
// snapshot. A value that cannot be marshalled is always a change.
func unchangedJSON(snapshot []byte, after any) bool {
	raw := jsonSnapshot(after)
	return snapshot != nil && raw != nil && sameRawJSON(snapshot, raw)
}

// projectRelationsOf lists a project's relations by ID: a dependency edit
// rewrites them in a new order even when the set stays the same.
func projectRelationsOf(data *domain.Bootstrap, projectID string) []domain.ProjectRelation {
	relations := []domain.ProjectRelation{}
	for _, relation := range data.ProjectRelations {
		if relation.ProjectID == projectID || relation.RelatedProjectID == projectID {
			relations = append(relations, relation)
		}
	}
	slices.SortFunc(relations, func(a, b domain.ProjectRelation) int { return strings.Compare(a.ID, b.ID) })
	return relations
}

var (
	timeType       = reflect.TypeOf(time.Time{})
	rawMessageType = reflect.TypeOf(json.RawMessage(nil))
)

func sameRawJSON(left, right []byte) bool {
	if string(left) == string(right) {
		return true
	}
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func sameValue(left, right reflect.Value) bool {
	if left.Type() != right.Type() {
		return false
	}
	switch left.Kind() {
	case reflect.Slice:
		if left.Type() == rawMessageType {
			// Stored JSON is compacted; a request's copy may not be.
			return sameRawJSON(left.Bytes(), right.Bytes())
		}
		if left.Len() != right.Len() {
			return false
		}
		if left.Type().Elem().Kind() == reflect.Uint8 {
			return string(left.Bytes()) == string(right.Bytes())
		}
		for index := 0; index < left.Len(); index++ {
			if !sameValue(left.Index(index), right.Index(index)) {
				return false
			}
		}
		return true
	case reflect.Array:
		for index := 0; index < left.Len(); index++ {
			if !sameValue(left.Index(index), right.Index(index)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if left.Len() != right.Len() {
			return false
		}
		iterator := left.MapRange()
		for iterator.Next() {
			other := right.MapIndex(iterator.Key())
			if !other.IsValid() || !sameValue(iterator.Value(), other) {
				return false
			}
		}
		return true
	case reflect.Pointer:
		if left.IsNil() || right.IsNil() {
			return left.IsNil() == right.IsNil()
		}
		return left.Pointer() == right.Pointer() || sameValue(left.Elem(), right.Elem())
	case reflect.Interface:
		if left.IsNil() || right.IsNil() {
			return left.IsNil() == right.IsNil()
		}
		return sameValue(left.Elem(), right.Elem())
	case reflect.Struct:
		if left.Type() == timeType && left.CanInterface() && right.CanInterface() {
			return left.Interface().(time.Time).Equal(right.Interface().(time.Time))
		}
		for index := 0; index < left.NumField(); index++ {
			if !sameValue(left.Field(index), right.Field(index)) {
				return false
			}
		}
		return true
	case reflect.Bool:
		return left.Bool() == right.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return left.Int() == right.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return left.Uint() == right.Uint()
	case reflect.Float32, reflect.Float64:
		return left.Float() == right.Float()
	case reflect.String:
		return left.String() == right.String()
	case reflect.Invalid:
		return true
	default:
		// Funcs, channels and the like are never part of a stored record.
		return false
	}
}
