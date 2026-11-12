package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// This opt-in integration measurement keeps seeding outside the measured open.
// 1x is 2,000 projects, 500 documents and 500 trash records; 10x grows counts,
// project memberships, subscriptions and history together, with unchanged body sizes.
func TestScaleP21ColdStartMeasurement(t *testing.T) {
	path := os.Getenv("FLOW_P21_COLD_DATABASE")
	if path == "" {
		t.Skip("set FLOW_P21_COLD_DATABASE for the cold-start measurement")
	}
	config := DatabaseConfig{Driver: "sqlite", Path: filepath.Clean(path)}
	if os.Getenv("FLOW_P21_COLD_SEED") == "1" {
		repo, err := OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		defer repo.Close()
		user, token, err := repo.Register(t.Context(), "P21 measurement", fmt.Sprintf("p21-%d@example.test", time.Now().UnixNano()), "p21-measurement-password")
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		user.EmailVerified = true
		data, err := repo.CreateWorkspace(ContextWithActor(t.Context(), user), "P21 measurement", fmt.Sprintf("p21-%d", time.Now().UnixNano()), "us")
		if err != nil {
			t.Fatal(err)
		}
		workspace := data.Workspace.URLKey
		// Materialize the collection headers through the canonical writer.
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects, data.Documents, data.Trash = []domain.Project{}, []domain.Document{}, []domain.TrashEntry{}
			data.Subscriptions = []domain.Subscription{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		body := strings.Repeat("p21 body ", 900)
		now := time.Now().UTC()
		for first := 0; first < 20000; first += 128 {
			tx, err := repo.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			for i := first; i < min(first+128, 20000); i++ {
				id := fmt.Sprintf("p21-%06d", i)
				project := domain.Project{ID: id, Name: id, Description: body, TeamIDs: []string{data.Teams[i%len(data.Teams)].ID}, MemberIDs: []string{user.ID}, CreatedAt: now, UpdatedAt: now,
					DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: id + "-revision", ProjectID: id, Description: body, Author: user, CreatedAt: now}},
					Comments:             []domain.Comment{{ID: id + "-comment", Body: "project discussion", User: user, CreatedAt: now}}}
				scaleP21SeedRow(t, tx, workspace, "projects", id, i, project)
				scaleP21SeedRow(t, tx, workspace, "subscriptions", id, i, domain.Subscription{ID: id, UserID: user.ID, ResourceType: "project", ResourceID: id, Events: []string{"all"}, CreatedAt: now})
				if i < 5000 {
					scaleP21SeedRow(t, tx, workspace, "documents", id, i, domain.Document{ID: id, Title: id, Content: body, Creator: user, TeamIDs: project.TeamIDs, CreatedAt: now, UpdatedAt: now})
					payload, _ := json.Marshal(project)
					scaleP21SeedRow(t, tx, workspace, "trash", id, i, domain.TrashEntry{ID: id, ResourceType: "project", ResourceID: id, Payload: payload, DeletedBy: user, DeletedAt: now, ExpiresAt: now.AddDate(1, 0, 0)})
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("seeded 10x cold-start fixture: %s", path)
		return
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	began := time.Now()
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(began)
	runtime.GC()
	runtime.ReadMemStats(&after)
	measurement := map[string]any{"elapsed_ms": elapsed.Milliseconds(), "allocated_bytes": after.TotalAlloc - before.TotalAlloc, "retained_heap_bytes": after.HeapAlloc - min(after.HeapAlloc, before.HeapAlloc), "heap_bytes": after.HeapAlloc, "full_metadata_decodes": repo.WorkspaceMetadataDecodes(), "workspaces": len(repo.WorkspaceKeys())}
	encoded, _ := json.Marshal(measurement)
	t.Logf("P21_COLD_START %s", encoded)
	runtime.KeepAlive(repo)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
}

// This measures the loader only. It deliberately does not bypass the
// OpenDatabase activation gate or certify the complete application startup.
func TestScaleP21RecoveryPrimitiveMeasurement(t *testing.T) {
	path := os.Getenv("FLOW_P21_RECOVERY_DATABASE")
	if path == "" {
		t.Skip("set FLOW_P21_RECOVERY_DATABASE for the loader measurement")
	}
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Clean(path)))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	repo := &SQLiteStore{db: &sqlDatabase{DB: db, dialect: "sqlite"}, dialect: "sqlite", maxStateBytes: 64 << 20, workspaces: map[string]domain.Bootstrap{}}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	began := time.Now()
	mode := os.Getenv("FLOW_P21_RECOVERY_MODE")
	if mode == "resident" {
		repo.metadataResidency = newMetadataResidency()
		// The isolated primitive comparison reads canonical policy directly;
		// production additionally requires the full catalog/journal gate.
		repo.metadataResidency.requireCoverage = false
		err = repo.loadResidentWorkspaces(t.Context())
	} else if mode == "legacy" {
		err = repo.loadOrSeed(t.Context())
	} else {
		t.Fatal("FLOW_P21_RECOVERY_MODE must be legacy or resident")
	}
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(began)
	runtime.GC()
	runtime.ReadMemStats(&after)
	measurement := map[string]any{"mode": mode, "elapsed_ms": elapsed.Milliseconds(), "allocated_bytes": after.TotalAlloc - before.TotalAlloc, "retained_heap_bytes": after.HeapAlloc - min(after.HeapAlloc, before.HeapAlloc), "heap_bytes": after.HeapAlloc, "full_metadata_decodes": repo.WorkspaceMetadataDecodes(), "workspaces": len(repo.WorkspaceKeys())}
	encoded, _ := json.Marshal(measurement)
	t.Logf("P21_RECOVERY_PRIMITIVE %s", encoded)
	runtime.KeepAlive(repo)
}

func scaleP21SeedRow(t *testing.T, tx *sqlTx, workspace, field, key string, order int, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMetadataRecordRow(context.Background(), tx, workspace, metadataRecordKey{field, key}, metadataRecord{order: order, data: raw}, nil); err != nil {
		t.Fatal(err)
	}
}
