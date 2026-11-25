package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW31CatalogScalar(t *testing.T, f scaleP09Fixture, raw string) (metadataCatalogRow, int, error) {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	key := RecordKey{Kind: "settings", ID: "sw31-scalar"}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, f.data.Workspace.URLKey, key.Kind, key.ID, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	queries := 0
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "SELECT SUBSTR(") {
			queries++
		}
	})
	row, err := f.projectCanonicalMetadata(ctx, tx, f.data.Workspace.URLKey, key, int64(len(raw)))
	return row, queries, err
}

func TestScaleP21SW31CatalogScalarEOF(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	values := []string{"0", "-0", "1", "-1", "12", "0.0", "-0.125", "1e0", "1E+2", "1e-2", "-12.5E-7", "1e309", "1e-1000", " 1", "\n-0.125", "1 \t\r\n"}
	for _, size := range []int{4095, 4096, 4097, metadataRecordChunkBytes - 1, metadataRecordChunkBytes, metadataRecordChunkBytes + 1} {
		values = append(values, "0."+strings.Repeat("0", size-3)+"1")
	}
	for index, raw := range values {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			if !json.Valid([]byte(raw)) {
				t.Fatal("invalid oracle case")
			}
			row, queries, err := scaleP21SW31CatalogScalar(t, f, raw)
			if err != nil {
				t.Fatalf("legal scalar number bytes=%d: %v", len(raw), err)
			}
			if !row.omitted || string(row.summary) != `{"id":"sw31-scalar"}` || row.version != metadataFingerprint([]byte(raw)) {
				t.Fatalf("scalar omission/key/exact fingerprint changed: %+v", row)
			}
			if queries != (len(raw)+metadataRecordChunkBytes-1)/metadataRecordChunkBytes {
				t.Fatalf("scalar SQL count=%d bytes=%d", queries, len(raw))
			}
		})
	}
}

func TestScaleP21SW31CatalogScalarInvalidEOF(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	for index, raw := range []string{"", " ", "-", "+", ".", "1.", "1e", "1E", "1e+", "1e-", "--1", "01", "-01", "+1", ".1", "00", "-00", "1.e1", "1e1e1", "1..0", "NaN", "Infinity", "-Infinity", "1+2", "1e+e", "[1", "{", "tru", "nul", "]", "0." + strings.Repeat("0", 4094) + "e"} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			if json.Valid([]byte(raw)) {
				t.Fatal("valid oracle in malformed corpus")
			}
			if _, _, err := scaleP21SW31CatalogScalar(t, f, raw); err == nil {
				t.Fatalf("malformed EOF scalar accepted: %q", raw)
			}
		})
	}
}

func TestScaleP21SW31CatalogScalarOtherBranches(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	for _, raw := range []string{`null`, `true`, `false`, `"scalar"`, `[]`, `[1,2]`, `{}`, `{"id":"source-id","name":"Name","description":"Private body"}`} {
		row, _, err := scaleP21SW31CatalogScalar(t, f, raw)
		if err != nil || row.version != metadataFingerprint([]byte(raw)) {
			t.Fatal("unrelated projector branch changed", raw, err)
		}
	}
	// Existing trailing-data behavior is outside this exact-EOF correction.
	for _, raw := range []string{"1 2", "1,2", "1}garbage", "0x1", "1_0", "1\x00", "1\xff", "1\\"} {
		if _, _, err := scaleP21SW31CatalogScalar(t, f, raw); err != nil {
			t.Fatal("scalar fix expanded preexisting non-EOF parsing behavior", raw, err)
		}
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = f.projectCanonicalMetadata(ctx, tx, f.data.Workspace.URLKey, RecordKey{Kind: "settings", ID: "cancelled"}, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("scalar EOF allowance suppressed cancellation", err)
	}
	_, err = f.projectCanonicalMetadata(t.Context(), tx, f.data.Workspace.URLKey, RecordKey{Kind: "settings", ID: "missing"}, 1)
	if err == nil {
		t.Fatal("scalar EOF allowance suppressed a SQL read error")
	}
}

func TestScaleP21SW31CatalogWorkspaceSettingsLifecycle(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	probe.metadataResidency.requireCoverage = true
	t.Cleanup(probe.releaseMetadataLogicalSources)
	for _, duration := range []int{47, 93} {
		if err := f.MutateWorkspace(t.Context(), workspace, "workspace.settings_updated", workspace, nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.SessionDurationDays = duration
			return nil
		}); err != nil {
			t.Fatal("actual workspace settings producer", err)
		}
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='workspaceSettings' AND record_key='sessionDurationDays'`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if string(raw) != fmt.Sprint(duration) {
			t.Fatalf("producer did not write the exact numeric scalar: %q", raw)
		}
		rows, err := f.db.QueryContext(t.Context(), `SELECT DISTINCT field FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		var fields []string
		for rows.Next() {
			var field string
			if err := rows.Scan(&field); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			fields = append(fields, field)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		if len(fields) == 0 {
			t.Fatal("actual producer has no canonical fields")
		}
		for _, field := range fields {
			// Restart the real cursor after the second actual producer update.
			if duration == 93 {
				if err := f.ResetMetadataCatalogBackfill(t.Context(), workspace, field); err != nil {
					t.Fatal(err)
				}
			}
			completed := false
			for pass := 0; pass < 1024; pass++ {
				progress, err := f.BackfillMetadataCatalog(t.Context(), workspace, field)
				if err != nil {
					t.Fatalf("actual backfill field=%s duration=%d: %v", field, duration, err)
				}
				if progress.Complete {
					completed = true
					break
				}
			}
			if !completed {
				t.Fatalf("backfill failed to complete field=%s", field)
			}
		}
		tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
		if err != nil {
			t.Fatal(err)
		}
		readyErr := f.validateResidentBackfill(t.Context(), tx, workspace)
		tx.Rollback()
		if readyErr != nil {
			t.Fatal("actual complete canonical backfill failed readiness", readyErr)
		}
		full, _, err := f.loadWorkspaceStamped(t.Context(), workspace)
		if err != nil {
			t.Fatal("literal full-hydration reload oracle", err)
		}
		if full.WorkspaceSettings.SessionDurationDays != duration {
			t.Fatal("full reload oracle lost the actual settings producer")
		}
		if err := probe.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal("coverage-enabled bounded reload", err)
		}
		actual, found := probe.storedWorkspace(workspace)
		if !found || !reflect.DeepEqual(actual.Workspace, full.Workspace) ||
			!reflect.DeepEqual(actual.WorkspaceSettings, full.WorkspaceSettings) ||
			!reflect.DeepEqual(resourcePolicyMetadata(actual), resourcePolicyMetadata(full)) {
			t.Fatal("bounded reload changed exact core/settings/policy")
		}
		if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
			t.Fatal("bounded reload decoded full metadata or retained work admission")
		}
	}
}
