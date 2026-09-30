package store

import (
	"reflect"
	"sync"
)

// aggregateSearchPlan caches, per type, whether a value of that type can hold
// an entity with an "ID" string field (or a string-keyed map that may carry an
// "id" key), so findAggregateValue can skip subtrees such as timestamps,
// string lists and flat records without walking them. The search order and
// match rules are unchanged; only subtrees that can never match are pruned.
type aggregateSearchPlan struct {
	mayContain bool
	idIndex    []int // struct: index path of the ID string field
	fields     []int // struct: exported, non json:"-" fields worth searching
}

var aggregateSearchPlans sync.Map

func aggregateSearchPlanFor(t reflect.Type, visiting map[reflect.Type]bool) *aggregateSearchPlan {
	if cached, ok := aggregateSearchPlans.Load(t); ok {
		return cached.(*aggregateSearchPlan)
	}
	if visiting[t] {
		// Recursive type still being planned: assume it may match.
		return &aggregateSearchPlan{mayContain: true}
	}
	visiting[t] = true
	defer delete(visiting, t)
	plan := &aggregateSearchPlan{}
	switch t.Kind() {
	case reflect.Interface:
		plan.mayContain = true
	case reflect.Pointer:
		plan.mayContain = aggregateSearchPlanFor(t.Elem(), visiting).mayContain
	case reflect.Map:
		plan.mayContain = t.Key().Kind() == reflect.String || aggregateSearchPlanFor(t.Elem(), visiting).mayContain
	case reflect.Slice, reflect.Array:
		plan.mayContain = t.Elem().Kind() != reflect.Uint8 && aggregateSearchPlanFor(t.Elem(), visiting).mayContain
	case reflect.Struct:
		if field, ok := t.FieldByName("ID"); ok && field.Type.Kind() == reflect.String {
			plan.idIndex = field.Index
			plan.mayContain = true
		}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" || field.Tag.Get("json") == "-" {
				continue
			}
			if aggregateSearchPlanFor(field.Type, visiting).mayContain {
				plan.fields = append(plan.fields, i)
				plan.mayContain = true
			}
		}
	}
	if len(visiting) == 1 {
		actual, _ := aggregateSearchPlans.LoadOrStore(t, plan)
		return actual.(*aggregateSearchPlan)
	}
	return plan
}

// findAggregateValue locates the entity in the typed snapshot before
// serializing. Walking it does not allocate a second workspace-sized JSON/map
// representation.
func findAggregateValue(value reflect.Value, id string) any {
	if !value.IsValid() || id == "" {
		return nil
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	plan := aggregateSearchPlanFor(value.Type(), map[reflect.Type]bool{})
	if !plan.mayContain {
		return nil
	}
	switch value.Kind() {
	case reflect.Struct:
		if plan.idIndex != nil {
			if field, err := value.FieldByIndexErr(plan.idIndex); err == nil && field.String() == id {
				return value.Interface()
			}
		}
		for _, index := range plan.fields {
			if found := findAggregateValue(value.Field(index), id); found != nil {
				return found
			}
		}
	case reflect.Map:
		if value.Type().Key().Kind() == reflect.String {
			key := reflect.ValueOf("id").Convert(value.Type().Key())
			field := value.MapIndex(key)
			if field.IsValid() {
				for field.Kind() == reflect.Interface {
					field = field.Elem()
				}
				if field.IsValid() && field.Kind() == reflect.String && field.String() == id {
					return value.Interface()
				}
			}
		}
		if !aggregateSearchPlanFor(value.Type().Elem(), map[reflect.Type]bool{}).mayContain {
			return nil
		}
		iter := value.MapRange()
		for iter.Next() {
			if found := findAggregateValue(iter.Value(), id); found != nil {
				return found
			}
		}
	case reflect.Slice, reflect.Array:
		elem := value.Type().Elem()
		if elem.Kind() == reflect.Struct {
			// Fast path for entity lists: compare ids before descending.
			elemPlan := aggregateSearchPlanFor(elem, map[reflect.Type]bool{})
			for i := 0; i < value.Len(); i++ {
				item := value.Index(i)
				if elemPlan.idIndex != nil {
					if field, err := item.FieldByIndexErr(elemPlan.idIndex); err == nil && field.String() == id {
						return item.Interface()
					}
				}
				for _, index := range elemPlan.fields {
					if found := findAggregateValue(item.Field(index), id); found != nil {
						return found
					}
				}
			}
			return nil
		}
		for i := 0; i < value.Len(); i++ {
			if found := findAggregateValue(value.Index(i), id); found != nil {
				return found
			}
		}
	}
	return nil
}
