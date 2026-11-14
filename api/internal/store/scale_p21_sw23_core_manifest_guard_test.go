package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW23CoreManifestGuardCanonicalRows(t *testing.T) {
	for _, field := range []string{"workspace", "viewer", "viewerRole"} {
		for _, hasRow := range []bool{false, true} {
			t.Run(field+map[bool]string{false: "-empty", true: "-row"}[hasRow], func(t *testing.T) {
				f, view, tx := scaleP21SW0ResidentScannerFixture(t)
				workspace := f.data.Workspace.URLKey
				var raw []byte
				if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var root map[string]json.RawMessage
				var shapes map[string]string
				if err := json.Unmarshal(raw, &root); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
					t.Fatal(err)
				}
				shapes[field] = "map"
				root[metadataCollectionsKey], _ = json.Marshal(shapes)
				raw, _ = json.Marshal(root)
				if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace); err != nil {
					t.Fatal(err)
				}
				if hasRow {
					if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,'name',999,?)`, workspace, field, []byte(`"split-core-value"`)); err != nil {
						t.Fatal(err)
					}
				}
				f.dropMetadataCache(t.Context(), workspace)
				want, legacyErr := f.decodeWorkspaceMetadata(t.Context(), workspace, raw, tx)
				if field != "viewerRole" || !hasRow {
					if legacyErr != nil {
						t.Fatal("legacy decoder rejected core fixture", legacyErr)
					}
				}
				if hasRow && field == "workspace" && want.Workspace.Name != "split-core-value" {
					t.Fatal("actual decoder workspace witness did not consume canonical row")
				}
				if hasRow && field == "viewer" && want.Viewer.Name != "split-core-value" {
					t.Fatal("actual decoder viewer witness did not consume canonical row")
				}
				// Root validation alone rejects this layout before a policy
				// scanner or any canonical body transfer can begin.
				if _, _, err := residentMetadataRoot(raw); !errors.Is(err, ErrBoundedMetadataCoverage) {
					t.Fatal("unsupported root core manifest was not rejected", err)
				}
				got, _, _, err := view.readResidentWorkspace(t.Context(), tx, workspace)
				if !errors.Is(err, ErrBoundedMetadataCoverage) || !reflect.DeepEqual(got, domain.Bootstrap{}) {
					t.Fatal("unsupported canonical core produced a partial result", err)
				}
				if len(view.metadataResidency.cache) != 0 || view.WorkspaceMetadataDecodes() != 0 ||
					view.metadataResidency.work.used != 0 {
					t.Fatal("unsupported core layout fetched/cached a body, decoded all metadata or leaked work")
				}
			})
		}
	}
}

func TestScaleP21SW23CoreManifestGuardInlineParity(t *testing.T) {
	f, view, tx := scaleP21SW0ResidentScannerFixture(t)
	workspace := f.data.Workspace.URLKey
	var raw []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	core := f.data.Workspace
	core.Name, core.Icon, core.LogoURL = " inline\x00workspace ", "\u5171\u4eab", "https://example.test/logo?x=\u212a"
	when := time.Date(2026, 11, 13, 1, 2, 3, 4, time.UTC)
	core.DeletionRequestedAt = &when
	viewer := f.data.Viewer
	viewer.Name = " inline\x00viewer "
	root["workspace"], _ = json.Marshal(core)
	root["viewer"], _ = json.Marshal(viewer)
	root["viewerRole"], _ = json.Marshal("guest\x00role")
	raw, _ = json.Marshal(root)
	if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace); err != nil {
		t.Fatal(err)
	}
	f.dropMetadataCache(t.Context(), workspace)
	want, err := f.decodeWorkspaceMetadata(t.Context(), workspace, raw, tx)
	if err != nil {
		t.Fatal(err)
	}
	for pass := range 2 {
		got, _, _, err := view.readResidentWorkspace(t.Context(), tx, workspace)
		if err != nil || !reflect.DeepEqual(got.Workspace, want.Workspace) ||
			!reflect.DeepEqual(got.Viewer, want.Viewer) || got.ViewerRole != want.ViewerRole {
			t.Fatalf("ordinary inline core changed pass=%d error=%v", pass, err)
		}
	}
	if want.Workspace.Name != core.Name || want.Viewer.Name != viewer.Name || want.ViewerRole != "guest\x00role" {
		t.Fatal("inline adversarial fixture lost its distinguishing values")
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("inline core triggered complete decode or leaked work")
	}
}

func TestScaleP21SW23CoreManifestGuardOtherCollections(t *testing.T) {
	for _, shape := range []string{"array", "map", "updates"} {
		root := map[string]any{
			"workspace": map[string]any{"id": "inline-workspace"},
			metadataCollectionsKey: map[string]string{
				"teams": "array", "teamSettings": "map", "workspaceSettings": "map",
				"documents": shape, "projects": "array",
			},
		}
		raw, _ := json.Marshal(root)
		if _, _, err := residentMetadataRoot(raw); err != nil {
			t.Fatal("core guard changed non-core collection validation", shape, err)
		}
	}
}
