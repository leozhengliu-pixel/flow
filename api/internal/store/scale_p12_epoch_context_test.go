package store

import (
	"errors"
	"testing"
)

func TestScaleP12LiveEventContextRejectsTheSoleHeldConnection(t *testing.T) {
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
