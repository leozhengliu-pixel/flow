package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP05CatalogExplainIndexes(t *testing.T) {
	s, workspace := scaleP05Database(t)
	previous := 0
	for _, count := range []int{23210, 232100} {
		scaleP05SeedPlanCatalog(t, s, workspace, previous, count)
		previous = count
		for _, table := range []string{"metadata_catalog", "metadata_catalog_relations"} {
			statement := "ANALYZE " + table
			if s.dialect == "mysql" {
				statement = "ANALYZE TABLE " + table
			} else if s.dialect == "postgres" {
				// Fresh inserts have no all-visible heap pages. Model a
				// maintained catalog so covering probes can use index-only scans.
				statement = "VACUUM (ANALYZE) " + table
			}
			rows, err := s.db.QueryContext(t.Context(), statement)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		t.Run(fmt.Sprintf("rows-%d", count), func(t *testing.T) {
			for _, order := range []string{"collection", "createdAt", "updatedAt"} {
				for _, archived := range []string{"all", "false", "true"} {
					for _, after := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/archived-%s/after-%t", order, archived, after), func(t *testing.T) {
							q := PageRequest{Kind: "projects", Order: order}
							query := ProjectRecordQuery{Admin: true, Archived: archived}
							cursor := metadataCatalogCursor{}
							if after {
								cursor = metadataCatalogCursor{Schema: 2, ID: fmt.Sprintf("catalog-%06d", count/2), Ordinal: int64(count / 2), Sort: time.Date(2026, 1, 1, 0, 8, 20, 0, time.UTC).Format(issueRecordTimestamp)}
							}
							statement, args, err := metadataCatalogPageStatement(workspace, q, query, cursor)
							if err != nil {
								t.Fatal(err)
							}
							index := map[string]string{"collection": "metadata_catalog_order_idx", "createdAt": "metadata_catalog_created_idx", "updatedAt": "metadata_catalog_updated_idx"}[order]
							// MySQL prefers the collection index for the full row,
							// filtering archived values without an extra sort.
							if order == "collection" && archived != "all" && s.dialect != "mysql" {
								index = "metadata_catalog_archived_idx"
							}
							scaleP05AssertPlan(t, s, statement, args, index)
						})
					}
				}
			}
			for _, archived := range []int{0, 1} {
				t.Run(fmt.Sprintf("archive-window-count-%d", archived), func(t *testing.T) {
					// Exercise the archive/order prefix on one bounded window.
					// An unbounded common-value count may favor the primary key.
					start := count - MetadataCatalogBackfillBatch
					statement := `SELECT COUNT(*) FROM metadata_catalog WHERE workspace_key=? AND field='projects' AND archived=? AND collection_order>=?`
					args := []any{workspace, archived, start}
					want := (count-1)/5 - (start-1)/5
					if archived == 0 {
						want = MetadataCatalogBackfillBatch - want
					}
					var actual int
					if err := s.db.QueryRowContext(t.Context(), statement, args...).Scan(&actual); err != nil || actual != want {
						t.Fatalf("archive window fixture: archived=%d rows=%d want=%d err=%v", archived, actual, want, err)
					}
					scaleP05AssertPlan(t, s, statement, args, "metadata_catalog_archived_idx")
				})
			}
			t.Run("team-relation", func(t *testing.T) {
				scaleP05AssertPlan(t, s, `SELECT record_key FROM metadata_catalog_relations WHERE workspace_key=? AND field='projects' AND relation='teamIds' AND value_hash=?`, []any{workspace, metadataFingerprint([]byte("team-3"))}, "metadata_catalog_relation_idx")
			})
		})
	}
}

