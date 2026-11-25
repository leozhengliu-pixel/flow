package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP05CanonicalBatchesAndLargeRecords(t *testing.T) {
	s, workspace := scaleP05Database(t)
	large := strings.Repeat("body ", 1<<20)
	document := domain.Document{ID: "document", Title: "Title", Content: large, ContentState: large, Revisions: []domain.DocumentRevision{{ID: "old", Content: large}}, Permissions: []domain.DocumentPermission{{ID: "grant", SubjectType: "user", SubjectID: "guest", Role: "viewer"}}}
	raw := scaleP05Put(t, s, workspace, "documents", document.ID, 0, document)
	small := scaleP05Put(t, s, workspace, "documents", "small", 1, domain.Document{ID: "small", Title: "Small", Content: "Exact full content", TeamIDs: []string{}})
	trash := domain.TrashEntry{ID: "trash", ResourceID: "document", ResourceType: "document", Title: "Deleted", Payload: json.RawMessage(raw)}
	scaleP05Put(t, s, workspace, "trash", "trash", 0, trash)
	batch, err := s.ReadCanonicalRecords(t.Context(), workspace, []RecordKey{{"documents", "document"}, {"documents", "small"}, {"documents", "missing"}}, "detail")
	if err != nil || len(batch.Items) != 3 {
		t.Fatalf("batch: %v", err)
	}
	if batch.Items[0].Version != metadataFingerprint(raw) || !batch.Items[0].Omitted || batch.Items[0].Detail == nil || strings.Contains(string(batch.Items[0].Data), "body ") {
		t.Fatal("large body lost its fingerprint or was materialized in the response")
	}
	if !reflect.DeepEqual([]byte(batch.Items[1].Data), small) || batch.Items[1].Omitted || !batch.Items[2].Missing || batch.Items[2].Version != "" {
		t.Fatal("canonical bytes or missing-record semantics changed")
	}
	encoded, _ := json.Marshal(batch)
	if len(encoded) > MetadataRecordPageMaxBytes {
		t.Fatalf("batch bytes=%d", len(encoded))
	}
	for _, kind := range []string{"documents", "trash"} {
		if kind == "documents" {
			var second *SQLiteStore
			if s.dialect == "sqlite" {
				var sequence int
				var name, path string
				if err := s.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
					t.Fatal(err)
				}
				second = scaleP05Connect(t, "sqlite", sqliteDSN(path))
			} else {
				second = scaleP05Connect(t, s.dialect, os.Getenv("FLOW_TEST_DATABASE_URL"))
			}
			defer second.Close()
			done := make(chan error, 1)
			go func() {
				_, err := s.BackfillMetadataCatalog(t.Context(), workspace, kind)
				done <- err
			}()
			deadline := time.Now().Add(2 * time.Second)
			for s.db.Stats().InUse == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			time.Sleep(20 * time.Millisecond)
			writeCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			_, writeErr := second.db.ExecContext(writeCtx, `UPDATE workspace_metadata_records SET collection_order=collection_order+1 WHERE workspace_key=? AND field='documents' AND record_key='small'`, workspace)
			cancel()
			backfillErr := <-done
			if writeErr != nil || backfillErr != nil {
				t.Fatalf("large-body shadow preparation blocked an old writer: write=%v backfill=%v", writeErr, backfillErr)
			}
		}
		scaleP05Finish(t, s, workspace, kind)
		page, err := s.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: kind})
		if err != nil || len(page.Items) == 0 || strings.Contains(string(page.Items[0].Data), "body ") {
			t.Fatalf("%s body survived summary: %v", kind, err)
		}
	}
	other, err := s.ReadCanonicalRecords(t.Context(), workspace+"-other", []RecordKey{{"documents", "small"}}, "detail")
	if err != nil || !other.Items[0].Missing {
		t.Fatalf("cross-workspace record: %+v %v", other, err)
	}
	if _, err := s.ReadCanonicalRecords(t.Context(), workspace, make([]RecordKey, 51), "detail"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatalf("unbounded batch accepted: %v", err)
	}
	if _, err := s.ReadCanonicalRecords(t.Context(), workspace, []RecordKey{{"documents", "small"}}, "unknown"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatalf("unknown projection accepted: %v", err)
	}
}

