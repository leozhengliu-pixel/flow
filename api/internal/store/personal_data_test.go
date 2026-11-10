package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScrubPersonJSONReplacesCopiesOfThePerson(t *testing.T) {
	anonymous := domain.DeletedUserIdentity("usr_pat")
	raw := []byte(`{"id":"issue_1","title":"Fix the login","creator":{"id":"usr_pat","name":"pat","displayName":"Pat Doe","email":"pat@example.com","avatarUrl":"https://example.com/pat.png","active":true},"assignee":{"id":"usr_sam","name":"sam","displayName":"Sam","email":"sam@example.com"},"body":{"type":"mention","attrs":{"id":"usr_pat","label":"Pat Doe"}},"audit":{"userId":"usr_pat","userName":"Pat Doe","userEmail":"pat@example.com"},"estimate":12345678901234567890}`)
	next, changed := ScrubPersonJSON(raw, anonymous, false)
	if !changed {
		t.Fatal("nothing changed")
	}
	text := string(next)
	for _, gone := range []string{"Pat Doe", "pat@example.com", "pat.png"} {
		if strings.Contains(text, gone) {
			t.Fatalf("%q kept: %s", gone, text)
		}
	}
	for _, kept := range []string{"Fix the login", `"displayName":"Sam"`, "sam@example.com", `"id":"usr_pat"`, "12345678901234567890", domain.DeletedUserDisplayName, `"deleted":true`} {
		if !strings.Contains(text, kept) {
			t.Fatalf("%q lost: %s", kept, text)
		}
	}
	// A second pass changes nothing.
	if _, again := ScrubPersonJSON(next, anonymous, false); again {
		t.Fatalf("not idempotent: %s", next)
	}
	// Values that do not name the person, and invalid JSON, stay as they are.
	if _, changed := ScrubPersonJSON([]byte(`{"id":"usr_sam","displayName":"Sam"}`), anonymous, false); changed {
		t.Fatal("another person was rewritten")
	}
	if out, changed := ScrubPersonJSON([]byte(`not json usr_pat`), anonymous, false); changed || string(out) != "not json usr_pat" {
		t.Fatal("invalid JSON was rewritten")
	}
	// Events about the person lose the profile they carry without an id.
	profile, changed := ScrubPersonJSON([]byte(`{"displayName":"Pat Doe","username":"pat","jobTitle":"Engineer","teamId":"team_1"}`), anonymous, true)
	if !changed || strings.Contains(string(profile), "Pat Doe") || strings.Contains(string(profile), "Engineer") || !strings.Contains(string(profile), "team_1") {
		t.Fatalf("profile event = %s", profile)
	}
}

func TestPersonalDataExportJobsAreRateLimited(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "exports.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	first, created, err := repository.CreatePersonalDataExport(ctx, "usr_admin", now, 24*time.Hour, 2)
	if err != nil || !created || first.Status != PersonalDataExportQueued {
		t.Fatalf("first = %+v %v %v", first, created, err)
	}
	// One at a time: a request while one is pending returns it.
	pending, created, err := repository.CreatePersonalDataExport(ctx, "usr_admin", now, 24*time.Hour, 2)
	if err != nil || created || pending.ID != first.ID {
		t.Fatalf("pending = %+v %v %v", pending, created, err)
	}
	if err := repository.FinishPersonalDataExport(ctx, first.ID, "personal_data_export_x.zip", 10, "", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, created, err := repository.CreatePersonalDataExport(ctx, "usr_admin", now, 24*time.Hour, 2)
	if err != nil || !created {
		t.Fatalf("second = %+v %v %v", second, created, err)
	}
	if err := repository.FinishPersonalDataExport(ctx, second.ID, "", 0, "boom", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.CreatePersonalDataExport(ctx, "usr_admin", now, 24*time.Hour, 2); !errors.Is(err, ErrPersonalDataExportLimited) {
		t.Fatalf("third = %v, want the limit", err)
	}
	// Another account has its own budget, and cannot read these.
	if _, err := repository.PersonalDataExport(ctx, "usr_other", first.ID); err == nil {
		t.Fatal("another account read the export")
	}
	// Expired downloads are found for purging, then marked expired.
	expired, err := repository.ExpiredPersonalDataExports(ctx, now.Add(2*time.Hour), 10)
	if err != nil || len(expired) != 1 || expired[0].ID != first.ID || expired[0].ObjectKey == "" {
		t.Fatalf("expired = %+v %v", expired, err)
	}
	if err := repository.ExpirePersonalDataExport(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if item, _ := repository.PersonalDataExport(ctx, "usr_admin", first.ID); item.Status != PersonalDataExportExpired || item.ObjectKey != "" {
		t.Fatalf("after purge = %+v", item)
	}
}

func TestErasedMemberCannotRejoinTheWorkspace(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "erase.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	user, _, err := repository.Register(ctx, "Pat Doe", "pat@example.com", "a-long-password")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := repository.BootstrapFor("test-workspace")
	if err := repository.EnsureWorkspaceMembership(ctx, data.Workspace.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	// The account still belongs to a workspace: it is erased from those first.
	if err := repository.EraseAccount(ctx, user.ID); err == nil {
		t.Fatal("an account with memberships was erased")
	}
	if err := repository.ErasePersonFromWorkspace(ctx, "test-workspace", user.ID); err != nil {
		t.Fatal(err)
	}
	if erased, err := repository.ErasedFromWorkspace(ctx, data.Workspace.ID, user.ID); err != nil || !erased {
		t.Fatalf("erased = %v %v", erased, err)
	}
	former, err := repository.FormerMembers(ctx, "test-workspace")
	if err != nil || !former[user.ID] {
		t.Fatalf("not a former member: %v %v", former, err)
	}
	if err := repository.EnsureWorkspaceMembership(ctx, data.Workspace.ID, user.ID); !errors.Is(err, ErrErasedFromWorkspace) {
		t.Fatalf("rejoin = %v", err)
	}
	departed, err := repository.ListDepartedMembers(ctx, data.Workspace.ID, "test-workspace")
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range departed {
		if member.User.ID == user.ID && (member.User.DisplayName != domain.DeletedUserDisplayName || member.User.Email != "") {
			t.Fatalf("departed member keeps their identity: %+v", member.User)
		}
	}
	if err := repository.EraseAccount(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	tombstone, err := repository.UserByID(ctx, user.ID)
	if err != nil || !tombstone.Deleted || tombstone.Email != "" || tombstone.DisplayName != domain.DeletedUserDisplayName || tombstone.Active {
		t.Fatalf("tombstone = %+v %v", tombstone, err)
	}
	// The address is free again.
	if _, _, err := repository.Register(ctx, "Pat Again", "pat@example.com", "a-long-password"); err != nil {
		t.Fatal(err)
	}
}

func TestSoleAdminWorkspaces(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "admins.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	sole, err := repository.SoleAdminWorkspaces(ctx, "usr_admin")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := repository.BootstrapFor("test-workspace")
	admins := 0
	for _, user := range data.Users {
		if role, status, err := repository.WorkspaceRole(ctx, data.Workspace.ID, user.ID); err == nil && status == "active" && isWorkspaceAdminRole(role) {
			admins++
		}
	}
	if (admins == 1) != (len(sole) == 1) {
		t.Fatalf("sole admin workspaces = %v with %d admins", sole, admins)
	}
}
