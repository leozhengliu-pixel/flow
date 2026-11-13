package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW17NativeSearchRepairDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw17-search-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21_sw17.search", "", nil, func(data *domain.Bootstrap) error {
		for index := range 270 {
			item := domain.Project{ID: fmt.Sprintf("%s-%03d", prefix, index), Name: "Searchable title"}
			if index == 0 {
				item.Description = strings.Repeat("complete large indexed body ", 18000) + "NeedleAtEnd"
			}
			data.Projects = append(data.Projects, item)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bounded, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	hash := func() [32]byte {
		t.Helper()
		digest := sha256.New()
		if err := f.StreamMetadataRecords(t.Context(), workspace, "projects", func(key RecordKey, reader io.Reader) error {
			_, _ = io.WriteString(digest, key.ID)
			_, err := io.Copy(digest, reader)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return [32]byte(digest.Sum(nil))
	}
	before := hash()
	for _, source := range []*SQLiteStore{f.SQLiteStore, bounded} {
		repair := func() {
			t.Helper()
			// The production migration owns the whole catalog. This isolated
			// fixture limits its catalog to its own workspace on shared databases.
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
		}
		for _, completed := range []int{0, 1} {
			if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_documents SET content='stale body' WHERE workspace_key=? AND field='projects'`, workspace); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) VALUES(?,'projects',?,'orphan') ON CONFLICT DO NOTHING`, workspace, prefix+"-orphan"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_migration_checkpoints SET completed=?,last_record_key='' WHERE workspace_key=?`, completed, workspace); err != nil {
				t.Fatal(err)
			}
			repair()
			var mismatches, total, retainedNeedle int
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records r LEFT JOIN metadata_search_documents d
				ON d.workspace_key=r.workspace_key AND d.field=r.field AND d.record_key=r.record_key
				WHERE r.workspace_key=? AND r.field='projects' AND (d.record_key IS NULL OR d.content<>`+source.metadataSearchContent("r.data")+`)`, workspace).Scan(&mismatches); err != nil || mismatches != 0 {
				t.Fatalf("search repair mismatches=%d err=%v", mismatches, err)
			}
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metadata_search_documents WHERE workspace_key=? AND field='projects'`, workspace).Scan(&total); err != nil || total != 270 {
				t.Fatalf("search repair rows=%d err=%v", total, err)
			}
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND content LIKE '%NeedleAtEnd%'`, workspace).Scan(&retainedNeedle); err != nil || retainedNeedle != 1 {
				t.Fatal("repair omitted the end of a legal large body", err)
			}
			if hash() != before {
				t.Fatal("search repair rewrote canonical metadata")
			}
		}
		checkpoint := prefix + "-127"
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key>?`, workspace, checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_migration_checkpoints SET completed=0,last_record_key=? WHERE workspace_key=? AND field='projects'`, checkpoint, workspace); err != nil {
			t.Fatal(err)
		}
		repair()
		var total int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metadata_search_documents WHERE workspace_key=? AND field='projects'`, workspace).Scan(&total); err != nil || total != 270 || hash() != before {
			t.Fatalf("nonzero repair checkpoint did not resume the tail: rows=%d err=%v", total, err)
		}
	}
	if bounded.WorkspaceMetadataDecodes() != 0 || bounded.metadataResidency.work.used != 0 {
		t.Fatal("native SQL search repair decoded complete metadata or leaked admission")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := bounded.migrateMetadataSearchIndex(cancelled); !errors.Is(err, context.Canceled) ||
		bounded.metadataResidency.work.used != 0 || hash() != before {
		t.Fatal("cancelled native repair changed canonical metadata or leaked admission", err)
	}
}