func TestScaleP05BatchReservesEveryKeyAndOwnsItsResults(t *testing.T) {
	s, workspace := scaleP05Database(t)
	keys := make([]RecordKey, 50)
	for i := range keys {
		keys[i] = RecordKey{Kind: "documents", ID: fmt.Sprintf("d-%02d", i)}
		scaleP05Put(t, s, workspace, "documents", keys[i].ID, i, domain.Document{ID: keys[i].ID, Content: strings.Repeat("x", 235<<10)})
	}
	batch, err := s.ReadCanonicalRecords(t.Context(), workspace, keys, "detail")
	if err != nil || len(batch.Items) != len(keys) {
		t.Fatalf("remaining keys were not reserved: len=%d err=%v", len(batch.Items), err)
	}
	raw, _ := json.Marshal(batch)
	if len(raw) > MetadataRecordPageMaxBytes {
		t.Fatalf("batch bytes=%d", len(raw))
	}
	batch.Items[0].Data[0] = '!'
	again, err := s.ReadCanonicalRecords(t.Context(), workspace, keys[:1], "detail")
	if err != nil || len(again.Items[0].Data) == 0 || again.Items[0].Data[0] != '{' {
		t.Fatalf("caller mutated canonical data: %v", err)
	}
}

func TestScaleP05PagesRespectEncodedBudgetAndCanonicalShapes(t *testing.T) {
	s, workspace := scaleP05Database(t)
	for i := range 80 {
		// Escaping expands the encoded JSON; budget the actual wire shape.
		scaleP05Put(t, s, workspace, "documents", fmt.Sprintf("d-%03d", i), i/2, domain.Document{ID: fmt.Sprintf("d-%03d", i), Title: strings.Repeat("<>&", 500), Content: strings.Repeat("detail", 10000), ProjectIDs: []string{}})
	}
	scaleP05Finish(t, s, workspace, "documents")
	for _, canonical := range []bool{false, true} {
		for _, budget := range []int{16 << 10, MetadataRecordPageMaxBytes} {
			seen := map[string]bool{}
			q := PageRequest{Kind: "documents", Limit: 100, MaxBytes: budget}
			for range 100 {
				var page RecordPage
				var err error
				if canonical {
					page, err = s.ReadCanonicalRecordPage(t.Context(), workspace, q)
				} else {
					page, err = s.ReadMetadataCatalogPage(t.Context(), workspace, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(page)
				if len(raw) > budget {
					t.Fatalf("canonical=%t encoded page=%d budget=%d", canonical, len(raw), budget)
				}
				for _, item := range page.Items {
					if seen[item.Key.ID] {
						t.Fatalf("duplicate %s", item.Key.ID)
					}
					seen[item.Key.ID] = true
					if !canonical && !item.Omitted {
						var doc domain.Document
						if err := json.Unmarshal(item.Data, &doc); err != nil || doc.TeamIDs != nil || doc.ProjectIDs == nil || doc.Content != "" {
							t.Fatalf("null/empty/body: %v", err)
						}
					}
				}
				if !page.HasMore {
					break
				}
				if page.Next == "" || len(page.Items) == 0 {
					t.Fatal("empty cursor progress")
				}
				q.Cursor = page.Next
			}
			if len(seen) != 80 {
				t.Fatalf("canonical=%t count=%d", canonical, len(seen))
			}
		}
	}
}

func TestScaleP05OversizedSummaryAndEmptyMapKey(t *testing.T) {
	s, workspace := scaleP05Database(t)
	scaleP05Put(t, s, workspace, "documents", "huge", 0, domain.Document{ID: "huge", Title: strings.Repeat("title", 1<<20)})
	scaleP05Put(t, s, workspace, "teamSettings", "", 0, map[string]any{"access": "private"})
	scaleP05Put(t, s, workspace, "teamSettings", "next", 0, map[string]any{"access": "workspace"})
	for _, kind := range []string{"documents", "teamSettings"} {
		scaleP05Finish(t, s, workspace, kind)
	}
	page, err := s.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: "documents"})
	if err != nil || len(page.Items) != 1 || !page.Items[0].Omitted || page.Items[0].Detail == nil || len(page.Items[0].Data) > 1024 {
		t.Fatalf("oversized summary: %+v %v", page, err)
	}
	page, err = s.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: "teamSettings", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Key.ID != "" || !page.HasMore {
		t.Fatalf("empty dictionary key skipped: %+v %v", page, err)
	}
	page, err = s.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: "teamSettings", Limit: 1, Cursor: page.Next})
	if err != nil || len(page.Items) != 1 || page.Items[0].Key.ID != "next" || page.HasMore {
		t.Fatalf("empty-key cursor: %+v %v", page, err)
	}
	archived := time.Now().UTC()
	scaleP05Put(t, s, workspace, "projects", "huge", 0, domain.Project{ID: "huge", Name: strings.Repeat("huge", 20000), TeamIDs: []string{"private"}, ArchivedAt: &archived})
	scaleP05Finish(t, s, workspace, "projects")
	if _, err := s.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: "projects", Filter: `{"Archived":"true","TeamIDs":["private"]}`}); !errors.Is(err, ErrMetadataCatalogGap) {
		t.Fatalf("omitted filter fields silently excluded a canonical record: %v", err)
	}
}

