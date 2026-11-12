package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestScaleP09SmallCommitUsesOneStatement(t *testing.T) {
	for _, query := range recordJournalPreparedWrites {
		if !journalWriteStatement(query) {
			t.Fatalf("prepared canonical write bypasses capture: %s", query)
		}
	}
	for _, mode := range []string{"shadow", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			f := scaleP09Database(t)
			if f.dialect != "sqlite" {
				t.Skip("SQLite statement fusion; other dialects use the bounded commit path")
			}
			after := scaleP09Head(t, f)
			fused, reads := 0, 0
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				if strings.Contains(query, "flow_rj_project") {
					fused++
				}
				if strings.HasPrefix(strings.TrimSpace(query), "SELECT") && strings.Contains(query, "record_journal_dirty") {
					reads++
				}
			})
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			scaleP09Put(t, tx, f.data.Workspace.URLKey, "projects", "one-statement", map[string]string{"id": "one-statement", "name": "Current summary"})
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 10, 2048)
			if err != nil || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != 1 || fused != 1 || reads != 0 {
				t.Fatalf("small commit did not use one statement: fused=%d reads=%d page=%+v err=%v", fused, reads, page, err)
			}
		})
	}
}

func TestScaleP09OffHasNoRowTriggers(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	var count int
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'flow_rj_%' AND tbl_name NOT IN ('record_journal_flush','record_journal_commit','record_journal_commit_v2','record_journal_commit_v3','record_journal_projection_v3','record_journal_context')`
	if f.dialect == "postgres" {
		query = `SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=current_schema() AND trigger_name LIKE 'flow_rj_%' AND event_object_table NOT IN ('record_journal_flush','record_journal_commit')`
	} else if f.dialect == "mysql" {
		query = `SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name LIKE 'flow_rj_%' AND event_object_table NOT IN ('record_journal_flush','record_journal_commit')`
	}
	if err := f.db.QueryRowContext(t.Context(), query).Scan(&count); err != nil || count != 0 {
		t.Fatalf("off retains canonical row triggers: %d %v", count, err)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scaleP09Put(t, tx, f.data.Workspace.URLKey, "projects", "off-project", map[string]string{"id": "off-project", "name": "Untracked"})
	if tx.journalToken != "" || tx.journalBudget != nil {
		t.Fatal("off performs application journal work")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"record_versions", "record_journal_dirty", "metadata_catalog"} {
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=? AND field='projects' AND record_key='off-project'`, f.data.Workspace.URLKey).Scan(&count); err != nil || count != 0 {
			t.Fatalf("off wrote %s: %d %v", table, count, err)
		}
	}
}

func TestScaleP09SmallCommitFaultRollsBackEverySidecar(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	head := scaleP09Head(t, f)
	epoch, err := f.AuthzEpoch(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("p09 fault after journal preparation")
	f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) { tx.writeErr = injected }})
	t.Cleanup(func() { f.db.commitHooks.Store(nil) })
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scaleP09Put(t, tx, workspace, "projects", "small-fault", map[string]string{"id": "small-fault", "name": "Must roll back"})
	if err := tx.Commit(); !errors.Is(err, injected) {
		t.Fatalf("precommit fault not returned: %v", err)
	}
	f.db.commitHooks.Store(nil)
	for _, table := range []string{"workspace_metadata_records", "record_versions", "metadata_catalog", "metadata_catalog_relations"} {
		var count int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=? AND field='projects' AND record_key='small-fault'`, workspace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s survived precommit fault: count=%d err=%v", table, count, err)
		}
	}
	current, err := f.AuthzEpoch(t.Context(), workspaceID)
	if err != nil || current != epoch || scaleP09Head(t, f) != head {
		t.Fatalf("epoch/cursor survived precommit fault: epoch=%s -> %s err=%v", epoch, current, err)
	}
}

func TestScaleP09UnchangedSummaryKeepsRelations(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	for i, name := range []string{"First", "First", "Renamed"} {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		scaleP09Put(t, tx, workspace, "projects", "summary-relations", map[string]any{"id": "summary-relations", "name": name, "teamIds": []string{"one", "two"}, "description": fmt.Sprintf("Private body %d", i)})
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metadata_catalog_relations WHERE workspace_key=? AND field='projects' AND record_key='summary-relations' AND relation='teamIds'`, workspace).Scan(&count); err != nil || count != 2 {
			t.Fatalf("summary update lost relationships: iteration=%d count=%d err=%v", i, count, err)
		}
		var summary []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT summary FROM metadata_catalog WHERE workspace_key=? AND field='projects' AND record_key='summary-relations'`, workspace).Scan(&summary); err != nil || !bytes.Contains(summary, []byte(name)) || bytes.Contains(summary, []byte("Private body")) {
			t.Fatalf("summary update changed projection: iteration=%d summary=%s err=%v", i, summary, err)
		}
	}
}

func TestScaleP09ContentKeysMatchGoEncoding(t *testing.T) {
	f := scaleP09Database(t)
	parameter := "?"
	if f.dialect == "postgres" {
		parameter = "CAST(? AS TEXT)"
	}
	for _, pair := range [][2]string{{"parent", "child"}, {`[],:\\\"`, `<>&`}, {"parent\u2028line", "child\u2029line"}, {"parent\t\n", "child\x01"}} {
		var key string
		if err := f.db.QueryRowContext(t.Context(), "SELECT "+recordJournalContentKeySQL(f.dialect, parameter, parameter), pair[0], pair[1]).Scan(&key); err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal([]string{pair[0], pair[1]})
		if key != string(want) {
			t.Fatalf("SQL content key differs from Go encoding: got=%q want=%q", key, want)
		}
	}
}

