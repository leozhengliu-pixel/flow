package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"flow/api/internal/domain"
)

func TestDefaultMetadataUpgradeUsesLegacyWhenAdmissionFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	flags := []string{"FLOW_BOUNDED_METADATA", "FLOW_RECORD_HYDRATION", "FLOW_RECORD_JOURNAL", "FLOW_METADATA_CATALOG"}
	for _, flag := range flags {
		t.Setenv(flag, "off")
	}
	t.Setenv("FLOW_RECORD_PEER_SYNC", "")
	open := func() *SQLiteStore {
		t.Helper()
		repo, err := OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	old := open()
	workspace := old.Bootstrap().Workspace.URLKey
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	for _, flag := range flags {
		t.Setenv(flag, "")
	}
	t.Setenv("FLOW_METADATA_SOURCE_BYTES", "1")
	fallback := open()
	t.Cleanup(func() { _ = fallback.Close() })
	if fallback.BoundedMetadataEnabled() || !fallback.RecordJournalReady() {
		t.Fatal("bounded admission failure should keep the journal and serve legacy reads")
	}
	data, ok := fallback.BootstrapFor(workspace)
	if !ok || len(data.Issues) == 0 {
		t.Fatal("legacy fallback lost the installed workspace")
	}
	const projectID = "upgrade-fallback-project"
	if err := fallback.MutateWorkspace(t.Context(), workspace, "test.upgrade_fallback", projectID, nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: projectID, Name: "Written before admission", TeamIDs: []string{data.Teams[0].ID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := fallback.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_METADATA_SOURCE_BYTES", "")
	upgraded := open()
	defer upgraded.Close()
	if !upgraded.BoundedMetadataEnabled() {
		t.Fatal("default restart did not admit the prepared workspace")
	}
	data, ok = upgraded.BootstrapForContext(WithColdWork(t.Context(), "upgrade fixture"), workspace)
	if !ok || !slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == projectID }) {
		t.Fatal("bounded restart lost a write committed during legacy fallback")
	}
}

