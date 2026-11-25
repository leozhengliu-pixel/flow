package store

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// SW591: with bounded metadata on, SQLite read-only transactions run on the
// snapshot pool. A read snapshot (a reload's, a legacy view's, an
// authorization's) no longer waits for, or holds up, the one write
// connection: it runs while a write transaction is open there and sees
// exactly the commits made before it began, as it did on that connection.
// A legacy view decoded from the snapshot pool equals the flag-off store's
// answer. Flag off, and for an in-memory database, there is no pool.
func TestScaleP21SW591SQLiteSnapshotPool(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SQLITE_SNAPSHOT_POOL", "on")
	t.Setenv("FLOW_SQLITE_SNAPSHOT_CONNS", "2")
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "pool.db")
	open := func(bounded bool) *SQLiteStore {
		t.Helper()
		value := "off"
		if bounded {
			value = "on"
		}
		t.Setenv("FLOW_BOUNDED_METADATA", value)
		repo, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path})
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	legacy := open(false)
	if legacy.db.snapshots != nil || legacy.db.sqliteReads != nil {
		t.Fatal("SW591 flag-off store opened an extra read pool")
	}
	user, token, err := legacy.Register(ctx, "SW591 owner", idgen.New("sw591-")+"@example.test", "sw591-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.VerifyEmail(ctx, token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	created, err := legacy.CreateWorkspace(ContextWithActor(ctx, user), "SW591", "sw591", "us")
	if err != nil {
		t.Fatal(err)
	}
	workspace := created.Workspace.URLKey
	if err := legacy.MutateWorkspace(WithMetadataFields(ctx, "labels"), workspace, "label.created", "", nil, func(data *domain.Bootstrap) error {
		data.Labels = append(data.Labels, domain.IssueLabel{ID: "sw591-label", Name: "Pool", Scope: "Workspace"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := scaleP21SW557CompleteCatalog(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	// The flag-off answer of the stored state.
	legacy = open(false)
	want, ok := legacy.WorkspaceMetadata(workspace)
	if !ok {
		t.Fatal("SW591 legacy workspace missing")
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	repo := open(true)
	defer repo.Close()
	if !repo.BoundedMetadataEnabled() || repo.db.snapshots == nil {
		t.Fatal("SW591 bounded SQLite store has no snapshot pool")
	}
	// The one write connection is held by an open write transaction.
	writer, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(ctx, `UPDATE workspace_states SET updated_at=updated_at WHERE workspace_key=?`, workspace); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	var before int
	go func() {
		reader, err := repo.db.BeginTx(ctx, metadataReadOptions(repo.dialect))
		if err == nil {
			err = reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='labels'`, workspace).Scan(&before)
			_ = reader.Rollback()
		}
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SW591 a read snapshot waited for the write connection")
	}
	// A legacy view decodes from a snapshot while the write is open.
	viewed := make(chan domain.Bootstrap, 1)
	go func() {
		data, _ := repo.WorkspaceMetadataForRead(WithColdWork(context.Background(), "sw591"), workspace)
		viewed <- data
	}()
	select {
	case got := <-viewed:
		gotJSON, _ := json.Marshal(got.Labels)
		wantJSON, _ := json.Marshal(want.Labels)
		if !bytes.Equal(gotJSON, wantJSON) || got.Workspace.ID != want.Workspace.ID {
			t.Fatalf("SW591 legacy view differs from the flag-off answer:\n%s\n%s", gotJSON, wantJSON)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SW591 a legacy view waited for the write connection")
	}
	// The snapshot sees the commits made before it began, not the open write.
	if _, err := writer.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='labels' AND record_key='sw591-label'`, workspace); err != nil {
		t.Fatal(err)
	}
	reader, err := repo.db.BeginTx(ctx, metadataReadOptions(repo.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var during int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='labels'`, workspace).Scan(&during); err != nil {
		t.Fatal(err)
	}
	// A live point read has its own connection even if the snapshot pool
	// and writer are both busy. It must not see the uncommitted deletion.
	secondReader, err := repo.db.BeginTx(ctx, metadataReadOptions(repo.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer secondReader.Rollback()
	pointCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var point int
	if err := repo.db.QueryRowContext(pointCtx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='labels'`, workspace).Scan(&point); err != nil || point != before {
		t.Fatalf("live point read during mutation: labels=%d want=%d err=%v", point, before, err)
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='labels'`, workspace).Scan(&after); err != nil {
		t.Fatal(err)
	}
	_ = reader.Rollback()
	if during != before || after != before || before == 0 {
		t.Fatalf("SW591 snapshot saw %d then %d labels, want %d (uncommitted writes invisible)", during, after, before)
	}
}
