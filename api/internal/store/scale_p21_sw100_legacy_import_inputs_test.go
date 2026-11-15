package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW100Row struct {
	Workspace, Kind, Resource, ID, Created, Raw, Owner, Parent, Status, Next string
	Version                                                                  int
}

func scaleP21SW100Fixture(t *testing.T) (scaleP09Fixture, *SQLiteStore) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	f.db.SetMaxOpenConns(1)
	return f, view
}

func scaleP21SW100Rows(t *testing.T, tx *sqlTx, workspace string) []scaleP21SW100Row {
	t.Helper()
	rows, err := tx.QueryContext(t.Context(), `SELECT workspace_key,kind,resource_id,id,created_at,data,owner_id,parent_id,status,next_attempt_at,record_version FROM workspace_content_records WHERE workspace_key IN (?,?) ORDER BY workspace_key,kind,resource_id,id`, workspace, workspace+"-foreign")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []scaleP21SW100Row{}
	for rows.Next() {
		var row scaleP21SW100Row
		var raw []byte
		if err := rows.Scan(&row.Workspace, &row.Kind, &row.Resource, &row.ID, &row.Created, &raw, &row.Owner, &row.Parent, &row.Status, &row.Next, &row.Version); err != nil {
			t.Fatal(err)
		}
		row.Raw = string(raw)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW100Data(f scaleP09Fixture) domain.Bootstrap {
	at := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	return domain.Bootstrap{
		Workspace: f.data.Workspace, Viewer: f.data.Viewer, ViewerRole: "native-role",
		ImportJobs: []domain.ImportJob{
			{ID: "duplicate", Status: "pending", Rows: []map[string]string{{"first": "value"}}, CreatedAt: at},
			{ID: "kept-compact", Status: "pending", Rows: nil, CreatedAt: at},
			{ID: "duplicate", Status: "pending", Rows: []map[string]string{{"last": "value", "": ""}}, CreatedAt: at.Add(time.Second)},
			{ID: "duplicate", Status: "pending", Rows: nil, CreatedAt: at},
			{ID: "", Status: "", Rows: []map[string]string{}, CreatedAt: at},
			{ID: "completed", Status: "completed", Rows: []map[string]string{{"skip": "entire input"}}, CreatedAt: at},
			{ID: "case-sensitive", Status: "Completed", Rows: []map[string]string{{"retain": "case"}}, CreatedAt: at},
		},
		MigrationJobs: []domain.MigrationJob{
			{ID: "duplicate", Status: "completed", Bundle: json.RawMessage(`{"first":true}`), CreatedAt: at},
			{ID: "migration-kept", Status: "pending", Bundle: nil, CreatedAt: at},
			{ID: "duplicate", Bundle: json.RawMessage(`{"last":"exact"}`), CreatedAt: at.Add(time.Second)},
			{ID: "", Bundle: json.RawMessage(`null`), CreatedAt: at},
		},
		Documents: []domain.Document{{ID: "unrelated-document", Content: "Preserve full source body"}},
		Settings:  map[string]any{"native": map[string]any{"integer": int64(9007199254740993), "nil": []string(nil), "empty": []string{}, "bytes": []byte{0, 255}}},
	}
}

func scaleP21SW100Seed(t *testing.T, f scaleP09Fixture, prior string) *sqlTx {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	workspace := f.data.Workspace.URLKey
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key IN (?,?) AND kind IN ('import_input','migration_input')`, workspace, workspace+"-foreign"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	for _, spec := range []struct{ workspace, kind, resource, id string }{
		{workspace, "import_input", "duplicate", "duplicate"},
		{workspace, "import_input", "other-resource", "duplicate"},
		{workspace, "import_input", "kept-compact", "kept-compact"},
		{workspace, "import_input", "completed", "completed"},
		{workspace, "import_input", "orphan", "orphan"},
		{workspace, "migration_input", "duplicate", "duplicate"},
		{workspace, "migration_input", "migration-kept", "migration-kept"},
		{workspace, "migration_input", "orphan", "orphan"},
		{workspace + "-foreign", "import_input", "foreign", "foreign"},
	} {
		value := importInput{ID: spec.id, CreatedAt: at}
		if spec.kind == "import_input" {
			value.Rows = []map[string]string{{"old": prior}}
		} else {
			value.Bundle = json.RawMessage(`{"old":true}`)
		}
		if err := writeContentRecord(t.Context(), tx, spec.workspace, spec.kind, spec.resource, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeContentRecord(t.Context(), tx, workspace, "comment", "unrelated", domain.Comment{ID: "unrelated", Body: "Preserve other content", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	return tx
}

func scaleP21SW100Compare(t *testing.T, f scaleP09Fixture, view *SQLiteStore, source *metadataLogicalSource, prior string, limit int) int {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	scope := MetadataRecordScope{Fields: []string{"importJobs", "migrationJobs", "documents", "settings"}}
	before, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	tx := scaleP21SW100Seed(t, f, prior)
	initial := scaleP21SW100Rows(t, tx, workspace)
	wantErr := writeImportInputs(t.Context(), tx, workspace, before)
	want := scaleP21SW100Rows(t, tx, workspace)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx = scaleP21SW100Seed(t, f, prior)
	if !reflect.DeepEqual(scaleP21SW100Rows(t, tx, workspace), initial) {
		t.Fatal("import-input paired SQL preimages differ")
	}
	budget, previousLimit := view.metadataResidency.work, view.metadataResidency.work.limit
	if limit != 0 {
		budget.limit = limit
	}
	defer func() { budget.limit = previousLimit }()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	peak, decodes := 0, view.WorkspaceMetadataDecodes()
	gotErr := view.persistLegacyImportInputs(WithSQLStatementObserver(t.Context(), func(string) { peak = max(peak, view.MetadataWorkUsedForTesting()) }), tx, workspace, source)
	if reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) || wantErr != nil && gotErr.Error() != wantErr.Error() {
		t.Fatalf("literal/owned input errors differ: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
	}
	if got := scaleP21SW100Rows(t, tx, workspace); !reflect.DeepEqual(got, want) {
		t.Fatal("literal/owned input rows, indexes, versions or prune results differ")
	}
	unchanged, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil || !reflect.DeepEqual(unchanged, before) {
		t.Fatal("input persistence mutated its source", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != decodes ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("input persistence retained work/staging or decoded full metadata")
	}
	scaleP21SW70Usable(t, tx)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	return peak
}

func TestScaleP21SW100LegacyImportInputsLiteral(t *testing.T) {
	f, view := scaleP21SW100Fixture(t)
	for _, variant := range []string{"duplicates-status-order", "nil-jobs", "empty-jobs", "compacted-prunes-orphans", "empty-raw-bundle-error", "invalid-raw-bundle-error", "legal-large-prior-body"} {
		t.Run(variant, func(t *testing.T) {
			data, prior := scaleP21SW100Data(f), "Original stored body"
			switch variant {
			case "nil-jobs":
				data.ImportJobs, data.MigrationJobs = nil, nil
			case "empty-jobs":
				data.ImportJobs, data.MigrationJobs = []domain.ImportJob{}, []domain.MigrationJob{}
			case "compacted-prunes-orphans":
				data = compactImportInputs(data)
			case "empty-raw-bundle-error":
				data.MigrationJobs = append(data.MigrationJobs, domain.MigrationJob{ID: "empty-bundle", Bundle: json.RawMessage{}})
			case "invalid-raw-bundle-error":
				data.MigrationJobs = append(data.MigrationJobs, domain.MigrationJob{ID: "bad-bundle", Bundle: json.RawMessage(`{"invalid":`)})
			case "legal-large-prior-body":
				prior = strings.Repeat("Complete legal mostly-string stored input. ", 1<<16)
			}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			scaleP21SW100Compare(t, f, view, source, prior, 0)
		})
	}
}

func TestScaleP21SW100LegacyImportInputsWarmReloadHeaderBound(t *testing.T) {
	peaks := map[bool]int{}
	for _, size := range []int{2000, 20000} {
		t.Run(fmt.Sprintf("body-%d", size), func(t *testing.T) {
			// Each size starts from a new owned workspace. Re-seeding changed
			// duplicate/inline jobs through the literal delta would itself
			// rebase missing split rows and alter the comparison's job set.
			f, view := scaleP21SW100Fixture(t)
			workspace := f.data.Workspace.URLKey
			body := strings.Repeat("Unneeded job metadata. ", size)
			data := compactImportInputs(scaleP21SW100Data(f))
			for index := range data.ImportJobs {
				data.ImportJobs[index].Filename = body
				data.ImportJobs[index].Mapping = map[string]string{"unrelated": body}
			}
			for index := range data.MigrationJobs {
				data.MigrationJobs[index].Filename = body
				data.MigrationJobs[index].Mappings = []domain.MigrationEntityMapping{{ID: "mapping", Metadata: map[string]any{"unrelated": body}}}
			}
			f.db.SetMaxOpenConns(4)
			restore := ForceFullMutationsForTesting(true)
			err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw100_seed", "", nil, func(next *domain.Bootstrap) error {
				next.ImportJobs, next.MigrationJobs = data.ImportJobs, data.MigrationJobs
				return nil
			})
			restore()
			if err != nil {
				t.Fatal(err)
			}
			for _, reload := range []bool{false, true} {
				t.Run(fmt.Sprintf("reload-%t", reload), func(t *testing.T) {
					f.db.SetMaxOpenConns(4)
					if reload {
						f.mu.Lock()
						f.forgetCopyRevisionLocked(workspace)
						f.mu.Unlock()
						if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
						t.Fatal(err)
					}
					source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					defer source.Release()
					f.db.SetMaxOpenConns(1)
					peak := scaleP21SW100Compare(t, f, view, source, strings.Repeat("Untouched complete stored body. ", 1<<17), 8<<20)
					if peak == 0 {
						t.Fatal("header-only input persistence admitted no work")
					}
					if peaks[reload] == 0 {
						peaks[reload] = peak
					}
					if peaks[reload] != peak {
						t.Fatal("10x unused job bodies increased header/prune work", peak, peaks[reload])
					}
					t.Logf("body_size=%d reload=%t input_sql_peak=%d", size, reload, peak)
				})
			}
		})
	}
}

func TestScaleP21SW100LegacyContentBeforeGuard(t *testing.T) {
	f, view := scaleP21SW100Fixture(t)
	workspace := f.data.Workspace.URLKey
	tx := scaleP21SW100Seed(t, f, strings.Repeat("Legal previous mostly-string input. ", 1<<17))
	raw, version, release, err := readLegacyContentBefore(t.Context(), tx, workspace, "import_input", "duplicate", "duplicate", view)
	if err != nil || len(raw) <= 2<<20 || version != 1 || view.MetadataWorkUsedForTesting() == 0 {
		release()
		t.Fatal("legal large previous input was refused or uncharged", len(raw), version, err)
	}
	var want []byte
	var wantVersion int
	if err := tx.QueryRowContext(t.Context(), `SELECT data,record_version FROM workspace_content_records WHERE workspace_key=? AND kind='import_input' AND resource_id='duplicate' AND id='duplicate'`, workspace).Scan(&want, &wantVersion); err != nil {
		release()
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) || version != wantVersion {
		release()
		t.Fatal("guard changed the literal previous row")
	}
	release()
	release()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("previous-row release retained work")
	}
	_, _, release, err = readLegacyContentBefore(t.Context(), tx, workspace, "import_input", "missing", "missing", view)
	release()
	if !errors.Is(err, sql.ErrNoRows) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("missing previous-row contract changed", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	_, _, release, err = readLegacyContentBefore(t.Context(), tx, workspace, "import_input", "duplicate", "duplicate", view)
	release()
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("previous-row quota failed to clean up", err)
	}
	scaleP21SW70Usable(t, tx)
}

func TestScaleP21SW100LegacyImportInputsActualReadChanges(t *testing.T) {
	f, view := scaleP21SW100Fixture(t)
	workspace := f.data.Workspace.URLKey
	for _, variant := range []string{"prior-body-growth", "prior-read-cancel", "prune-header-growth", "cancel-before", "quota"} {
		t.Run(variant, func(t *testing.T) {
			tx := scaleP21SW100Seed(t, f, "Small prior value")
			data := scaleP21SW100Data(f)
			data.ImportJobs = data.ImportJobs[:1]
			data.MigrationJobs = nil
			if variant == "prune-header-growth" {
				data = compactImportInputs(data)
			}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			scope := MetadataRecordScope{Fields: []string{"importJobs", "migrationJobs"}}
			before, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fired := false
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if fired || !strings.Contains(query, "workspace_content_records") {
					return
				}
				if (variant == "prior-body-growth" || variant == "prior-read-cancel") && strings.Contains(query, "THEN data ELSE NULL END") {
					fired = true
					if variant == "prior-read-cancel" {
						cancel()
						return
					}
					value := importInput{ID: "duplicate", Rows: []map[string]string{{"grown": strings.Repeat("After preflight. ", 1000)}}, CreatedAt: time.Now().UTC()}
					raw, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='import_input' AND resource_id='duplicate' AND id='duplicate'`, raw, workspace); err != nil {
						t.Fatal(err)
					}
				} else if variant == "prune-header-growth" && strings.Contains(query, "THEN id ELSE NULL END") {
					fired = true
					id := strings.Repeat("n", 180)
					if err := writeContentRecord(t.Context(), tx, workspace, "import_input", id, importInput{ID: id, CreatedAt: time.Now().UTC()}); err != nil {
						t.Fatal(err)
					}
				}
			})
			if variant == "cancel-before" {
				cancel()
			}
			limit := view.metadataResidency.work.limit
			if variant == "quota" {
				view.metadataResidency.work.limit = 1
			}
			gotErr := view.persistLegacyImportInputs(ctx, tx, workspace, source)
			view.metadataResidency.work.limit = limit
			switch variant {
			case "prior-body-growth", "prune-header-growth":
				if !fired || !errors.Is(gotErr, ErrConcurrentWrite) {
					t.Fatal("post-preflight growth escaped the actual read guard", fired, gotErr)
				}
			case "prior-read-cancel":
				if !fired || !errors.Is(gotErr, context.Canceled) {
					t.Fatal("actual guarded read ignored cancellation", fired, gotErr)
				}
			case "cancel-before":
				if !errors.Is(gotErr, context.Canceled) {
					t.Fatal("canceled input persistence ran", gotErr)
				}
			case "quota":
				if !errors.Is(gotErr, ErrMetadataWorkBudget) {
					t.Fatal("input persistence ignored work quota", gotErr)
				}
			}
			if view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("failed input persistence retained work or staging")
			}
			unchanged, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil || !reflect.DeepEqual(unchanged, before) {
				t.Fatal("failed input persistence mutated the source", err)
			}
			scaleP21SW70Usable(t, tx)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func scaleP21SW100HydrationRows(t *testing.T, f scaleP09Fixture, variant string, foreign bool) *sqlTx {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	workspace := f.data.Workspace.URLKey
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind IN ('import_input','migration_input')`, workspace); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	put := func(ws, kind, resource, id string, seconds int, raw []byte) {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,?)`,
			ws, kind, resource, id, at.Add(time.Duration(seconds)*time.Second).Format(issueRecordTimestamp), raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"import_input", "migration_input"} {
		for _, row := range []struct {
			resource, id, marker string
			seconds              int
		}{
			{"duplicate", "z-row", "z", 0}, {"duplicate", "a-row", "a", 0},
			{"kept-compact", "kept-row", "kept", 1}, {"migration-kept", "migration-kept", "kept", 1},
			{"completed", "completed-row", "completed", 1}, {"", "empty-resource", "empty", 1},
			{"orphan", "orphan-row", "orphan", 1},
		} {
			marker := row.marker
			if variant == "large-input" && row.resource == "duplicate" && row.id == "a-row" {
				marker = strings.Repeat("Full legal input. ", 1<<15)
			}
			input := importInput{ID: "decoded-id-must-not-regroup-" + row.id, CreatedAt: at,
				Rows: []map[string]string{{"which": marker}}, Bundle: json.RawMessage(`{"which":"` + marker + `"}`)}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "migration_input" && row.resource == "migration-kept" {
				raw = []byte(`{"id":"decoded-whitespace","bundle": { "nested" : [1, 2] }, "createdAt":"2026-11-14T01:02:03Z"}`)
			}
			put(workspace, kind, row.resource, row.id, row.seconds, raw)
		}
		put(workspace, kind, "nil-first", "nil-first", -1, []byte("null"))
		raw, err := json.Marshal(importInput{ID: "later", Rows: []map[string]string{{"ignored": "later"}}, Bundle: json.RawMessage(`{"ignored":"later"}`), CreatedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		put(workspace, kind, "nil-first", "nil-later", 2, raw)
		if foreign {
			put(workspace+"-foreign", kind, "ignored-foreign", "ignored-foreign", -5, []byte(`{"rows":`))
		}
	}
	if strings.Contains(variant, "bad-import") {
		put(workspace, "import_input", "unmatched-bad", "bad-first", -4, []byte(`{"rows":17}`))
		put(workspace, "import_input", "unmatched-bad", "bad-second", -3, []byte(`{"bundle":`))
	}
	if strings.Contains(variant, "bad-migration") {
		put(workspace, "migration_input", "unmatched-bad", "bad-migration", -4, []byte(`{"rows":true}`))
	}
	return tx
}

func TestScaleP21SW100LegacyImportImageLiteral(t *testing.T) {
	f, view := scaleP21SW100Fixture(t)
	workspace := f.data.Workspace.URLKey
	for _, variant := range []string{
		"mixed", "compacted", "nil-jobs", "empty-jobs", "supplied-empty", "supplied-empty-bundle", "supplied-values",
		"large-input", "bad-import-bad-migration", "bad-migration", "supplied-bad-import", "nil-jobs-bad-migration",
	} {
		t.Run(variant, func(t *testing.T) {
			data := scaleP21SW100Data(f)
			data.ImportJobs = append(data.ImportJobs, domain.ImportJob{ID: "nil-first", Status: "pending"})
			data.MigrationJobs = append(data.MigrationJobs, domain.MigrationJob{ID: "nil-first"})
			switch variant {
			case "compacted":
				data = compactImportInputs(data)
			case "nil-jobs", "nil-jobs-bad-migration":
				data.ImportJobs, data.MigrationJobs = nil, nil
			case "empty-jobs":
				data.ImportJobs, data.MigrationJobs = []domain.ImportJob{}, []domain.MigrationJob{}
			case "supplied-empty":
				for i := range data.ImportJobs {
					data.ImportJobs[i].Rows = []map[string]string{}
				}
				for i := range data.MigrationJobs {
					data.MigrationJobs[i].Bundle = json.RawMessage(`null`)
				}
			case "supplied-empty-bundle":
				for i := range data.MigrationJobs {
					data.MigrationJobs[i].Bundle = json.RawMessage{}
				}
			case "supplied-values", "supplied-bad-import":
				for i := range data.ImportJobs {
					data.ImportJobs[i].Rows = []map[string]string{{"provided": "unchanged"}}
				}
				for i := range data.MigrationJobs {
					data.MigrationJobs[i].Bundle = json.RawMessage(`{"provided":"unchanged"}`)
				}
			}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(source.Release)
			scope := MetadataRecordScope{Fields: []string{"importJobs", "migrationJobs", "documents", "settings"}}
			before, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			want, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			tx := scaleP21SW100HydrationRows(t, f, variant, true)
			initial := scaleP21SW100Rows(t, tx, workspace)
			// The original database wrapper binds an explicit snapshot by
			// database, even when its namespace label differs.
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace + "-caller-label", reader: tx})
			wantErr := f.hydrateImportInputs(ctx, workspace, &want)
			queries := 0
			result, gotErr := view.prepareLegacyImportImage(WithSQLStatementObserver(ctx, func(string) { queries++ }), workspace, source)
			if reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) || wantErr != nil && gotErr.Error() != wantErr.Error() {
				if result != nil {
					result.Release()
				}
				t.Fatalf("literal/import-image errors differ: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
			}
			if wantErr != nil {
				if result != nil {
					result.Release()
					t.Fatal("failed hydration exposed a partial source")
				}
			} else {
				if result == nil {
					t.Fatal("successful hydration omitted its owned source")
				}
				t.Cleanup(result.Release)
				scaleP21SW72Compare(t, result, scope, want)
				if result.Stamp() != source.Stamp() {
					t.Fatal("import hydration changed source generation")
				}
				for i, node := range source.fields {
					name := reflect.TypeFor[domain.Bootstrap]().Field(i).Tag.Get("json")
					if name != "-" && name != "importJobs" && name != "migrationJobs" && result.fields[i] != node {
						t.Fatal("import hydration rewrote another source field", name)
					}
				}
			}
			unchanged, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil || !reflect.DeepEqual(unchanged, before) {
				t.Fatal("import hydration mutated its source", err)
			}
			if !reflect.DeepEqual(scaleP21SW100Rows(t, tx, workspace), initial) {
				t.Fatal("import hydration changed canonical input rows")
			}
			if queries == 0 {
				t.Fatal("input kinds were not read even for supplied/nil job collections")
			}
			scaleP21SW70Usable(t, tx)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			source.Release()
			if result != nil {
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := result.Select(cancelled, scope, 64<<20); !errors.Is(err, context.Canceled) {
					t.Fatal("canceled result read did not cancel", err)
				}
				scaleP21SW72Compare(t, result, scope, want)
				result.Release()
				result.Release()
			}
			if view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk ||
				metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("import hydration retained transient work or source/output storage")
			}
		})
	}
}

