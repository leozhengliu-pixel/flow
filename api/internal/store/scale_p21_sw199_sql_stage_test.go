package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestScaleP21SW199SQLStageMigration(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	for index := 0; index < 2; index++ {
		if err := fixture.createBoundedMetadataStagingSchema(t.Context()); err != nil {
			t.Fatal("staging schema is not idempotent", err)
		}
	}
	var recorded int
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version=? AND name=?`,
		boundedMetadataStagingSchemaVersion, "transactional bounded metadata write staging").Scan(&recorded); err != nil || recorded != 1 {
		t.Fatal("staging schema did not run through versioned migration", recorded, err)
	}
	var rows int
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("staging migration created persistent input", rows, err)
	}
	if fixture.dialect == "mysql" {
		var unicodeColumns int
		if err := fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='bounded_metadata_write_stage' AND column_name IN ('data','content') AND character_set_name='utf8mb4' AND collation_name='utf8mb4_bin'`).Scan(&unicodeColumns); err != nil || unicodeColumns != 2 {
			t.Fatal("staging payload inherited a lossy database character set", unicodeColumns, err)
		}
	}
}

func TestScaleP21SW199SQLStageDialectSQL(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		tx := &sqlTx{dialect: dialect}
		stage := &metadataSQLWriteStage{tx: tx, id: "sw199-dialect"}
		tx.metadataSQLStageOwners = map[string]*metadataSQLWriteStage{stage.id: stage}
		for _, column := range []string{"data", "content"} {
			operation := metadataSQLStageAppendData
			if column == "content" {
				operation = metadataSQLStageAppendSearch
			}
			query, err := stage.statementQuery(operation)
			if err != nil {
				t.Fatal(err)
			}
			expression := column + " || ?"
			if dialect == "mysql" {
				expression = "CONCAT(" + column + ",CONVERT(CAST(? AS BINARY) USING utf8mb4))"
			}
			want := "UPDATE bounded_metadata_write_stage SET " + column + "=" + expression + " WHERE operation_id=?"
			if query != want {
				t.Fatal("staging append changed byte interpretation or operation scope", dialect, query, want)
			}
		}
		value, err := stage.valueSQL("data", true)
		if err != nil {
			t.Fatal(err)
		}
		expression := "CAST(data AS BLOB)"
		if dialect == "mysql" {
			expression = "CAST(data AS BINARY)"
		} else if dialect == "postgres" {
			expression = "convert_to(data,'UTF8')"
		}
		if value != "(SELECT "+expression+" FROM bounded_metadata_write_stage WHERE operation_id=?)" {
			t.Fatal("staging final metadata value changed binary representation", dialect, value)
		}
	}
}

func TestScaleP21SW199SQLStageScopedWritesAndCommit(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	first, err := beginMetadataSQLWriteStage(t.Context(), tx, metadataSQLStageTarget{
		workspace: fixture.data.Workspace.URLKey, field: "agentSessions", key: "sw199-first",
		dataBytes: 4*metadataRecordChunkBytes + 2, searchBytes: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close(context.WithoutCancel(t.Context()))
	second, err := beginMetadataSQLWriteStage(t.Context(), tx, metadataSQLStageTarget{
		workspace: fixture.data.Workspace.URLKey, field: "agentSessions", key: "sw199-second",
		dataBytes: len("another operation"), searchBytes: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.WithoutCancel(t.Context()))
	if first.id == second.id {
		t.Fatal("staging operations reused a scope")
	}
	// Put four-byte runes over several SQL boundaries; each argument must
	// remain valid UTF-8 even when a rune straddles the 16 KiB boundary.
	wanted := []string{
		strings.Repeat("x", metadataRecordChunkBytes-2) + "\U0001f600" + strings.Repeat("\u4e2d", metadataRecordChunkBytes),
		"another operation",
	}
	for index, stage := range []*metadataSQLWriteStage{first, second} {
		writer, err := stage.writer(t.Context(), "data")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(wanted[index])); err != nil {
			t.Fatal(err)
		}
		if err := writer.flush(true); err != nil {
			t.Fatal(err)
		}
		valueSQL, err := stage.valueSQL("data", false)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if err := tx.QueryRowContext(t.Context(), "SELECT "+valueSQL, stage.id).Scan(&got); err != nil || got != wanted[index] {
			t.Fatal("chunk staging mixed scopes or changed bytes", index, err)
		}
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := first.writer(t.Context(), "data"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("closed staging accepted a writer", err)
	}
	var count int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage WHERE operation_id=?`, second.id).Scan(&count); err != nil || count != 1 {
		t.Fatal("closing one stage touched another stage", count, err)
	}
	if err := second.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage`).Scan(&count); err != nil || count != 0 {
		t.Fatal("committing successful mutation left staged input", count, err)
	}
}

func TestScaleP21SW199SQLStageCancellationAndRollback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stage, err := beginMetadataSQLWriteStage(t.Context(), tx, metadataSQLStageTarget{
		workspace: fixture.data.Workspace.URLKey, field: "agentSessions", key: "sw199-cancel",
		dataBytes: 3 * metadataRecordChunkBytes, searchBytes: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	appends := 0
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.HasPrefix(query, "UPDATE bounded_metadata_write_stage SET data=") {
			appends++
			if appends == 2 {
				cancel()
			}
		}
	})
	writer, err := stage.writer(ctx, "data")
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.Write([]byte(strings.Repeat("x", 3*metadataRecordChunkBytes)))
	if !errors.Is(err, context.Canceled) || appends != 2 {
		t.Fatal("partial staging lost cancellation", appends, err)
	}
	// The caller rolls back its complete write. No special cleanup session
	// or permission is needed for an unfinished row to disappear.
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage WHERE operation_id=?`, stage.id).Scan(&count); err != nil || count != 0 {
		t.Fatal("rollback committed partial staging data", count, err)
	}
}

func TestScaleP21SW199SQLStageOpenCommitRefused(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stage, err := beginMetadataSQLWriteStage(t.Context(), tx, metadataSQLStageTarget{
		workspace: fixture.data.Workspace.URLKey, field: "agentSessions", key: "sw199-open",
		dataBytes: 0, searchBytes: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); !errors.Is(err, ErrMetadataSQLStageOpen) {
		t.Fatal("an unfinished staging row could commit", err)
	}
	var count int
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage WHERE operation_id=?`, stage.id).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed stage close left committed input", count, err)
	}
}
