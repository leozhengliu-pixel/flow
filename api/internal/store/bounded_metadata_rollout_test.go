package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/idgen"
)

// rolloutEnv is the flag-off rollout state: hydration and journal on.
func rolloutEnv(t *testing.T) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
}

// rolloutWorkspaces registers one owner and creates count workspaces.
func rolloutWorkspaces(t *testing.T, repo *SQLiteStore, count int) []string {
	t.Helper()
	user, token, err := repo.Register(t.Context(), "Rollout owner", idgen.New("rollout-")+"@example.test", "rollout-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	keys := make([]string, 0, count)
	for i := range count {
		data, err := repo.CreateWorkspace(ContextWithActor(t.Context(), user), fmt.Sprintf("Rollout %d", i), idgen.New("rollout-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, data.Workspace.URLKey)
	}
	return keys
}

func rolloutPutRaw(t *testing.T, repo *SQLiteStore, workspace, field, id string, value any) {
	t.Helper()
	tx, err := repo.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, field, id, value); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func rolloutCatalogPhase(t *testing.T, repo *SQLiteStore, workspace, field string) int {
	t.Helper()
	phase := -1
	err := repo.db.QueryRowContext(t.Context(), `SELECT phase FROM metadata_catalog_collections WHERE workspace_key=? AND field=?`, workspace, field).Scan(&phase)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("checkpoint %s/%s: %v", workspace, field, err)
	}
	return phase // -1 without a checkpoint
}

// Older writers stored some canonical rows as SQLite TEXT. SQLite's LENGTH
// counts the characters of a TEXT value, so a row with CJK text measured
// shorter than its bytes and the catalog's byte reader stopped mid-document:
// "invalid canonical JSON", and the shadow worker stopped for good. The
// backfill now measures bytes, the pass rewrites such rows as BLOBs of the
// same bytes, and a row that really is not JSON skips only its collection.
func TestBoundedRolloutCatalogTextRowsAndSkips(t *testing.T) {
	rolloutEnv(t)
	// The rows predate the journal, as on the copy that hit this (with the
	// journal on, its capture refuses a row that is not JSON at all). A
	// flag-off store refuses to open on such a row, so it arrives while the
	// store is open, as an old replica's write would.
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	path := filepath.Join(t.TempDir(), "text.db")
	repo, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	workspaces := rolloutWorkspaces(t, repo, 2)
	workspace, other := workspaces[0], workspaces[1]
	body := strings.Repeat("会话记录：日本語のテキストと한국어 문장. ", 900) // > 64 KiB of bytes, ~3x its characters
	session := map[string]any{"id": "agent_session_cjk", "title": "智能体会话 ✓", "createdAt": "2026-11-01T08:00:00Z", "updatedAt": "2026-11-02T08:00:00Z", "transcript": body}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	rolloutPutRaw(t, repo, workspace, "agentSessions", "agent_session_cjk", string(raw))
	rolloutPutRaw(t, repo, workspace, "agentSessions", "agent_session_ascii", `{"id":"agent_session_ascii","title":"plain","createdAt":"2026-11-01T08:00:00Z"}`)
	rolloutPutRaw(t, repo, other, "agentSessions", "agent_session_broken", []byte(`{"id":"agent_session_broken","title":`))
	var storage string
	if err := repo.db.QueryRowContext(t.Context(), `SELECT typeof(data) FROM workspace_metadata_records WHERE workspace_key=? AND field='agentSessions' AND record_key='agent_session_cjk'`, workspace).Scan(&storage); err != nil || storage != "text" {
		t.Fatalf("fixture row is stored as %q (%v), want text", storage, err)
	}

	// The backfill alone reads the TEXT row by its byte length.
	for range 64 {
		progress, err := repo.BackfillMetadataCatalog(t.Context(), workspace, "agentSessions")
		if err != nil {
			t.Fatalf("backfill of a TEXT row: %v", err)
		}
		if progress.Complete {
			break
		}
	}
	sum := sha256.Sum256(raw)
	var version, summary string
	var bodyBytes int64
	if err := repo.db.QueryRowContext(t.Context(), `SELECT version,CAST(summary AS TEXT),body_bytes FROM metadata_catalog WHERE workspace_key=? AND field='agentSessions' AND record_key='agent_session_cjk'`, workspace).Scan(&version, &summary, &bodyBytes); err != nil {
		t.Fatal(err)
	}
	if version != hex.EncodeToString(sum[:]) || bodyBytes != int64(len(raw)) || !strings.Contains(summary, "智能体会话 ✓") {
		t.Fatalf("catalog row version %s bytes %d summary %s; want the hash and size of the stored bytes", version, bodyBytes, summary)
	}

	// The pass normalises the row, completes every collection it can and
	// reports the one it cannot instead of stopping.
	report, err := repo.metadataCatalogPass(t.Context(), catalogRevisit)
	if err != nil {
		t.Fatal(err)
	}
	if report.Normalized != 2 {
		t.Fatalf("normalized %d rows, want the two TEXT rows", report.Normalized)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Workspace != other || report.Skipped[0].Field != "agentSessions" || !strings.Contains(report.Skipped[0].Error, "agentSessions/agent_session_broken") {
		t.Fatalf("skipped %+v, want only the broken row's collection", report.Skipped)
	}
	if report.Completed != report.Collections-1 {
		t.Fatalf("completed %d of %d collections", report.Completed, report.Collections)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT typeof(data) FROM workspace_metadata_records WHERE workspace_key=? AND field='agentSessions' AND record_key='agent_session_cjk'`, workspace).Scan(&storage); err != nil || storage != "blob" {
		t.Fatalf("row is stored as %q after the pass (%v), want blob", storage, err)
	}
	var stored []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='agentSessions' AND record_key='agent_session_cjk'`, workspace).Scan(&stored); err != nil || string(stored) != string(raw) {
		t.Fatalf("normalisation changed the stored bytes (%v)", err)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT version FROM metadata_catalog WHERE workspace_key=? AND field='agentSessions' AND record_key='agent_session_cjk'`, workspace).Scan(&version); err != nil || version != hex.EncodeToString(sum[:]) {
		t.Fatalf("version after normalisation %s (%v)", version, err)
	}
	if phase := rolloutCatalogPhase(t, repo, workspace, "agentSessions"); phase != 2 {
		t.Fatalf("normalised collection phase %d", phase)
	}
	if phase := rolloutCatalogPhase(t, repo, other, "agentSessions"); phase == 2 {
		t.Fatal("the collection with the broken row is reported complete")
	}
	catalogs, err := repo.MetadataCatalogCompleteness(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	incomplete := map[string]int{}
	for _, catalog := range catalogs {
		incomplete[catalog.Workspace] = catalog.Incomplete
	}
	if incomplete[workspace] != 0 || incomplete[other] != 1 {
		t.Fatalf("completeness %v, want only the broken collection incomplete", incomplete)
	}
	// The worker counts the skip, finishes its pass and keeps running; it
	// stops only when cancelled.
	logs := captureSlog(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- repo.RunMetadataCatalogBackfill(ctx) }()
	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(logs.String(), "metadata catalog pass finished") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	line := logs.String()
	if !strings.Contains(line, "level=WARN msg=\"metadata catalog pass finished\"") || !strings.Contains(line, "skipped=1") || strings.Contains(line, other) {
		t.Fatalf("worker log:\n%s", line)
	}
	select {
	case err := <-done:
		t.Fatalf("worker stopped: %v", err)
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("worker ended with %v", err)
	}

	// Removing the broken row lets the next pass finish the collection.
	if _, err := repo.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='agentSessions' AND record_key='agent_session_broken'`, other); err != nil {
		t.Fatal(err)
	}
	report, err = repo.metadataCatalogPass(t.Context(), catalogRevisit)
	if err != nil || len(report.Skipped) != 0 || report.Normalized != 0 || report.Completed != report.Collections {
		t.Fatalf("pass after the fix: %+v %v", report, err)
	}

	// With the journal on (the rollout state), normalising a TEXT row is an
	// ordinary journaled write and the preparation ends ready.
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	if repo, err = OpenSQLite(path); err != nil {
		t.Fatal(err)
	}
	rolloutPutRaw(t, repo, other, "agentSessions", "agent_session_cjk_later", strings.Replace(string(raw), "agent_session_cjk", "agent_session_cjk_later", 1))
	prepared, err := repo.PrepareBoundedMetadata(t.Context())
	if err != nil || !prepared.Ready || len(prepared.Catalog.Skipped) != 0 || len(prepared.Journal.Incomplete) != 0 || prepared.Catalog.Normalized != 1 {
		t.Fatalf("prepare with the journal on: %+v %v", prepared, err)
	}
}

// Turning the journal on over existing workspaces leaves each one's writer
// coverage to a bounded repair, which nothing ran in production. The repair
// pass (worker and metadata-prepare) finishes it, resumes a partial repair
// and finds nothing to do on a second run.
func TestBoundedRolloutJournalRepairPass(t *testing.T) {
	rolloutEnv(t)
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	path := filepath.Join(t.TempDir(), "repair.db")
	repo, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	workspaces := rolloutWorkspaces(t, repo, 5)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	uncovered := func() int {
		count := 0
		for _, workspace := range workspaces {
			if err := checkRecordJournalCoverage(t.Context(), repo.db, workspace); errors.Is(err, ErrRecordJournalCoverage) {
				count++
			} else if err != nil {
				t.Fatal(err)
			}
		}
		return count
	}
	if got := uncovered(); got != len(workspaces) {
		t.Fatalf("%d of %d workspaces lack coverage after enabling the journal, want all", got, len(workspaces))
	}
	// A partial repair of the first workspace, as an interrupted pass leaves.
	var id string
	if err := repo.db.QueryRowContext(t.Context(), `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`, workspaces[0]).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RepairRecordJournal(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	report, err := repo.RepairRecordJournalPass(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Repaired != report.Workspaces || len(report.Incomplete) != 0 || report.Workspaces < len(workspaces)-1 {
		t.Fatalf("repair pass %+v", report)
	}
	if got := uncovered(); got != 0 {
		t.Fatalf("%d workspaces still lack coverage after the pass", got)
	}
	again, err := repo.RepairRecordJournalPass(t.Context())
	if err != nil || again.Workspaces != 0 {
		t.Fatalf("second pass %+v (%v), want nothing to repair", again, err)
	}
	prepared, err := repo.PrepareBoundedMetadata(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Ready || prepared.UncoveredWorkspaces != 0 || prepared.IncompleteCollections != 0 || len(prepared.Catalog.Skipped) != 0 {
		t.Fatalf("prepare %+v", prepared)
	}
}

// The production catalog pass runs the same bounded steps back to back that
// the step-by-step completion runs, so it leaves identical catalog state.
func TestBoundedRolloutFastCatalogEqualsStepwise(t *testing.T) {
	rolloutEnv(t)
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.db")
	repo, err := OpenSQLite(seed)
	if err != nil {
		t.Fatal(err)
	}
	workspaces := rolloutWorkspaces(t, repo, 6)
	for i, workspace := range workspaces {
		for j := range 300 {
			rolloutPutRaw(t, repo, workspace, "projects", fmt.Sprintf("project-%03d", j), []byte(fmt.Sprintf(`{"id":"project-%03d","name":"Project %d of %d","priority":%d,"labelIds":["l%d","l%d"],"createdAt":"2026-10-%02dT09:00:00Z","description":"%s"}`, j, j, i, j%5, j%7, j%11, 1+j%28, strings.Repeat("x", 4000+j))))
		}
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	dump := func(path string, complete func(*SQLiteStore) error) (map[string][]string, time.Duration) {
		scaleP21SW557CopyDatabase(t, seed, path)
		repo, err := OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		defer repo.Close()
		began := time.Now()
		if err := complete(repo); err != nil {
			t.Fatal(err)
		}
		took := time.Since(began)
		tables := map[string][]string{}
		for table, query := range map[string]string{
			"metadata_catalog":             `SELECT workspace_key,field,record_key,collection_order,version,body_bytes,CAST(summary AS TEXT),omitted,created_at,updated_at,archived FROM metadata_catalog ORDER BY 1,2,3`,
			"metadata_catalog_relations":   `SELECT workspace_key,field,record_key,relation,value_hash,value FROM metadata_catalog_relations ORDER BY 1,2,3,4,5`,
			"metadata_catalog_collections": `SELECT workspace_key,field,last_record_key,started,phase,omissions,digest,revision FROM metadata_catalog_collections ORDER BY 1,2`,
			"record_versions":              `SELECT workspace_key,field,record_key,version,deleted FROM record_versions ORDER BY 1,2,3`,
		} {
			rows, err := repo.db.QueryContext(t.Context(), query)
			if err != nil {
				t.Fatal(err)
			}
			columns, _ := rows.Columns()
			for rows.Next() {
				values := make([]any, len(columns))
				pointers := make([]any, len(columns))
				for i := range values {
					pointers[i] = &values[i]
				}
				if err := rows.Scan(pointers...); err != nil {
					t.Fatal(err)
				}
				tables[table] = append(tables[table], fmt.Sprint(values...))
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				t.Fatal(err)
			}
		}
		return tables, took
	}
	stepwise, slow := dump(filepath.Join(dir, "stepwise.db"), func(repo *SQLiteStore) error { return repo.CompleteMetadataCatalogForTesting(t.Context()) })
	pass, fast := dump(filepath.Join(dir, "pass.db"), func(repo *SQLiteStore) error {
		report, err := repo.metadataCatalogPass(t.Context(), catalogRevisit)
		if err == nil && (len(report.Skipped) != 0 || report.Completed != report.Collections) {
			err = fmt.Errorf("pass %+v", report)
		}
		return err
	})
	if len(stepwise["metadata_catalog"]) < 6*300 || len(stepwise["metadata_catalog_collections"]) == 0 {
		t.Fatalf("stepwise completion cataloged %d records", len(stepwise["metadata_catalog"]))
	}
	for table := range stepwise {
		if !reflect.DeepEqual(stepwise[table], pass[table]) {
			t.Fatalf("%s differs between the stepwise completion (%d rows) and the pass (%d rows)", table, len(stepwise[table]), len(pass[table]))
		}
	}
	t.Logf("stepwise completion %v, production pass %v", slow, fast)
}
