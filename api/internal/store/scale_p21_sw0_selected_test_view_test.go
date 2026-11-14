package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW0SelectedExtraWorkspace(t *testing.T, f scaleP09Fixture, key string) domain.Bootstrap {
	t.Helper()
	data, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "Selected extra fixture", key, "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := f.DeleteWorkspace(ctx, key); err != nil {
			t.Error(err)
		}
	})
	return data
}

func TestScaleP21SW0SelectedTestViewIsolatesMalformedWorkspace(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	valid := f.data.Workspace.URLKey
	invalid := scaleP21SW0SelectedExtraWorkspace(t, f, idgen.New("sw0-unselected-")).Workspace.URLKey
	var original []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, invalid).Scan(&original); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, original, invalid); err != nil {
			t.Error(err)
		}
	})
	var root map[string]json.RawMessage
	if err := json.Unmarshal(original, &root); err != nil {
		t.Fatal(err)
	}
	root["members"] = json.RawMessage(`[{"user":{"id":"fixture-only"},"status":"suspended"}]`)
	var shapes map[string]string
	if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
		t.Fatal(err)
	}
	delete(shapes, "members")
	encoded, err := json.Marshal(shapes)
	if err != nil {
		t.Fatal(err)
	}
	root[metadataCollectionsKey] = encoded
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, invalid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.BoundedMetadataViewForTesting(t.Context()); err == nil {
		t.Fatalf("legacy all-workspace test view stopped checking unrelated unsupported data: %v", err)
	}
	if _, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context()); err == nil {
		t.Fatalf("noargs variant changed the existing all-workspace behavior: %v", err)
	}
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "SELECT workspace_key FROM workspace_states") {
			t.Error("selected helper enumerated shared workspaces")
		}
	})
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(ctx, valid, valid)
	if err != nil {
		t.Fatal(err)
	}
	if view.db != f.db || !view.BoundedMetadataEnabled() || len(view.workspaces) != 1 || view.lastWorkspaceKey != valid {
		t.Fatal("selected view changed shared SQL ownership, default or selection")
	}
	selected, found, err := view.WorkspaceMetadataScoped(t.Context(), "", MetadataRecordScope{Fields: []string{"workspaceSettings"}})
	if err != nil || !found || selected.Workspace.ID != f.data.Workspace.ID {
		t.Fatalf("selected default identity: found=%v error=%v", found, err)
	}
	if _, found, err := view.WorkspaceMetadataScoped(t.Context(), invalid, MetadataRecordScope{}); err != nil || found {
		t.Fatalf("unselected workspace became readable: found=%v error=%v", found, err)
	}
	if _, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), invalid); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("selected malformed workspace did not fail closed: %v", err)
	}
	if _, exact := view.copyRevisionOf(valid); exact {
		t.Fatal("test helper certified a complete installed Bootstrap")
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 || view.metadataResidency.policyBytes <= 0 {
		t.Fatal("selected view decoded complete metadata, leaked work or failed policy accounting")
	}
}

