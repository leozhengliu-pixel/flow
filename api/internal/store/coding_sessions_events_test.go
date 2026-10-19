package store

import (
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// coding_session.updated follows visible progress: a no-op update writes
// nothing, and bookkeeping-only updates write without an event.
func TestCodingSessionUpdateEventsAreCoalesced(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "coding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	ctx := t.Context()
	now := time.Now().UTC()
	updates := 0
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		if event.Type == "coding_session.updated" && event.AggregateID == "cs1" {
			updates++
		}
	})
	session := domain.CodingSession{ID: "cs1", WorkspaceKey: "w", AgentSessionID: "task1", CreatorID: "u", Source: domain.CodingSourceIssue, State: domain.CodingSessionPending, Phase: domain.CodingPhaseQueued, PhaseStartedAt: now, CreatedAt: now, UpdatedAt: now}
	anchor := domain.AgentTask{ID: "task1", WorkspaceKey: "w", AppUserID: "app", CreatorID: "u", Status: "pending", Version: 1, CreatedAt: now, UpdatedAt: now}
	created, _, err := repo.CreateCodingSession(ctx, session, anchor, "")
	if err != nil {
		t.Fatal(err)
	}
	update := func(mutate func(*domain.CodingSession)) domain.CodingSession {
		t.Helper()
		saved, err := repo.UpdateCodingSession(ctx, "w", "cs1", func(current *domain.CodingSession) error {
			mutate(current)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}

	cases := []struct {
		name        string
		mutate      func(*domain.CodingSession)
		wantVersion int64 // relative to the previous version
		wantEvents  int   // new events
	}{
		{name: "no-op (repeated reconcile pass)", mutate: func(*domain.CodingSession) {}, wantVersion: 0, wantEvents: 0},
		{name: "same values rewritten", mutate: func(s *domain.CodingSession) { s.Phase = domain.CodingPhaseQueued }, wantVersion: 0, wantEvents: 0},
		{name: "bookkeeping only (retry counter, deadline)", mutate: func(s *domain.CodingSession) {
			s.Attempts++
			deadline := now.Add(time.Hour)
			s.PhaseDeadline = &deadline
		}, wantVersion: 1, wantEvents: 0},
		{name: "reschedule only", mutate: func(s *domain.CodingSession) { s.NextReconcileAt = now.Add(time.Minute) }, wantVersion: 1, wantEvents: 0},
		{name: "phase change", mutate: func(s *domain.CodingSession) { s.Phase = domain.CodingPhaseProvisioning }, wantVersion: 1, wantEvents: 1},
		{name: "summary change", mutate: func(s *domain.CodingSession) { s.Summary = "Working on it" }, wantVersion: 1, wantEvents: 1},
	}
	version := created.Version
	for _, tc := range cases {
		before := updates
		saved := update(tc.mutate)
		if saved.Version != version+tc.wantVersion {
			t.Errorf("%s: version %d, want %d", tc.name, saved.Version, version+tc.wantVersion)
		}
		if got := updates - before; got != tc.wantEvents {
			t.Errorf("%s: %d events, want %d", tc.name, got, tc.wantEvents)
		}
		version = saved.Version
	}
	if stored, err := repo.CodingSession(ctx, "w", "cs1"); err != nil || stored.Version != version || stored.Summary != "Working on it" {
		t.Fatalf("stored: %+v %v", stored, err)
	}
}
