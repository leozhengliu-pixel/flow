package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
)

// cycleSettingsDefaultsScenario seeds one team whose stored cycle settings
// predate the capacity setting (no "capacity" key) and one whose settings carry
// capacity 0 with autoCreate off, re-arms the migration, reopens the store
// through open and checks that only the first team gets the old defaults.
func cycleSettingsDefaultsScenario(t *testing.T, workspace string, create func() *SQLiteStore, open func() *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	repo := create()
	legacy := domain.CycleSettings{Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2}
	explicit := domain.CycleSettings{Enabled: true, DurationWeeks: 3, StartsOn: 2, UpcomingCount: 1, Capacity: 0, AutoCreate: false}
	if err := repo.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), workspace, "test.cycle_settings_seeded", "", nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, domain.Team{ID: "team_legacy_cycles", Key: "LEG", Name: "Legacy"}, domain.Team{ID: "team_explicit_cycles", Key: "EXP", Name: "Explicit"})
		next.TeamSettings["team_legacy_cycles"] = next.TeamSettings[next.Teams[0].ID]
		next.TeamSettings["team_explicit_cycles"] = next.TeamSettings[next.Teams[0].ID]
		if next.CycleSettings == nil {
			next.CycleSettings = map[string]domain.CycleSettings{}
		}
		next.CycleSettings["team_legacy_cycles"] = legacy
		next.CycleSettings["team_explicit_cycles"] = explicit
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Strip the capacity key from the legacy record, as a row stored before the
	// setting existed has it.
	var raw []byte
	if err := repo.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='cycleSettings' AND record_key=?`, workspace, "team_legacy_cycles").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["capacity"]; !ok {
		t.Fatalf("stored settings carry no capacity key: %s", raw)
	}
	delete(fields, "capacity")
	stripped, _ := json.Marshal(fields)
	if _, err := repo.db.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='cycleSettings' AND record_key=?`, stripped, workspace, "team_legacy_cycles"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, cycleSettingsDefaultsMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	wantLegacy := legacy
	wantLegacy.Capacity, wantLegacy.AutoCreate, wantLegacy.AutoMigrate = 4, true, true
	for _, stage := range []string{"migrated", "reopened"} {
		repo := open()
		if applied, err := repo.migrationApplied(ctx, cycleSettingsDefaultsMigrationVersion); err != nil || !applied {
			t.Fatalf("%s: migration not recorded: %v %v", stage, applied, err)
		}
		data, ok := repo.BootstrapFor(workspace)
		if !ok {
			t.Fatal("workspace missing")
		}
		if got := data.CycleSettings["team_legacy_cycles"]; got != wantLegacy {
			t.Fatalf("%s: legacy settings = %+v, want %+v", stage, got, wantLegacy)
		}
		if got := data.CycleSettings["team_explicit_cycles"]; got != explicit {
			t.Fatalf("%s: explicit settings = %+v, want %+v", stage, got, explicit)
		}
		repo.Close()
	}
}

func TestCycleSettingsDefaultsMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	open := func() *SQLiteStore {
		repo, err := OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	cycleSettingsDefaultsScenario(t, accessWorkspace, open, open)
}
