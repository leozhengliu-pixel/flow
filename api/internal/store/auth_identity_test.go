package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestLoginExternalIdentityWithoutEmailUsesStableProviderIdentity(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
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
	linked, _, err := repository.LoginExternalIdentity(t.Context(), "saml", "https://idp.example/saml", "employee-10042", "A-10042", "zhang.san@example.com", "Zhang San Updated", "", `{"department":"engineering"}`, true)
	if err != nil || linked.User.ID != first.User.ID {
		t.Fatalf("second provider did not link the existing account: linked=%#v err=%v", linked.User, err)
	}
	identities, err := repository.ListAuthIdentities(t.Context(), first.User.ID)
	if err != nil || len(identities) != 2 {
		t.Fatalf("identities=%#v err=%v", identities, err)
	}
	for _, identity := range identities {
		if identity.ClaimsJSON != "" {
			t.Fatal("identity claims leaked through the account API")
		}
	}
	if err := repository.UnlinkAuthIdentity(t.Context(), first.User.ID, identities[0].ID); err != nil {
		t.Fatal(err)
	}
	identities, err = repository.ListAuthIdentities(t.Context(), first.User.ID)
	if err != nil || len(identities) != 1 {
		t.Fatalf("identities after unlink=%#v err=%v", identities, err)
	}
	if err := repository.UnlinkAuthIdentity(t.Context(), first.User.ID, identities[0].ID); err == nil {
		t.Fatal("the only sign-in method was unlinked")
	}
}

func TestLoginExternalRemainsEmailBased(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	if _, _, err := repository.LoginExternal(t.Context(), "", "Employee", "", true); err == nil {
		t.Fatal("LoginExternal accepted an empty email")
	}
}

func TestLoginExternalIdentityDoesNotAutoJoinWhenDisabled(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "external-membership.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	first, _, err := repository.LoginExternalIdentity(t.Context(), "oidc", "https://idp.example/tenant", "employee-200", "employee-200", "employee.200@example.com", "Employee 200", "", `{}`, true)
	if err != nil {
		t.Fatal(err)
	}
	workspace, ok := repository.BootstrapFor("test-workspace")
	if !ok {
		t.Fatal("test workspace not found")
	}
	if _, err := repository.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspace.Workspace.ID, first.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(t.Context(), `DELETE FROM team_memberships WHERE workspace_id=? AND user_id=?`, workspace.Workspace.ID, first.User.ID); err != nil {
		t.Fatal(err)
	}

	second, _, err := repository.LoginExternalIdentity(t.Context(), "oidc", "https://idp.example/tenant", "employee-200", "employee-200", "employee.200@example.com", "Employee 200", "", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Memberships) != 0 {
		t.Fatalf("disabled auto-provision restored %d workspace memberships", len(second.Memberships))
	}
	var count int
	if err := repository.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspace.Workspace.ID, first.User.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("workspace memberships=%d, want 0", count)
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
	repository, err := OpenSQLiteTestFixture(path)
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
