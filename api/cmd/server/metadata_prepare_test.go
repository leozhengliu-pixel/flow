package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// metadataPrepareDatabase is the database the command test prepares: a
// SQLite file, or an empty scratch database next to the one
// FLOW_TEST_DATABASE_DRIVER/URL names (dropped when the test ends), created
// with the server's default character set and collation as an operator's
// database for the app is.
func metadataPrepareDatabase(t *testing.T) store.DatabaseConfig {
	t.Helper()
	driver, base := strings.ToLower(os.Getenv("FLOW_TEST_DATABASE_DRIVER")), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || base == "" {
		return store.DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "prepare.db")}
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
		sqlDriver, adminDSN = "mysql", parsed.User.String()+"@tcp("+parsed.Host+")/"
	}
	admin, err := sql.Open(sqlDriver, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("flow_prepare_cmd_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
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
	return store.DatabaseConfig{Driver: driver, URL: parsed.String(), MaxOpenConns: 4, MaxIdleConns: 2}
}

func metadataPrepareWorkspaces(t *testing.T, repo *store.SQLiteStore, count int) {
	t.Helper()
	user, token, err := repo.Register(t.Context(), "Prepare owner", idgen.New("prepare-")+"@example.test", "prepare-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	for i := range count {
		if _, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), user), fmt.Sprintf("Prepare %d", i), idgen.New("prepare-"), "us"); err != nil {
			t.Fatal(err)
		}
	}
}

// `server metadata-prepare` takes a database whose workspaces predate the
// record journal (every one lacks writer coverage, as on the 2520-workspace
// copy) and workspaces created with the journal on, as a database created
// through the app has, to a state a bounded start accepts: it repairs the
// journal, completes the catalog of every collection (the app's teams and
// teamSettings among them, which a case-insensitive MySQL collation sorts
// differently from bytes), reports ready and exits 0; run again it has
// nothing to repair. It runs on SQLite and on the database
// FLOW_TEST_DATABASE_DRIVER/URL names.
func TestMetadataPrepareCommandReadiesBoundedStart(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	config := metadataPrepareDatabase(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	metadataPrepareWorkspaces(t, repo, 3)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	if repo, err = store.OpenDatabase(config); err != nil {
		t.Fatal(err)
	}
	metadataPrepareWorkspaces(t, repo, 2)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_DATABASE_DRIVER", config.Driver)
	t.Setenv("FLOW_DATABASE_PATH", config.Path)
	t.Setenv("FLOW_DATABASE_URL", config.URL)

	var stdout, stderr bytes.Buffer
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	if code := metadataPrepare(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "FLOW_RECORD_JOURNAL=on") {
		t.Fatalf("journal off: exit %d, stderr %s", code, stderr.String())
	}

	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	// An explicit bounded start with automatic preparation disabled still
	// refuses incomplete coverage.
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	if bounded, err := store.OpenDatabase(config); err == nil {
		_ = bounded.Close()
		t.Fatal("bounded start accepted workspaces without journal coverage")
	}
	// The command prepares flag-off even when the environment says on.
	for run := range 2 {
		stdout.Reset()
		stderr.Reset()
		done := make(chan int, 1)
		go func() { done <- metadataPrepare(nil, &stdout, &stderr) }()
		var code int
		select {
		case code = <-done:
		case <-time.After(5 * time.Minute):
			t.Fatalf("run %d did not finish", run)
		}
		if code != 0 {
			t.Fatalf("run %d: exit %d, stderr %s, report %s", run, code, stderr.String(), stdout.String())
		}
		var report store.BoundedMetadataPreparation
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("report %q: %v", stdout.String(), err)
		}
		if !report.Ready || report.Catalog.Collections == 0 || report.Catalog.Completed != report.Catalog.Collections || len(report.Catalog.Skipped) != 0 || len(report.Uncovered) != 0 {
			t.Fatalf("run %d report %+v", run, report)
		}
		if run == 0 && report.Journal.Repaired < 3 || run == 1 && report.Journal.Workspaces != 0 {
			t.Fatalf("run %d journal %+v", run, report.Journal)
		}
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	bounded, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatalf("bounded start after metadata-prepare: %v", err)
	}
	if err := bounded.Close(); err != nil {
		t.Fatal(err)
	}
}
