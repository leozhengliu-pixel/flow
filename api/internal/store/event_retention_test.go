package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneDomainEventsDeletesOldEventsInBatches(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM domain_events`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insert := func(id, kind string, at time.Time) {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, id, kind, "issue_1", []byte(`{}`), at.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	old := DomainEventPruneBatch*2 + 300
	for i := 0; i < old; i++ {
		insert(fmt.Sprintf("evt_old_%05d", i), "issue.updated", now.Add(-100*24*time.Hour).Add(time.Duration(i)*time.Second))
	}
	insert("evt_preferences", "workspace_preferences.updated", now.Add(-200*24*time.Hour))
	for i := 0; i < 5; i++ {
		insert(fmt.Sprintf("evt_new_%d", i), "issue.updated", now.Add(-time.Duration(i)*time.Hour))
	}
	cutoff := now.Add(-90 * 24 * time.Hour)
	batches, total := 0, 0
	for {
		deleted, err := s.PruneDomainEvents(ctx, cutoff)
		if err != nil {
			t.Fatal(err)
		}
		if deleted > DomainEventPruneBatch {
			t.Fatalf("one call deleted %d events", deleted)
		}
		total += deleted
		batches++
		if deleted < DomainEventPruneBatch {
			break
		}
	}
	if total != old || batches != 3 {
		t.Fatalf("pruned %d events in %d calls, want %d in 3", total, batches, old)
	}
	events, err := s.Events(ctx, "issue_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 {
		t.Fatalf("kept %d events, want the 5 recent ones and the preferences event", len(events))
	}
	if events[0].ID != "evt_preferences" {
		t.Fatalf("the preferences event read on startup was pruned: %s", events[0].ID)
	}
}
