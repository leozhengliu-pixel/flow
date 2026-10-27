package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Stored capacities that are defaults (4, or the team's capacity setting) stay
// automatic; typed ones become overrides; existing overrides are kept.
func TestCycleCapacityOverrideMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	kept := 12
	cycles := []domain.Cycle{
		{ID: "cycle_old_default", Capacity: 4},
		{ID: "cycle_team_default", Capacity: 9},
		{ID: "cycle_typed", Capacity: 25},
		{ID: "cycle_zero", Capacity: 0},
		{ID: "cycle_override", Capacity: 40, CapacityOverride: &kept},
	}
	if err := repo.MutateWorkspace(ctx, accessWorkspace, "test.seeded", "cycles", nil, func(next *domain.Bootstrap) error {
		teamID := next.Teams[0].ID
		settings := next.CycleSettings[teamID]
		settings.Capacity = 9
		if next.CycleSettings == nil {
			next.CycleSettings = map[string]domain.CycleSettings{}
		}
		next.CycleSettings[teamID] = settings
		for index, cycle := range cycles {
			cycle.TeamID, cycle.Number, cycle.Name, cycle.Status = teamID, 100+index, cycle.ID, "upcoming"
			cycle.StartsAt = updatedAt.AddDate(0, 0, 14*index)
			cycle.EndsAt = cycle.StartsAt.AddDate(0, 0, 13)
			cycle.CreatedAt, cycle.UpdatedAt = updatedAt, updatedAt
			next.Cycles = append(next.Cycles, cycle)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, cycleCapacityOverrideMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	want := map[string]*int{"cycle_old_default": nil, "cycle_team_default": nil, "cycle_typed": new(25), "cycle_zero": nil, "cycle_override": new(12)}
	check := func(stage string) {
		t.Helper()
		repo, err := OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		defer repo.Close()
		if applied, err := repo.migrationApplied(ctx, cycleCapacityOverrideMigrationVersion); err != nil || !applied {
			t.Fatalf("%s: migration not recorded: %v %v", stage, applied, err)
		}
		data, ok := repo.WorkspaceMetadataFields(accessWorkspace, "cycles")
		if !ok {
			t.Fatal("workspace missing")
		}
		found := 0
		for _, cycle := range data.Cycles {
			expected, tracked := want[cycle.ID]
			if !tracked {
				continue
			}
			found++
			switch {
			case expected == nil && cycle.CapacityOverride != nil:
				t.Errorf("%s: %s override = %d, want automatic", stage, cycle.ID, *cycle.CapacityOverride)
			case expected != nil && (cycle.CapacityOverride == nil || *cycle.CapacityOverride != *expected):
				t.Errorf("%s: %s override = %v, want %d", stage, cycle.ID, cycle.CapacityOverride, *expected)
			}
			// Nothing is cleaned up: the old stored value stays, and the migration is not an edit.
			if cycle.ID == "cycle_old_default" && cycle.Capacity != 4 || !cycle.UpdatedAt.Equal(updatedAt) {
				t.Errorf("%s: %s capacity=%v updatedAt=%v", stage, cycle.ID, cycle.Capacity, cycle.UpdatedAt)
			}
		}
		if found != len(want) {
			t.Fatalf("%s: found %d of %d cycles", stage, found, len(want))
		}
	}
	check("migrated")
	check("reopened")
}