func TestScaleP09TimezoneKeepsEpochAndAccessChangesRevoke(t *testing.T) {
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	write := func(timezone, access string) {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		scaleP09Put(t, tx, workspace, "teamSettings", "timezone-epoch", map[string]string{"teamId": "timezone-epoch", "timezone": timezone, "access": access})
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	write("UTC", "public")
	epoch, err := f.AuthzEpoch(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	after := scaleP09Head(t, f)
	write("Asia/Shanghai", "public")
	current, err := f.AuthzEpoch(t.Context(), workspaceID)
	if err != nil || current != epoch {
		t.Fatalf("timezone-only write revoked access: %s -> %s %v", epoch, current, err)
	}
	page, err := f.ReadCommittedChanges(t.Context(), workspaceID, after, 10, 2048)
	if err != nil || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != 1 || page.Transactions[0].Records[0].Version != "2" {
		t.Fatalf("timezone write lost its committed version: %+v %v", page, err)
	}
	write("Asia/Shanghai", "private")
	current, err = f.AuthzEpoch(t.Context(), workspaceID)
	if err != nil || current == epoch {
		t.Fatalf("access change did not revoke the epoch: %s -> %s %v", epoch, current, err)
	}
}

func TestScaleP09SQLiteTextRowsUseByteOffsets(t *testing.T) {
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite TEXT and BLOB storage classes")
	}
	raw, _ := json.Marshal(map[string]string{"id": "text-row", "name": "UTF-8 summary", "description": strings.Repeat("\u6b63\u6587\U0001f642", 10000)})
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects','text-row',0,CAST(? AS TEXT))`, f.data.Workspace.URLKey, raw); err != nil {
		t.Fatal(err)
	}
	var version string
	var size int64
	var summary []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT version,body_bytes,summary FROM metadata_catalog WHERE workspace_key=? AND field='projects' AND record_key='text-row'`, f.data.Workspace.URLKey).Scan(&version, &size, &summary); err != nil {
		t.Fatal(err)
	}
	if version != metadataFingerprint(raw) || size != int64(len(raw)) || bytes.Contains(summary, []byte("\u6b63\u6587")) || !bytes.Contains(summary, []byte("UTF-8 summary")) {
		t.Fatalf("TEXT projection lost bytes or copied its body: version=%s size=%d summary=%s", version, size, summary)
	}
}

func TestScaleP09ContentKeyFitsCanonicalIDLimits(t *testing.T) {
	f := scaleP09Database(t)
	resource, id := strings.Repeat("<", 191), strings.Repeat(">", 191)
	raw, _ := json.Marshal(map[string]string{"id": id, "body": "Small content"})
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,'comment',?,?,?,?)`, f.data.Workspace.URLKey, resource, id, time.Now().UTC().Format(issueRecordTimestamp), raw); err != nil {
		t.Fatal(err)
	}
	key, _ := json.Marshal([]string{resource, id})
	version, deleted, err := f.RecordVersion(t.Context(), f.data.Workspace.URLKey, RecordKey{"comments", string(key)})
	if err != nil || version != "1" || deleted {
		t.Fatalf("valid canonical content IDs lost their version: keyBytes=%d version=%s deleted=%t err=%v", len(key), version, deleted, err)
	}
}

