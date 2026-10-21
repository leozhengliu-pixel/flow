package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewImportStateLeaseAndItems(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	checkReviewImportStore(t, repository, "review-import-workspace")
}

// checkReviewImportStore exercises the import tables on any dialect.
func checkReviewImportStore(t *testing.T, repository *SQLiteStore, workspace string) {
	t.Helper()
	ctx := t.Context()
	record, err := repository.ReviewImportState(ctx, workspace)
	if err != nil || record.Enabled || record.LeaseOwner != "" {
		t.Fatalf("default record = %+v, %v", record, err)
	}
	if due, err := repository.DueReviewImports(ctx, time.Now(), 10); err != nil || len(due) != 0 {
		t.Fatalf("due without a setting = %v, %v", due, err)
	}
	if err := repository.SaveReviewImportSettings(ctx, workspace, `{"repositories":[]}`, true, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if due, err := repository.DueReviewImports(ctx, time.Now(), 10); err != nil || len(due) != 1 || due[0] != workspace {
		t.Fatalf("due = %v, %v", due, err)
	}
	// One lease holder at a time; another owner waits until it is released or expires.
	if ok, err := repository.ClaimReviewImport(ctx, workspace, "replica-a", time.Minute); err != nil || !ok {
		t.Fatalf("claim a = %v, %v", ok, err)
	}
	if ok, err := repository.ClaimReviewImport(ctx, workspace, "replica-b", time.Minute); err != nil || ok {
		t.Fatalf("claim b while a holds = %v, %v", ok, err)
	}
	if ok, _ := repository.ClaimReviewImport(ctx, workspace, "replica-a", time.Minute); ok {
		t.Fatal("a second run of the same replica took a held lease")
	}
	if due, _ := repository.DueReviewImports(ctx, time.Now(), 10); len(due) != 0 {
		t.Fatalf("a leased workspace is due: %v", due)
	}
	if err := repository.FinishReviewImport(ctx, workspace, "replica-b", `{"x":1}`, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	if record, _ := repository.ReviewImportState(ctx, workspace); record.LeaseOwner != "replica-a" || record.Status == `{"x":1}` {
		t.Fatalf("another owner finished a's run: %+v", record)
	}
	next := time.Now().Add(10 * time.Minute)
	if err := repository.FinishReviewImport(ctx, workspace, "replica-a", `{"startedAt":"2026-10-20T00:00:00Z"}`, 3, next); err != nil {
		t.Fatal(err)
	}
	record, _ = repository.ReviewImportState(ctx, workspace)
	if record.LeaseOwner != "" || record.Cursor != 3 || !record.Enabled || record.NextRunAt.Sub(next).Abs() > time.Second || !strings.Contains(record.Status, "startedAt") {
		t.Fatalf("finished record = %+v", record)
	}
	if ok, err := repository.ClaimReviewImport(ctx, workspace, "replica-b", -time.Second); err != nil || !ok {
		t.Fatalf("claim after release = %v, %v", ok, err)
	}
	if ok, err := repository.ClaimReviewImport(ctx, workspace, "replica-c", time.Minute); err != nil || !ok {
		t.Fatalf("claim of an expired lease = %v, %v", ok, err)
	}

	backoff := time.Now().Add(time.Hour)
	long := "gitlab:" + strings.Repeat("deep/", 50) + "project#12"
	for _, item := range []ReviewImportItem{
		{Key: "github:acme/store#1", Scope: "github:acme/store", Fingerprint: "sha|t|open", SyncedHead: "sha"},
		{Key: "github:acme/store#2", Scope: "github:acme/store", Failures: 2, BackoffUntil: backoff, LastError: "HTTP 500"},
		{Key: long, Scope: "gitlab:" + strings.Repeat("deep/", 50) + "project", Fingerprint: "x"},
	} {
		if err := repository.SaveReviewImportItem(ctx, workspace, item); err != nil {
			t.Fatal(err)
		}
	}
	items, err := repository.ReviewImportItems(ctx, workspace, "github:acme/store")
	if err != nil || len(items) != 2 || items["github:acme/store#1"].SyncedHead != "sha" || items["github:acme/store#2"].Failures != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	items, _ = repository.ReviewImportItems(ctx, workspace, "gitlab:"+strings.Repeat("deep/", 50)+"project")
	if item, ok := items[ReviewImportKey(long)]; !ok || item.Fingerprint != "x" || len(item.Key) > 191 {
		t.Fatalf("long key item = %+v", items)
	}
	backoffs, err := repository.ReviewImportBackoffs(ctx, workspace, time.Now(), 10)
	if err != nil || len(backoffs) != 1 || backoffs[0].Key != "github:acme/store#2" || backoffs[0].BackoffUntil.Sub(backoff).Abs() > time.Second {
		t.Fatalf("backoffs = %+v, %v", backoffs, err)
	}
}
