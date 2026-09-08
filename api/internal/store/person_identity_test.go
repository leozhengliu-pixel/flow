package store

import (
	"path/filepath"
	"testing"
)

func TestMemberDirectoryIncludesEnterpriseUserIDWithoutEmail(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	first, _, err := repository.LoginExternalIdentity(t.Context(), "oidc", "https://idp.example", "EMP-1001", "same.login.name", "", "Same name", "", `{}`, true)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := repository.LoginExternalIdentity(t.Context(), "oidc", "https://idp.example", "EMP-1002", "EMP-1002", "", "Same name", "", `{}`, true)
	if err != nil {
		t.Fatal(err)
	}
	workspace := repository.Bootstrap().Workspace
	// A SCIM identity from another workspace must not become this workspace's employee ID.
	_, err = repository.db.ExecContext(t.Context(), `INSERT INTO auth_identities(id,user_id,provider,issuer,subject,identity_key,username,claims_json,created_at,last_login_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "foreign-scim", first.User.ID, "scim", "other-workspace", "foreign", "foreign-key", "FOREIGN-EMPLOYEE", "{}", "2020-01-01", "2020-01-01")
	if err != nil {
		t.Fatal(err)
	}
	members, err := repository.ListMembers(t.Context(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, member := range members {
		want := ""
		if member.User.ID == first.User.ID {
			want = "EMP-1001"
		}
		if member.User.ID == second.User.ID {
			want = "EMP-1002"
		}
		if want == "" {
			continue
		}
		found++
		if member.User.UserID != want || member.User.Email != "" || member.User.ID == member.User.UserID {
			t.Fatalf("incorrect public identity: %#v", member.User)
		}
	}
	if found != 2 {
		t.Fatalf("expected both same-name members, got %d", found)
	}
	data, ok, err := repository.BootstrapForUser(t.Context(), workspace.URLKey, first.User.ID)
	if err != nil || !ok {
		t.Fatalf("bootstrap failed: %v", err)
	}
	for _, user := range data.Users {
		if user.ID == first.User.ID && user.UserID == "EMP-1001" {
			return
		}
	}
	t.Fatal("enterprise userId missing from bootstrap users")
}
