package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// PATCH /api/teams/{id}/cycle-settings stores exactly the settings it answers
// with: a team without stored settings starts from the built-in defaults, and
// loading a database never rewrites them (it used to turn a stored
// autoCreate=false into true when the capacity was 0). Each case runs with
// bounded metadata off and on; the PATCH answer, the warm bootstrap and a
// fresh process (legacy and bounded) must agree, and the cycle maintenance
// tick creates cycles only when autoCreate is on.

func cycleSettingsPersistRun(t *testing.T, bounded bool, input map[string]any, want domain.CycleSettings, wantCycles bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	reader := repo
	if bounded {
		view, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		reader = view
	}
	service := &server{store: reader, authDisabled: true, uploadPath: t.TempDir()}
	handler := newHandler(service)
	created := requestJSON[domain.Bootstrap](t, handler, "POST", "/api/workspaces", map[string]any{"name": "Cycles", "urlKey": "cycles-persist", "region": "us", "tutorial": false}, http.StatusCreated)
	const key = "cycles-persist"
	team := created.Teams[0].ID
	got := requestJSON[domain.CycleSettings](t, handler, "PATCH", "/api/teams/"+team+"/cycle-settings?workspace="+key, input, http.StatusOK)
	if got != want {
		t.Fatalf("PATCH answered %+v, want %+v", got, want)
	}
	check := func(label string, data domain.Bootstrap) {
		t.Helper()
		if data.CycleSettings[team] != want {
			t.Fatalf("%s cycle settings = %+v, want %+v", label, data.CycleSettings[team], want)
		}
		if (len(data.Cycles) > 0) != wantCycles {
			t.Fatalf("%s has %d cycles, want cycles=%t", label, len(data.Cycles), wantCycles)
		}
	}
	warm, _ := reader.BootstrapFor(key)
	check("warm", warm)
	service.maintainCycleSchedule(t.Context(), key)
	warm, _ = reader.BootstrapFor(key)
	check("warm after maintenance", warm)

	for _, reload := range []string{"legacy", "bounded"} {
		fresh, err := store.OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		freshReader := fresh
		if reload == "bounded" {
			view, err := fresh.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			freshReader = view
		}
		data, _ := freshReader.BootstrapFor(key)
		check("reload "+reload, data)
		(&server{store: freshReader, authDisabled: true, uploadPath: t.TempDir()}).maintainCycleSchedule(t.Context(), key)
		data, _ = freshReader.BootstrapFor(key)
		check("reload "+reload+" after maintenance", data)
		fresh.Close()
	}
}

func TestCycleSettingsPersistAutoCreateOff(t *testing.T) {
	want := domain.CycleSettings{Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 4, AutoCreate: false, AutoMigrate: true}
	for _, bounded := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[bounded], func(t *testing.T) {
			cycleSettingsPersistRun(t, bounded, map[string]any{"enabled": true, "autoCreate": false}, want, false)
		})
	}
}

func TestCycleSettingsPersistEveryField(t *testing.T) {
	want := domain.CycleSettings{Enabled: true, DurationWeeks: 3, CooldownWeeks: 1, StartsOn: 5, UpcomingCount: 4, Capacity: 0, AutoCreate: false,
		AutoAddActive: true, AutoAddDueDate: true, AutoAddStarted: true, AutoAddCompleted: true, AutoMigrate: false, FavoriteView: true}
	input := map[string]any{"enabled": true, "durationWeeks": 3, "cooldownWeeks": 1, "startsOn": 5, "upcomingCount": 4, "capacity": 0, "autoCreate": false,
		"autoAddActive": true, "autoAddDueDate": true, "autoAddStarted": true, "autoAddCompleted": true, "autoMigrate": false, "favoriteView": true}
	for _, bounded := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[bounded], func(t *testing.T) {
			cycleSettingsPersistRun(t, bounded, input, want, false)
		})
	}
}

func TestCycleSettingsAutoCreateOnCreatesCycles(t *testing.T) {
	want := domain.CycleSettings{Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 4, AutoCreate: true, AutoMigrate: true}
	for _, bounded := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[bounded], func(t *testing.T) {
			cycleSettingsPersistRun(t, bounded, map[string]any{"enabled": true, "autoCreate": true}, want, true)
		})
	}
}

