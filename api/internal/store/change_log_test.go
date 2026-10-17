package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeChange(t *testing.T, s *SQLiteStore, workspaceID, id string, at time.Time) {
	t.Helper()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertDomainEvent(context.Background(), tx, domainEventRow{ID: id, Type: "issue.updated", AggregateID: "issue_1", Payload: []byte(`{}`), CreatedAt: at.Format(time.RFC3339Nano), WorkspaceID: workspaceID}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestChangeLogSeqIsMonotonicUnderConcurrentWritesAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	s, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := s.Bootstrap().Workspace.ID
	start, err := s.WorkspaceChanges(ctx, workspaceID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				writeChange(t, s, workspaceID, fmt.Sprintf("evt_c_%d_%d", w, i), time.Now().UTC())
			}
		}()
	}
	wg.Wait()
	page, err := s.WorkspaceChanges(ctx, workspaceID, start.Head, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 200 || page.Head != start.Head+200 || page.HasMore {
		t.Fatalf("records=%d head=%d start=%d more=%v", len(page.Records), page.Head, start.Head, page.HasMore)
	}
	for i, record := range page.Records {
		if record.Seq != start.Head+int64(i)+1 {
			t.Fatalf("record %d has seq %d, want a gap-free run from %d", i, record.Seq, start.Head+1)
		}
	}
	head := page.Head
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	writeChange(t, s, workspaceID, "evt_after_restart", time.Now().UTC())
	after, err := s.WorkspaceChanges(ctx, workspaceID, head, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Records) != 1 || after.Records[0].Seq != head+1 || after.Records[0].Event.ID != "evt_after_restart" {
		t.Fatalf("after restart: %+v", after)
	}
	if got, _ := s.WorkspaceChangeHead(ctx, s.Bootstrap().Workspace.URLKey); got != head+1 {
		t.Fatalf("head by key = %d, want %d", got, head+1)
	}
}

func TestChangeLogPaginatesAndResyncsBeyondRetention(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	workspaceID := s.Bootstrap().Workspace.ID
	s.SetChangeLogMinRows(5)
	now := time.Now().UTC()
	first, _ := s.WorkspaceChanges(ctx, workspaceID, 0, 1)
	for i := 0; i < 30; i++ {
		writeChange(t, s, workspaceID, fmt.Sprintf("evt_r_%02d", i), now.Add(-100*24*time.Hour).Add(time.Duration(i)*time.Second))
	}
	head := first.Head + 30
	seen, cursor := 0, first.Head
	for {
		page, err := s.WorkspaceChanges(ctx, workspaceID, cursor, 7)
		if err != nil {
			t.Fatal(err)
		}
		seen += len(page.Records)
		cursor = page.Records[len(page.Records)-1].Seq
		if !page.HasMore {
			break
		}
	}
	if seen != 30 || cursor != head {
		t.Fatalf("paged %d events ending at %d, want 30 ending at %d", seen, cursor, head)
	}
	for {
		deleted, err := s.PruneDomainEvents(ctx, now.Add(-90*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if deleted < DomainEventPruneBatch {
			break
		}
	}
	// The newest five rows survive the age cutoff; a client behind them must reload.
	stale, err := s.WorkspaceChanges(ctx, workspaceID, first.Head, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.ResyncRequired || len(stale.Records) != 0 || stale.Head != head {
		t.Fatalf("stale cursor: %+v", stale)
	}
	current, err := s.WorkspaceChanges(ctx, workspaceID, head-5, 100)
	if err != nil {
		t.Fatal(err)
	}
	if current.ResyncRequired || len(current.Records) != 5 {
		t.Fatalf("cursor inside the kept window: %+v", current)
	}
	if ahead, _ := s.WorkspaceChanges(ctx, workspaceID, head+1, 10); !ahead.ResyncRequired {
		t.Fatal("a cursor ahead of the head must resync")
	}
}

func TestChangeLogMigrationBackfillsExistingEvents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	s, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := s.Bootstrap().Workspace.ID
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at,workspace_id) VALUES(?,?,?,?,?,?)`, fmt.Sprintf("evt_legacy_%d", 2-i), "issue.updated", "issue_1", []byte(`{}`), base.Add(time.Duration(i)*time.Second).Format(time.RFC3339Nano), workspaceID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=10`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE domain_events SET seq=NULL`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page, err := s.WorkspaceChanges(ctx, workspaceID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var legacy []string
	for _, record := range page.Records {
		if len(record.Event.ID) > 11 && record.Event.ID[:11] == "evt_legacy_" {
			legacy = append(legacy, record.Event.ID)
		}
	}
	// created_at order, not id order: legacy_2 was written first.
	if fmt.Sprint(legacy) != "[evt_legacy_2 evt_legacy_1 evt_legacy_0]" {
		t.Fatalf("backfill order %v", legacy)
	}
	for i, record := range page.Records {
		if record.Seq != int64(i)+1 {
			t.Fatalf("backfilled seq %d at index %d", record.Seq, i)
		}
	}
}