func TestScaleP05CatalogCursorBindingAndRevision(t *testing.T) {
	s, workspace := scaleP05Database(t)
	scaleP05Projects(t, s, workspace, 4)
	scaleP05Finish(t, s, workspace, "projects")
	q := PageRequest{Kind: "projects", Filter: `{"Archived":"all"}`, Limit: 1}
	page, err := s.ReadMetadataCatalogPage(t.Context(), workspace, q)
	if err != nil || !page.HasMore {
		t.Fatalf("first page: %v", err)
	}
	for _, change := range []PageRequest{
		{Kind: "projects", Cursor: page.Next, Order: "createdAt", Filter: q.Filter},
		{Kind: "projects", Cursor: page.Next, Filter: `{"Archived":"true"}`},
		{Kind: "projects", Cursor: "broken"},
	} {
		if _, err := s.ReadMetadataCatalogPage(t.Context(), workspace, change); !errors.Is(err, ErrMetadataCursor) {
			t.Fatalf("rebound cursor accepted: %+v %v", change, err)
		}
	}
	scaleP05Put(t, s, workspace, "projects", "p-000", 0, domain.Project{ID: "p-000", Name: "Changed"})
	if err := s.ResetMetadataCatalogBackfill(t.Context(), workspace, "projects"); err != nil {
		t.Fatal(err)
	}
	scaleP05Finish(t, s, workspace, "projects")
	q.Cursor = page.Next
	if _, err := s.ReadMetadataCatalogPage(t.Context(), workspace, q); !errors.Is(err, ErrMetadataCursor) {
		t.Fatalf("stale revision cursor accepted: %v", err)
	}
	if _, err := s.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: "projects", MaxBytes: 1024}); !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatalf("impossible page budget: %v", err)
	}
}

func TestScaleP05CatalogReadsFetchNoCanonicalBodies(t *testing.T) {
	s, workspace := scaleP05Database(t)
	if s.dialect != "sqlite" {
		t.Skip("table-isolation proof uses a private SQLite database; external plans checked separately")
	}
	scaleP05Projects(t, s, workspace, 10)
	scaleP05Finish(t, s, workspace, "projects")
	if _, err := s.db.ExecContext(t.Context(), `ALTER TABLE workspace_metadata_records RENAME TO scale_p05_canonical_held`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := s.db.ExecContext(context.Background(), `ALTER TABLE scale_p05_canonical_held RENAME TO workspace_metadata_records`); err != nil {
			t.Error(err)
		}
	}()
	page, err := s.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: "projects"})
	if err != nil || len(page.Items) != 8 {
		t.Fatalf("catalog consulted canonical storage: items=%d err=%v", len(page.Items), err)
	}
}