func TestDefaultMetadataUpgradeReplacesSQLiteContentTriggers(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "")
	t.Setenv("FLOW_RECORD_HYDRATION", "")
	t.Setenv("FLOW_RECORD_JOURNAL", "")
	t.Setenv("FLOW_METADATA_CATALOG", "")
	path := filepath.Join(t.TempDir(), "flow.db")
	old, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })
	fixture := old.Bootstrap()
	workspace, issue := fixture.Workspace.URLKey, fixture.Issues[0].ID
	const id = "legacy-comment"
	ctx := t.Context()
	tx, err := old.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writeContentRecord(ctx, tx, workspace, "comment", issue, domain.Comment{ID: id, User: fixture.Viewer, Body: "Existing comment"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.ExecContext(ctx, `UPDATE workspace_content_records SET id=? WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, []byte(id), workspace, issue, id); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`DROP TRIGGER flow_rj_workspace_content_records_delete`,
		`CREATE TRIGGER flow_rj_workspace_content_records_delete AFTER DELETE ON workspace_content_records
			BEGIN SELECT json_array(OLD.resource_id,OLD.id); END`,
	} {
		if _, err := old.db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, sqliteJournalContentKeysVersion); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if !upgraded.BoundedMetadataEnabled() {
		t.Fatal("default startup fell back with a legacy content key")
	}
	if _, err := upgraded.db.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, workspace, issue, []byte(id)); err != nil {
		t.Fatal(err)
	}
	key, err := json.Marshal([]string{issue, id})
	if err != nil {
		t.Fatal(err)
	}
	var deleted int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT deleted FROM record_versions WHERE workspace_key=? AND field='comments' AND record_key=?`, workspace, string(key)).Scan(&deleted); err != nil || deleted != 1 {
		t.Fatalf("removed legacy content was not journaled as deleted: %v", err)
	}
}

func TestDefaultMetadataUpgradeResumesLegacyWrites(t *testing.T) {
	rolloutEnv(t)
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	config := preparationDatabase(t)
	old := openPreparationDatabase(t, config)
	t.Cleanup(func() { _ = old.Close() })
	workspace := rolloutWorkspaces(t, old, 1)[0]
	if err := old.MutateWorkspace(t.Context(), workspace, "test.upgrade_projects", "", nil, func(data *domain.Bootstrap) error {
		for i := range MetadataCatalogBackfillBatch + 1 {
			data.Projects = append(data.Projects, domain.Project{
				ID: fmt.Sprintf("upgrade-project-%03d", i), Name: "Before upgrade", TeamIDs: []string{data.Teams[0].ID},
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Stop preparation after committing one catalog page. The already
	// running old replica still writes without a journal transaction token.
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	preparing := openPreparationDatabase(t, config)
	t.Cleanup(func() { _ = preparing.Close() })
	progress, err := preparing.BackfillMetadataCatalog(t.Context(), workspace, "projects")
	if err != nil || progress.Complete || progress.Processed == 0 {
		t.Fatalf("expected a resumable catalog page: processed=%d, complete=%t, err=%v", progress.Processed, progress.Complete, err)
	}
	if err := preparing.Close(); err != nil {
		t.Fatal(err)
	}
	const updatedID, removedID = "upgrade-project-000", "upgrade-project-001"
	rolloutPutRaw(t, old, workspace, "projects", updatedID, []byte(`{"id":"upgrade-project-000","name":"Written during upgrade"}`))
	if _, err := old.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, removedID); err != nil {
		t.Fatal(err)
	}
	if err := checkRecordJournalCoverage(t.Context(), old.db, workspace); !errors.Is(err, ErrRecordJournalCoverage) {
		t.Fatalf("legacy changes from interrupted preparation must leave a coverage gap: %v", err)
	}

	for _, flag := range []string{"FLOW_BOUNDED_METADATA", "FLOW_RECORD_HYDRATION", "FLOW_RECORD_JOURNAL", "FLOW_METADATA_CATALOG"} {
		t.Setenv(flag, "")
	}
	ceiling, _ := defaultMetadataMemory(1<<30, 16<<30, 0)
	t.Setenv("FLOW_METADATA_MEMORY_BYTES", strconv.FormatInt(ceiling, 10))
	upgraded := openPreparationDatabase(t, config)
	t.Cleanup(func() { _ = upgraded.Close() })
	if !upgraded.BoundedMetadataEnabled() {
		t.Fatal("default startup fell back after an interrupted preparation")
	}
	var report BoundedMetadataPreparation
	if err := upgraded.boundedMetadataReadiness(t.Context(), &report); err != nil || !report.Ready {
		t.Fatalf("incomplete journal or catalog after restart: %v", err)
	}
	var summary []byte
	if err := upgraded.db.QueryRowContext(t.Context(), `SELECT summary FROM metadata_catalog WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, updatedID).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := json.Unmarshal(summary, &project); err != nil || project.Name != "Written during upgrade" {
		t.Fatal("repaired catalog lost the legacy replica's edit")
	}
	var count int
	if err := upgraded.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metadata_catalog WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, removedID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("repaired catalog retained a deleted project: count=%d, err=%v", count, err)
	}

	// During a rolling upgrade an old replica can keep writing after the
	// first bounded replica admits the workspace.
	if err := old.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := old.MutateWorkspace(t.Context(), workspace, "test.upgrade_live_write", updatedID, nil, func(data *domain.Bootstrap) error {
		for i := range data.Projects {
			if data.Projects[i].ID == updatedID {
				data.Projects[i].Name = "Written after admission"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := checkRecordJournalCoverage(t.Context(), upgraded.db, workspace); !errors.Is(err, ErrRecordJournalCoverage) {
		t.Fatalf("expected a journal gap from the running legacy writer: %v", err)
	}
	if err := upgraded.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := checkRecordJournalCoverage(t.Context(), upgraded.db, workspace); err != nil {
		t.Fatalf("reload left a journal coverage gap: %v", err)
	}
	if err := upgraded.db.QueryRowContext(t.Context(), `SELECT summary FROM metadata_catalog WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, updatedID).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	project = domain.Project{}
	if err := json.Unmarshal(summary, &project); err != nil || project.Name != "Written after admission" {
		t.Fatal("repaired catalog lost the live legacy update")
	}
	data, ok := upgraded.BootstrapForContext(WithColdWork(t.Context(), "upgrade fixture"), workspace)
	if !ok || !slices.ContainsFunc(data.Projects, func(p domain.Project) bool {
		return p.ID == updatedID && p.Name == "Written after admission"
	}) {
		t.Fatal("bounded replica did not install the legacy writer's edit")
	}

	newWorkspace := rolloutWorkspaces(t, old, 1)[0]
	if err := upgraded.ReloadAllWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !upgraded.HasWorkspace(newWorkspace) {
		t.Fatal("bounded replica did not install the workspace created by the legacy writer")
	}
	if err := upgraded.boundedMetadataReadiness(t.Context(), &report); err != nil || !report.Ready {
		t.Fatalf("new workspace was installed without complete journal and catalog coverage: %v", err)
	}
}
