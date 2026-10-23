//go:build integration

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// freshExternalDatabase creates an empty database next to the one named by
// FLOW_TEST_DATABASE_URL (the URL's user needs CREATE DATABASE) and returns
// its URL. The database is dropped when the test ends.
func freshExternalDatabase(t *testing.T) (driver, databaseURL string) {
	t.Helper()
	driver, base := strings.ToLower(os.Getenv("FLOW_TEST_DATABASE_DRIVER")), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || base == "" {
		t.Skip("external database configuration is not set")
	}
	if driver == "postgresql" {
		driver = "postgres"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		t.Skip("FLOW_TEST_DATABASE_URL is not a URL")
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
	name := fmt.Sprintf("flow_startup_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		admin.Close()
		t.Skipf("cannot create a scratch database: %v", err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		if driver == "postgres" {
			_, _ = admin.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		}
		if _, err := admin.Exec("DROP DATABASE " + name); err != nil {
			t.Logf("drop scratch database %s: %v", name, err)
		}
	})
	parsed.Path = "/" + name
	return driver, parsed.String()
}

func openConcurrently(t *testing.T, config DatabaseConfig, count int) []*SQLiteStore {
	t.Helper()
	stores := make([]*SQLiteStore, count)
	errs := make([]error, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			stores[index], errs[index] = OpenDatabase(config)
		}()
	}
	close(start)
	wg.Wait()
	for index, store := range stores {
		if store != nil {
			t.Cleanup(func() { store.Close() })
		}
		if errs[index] != nil {
			t.Fatalf("server %d failed to start: %v", index, errs[index])
		}
	}
	return stores
}

func checkMigrationsRecordedOnce(t *testing.T, repository *SQLiteStore) {
	t.Helper()
	var total, distinct, latest int
	if err := repository.db.QueryRowContext(context.Background(), `SELECT COUNT(*), COUNT(DISTINCT version), COALESCE(MAX(CASE WHEN version < 1000 THEN version END), 0) FROM schema_migrations`).Scan(&total, &distinct, &latest); err != nil {
		t.Fatal(err)
	}
	if total == 0 || total != distinct || latest < 14 {
		t.Fatalf("schema_migrations total=%d distinct=%d latest=%d", total, distinct, latest)
	}
	for _, table := range []string{"workspace_states", "auth_users", "review_import_state", "personal_connections", "issue_records"} {
		var rows int
		if err := repository.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&rows); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
}

// Three servers starting at the same moment on a fresh database: one runs
// the schema work, the others wait for it and find the schema current.
func TestExternalDatabaseConcurrentStartupOnFreshDatabase(t *testing.T) {
	driver, databaseURL := freshExternalDatabase(t)
	config := DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2, MigrationLockTimeout: 5 * time.Minute}
	stores := openConcurrently(t, config, 3)
	checkMigrationsRecordedOnce(t, stores[0])
	var workspaces int
	if err := stores[0].db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM workspace_states`).Scan(&workspaces); err != nil {
		t.Fatal(err)
	}
	if workspaces > 1 {
		t.Fatalf("%d workspaces seeded, want at most one", workspaces)
	}
	// The pool may be a single connection: the lock uses its own.
	single := openConcurrently(t, DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 1, MaxIdleConns: 1}, 2)
	checkMigrationsRecordedOnce(t, single[1])
}

// The upgrade case: the database is at an older schema version (the last
// two migrations and their tables are missing) and three servers of the new
// version start together. Each migration runs and is recorded once.
func TestExternalDatabaseConcurrentStartupUpgrade(t *testing.T) {
	driver, databaseURL := freshExternalDatabase(t)
	config := DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2, MigrationLockTimeout: 5 * time.Minute}
	first, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE review_import_items`, `DROP TABLE review_import_state`, `DROP TABLE personal_connections`,
		`DELETE FROM schema_migrations WHERE version IN (13, 14)`,
	} {
		if _, err := first.db.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	first.Close()
	stores := openConcurrently(t, config, 3)
	checkMigrationsRecordedOnce(t, stores[2])
}

// A server that cannot get the lock within its timeout fails with a clear
// error instead of migrating alongside the holder.
func TestExternalDatabaseStartupLockTimeout(t *testing.T) {
	driver, databaseURL := freshExternalDatabase(t)
	sqlDriver, dsn := "pgx", databaseURL
	if driver == "mysql" {
		var err error
		sqlDriver = "mysql"
		if dsn, err = mysqlDSN(databaseURL); err != nil {
			t.Fatal(err)
		}
	}
	backend, err := openStartupLockBackend(context.Background(), driver, sqlDriver, dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	held, err := acquireStartupLock(context.Background(), backend, startupLockOptions{name: "test holder"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MigrationLockTimeout: 2 * time.Second})
	if !errors.Is(err, ErrStartupLockTimeout) {
		held.release()
		t.Fatalf("err = %v, want ErrStartupLockTimeout", err)
	}
	if want := "pid"; driver == "postgres" && !strings.Contains(err.Error(), want) {
		t.Fatalf("timeout error does not name the holder: %v", err)
	}
	t.Logf("timeout error: %v", err)
	held.release()
	// Nothing was created while the lock was held; after release a start
	// succeeds.
	repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	var tables int
	if err := repository.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&tables); err != nil || tables == 0 {
		t.Fatalf("after release: rows=%d err=%v", tables, err)
	}
}