func TestScaleP09InlineKeysAreBoundedAndSpillIsAtomic(t *testing.T) {
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite transaction input")
	}
	after := scaleP09Head(t, f)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 20 {
		id := fmt.Sprintf("inline-%02d", i)
		scaleP09Put(t, tx, f.data.Workspace.URLKey, "projects", id, map[string]string{"id": id, "name": "Inline"})
		if i == 0 {
			var keys, durable int
			if err := tx.QueryRowContext(t.Context(), `SELECT json_array_length(keys),(SELECT COUNT(*) FROM record_journal_dirty WHERE tx_id=?) FROM record_journal_context WHERE id=1`, tx.journalToken).Scan(&keys, &durable); err != nil || keys != 1 || durable != 0 {
				t.Fatalf("small write persisted duplicate dirty indexes: keys=%d durable=%d err=%v", keys, durable, err)
			}
		}
	}
	var keys, durable int
	if err := tx.QueryRowContext(t.Context(), `SELECT json_array_length(keys),(SELECT COUNT(*) FROM record_journal_dirty WHERE tx_id=?) FROM record_journal_context WHERE id=1`, tx.journalToken).Scan(&keys, &durable); err != nil || keys != 16 || durable != 4 {
		t.Fatalf("transaction input exceeded its bound: keys=%d durable=%d err=%v", keys, durable, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 10, 64<<10)
	if err != nil || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != 20 {
		t.Fatalf("spill lost atomic dirty keys: %+v %v", page, err)
	}
}

func TestScaleP09InlineProtocolBypassFailsCoverageAndRepairs(t *testing.T) {
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite transaction input")
	}
	after := scaleP09Head(t, f)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scaleP09Put(t, tx, f.data.Workspace.URLKey, "projects", "inline-bypass", map[string]string{"id": "inline-bypass", "name": "Interrupted protocol"})
	if err := tx.Tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 10, 4096); !errors.Is(err, ErrRecordJournalCoverage) {
		t.Fatalf("unfinished transaction input claimed coverage: %v", err)
	}
	remaining, err := f.RepairRecordJournal(t.Context(), f.data.Workspace.ID)
	if err != nil || remaining {
		t.Fatalf("unfinished transaction input was not recovered: remaining=%t err=%v", remaining, err)
	}
	page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, after, 10, 4096)
	if err != nil || len(page.Transactions) != 1 || !containsString(page.Transactions[0].Invalidations, "workspace") {
		t.Fatalf("recovery lost invalidation: %+v %v", page, err)
	}
}

