package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP05BenchmarkStore(b *testing.B, bodyScale int) *SQLiteStore {
	b.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(b.TempDir(), "catalog.db")))
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	b.Cleanup(func() { _ = db.Close() })
	s := &SQLiteStore{db: &sqlDatabase{DB: db, dialect: "sqlite"}, dialect: "sqlite"}
	ctx := context.Background()
	if err := s.ensureWorkspaceMetadataRecords(ctx); err != nil {
		b.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 205 {
		project := domain.Project{ID: fmt.Sprintf("p-%04d", i), Name: fmt.Sprintf("Project %04d", i), SlugID: fmt.Sprintf("project-%04d", i), Summary: "Directory summary", Description: strings.Repeat("x", bodyScale*24*1024), TeamIDs: []string{"team"}}
		raw, err := json.Marshal(project)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES('bench','projects',?,?,?)`, project.ID, i, raw); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return s
}

func BenchmarkScaleP05CanonicalDirectory(b *testing.B) {
	for _, scale := range []int{1, 10} {
		b.Run(fmt.Sprintf("body%d", scale), func(b *testing.B) {
			s := scaleP05BenchmarkStore(b, scale)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				page, err := s.QueryProjectDirectory(context.Background(), ProjectRecordQuery{Workspace: "bench", Limit: 100, IncludeTotal: true, Admin: true})
				if err != nil || len(page.Items) != 100 || page.Total != 205 {
					b.Fatalf("directory: items=%d total=%d err=%v", len(page.Items), page.Total, err)
				}
			}
		})
	}
}

func BenchmarkScaleP05CatalogDirectory(b *testing.B) {
	for _, scale := range []int{1, 10} {
		b.Run(fmt.Sprintf("body%d", scale), func(b *testing.B) {
			s := scaleP05BenchmarkStore(b, scale)
			ctx := context.Background()
			if _, err := s.db.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,applied_at TEXT NOT NULL)`); err != nil {
				b.Fatal(err)
			}
			if err := s.ensureMetadataCatalog(ctx); err != nil {
				b.Fatal(err)
			}
			for {
				progress, err := s.BackfillMetadataCatalog(ctx, "bench", "projects")
				if err != nil {
					b.Fatal(err)
				}
				if progress.Complete {
					break
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				page, err := s.ReadMetadataCatalogPage(ctx, "bench", PageRequest{Kind: "projects", Limit: 100})
				if err != nil || len(page.Items) != 100 || !page.HasMore {
					b.Fatalf("catalog: items=%d err=%v", len(page.Items), err)
				}
			}
			b.StopTimer()
			page, err := s.ReadMetadataCatalogPage(ctx, "bench", PageRequest{Kind: "projects", Limit: 100})
			if err != nil {
				b.Fatal(err)
			}
			raw, err := json.Marshal(page)
			if err != nil || len(raw) > MetadataRecordPageMaxBytes {
				b.Fatalf("encoded page bytes=%d err=%v", len(raw), err)
			}
			b.ReportMetric(float64(len(raw)), "page-B")
		})
	}
}
