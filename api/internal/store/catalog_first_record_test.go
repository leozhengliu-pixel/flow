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
)

// scaleCompleteWorkspaceCatalog completes the metadata catalog of every
// collection of workspace, as the shadow catalog worker does.
func scaleCompleteWorkspaceCatalog(t *testing.T, f scaleFixture, workspace string) {
	t.Helper()
	rows, err := f.db.QueryContext(t.Context(), `SELECT DISTINCT field FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for rows.Next() {
		var field string
		if err := rows.Scan(&field); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		fields = append(fields, field)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		for pass := 0; ; pass++ {
			progress, err := f.BackfillMetadataCatalog(t.Context(), workspace, field)
			if err != nil && !errors.Is(err, ErrConcurrentWrite) {
				t.Fatalf("catalog %s: %v", field, err)
			}
			if err == nil && progress.Complete {
				break
			}
			if pass > 4096 {
				t.Fatalf("catalog %s did not complete", field)
			}
		}
	}
}

// A flag-off replica can maintain catalog summaries without opening a
// checkpoint. A flag-on writer with an older snapshot must count those
// summaries when it publishes the first checkpoint.
func TestRecordJournalMySQLCheckpointUsesCurrentOmissions(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := captureMySQLFixture(t)
	workspace, field := f.data.Workspace.URLKey, "emailIntakeMessages"
	ctx := t.Context()
	tx, err := f.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, workspace, field).Scan(&count); err != nil || count != 0 {
		t.Fatal("expected an empty collection in the write snapshot", count, err)
	}
	const put = `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?)`
	if _, err := f.db.ExecContext(ctx, put, workspace, field, "legacy", []byte(`"opaque"`)); err != nil {
		t.Fatal(err)
	}
	if _, _, found := scaleCatalogCheckpoint(t, f, workspace, field); found {
		t.Fatal("flag-off write opened a checkpoint")
	}
	f.db.boundedCatalog.Store(true)
	if _, err := tx.ExecContext(ctx, put, workspace, field, "current", []byte(`"opaque"`)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if phase, omissions, found := scaleCatalogCheckpoint(t, f, workspace, field); !found || phase != 2 || omissions != 2 {
		t.Fatal("new checkpoint did not count both omissions", found, phase, omissions)
	}
}

func TestRecordJournalMySQLBackfillLockOrder(t *testing.T) {
	for _, test := range []struct {
		name       string
		checkpoint bool
	}{
		{"existing-checkpoint", true},
		{"flag-off-writer", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_METADATA_CATALOG", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			f := captureMySQLFixture(t)
			workspace, field := f.data.Workspace.URLKey, "emailIntakeMessages"
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			f.db.boundedCatalog.Store(test.checkpoint)
			const put = `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data)
				VALUES(?,?,'message',0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`
			if _, err := f.db.ExecContext(ctx, put, workspace, field, []byte(`{"id":"message","name":"before"}`)); err != nil {
				t.Fatal(err)
			}
			if test.checkpoint {
				if err := f.ResetMetadataCatalogBackfill(ctx, workspace, field); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, found := scaleCatalogCheckpoint(t, f, workspace, field); found != test.checkpoint {
				t.Fatal("unexpected checkpoint state before backfill")
			}

			writer, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback()
			if _, err := writer.ExecContext(ctx, put, workspace, field, []byte(`{"id":"message","name":"after"}`)); err != nil {
				t.Fatal(err)
			}

			seeding := make(chan struct{})
			backfillDone := make(chan error, 1)
			backfill := WithSQLStatementObserver(ctx, func(query string) {
				if strings.HasPrefix(query, "INSERT INTO record_versions(") {
					close(seeding)
				}
			})
			go func() {
				_, err := f.BackfillMetadataCatalog(backfill, workspace, field)
				backfillDone <- err
			}()
			select {
			case <-seeding:
			case err := <-backfillDone:
				t.Fatalf("backfill finished before seeding versions: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

			writerErr := writer.Commit()
			backfillErr := <-backfillDone
			if writerErr != nil || !errors.Is(backfillErr, ErrConcurrentWrite) {
				t.Fatalf("writer must commit and backfill must discard its stale snapshot: writer=%v backfill=%v", writerErr, backfillErr)
			}
		})
	}
}

func scaleCatalogReady(t *testing.T, f scaleFixture, workspace string) error {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return f.validateResidentBackfill(t.Context(), tx, workspace)
}

func scaleCatalogCheckpoint(t *testing.T, f scaleFixture, workspace, field string) (phase int, omissions int64, found bool) {
	t.Helper()
	err := f.db.QueryRowContext(t.Context(), `SELECT phase,omissions FROM metadata_catalog_collections WHERE workspace_key=? AND field=?`, workspace, field).Scan(&phase, &omissions)
	if err == nil {
		return phase, omissions, true
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return 0, 0, false
}

// A collection whose first record a bounded (point) writer creates after the
// catalog pass gets a complete catalog checkpoint in that write, so a COLD
// write that follows publishes its workspace: the installed image equals a
// fresh SQL reload and the legacy result. Flag off, the same writes open no
// checkpoint. Before, every reload of the workspace was refused ("canonical
// records, catalog or old-writer repair is incomplete") and the installed
// copy stayed stale.
func TestScaleCatalogFirstRecordThenColdWrite(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	workspace := f.data.Workspace.URLKey
	workflowID := "catalog-first-" + f.data.Workspace.ID
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.catalog_first_seed", "", nil, func(data *domain.Bootstrap) error {
		data.WorkflowDefinitions = []domain.WorkflowDefinition{{ID: workflowID, Name: "First run", Enabled: true}}
		data.WorkflowRuns = nil
		data.Settings["catalog-first-target"] = map[string]any{"value": "before"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var runs int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='workflowRuns'`, workspace).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("fixture already has workflow runs", runs, err)
	}
	scaleCompleteWorkspaceCatalog(t, f, workspace)
	if err := scaleCatalogReady(t, f, workspace); err != nil {
		t.Fatal("completed catalog is not ready", err)
	}
	stamp := time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	run := domain.WorkflowRun{ID: workflowID + "-first", WorkflowID: workflowID, Trigger: "manual", Status: "succeeded", Attempt: 1,
		Output: map[string]string{"issue": "created"}, ScheduledAt: stamp, StartedAt: stamp}
	addRun := func(data *domain.Bootstrap) error {
		data.WorkflowRuns = append(data.WorkflowRuns, run)
		return nil
	}
	setTarget := func(data *domain.Bootstrap) (string, error) {
		data.Settings["catalog-first-target"] = map[string]any{"value": "after"}
		return "", nil
	}

	// Legacy (flag off): the same two writes on the literal store.
	f.mu.RLock()
	original := cloneBootstrap(f.workspaces[workspace])
	f.mu.RUnlock()
	saved := scaleColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
	if err := f.MutateWorkspace(t.Context(), workspace, "workflow_run.executed", workflowID, nil, addRun); err != nil {
		t.Fatal(err)
	}
	if err := f.mutateFull(t.Context(), workspace, "application_policy.updated", nil, setTarget); err != nil {
		t.Fatal(err)
	}
	if _, _, found := scaleCatalogCheckpoint(t, f, workspace, "workflowRuns"); found {
		t.Fatal("a flag-off write opened a catalog checkpoint")
	}
	wantSQL := scaleColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
	wantNative, _ := f.WorkspaceMetadata(workspace)
	wantNative = cloneBootstrap(wantNative)
	scaleColdPreferenceRestore(t, f, workspace, saved, original)
	if err := scaleCatalogReady(t, f, workspace); err != nil {
		t.Fatal("restored catalog is not ready", err)
	}

	// Bounded: a bounded store serves the database (production coverage).
	f.db.boundedCatalog.Store(true)
	t.Cleanup(func() { f.db.boundedCatalog.Store(false) })
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	view.metadataResidency.requireCoverage = true
	var closes []func()
	output := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
		closes = append(closes, close)
		return true
	})
	err = view.MutateWorkflowRun(output, workspace, workflowID, "", "", nil, addRun)
	for _, close := range closes {
		close()
	}
	if err != nil {
		t.Fatal("bounded workflow run writer", err)
	}
	if phase, omissions, found := scaleCatalogCheckpoint(t, f, workspace, "workflowRuns"); !found || phase != 2 || omissions != 0 {
		t.Fatal("the first workflow run opened no complete catalog checkpoint", found, phase, omissions)
	}
	if err := scaleCatalogReady(t, f, workspace); err != nil {
		t.Fatal("catalog incomplete after the first workflow run", err)
	}
	if err := view.coldLegacyMutation(t.Context(), workspace, "application_policy.updated", nil, setTarget); err != nil {
		t.Fatal("COLD legacy write", err)
	}
	if got := scaleColdPreferenceSnapshot(t, f.SQLiteStore, workspace); !reflect.DeepEqual(got.rows, wantSQL.rows) {
		t.Fatal("bounded canonical records differ from legacy")
	}
	warm := scaleColdTeamNative(t, view, workspace)

	// A fresh bounded view hydrated from SQL with production coverage.
	fresh, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fresh.releaseMetadataLogicalSources)
	fresh.metadataResidency.requireCoverage = true
	fresh.writeMu.Lock()
	fresh.dropResidentWorkspace(workspace)
	err = fresh.reloadResidentWorkspaceLogical(t.Context(), workspace, true)
	fresh.writeMu.Unlock()
	if err != nil {
		t.Fatal("SQL reload with coverage", err)
	}
	reloaded := scaleColdTeamNative(t, fresh, workspace)
	legacyReload, _, err := f.loadWorkspaceStamped(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if target := warm.Settings["catalog-first-target"]; !reflect.DeepEqual(target, map[string]any{"value": "after"}) || len(warm.WorkflowRuns) != 1 {
		t.Fatal("the installed image is stale after the COLD write", target, len(warm.WorkflowRuns))
	}
	for name, image := range map[string]domain.Bootstrap{"warm": warm, "reloaded": reloaded} {
		if !reflect.DeepEqual(image.WorkflowRuns, wantNative.WorkflowRuns) || !reflect.DeepEqual(image.Settings, wantNative.Settings) ||
			!reflect.DeepEqual(image.WorkflowRuns, legacyReload.WorkflowRuns) || !reflect.DeepEqual(image.Settings, legacyReload.Settings) {
			t.Fatalf("%s image differs from legacy", name)
		}
	}
	if !reflect.DeepEqual(warm, reloaded) {
		t.Fatal("the installed image differs from a fresh SQL reload")
	}
}