func TestScaleP21SW100LegacyImportImageIndependentKinds(t *testing.T) {
	f, view := scaleP21SW100Fixture(t)
	workspace := f.data.Workspace.URLKey
	f.db.SetMaxOpenConns(4)
	tx := scaleP21SW100HydrationRows(t, f, "normal", false)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	data := compactImportInputs(scaleP21SW100Data(f))
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	scope := MetadataRecordScope{Fields: []string{"importJobs", "migrationJobs", "documents", "settings"}}
	want, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.hydrateImportInputs(t.Context(), workspace, &want); err != nil {
		t.Fatal(err)
	}
	changedBundle := json.RawMessage(`{"changed":"between-kinds"}`)
	for i := range want.MigrationJobs {
		if want.MigrationJobs[i].ID == "duplicate" {
			want.MigrationJobs[i].Bundle = changedBundle
		}
	}
	preflights, changed := 0, false
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if !strings.Contains(query, "COALESCE(MAX(") || !strings.Contains(query, "workspace_content_records") {
			return
		}
		preflights++
		if preflights != 2 {
			return
		}
		changed = true
		for _, kind := range []string{"import_input", "migration_input"} {
			input := importInput{ID: "duplicate", Rows: []map[string]string{{"changed": "too-late"}}, Bundle: changedBundle}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind=? AND resource_id='duplicate' AND id='a-row'`, raw, workspace, kind); err != nil {
				t.Fatal(err)
			}
		}
	})
	result, err := view.prepareLegacyImportImage(ctx, workspace, source)
	if err != nil || result == nil {
		t.Fatal("independent-kind hydration failed", err)
	}
	defer result.Release()
	if !changed || preflights != 2 {
		t.Fatal("per-kind snapshot transition was not exercised", preflights)
	}
	scaleP21SW72Compare(t, result, scope, want)
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("independent-kind hydration retained work")
	}
}

func TestScaleP21SW100LegacyImportImageFailures(t *testing.T) {
	f, view := scaleP21SW100Fixture(t)
	workspace := f.data.Workspace.URLKey
	data := compactImportInputs(scaleP21SW100Data(f))
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	tx := scaleP21SW100HydrationRows(t, f, "normal", true)
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	result, err := view.prepareLegacyImportImage(cancelled, workspace, source)
	if result != nil || !errors.Is(err, context.Canceled) {
		if result != nil {
			result.Release()
		}
		t.Fatal("canceled import hydration escaped", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	result, err = view.prepareLegacyImportImage(ctx, workspace, source)
	view.metadataResidency.work.limit = limit
	if result != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		if result != nil {
			result.Release()
		}
		t.Fatal("import hydration ignored quota", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("failed import hydration retained work or staging")
	}
	scaleP21SW70Usable(t, tx)
}
