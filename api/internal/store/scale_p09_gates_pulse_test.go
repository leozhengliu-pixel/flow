package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestScaleP09GatesPulseDeliveryCursorKeepsEpoch(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	epoch := func() string {
		t.Helper()
		value, err := f.AuthzEpoch(t.Context(), workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	write := func(key, value string) {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		scaleP09Put(t, tx, workspace, "settings", key, map[string]string{"owner": value})
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	original := epoch()
	for _, timestamp := range []string{"2026-11-13T00:00:00Z", "2026-11-14T00:00:00Z"} {
		write("pulseDeliveryCursors", timestamp)
		if current := epoch(); current != original {
			t.Fatalf("delivery bookkeeping revoked access: %s -> %s", original, current)
		}
	}
	var version int
	if err := f.db.QueryRowContext(t.Context(), `SELECT version FROM record_versions WHERE workspace_key=? AND field='settings' AND record_key='pulseDeliveryCursors'`, workspace).Scan(&version); err != nil || version != 2 {
		t.Fatalf("delivery cursor changes were not journaled: version=%d err=%v", version, err)
	}
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='pulseDeliveryCursors'`, workspace); err != nil {
		t.Fatal(err)
	}
	if current := epoch(); current != original {
		t.Fatalf("removing delivery bookkeeping revoked access: %s -> %s", original, current)
	}
	write("PulseDeliveryCursors", "case-sensitive-policy-setting")
	if current := epoch(); current == original {
		t.Fatal("another setting was mistaken for the telemetry key")
	}
	original = epoch()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scaleP09Put(t, tx, workspace, "settings", "pulseDeliveryCursors", map[string]string{"owner": "2026-11-15T00:00:00Z"})
	scaleP09Put(t, tx, workspace, "settings", "policy-gates", map[string]string{"access": "restricted"})
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if epoch() == original {
		t.Fatal("a mixed telemetry/policy write failed to invalidate access")
	}
}

func TestScaleP09GatesPulseEpochTriggerDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			found := 0
			for _, spec := range recordJournalTriggerSpecs(dialect) {
				if spec.table != "workspace_metadata_records" {
					continue
				}
				found++
				row := "NEW"
				if spec.event == "DELETE" {
					row = "OLD"
				}
				clause := recordJournalIdentitySQL(dialect, row+".field") + `<>'settings' OR ` + recordJournalIdentitySQL(dialect, row+".record_key") + `<>'pulseDeliveryCursors'`
				if !strings.Contains(spec.body, clause) {
					t.Fatalf("%s misses the exact telemetry-key exception", spec.name)
				}
			}
			if found != 3 {
				t.Fatalf("checked %d mutation triggers, want 3", found)
			}
		})
	}
}

func TestScaleP09GatesPulseEpochTriggerUpgrade(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	// Migration simulation replaces global triggers, so it owns an isolated
	// database even when normal test fixtures use a shared persistent database.
	databasePath := filepath.Join(t.TempDir(), "pulse-epoch-upgrade.db")
	f, err := OpenSQLiteTestFixture(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, spec := range recordJournalTriggerSpecs("sqlite") {
		if spec.table != "workspace_metadata_records" {
			continue
		}
		row := "NEW"
		if spec.event == "DELETE" {
			row = "OLD"
		}
		exception := ` AND (` + row + `.field<>'settings' OR ` + row + `.record_key<>'pulseDeliveryCursors')`
		spec.body = strings.ReplaceAll(spec.body, exception, "")
		if _, err := f.db.ExecContext(t.Context(), `DROP TRIGGER `+spec.name); err != nil {
			t.Fatal(err)
		}
		if err := f.installRecordJournalTrigger(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE version=30`); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteTestFixture(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var installed string
	if err := reopened.db.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master WHERE type='trigger' AND name='flow_rj_workspace_metadata_records_update'`).Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(installed, "pulseDeliveryCursors") {
		t.Fatal("startup retained the previous authorization trigger")
	}
	var count int
	if err := reopened.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version IN (29,30)`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("upgrade lost a migration marker: count=%d err=%v", count, err)
	}
}
