package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW188InlineProjects(t *testing.T) (scaleP09Fixture, *SQLiteStore, []byte) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects", "teams", "teamSettings"), f.data.Workspace.URLKey, "scale_p21_sw188.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = []domain.Project{
			{ID: "sw188-duplicate", Name: "First", Description: strings.Repeat("first", 32000)},
			{ID: "sw188-duplicate", Name: "Second", Description: strings.Repeat("second", 32000)},
		}
		parent := "sw188-private"
		child := "sw188-child"
		data.Teams = append(data.Teams, domain.Team{ID: parent, Key: parent, Private: true}, domain.Team{ID: child, Key: child, Private: true})
		data.TeamSettings[parent] = domain.TeamSettings{TeamID: parent, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, Access: "private", ParentTeamID: parent}
		normalized := cloneBootstrap(*data)
		normalizeStoredMetadata(&normalized)
		data.Teams, data.TeamSettings = normalized.Teams, normalized.TeamSettings
		return nil
	}); err != nil {
		t.Fatal("supported inline producer", err)
	}
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", f.data.Workspace.URLKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) <= MetadataRecordPageMaxBytes {
		t.Fatalf("fixture did not require streamed projection: %d", len(raw))
	}
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	t.Cleanup(probe.releaseMetadataLogicalSources)
	return f, probe, raw
}

func scaleP21SW188ComparePolicy(t *testing.T, got, want domain.Bootstrap) {
	t.Helper()
	left, right := reflect.ValueOf(got), reflect.ValueOf(want)
	for _, field := range persistedMetadataFields() {
		mode := metadataResidencyFields()[field.name]
		if mode != "core" && mode != "policy" {
			continue
		}
		if !reflect.DeepEqual(left.Field(field.index).Interface(), right.Field(field.index).Interface()) {
			t.Fatalf("resident %s differs from full hydration", field.name)
		}
	}
}

func TestScaleP21SW188InlineResidentDifferential(t *testing.T) {
	f, probe, raw := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	legacy, _, err := f.loadWorkspaceStamped(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	want := resourcePolicyMetadata(legacy)
	for _, storage := range []string{"blob", "text", "last-property-wins"} {
		t.Run(storage, func(t *testing.T) {
			var stored any = raw
			switch storage {
			case "text":
				stored = string(raw)
			case "last-property-wins":
				// The map-based resident projection selects the last exact key,
				// including an earlier manifest replaced by the producer's one.
				stored = []byte(`{"_flowCollections":{},` + strings.Repeat(`"viewerRole":"earlier-role",`, 300) + string(raw[1:]))
			}
			if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", stored, workspace); err != nil {
				t.Fatal(err)
			}
			_, stamp, err := f.loadWorkspaceStamped(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				chunks := 0
				ctx := WithSQLStatementObserver(t.Context(), func(query string) {
					if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
						chunks++
					}
				})
				tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
				if err != nil {
					t.Fatal(err)
				}
				got, gotStamp, _, err := probe.readResidentWorkspace(ctx, tx, workspace)
				tx.Rollback()
				if err != nil || !reflect.DeepEqual(resourcePolicyMetadata(got), want) || gotStamp != stamp {
					t.Fatalf("policy/stamp differs from full hydration: err=%v stamp=%v/%v", err, gotStamp, stamp)
				}
				scaleP21SW188ComparePolicy(t, got, legacy)
				if len(got.Projects) != 0 || chunks < 2 || probe.metadataResidency.work.used != 0 || probe.WorkspaceMetadataDecodes() != 0 {
					t.Fatalf("record body retained or work not released: chunks=%d work=%d", chunks, probe.metadataResidency.work.used)
				}
			}
		})
	}
	if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", raw, workspace); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := probe.loadResidentWorkspaces(t.Context()); err != nil {
			t.Fatal("actual resident startup/reload", err)
		}
		got, ok := probe.storedWorkspace(workspace)
		if !ok || !reflect.DeepEqual(resourcePolicyMetadata(got), want) {
			t.Fatal("resident startup/reload policy differs")
		}
		scaleP21SW188ComparePolicy(t, got, legacy)
	}
	t.Logf("legacy physical root=%d bytes; bounded resident policy excludes both duplicate project bodies", len(raw))
}

func TestScaleP21SW188InlineResidentCancellation(t *testing.T) {
	f, probe, _ := scaleP21SW188InlineProjects(t)
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	chunks := 0
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
			chunks++
			if chunks == 2 {
				cancel()
			}
		}
	})
	tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = probe.readResidentWorkspace(ctx, tx, f.data.Workspace.URLKey)
	tx.Rollback()
	if !errors.Is(err, context.Canceled) || chunks != 2 || probe.metadataResidency.work.used != 0 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatalf("cancelled projection leaked admission or resources: err=%v chunks=%d work=%d", err, chunks, probe.metadataResidency.work.used)
	}
}

func TestScaleP21SW188InlineResidentSkippedValidation(t *testing.T) {
	f, probe, raw := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	cases := []struct {
		name, suffix string
		coverage     bool
	}{
		{"invalid-record-json", `,"projects":[0,]}`, false},
		{"unknown-root", `,"unregistered":true}`, true},
		{"unsplit-external", `,"notifications":[{}]}`, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			changed := append(append([]byte(nil), raw[:len(raw)-1]...), item.suffix...)
			if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", changed, workspace); err != nil {
				t.Fatal(err)
			}
			tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, err = probe.readResidentWorkspace(t.Context(), tx, workspace)
			tx.Rollback()
			if err == nil || item.coverage && !errors.Is(err, ErrBoundedMetadataCoverage) || probe.metadataResidency.work.used != 0 {
				t.Fatalf("invalid skipped field accepted or leaked: %v", err)
			}
		})
	}
}

func TestScaleP21SW188InlineResidentSelectedCoreBound(t *testing.T) {
	f, probe, raw := scaleP21SW188InlineProjects(t)
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace
	workspace.Name = strings.Repeat("x", MetadataRecordPageMaxBytes+1)
	body, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	root["workspace"] = body
	raw, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", raw, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = probe.readResidentWorkspace(t.Context(), tx, f.data.Workspace.URLKey)
	tx.Rollback()
	if !errors.Is(err, ErrMetadataPageBudget) || probe.metadataResidency.work.used != 0 {
		t.Fatalf("selected core exceeded existing bound: %v", err)
	}
}
