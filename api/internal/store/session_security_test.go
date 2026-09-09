package store

import (
	"path/filepath"
	"testing"
)

func TestExistingDatabaseMigratesSessionAuthenticationContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.db.ExecContext(t.Context(), `DROP TABLE auth_session_security`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.db.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE version=4`); err != nil {
		t.Fatal(err)
	}
	before := len(repo.Bootstrap().Issues)
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	auth, err := repo.SessionAuthentication(t.Context(), "legacy-session-without-context")
	if err != nil || auth.Provider != "legacy" || auth.MFAVerifiedAt != nil {
		t.Fatalf("legacy session context: %+v, %v", auth, err)
	}
	if len(repo.Bootstrap().Issues) != before {
		t.Fatal("schema upgrade changed issues")
	}
	var applied int
	if err = repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version=4`).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("migration not recorded: %d %v", applied, err)
	}
	if err = repo.migrate(t.Context()); err != nil {
		t.Fatalf("migration not idempotent: %v", err)
	}
}
