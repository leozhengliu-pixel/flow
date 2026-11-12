package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP06ReplaySQLByteGuard(t *testing.T) {
	var repo *SQLiteStore
	var err error
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		repo, err = OpenDatabase(DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
	} else {
		repo, err = OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "replay.db"))
	}
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx, workspace := t.Context(), strings.ReplaceAll(idgen.New("p06-replay"), "_", "-")
	data, err := repo.CreateWorkspace(ctx, "P06 replay fixture", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := repo.DeleteWorkspace(cleanup, workspace); err != nil {
			t.Error("could not clean up the replay test workspace")
		}
	}()
	before, err := repo.WorkspaceChangeHead(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(ctx, workspace, "workspace.updated", data.Workspace.ID, map[string]string{"body": strings.Repeat("\u754c", 2000)}, func(data *domain.Bootstrap) error { data.Workspace.Name = "P06 replay"; return nil }); err != nil {
		t.Fatal(err)
	}
	old, err := repo.WorkspaceChanges(ctx, data.Workspace.ID, before, 1)
	if err != nil || len(old.Records) != 1 {
		t.Fatalf("old replay rows=%d err=%v", len(old.Records), err)
	}
	bounded, err := repo.ReadRealtimeReplay(ctx, data.Workspace.ID, before, 8<<10)
	if err != nil {
		t.Fatal(err)
	}
	oldJSON, _ := json.Marshal(old)
	newJSON, _ := json.Marshal(bounded)
	if string(oldJSON) != string(newJSON) {
		t.Fatal("bounded replay changed cursor/record fields")
	}
	oversized, err := repo.ReadRealtimeReplay(ctx, data.Workspace.ID, before, 4000)
	if err != nil || !oversized.ResyncRequired || len(oversized.Records) != 0 {
		t.Fatalf("UTF-8 byte guard failed: resync=%v rows=%d err=%v", oversized.ResyncRequired, len(oversized.Records), err)
	}
	for _, cursor := range []int64{-1, old.Head + 1} {
		page, err := repo.ReadRealtimeReplay(ctx, data.Workspace.ID, cursor, 8<<10)
		if err != nil || !page.ResyncRequired {
			t.Fatal("invalid cursor lost retention semantics")
		}
	}
}
