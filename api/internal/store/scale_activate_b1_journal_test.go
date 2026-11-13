package store

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestScaleP09ActivateB1PointCommitMatchesGeneric(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite commit plan; other dialects retain their existing path")
	}
	workspace := f.data.Workspace.URLKey
	for _, operation := range []string{"create", "body", "rename", "timezone", "spill", "delete", "oversize-key", "authz-key"} {
		t.Run(operation, func(t *testing.T) {
			head := scaleP09Head(t, f)
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if operation == "timezone" {
				scaleP09Put(t, tx, workspace, "teamSettings", "b1-timezone", map[string]string{"teamId": "b1-timezone", "timezone": "Asia/Shanghai", "access": "public"})
			} else if operation == "delete" {
				if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='b1-project'`, workspace); err != nil {
					t.Fatal(err)
				}
			} else if operation == "oversize-key" {
				scaleP09Put(t, tx, workspace, "projects", strings.Repeat("key", 24000), map[string]string{"id": "short-body-id", "name": "Bounded delta"})
			} else if operation == "authz-key" {
				scaleP09Put(t, tx, workspace, "@authz", "b1-authority", map[string]string{"id": "b1-authority"})
			} else {
				name := "Point project"
				if operation == "rename" || operation == "spill" {
					name = "Point project renamed"
				}
				scaleP09Put(t, tx, workspace, "projects", "b1-project", map[string]any{"id": "b1-project", "name": name, "description": operation, "teamIds": []string{"one", "two"}, "previousSlugIds": []string{"old-point"}})
				if operation == "spill" {
					if err := tx.materializeInlineJournal(); err != nil {
						t.Fatal(err)
					}
					scaleP09Put(t, tx, workspace, "projects", "b1-project", map[string]any{"id": "b1-project", "name": name, "description": "latest body", "teamIds": []string{"one", "two"}, "previousSlugIds": []string{"old-point"}})
					scaleP09Put(t, tx, workspace, "initiatives", "b1-initiative", map[string]any{"id": "b1-initiative", "name": "Related initiative", "projectIds": []string{"b1-project"}})
				}
			}
			scaleP09Event(t, f, tx, []byte(`{"activation":"b1"}`))
			created := time.Now().UTC().Format(time.RFC3339Nano)
			queued, err := json.Marshal([]map[string]any{{"Workspace": workspace, "Kind": "metadata", "Flush": true}})
			if err != nil {
				t.Fatal(err)
			}
			bundles := []any{}
			for _, query := range []string{recordJournalSQLCommitData, recordJournalPointCommitData} {
				var raw []byte
				if err := tx.QueryRowContext(t.Context(), `SELECT json(data) FROM (`+query+`)`, tx.journalToken, created, queued).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var bundle struct {
					Created         string
					CacheRevisions  int
					QueuedRevisions any
					Workspaces      []struct {
						Key, ID string
						Events  []string
						Change  CommittedChange
						Catalog []recordJournalProjection
					}
				}
				if err := json.Unmarshal(raw, &bundle); err != nil {
					t.Fatal(err)
				}
				for i := range bundle.Workspaces {
					for j := range bundle.Workspaces[i].Catalog {
						row := &bundle.Workspaces[i].Catalog[j]
						slices.SortFunc(row.Relations, compareRecordJournalPointRelation)
					}
				}
				bundles = append(bundles, bundle)
			}
			if !reflect.DeepEqual(bundles[0], bundles[1]) {
				t.Fatalf("%s point bundle differs from generic: generic=%+v point=%+v", operation, bundles[0], bundles[1])
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			page, err := f.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, head, 10, 64<<10)
			if err != nil || len(page.Transactions) != 1 {
				t.Fatalf("commit missing from journal: page=%+v err=%v", page, err)
			}
			want := 1
			if operation == "spill" {
				want = 2
			} else if operation == "oversize-key" || operation == "authz-key" {
				want = 0
			}
			if len(page.Transactions[0].Records) != want {
				t.Fatalf("committed %d deltas, want %d", len(page.Transactions[0].Records), want)
			}
		})
	}
}

func compareRecordJournalPointRelation(a, b recordJournalRelation) int {
	for _, pair := range [][2]string{{a.Name, b.Name}, {a.Hash, b.Hash}, {a.Value, b.Value}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func TestScaleP09ActivateB1PointCommitUsesIndexes(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite commit plan")
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `WITH RECURSIVE items(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM items WHERE i<2000) INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) SELECT ?,'projects',printf('b1-plan-%04d',i),i,json_object('id',printf('b1-plan-%04d',i),'name','Indexed point') FROM items`, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, statistics := range []string{"unanalysed", "analysed"} {
		t.Run(statistics, func(t *testing.T) {
			if statistics == "analysed" {
				if _, err := f.db.ExecContext(t.Context(), `ANALYZE`); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := f.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+recordJournalPointCommitSQL, "point-plan", "2026-11-12T00:00:00Z", []byte(`[]`))
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				t.Logf("%d/%d %s", id, parent, detail)
				for _, alias := range []string{"m", "c", "v"} {
					if detail == "SCAN "+alias || strings.HasPrefix(detail, "SCAN "+alias+" ") {
						t.Errorf("point commit scans a canonical/catalog/version table: %s", detail)
					}
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
