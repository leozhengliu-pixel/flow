package store

import (
	"errors"
	"fmt"
	"testing"
)

func TestSCIMUserQueriesUseExactMatches(t *testing.T) {
	repository, data := originTestStore(t)
	checkSCIMUserQueries(t, repository, data.Workspace.ID)
}

func checkSCIMUserQueries(t *testing.T, repository *SQLiteStore, workspaceID string) {
	t.Helper()
	ctx := t.Context()
	for index := 1; index <= 3; index++ {
		if _, err := repository.CreateSCIMUser(ctx, workspaceID, SCIMUserInput{ExternalID: fmt.Sprintf("ext-%d", index), UserName: fmt.Sprintf("User%d@Example.test", index), Name: SCIMName{GivenName: "Given", FamilyName: fmt.Sprintf("Family %d", index)}, Role: "member", Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		query SCIMUserQuery
		want  int
	}{
		{SCIMUserQuery{Limit: 10}, 3},
		{SCIMUserQuery{Attribute: "userName", Value: "USER2@example.TEST", Limit: 10}, 1},
		{SCIMUserQuery{Attribute: "externalId", Value: "ext-3", Limit: 10}, 1},
		{SCIMUserQuery{Attribute: "externalId", Value: "EXT-3", Limit: 10}, 0},
		{SCIMUserQuery{Attribute: "email", Value: "user1@example.test", Limit: 10}, 1},
		{SCIMUserQuery{Attribute: "displayName", Value: "given family 2", Limit: 10}, 1},
		{SCIMUserQuery{Attribute: "userName", Value: "nobody@example.test", Limit: 10}, 0},
	}
	for _, item := range cases {
		users, total, err := repository.ListSCIMUsers(ctx, workspaceID, item.query)
		if err != nil || total != item.want || len(users) != item.want {
			t.Fatalf("%+v: users=%d total=%d err=%v, want %d", item.query, len(users), total, err, item.want)
		}
	}
	users, total, err := repository.ListSCIMUsers(ctx, workspaceID, SCIMUserQuery{Attribute: "userName", Value: "user1@example.test"})
	if err != nil || total != 1 || len(users) != 0 {
		t.Fatalf("count-only query = %d users, total %d, err %v", len(users), total, err)
	}
	users, _, _ = repository.ListSCIMUsers(ctx, workspaceID, SCIMUserQuery{Attribute: "externalId", Value: "ext-1", Limit: 1})
	if users[0].User.Name != "Given Family 1" || users[0].Name.FamilyName != "Family 1" || users[0].User.Email != "user1@example.test" {
		t.Fatalf("stored user = %+v", users[0])
	}
}

func TestSCIMUserDeleteSuspendsAndRecreateRestores(t *testing.T) {
	repository, data := originTestStore(t)
	checkSCIMDeleteAndRestore(t, repository, data.Workspace.ID)
}

func checkSCIMDeleteAndRestore(t *testing.T, repository *SQLiteStore, workspaceID string) {
	t.Helper()
	ctx := t.Context()
	user, err := repository.CreateSCIMUser(ctx, workspaceID, SCIMUserInput{ExternalID: "ext-del", UserName: "gone@example.test", Role: "admin", RoleExplicit: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err := repository.CreateSCIMGroup(ctx, workspaceID, "", "Readers", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PatchSCIMGroupMembers(ctx, workspaceID, group.ID, []string{"ext-del"}, nil); err != nil {
		t.Fatal(err)
	}
	groups, err := repository.DeprovisionSCIMUser(ctx, workspaceID, user.User.ID)
	if err != nil || len(groups) != 1 || groups[0] != group.ID {
		t.Fatalf("deprovision groups=%v err=%v", groups, err)
	}
	if _, err := repository.SCIMUser(ctx, workspaceID, user.User.ID); !errors.Is(err, ErrSCIMNotFound) {
		t.Fatalf("deleted SCIM user lookup err = %v", err)
	}
	if reloaded, _ := repository.SCIMGroup(ctx, workspaceID, group.ID); len(reloaded.Members) != 0 {
		t.Fatalf("deleted user still in group: %+v", reloaded.Members)
	}
	restored, err := repository.CreateSCIMUser(ctx, workspaceID, SCIMUserInput{ExternalID: "ext-del", UserName: "gone@example.test", Role: "member", Active: true})
	if err != nil || restored.User.ID != user.User.ID || !restored.User.Active || restored.Role != "admin" {
		t.Fatalf("restored = %+v err=%v (same account, earlier role)", restored, err)
	}
	if _, err := repository.CreateSCIMUser(ctx, workspaceID, SCIMUserInput{ExternalID: "other", UserName: "GONE@example.test", Active: true}); !errors.Is(err, ErrSCIMUniqueness) {
		t.Fatalf("duplicate userName err = %v", err)
	}
}

func TestSCIMGroupMemberPatchesAreIdempotentAndKeepTimestamps(t *testing.T) {
	repository, data := originTestStore(t)
	checkSCIMGroupMemberPatches(t, repository, data.Workspace.ID)
}

func checkSCIMGroupMemberPatches(t *testing.T, repository *SQLiteStore, workspaceID string) {
	t.Helper()
	ctx := t.Context()
	first, _ := repository.CreateSCIMUser(ctx, workspaceID, SCIMUserInput{UserName: "first@example.test", Active: true})
	second, _ := repository.CreateSCIMUser(ctx, workspaceID, SCIMUserInput{UserName: "second@example.test", Active: true})
	group, err := repository.CreateSCIMGroup(ctx, workspaceID, "g-1", "Group", "")
	if err != nil {
		t.Fatal(err)
	}
	group, err = repository.PatchSCIMGroupMembers(ctx, workspaceID, group.ID, []string{first.User.ID}, nil)
	if err != nil || len(group.Members) != 1 {
		t.Fatalf("add = %+v err=%v", group.Members, err)
	}
	stamp := group.Members[0].UpdatedAt
	group, err = repository.ReplaceSCIMGroupMembers(ctx, workspaceID, group.ID, []string{first.User.ID, second.User.ID, first.User.ID})
	if err != nil || len(group.Members) != 2 {
		t.Fatalf("replace = %+v err=%v", group.Members, err)
	}
	for _, member := range group.Members {
		if member.UserID == first.User.ID && !member.UpdatedAt.Equal(stamp) {
			t.Fatalf("existing member re-stamped: %v -> %v", stamp, member.UpdatedAt)
		}
	}
	group, err = repository.PatchSCIMGroupMembers(ctx, workspaceID, group.ID, []string{second.User.ID}, []string{first.User.ID, "usr_unknown"})
	if err != nil || len(group.Members) != 1 || group.Members[0].UserID != second.User.ID {
		t.Fatalf("patch = %+v err=%v", group.Members, err)
	}
	if _, err := repository.PatchSCIMGroupMembers(ctx, workspaceID, group.ID, []string{"usr_unknown"}, nil); !errors.Is(err, ErrSCIMMemberNotFound) {
		t.Fatalf("unknown member add err = %v", err)
	}
	if _, err := repository.CreateSCIMGroup(ctx, workspaceID, "g-1", "Other", ""); !errors.Is(err, ErrSCIMUniqueness) {
		t.Fatalf("duplicate externalId err = %v", err)
	}
	groups, err := repository.ListSCIMGroups(ctx, workspaceID, false)
	if err != nil || len(groups) != 1 || groups[0].Members != nil {
		t.Fatalf("list without members = %+v err=%v", groups, err)
	}
	groups, _ = repository.ListSCIMGroups(ctx, workspaceID, true)
	if len(groups[0].Members) != 1 {
		t.Fatalf("list with members = %+v", groups)
	}
}
