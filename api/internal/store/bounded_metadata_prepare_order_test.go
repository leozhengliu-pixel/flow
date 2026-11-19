package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/idgen"
)

// preparationDatabase returns the configuration of an empty database of the
// dialect FLOW_TEST_DATABASE_DRIVER/URL names (SQLite without them), dropped
// when the test ends. MySQL scratch databases take the server's default
// collation (utf8mb4_0900_ai_ci on 8.4), as a database created for the app
// does. PostgreSQL ones use a linguistic ICU collation when the server has
// one: canonical key columns then sort case-insensitively first, as on a
// glibc en_US.UTF-8 production database.
func preparationDatabase(t *testing.T) DatabaseConfig {
	t.Helper()
	driver, base := strings.ToLower(os.Getenv("FLOW_TEST_DATABASE_DRIVER")), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || base == "" {
		return DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "prepare.db")}
	}
	if driver == "postgresql" {
		driver = "postgres"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		t.Fatalf("FLOW_TEST_DATABASE_URL is not a URL: %v", err)
	}
	sqlDriver, adminDSN := "pgx", base
	if driver == "mysql" {
		sqlDriver = "mysql"
		if adminDSN, err = mysqlDSN(base); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := sql.Open(sqlDriver, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("flow_prepare_%d", time.Now().UnixNano())
	create := "CREATE DATABASE " + name
	if driver == "mysql" {
		create += " CHARACTER SET utf8mb4"
	}
	if driver == "postgres" {
		if _, err := admin.Exec(create + ` TEMPLATE template0 LOCALE_PROVIDER icu ICU_LOCALE 'en-US' LOCALE 'C'`); err != nil {
			t.Logf("no ICU collation (%v); using the server default", err)
			_, err = admin.Exec(create)
			if err != nil {
				admin.Close()
				t.Fatalf("create scratch database: %v", err)
			}
		}
	} else if _, err := admin.Exec(create); err != nil {
		admin.Close()
		t.Fatalf("create scratch database: %v", err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		if driver == "postgres" {
			_, _ = admin.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		}
		if _, err := admin.Exec("DROP DATABASE " + name); err != nil {
			t.Logf("drop scratch database %s: %v", name, err)
		}
	})
	parsed.Path = "/" + name
	return DatabaseConfig{Driver: driver, URL: parsed.String(), MaxOpenConns: 4, MaxIdleConns: 2}
}

func openPreparationDatabase(t *testing.T, config DatabaseConfig) *SQLiteStore {
	t.Helper()
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func rolloutCollectionShapes(t *testing.T, repo *SQLiteStore, workspace string) map[string]string {
	t.Helper()
	var raw []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	shapes := map[string]string{}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
		t.Fatal(err)
	}
	return shapes
}

// mixedCaseCollections are real metadata collections whose names share a
// lowercase prefix with a name that continues in upper case. Case-insensitive
// collations (MySQL's utf8mb4_0900_ai_ci, PostgreSQL's linguistic locales)
// sort "teams" before "teamSettings"; byte order sorts it after.
var mixedCaseCollections = []string{"teams", "teamSettings", "teamParents", "teamMembers", "labels", "labelGroups", "cycles", "cycleSettings", "projects", "projectTemplates", "projectStatuses"}

func distinctMetadataCollections(t *testing.T, repo *SQLiteStore) int {
	t.Helper()
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM (SELECT DISTINCT workspace_key,field FROM workspace_metadata_records) c`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

type journalVersionKey struct{ workspace, field, id string }

func canonicalMetadataVersions(t *testing.T, repo *SQLiteStore, workspace string) map[journalVersionKey]int64 {
	t.Helper()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT m.field,m.record_key,COALESCE(v.version,0) FROM workspace_metadata_records m LEFT JOIN record_versions v ON v.workspace_key=m.workspace_key AND v.field=m.field AND v.record_key=m.record_key WHERE m.workspace_key=?`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	versions := map[journalVersionKey]int64{}
	for rows.Next() {
		key := journalVersionKey{workspace: workspace}
		var version int64
		if err := rows.Scan(&key.field, &key.id, &version); err != nil {
			t.Fatal(err)
		}
		versions[key] = version
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return versions
}

// `server metadata-prepare` walks every collection of every workspace once.
// MySQL's case-insensitive canonical key columns sorted "teams" before
// "teamSettings" while the byte-ordered catalog checkpoints and Go sorted it
// after, and the walk went back and forth between the two forever. Several
// workspaces with mixed-case collections larger than one repair batch now
// prepare to ready: the journal's activation walk versions every canonical
// key exactly once, the catalog pass visits each collection exactly once, and
// a second run has nothing to repair.
func TestBoundedMetadataRolloutPrepareMixedCaseCollections(t *testing.T) {
	rolloutEnv(t)
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	config := preparationDatabase(t)
	repo := openPreparationDatabase(t, config)
	workspaces := rolloutWorkspaces(t, repo, 3)
	// The app's own collections include teams and teamSettings. Raw rows go
	// into the root's array collections that carry no policy.
	extra := []string{}
	for _, field := range mixedCaseCollections {
		if shape, ok := rolloutCollectionShapes(t, repo, workspaces[0])[field]; ok && shape == "array" && metadataResidencyFields()[field] != "policy" {
			extra = append(extra, field)
		}
	}
	if !slices.Contains(extra, "labels") || !slices.Contains(extra, "labelGroups") {
		t.Fatalf("collections for raw rows: %v", extra)
	}
	for index, workspace := range workspaces {
		for _, field := range extra {
			count := 3
			if index == 0 && (field == "labels" || field == "labelGroups") {
				// More than one repair batch, so the walk's cursor crosses
				// between the two collections.
				count = 140
			}
			for i := range count {
				id := fmt.Sprintf("%s_%03d", strings.ToLower(field), i)
				rolloutPutRaw(t, repo, workspace, field, id, []byte(fmt.Sprintf(`{"id":%q,"name":"Prepare %d","teamId":"t"}`, id, i)))
			}
		}
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo = openPreparationDatabase(t, config)
	defer func() { repo.Close() }()
	before := map[journalVersionKey]int64{}
	for _, workspace := range workspaces {
		for key, version := range canonicalMetadataVersions(t, repo, workspace) {
			before[key] = version
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	journal, err := repo.RepairRecordJournalPass(ctx)
	if err != nil || journal.Repaired != len(workspaces) || len(journal.Incomplete) != 0 {
		t.Fatalf("journal repair %+v: %v", journal, err)
	}
	for _, workspace := range workspaces {
		for key, version := range canonicalMetadataVersions(t, repo, workspace) {
			if want := before[key] + 1; version != want {
				t.Errorf("activation walk left %s/%s/%s at version %d, want %d (each key exactly once)", key.workspace, key.field, key.id, version, want)
			}
		}
	}
	collections := distinctMetadataCollections(t, repo)
	for _, pair := range [][2]string{{"teams", "teamSettings"}, {"labels", "labelGroups"}} {
		var both int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(DISTINCT field) FROM workspace_metadata_records WHERE workspace_key=? AND field IN (?,?)`, workspaces[0], pair[0], pair[1]).Scan(&both); err != nil || both != 2 {
			t.Fatalf("workspace lacks %v canonical rows (%d, %v)", pair, both, err)
		}
	}
	for run := range 2 {
		prepared, err := repo.PrepareBoundedMetadata(ctx)
		if err != nil {
			t.Fatalf("run %d: %v (%+v)", run, err, prepared)
		}
		if !prepared.Ready || prepared.Catalog.Collections != collections || prepared.Catalog.Completed != collections || len(prepared.Catalog.Skipped) != 0 || prepared.Journal.Workspaces != 0 {
			t.Fatalf("run %d: %+v, want %d collections each visited once and ready", run, prepared, collections)
		}
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	repo = openPreparationDatabase(t, config)
}

// The pass's collection walk returns every collection with canonical rows,
// then every checkpoint left without them, each exactly once, whatever the
// collation of the canonical key columns.
func TestCatalogCollectionWalkVisitsEachOnce(t *testing.T) {
	rolloutEnv(t)
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	repo := openPreparationDatabase(t, preparationDatabase(t))
	defer repo.Close()
	ctx := t.Context()
	want := map[[2]string]bool{}
	workspaces := []string{"walk-a", "walk-B", "walk-b2", "Walk-c"}
	for _, workspace := range workspaces {
		for _, field := range mixedCaseCollections {
			rolloutPutRaw(t, repo, workspace, field, "r1", []byte(`{"id":"r1"}`))
			want[[2]string{workspace, field}] = true
		}
	}
	// Checkpoints with and without canonical rows, interleaved in both orders.
	for _, checkpoint := range [][2]string{{"walk-a", "teamSettings"}, {"walk-a", "teams"}, {"walk-B", "labels"}, {"walk-a", "teamZ"}, {"walk-B", "cycleS"}, {"walk-gone", "teams"}, {"walk-gone", "teamSettings"}, {"Walk-c", "aOrphan"}} {
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO metadata_catalog_collections(workspace_key,field,last_record_key,started,phase,omissions,digest,revision) VALUES(?,?,'',0,2,0,'','')`, checkpoint[0], checkpoint[1]); err != nil {
			t.Fatal(err)
		}
		want[checkpoint] = true
	}
	seen := map[[2]string]int{}
	order := []string{}
	cursor := metadataCollectionCursor{}
	for steps := 0; ; steps++ {
		if steps > 2*len(want) {
			t.Fatalf("walk did not end after %d steps: %v", steps, order)
		}
		next, err := repo.nextMetadataCatalogCollection(ctx, cursor)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		key := [2]string{next.Workspace, next.Field}
		seen[key]++
		order = append(order, next.Workspace+"/"+next.Field)
		cursor = next
	}
	for key := range want {
		if seen[key] != 1 {
			t.Errorf("%s/%s visited %d times, want once; walk %v", key[0], key[1], seen[key], order)
		}
	}
	for key, count := range seen {
		if !want[key] {
			t.Errorf("unexpected collection %s/%s (%d)", key[0], key[1], count)
		}
	}
	if t.Failed() {
		return
	}
	// Collections with canonical rows come first.
	split := slices.IndexFunc(order, func(v string) bool {
		return strings.HasSuffix(v, "teamZ") || strings.HasPrefix(v, "walk-gone/") || strings.HasSuffix(v, "cycleS") || strings.HasSuffix(v, "aOrphan")
	})
	if split != len(order)-5 {
		t.Errorf("orphan checkpoints are not last: %v", order)
	}
}

// Journal keys no workspace owns any more (or never did) can only be
// discarded: no repair can publish them. A bulk copy of the auth tables that
// loads memberships before workspace_states leaves @authz keys under the
// empty workspace key; a workspace an old binary deleted leaves its own
// keys. Prepare discards both, reports them, and ends ready.
func TestBoundedMetadataRolloutPrepareOrphanJournalScopes(t *testing.T) {
	rolloutEnv(t)
	config := preparationDatabase(t)
	repo := openPreparationDatabase(t, config)
	defer func() { repo.Close() }()
	workspaces := rolloutWorkspaces(t, repo, 2)
	ctx := t.Context()
	prepared, err := repo.PrepareBoundedMetadata(ctx)
	if err != nil || !prepared.Ready {
		t.Fatalf("first prepare %+v: %v", prepared, err)
	}
	// Tokenless writers: a bulk copy of memberships for workspaces whose
	// roots are not there yet, and an old binary deleting a workspace.
	var userID string
	if err := repo.db.QueryRowContext(ctx, `SELECT id FROM auth_users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	tokenless := func(query string, args ...any) {
		t.Helper()
		// The raw pool: no journal transaction, as an older binary writes.
		if _, err := repo.db.DB.ExecContext(ctx, rewriteSQL(query, repo.dialect), args...); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		tokenless(`INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at) VALUES(?,?,'member','active','2026-01-01T00:00:00Z')`, idgen.New("copied-ws-"), userID)
	}
	tokenless(`DELETE FROM workspace_metadata_records WHERE workspace_key=?`, workspaces[1])
	tokenless(`DELETE FROM workspace_states WHERE workspace_key=?`, workspaces[1])
	var orphanKeys int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM record_journal_dirty WHERE workspace_key IN ('',?)`, workspaces[1]).Scan(&orphanKeys); err != nil {
		t.Fatal(err)
	}
	if orphanKeys == 0 {
		t.Fatal("the tokenless writes left no journal keys to clean up")
	}
	for run := range 2 {
		prepared, err = repo.PrepareBoundedMetadata(ctx)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if !prepared.Ready || prepared.UncoveredWorkspaces != 0 || len(prepared.Uncovered) != 0 {
			t.Fatalf("run %d: %+v", run, prepared)
		}
		if run == 0 && prepared.Journal.Discarded != 2 || run == 1 && prepared.Journal.Discarded != 0 {
			t.Fatalf("run %d discarded %d orphan scopes: %+v", run, prepared.Journal.Discarded, prepared.Journal)
		}
	}
	var left int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM record_journal_dirty`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d journal keys left (%v)", left, err)
	}
	// A key a live workspace cannot repair is reported by scope.
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO record_journal_coverage(workspace_key,phase,last_field,last_key) VALUES(?,0,'','')`, workspaces[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO record_journal_dirty(tx_id,workspace_key,field,record_key,source) VALUES('stale',?,'@authz','u1','workspace_memberships')`, workspaces[0]); err != nil {
		t.Fatal(err)
	}
	result := BoundedMetadataPreparation{}
	if err := repo.boundedMetadataReadiness(ctx, &result); err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.UncoveredWorkspaces != 1 || len(result.Uncovered) != 1 {
		t.Fatalf("readiness %+v", result)
	}
	gap := result.Uncovered[0]
	if gap.Workspace != workspaces[0] || !gap.WorkspaceExists || !gap.ActivationWalk || len(gap.Pending) != 1 || gap.Pending[0] != (RecordJournalPendingScope{Field: "@authz", Source: "workspace_memberships", Keys: 1}) {
		t.Fatalf("uncovered %+v", gap)
	}
}