func TestScaleP05ShadowFlagWorkerStopAndRestart(t *testing.T) {
	for _, mode := range []string{"", "shadow", "off", "on", "invalid"} {
		t.Setenv("FLOW_METADATA_CATALOG", mode)
		if got := metadataCatalogShadowEnabled(); got != (mode == "shadow") {
			t.Fatalf("flag %q enabled=%t", mode, got)
		}
	}
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	path := filepath.Join(t.TempDir(), "restart.db")
	s, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(t.Context(), "Shadow", "shadow", "us"); err != nil {
		t.Fatal(err)
	}
	scaleP05Put(t, s, "shadow", "projects", "project", 0, domain.Project{ID: "project", Name: "Canonical"})
	status, err := s.MetadataCatalogStatus(t.Context(), "shadow", "projects")
	if err != nil || status.Complete || status.Ready {
		t.Fatalf("off flag built catalog: %+v %v", status, err)
	}
	scaleP05Finish(t, s, "shadow", "projects")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err = s.MetadataCatalogStatus(t.Context(), "shadow", "projects")
	if err != nil || !status.Complete || status.Ready {
		t.Fatalf("restart dropped sidecar: %+v %v", status, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.RunMetadataCatalogBackfill(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var phase int
		if err := s.db.QueryRowContext(t.Context(), `SELECT phase FROM metadata_catalog_collections WHERE workspace_key='shadow' AND field='projects'`).Scan(&phase); err != nil {
			t.Fatal(err)
		}
		if phase != 2 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("worker never resumed the shadow scan")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	canonical, err := s.ReadCanonicalRecords(t.Context(), "shadow", []RecordKey{{"projects", "project"}}, "detail")
	if err != nil || len(canonical.Items) != 1 || canonical.Items[0].Missing {
		t.Fatalf("stopping worker changed canonical reads: %v", err)
	}
}

func TestScaleP05WorkspaceRenamePurgeAndOldReplicaDeletion(t *testing.T) {
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	s, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := s.Bootstrap()
	key := data.Workspace.URLKey
	content := `{"workspaceKey":"` + key + `"}`
	if err := s.MutateWorkspace(WithoutIssueRecordMutations(t.Context()), key, "project.created", "catalog-project", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "catalog-project", Name: "Project", Summary: content, TeamIDs: []string{"team"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scaleP05Finish(t, s, key, "projects")
	workspace := data.Workspace
	workspace.URLKey = "scale-p05-renamed"
	if _, err := s.UpdateWorkspace(t.Context(), key, workspace); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := s.db.QueryRowContext(t.Context(), `SELECT summary FROM metadata_catalog WHERE workspace_key=? AND field='projects' AND record_key='catalog-project'`, workspace.URLKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := json.Unmarshal(raw, &project); err != nil || project.Summary != content {
		t.Fatalf("workspace rename rewrote user content: %q %v", project.Summary, err)
	}
	if err := s.DeleteWorkspace(t.Context(), workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"metadata_catalog", "metadata_catalog_relations", "metadata_catalog_collections"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key IN (?,?)`, key, workspace.URLKey).Scan(&count); err != nil || count != 0 {
			t.Fatalf("workspace purge left %s rows=%d err=%v", table, count, err)
		}
	}
	if _, err := s.CreateWorkspace(t.Context(), "Old deletion", "scale-p05-old-delete", "us"); err != nil {
		t.Fatal(err)
	}
	key = "scale-p05-old-delete"
	scaleP05Put(t, s, key, "projects", "stale", 0, domain.Project{ID: "stale", Name: "Protected name"})
	scaleP05Finish(t, s, key, "projects")
	// An old binary removes the authoritative workspace catalog before it
	// purges the canonical rows, without knowing the new sidecar tables.
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM workspace_states WHERE workspace_key=?`, key); err != nil {
		t.Fatal(err)
	}
	scaleP05Finish(t, s, key, "projects")
	for _, table := range []string{"metadata_catalog", "metadata_catalog_relations", "metadata_catalog_collections"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=?`, key).Scan(&count); err != nil || count != 0 {
			t.Fatalf("old replica deletion left %s rows=%d err=%v", table, count, err)
		}
	}
}
