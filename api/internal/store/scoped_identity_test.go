package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW0ScopedIdentityMissingInstalledKeyAvoidsSQL(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var existing int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&existing); err != nil || existing != 1 {
		t.Fatalf("fixture SQL row missing: count=%d error=%v", existing, err)
	}
	f.mu.Lock()
	retained := f.workspaces[workspace]
	delete(f.workspaces, workspace)
	f.mu.Unlock()
	t.Cleanup(func() {
		f.mu.Lock()
		f.workspaces[workspace] = retained
		f.mu.Unlock()
	})
	view.mu.Lock()
	delete(view.workspaces, workspace)
	view.lastWorkspaceKey = workspace
	view.mu.Unlock()
	if _, ok := f.WorkspaceMetadata(workspace); ok {
		t.Fatal("legacy exact installed-key oracle unexpectedly found removed key")
	}
	if _, ok, err := view.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Fields: []string{"workspaceSettings"}}); err != nil || ok {
		t.Fatalf("SQL row revived absent installed workspace: found=%v error=%v", ok, err)
	}
	// Its SQL row still exists. Occupying the sole connection proves the
	// bounded trusted read does not issue SQL for a missing installed key.
	f.db.SetMaxOpenConns(1)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, key := range []string{workspace, "", strings.ToUpper(workspace), workspace + "\x00"} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		_, ok, err := view.WorkspaceMetadataScoped(ctx, key, MetadataRecordScope{Fields: []string{"workspaceSettings"}})
		cancel()
		if err != nil || ok {
			t.Fatalf("missing exact installed key %q: found=%v error=%v", key, ok, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.WorkspaceMetadataScoped(ctx, workspace, MetadataRecordScope{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled missing-key read=%v", err)
	}
	if _, err := view.readScopedMetadata(t.Context(), workspace, MetadataRecordScope{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("private missing-key contract=%v", err)
	}
}

func TestScaleP21SW0ScopedIdentityExactDefaultAndOffBranch(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	view.mu.Lock()
	view.lastWorkspaceKey = workspace
	view.mu.Unlock()
	scope := MetadataRecordScope{Fields: []string{"workspaceSettings"}}
	explicit, ok, err := view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
	if err != nil || !ok {
		t.Fatal("exact key failed", err)
	}
	fromDefault, ok, err := view.WorkspaceMetadataScoped(t.Context(), "", scope)
	if err != nil || !ok || !reflect.DeepEqual(explicit, fromDefault) || explicit.Workspace.ID != f.data.Workspace.ID {
		t.Fatalf("default identity changed: found=%v error=%v", ok, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.WorkspaceMetadataScoped(canceled, workspace, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("known-key cancellation=%v", err)
	}
	// Off mode still calls the legacy accessor, ignoring the supplied scope
	// and cancellation exactly as it did before this guard.
	expected, _ := f.WorkspaceMetadata(workspace)
	actual, ok, err := f.WorkspaceMetadataScoped(canceled, workspace, MetadataRecordScope{Fields: []string{"invalid"}})
	if err != nil || !ok || !reflect.DeepEqual(actual, expected) {
		t.Fatal("off branch changed")
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("scoped identity test decoded full metadata or leaked work")
	}
}

func TestScaleP21SW0ScopedIdentityNULPhysicalWorkspace(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	if f.dialect == "postgres" {
		t.Skip("Postgres text storage excludes physical NUL keys")
	}
	workspace := idgen.New("sw0-scoped-nul-") + "\x00physical"
	data, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "Scoped physical NUL", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := f.DeleteWorkspace(ctx, workspace); err != nil {
			t.Error(err)
		}
	})
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expected, ok := f.WorkspaceMetadata(workspace)
	if !ok {
		t.Fatal("trusted legacy accessor did not preserve physical NUL workspace")
	}
	var physicalKey string
	if err := f.db.QueryRowContext(t.Context(), `SELECT workspace_key FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&physicalKey); err != nil || physicalKey != workspace || expected.Workspace.URLKey != workspace {
		t.Fatalf("fixture did not retain its raw physical/warm key: SQL=%q warm=%q error=%v", physicalKey, expected.Workspace.URLKey, err)
	}
	actual, ok, err := view.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Fields: []string{"workspaceSettings"}})
	if err != nil || !ok || actual.Workspace.ID != data.Workspace.ID {
		t.Fatalf("physical NUL workspace: found=%v workspace=%+v error=%v", ok, actual.Workspace, err)
	}
	if actual.Workspace.URLKey != expected.Workspace.URLKey {
		t.Logf("separate persisted-JSON normalization debt: warm URLKey=%q scoped URLKey=%q; this test certifies physical-key selection only", expected.Workspace.URLKey, actual.Workspace.URLKey)
	}
}

func TestScaleP21SW0ScopedIdentityRejectsAliasesWithoutDatabase(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			s := &SQLiteStore{
				dialect: dialect, metadataResidency: newMetadataResidency(),
				workspaces:       map[string]domain.Bootstrap{"exact-key": {}},
				lastWorkspaceKey: "exact-key",
			}
			for _, missing := range []string{"EXACT-KEY", "exact-key\x00", "missing"} {
				if _, ok, err := s.WorkspaceMetadataScoped(t.Context(), missing, MetadataRecordScope{}); err != nil || ok {
					t.Fatalf("missing alias %q: found=%v error=%v", missing, ok, err)
				}
			}
			if dialect == "postgres" {
				// Even a retained raw key must not alias its sanitized SQL key.
				s.workspaces["exact-key\x00"] = domain.Bootstrap{}
				if _, ok, err := s.WorkspaceMetadataScoped(t.Context(), "exact-key\x00", MetadataRecordScope{}); err != nil || ok {
					t.Fatalf("Postgres raw NUL alias: found=%v error=%v", ok, err)
				}
			}
		})
	}
}