func TestScaleP21SW0SelectedTestViewDefaultsViewerAndExactKeys(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	first := f.data.Workspace.URLKey
	second := scaleP21SW0SelectedExtraWorkspace(t, f, idgen.New("sw0-selected-default-")).Workspace.URLKey
	f.mu.Lock()
	originalViewer := f.viewer
	when := time.Now()
	expectedWhen := when
	f.viewer.AppScopes = []string{"read"}
	f.viewer.AppTeamIDs = []string{}
	f.viewer.OutOfOfficeUntil = &when
	f.viewer.Via = &domain.ActorBot{ID: "test-attribution", Name: "Warm via actor"}
	wantViewer := f.viewer
	last := f.lastWorkspaceKey
	f.mu.Unlock()
	t.Cleanup(func() {
		f.mu.Lock()
		f.viewer = originalViewer
		f.mu.Unlock()
	})
	if last != second {
		t.Fatal("fixture default was not the second workspace")
	}
	for _, keys := range [][]string{{""}, {first, second}, {first, first}} {
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), keys...)
		if err != nil {
			t.Fatal(err)
		}
		wantDefault := second
		if len(keys) == 2 && keys[0] == first && keys[1] == first {
			wantDefault = first
		}
		if view.lastWorkspaceKey != wantDefault || !reflect.DeepEqual(view.viewer, wantViewer) {
			t.Fatal("selected view changed owner default/viewer")
		}
		view.viewer.AppScopes[0] = "changed"
		view.viewer.AppTeamIDs = append(view.viewer.AppTeamIDs, "new-team")
		*view.viewer.OutOfOfficeUntil = time.Time{}
		view.viewer.Via.Name = "changed"
		f.mu.RLock()
		unchanged := f.viewer.AppScopes[0] == "read" && len(f.viewer.AppTeamIDs) == 0 &&
			*f.viewer.OutOfOfficeUntil == expectedWhen && f.viewer.Via.Name == "Warm via actor"
		f.mu.RUnlock()
		if !unchanged {
			t.Fatal("selected view retained mutable owner viewer aliases")
		}
		var one int
		if err := f.db.QueryRowContext(t.Context(), "SELECT 1").Scan(&one); err != nil {
			t.Fatalf("helper ended owner SQL lifetime: %v", err)
		}
	}
	for _, key := range []string{first + "-missing", strings.ToUpper(first), first + "\x00"} {
		queries := 0
		ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
		if _, err := f.BoundedMetadataViewForWorkspacesForTesting(ctx, key); !errors.Is(err, sql.ErrNoRows) || queries != 0 {
			t.Fatalf("missing exact requested key queried SQL: key=%q queries=%d error=%v", key, queries, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.BoundedMetadataViewForWorkspacesForTesting(cancelled, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled selected view=%v", err)
	}
}

func TestScaleP21SW0SelectedTestViewPhysicalNULAndPoolOne(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	if f.dialect == "postgres" {
		// Postgres cannot store this physical namespace, even if a synthetic
		// installed test copy has the raw alias.
		s := &SQLiteStore{dialect: "postgres", workspaces: map[string]domain.Bootstrap{"raw\x00": {}}}
		if _, err := s.BoundedMetadataViewForWorkspacesForTesting(t.Context(), "raw\x00"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("Postgres NUL alias=%v", err)
		}
		return
	}
	rawKey := idgen.New("sw0-selected-nul-") + "\x00physical"
	data := scaleP21SW0SelectedExtraWorkspace(t, f, rawKey)
	f.db.SetMaxOpenConns(1)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), rawKey)
	if err != nil || view == nil {
		t.Fatal(err)
	}
	if _, present := view.workspaces[rawKey]; !present || view.lastWorkspaceKey != rawKey {
		t.Fatal("physical NUL key lost its selected/default namespace")
	}
	got, found, err := view.WorkspaceMetadataScoped(t.Context(), rawKey, MetadataRecordScope{})
	if err != nil || !found || got.Workspace.ID != data.Workspace.ID {
		t.Fatalf("physical NUL selected identity: found=%v error=%v", found, err)
	}
}

func TestScaleP21SW0SelectedTestViewViewerBudgetBeforeSQL(t *testing.T) {
	same := strings.Repeat("x", 64<<10)
	scopes := make([]string, 4096)
	for index := range scopes {
		scopes[index] = same
	}
	s := &SQLiteStore{
		dialect: "sqlite", viewer: domain.User{AppScopes: scopes},
		workspaces: map[string]domain.Bootstrap{"selected": {}},
	}
	queries := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
	if _, err := s.BoundedMetadataViewForWorkspacesForTesting(ctx, "selected"); !errors.Is(err, ErrMetadataWorkBudget) || queries != 0 {
		t.Fatalf("oversized logical viewer not rejected before clone/SQL: queries=%d error=%v", queries, err)
	}
	cost, err := metadataTestViewViewerCost(domain.User{Via: &domain.ActorBot{Name: "included"}}, metadataPolicyBytes)
	without, plainErr := metadataTestViewViewerCost(domain.User{}, metadataPolicyBytes)
	if err != nil || plainErr != nil || cost <= without {
		t.Fatal("viewer attribution was not charged")
	}
}
