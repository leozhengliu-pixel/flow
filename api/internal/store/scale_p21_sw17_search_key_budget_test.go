package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"flow/api/internal/domain"
)

func sw17SearchBudgetFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, *SQLiteStore) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if f.startupLock != nil {
		backend, err := f.startupLock(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if backend != nil {
			lock, err := acquireStartupLock(t.Context(), backend, startupLockOptions{name: "SW17 search key oracle"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(lock.release)
		}
	}
	legacy := &SQLiteStore{db: f.db, dialect: f.dialect, workspaces: map[string]domain.Bootstrap{
		f.data.Workspace.URLKey: {Workspace: f.data.Workspace},
	}}
	probe := &SQLiteStore{db: f.db, dialect: f.dialect, workspaces: legacy.workspaces, metadataResidency: newMetadataResidency()}
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO metadata_search_migration_checkpoints(workspace_key,field,last_record_key,completed) VALUES(?,'projects','',0) ON CONFLICT DO NOTHING`, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	return f, legacy, probe
}

type sw17SearchSnapshot struct {
	Rows     [][]string
	Last     string
	Complete int
}

func sw17SearchBudgetSnapshot(t *testing.T, f scaleP09Fixture) sw17SearchSnapshot {
	t.Helper()
	result := sw17SearchSnapshot{Rows: [][]string{}}
	rows, err := f.db.QueryContext(t.Context(), `SELECT record_key,content FROM metadata_search_documents WHERE workspace_key=? AND field='projects' ORDER BY record_key`, f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key, content string
		if err := rows.Scan(&key, &content); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		result.Rows = append(result.Rows, []string{key, content})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(t.Context(), `SELECT last_record_key,completed FROM metadata_search_migration_checkpoints WHERE workspace_key=? AND field='projects'`, f.data.Workspace.URLKey).Scan(&result.Last, &result.Complete); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21SW17SearchKeyBoundDifferential(t *testing.T) {
	f, legacy, probe := sw17SearchBudgetFixture(t)
	workspace := f.data.Workspace.URLKey
	prefix := strings.Repeat("界", 180)
	keyFor := func(index int) string { return fmt.Sprintf("%s-%03d", prefix, index) }
	if len(keyFor(0)) <= 191 || utf8.RuneCountInString(keyFor(0)) > 191 {
		t.Fatal("UTF8 fixture does not distinguish byte/character bounds")
	}
	for index := range 270 {
		raw, err := json.Marshal(domain.Project{ID: keyFor(index), Name: fmt.Sprintf("Complete search %03d", index)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects',?,?,?)`, workspace, keyFor(index), index, raw); err != nil {
			t.Fatal(err)
		}
	}
	canonicalHash := func() [32]byte {
		t.Helper()
		hash := sha256.New()
		rows, err := f.db.QueryContext(t.Context(), `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' ORDER BY record_key`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var raw []byte
			if err := rows.Scan(&key, &raw); err != nil {
				t.Fatal(err)
			}
			hash.Write([]byte(key))
			hash.Write(raw)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return [32]byte(hash.Sum(nil))
	}
	before := canonicalHash()
	for _, phase := range []string{"fresh", "completed", "resume"} {
		t.Run(phase, func(t *testing.T) {
			var oracle sw17SearchSnapshot
			for _, source := range []*SQLiteStore{legacy, probe} {
				if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_documents SET content='stale' WHERE workspace_key=? AND field='projects'`, workspace); err != nil {
					t.Fatal(err)
				}
				if _, err := f.db.ExecContext(t.Context(), `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) VALUES(?,'projects','orphan','orphan') ON CONFLICT DO NOTHING`, workspace); err != nil {
					t.Fatal(err)
				}
				last, complete := "", 0
				if phase != "fresh" {
					last = keyFor(127)
				}
				if phase == "completed" {
					complete = 1
				}
				if phase == "resume" {
					if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_documents SET content=(SELECT `+source.metadataSearchContent("m.data")+` FROM workspace_metadata_records m WHERE m.workspace_key=metadata_search_documents.workspace_key AND m.field=metadata_search_documents.field AND m.record_key=metadata_search_documents.record_key) WHERE workspace_key=? AND field='projects' AND record_key<=? AND record_key<>'orphan'`, workspace, last); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_migration_checkpoints SET last_record_key=?,completed=? WHERE workspace_key=? AND field='projects'`, last, complete, workspace); err != nil {
					t.Fatal(err)
				}
				statements := 0
				ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
				if err := source.migrateMetadataSearchIndex(ctx); err != nil {
					t.Fatal(err)
				}
				result := sw17SearchBudgetSnapshot(t, f)
				if len(result.Rows) != 270 || result.Last != keyFor(269) || result.Complete != 1 {
					t.Fatal("UTF8 key repair lost rows or checkpoint state", len(result.Rows), result.Complete)
				}
				for index, row := range result.Rows {
					if row[0] != keyFor(index) || !strings.Contains(row[1], fmt.Sprintf("Complete search %03d", index)) {
						t.Fatal("repair skipped, reordered or retained stale key", index)
					}
				}
				if source == legacy {
					oracle = result
				} else if !reflect.DeepEqual(oracle, result) {
					t.Fatal("bounded UTF8 repair differs from legacy rows/checkpoint")
				}
				t.Logf("phase=%s bounded=%v SQL-statements=%d key-bytes=%d key-characters=%d", phase, source == probe, statements, len(result.Last), utf8.RuneCountInString(result.Last))
			}
		})
	}
	if canonicalHash() != before || probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("bounded key repair changed canonical rows or leaked decode/work")
	}
}

func TestScaleP21SW17SearchKeyBoundBudget(t *testing.T) {
	f, legacy, probe := sw17SearchBudgetFixture(t)
	if f.dialect != "sqlite" {
		t.Skip("legacy SQLite accepts raw keys beyond the external VARCHAR character width")
	}
	workspace := f.data.Workspace.URLKey
	key := strings.Repeat("界", 500000)
	raw := []byte(`{"id":"sw17-large-raw-key","name":"Complete indexed body"}`)
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects',?,0,?)`, workspace, key, raw); err != nil {
		t.Fatal(err)
	}
	probe.metadataResidency.work.limit = 3 << 20
	for _, checkpoint := range []bool{false, true} {
		t.Run(fmt.Sprintf("checkpoint=%v", checkpoint), func(t *testing.T) {
			last := ""
			if checkpoint {
				last = key
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_migration_checkpoints SET last_record_key=?,completed=0 WHERE workspace_key=? AND field='projects'`, last, workspace); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_documents SET content='stale' WHERE workspace_key=? AND field='projects'`, workspace); err != nil {
				t.Fatal(err)
			}
			before := sw17SearchBudgetSnapshot(t, f)
			keyTransfers, checkpointTransfers := 0, 0
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				if strings.HasPrefix(query, "SELECT record_key FROM workspace_metadata_records") {
					keyTransfers++
				}
				if strings.HasPrefix(query, "SELECT last_record_key FROM metadata_search_migration_checkpoints") {
					checkpointTransfers++
				}
			})
			if err := probe.migrateMetadataSearchIndex(ctx); !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("long raw key/checkpoint did not fail explicit admission", err)
			}
			after := sw17SearchBudgetSnapshot(t, f)
			if !reflect.DeepEqual(before, after) || keyTransfers != 0 || checkpoint && checkpointTransfers != 0 || probe.metadataResidency.work.used != 0 {
				t.Fatal("budget failure transferred oversized strings, published rows/checkpoint, or leaked work", keyTransfers, checkpointTransfers)
			}
			if err := legacy.migrateMetadataSearchIndex(t.Context()); err != nil {
				t.Fatal("flag-off repair must retain the accepted legacy long-key behavior", err)
			}
			t.Logf("legacy accepts key-bytes=%d; bounded limit=%d failed before raw-key/checkpoint transfer=%v", len(key), probe.metadataResidency.work.limit, checkpoint)
		})
	}
	probe.metadataResidency.work.limit = metadataWorkBytes
	if err := probe.migrateMetadataSearchIndex(context.Background()); err != nil {
		t.Fatal("legal SQLite raw key did not recover with sufficient work budget", err)
	}
	result := sw17SearchBudgetSnapshot(t, f)
	if len(result.Rows) != 1 || result.Last != key || result.Complete != 1 || !strings.Contains(result.Rows[0][1], "Complete indexed body") || probe.metadataResidency.work.used != 0 {
		t.Fatal("recovered large-key repair lost output/checkpoint or work allowance")
	}
}
