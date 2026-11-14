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

func scaleP21SW0ResidentBoundsFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, *sqlTx) {
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

func TestScaleP21SW0ResidentBoundsOversizedKeyNoTransfer(t *testing.T) {
	f, view, tx := scaleP21SW0ResidentBoundsFixture(t)
	if f.dialect == "mysql" {
		t.Skip("VARBINARY(191) enforces the byte bound at insertion")
	}
	workspace := f.data.Workspace.URLKey
	fixtures := []struct {
		name, key string
		blob      bool
	}{
		{"ascii-text", strings.Repeat("x", 65536), false},
		{"unicode-text", strings.Repeat("名", 64), false},
		{"unicode-blob", strings.Repeat("名", 64), true},
	}
	if f.dialect == "postgres" {
		// VARCHAR(191) permits this 64-rune key, but the existing canonical
		// Go key contract is 191 bytes, not 191 Unicode code points.
		fixtures = fixtures[1:2]
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			var key any = fixture.key
			if fixture.blob {
				key = []byte(fixture.key)
			}
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teams',?,0,?)`, workspace, key, []byte(`{"id":"oversized"}`)); err != nil {
				t.Fatal(err)
			}
			queries, transferred := 0, false
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				queries++
				if strings.Contains(query, "SELECT record_key") {
					transferred = true
				}
			})
			err := view.scanResidentMetadataField(ctx, tx, workspace, "teams", copyStamp{}, func([]metadataCacheRow) error {
				t.Fatal("oversized key reached visitor")
				return nil
			})
			if !errors.Is(err, ErrMetadataRecordQuery) || transferred || queries != 1 {
				t.Fatalf("key crossed driver before bounds error: error=%v queries=%d keyQuery=%v", err, queries, transferred)
			}
			if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='teams'`, workspace); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScaleP21SW0ResidentBoundsAcceptedKeysAndPoolOne(t *testing.T) {
	f, view, tx := scaleP21SW0ResidentBoundsFixture(t)
	workspace := f.data.Workspace.URLKey
	keys := []string{"", strings.Repeat("名", 63) + "ab", strings.Repeat("x", 191), "ordinary"}
	for index, key := range keys {
		body, err := json.Marshal(domain.Team{ID: fmt.Sprint(index), Name: key})
		if err != nil {
			t.Fatal(err)
		}
		var physicalKey any = key
		if f.dialect == "sqlite" && index%2 == 0 {
			physicalKey = []byte(key)
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teams',?,?,?)`, workspace, physicalKey, index-3, body); err != nil {
			t.Fatal(err)
		}
	}
	var raw []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	f.dropMetadataCache(t.Context(), workspace)
	want, err := f.decodeWorkspaceMetadata(t.Context(), workspace, raw, tx)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var rows []metadataCacheRow
		if err := view.scanResidentMetadataField(t.Context(), tx, workspace, "teams", copyStamp{}, func(page []metadataCacheRow) error {
			rows = append(rows, page...)
			var one int
			return tx.QueryRowContext(t.Context(), "SELECT 1").Scan(&one)
		}); err != nil {
			t.Fatal(err)
		}
		sortMetadataRecordRows(rows)
		actual := domain.Bootstrap{}
		for _, row := range rows {
			if err := appendResidentMetadataRow(&actual, row, "array"); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(actual.Teams, want.Teams) {
			t.Fatal("byte preflight changed valid empty/191-byte keys or signed order")
		}
	}
}

