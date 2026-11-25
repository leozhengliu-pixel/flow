package store

import (
	"errors"
	"path/filepath"
	"testing"
)

type identityLinkFixture struct {
	repository  *SQLiteStore
	workspaceID string
	victimID    string
}

func newIdentityLinkFixture(t *testing.T) identityLinkFixture {
	t.Helper()
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "identity-link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	victim, _, err := repository.LoginExternal(t.Context(), "victim@example.com", "Victim", "", true)
	if err != nil {
		t.Fatal(err)
	}
	workspace, ok := repository.BootstrapFor("test-workspace")
	if !ok {
		t.Fatal("test workspace not found")
	}
	return identityLinkFixture{repository: repository, workspaceID: workspace.Workspace.ID, victimID: victim.User.ID}
}

func (f identityLinkFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.repository.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f identityLinkFixture) assertNothingLinked(t *testing.T, sessionsBefore int) {
	t.Helper()
	if n := f.count(t, `SELECT COUNT(*) FROM auth_identities WHERE user_id=?`, f.victimID); n != 0 {
		t.Fatalf("the victim account got %d linked identities", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM auth_sessions WHERE user_id=?`, f.victimID); n != sessionsBefore {
		t.Fatalf("victim sessions=%d, want %d", n, sessionsBefore)
	}
}

func TestLoginExternalIdentityRefusesEmailMatchWithoutTrust(t *testing.T) {
	f := newIdentityLinkFixture(t)
	before := f.count(t, `SELECT COUNT(*) FROM auth_sessions WHERE user_id=?`, f.victimID)
	for name, policy := range map[string]ExternalLinkPolicy{
		"zero policy":      {},
		"unverified email": {UnverifiedEmail: true},
	} {
		session, token, err := f.repository.LoginExternalIdentityWithPolicy(t.Context(), "enterprise:evil", "https://evil.example", "attacker-sub", "attacker", "victim@example.com", "Attacker", "https://evil.example/a.png", `{}`, true, policy)
		if !errors.Is(err, ErrIdentityConflict) || token != "" || session.User.ID != "" {
			t.Fatalf("%s: err=%v token=%q user=%q", name, err, token, session.User.ID)
		}
	}
	f.assertNothingLinked(t, before)
	if _, _, err := f.repository.LoginExternalIdentity(t.Context(), "saml", "https://idp.example", "sub", "sub", "victim@example.com", "Attacker", "", `{}`, true); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("LoginExternalIdentity merged by email: %v", err)
	}
	f.assertNothingLinked(t, before)
	var name string
	if err := f.repository.db.QueryRowContext(t.Context(), `SELECT name FROM auth_users WHERE id=?`, f.victimID).Scan(&name); err != nil || name != "Victim" {
		t.Fatalf("victim profile was overwritten: %q %v", name, err)
	}
}

func TestLoginExternalIdentityTrustedEmailLinksOnlyAMember(t *testing.T) {
	f := newIdentityLinkFixture(t)
	policy := ExternalLinkPolicy{TrustedEmail: true, MembershipWorkspaceID: f.workspaceID}
	session, _, err := f.repository.LoginExternalIdentityWithPolicy(t.Context(), "enterprise:corp", "https://idp.corp.example", "corp-sub", "victim", "victim@example.com", "Victim", "", `{}`, false, policy)
	if err != nil || session.User.ID != f.victimID {
		t.Fatalf("trusted member was not linked: user=%q err=%v", session.User.ID, err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM auth_identities WHERE user_id=?`, f.victimID); n != 1 {
		t.Fatalf("identities=%d, want 1", n)
	}
	// Not a member of that workspace (or suspended): conflict even when trusted.
	if _, err := f.repository.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.workspaceID, f.victimID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repository.db.ExecContext(t.Context(), `DELETE FROM auth_identities WHERE user_id=?`, f.victimID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.repository.LoginExternalIdentityWithPolicy(t.Context(), "enterprise:corp", "https://idp.corp.example", "corp-sub", "victim", "victim@example.com", "Victim", "", `{}`, false, policy); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("suspended member linked: %v", err)
	}
	if _, err := f.repository.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, f.workspaceID, f.victimID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.repository.LoginExternalIdentityWithPolicy(t.Context(), "enterprise:corp", "https://idp.corp.example", "corp-sub", "victim", "victim@example.com", "Victim", "", `{}`, false, policy); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("non-member linked: %v", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM auth_identities WHERE user_id=?`, f.victimID); n != 0 {
		t.Fatalf("identities=%d, want 0", n)
	}
}

func TestLoginExternalIdentityExistingIdentityAndNewUsersUnchanged(t *testing.T) {
	f := newIdentityLinkFixture(t)
	if err := f.repository.LinkExternalIdentity(t.Context(), f.victimID, "oidc", "https://idp.example", "victim-sub", "victim", `{}`); err != nil {
		t.Fatal(err)
	}
	// A linked identity signs in without any policy, whatever email it sends.
	session, _, err := f.repository.LoginExternalIdentity(t.Context(), "oidc", "https://idp.example", "victim-sub", "victim", "someone-else@example.com", "Victim", "", `{}`, false)
	if err != nil || session.User.ID != f.victimID {
		t.Fatalf("linked identity login: user=%q err=%v", session.User.ID, err)
	}
	// A new user with an unmatched email is provisioned as before.
	fresh, _, err := f.repository.LoginExternalIdentity(t.Context(), "oidc", "https://idp.example", "new-sub", "new", "new.person@example.com", "New Person", "", `{}`, true)
	if err != nil || fresh.User.ID == "" || fresh.User.ID == f.victimID || fresh.User.Email != "new.person@example.com" {
		t.Fatalf("new user: %#v err=%v", fresh.User, err)
	}
}

func TestLinkExternalIdentityBindsToTheSessionUser(t *testing.T) {
	f := newIdentityLinkFixture(t)
	other, _, err := f.repository.LoginExternal(t.Context(), "other@example.com", "Other", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repository.LinkExternalIdentity(t.Context(), f.victimID, "oidc", "https://idp.example", "shared-sub", "victim", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.LinkExternalIdentity(t.Context(), f.victimID, "oidc", "https://idp.example", "shared-sub", "victim", `{}`); err != nil {
		t.Fatalf("relinking the same account must be idempotent: %v", err)
	}
	if err := f.repository.LinkExternalIdentity(t.Context(), other.User.ID, "oidc", "https://idp.example", "shared-sub", "other", `{}`); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("identity moved to another account: %v", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM auth_identities WHERE user_id=?`, other.User.ID); n != 0 {
		t.Fatalf("other account identities=%d", n)
	}
	// Once linked, that identity signs in to the linked account.
	session, _, err := f.repository.LoginExternalIdentity(t.Context(), "oidc", "https://idp.example", "shared-sub", "victim", "", "", "", `{}`, false)
	if err != nil || session.User.ID != f.victimID {
		t.Fatalf("linked identity login: %v %v", session.User.ID, err)
	}
}
