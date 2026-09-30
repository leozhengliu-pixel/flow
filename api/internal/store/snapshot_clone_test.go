package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestWorkspaceSettingsMetadataOmitsTeamsAndStates(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "settings-meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	_ = seedBulkTeams(t, repo, 400)
	data, ok := repo.WorkspaceSettingsMetadata("")
	if !ok || len(data.Teams) != 0 || len(data.States) != 0 || data.Workspace.ID == "" {
		t.Fatalf("settings metadata leaked catalog: teams=%d states=%d workspace=%q", len(data.Teams), len(data.States), data.Workspace.ID)
	}
}

// referenceCloneSnapshotValue is the original reflective deep clone. The
// planned clone must produce identical values.
func referenceCloneSnapshotValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copy := reflect.New(value.Type().Elem())
		copy.Elem().Set(referenceCloneSnapshotValue(value.Elem()))
		return copy
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copy := reflect.New(value.Type()).Elem()
		copy.Set(referenceCloneSnapshotValue(value.Elem()))
		return copy
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copy := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			copy.Index(i).Set(referenceCloneSnapshotValue(value.Index(i)))
		}
		return copy
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copy := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			copy.SetMapIndex(iter.Key(), referenceCloneSnapshotValue(iter.Value()))
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
			copy.Field(i).Set(referenceCloneSnapshotValue(value.Field(i)))
		}
		return copy
	case reflect.Array:
		copy := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			copy.Index(i).Set(referenceCloneSnapshotValue(value.Index(i)))
		}
		return copy
	default:
		return value
	}
}

// assertNoSharedReferences fails when a clone shares a mutable slice backing
// array, map or pointer with its source.
func assertNoSharedReferences(t *testing.T, path string, original, clone reflect.Value) {
	t.Helper()
	switch original.Kind() {
	case reflect.Pointer:
		if original.IsNil() {
			return
		}
		if original.Pointer() == clone.Pointer() {
			t.Fatalf("%s: pointer shared", path)
		}
		assertNoSharedReferences(t, path, original.Elem(), clone.Elem())
	case reflect.Interface:
		if !original.IsNil() {
			assertNoSharedReferences(t, path, original.Elem(), clone.Elem())
		}
	case reflect.Slice:
		if original.IsNil() || original.Len() == 0 {
			return
		}
		if original.Pointer() == clone.Pointer() {
			t.Fatalf("%s: slice shared", path)
		}
		for i := 0; i < original.Len(); i++ {
			assertNoSharedReferences(t, fmt.Sprintf("%s[%d]", path, i), original.Index(i), clone.Index(i))
		}
	case reflect.Map:
		if original.IsNil() || original.Len() == 0 {
			return
		}
		if original.Pointer() == clone.Pointer() {
			t.Fatalf("%s: map shared", path)
		}
		iter := original.MapRange()
		for iter.Next() {
			assertNoSharedReferences(t, fmt.Sprintf("%s[%v]", path, iter.Key()), iter.Value(), clone.MapIndex(iter.Key()))
		}
	case reflect.Struct:
		for i := 0; i < original.NumField(); i++ {
			field := original.Type().Field(i)
			if field.PkgPath != "" || field.Tag.Get("json") == "-" {
				continue
			}
			assertNoSharedReferences(t, path+"."+field.Name, original.Field(i), clone.Field(i))
		}
	case reflect.Array:
		for i := 0; i < original.Len(); i++ {
			assertNoSharedReferences(t, fmt.Sprintf("%s[%d]", path, i), original.Index(i), clone.Index(i))
		}
	}
}

func TestCloneBootstrapMatchesReflectiveDeepClone(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "clone.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	if len(data.Issues) == 0 || len(data.Teams) == 0 {
		t.Fatal("fixture is empty")
	}
	paused := time.Now().UTC()
	data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: "sla", PausedAt: &paused})
	data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{ID: "audit", Actor: data.Users[0], Metadata: map[string]any{"list": []any{"a", map[string]any{"b": 1}}}})
	data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: "sub", Events: []string{"a", "b"}})
	data.Settings = map[string]any{"nested": map[string]any{"values": []any{1.0, "x"}}}
	data.TeamByID = map[string]int{"team": 0}
	clone := cloneBootstrap(data)
	reference := referenceCloneSnapshotValue(reflect.ValueOf(data)).Interface().(domain.Bootstrap)
	if !reflect.DeepEqual(clone, reference) {
		t.Fatal("planned clone differs from the reflective deep clone")
	}
	assertNoSharedReferences(t, "Bootstrap", reflect.ValueOf(data), reflect.ValueOf(clone))
	clone.Subscriptions[len(clone.Subscriptions)-1].Events[0] = "changed"
	*clone.IssueSLAs[len(clone.IssueSLAs)-1].PausedAt = time.Time{}
	if data.Subscriptions[len(data.Subscriptions)-1].Events[0] != "a" || data.IssueSLAs[len(data.IssueSLAs)-1].PausedAt.IsZero() {
		t.Fatal("clone mutation leaked into the source")
	}
}