// The checkpoint opened for a new collection starts from the catalog's
// omission count, and a collection with older uncatalogued records is left
// to the backfill.
func TestScaleCatalogNewCollectionCheckpointBounds(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	workspace := f.data.Workspace.URLKey
	scaleCompleteWorkspaceCatalog(t, f, workspace)
	f.db.boundedCatalog.Store(true)
	t.Cleanup(func() { f.db.boundedCatalog.Store(false) })
	write := func(field, key string, data string) {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, field, key, []byte(data)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	// One omitted (non-object) record and one summarized record, in two
	// transactions: the second adds its omission delta to the checkpoint.
	write("emailIntakeMessages", "first", `"opaque"`)
	if phase, omissions, found := scaleCatalogCheckpoint(t, f, workspace, "emailIntakeMessages"); !found || phase != 2 || omissions != 1 {
		t.Fatal("new collection checkpoint", found, phase, omissions)
	}
	write("emailIntakeMessages", "second", `["opaque"]`)
	if _, omissions, _ := scaleCatalogCheckpoint(t, f, workspace, "emailIntakeMessages"); omissions != 2 {
		t.Fatal("omission delta not applied", omissions)
	}
	// A collection holding a record the catalog lacks (an old writer's)
	// stays for the backfill.
	f.db.boundedCatalog.Store(false)
	write("emailIntakeAddresses", "old", `{"id":"old"}`)
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM metadata_catalog WHERE workspace_key=? AND field='emailIntakeAddresses' AND record_key='old'`, workspace); err != nil {
		t.Fatal(err)
	}
	f.db.boundedCatalog.Store(true)
	write("emailIntakeAddresses", "new", `{"id":"new"}`)
	if _, _, found := scaleCatalogCheckpoint(t, f, workspace, "emailIntakeAddresses"); found {
		t.Fatal("a collection with an uncatalogued record was marked complete")
	}
}