func TestScaleP09LockedOrderIsReusedAndInvalidatedByWrites(t *testing.T) {
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite single-record order cache")
	}
	workspace := f.data.Workspace.URLKey
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects','cached-order',37,'{"id":"cached-order","name":"Initial"}')`, workspace); err != nil {
		t.Fatal(err)
	}
	reads := 0
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.HasPrefix(query, "SELECT collection_order FROM") {
			reads++
		}
	})
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := lockMetadataRecords(ctx, tx, workspace, "projects", []string{"cached-order"}); err != nil {
		t.Fatal(err)
	}
	changes := []metadataRecordChange{{field: "projects", key: "cached-order", raw: json.RawMessage(`{"id":"cached-order","name":"First"}`)}}
	if err := writeMetadataRecordUpserts(ctx, tx, workspace, changes); err != nil || reads != 0 || changes[0].order != 37 {
		t.Fatalf("locked order was reread or changed: reads=%d order=%d err=%v", reads, changes[0].order, err)
	}
	if _, err := lockMetadataRecords(ctx, tx, workspace, "projects", []string{"cached-order"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET collection_order=77 WHERE workspace_key=? AND field='projects' AND record_key='cached-order'`, workspace); err != nil {
		t.Fatal(err)
	}
	changes[0].raw = json.RawMessage(`{"id":"cached-order","name":"Second"}`)
	if err := writeMetadataRecordUpserts(ctx, tx, workspace, changes); err != nil || reads != 1 || changes[0].order != 77 {
		t.Fatalf("SQL write retained stale order: reads=%d order=%d err=%v", reads, changes[0].order, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP09AccountNoopPreservesTimestampAndWriteBudget(t *testing.T) {
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite account comparison")
	}
	var key, stamp string
	var viewer []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT last_workspace_key,viewer,updated_at FROM account_state WHERE id=1`).Scan(&key, &viewer, &stamp); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `UPDATE account_state SET viewer=CAST(viewer AS TEXT) WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	scaleP09Put(t, tx, f.data.Workspace.URLKey, "projects", "account-noop", map[string]string{"id": "account-noop"})
	before := tx.writtenBytes
	if err := writeAccountMetadata(t.Context(), tx, key, viewer); err != nil || tx.writtenBytes != before {
		t.Fatalf("account no-op consumed the legacy write budget: before=%d after=%d err=%v", before, tx.writtenBytes, err)
	}
	var current string
	if err := tx.QueryRowContext(t.Context(), `SELECT updated_at FROM account_state WHERE id=1`).Scan(&current); err != nil || current != stamp {
		t.Fatalf("account no-op changed timestamp: %s -> %s err=%v", stamp, current, err)
	}
	tx.maxTransactionBytes = tx.writtenBytes
	changed := append(append([]byte(nil), viewer...), ' ')
	if err := writeAccountMetadata(t.Context(), tx, key, changed); !errors.Is(err, ErrTransactionWriteBudget) {
		t.Fatalf("account change bypassed the legacy write budget: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT viewer,updated_at FROM account_state WHERE id=1`).Scan(&stored, &current); err != nil || !bytes.Equal(stored, viewer) || current != stamp {
		t.Fatalf("account change survived budget rollback: err=%v", err)
	}
}

