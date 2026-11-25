package store

import (
	"errors"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestScaleP21SW288PulseScheduleReadSource pins the fact the SW286 wrapper
// guard relies on: a pulse read has no logical source only when the flag-off
// branch of beginPulseRead built it, so LoadSchedule's literal
// WorkspaceMetadataFields branch never runs with bounded metadata on. A
// bounded read answers from its retained source even when the resident copy
// holds only the workspace identity; a closed read refuses before either
// branch.
func TestScaleP21SW288PulseScheduleReadSource(t *testing.T) {
	cursor := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	data := domain.Bootstrap{
		Workspace:         domain.Workspace{ID: "sw288-workspace", URLKey: "logical"},
		WorkspaceSettings: domain.WorkspaceSettings{FeatureFlags: map[string]bool{"pulse": true}},
		Settings:          map[string]any{"pulseDeliveryCursors": map[string]time.Time{"user": cursor}},
		UserSettings:      map[string]domain.UserSettings{"user": {UserID: "user", PulseSchedule: "weekly", Timezone: "Asia/Tokyo"}},
		TeamSettings:      map[string]domain.TeamSettings{"team": {Timezone: "Europe/London"}},
	}
	literal := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": data}}
	bounded := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	if len(bounded.workspaces["physical"].UserSettings) != 0 {
		t.Fatal("bounded fixture must hold only the resident workspace identity")
	}
	for _, source := range []*SQLiteStore{literal, bounded} {
		read, found, err := source.BeginPulseScheduleRead(t.Context(), "physical")
		if err != nil || !found || read == nil {
			t.Fatal("schedule read", found, err)
		}
		if (read.source != nil) != source.BoundedMetadataEnabled() {
			t.Fatalf("bounded=%t read has source=%t", source.BoundedMetadataEnabled(), read.source != nil)
		}
		if err := read.LoadSchedule(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := read.Snapshot.UserSettings["user"]; got.PulseSchedule != "weekly" || got.Timezone != "Asia/Tokyo" ||
			read.Snapshot.TeamSettings["team"].Timezone != "Europe/London" {
			t.Fatalf("bounded=%t schedule inputs lost: %+v", source.BoundedMetadataEnabled(), read.Snapshot)
		}
		read.Close()
		if err := read.LoadSchedule(t.Context()); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatalf("bounded=%t closed read reached a schedule branch: %v", source.BoundedMetadataEnabled(), err)
		}
		missing, found, err := source.BeginPulseScheduleRead(t.Context(), "missing")
		if err != nil || found || missing != nil {
			t.Fatalf("bounded=%t missing workspace returned a read: %v %t %v", source.BoundedMetadataEnabled(), missing, found, err)
		}
	}
}
