package store

import (
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
)

func metadataEventGroup(event string) string {
	switch {
	case strings.HasPrefix(event, "agent."), strings.HasPrefix(event, "release_pipeline."):
		return "hot"
	case strings.HasPrefix(event, "migration."), strings.HasPrefix(event, "integration.token_"), strings.HasPrefix(event, "oauth_sync_group."):
		return "bookkeeping"
	default:
		return "records"
	}
}

func metadataScaleProbe(t *testing.T) (*sql.DB, func() int64) {
	t.Helper()
	admin, err := sql.Open("mysql", os.Getenv("FLOW_TEST_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	if _, err := admin.Exec(`CREATE TABLE IF NOT EXISTS metadata_write_probe(table_name VARCHAR(191),record_kind VARCHAR(191))`); err != nil {
		t.Fatal(err)
	}
	rows, err := admin.Query(`SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE' AND TABLE_NAME<>'metadata_write_probe'`)
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
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for i, table := range tables {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			name := fmt.Sprintf("metadata_probe_%d_%s", i, op)
			if _, err := admin.Exec("DROP TRIGGER IF EXISTS " + name); err != nil {
				t.Fatal(err)
			}
			kind := "''"
			if table == "workspace_content_records" {
				kind = "NEW.kind"
				if op == "DELETE" {
					kind = "OLD.kind"
				}
			}
			if _, err := admin.Exec(fmt.Sprintf("CREATE TRIGGER %s AFTER %s ON `%s` FOR EACH ROW INSERT INTO metadata_write_probe VALUES('%s',%s)", name, op, table, table, kind)); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for i := range tables {
			for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
				if _, err := admin.Exec(fmt.Sprintf("DROP TRIGGER IF EXISTS metadata_probe_%d_%s", i, op)); err != nil {
					t.Error(err)
				}
			}
		}
	})
	return admin, func() int64 {
		var n int64
		if err := admin.QueryRow(`SELECT COUNT(*) FROM mysql.general_log WHERE user_host LIKE 'flow[flow]%' AND command_type IN ('Query','Execute')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
}

func TestMetadataEventScaleMeasure(t *testing.T) {
	mode, group := os.Getenv("FLOW_METADATA_MEASURE"), os.Getenv("FLOW_METADATA_GROUP")
	if mode != "full" && mode != "metadata" {
		t.Skip("opt-in metadata event gate")
	}
	repo := contractScaleStore(t)
	if repo.dialect != "mysql" {
		t.Fatal("the measurement requires throwaway MySQL")
	}
	ctx := t.Context()
	var issues, content int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_records WHERE workspace_key=?`, contractScaleWorkspace).Scan(&issues); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_content_records WHERE workspace_key=? AND kind IN ('comment','activity')`, contractScaleWorkspace).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if issues != 75000 || content != 150000 {
		t.Fatal("run the 75k seed first")
	}
	// Reset metadata outside the measured interval: both paths start identical.
	if err := repo.MutateWorkspace(ctx, contractScaleWorkspace, "loop.created", "metadata_seed", nil, func(data *domain.Bootstrap) error { seedMetadataEvents(data); return nil }); err != nil {
		t.Fatal(err)
	}
	coordinator, err := coordination.Open(ctx, coordination.Config{Mode: "standalone", URL: os.Getenv("FLOW_TEST_REDIS_URL"), Prefix: "metadata-measure"})
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	repo.SetWorkspaceCoordinator(coordinator)
	admin, queryCount := metadataScaleProbe(t)
	if _, err := admin.Exec(`TRUNCATE metadata_write_probe`); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	stop, peakResult := make(chan struct{}), make(chan uint64, 1)
	go func() {
		var stats runtime.MemStats
		var peak uint64
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
	before, begin := queryCount(), time.Now()
	var lock time.Duration
	n := 0
	for _, test := range metadataEventCases() {
		if metadataEventGroup(test.event) != group {
			continue
		}
		n++
		traceCtx, trace := startMutationTrace(ctx)
		event := test.event
		if mode == "full" {
			// The suffix bypasses only the new allowlist. Unlike the global
			// full switch it preserves the original delta persistence algorithm.
			event += ".baseline"
		}
		err = repo.dispatchMutation(withMutationAggregateID(traceCtx, test.aggregate), contractScaleWorkspace, event, nil, func(data *domain.Bootstrap) (string, error) { test.apply(data); return test.aggregate, nil })
		lock += trace.lockHeld
		if err != nil {
			break
		}
		if mode == "metadata" && trace.path != "metadata" {
			t.Errorf("%s fell back to %s", test.event, trace.path)
		}
	}
	elapsed := time.Since(begin)
	close(stop)
	peak := <-peakResult
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("empty measurement group")
	}
	queries := queryCount() - before
	var written, unrelated int
	if err := admin.QueryRow(`SELECT COUNT(*) FROM metadata_write_probe`).Scan(&written); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(`SELECT COUNT(*) FROM metadata_write_probe WHERE table_name='issue_records' OR (table_name='workspace_content_records' AND record_kind IN ('comment','activity','notification','delivery'))`).Scan(&unrelated); err != nil {
		t.Fatal(err)
	}
	if unrelated != 0 {
		t.Fatalf("rewrote %d issue/content rows", unrelated)
	}
	t.Logf("METADATA_MEASURE group=%s mode=%s events=%d queries=%d row_operations=%d issue_content_rows=%d lock_ms=%.3f elapsed_ms=%.3f peak_heap_mib=%.2f", group, mode, n, queries, written, unrelated, float64(lock)/float64(time.Millisecond), float64(elapsed)/float64(time.Millisecond), float64(peak)/(1<<20))
}
