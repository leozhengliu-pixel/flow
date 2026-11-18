package store

import (
	"context"
	"fmt"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// MySQL renders a JSON object, array or number field (a project's status, a
// customer's domains) with its own spacing and key order, while the
// application writes Go's compact JSON. The metadata search repair, which
// runs on every start, compared the two, so it rewrote every such document on
// every start. It now writes the application's text byte for byte: after a
// start repairs a stale and a missing document, the next start (flag off and
// bounded) rewrites none.
func TestScaleP21SW470MetadataSearchRepairKeepsApplicationText(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	if f.dialect != "mysql" {
		t.Skip("SQLite and PostgreSQL repairs keep their database text")
	}
	ctx, workspace, prefix := t.Context(), f.data.Workspace.URLKey, idgen.New("sw470-")
	if err := f.MutateWorkspace(WithMetadataFields(ctx, "projects", "customers"), workspace, "scale_p21_sw470.search", "", nil, func(data *domain.Bootstrap) error {
		for i := range 6 {
			data.Projects = append(data.Projects, domain.Project{
				ID: fmt.Sprintf("%s-p%02d", prefix, i), Name: fmt.Sprintf("Kestrel03 plan%02d", i), Description: "Body \"quoted\" <b>&</b> 界",
				Status: domain.ProjectStatus{ID: fmt.Sprintf("status-%d", i%2), Name: "R&D <build> 界", Color: "#5e6ad2", Type: "started", Position: float64(i) + 0.5},
			})
			data.Customers = append(data.Customers, domain.Customer{
				ID: fmt.Sprintf("%s-c%02d", prefix, i), Name: fmt.Sprintf("Kestrel03 customer%02d", i), Status: "null",
				Domains: []string{fmt.Sprintf("c%d.example.test", i), "shared.example.test"},
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Count the documents a repair writes, through triggers of this test.
	table := "sw470_metadata_search_writes"
	cleanup := []string{`DROP TRIGGER IF EXISTS sw470_search_insert`, `DROP TRIGGER IF EXISTS sw470_search_update`, `DROP TABLE IF EXISTS ` + table}
	for _, statement := range append(append([]string{}, cleanup...),
		`CREATE TABLE `+table+`(record_key VARCHAR(191) NOT NULL)`,
		`CREATE TRIGGER sw470_search_insert AFTER INSERT ON metadata_search_documents FOR EACH ROW INSERT INTO `+table+`(record_key) SELECT NEW.record_key FROM DUAL WHERE NEW.workspace_key='`+workspace+`'`,
		`CREATE TRIGGER sw470_search_update AFTER UPDATE ON metadata_search_documents FOR EACH ROW INSERT INTO `+table+`(record_key) SELECT NEW.record_key FROM DUAL WHERE NEW.workspace_key='`+workspace+`' AND CAST(NEW.content AS BINARY)<>CAST(OLD.content AS BINARY)`) {
		if _, err := f.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	t.Cleanup(func() {
		for _, statement := range cleanup {
			if _, err := f.db.ExecContext(context.Background(), statement); err != nil {
				t.Error(statement, err)
			}
		}
	})
	written := func() int {
		t.Helper()
		var count int
		if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			t.Fatal(err)
		}
		return count
	}
	// Every document holds the application's text and is findable.
	check := func(stage string) {
		t.Helper()
		rows, err := f.db.QueryContext(ctx, `SELECT m.field,m.record_key,m.data,COALESCE(d.content,'<missing>') FROM workspace_metadata_records m LEFT JOIN metadata_search_documents d ON d.workspace_key=m.workspace_key AND d.field=m.field AND d.record_key=m.record_key WHERE m.workspace_key=? AND m.field IN ('projects','customers') AND m.record_key LIKE ?`, workspace, prefix+"%")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		documents := 0
		for rows.Next() {
			var field, key, content string
			var raw []byte
			if err := rows.Scan(&field, &key, &raw, &content); err != nil {
				t.Fatal(err)
			}
			if want := capSearchText(metadataSearchText(raw), f.db.searchMaxChars); content != want {
				t.Fatalf("%s: %s %s holds %q, the application writes %q", stage, field, key, content, want)
			}
			documents++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if documents != 12 {
			t.Fatalf("%s: %d of 12 records", stage, documents)
		}
		for _, field := range []string{"projects", "customers"} {
			selection, value := f.metadataTermSelection("Kestrel03")
			var found int
			if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+selection+`) hits WHERE record_key LIKE ?`, workspace, field, value, prefix+"%").Scan(&found); err != nil {
				t.Fatal(err)
			}
			if found != 6 {
				t.Fatalf("%s: the search found %d of 6 %s", stage, found, field)
			}
		}
	}
	check("written")
	bounded, err := f.BoundedMetadataViewForTesting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, bounded} {
		repair := func() {
			t.Helper()
			// The production repair owns the whole catalog; this fixture
			// limits it to its own workspace on shared databases.
			source.mu.Lock()
			catalog := source.workspaces
			source.workspaces = map[string]domain.Bootstrap{workspace: catalog[workspace]}
			source.mu.Unlock()
			err := source.migrateMetadataSearchIndex(ctx)
			source.mu.Lock()
			source.workspaces = catalog
			source.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.db.ExecContext(ctx, `UPDATE metadata_search_documents SET content='Kestrel03 stale' WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, prefix+"-p02"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(ctx, `DELETE FROM metadata_search_documents WHERE workspace_key=? AND field='customers' AND record_key=?`, workspace, prefix+"-c03"); err != nil {
			t.Fatal(err)
		}
		written()
		repair()
		if count := written(); count != 2 {
			t.Fatalf("bounded=%v: the first start wrote %d documents, want the stale and the missing one", source != f.SQLiteStore, count)
		}
		check("first start")
		repair()
		if count := written(); count != 0 {
			t.Fatalf("bounded=%v: the second start rewrote %d unchanged documents", source != f.SQLiteStore, count)
		}
		check("second start")
	}
}
