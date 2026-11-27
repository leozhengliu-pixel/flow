package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func captureArgs(n int) []any {
	args := make([]any, n)
	for i := range args {
		args[i] = "v" + strconv.Itoa(i)
	}
	return args
}

// Every complete journal-table write literal in the package must be one
// application capture analyses; the fragments below are completed at run
// time and covered by TestRecordCaptureDynamicStatements.
func TestRecordCaptureAnalysesEveryWriterLiteral(t *testing.T) {
	fragments := map[string]bool{
		// "UPDATE domain_events SET " + columns: domain events are
		// captured on insert only.
		"integration_oauth_tokens.go:scrubIntegrationTokenEvents": true,
		// "UPDATE issue_records r JOIN (" ... "SET r.list_data=...": the
		// list projection is not a captured column.
		"issue_list_projection.go:migrateIssueListProjectionPass": true,
		// "... VALUES(?,?,?,?," + staged value + ")": see "staged row".
		"metadata_sql_stage_accounting.go:statementQuery": true,
		// The batch values and CASE arms are assembled at runtime.
		"legacy_array_reorder.go:flushUpserts": true,
		"legacy_array_reorder.go:flushMoves":   true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	analysed := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || path == "record_journal.go" && fn.Name.Name == "journalWriteStatement" {
				continue
			}
			ast.Inspect(fn, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(literal.Value)
				if err != nil || !journalWriteStatement(text) {
					return true
				}
				st := parseCaptureStatement(text, captureArgs(4096))
				if st.unknown != "" || st.verb == captureInsert && len(st.tuples) == 0 && st.selectSQL == "" {
					if !fragments[path+":"+fn.Name.Name] {
						t.Errorf("%s:%s: capture cannot analyse %q (%s)", path, fn.Name.Name, text, st.unknown)
					}
					return true
				}
				if captureTables[st.table] == nil {
					t.Errorf("%s: unknown capture table %q", path, st.table)
				}
				analysed++
				return true
			})
		}
	}
	if analysed < 100 {
		t.Fatalf("analysed only %d writer literals", analysed)
	}
}

