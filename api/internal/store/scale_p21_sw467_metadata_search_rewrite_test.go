package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// MySQL 8.4 drops the rows that a multi-row INSERT ... ON DUPLICATE KEY
// UPDATE changes from an InnoDB FULLTEXT index (mysqlFullTextIndexTables).
// Both metadata search index repairs rewrote a page of documents with one
// INSERT ... SELECT ... ON DUPLICATE KEY UPDATE, so of several stale
// documents in a page only the first stayed findable on MySQL. (There the
// repair rewrites every project document: it formats a JSON object field such
// as status differently from the application's text, so before the fix a
// repair left 1 of 12 projects findable.) After either
// repair every record must be found by the full-text query the search uses
// (metadataTermSelection), with stale, missing, orphan and unchanged
// documents in the same page.

// TestScaleP21SW467LegacyMetadataSearchRepairKeepsDocumentsFindable covers
// the flag-off repair (migrateMetadataSearchIndex).
func TestScaleP21SW467LegacyMetadataSearchRepairKeepsDocumentsFindable(t *testing.T) {
	scaleP21SW467MetadataSearchRepair(t, false)
}

// TestScaleP21SW468BoundedMetadataSearchRepairKeepsDocumentsFindable covers
// the bounded repair (sw17MigrateMetadataSearchIndexBounded).
func TestScaleP21SW468BoundedMetadataSearchRepairKeepsDocumentsFindable(t *testing.T) {
	scaleP21SW467MetadataSearchRepair(t, true)
}

func scaleP21SW467MetadataSearchRepair(t *testing.T, bounded bool) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw467-")
	var keys []string
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21_sw467.search", "", nil, func(data *domain.Bootstrap) error {
		for index := range 12 {
			id := fmt.Sprintf("%s-%02d", prefix, index)
			keys = append(keys, id)
			data.Projects = append(data.Projects, domain.Project{ID: id, Name: fmt.Sprintf("Kestrel01 Falcon01 note%04d", index)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	source := f.SQLiteStore
	if bounded {
		view, err := f.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		source = view
	}
	found := func(stage string) {
		t.Helper()
		selection, value := f.metadataTermSelection("Kestrel01")
		rows, err := f.db.QueryContext(t.Context(), selection, workspace, "projects", value)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(id, prefix) {
				ids = append(ids, id)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		slices.Sort(ids)
		if !slices.Equal(ids, keys) {
			t.Fatalf("%s: the search found %d of %d projects: %v", stage, len(ids), len(keys), ids)
		}
	}
	found("written")
	// The documents of four records went stale (a write that missed its
	// document), one is missing and an orphan remains: one repair page
	// rewrites several documents next to unchanged ones.
	stale := []string{keys[1], keys[4], keys[7], keys[10]}
	where, args := bindList("record_key", stale)
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, keys[6]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) VALUES(?,'projects',?,'Kestrel01 orphan')`, workspace, prefix+"-orphan"); err != nil {
		t.Fatal(err)
	}
	for _, completed := range []int{1, 0} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_documents SET content='Kestrel01 stale body' WHERE workspace_key=? AND field='projects' AND `+where, append([]any{workspace}, args...)...); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_migration_checkpoints SET completed=?,last_record_key='' WHERE workspace_key=?`, completed, workspace); err != nil {
			t.Fatal(err)
		}
		// The production repair owns the whole catalog; this fixture limits
		// it to its own workspace on shared databases.
		source.mu.Lock()
		catalog := source.workspaces
		source.workspaces = map[string]domain.Bootstrap{workspace: catalog[workspace]}
		source.mu.Unlock()
		err := source.migrateMetadataSearchIndex(t.Context())
		source.mu.Lock()
		source.workspaces = catalog
		source.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		var stalePending, orphans, documents int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COALESCE(SUM(CASE WHEN content LIKE '%stale%' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN record_key=? THEN 1 ELSE 0 END),0),COUNT(*) FROM metadata_search_documents WHERE workspace_key=? AND field='projects'`, prefix+"-orphan", workspace).Scan(&stalePending, &orphans, &documents); err != nil {
			t.Fatal(err)
		}
		if stalePending != 0 || orphans != 0 || documents != len(keys) {
			t.Fatalf("completed=%d: repair left %d stale, %d orphan, %d documents for %d records", completed, stalePending, orphans, documents, len(keys))
		}
		found(fmt.Sprintf("repaired (completed=%d)", completed))
	}
}
