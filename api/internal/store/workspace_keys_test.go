package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func sw26TestStage(t *testing.T) *sw26MigrationStage {
	t.Helper()
	stage, err := sw26NewMigrationStage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, query := range []string{
		`CREATE TABLE live(id TEXT PRIMARY KEY COLLATE BINARY,key TEXT NOT NULL) WITHOUT ROWID`,
		`CREATE TABLE pairs(id TEXT COLLATE BINARY,key TEXT COLLATE BINARY,at TEXT NOT NULL,PRIMARY KEY(id,key)) WITHOUT ROWID`,
	} {
		if _, err := stage.exec(t.Context(), 0, query); err != nil {
			t.Fatal(err)
		}
	}
	return stage
}

func TestScaleP21SW26RenameEventProjectionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	fixtures := []string{
		`{"urlKey":"before"}`,
		`{"URLKEY":"case","urlKey":null}`,
		`{"urlKey":12,"URLKEY":"after-type-error"}`,
		`{"urlKey":"before","URLKEY":{},"unknown":[null,1,true]}`,
		`{"urlKey":"before","urlKey":"after"}`,
		`{"urlKey":"unicode-fold"}`,
		`{"urlKey":"\ud800"}`,
		`{"urlKey":"before","unknown":[1,]}`,
		`{"urlKey":"before"} {"urlKey":"after"}`,
		`null`, `[]`, `7`, `""`,
		`{"unknown":"` + strings.Repeat("ignored", 300000) + `","urlKey":"bounded"}`,
		`{"\u0075rlKey":"` + strings.Repeat(`\u0061`, 191) + `"}`,
		`{"urlKey":"` + strings.Repeat("名", 64) + `"}`,
	}
	f.db.SetMaxOpenConns(1)
	for index, payload := range fixtures {
		id := idgen.New("sw26-projection-")
		if index == 3 {
			id = strings.Repeat("名", 100) + id
		}
		stored := any([]byte(payload))
		if f.dialect == "sqlite" && index%2 == 0 {
			stored = payload
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO domain_events(id,event_type,aggregate_id,workspace_id,payload,created_at) VALUES(?,'workspace.updated',?,?,?,?)`, id, f.data.Workspace.ID, f.data.Workspace.ID, stored, "2026-01-02T03:04:05Z"); err != nil {
			t.Fatal(err)
		}
		var oracle struct {
			URLKey string `json:"urlKey"`
		}
		_ = json.Unmarshal([]byte(payload), &oracle)
		tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
		if err != nil {
			t.Fatal(err)
		}
		actual, err := sw26EventURLKey(t.Context(), &sw26EventReaderAt{ctx: t.Context(), tx: tx, id: id, size: int64(len(payload))})
		tx.Rollback()
		if err != nil || len(actual) <= 191 && actual != oracle.URLKey || (len(actual) > 191) != (len(oracle.URLKey) > 191) {
			t.Fatalf("fixture %d differs from ignored-error typed decoder: actual=%q expected=%q error=%v", index, actual, oracle.URLKey, err)
		}
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM domain_events WHERE id=? AND workspace_id=?`, id, f.data.Workspace.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScaleP21SW26RenameStageAggregationAndResume(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	stage := sw26TestStage(t)
	id := f.data.Workspace.ID
	if _, err := stage.exec(t.Context(), int64(len(id)+len(f.data.Workspace.URLKey)), `INSERT INTO live(id,key) VALUES(?,?)`, id, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	ownPrefix := idgen.New("sw26-pair-")
	owned := []string{}
	t.Cleanup(func() {
		for _, key := range owned {
			if _, err := f.db.ExecContext(context.Background(), `DELETE FROM workspace_key_renames WHERE workspace_id=? AND old_key=?`, id, key); err != nil {
				t.Error(err)
			}
		}
	})
	for index := range 133 {
		key := fmt.Sprintf("%s-%03d", ownPrefix, index)
		owned = append(owned, key)
		for _, at := range []string{"2025-01-02T03:04:05Z", "2026-01-02T03:04:05Z", "2024-01-02T03:04:05Z"} {
			if _, err := stage.exec(t.Context(), int64(len(id)+len(key)+len(at)), `INSERT INTO pairs(id,key,at) VALUES(?,?,?) ON CONFLICT(id,key) DO UPDATE SET at=CASE WHEN excluded.at>pairs.at THEN excluded.at ELSE pairs.at END`, id, key, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(t.Context())
	statements := 0
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, "INSERT INTO workspace_key_renames") {
			statements++
			if statements == 2 {
				cancel()
			}
		}
	})
	count, err := f.sw26ApplyRenamePairs(ctx, stage)
	if count != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("partial autocommit/resume contract: count=%d error=%v", count, err)
	}
	count, err = f.sw26ApplyRenamePairs(t.Context(), stage)
	if err != nil || count != 132 {
		t.Fatalf("resume count=%d error=%v", count, err)
	}
	count, err = f.sw26ApplyRenamePairs(t.Context(), stage)
	if err != nil || count != 0 {
		t.Fatalf("idempotent rerun count=%d error=%v", count, err)
	}
	for _, key := range owned {
		var current, at string
		if err := f.db.QueryRowContext(t.Context(), `SELECT new_key,renamed_at FROM workspace_key_renames WHERE workspace_id=? AND old_key=?`, id, key).Scan(&current, &at); err != nil ||
			current != f.data.Workspace.URLKey || at != "2026-01-02T03:04:05Z" {
			t.Fatalf("staged pair changed legacy max/string order: key=%s current=%s at=%s error=%v", key, current, at, err)
		}
	}
}

func TestScaleP21SW26MigrationStageQuotaAndCleanup(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	before := recordSpoolBytes.Load()
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", fmt.Sprint(before))
	if _, err := sw26NewMigrationStage(t.Context()); !errors.Is(err, ErrRecordSpoolBudget) {
		t.Fatalf("quota denial allocated stage: %v", err)
	}
	files, err := os.ReadDir(temp)
	if err != nil || len(files) != 0 {
		t.Fatalf("quota failure left files: %v %v", files, err)
	}
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", fmt.Sprint(before+sw26MigrationStageChunk))
	stage := sw26TestStage(t)
	path := stage.path
	denied := false
	for index := range 20000 {
		_, err := stage.exec(t.Context(), 256, `INSERT INTO pairs(id,key,at) VALUES(?,?,?)`, "id", fmt.Sprintf("%08d-%s", index, strings.Repeat("x", 180)), strings.Repeat("0", 40))
		if err != nil {
			if !errors.Is(err, ErrRecordSpoolBudget) {
				t.Fatal(err)
			}
			denied = true
			break
		}
	}
	if !denied {
		t.Fatal("stage growth never enforced its configured quota")
	}
	var pages, size, ceiling int64
	for query, target := range map[string]*int64{"PRAGMA page_count": &pages, "PRAGMA page_size": &size, "PRAGMA max_page_count": &ceiling} {
		if err := stage.db.QueryRowContext(t.Context(), query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > stage.reserved || pages*size > stage.reserved || ceiling*size != stage.reserved {
		t.Fatalf("physical stage exceeded charged ceiling: pages=%d size=%d ceiling=%d reserved=%d error=%v", pages, size, ceiling, stage.reserved, err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	stage.Close()
	files, err = os.ReadDir(temp)
	if err != nil || len(files) != 0 || recordSpoolBytes.Load() != before {
		t.Fatalf("stage cleanup leaked files or ledger: files=%v bytes=%d before=%d error=%v", files, recordSpoolBytes.Load(), before, err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(filepath.Clean(path + suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unbudgeted SQLite side file %s: %v", suffix, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sw26NewMigrationStage(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestScaleP21SW26RenameEventPagesBounded(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	stage := sw26TestStage(t)
	prefix := idgen.New("sw26-event-key-")
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := range 507 {
		payload := fmt.Sprintf(`{"urlKey":"%s-%03d"}`, prefix, index)
		if index == 20 {
			payload = `{"ignored":"` + strings.Repeat("共享", 100000) + `","urlKey":"` + prefix + `-020"}`
		}
		var stored any = []byte(payload)
		if f.dialect == "sqlite" && index%2 == 0 {
			stored = payload
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO domain_events(id,event_type,aggregate_id,workspace_id,payload,created_at) VALUES(?,'workspace.updated',?,?,?,?)`, idgen.New("sw26-event-"), f.data.Workspace.ID, f.data.Workspace.ID, stored, "2026-01-02T03:04:05Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.db.SetMaxOpenConns(1)
	last, pages := "", 0
	for {
		count, next, err := f.sw26StageRenameEventPage(t.Context(), stage, last)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if count > 500 || count > 0 && next <= last {
			t.Fatalf("source page did not advance within bound: count=%d last=%q next=%q", count, last, next)
		}
		last = next
		if count < 500 {
			break
		}
	}
	var count int
	if err := stage.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pairs WHERE id=? AND key LIKE ?`, f.data.Workspace.ID, prefix+"-%").Scan(&count); err != nil || count != 507 || pages < 2 {
		t.Fatalf("event page snapshot/aggregation differs: rows=%d pages=%d error=%v", count, pages, err)
	}
}

func TestScaleP21SW26RenameBoundedCancellationAndQuota(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{}}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.seedWorkspaceKeyRenamesBounded(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled bounded entry queried SQL", err)
	}
	used := recordSpoolBytes.Load()
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", fmt.Sprint(used))
	if _, err := s.seedWorkspaceKeyRenamesBounded(t.Context()); !errors.Is(err, ErrRecordSpoolBudget) {
		t.Fatal("quota denied entry queried SQL", err)
	}
	s.metadataResidency.work.mu.Lock()
	work, waiting := s.metadataResidency.work.used, s.metadataResidency.work.waiting
	s.metadataResidency.work.mu.Unlock()
	if work != 0 || waiting != 0 || recordSpoolBytes.Load() != used {
		t.Fatal("bounded error path leaked admission")
	}
}
