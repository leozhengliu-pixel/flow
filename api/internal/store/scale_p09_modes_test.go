package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestScaleP09UnreadyJournalPreservesLegacyRecords(t *testing.T) {
	for _, schema := range []string{"absent", "disabled"} {
		t.Run(schema, func(t *testing.T) {
			var legacyJSON []byte
			var legacySQL []string
			for _, mode := range []string{"off", "", "shadow", "on"} {
				t.Run(fmt.Sprintf("mode-%q", mode), func(t *testing.T) {
					t.Setenv("FLOW_RECORD_JOURNAL", "off")
					var s *SQLiteStore
					var workspace string
					if schema == "absent" {
						s, workspace = scaleP05Database(t)
					} else {
						f := scaleP09Database(t)
						s, workspace = f.SQLiteStore, f.data.Workspace.URLKey
					}
					t.Setenv("FLOW_RECORD_JOURNAL", mode)
					if s.db.journalReady.Load() {
						t.Fatal("fixture unexpectedly has a ready journal")
					}
					var statements []string
					ctx := WithSQLStatementObserver(t.Context(), func(query string) { statements = append(statements, query) })
					key := RecordKey{"projects", "unready-project"}
					raw := []byte(`{"id":"unready-project","name":"Legacy project","description":"Canonical body"}`)
					if _, err := s.db.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?)`, workspace, key.Kind, key.ID, raw); err != nil {
						t.Fatal(err)
					}
					for pass := 0; ; pass++ {
						progress, err := s.BackfillMetadataCatalog(ctx, workspace, key.Kind)
						if err != nil || pass >= 5 {
							t.Fatalf("catalog backfill: pass=%d err=%v", pass, err)
						}
						if progress.Complete {
							break
						}
					}
					detail, err := s.ReadCanonicalRecords(ctx, workspace, []RecordKey{key, {Kind: key.Kind, ID: "missing"}}, "detail")
					if err != nil || len(detail.Items) != 2 || detail.Items[0].Version != metadataFingerprint(raw) || !bytes.Equal(detail.Items[0].Data, raw) || !detail.Items[1].Missing {
						t.Fatalf("canonical detail: %+v err=%v", detail, err)
					}
					summary, err := s.ReadCanonicalRecords(ctx, workspace, []RecordKey{key}, "summary")
					if err != nil {
						t.Fatal(err)
					}
					canonical, err := s.ReadCanonicalRecordPage(ctx, workspace, PageRequest{Kind: key.Kind})
					if err != nil {
						t.Fatal(err)
					}
					catalog, err := s.ReadMetadataCatalogPage(ctx, workspace, PageRequest{Kind: key.Kind})
					if err != nil || len(catalog.Items) != 1 || catalog.Items[0].Version != metadataFingerprint(raw) {
						t.Fatalf("catalog page: %+v err=%v", catalog, err)
					}
					version, err := s.RealtimeMetadataVersion(ctx, workspace, key)
					if err != nil || version != metadataFingerprint(raw) {
						t.Fatalf("realtime version: %s err=%v", version, err)
					}
					encoded, err := json.Marshal([]any{detail, summary, canonical, catalog, version})
					if err != nil {
						t.Fatal(err)
					}
					if mode == "off" {
						legacyJSON, legacySQL = encoded, statements
					} else if !bytes.Equal(encoded, legacyJSON) || !slices.Equal(statements, legacySQL) {
						t.Fatalf("unready journal changed legacy JSON or SQL in mode %q", mode)
					}
				})
			}
		})
	}
}

func TestScaleP09CatalogExplainIndexes(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	t.Cleanup(func() {
		for _, table := range []string{"metadata_catalog_relations", "metadata_catalog"} {
			if _, err := f.db.ExecContext(context.Background(), `DELETE FROM `+table+` WHERE workspace_key=?`, f.data.Workspace.URLKey); err != nil {
				t.Error(err)
			}
		}
	})
	scaleP05CatalogExplainIndexes(t, f.SQLiteStore, f.data.Workspace.URLKey, true)
}

func TestScaleP09CatalogCursorMatchesLegacyOrder(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		id := fmt.Sprintf("cursor-project-%d", i)
		raw, err := json.Marshal(map[string]any{"id": id, "name": id, "createdAt": base.Add(time.Duration(i/2) * time.Second), "updatedAt": base.Add(time.Duration((6-i)/2) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"projects", id}, metadataRecord{data: raw, order: i / 2}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	scaleP05Finish(t, f.SQLiteStore, workspace, "projects")
	for _, order := range []string{"collection", "createdAt", "updatedAt"} {
		t.Run(order, func(t *testing.T) {
			var legacy []string
			for _, mode := range []string{"shadow", "on"} {
				t.Run(mode, func(t *testing.T) {
					t.Setenv("FLOW_RECORD_JOURNAL", mode)
					q := PageRequest{Kind: "projects", Order: order, Limit: 2}
					var ids []string
					for pass := 0; ; pass++ {
						page, err := f.ReadMetadataCatalogPage(t.Context(), workspace, q)
						if err != nil || pass >= 5 {
							t.Fatalf("catalog cursor: pass=%d err=%v", pass, err)
						}
						for _, item := range page.Items {
							ids = append(ids, item.Key.ID)
						}
						if !page.HasMore {
							break
						}
						q.Cursor = page.Next
					}
					if len(ids) != 7 {
						t.Fatalf("catalog cursor lost records: %v", ids)
					}
					if mode == "shadow" {
						legacy = ids
					} else if !slices.Equal(ids, legacy) {
						t.Fatalf("journal cursor changed legacy order: got=%v want=%v", ids, legacy)
					}
				})
			}
		})
	}
}
