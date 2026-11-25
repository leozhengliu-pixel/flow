package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW23MetadataRow struct {
	Field, Key string
	Order      int
	Data       []byte
}

func scaleP21SW23MigrationRows(t *testing.T, repository *SQLiteStore, workspace string) ([]byte, []scaleP21SW23MetadataRow) {
	t.Helper()
	var root []byte
	if err := repository.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&root); err != nil {
		t.Fatal(err)
	}
	rows, err := repository.db.QueryContext(t.Context(), `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []scaleP21SW23MetadataRow{}
	for rows.Next() {
		var row scaleP21SW23MetadataRow
		if err := rows.Scan(&row.Field, &row.Key, &row.Order, &row.Data); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return root, result
}

func scaleP21SW23MigrationSettings(settings domain.WorkspaceSettings) domain.WorkspaceSettings {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	value := reflect.ValueOf(&settings).Elem()
	for index := 0; index < value.NumField(); index++ {
		field, definition := value.Field(index), value.Type().Field(index)
		if !field.IsZero() || !strings.Contains(definition.Tag.Get("json"), ",omitempty") {
			continue
		}
		switch field.Kind() {
		case reflect.String:
			field.SetString("sw23-retained")
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.String {
				field.Set(reflect.ValueOf([]string{"sw23-retained"}))
			}
		case reflect.Map:
			if field.Type().Key().Kind() == reflect.String && field.Type().Elem().Kind() == reflect.String {
				field.Set(reflect.ValueOf(map[string]string{"sw23-retained": "member"}))
			}
		case reflect.Pointer:
			switch field.Type() {
			case reflect.TypeFor[*time.Time]():
				owned := stamp
				field.Set(reflect.ValueOf(&owned))
			case reflect.TypeFor[*bool]():
				owned := true
				field.Set(reflect.ValueOf(&owned))
			}
		}
	}
	settings.AgentWebAccess = nil
	settings.AgentWebSearch, settings.AgentWebSearchDisabled = false, false
	settings.AgentInstructions = strings.Repeat("ignored guidance ", 10000)
	settings.AllowedMCPConnectors = strings.Repeat("ignored connector ", 10000)
	return settings
}

func TestScaleP21SW23AgentWebAccessMigrationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	enabledAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	off, on := false, true
	for _, spec := range []struct {
		name             string
		explicit         *bool
		search, disabled bool
		updated, history time.Time
		changed, off     bool
	}{
		{"never-set", nil, false, false, enabledAt.Add(time.Hour), time.Time{}, false, false},
		{"saved-before-history", nil, false, false, enabledAt.Add(-time.Hour), enabledAt, false, false},
		{"saved-equal-history", nil, false, false, enabledAt, enabledAt, false, false},
		{"historical-off", nil, false, false, enabledAt.Add(time.Hour), enabledAt, true, true},
		{"historical-off-json-null", nil, false, false, enabledAt.Add(time.Hour), enabledAt, true, true},
		{"legacy-search-on", nil, true, false, enabledAt, time.Time{}, true, false},
		{"disabled", nil, false, true, enabledAt, time.Time{}, true, true},
		{"disabled-precedes-search", nil, true, true, enabledAt, time.Time{}, true, true},
		{"explicit-on", &on, false, true, enabledAt, enabledAt, false, false},
		{"explicit-off", &off, true, false, enabledAt, enabledAt, false, false},
		{"zero-update", nil, false, false, time.Time{}, enabledAt, false, false},
	} {
		t.Run(spec.name, func(t *testing.T) {
			settings := scaleP21SW23MigrationSettings(f.data.WorkspaceSettings)
			settings.AgentWebAccess, settings.AgentWebSearch, settings.AgentWebSearchDisabled = spec.explicit, spec.search, spec.disabled
			settings.UpdatedAt = spec.updated
			raw, _ := json.Marshal(settings)
			var properties map[string]json.RawMessage
			_ = json.Unmarshal(raw, &properties)
			if len(properties) <= MetadataRecordBatchMaxKeys || len(raw) <= MetadataRecordPageMaxBytes {
				t.Fatal("migration fixture must exceed complete-settings bounds")
			}
			reset := func() {
				t.Helper()
				f.SetWebhookSink(nil)
				f.SetRealtimeSink(nil)
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21_sw23.reset_web", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings = settings
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if spec.name == "historical-off-json-null" {
					tx, err := f.db.BeginTx(t.Context(), nil)
					if err != nil {
						t.Fatal(err)
					}
					if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"workspaceSettings", "agentWebAccess"}, metadataRecord{data: []byte("null")}, nil); err != nil {
						_ = tx.Rollback()
						t.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				f.mu.Lock()
				f.startupCacheDrops = slices.DeleteFunc(f.startupCacheDrops, func(key string) bool { return key == workspace })
				f.mu.Unlock()
			}
			type outcome struct {
				Changed, Off bool
				Root         []byte
				Rows         []scaleP21SW23MetadataRow
				Settings     domain.WorkspaceSettings
				Drops        int
			}
			var outcomes []outcome
			for _, bounded := range []bool{false, true} {
				reset()
				source := f.SQLiteStore
				if bounded {
					var err error
					source, err = f.BoundedMetadataViewForTesting(t.Context())
					if err != nil {
						t.Fatal(err)
					}
				}
				beforeRoot, _ := scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace)
				published := 0
				source.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
				source.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
				var beforeEvents int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&beforeEvents); err != nil {
					t.Fatal(err)
				}
				changed, isOff, err := source.migrateWorkspaceAgentWebAccess(t.Context(), workspace, spec.history)
				if err != nil || changed != spec.changed || isOff != spec.off {
					t.Fatalf("bounded=%t changed/off=%v/%v err=%v want=%v/%v", bounded, changed, isOff, err, spec.changed, spec.off)
				}
				root, records := scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace)
				if !bytes.Equal(root, beforeRoot) {
					t.Fatal("property migration rewrote the canonical root or shapes")
				}
				current, ok := source.WorkspaceSettingsMetadata(workspace)
				if !ok || !current.WorkspaceSettings.UpdatedAt.Equal(spec.updated) ||
					current.WorkspaceSettings.AgentInstructions != settings.AgentInstructions ||
					current.WorkspaceSettings.AllowedMCPConnectors != settings.AllowedMCPConnectors {
					t.Fatal("migration changed UpdatedAt or unrelated resident properties")
				}
				var afterEvents int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&afterEvents); err != nil {
					t.Fatal(err)
				}
				if afterEvents != beforeEvents || published != 0 {
					t.Fatal("data migration introduced an event or publication")
				}
				drops := 0
				for _, key := range source.startupCacheDrops {
					if key == workspace {
						drops++
					}
				}
				outcomes = append(outcomes, outcome{changed, isOff, root, records, current.WorkspaceSettings, drops})
				if changed, _, err := source.migrateWorkspaceAgentWebAccess(t.Context(), workspace, spec.history); err != nil || changed {
					t.Fatalf("migration was not idempotent: changed=%v err=%v", changed, err)
				}
				if afterRoot, afterRows := scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace); !bytes.Equal(root, afterRoot) || !reflect.DeepEqual(records, afterRows) {
					t.Fatal("repeated migration rewrote canonical state")
				}
				if bounded && (source.WorkspaceMetadataDecodes() != 0 || source.metadataResidency.work.used != 0 || source.metadataResidency.policyBytes > source.metadataResidency.policyLimit) {
					t.Fatal("migration decoded full metadata or leaked/exceeded residency work")
				}
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("native migration differs: legacy=%+v bounded=%+v", outcomes[0].Changed, outcomes[1].Changed)
			}
		})
	}
}

func TestScaleP21SW23AgentWebAccessFreshPolicyAndCapacity(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	seed := func() {
		t.Helper()
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21_sw23.web_capacity", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.AgentWebAccess = nil
			data.WorkspaceSettings.AgentWebSearch, data.WorkspaceSettings.AgentWebSearchDisabled = true, false
			data.WorkspaceSettings.WelcomeMessage = "Before peer change"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// A peer's unrelated policy change is already committed while this view
	// retains its old policy. Stage the complete fresh candidate in the write.
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21_sw23.peer_policy", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.WelcomeMessage = "Peer policy must survive"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if changed, off, err := view.migrateWorkspaceAgentWebAccess(t.Context(), workspace, time.Time{}); err != nil || !changed || off {
		t.Fatalf("stale-view migration changed/off=%v/%v err=%v", changed, off, err)
	}
	policy, _ := view.WorkspaceSettingsMetadata(workspace)
	if policy.WorkspaceSettings.WelcomeMessage != "Peer policy must survive" || policy.WorkspaceSettings.AgentWebAccess == nil || !*policy.WorkspaceSettings.AgentWebAccess {
		t.Fatal("migration installed a partial or stale resident policy")
	}
	seed()
	view, err = f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	beforeRoot, beforeRows := scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace)
	beforePolicy, _ := view.WorkspaceSettingsMetadata(workspace)
	view.metadataResidency.policyLimit = view.metadataResidency.policyBytes
	if changed, _, err := view.migrateWorkspaceAgentWebAccess(t.Context(), workspace, time.Time{}); changed || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("resident candidate capacity changed=%v err=%v", changed, err)
	}
	if root, rows := scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace); !bytes.Equal(root, beforeRoot) || !reflect.DeepEqual(rows, beforeRows) {
		t.Fatal("resident-capacity failure committed a property patch")
	}
	afterPolicy, _ := view.WorkspaceSettingsMetadata(workspace)
	if !reflect.DeepEqual(beforePolicy, afterPolicy) || view.metadataResidency.work.used != 0 || len(view.startupCacheDrops) != 0 {
		t.Fatal("resident-capacity failure installed policy or leaked invalidation/work")
	}
	view.metadataResidency.policyLimit = metadataPolicyBytes
	if changed, _, err := view.migrateWorkspaceAgentWebAccess(t.Context(), workspace, time.Time{}); err != nil || !changed {
		t.Fatal("migration did not recover after candidate-capacity failure", err)
	}
}

func TestScaleP21SW23AgentWebAccessMigrationRollback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21_sw23.web_failure", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.AgentWebAccess = nil
		data.WorkspaceSettings.AgentWebSearch, data.WorkspaceSettings.AgentWebSearchDisabled = false, true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	beforeRoot, beforeRows := scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace)
	beforePolicy, _ := view.WorkspaceSettingsMetadata(workspace)
	injected := errors.New("sw23 precommit failure")
	f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) { tx.writeErr = injected }})
	t.Cleanup(func() { f.db.commitHooks.Store(nil) })
	if changed, _, err := view.migrateWorkspaceAgentWebAccess(t.Context(), workspace, time.Time{}); changed || !errors.Is(err, injected) {
		t.Fatalf("failed commit changed=%v err=%v", changed, err)
	}
	f.db.commitHooks.Store(nil)
	if root, rows := scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace); !bytes.Equal(root, beforeRoot) || !reflect.DeepEqual(rows, beforeRows) {
		t.Fatal("failed commit exposed a partial property set")
	}
	afterPolicy, _ := view.WorkspaceSettingsMetadata(workspace)
	if !reflect.DeepEqual(beforePolicy, afterPolicy) || len(view.startupCacheDrops) != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("failed commit installed policy, scheduled cache drops or leaked work")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if changed, _, err := view.migrateWorkspaceAgentWebAccess(ctx, workspace, time.Time{}); changed || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled migration changed=%v err=%v", changed, err)
	}
	if changed, off, err := view.migrateWorkspaceAgentWebAccess(t.Context(), workspace, time.Time{}); err != nil || !changed || !off {
		t.Fatalf("migration after rollback changed/off=%v/%v err=%v", changed, off, err)
	}
	if changed, off, err := view.migrateWorkspaceAgentWebAccess(t.Context(), workspace+"-missing", time.Time{}); err != nil || changed || off {
		t.Fatalf("missing workspace changed/off=%v/%v err=%v", changed, off, err)
	}
}
