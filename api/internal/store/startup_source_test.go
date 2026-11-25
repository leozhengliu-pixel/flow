package store

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW17StartupMigrationInstalledSource(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	cycleID := "sw17-startup-source-cycle"
	ctx := WithMetadataFields(t.Context(), "cycles", "cycleSettings", "userSettings")
	if err := f.MutateWorkspace(ctx, workspace, "test.sw17.startup_source", "", nil, func(data *domain.Bootstrap) error {
		data.Cycles = []domain.Cycle{{ID: cycleID, TeamID: data.Teams[0].ID, Capacity: 9}}
		data.CycleSettings[data.Teams[0].ID] = domain.CycleSettings{Capacity: 4}
		delete(data.UserSettings, data.Viewer.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	warm, ok := f.storedWorkspace(workspace)
	if !ok {
		t.Fatal("installed fixture missing")
	}
	cold := cloneBootstrap(warm)
	normalize(&cold)
	if len(warm.Cycles) != 1 || warm.Cycles[0].Resources != nil || warm.Cycles[0].Insight != nil ||
		cold.Cycles[0].Resources == nil || cold.Cycles[0].Insight == nil {
		t.Fatal("fixture did not retain distinct accepted warm/cold cycle normalization")
	}
	if _, exists := warm.UserSettings[warm.Viewer.ID]; exists {
		t.Fatal("accepted warm preference removal was unexpectedly normalized")
	}
	if _, exists := cold.UserSettings[cold.Viewer.ID]; !exists {
		t.Fatal("cold normalization did not synthesize the user's preference")
	}
	var canonicalCycle []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='cycles' AND record_key=?`, workspace, cycleID).Scan(&canonicalCycle); err != nil {
		t.Fatal(err)
	}
	var rawCycle domain.Cycle
	if err := json.Unmarshal(canonicalCycle, &rawCycle); err != nil {
		t.Fatal(err)
	}
	if rawCycle.Resources != nil || rawCycle.Insight != nil {
		t.Fatal("canonical source lost the warm nil collection witness")
	}
	outputs := make([][]byte, 0, 2)
	for _, data := range []domain.Bootstrap{warm, cold} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='cycles' AND record_key=?`, canonicalCycle, workspace, cycleID); err != nil {
			t.Fatal(err)
		}
		source := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes,
			workspaces: map[string]domain.Bootstrap{workspace: data}}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		count, err := source.migrateWorkspaceCycleCapacity(context.Background(), workspace)
		runtime.ReadMemStats(&after)
		if err != nil || count != 1 {
			t.Fatal("legacy cycle migration witness failed", count, err)
		}
		var output []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='cycles' AND record_key=?`, workspace, cycleID).Scan(&output); err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, output)
		t.Logf("legacy cycle warm=%v migrated=%d allocated-bytes=%d allocations=%d", len(outputs) == 1, count,
			after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
	}
	if bytes.Equal(outputs[0], outputs[1]) {
		t.Fatal("canonical-only source unexpectedly predicts both accepted installed cycle outputs")
	}
	var warmOutput, coldOutput domain.Cycle
	if json.Unmarshal(outputs[0], &warmOutput) != nil || json.Unmarshal(outputs[1], &coldOutput) != nil ||
		warmOutput.Resources != nil || coldOutput.Resources == nil || warmOutput.Insight != nil || coldOutput.Insight == nil {
		t.Fatal("legacy cycle output difference did not preserve installed normalization")
	}
	for index, data := range []domain.Bootstrap{warm, cold} {
		var beforeEvents, afterEvents, beforeRows, afterRows int
		eventCount := func(out *int) {
			t.Helper()
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=? AND event_type='user_settings.comment_actions_migrated'`, warm.Workspace.ID).Scan(out); err != nil {
				t.Fatal(err)
			}
		}
		preferenceCount := func(out *int) {
			t.Helper()
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='userSettings' AND record_key=?`, workspace, warm.Viewer.ID).Scan(out); err != nil {
				t.Fatal(err)
			}
		}
		eventCount(&beforeEvents)
		preferenceCount(&beforeRows)
		if beforeRows != 0 {
			t.Fatal("comment migration oracle did not begin from the same absent canonical preference")
		}
		source := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes,
			workspaces: map[string]domain.Bootstrap{workspace: data}}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if err := source.migrateCommentActions(t.Context()); err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&after)
		eventCount(&afterEvents)
		preferenceCount(&afterRows)
		// The native record merge preserves the canonical deletion in both
		// cases, but only the cold logical callback emits a migration event.
		if afterRows != 0 || afterEvents-beforeEvents != index {
			t.Fatal("legacy comment migration lost installed normalization difference", index, afterRows, afterEvents-beforeEvents)
		}
		t.Logf("legacy comment warm=%v rows-created=%d events-created=%d allocated-bytes=%d allocations=%d", index == 0,
			afterRows, afterEvents-beforeEvents, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
	}
}
