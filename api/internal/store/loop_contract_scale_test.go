package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
)

const contractScaleWorkspace = "contract-scale"

func contractScaleStore(t *testing.T) *SQLiteStore {
	t.Helper()
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Fatal("the contract gate requires a throwaway external database")
	}
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

// Run only against the gate's throwaway database. The seed is shared with the
// separate-process API test, which verifies the persisted state afterwards.
func TestLoopContractScaleSeed(t *testing.T) {
	if os.Getenv("FLOW_LOOP_CONTRACT_GATE") != "seed" {
		t.Skip("opt-in contract write gate")
	}
	repo := contractScaleStore(t)
	ctx := t.Context()
	if _, err := repo.db.ExecContext(ctx, `DROP TABLE IF EXISTS contract_write_probe`); err != nil {
		t.Fatal(err)
	}
	if repo.HasWorkspace(contractScaleWorkspace) {
		if err := repo.DeleteWorkspace(ctx, contractScaleWorkspace); err != nil {
			t.Fatal(err)
		}
	}
	data, err := repo.CreateWorkspace(ctx, "Contract scale", contractScaleWorkspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(ctx, contractScaleWorkspace, "loop.created", "contract_loop", nil, func(next *domain.Bootstrap) error {
		next.Loops = append(next.Loops, domain.Loop{ID: "contract_loop", Name: "Summary", Instructions: "Reply with a summary.", Status: "published", Level: "workspace", Creator: next.Viewer, OwnerID: next.Viewer.ID})
		for i := range 3 {
			next.LoopVersions = append(next.LoopVersions, domain.LoopVersion{ID: fmt.Sprintf("contract_version_%d", i), LoopID: "contract_loop", Definition: domain.LoopDefinition{Name: "Summary", Instructions: "Reply with a summary."}})
		}
		for i := range 2 {
			next.Drafts = append(next.Drafts, domain.Draft{ID: fmt.Sprintf("contract_draft_%d", i), UserID: next.Viewer.ID, Type: "issue", Title: "Seed draft"})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	count := 75000
	if value, err := strconv.Atoi(os.Getenv("FLOW_LOOP_CONTRACT_ISSUES")); err == nil && value > 0 {
		count = value
	}
	now := time.Now().UTC()
	for start := 0; start < count; start += 250 {
		tx, err := repo.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		args := []any{}
		values := []string{}
		content := []contentRecordRow{}
		for i := start; i < min(start+250, count); i++ {
			id := fmt.Sprintf("contract_issue_%06d", i)
			issue := domain.Issue{ID: id, Identifier: fmt.Sprintf("SCL-%d", i+1), Number: i + 1, Title: "Contract scale issue", Description: strings.Repeat("work item context ", 24), Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: now, UpdatedAt: now, Version: 1}
			row, err := issueRecordValues(contractScaleWorkspace, issue)
			if err != nil {
				t.Fatal(err)
			}
			args = append(args, row...)
			values = append(values, issueRecordValuesSQL)
			for kind, item := range map[string]any{
				"comment":  domain.Comment{ID: "comment_" + id, Body: "Scale comment with unchanged discussion content.", User: data.Viewer, CreatedAt: now},
				"activity": domain.ActivityEvent{ID: "activity_" + id, Type: "issue.created", Actor: data.Viewer, CreatedAt: now},
			} {
				raw, _ := json.Marshal(item)
				record, err := buildContentRecordRow(contractScaleWorkspace, kind, id, raw)
				if err != nil {
					t.Fatal(err)
				}
				content = append(content, record)
			}
		}
		if _, err := tx.ExecContext(ctx, issueRecordInsert+strings.Join(values, ","), args...); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := writeContentRecordRows(ctx, tx, content); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE issue_collection_counts SET total=? WHERE workspace_key=?`, count, contractScaleWorkspace); err != nil {
		t.Fatal(err)
	}
	t.Logf("CONTRACT_SEED issues=%d comments=%d activities=%d", count, count, count)
}

func TestLoopContractScaleMeasure(t *testing.T) {
	mode := os.Getenv("FLOW_LOOP_CONTRACT_GATE")
	if mode != "full" && mode != "narrow" && mode != "repeat" && mode != "stale" {
		t.Skip("opt-in contract write gate")
	}
	repo := contractScaleStore(t)
	if repo.dialect != "mysql" {
		t.Skip("query and row instrumentation uses the throwaway MySQL server")
	}
	ctx := t.Context()
	admin, err := sql.Open("mysql", os.Getenv("FLOW_TEST_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	// Trigger observations count affected data rows, not a guessed number of
	// rewritten issues or an upsert's driver-specific RowsAffected value.
	if _, err := admin.Exec(`CREATE TABLE IF NOT EXISTS contract_write_probe(table_name VARCHAR(191),operation VARCHAR(8))`); err != nil {
		t.Fatal(err)
	}
	rows, err := admin.Query(`SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE' AND TABLE_NAME<>'contract_write_probe'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	rows.Close()
	for i, table := range tables {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			name := fmt.Sprintf("contract_probe_%d_%s", i, op)
			if _, err := admin.Exec("DROP TRIGGER IF EXISTS " + name); err != nil {
				t.Fatal(err)
			}
			if _, err := admin.Exec(fmt.Sprintf("CREATE TRIGGER %s AFTER %s ON `%s` FOR EACH ROW INSERT INTO contract_write_probe VALUES('%s','%s')", name, op, table, table, op)); err != nil {
				t.Fatal(err)
			}
		}
	}
	defer func() {
		for i := range tables {
			for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
				_, _ = admin.Exec(fmt.Sprintf("DROP TRIGGER IF EXISTS contract_probe_%d_%s", i, op))
			}
		}
	}()
	var issueCount, contentCount int
	_ = admin.QueryRow(`SELECT COUNT(*) FROM issue_records WHERE workspace_key=?`, contractScaleWorkspace).Scan(&issueCount)
	_ = admin.QueryRow(`SELECT COUNT(*) FROM workspace_content_records WHERE workspace_key=?`, contractScaleWorkspace).Scan(&contentCount)
	if issueCount < 75000 || contentCount < 150000 {
		t.Fatal("run the 75k seed first")
	}
	if rawURL := os.Getenv("FLOW_TEST_REDIS_URL"); rawURL != "" {
		coordinator, err := coordination.Open(ctx, coordination.Config{Mode: "standalone", URL: rawURL, Prefix: "contract-gate"})
		if err != nil {
			t.Fatal(err)
		}
		defer coordinator.Close()
		repo.SetWorkspaceCoordinator(coordinator)
	}
	queryCount := func() int64 {
		var count int64
		if err := admin.QueryRow(`SELECT COUNT(*) FROM mysql.general_log WHERE user_host LIKE 'flow[flow]%' AND command_type IN ('Query','Execute')`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if _, err := admin.Exec(`TRUNCATE contract_write_probe`); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	peak := stats.HeapAlloc
	stop := make(chan struct{})
	peakResult := make(chan uint64, 1)
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			runtime.ReadMemStats(&stats)
			peak = max(peak, stats.HeapAlloc)
			select {
			case <-stop:
				peakResult <- peak
				return
			case <-ticker.C:
			}
		}
	}()
	queriesBefore := queryCount()
	traceCtx, trace := startMutationTrace(ctx)
	begin := time.Now()
	apply := func(data *domain.Bootstrap) (string, error) {
		if mode == "stale" {
			// The Loop text changed and matching versions are already cached.
			return "contract_loop", nil
		}
		if mode == "repeat" {
			// Copying the same stored value exercises the comparison path.
			data.Loops[0].OutputContract = cloneBootstrap(domain.Bootstrap{Loops: data.Loops}).Loops[0].OutputContract
			return "contract_loop", nil
		}
		contract := &domain.LoopOutputContract{Source: "model", InstructionsHash: mode, ReadOnly: true, ExtractedAt: &begin}
		for i := range data.Loops {
			if data.Loops[i].ID == "contract_loop" {
				data.Loops[i].OutputContract = contract
			}
		}
		for i := range data.LoopVersions {
			if data.LoopVersions[i].LoopID == "contract_loop" {
				data.LoopVersions[i].OutputContract = contract
			}
		}
		return "contract_loop", nil
	}
	// Call the old dispatcher directly for the baseline, without forcing the
	// legacy persistence algorithm (which would exaggerate the old row writes).
	if mode == "full" {
		err = repo.mutateFull(traceCtx, contractScaleWorkspace, "loop.output_contract", nil, apply)
	} else {
		err = repo.dispatchMutation(withMutationAggregateID(traceCtx, "contract_loop"), contractScaleWorkspace, "loop.output_contract", nil, apply)
	}
	elapsed := time.Since(begin)
	close(stop)
	peak = <-peakResult
	if err != nil {
		t.Fatal(err)
	}
	queries := queryCount() - queriesBefore
	var written, unrelated int
	if err := admin.QueryRow(`SELECT COUNT(*) FROM contract_write_probe`).Scan(&written); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(`SELECT COUNT(*) FROM contract_write_probe WHERE table_name IN ('issue_records','workspace_content_records')`).Scan(&unrelated); err != nil {
		t.Fatal(err)
	}
	if (mode == "repeat" || mode == "stale") && written != 0 {
		t.Fatalf("no-op wrote %d rows", written)
	}
	if unrelated != 0 {
		t.Fatalf("contract save rewrote %d issue/content rows", unrelated)
	}
	t.Logf("CONTRACT_MEASURE mode=%s queries=%d row_operations=%d issue_content_rows=%d lock_ms=%.3f elapsed_ms=%.3f peak_heap_mib=%.2f issues=%d content=%d", mode, queries, written, unrelated, float64(trace.lockHeld)/float64(time.Millisecond), float64(elapsed)/float64(time.Millisecond), float64(peak)/(1<<20), issueCount, contentCount)
}
