package store

import (
	"errors"
	"testing"
)

func TestScaleP12LiveEventContextRejectsTheSoleHeldConnection(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		s.db.SetMaxOpenConns(1)
		ctx, release, err := s.BeginWorkspaceRead(t.Context(), fixture.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.LiveEventContext(ctx); !errors.Is(err, ErrRecordJournalUnavailable) {
			release()
			t.Fatalf("sole snapshot allowed a live read: %v", err)
		}
		if _, err := s.RealtimeSourceVersion(ctx, fixture.data.Workspace.URLKey); !errors.Is(err, ErrRecordJournalUnavailable) {
			release()
			t.Fatalf("source version waited on its own snapshot: %v", err)
		}
		release()
		s.db.SetMaxOpenConns(4)
	}
}

func TestScaleP12LiveEventContextWithoutJournalKeepsLegacySnapshot(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		s.db.SetMaxOpenConns(1)
		ctx, release, err := s.BeginWorkspaceRead(t.Context(), fixture.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		if live, err := s.LiveEventContext(ctx); err != nil || live != ctx {
			release()
			t.Fatalf("journal-off snapshot lost the legacy context: %v", err)
		}
		if _, err := s.RealtimeSourceVersion(ctx, fixture.data.Workspace.URLKey); err != nil {
			release()
			t.Fatalf("journal-off source version failed: %v", err)
		}
		release()
		s.db.SetMaxOpenConns(4)
	}
}
