package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Historical SQLite rows can have BLOB identities that the current writer's
// inline JSON protocol cannot encode. Keep the installed old-writer triggers
// active while constructing these rollback-only reader fixtures.
func scaleP12GatesHistoricalFixtureExec(ctx context.Context, tx *sqlTx, query string, args ...any) (sql.Result, error) {
	if tx.dialect != "sqlite" {
		return tx.ExecContext(ctx, query, args...)
	}
	if tx.journalToken != "" {
		return nil, fmt.Errorf("historical fixture joined current journal writer %q", tx.journalToken)
	}
	var token string
	if err := tx.Tx.QueryRowContext(ctx, `SELECT tx_id FROM record_journal_context WHERE id=1`).Scan(&token); err != nil {
		return nil, err
	}
	if token != "" {
		return nil, fmt.Errorf("historical fixture inherited journal writer %q", token)
	}
	return tx.Tx.ExecContext(ctx, query, args...)
}

func TestScaleP12GatesHistoricalFixtureJournalCapture(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite historical TEXT/BLOB physical identities")
	}
	f.db.SetMaxOpenConns(1)
	workspace := f.data.Workspace.URLKey
	const key = "p12-historical-physical-alias"
	before := scaleP09Head(t, f)
	if !f.RecordJournalReady() {
		t.Fatal("historical fixture must retain journal readiness")
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, physicalKey := range []any{key, []byte(key)} {
		if _, err := scaleP12GatesHistoricalFixtureExec(t.Context(), tx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teams',?,0,?)`, workspace, physicalKey, []byte(`{"id":"historical-team"}`)); err != nil {
			t.Fatal(err)
		}
	}
	var enabled, capturing int
	if err := tx.QueryRowContext(t.Context(), `SELECT enabled,capturing FROM record_journal_mode WHERE id=1`).Scan(&enabled, &capturing); err != nil || enabled != 1 || capturing != 1 || !f.RecordJournalReady() {
		t.Fatal("historical fixture disabled journal capture", enabled, capturing, err)
	}
	if tx.journalToken != "" {
		t.Fatal("historical fixture acquired a current writer participant")
	}
	for _, table := range []string{"workspace_metadata_records", "record_journal_dirty"} {
		var rows, storageClasses int
		if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*),COUNT(DISTINCT typeof(record_key)) FROM `+table+` WHERE workspace_key=? AND field='teams' AND CAST(record_key AS BLOB)=CAST(? AS BLOB)`, workspace, key).Scan(&rows, &storageClasses); err != nil || rows != 2 || storageClasses != 2 {
			t.Fatal("historical fixture lost physical aliases or durable capture", table, rows, storageClasses, err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"workspace_metadata_records", "record_journal_dirty"} {
		var rows int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=? AND field='teams' AND CAST(record_key AS BLOB)=CAST(? AS BLOB)`, workspace, key).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("historical fixture rollback retained imported rows", table, rows, err)
		}
	}
	current, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Rollback()
	const currentKey = "p12-historical-normal-writer"
	if _, err := current.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects',?,0,?)`, workspace, currentKey, []byte(`{"id":"p12-historical-normal-writer","name":"Current writer"}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(current.journalToken, "rjv2") {
		t.Fatal("normal writer did not resume the current journal protocol")
	}
	if err := current.Commit(); err != nil {
		t.Fatal(err)
	}
	if !f.RecordJournalReady() || scaleP09Head(t, f) == before {
		t.Fatal("normal writer did not publish a committed journal change")
	}
	var version int64
	if err := f.db.QueryRowContext(t.Context(), `SELECT version FROM record_versions WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, currentKey).Scan(&version); err != nil || version < 1 {
		t.Fatal("normal writer lost its journal version", version, err)
	}
}