func TestScaleP21SW0ResidentBoundsPhysicalKeyAliasesFailClosed(t *testing.T) {
	f, view, tx := scaleP21SW0ResidentBoundsFixture(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite TEXT/BLOB storage classes permit distinct physical keys with equal decoded bytes")
	}
	workspace := f.data.Workspace.URLKey
	for index, key := range []any{"same-key", []byte("same-key")} {
		raw := []byte(fmt.Sprintf(`{"id":"body-%d"}`, index))
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teams',?,?,?)`, workspace, key, index, raw); err != nil {
			t.Fatal(err)
		}
	}
	for pass := range 2 {
		if pass == 1 {
			view.metadataResidency.cacheRow(metadataResidentCacheKey{workspace: workspace, field: "teams", key: "same-key"}, metadataCacheRow{Field: "teams", Key: "same-key", Data: []byte(`{"id":"cached"}`)})
		}
		queries, transferred := 0, false
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			queries++
			if strings.Contains(query, "SELECT record_key") {
				transferred = true
			}
		})
		err := view.scanResidentMetadataField(ctx, tx, workspace, "teams", copyStamp{}, func([]metadataCacheRow) error {
			t.Fatal("aliased physical keys reached visitor")
			return nil
		})
		if !errors.Is(err, ErrMetadataRecordQuery) || transferred || queries != 1 {
			t.Fatalf("physical aliases were not rejected before keys/cache/body: pass=%d error=%v queries=%d keyQuery=%v", pass, err, queries, transferred)
		}
	}
}

func TestScaleP21SW0ResidentBoundsRootTextBytes(t *testing.T) {
	f, view, tx := scaleP21SW0ResidentBoundsFixture(t)
	workspace := f.data.Workspace.URLKey
	var original []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&original); err != nil {
		t.Fatal(err)
	}
	for _, oversized := range []bool{false, true} {
		var root map[string]json.RawMessage
		if err := json.Unmarshal(original, &root); err != nil {
			t.Fatal(err)
		}
		name := strings.Repeat("共享", 50)
		if oversized {
			name = strings.Repeat("名", MetadataRecordPageMaxBytes/2)
		}
		current := f.data.Workspace
		current.Name = name
		body, err := json.Marshal(current)
		if err != nil {
			t.Fatal(err)
		}
		root["workspace"] = body
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		var stored any = raw
		if f.dialect == "sqlite" {
			stored = string(raw)
		}
		if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, stored, workspace); err != nil {
			t.Fatal(err)
		}
		var rootQuery string
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			if strings.Contains(query, "FROM workspace_states WHERE") && strings.Contains(query, "CASE WHEN") {
				rootQuery = query
			}
		})
		got, _, _, err := view.readResidentWorkspace(ctx, tx, workspace)
		if oversized {
			if !errors.Is(err, ErrMetadataPageBudget) || rootQuery == "" {
				t.Fatalf("oversized root error=%v query=%q", err, rootQuery)
			}
			var workspaceID string
			var revision, size int
			var transferred []byte
			if err := tx.QueryRowContext(t.Context(), rootQuery, workspace, cacheKindMetadata, MetadataRecordPageMaxBytes, workspace).Scan(&workspaceID, &revision, &size, &transferred); err != nil {
				t.Fatal(err)
			}
			if size != len(raw) || transferred != nil {
				t.Fatalf("root bound followed characters or transferred oversized body: size=%d bytes=%d transferred=%d", size, len(raw), len(transferred))
			}
		} else if err != nil || got.Workspace.Name != name {
			t.Fatalf("legal Unicode root rejected: name=%q error=%v", got.Workspace.Name, err)
		}
	}
}

func TestScaleP21SW0ResidentBoundsCancelledNoSQL(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	view := &SQLiteStore{dialect: "sqlite", metadataResidency: newMetadataResidency()}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	queries := 0
	ctx := WithSQLStatementObserver(cancelled, func(string) { queries++ })
	// Nil readers/transactions make any attempt to query visible as a panic.
	if err := view.scanResidentMetadataField(ctx, nil, "workspace", "teams", copyStamp{}, func([]metadataCacheRow) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled header scanner=%v", err)
	}
	if _, _, _, err := view.readResidentWorkspace(ctx, nil, "workspace"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled root reader=%v", err)
	}
	if queries != 0 {
		t.Fatalf("cancelled readers issued %d queries", queries)
	}
}
