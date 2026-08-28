package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestLoginExternalIdentityWithoutEmailUsesStableProviderIdentity(t *testing.T) {
	repository, err := OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	first, _, err := repository.LoginExternalIdentity(t.Context(), "oidc", "https://idp.example/tenant", "A-10042", "A-10042", "", "Zhang San", "", `{"employee_id":"A-10042"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.User.ID == "" || first.User.Email != "" || first.User.DisplayName != "Zhang San" {
		t.Fatalf("unexpected first login user: %#v", first.User)
	}

	second, _, err := repository.LoginExternalIdentity(t.Context(), "oidc", "https://idp.example/tenant", "A-10042", "A-10042", "zhang.san@example.com", "Zhang San Updated", "", `{ "employee_id": "A-10042" }`, true)
	if err != nil {
		t.Fatal(err)
	}
	if second.User.ID != first.User.ID || second.User.Email != "zhang.san@example.com" || second.User.DisplayName != "Zhang San Updated" {
		t.Fatalf("identity did not resolve and sync existing user: first=%#v second=%#v", first.User, second.User)
	}

	var count int
	if err := repository.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM auth_identities WHERE provider=? AND issuer=? AND subject=?`, "oidc", "https://idp.example/tenant", "A-10042").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("identity rows=%d, want 1", count)
	}
}

func TestLoginExternalRemainsEmailBased(t *testing.T) {
	repository, err := OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	if _, _, err := repository.LoginExternal(t.Context(), "", "Employee", "", true); err == nil {
		t.Fatal("LoginExternal accepted an empty email")
	}
}

func TestOpenSQLiteMigratesLegacyRequiredEmailColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE auth_users (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, name TEXT NOT NULL, display_name TEXT NOT NULL, avatar_url TEXT NOT NULL DEFAULT '', password_hash TEXT NOT NULL, email_verified_at TEXT, active INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	var required int
	if err := repository.db.QueryRowContext(t.Context(), `SELECT "notnull" FROM pragma_table_info('auth_users') WHERE name='email'`).Scan(&required); err != nil {
		t.Fatal(err)
	}
	if required != 0 {
		t.Fatalf("legacy email column remains NOT NULL: %d", required)
	}
}
