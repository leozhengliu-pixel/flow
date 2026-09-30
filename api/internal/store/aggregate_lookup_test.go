package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func referenceFindAggregateValue(value reflect.Value, id string) any {
	if !value.IsValid() || id == "" {
		return nil
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		if field := value.FieldByName("ID"); field.IsValid() && field.Kind() == reflect.String && field.String() == id {
			return value.Interface()
		}
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).PkgPath != "" || value.Type().Field(i).Tag.Get("json") == "-" {
				continue
			}
			if found := referenceFindAggregateValue(value.Field(i), id); found != nil {
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
		iter := value.MapRange()
		for iter.Next() {
			if found := referenceFindAggregateValue(iter.Value(), id); found != nil {
				return found
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return nil
		}
		for i := 0; i < value.Len(); i++ {
			if found := referenceFindAggregateValue(value.Index(i), id); found != nil {
				return found
			}
		}
	}
	return nil
}

// The pruned search must return the same entity as the full reflective walk.
func TestFindAggregateValueMatchesFullWalk(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "aggregate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	now := time.Now().UTC()
	data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "view_1", Name: "View"})
	data.Settings = map[string]any{"dashboards.v1": []any{map[string]any{"id": "dashboard_x", "name": "Health"}}}
	data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{ID: "audit_1", Actor: data.Users[0], Metadata: map[string]any{"nested": map[string]any{"id": "nested_1"}}, CreatedAt: now})
	ids := map[string]bool{"missing": true, "dashboard_x": true, "nested_1": true}
	var collect func(any)
	collect = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if id, ok := typed["id"].(string); ok {
				ids[id] = true
			}
			for _, child := range typed {
				collect(child)
			}
		case []any:
			for _, child := range typed {
				collect(child)
			}
		}
	}
	raw, _ := json.Marshal(data)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	collect(decoded)
	if len(ids) < 30 {
		t.Fatalf("collected only %d ids", len(ids))
	}
	for id := range ids {
		got := findAggregateValue(reflect.ValueOf(data), id)
		want := referenceFindAggregateValue(reflect.ValueOf(data), id)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: pruned search found %#v, full walk %#v", id, got, want)
		}
	}
}
