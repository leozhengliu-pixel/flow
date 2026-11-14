package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

type scaleP21SW0ResidentScannerReader struct {
	tx      *sqlTx
	queries int
	details int
}

func (r *scaleP21SW0ResidentScannerReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.queries++
	if strings.Contains(query, "CASE WHEN") {
		r.details++
	}
	return r.tx.QueryContext(ctx, query, args...)
}

func scaleP21SW0ResidentScannerFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, *sqlTx) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	f.db.SetMaxOpenConns(1)
	options := *metadataReadOptions(f.dialect)
	options.ReadOnly = false
	tx, err := f.db.BeginTx(t.Context(), &options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='teams'`, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{}}
	view.metadataResidency.requireCoverage = false
	return f, view, tx
}

func TestScaleP21SW0ResidentScannerMixedStorageOracle(t *testing.T) {
	f, view, tx := scaleP21SW0ResidentScannerFixture(t)
	workspace := f.data.Workspace.URLKey
	expectedKeys := make([]string, 0, 73)
	for index := range 73 {
		key := fmt.Sprintf("scanner-%03d", index)
		if index%5 == 0 {
			key += "-共享"
		} else if index%5 == 1 {
			key += "-A"
		} else if index%5 == 2 {
			key += "-a"
		}
		body, err := json.Marshal(domain.Team{ID: fmt.Sprintf("duplicate-body-%d", index%7), Name: strings.Repeat("共享", 4000), Key: fmt.Sprint(index)})
		if err != nil {
			t.Fatal(err)
		}
		if index%17 == 0 {
			body = []byte("null")
		}
		var storedKey, storedBody any = key, body
		if f.dialect == "sqlite" {
			if index%3 == 0 {
				storedKey = []byte(key)
			}
			if index%2 == 0 {
				storedBody = string(body)
			}
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teams',?,?,?)`, workspace, storedKey, index%9-5, storedBody); err != nil {
			t.Fatal(err)
		}
		expectedKeys = append(expectedKeys, key)
	}
	slices.Sort(expectedKeys)
	var raw []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	f.dropMetadataCache(t.Context(), workspace)
	want, err := f.decodeWorkspaceMetadata(t.Context(), workspace, raw, tx)
	if err != nil || len(want.Teams) != len(expectedKeys) {
		t.Fatalf("actual decoder oracle: rows=%d error=%v", len(want.Teams), err)
	}
	stamp, err := readMetadataStamp(t.Context(), tx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for pass := range 2 {
		trace := &scaleP21SW0ResidentScannerReader{tx: tx}
		var actualRows []metadataCacheRow
		var actualKeys []string
		pages := 0
		err := view.scanResidentMetadataField(t.Context(), trace, workspace, "teams", stamp, func(rows []metadataCacheRow) error {
			pages++
			used := metadataPageOverhead
			if len(rows) > metadataRecoveryPageRows {
				t.Fatalf("page exceeded row cap: %d", len(rows))
			}
			for _, row := range rows {
				used += len(row.Data) + len(row.Key) + len(row.Field) + 64
				actualKeys = append(actualKeys, row.Key)
				actualRows = append(actualRows, row)
			}
			if used > MetadataRecordPageMaxBytes {
				t.Fatalf("page exceeded encoded byte cap: %d", used)
			}
			var one int
			return tx.QueryRowContext(t.Context(), "SELECT 1").Scan(&one)
		})
		if err != nil {
			t.Fatalf("pass %d scanner: %v", pass, err)
		}
		if !reflect.DeepEqual(actualKeys, expectedKeys) || pages < 3 {
			t.Fatalf("keyset missed, repeated or misordered mixed keys: pages=%d actual=%q expected=%q", pages, actualKeys, expectedKeys)
		}
		if pass == 0 && trace.details == 0 || pass == 1 && trace.details != 0 {
			t.Fatalf("unexpected cold/warm detail fetches: pass=%d details=%d", pass, trace.details)
		}
		sortMetadataRecordRows(actualRows)
		actual := domain.Bootstrap{}
		for _, row := range actualRows {
			if err := appendResidentMetadataRow(&actual, row, "array"); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(actual.Teams, want.Teams) {
			t.Fatal("mixed storage scanner differs from actual typed decoder")
		}
		// Cached bytes must have independent ownership from visitor buffers.
		for _, row := range actualRows {
			if len(row.Data) > 0 {
				row.Data[0] = '!'
			}
		}
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("scanner hydrated full metadata or leaked work")
	}
}

func TestScaleP21SW0ResidentScannerUnicodeBudgetAndCancellation(t *testing.T) {
	f, view, tx := scaleP21SW0ResidentScannerFixture(t)
	workspace := f.data.Workspace.URLKey
	raw := `{"id":"oversized","name":"` + strings.Repeat("名", MetadataRecordPageMaxBytes/2) + `"}`
	var stored any = []byte(raw)
	if f.dialect == "sqlite" {
		stored = raw
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teams','oversized',-9,?)`, workspace, stored); err != nil {
		t.Fatal(err)
	}
	stamp, err := readMetadataStamp(t.Context(), tx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	trace := &scaleP21SW0ResidentScannerReader{tx: tx}
	if err := view.scanResidentMetadataField(t.Context(), trace, workspace, "teams", stamp, func([]metadataCacheRow) error {
		t.Fatal("oversized encoded row reached the visitor")
		return nil
	}); !errors.Is(err, ErrMetadataPageBudget) || trace.details != 0 {
		t.Fatalf("oversized byte header was not rejected before body transfer: error=%v detailQueries=%d", err, trace.details)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	trace = &scaleP21SW0ResidentScannerReader{tx: tx}
	if err := view.scanResidentMetadataField(cancelled, trace, workspace, "teams", stamp, func([]metadataCacheRow) error { return nil }); !errors.Is(err, context.Canceled) || trace.queries != 0 {
		t.Fatalf("cancelled scanner queried SQL: error=%v queries=%d", err, trace.queries)
	}
}