// Populate only summaries: this checks planner choices at the actual catalog
// sizes without generating gigabytes of unrelated canonical record bodies.
func scaleP05SeedPlanCatalog(t *testing.T, s *SQLiteStore, workspace string, first, end int) {
	t.Helper()
	const batchSize = 128
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for first < end {
		stop := min(first+batchSize, end)
		values, relations := []string{}, []string{}
		args, relationArgs := []any{}, []any{}
		for i := first; i < stop; i++ {
			id, team := fmt.Sprintf("catalog-%06d", i), fmt.Sprintf("team-%d", i%128)
			created, updated := base.Add(time.Duration(i*17%1009)*time.Second), base.Add(time.Duration(i*29%1013)*time.Second)
			project := domain.Project{ID: id, Name: "Project " + id, SlugID: "project-" + id, Summary: strings.Repeat("summary ", 32), TeamIDs: []string{team}, CreatedAt: created, UpdatedAt: updated}
			archived := 0
			if i%5 == 0 {
				archived, project.ArchivedAt = 1, &created
			}
			raw, err := json.Marshal(ProjectListProjection(project))
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, "(?,'projects',?,?,?,0,?,0,?,?,?)")
			args = append(args, workspace, id, i, strings.Repeat("a", 64), raw, created.Format(issueRecordTimestamp), updated.Format(issueRecordTimestamp), archived)
			relations = append(relations, "(?,'projects',?,'teamIds',?,?)")
			relationArgs = append(relationArgs, workspace, id, metadataFingerprint([]byte(team)), team)
		}
		tx, err := s.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO metadata_catalog(workspace_key,field,record_key,collection_order,version,body_bytes,summary,omitted,created_at,updated_at,archived) VALUES `+strings.Join(values, ","), args...); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO metadata_catalog_relations(workspace_key,field,record_key,relation,value_hash,value) VALUES `+strings.Join(relations, ","), relationArgs...); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		first = stop
	}
}

func scaleP05AssertPlan(t *testing.T, s *SQLiteStore, statement string, args []any, index string) {
	t.Helper()
	if s.dialect == "sqlite" {
		rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+statement, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		plan := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail + "\n"
		}
		if err := rows.Err(); err != nil || !strings.Contains(plan, index) || strings.Contains(plan, "TEMP B-TREE") || strings.Contains(plan, "workspace_metadata_records") {
			t.Fatalf("index=%s plan=%s err=%v", index, plan, err)
		}
		t.Log(plan)
		return
	}
	prefix := "EXPLAIN (FORMAT JSON) "
	if s.dialect == "mysql" {
		prefix = "EXPLAIN FORMAT=JSON "
	}
	trace := s.dialect == "mysql" && index == "metadata_catalog_archived_idx"
	if trace {
		if _, err := s.db.ExecContext(t.Context(), `SET optimizer_trace='enabled=on'`); err != nil {
			t.Fatal(err)
		}
		defer s.db.ExecContext(t.Context(), `SET optimizer_trace='enabled=off'`)
	}
	var raw []byte
	if err := s.db.QueryRowContext(t.Context(), prefix+statement, args...).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	usedIndex, sorted, canonical := false, false, false
	var inspect func(any)
	inspect = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if name, ok := node["Node Type"].(string); ok && strings.Contains(name, "Sort") {
				sorted = true
			}
			if filesort, _ := node["using_filesort"].(bool); filesort {
				sorted = true
			}
			if node["Index Name"] == index || node["key"] == index {
				usedIndex = true
			}
			if node["Relation Name"] == "workspace_metadata_records" || node["table_name"] == "workspace_metadata_records" {
				canonical = true
			}
			for _, child := range node {
				inspect(child)
			}
		case []any:
			for _, child := range node {
				inspect(child)
			}
		}
	}
	inspect(plan)
	if !usedIndex || sorted || canonical {
		if trace {
			scaleP05LogMySQLIndexChoice(t, s)
		}
		t.Fatalf("index=%s used=%t sorted=%t canonical=%t plan=%s", index, usedIndex, sorted, canonical, raw)
	}
	t.Log(string(raw))
}

func scaleP05LogMySQLIndexChoice(t *testing.T, s *SQLiteStore) {
	t.Helper()
	var trace []byte
	if err := s.db.QueryRowContext(t.Context(), `SELECT TRACE FROM information_schema.OPTIMIZER_TRACE`).Scan(&trace); err != nil {
		t.Logf("optimizer trace: %v", err)
	} else {
		t.Logf("optimizer trace: %s", trace)
	}
	rows, err := s.db.QueryContext(t.Context(), `SELECT COLUMN_NAME,SEQ_IN_INDEX,COLLATION,CARDINALITY,IS_VISIBLE FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='metadata_catalog' AND INDEX_NAME='metadata_catalog_archived_idx' ORDER BY SEQ_IN_INDEX`)
	if err != nil {
		t.Logf("archive index definition: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var column, collation, visible string
		var sequence int
		var cardinality sql.NullInt64
		if err := rows.Scan(&column, &sequence, &collation, &cardinality, &visible); err != nil {
			t.Logf("archive index definition: %v", err)
			return
		}
		t.Logf("archive index: position=%d column=%s collation=%s cardinality=%v visible=%s", sequence, column, collation, cardinality, visible)
	}
	if err := rows.Err(); err != nil {
		t.Logf("archive index definition: %v", err)
	}
}