func TestRecordCaptureDynamicStatements(t *testing.T) {
	for _, test := range []struct {
		name, query string
		verb        captureVerb
		table       string
		where       string
		filter      int
		assigned    []string
		tuples      int
		conflict    captureConflict
		limited     bool
	}{
		{name: "metadata upsert", query: recordJournalPreparedWrites[0], verb: captureInsert, table: "workspace_metadata_records", tuples: 1, conflict: conflictUpdate, assigned: []string{"data"}},
		{name: "suggestion batch", query: `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?),(?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`,
			verb: captureInsert, table: "workspace_metadata_records", tuples: 2, conflict: conflictUpdate, assigned: []string{"collection_order", "data"}},
		{name: "suggestion batch delete", query: `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key IN (?,?)`,
			verb: captureDelete, table: "workspace_metadata_records", filter: 4},
		{name: "suggestion batch move", query: `UPDATE workspace_metadata_records SET collection_order=CASE record_key WHEN ? THEN ? WHEN ? THEN ? ELSE collection_order END WHERE workspace_key=? AND field=? AND record_key IN (?,?)`,
			verb: captureUpdate, table: "workspace_metadata_records", filter: 4, assigned: []string{"collection_order"}},
		{name: "event insert", query: recordJournalPreparedWrites[1], verb: captureInsert, table: "domain_events", tuples: 1},
		{name: "api key use", query: "UPDATE workspace_metadata_records SET data=JSON_SET(CONVERT(data USING utf8mb4),'$.lastUsedAt',?) WHERE workspace_key=? AND field=? AND record_key=? AND JSON_UNQUOTE(JSON_EXTRACT(data,'$.revokedAt')) IS NULL",
			verb: captureUpdate, table: "workspace_metadata_records", filter: 3, assigned: []string{"data"}},
		{name: "rename move", query: "UPDATE issue_records SET workspace_key=? WHERE workspace_key=?", verb: captureUpdate, table: "issue_records", filter: 1, assigned: []string{"workspace_key"}},
		{name: "rename conflicts", query: "DELETE o FROM workspace_metadata_records AS o JOIN workspace_metadata_records AS c ON c.workspace_key=? AND c.field=o.field AND c.record_key=o.record_key WHERE o.workspace_key=?",
			verb: captureDelete, table: "workspace_metadata_records", filter: 2},
		{name: "purge batch", query: "DELETE FROM workspace_content_records WHERE workspace_key=? LIMIT 500", verb: captureDelete, table: "workspace_content_records", filter: 1, limited: true},
		{name: "list projection", query: "UPDATE issue_records r JOIN (SELECT id FROM issue_records WHERE workspace_key=? LIMIT 10) p ON p.id=r.id SET r.list_data=? WHERE r.workspace_key=?",
			verb: captureUpdate, table: "issue_records", filter: 2, assigned: []string{"list_data"}},
		{name: "team ancestors", query: `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) SELECT workspace_id,?,user_id,'member',MIN(joined_at) FROM team_memberships WHERE workspace_id=? AND team_id IN (?) AND user_id IN (SELECT user_id FROM workspace_memberships WHERE workspace_id=team_memberships.workspace_id AND role<>'guest' AND status='active') GROUP BY workspace_id,user_id ON CONFLICT(workspace_id,team_id,user_id) DO NOTHING`,
			verb: captureInsert, table: "team_memberships", conflict: conflictNothing},
		{name: "notification batch", query: "UPDATE workspace_content_records SET data=JSON_SET(data,'$.readAt',?,'$.updatedAt',?),next_attempt_at=? WHERE workspace_key=? AND kind='notification' AND owner_id=? AND id IN (?,?)",
			verb: captureUpdate, table: "workspace_content_records", filter: 4, assigned: []string{"data", "next_attempt_at"}},
		{name: "issue index tuples", query: "INSERT INTO issue_permission_records(workspace_key,issue_id,subject_type,subject_id,role) VALUES (?,?,?,?,?),(?,?,?,?,?) ON CONFLICT DO NOTHING",
			verb: captureInsert, table: "issue_permission_records", tuples: 2, conflict: conflictNothing},
		{name: "mysql ignore", query: "INSERT IGNORE INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,'member','active',?,NULL)",
			verb: captureInsert, table: "workspace_memberships", tuples: 1, conflict: conflictNothing},
		{name: "staged row", query: "INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,(SELECT CAST(CONCAT((SELECT chunk FROM bounded_metadata_write_chunks WHERE operation_id=bounded_metadata_write_stage.operation_id AND part=0 AND seq=0)) AS BINARY) FROM bounded_metadata_write_stage WHERE operation_id=?)) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data",
			verb: captureInsert, table: "workspace_metadata_records", tuples: 1, conflict: conflictUpdate, assigned: []string{"collection_order", "data"}},
		{name: "staged root", query: "UPDATE workspace_states SET workspace_id=?,data=(SELECT CAST(CONCAT((SELECT chunk FROM bounded_metadata_write_chunks WHERE operation_id=bounded_metadata_write_stage.operation_id AND part=0 AND seq=0)) AS BINARY) FROM bounded_metadata_write_stage WHERE operation_id=?),updated_at=? WHERE workspace_key=?",
			verb: captureUpdate, table: "workspace_states", filter: 1, assigned: []string{"workspace_id", "data", "updated_at"}},
		{name: "quoted placeholder", query: "UPDATE workspace_states SET data=? WHERE workspace_key=? AND data<>'?'", verb: captureUpdate, table: "workspace_states", filter: 1, assigned: []string{"data"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := parseCaptureStatement(test.query, captureArgs(32))
			if st.unknown != "" || st.verb != test.verb || st.table != test.table || len(st.tuples) != test.tuples || st.conflict != test.conflict || st.limited != test.limited {
				t.Fatalf("analysis %+v", st)
			}
			if test.verb != captureInsert && len(st.filterArgs) != test.filter {
				t.Fatalf("filter arguments %v, want %d", st.filterArgs, test.filter)
			}
			for _, column := range test.assigned {
				if _, ok := st.assigned[column]; !ok {
					t.Fatalf("assigned %v lacks %s", st.assigned, column)
				}
			}
			if test.verb == captureUpdate && len(st.assigned) != len(test.assigned) {
				t.Fatalf("assigned %v", st.assigned)
			}
		})
	}
	// Bound values map to their positions.
	st := parseCaptureStatement("UPDATE workspace_states SET data=?,workspace_key=? WHERE workspace_key=?", []any{"d", "new", "old"})
	if st.assigned["workspace_key"].value != "new" || !slices.Equal(st.filterArgs, []any{"old"}) {
		t.Fatalf("bound values %+v", st)
	}
	st = parseCaptureStatement("INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'settings',?,0,?)", []any{"w", "k", "d"})
	if st.tuples[0][0].value != "w" || st.tuples[0][1].value != "settings" || st.tuples[0][2].value != "k" || st.tuples[0][3].value != "0" {
		t.Fatalf("tuple values %+v", st.tuples)
	}
	for _, query := range []string{
		"DELETE FROM workspace_states",
		"DELETE FROM issue_records AS o USING issue_records AS c WHERE o.workspace_key=?",
		"INSERT INTO issue_records SELECT * FROM issue_records",
	} {
		if parseCaptureStatement(query, captureArgs(4)).unknown == "" {
			t.Errorf("analysed an unsupported shape: %s", query)
		}
	}
}
