package store

import (
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW51InputRows(t *testing.T, repo *SQLiteStore, workspace string) map[string][32]byte {
	t.Helper()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT kind,id,data FROM workspace_content_records WHERE workspace_key=? AND kind IN ('import_input','migration_input')`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string][32]byte{}
	for rows.Next() {
		var kind, id string
		var raw []byte
		if err := rows.Scan(&kind, &id, &raw); err != nil {
			t.Fatal(err)
		}
		result[kind+"/"+id] = sha256.Sum256(raw)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21SW51ScopedImportInputMembership(t *testing.T) {
	for _, name := range []string{"unrelated-setting", "read-only-import", "import-point", "complete-import", "delete-migration", "import-field", "migration-field"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			prefix := idgen.New("sw51_")
			first, second := prefix+"first", prefix+"second"
			now := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
			var results []map[string][32]byte
			for _, bounded := range []bool{false, true} {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				body := "unrelated body"
				if name == "unrelated-setting" {
					body = strings.Repeat("x", 2<<20)
				}
				err := f.MutateWorkspace(WithMetadataFields(t.Context(), "importJobs", "migrationJobs"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.ImportJobs = []domain.ImportJob{
						{ID: first, Status: "pending", Rows: []map[string]string{{"title": "first"}}, CreatedAt: now},
						{ID: second, Status: "pending", Rows: []map[string]string{{"title": body}}, CreatedAt: now},
					}
					data.MigrationJobs = []domain.MigrationJob{
						{ID: first, Status: "pending", Bundle: json.RawMessage(`{"input":"first"}`), CreatedAt: now},
						{ID: second, Status: "pending", Bundle: json.RawMessage(`{"input":"second"}`), CreatedAt: now},
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				before := scaleP21SW51InputRows(t, f.SQLiteStore, workspace)
				if len(before) != 4 {
					t.Fatal("real upload inputs were not seeded", len(before))
				}
				source := f.SQLiteStore
				if bounded {
					source, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
				}
				scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw51"}}}
				switch name {
				case "read-only-import":
					scope.Read = []RecordKey{{Kind: "importJobs", ID: first}}
				case "import-point", "complete-import":
					scope = MetadataRecordScope{Write: []RecordKey{{Kind: "importJobs", ID: first}}}
				case "delete-migration":
					scope = MetadataRecordScope{Write: []RecordKey{{Kind: "migrationJobs", ID: first}}}
				case "import-field":
					scope = MetadataRecordScope{Fields: []string{"importJobs"}}
				case "migration-field":
					scope = MetadataRecordScope{Fields: []string{"migrationJobs"}}
				}
				ctx := WithMetadataFields(t.Context(), metadataRecordFields(scope)...)
				err = source.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", "", nil, scope, func(data *domain.Bootstrap) error {
					switch name {
					case "unrelated-setting", "read-only-import":
						data.Settings["sw51"] = "after"
					case "import-point":
						data.ImportJobs[0].Status = "cancelled"
					case "complete-import":
						data.ImportJobs[0].Status = "completed"
					case "delete-migration":
						data.MigrationJobs = slices.DeleteFunc(data.MigrationJobs, func(job domain.MigrationJob) bool { return job.ID == first })
					case "import-field":
						data.ImportJobs = slices.DeleteFunc(data.ImportJobs, func(job domain.ImportJob) bool { return job.ID == first })
					case "migration-field":
						data.MigrationJobs = []domain.MigrationJob{}
					}
					return nil
				})
				if err != nil {
					t.Fatal("input mutation", bounded, err)
				}
				after := scaleP21SW51InputRows(t, f.SQLiteStore, workspace)
				expected := map[string][32]byte{}
				for key, value := range before {
					expected[key] = value
				}
				switch name {
				case "complete-import", "import-field":
					delete(expected, "import_input/"+first)
				case "delete-migration":
					delete(expected, "migration_input/"+first)
				case "migration-field":
					delete(expected, "migration_input/"+first)
					delete(expected, "migration_input/"+second)
				}
				if !reflect.DeepEqual(after, expected) {
					t.Fatalf("input membership/body changed outside declared writes: bounded=%v before=%v after=%v expected=%v", bounded, before, after, expected)
				}
				if bounded {
					if source.WorkspaceMetadataDecodes() != 0 || source.metadataResidency.work.used != 0 {
						t.Fatal("bounded input maintenance hydrated full state or leaked work")
					}
					source.releaseMetadataLogicalSources()
				}
				results = append(results, after)
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatal("old versus bounded input effects differ")
			}
		})
	}
}