// Cycle settings stored before the capacity setting existed (no capacity key)
// keep running with the old defaults, capacity 4 with cycles created and issues
// migrated automatically, after the one-time upgrade; a row that carries
// capacity 0 and autoCreate=false keeps its values and creates no cycles.
func TestCycleSettingsLegacyRowsUpgradeOnce(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[bounded], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "flow.db")
			repo, err := store.OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			const key = "cycles-legacy"
			created, err := repo.CreateWorkspace(t.Context(), "Legacy", key, "us")
			if err != nil {
				t.Fatal(err)
			}
			legacy, explicit := created.Teams[0].ID, "team_explicit_cycles"
			if err := repo.MutateWorkspace(workspaceFixtureContext(t.Context()), key, "test.seeded", "", nil, func(next *domain.Bootstrap) error {
				next.Teams = append(next.Teams, domain.Team{ID: explicit, Key: "EXP", Name: "Explicit"})
				next.TeamSettings[explicit] = next.TeamSettings[legacy]
				next.CycleSettings[legacy] = domain.CycleSettings{Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2}
				next.CycleSettings[explicit] = domain.CycleSettings{Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2}
				next.Cycles = []domain.Cycle{}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			repo.Close()
			db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
			if err != nil {
				t.Fatal(err)
			}
			var raw []byte
			if err := db.QueryRow(`SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='cycleSettings' AND record_key=?`, key, legacy).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, "capacity")
			stripped, _ := json.Marshal(fields)
			if _, err := db.Exec(`UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='cycleSettings' AND record_key=?`, stripped, key, legacy); err != nil {
				t.Fatal(err)
			}
			// A migration record from before this fix would not exist: it runs on open.
			db.Exec(`DELETE FROM schema_migrations WHERE version=1013`)
			db.Close()

			for _, reopen := range []string{"first open", "second open"} {
				fresh, err := store.OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				var reader = fresh
				if bounded {
					if reader, err = fresh.BoundedMetadataViewForTesting(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				(&server{store: reader, authDisabled: true, uploadPath: t.TempDir()}).maintainCycleSchedule(t.Context(), key)
				data, _ := reader.BootstrapFor(key)
				if got := data.CycleSettings[legacy]; !got.AutoCreate || !got.AutoMigrate || got.Capacity != 4 {
					t.Fatalf("%s: legacy settings = %+v", reopen, got)
				}
				count := map[string]int{}
				for _, cycle := range data.Cycles {
					count[cycle.TeamID]++
				}
				if count[legacy] == 0 {
					t.Fatalf("%s: legacy team has no auto-created cycles", reopen)
				}
				fresh.Close()
			}
		})
	}
}

func TestCycleSettingsExplicitZeroCapacityRowIsKept(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[bounded], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "flow.db")
			repo, err := store.OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			const key = "cycles-explicit"
			created, err := repo.CreateWorkspace(t.Context(), "Explicit", key, "us")
			if err != nil {
				t.Fatal(err)
			}
			team := created.Teams[0].ID
			want := domain.CycleSettings{Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 0, AutoCreate: false}
			if err := repo.MutateWorkspace(workspaceFixtureContext(t.Context()), key, "test.seeded", "", nil, func(next *domain.Bootstrap) error {
				next.CycleSettings[team] = want
				next.Cycles = []domain.Cycle{}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			repo.Close()
			db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
			if err != nil {
				t.Fatal(err)
			}
			db.Exec(`DELETE FROM schema_migrations WHERE version=1013`)
			db.Close()
			fresh, err := store.OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			reader := fresh
			if bounded {
				if reader, err = fresh.BoundedMetadataViewForTesting(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			(&server{store: reader, authDisabled: true, uploadPath: t.TempDir()}).maintainCycleSchedule(t.Context(), key)
			data, _ := reader.BootstrapFor(key)
			if data.CycleSettings[team] != want || len(data.Cycles) != 0 {
				t.Fatalf("settings = %+v cycles = %d, want %+v and none", data.CycleSettings[team], len(data.Cycles), want)
			}
		})
	}
}