func TestScaleP09EnableRequiresBoundedRepair(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	before, err := f.AuthzEpoch(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scaleP09Put(t, tx, workspace, "projects", "removed-while-off", map[string]string{"id": "removed-while-off"})
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after := scaleP09Head(t, f)
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	// Exercise the same startup transition on the owned store. Other
	// enabled handles must stop trusting the database-wide authority too.
	f.db.journalReady.Store(false)
	if err := f.ensureRecordJournal(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuthzEpoch(t.Context(), f.db, workspaceID); !errors.Is(err, ErrRecordJournalUnavailable) {
		t.Fatalf("enabled peer trusted disabled authority: %v", err)
	}
	tx, err = f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='removed-while-off'`, workspace); err != nil {
		t.Fatal(err)
	}
	for i := range 260 {
		id := fmt.Sprintf("off-project-%03d", i)
		scaleP09Put(t, tx, workspace, "projects", id, map[string]string{"id": id, "name": "Written while off"})
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	if err := f.ensureRecordJournal(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := f.AuthzEpoch(t.Context(), workspaceID)
	if err != nil || current == before {
		t.Fatalf("enable did not revoke old access contexts: %s -> %s %v", before, current, err)
	}
	if _, err := f.ReadCommittedChanges(t.Context(), workspaceID, after, 100, 256<<10); !errors.Is(err, ErrRecordJournalCoverage) {
		t.Fatalf("enable trusted missing history: %v", err)
	}
	if _, _, err := f.RecordVersion(t.Context(), workspace, RecordKey{"projects", "off-project-000"}); !errors.Is(err, ErrRecordJournalCoverage) {
		t.Fatalf("enable trusted missing versions: %v", err)
	}
	passes := 0
	for remaining := true; remaining; {
		passes++
		remaining, err = f.RepairRecordJournal(t.Context(), workspaceID)
		if err != nil || passes > 30 {
			t.Fatalf("activation repair: passes=%d remaining=%t %v", passes, remaining, err)
		}
		if passes == 1 && !remaining {
			t.Fatal("activation repair was not bounded")
		}
	}
	version, deleted, err := f.RecordVersion(t.Context(), workspace, RecordKey{"projects", "removed-while-off"})
	if err != nil || !deleted || version != "2" {
		t.Fatalf("off deletion lost tombstone: %s %t %v", version, deleted, err)
	}
	page, err := f.ReadCommittedChanges(t.Context(), workspaceID, after, 100, 256<<10)
	if err != nil || page.ResyncRequired || len(page.Transactions) == 0 {
		t.Fatalf("repaired journal unavailable: %+v %v", page, err)
	}
	for _, change := range page.Transactions {
		if len(change.Records) != 0 || !containsString(change.Invalidations, "workspace") {
			t.Fatalf("activation invented historical transaction boundaries: %+v", change)
		}
	}
}

func TestScaleP09SmallSummaryMatchesStreaming(t *testing.T) {
	f := scaleP09Database(t)
	inputs := []string{
		` { "id":"p09-projection", "name":"A name", "description":"Secret body", "teamIds":["one","two"], "createdAt":"2026-11-11T10:00:00Z" } `,
		`{"id":"p09-projection","slugId":"example-suffix","milestones":[{"id":"m","name":"Milestone","description":"Secret milestone","milestones":[{"id":"hidden"}]}],"status":{"id":"s","name":"Active","type":"started"}}`,
		`{"id":"p09-projection","owner":null,"domains":null,"scope":"read","archivedAt":"2026-11-11T10:00:00Z"}`,
		`{"teamId":"p09-projection","timezone":"UTC","parentTeamId":"parent"}`,
		`"scalar"`, `null`,
	}
	for _, input := range inputs {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		key := RecordKey{"projects", "p09-projection"}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, f.data.Workspace.URLKey, key.Kind, key.ID, []byte(input)); err != nil {
			t.Fatal(err)
		}
		if input[0] == '"' || input == "null" {
			// The existing scalar path intentionally emits only its key.
			row := recordJournalProjection{Kind: key.Kind, ID: key.ID}
			if err := projectRecordJournalSummary([]byte(input), &row); err != nil || row.Omitted != 1 || !bytes.Contains(row.Summary, []byte(key.ID)) {
				t.Fatalf("scalar projection: %+v %v", row, err)
			}
		} else {
			want, err := f.projectCanonicalMetadata(context.Background(), tx, f.data.Workspace.URLKey, key, int64(len(input)))
			if err != nil {
				t.Fatal(err)
			}
			got := recordJournalProjection{Kind: key.Kind, ID: key.ID}
			if err := projectRecordJournalSummary([]byte(input), &got); err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(got.Summary, &fields); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want.summary, got.Summary) || want.version != got.Version || want.created != got.Created || want.updated != got.Updated || want.archived != got.Archived || !reflect.DeepEqual(metadataCatalogRelations(got.Summary), recordJournalSummaryRelations(fields)) {
				t.Fatalf("summary/relations differ: want=%+v got=%+v", want, got)
			}
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScaleP09ActivationRepairMakesProgressWithinWriteBudget(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	teams := make([]string, 500)
	for i := range teams {
		teams[i] = fmt.Sprintf("grant-%03d", i)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 6 {
		id := fmt.Sprintf("repair-budget-%02d", i)
		scaleP09Put(t, tx, workspace, "projects", id, map[string]any{"id": id, "name": "Large relation summary", "teamIds": teams})
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	if err := f.ensureRecordJournal(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.db.maxTransactionBytes = 600 << 10
	for pass := 0; ; pass++ {
		remaining, err := f.RepairRecordJournal(t.Context(), f.data.Workspace.ID)
		if err != nil || pass >= 50 {
			t.Fatalf("repair stalled under the existing write budget: pass=%d remaining=%t err=%v", pass, remaining, err)
		}
		if !remaining {
			break
		}
	}
	var count int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metadata_catalog_relations WHERE workspace_key=? AND field='projects' AND relation='teamIds' AND record_key LIKE 'repair-budget-%'`, workspace).Scan(&count); err != nil || count != 3000 {
		t.Fatalf("repair lost relationship rows: count=%d err=%v", count, err)
	}
	if _, _, err := f.RecordVersion(t.Context(), workspace, RecordKey{"projects", "repair-budget-05"}); err != nil {
		t.Fatal(err)
	}
}
