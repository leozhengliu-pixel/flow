package store

import (
	"errors"
	"fmt"
	"testing"

	"flow/api/internal/domain"
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

func TestSCIMProfileAndSorting(t *testing.T) {
	repository, data := originTestStore(t)
	checkSCIMProfileAndSort(t, repository, data.Workspace.ID)
}

func checkSCIMProfileAndSort(t *testing.T, repository *SQLiteStore, workspaceID string) {
	t.Helper()
	ctx := t.Context()
	inputs := []SCIMUserInput{
		{ExternalID: "ext-b", UserName: "b@example.test", DisplayName: "Charlie", Active: true},
		{UserName: "c@example.test", DisplayName: "alice", Active: true},
		{ExternalID: "ext-a", UserName: "a@example.test", DisplayName: "Bob", Active: true, Profile: SCIMProfile{
			Title: " Lead ", Timezone: "Europe/Paris", PhoneNumbers: []SCIMTypedItem{{Value: "+1 555", Type: "work"}, {Value: " "}},
			Addresses:  []SCIMAddress{{Type: "work", Locality: "Paris"}, {Type: "home"}},
			Enterprise: SCIMEnterprise{EmployeeNumber: "42", Department: "Eng", Manager: SCIMManager{Value: "m-1"}},
		}},
	}
	for _, input := range inputs {
		if _, err := repository.CreateSCIMUser(ctx, workspaceID, input); err != nil {
			t.Fatal(err)
		}
	}
	item, err := repository.SCIMUser(ctx, workspaceID, "ext-a")
	if err != nil {
		t.Fatal(err)
	}
	profile := item.Profile
	if profile.Title != "Lead" || len(profile.PhoneNumbers) != 1 || len(profile.Addresses) != 1 || profile.Enterprise.Department != "Eng" || profile.Enterprise.Manager.Value != "m-1" || !item.Active() || item.MemberStatus != "active" {
		t.Fatalf("profile = %+v active=%v", item, item.Active())
	}
	initials := func(sortBy string, descending bool) string {
		users, _, err := repository.ListSCIMUsers(ctx, workspaceID, SCIMUserQuery{SortBy: sortBy, Descending: descending, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		result := ""
		for _, user := range users {
			result += user.UserName[:1]
		}
		return result
	}
	cases := []struct {
		sortBy     string
		descending bool
		want       string
	}{
		{"userName", false, "abc"},
		{"userName", true, "cba"},
		{"displayName", false, "cab"},
		{"externalId", false, "abc"},
		{"externalId", true, "cba"},
		{"email", true, "cba"},
		{"created", false, "bca"},
		{"lastModified", true, "acb"},
	}
	for _, item := range cases {
		if got := initials(item.sortBy, item.descending); got != item.want {
			t.Errorf("sort %s desc=%v = %q, want %q", item.sortBy, item.descending, got, item.want)
		}
	}
	if _, _, err := repository.ListSCIMUsers(ctx, workspaceID, SCIMUserQuery{SortBy: "title", Limit: 1}); err == nil {
		t.Fatal("unsupported SQL sort accepted")
	}
	// externalId is readWrite and case-exact; removing it keys by userName.
	b, _ := repository.SCIMUser(ctx, workspaceID, "ext-b")
	if _, err := repository.UpdateSCIMUser(ctx, workspaceID, b.User.ID, SCIMUserInput{ExternalID: "ext-a", UserName: b.UserName, Active: true}); !errors.Is(err, ErrSCIMUniqueness) {
		t.Fatalf("taken externalId err = %v", err)
	}
	if renamed, err := repository.UpdateSCIMUser(ctx, workspaceID, b.User.ID, SCIMUserInput{ExternalID: "EXT-A", UserName: b.UserName, Active: true}); err != nil || renamed.ExternalID != "EXT-A" {
		t.Fatalf("case-exact rename = %+v err=%v", renamed, err)
	}
	if cleared, err := repository.UpdateSCIMUser(ctx, workspaceID, b.User.ID, SCIMUserInput{ClearExternalID: true, UserName: b.UserName, Active: true}); err != nil || !cleared.ExternalIDDefaulted || cleared.ExternalID != b.UserName {
		t.Fatalf("cleared externalId = %+v err=%v", cleared, err)
	}
	if _, err := repository.SCIMUser(ctx, workspaceID, "EXT-A"); !errors.Is(err, ErrSCIMNotFound) {
		t.Fatalf("old externalId still resolves: %v", err)
	}
	states, err := repository.SCIMMemberStates(ctx, workspaceID, []string{item.User.ID, "usr_missing"})
	if err != nil || len(states) != 1 || states[item.User.ID].Role != "member" || states[item.User.ID].Status != "active" {
		t.Fatalf("member states = %+v err=%v", states, err)
	}
}

func TestSCIMRemovedMembershipAndManagedMarker(t *testing.T) {
	repository, data := originTestStore(t)
	checkSCIMRemovedMembership(t, repository, data.Workspace.ID)
}

func checkSCIMRemovedMembership(t *testing.T, repository *SQLiteStore, workspaceID string) {
	t.Helper()
	ctx := t.Context()
	_, workspaceKey, _ := repository.workspaceByID(workspaceID)
	user, err := repository.CreateSCIMUser(ctx, workspaceID, SCIMUserInput{ExternalID: "ext-rm", UserName: "rm@example.test", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if managed, err := repository.IsSCIMManagedMember(ctx, workspaceID, user.User.ID); err != nil || managed {
		t.Fatalf("managed before SCIM is on = %v err=%v", managed, err)
	}
	if err := repository.MutateWorkspace(ctx, workspaceKey, "scim.test", "workspace", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.SCIMEnabled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	token, err := repository.CreateSCIMToken(ctx, workspaceID, "idp")
	if err != nil {
		t.Fatal(err)
	}
	if managed, err := repository.IsSCIMManagedMember(ctx, workspaceID, user.User.ID); err != nil || !managed {
		t.Fatalf("managed with SCIM on = %v err=%v", managed, err)
	}
	members, err := repository.ListMembers(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		want := ""
		if member.User.ID == user.User.ID {
			want = domain.MemberManagedBySCIM
		}
		if member.ManagedBy != want {
			t.Fatalf("member %s managedBy = %q, want %q", member.User.ID, member.ManagedBy, want)
		}
	}
	group, err := repository.CreateSCIMGroup(ctx, workspaceID, "", "Removed group", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PatchSCIMGroupMembers(ctx, workspaceID, group.ID, []string{user.User.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdateSCIMUser(ctx, workspaceID, user.User.ID, SCIMUserInput{ExternalID: "ext-rm", UserName: "rm@example.test", Active: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RemoveMemberWithChanges(ctx, workspaceID, user.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.DetachSCIMUserTeams(ctx, workspaceID, user.User.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := repository.SCIMUser(ctx, workspaceID, user.User.ID)
	if err != nil || removed.MemberStatus != "" || removed.Active() {
		t.Fatalf("removed SCIM user = %+v err=%v", removed, err)
	}
	if managed, _ := repository.IsSCIMManagedMember(ctx, workspaceID, user.User.ID); managed {
		t.Fatal("a removed user is still a managed member")
	}
	again, err := repository.UpdateSCIMUser(ctx, workspaceID, user.User.ID, SCIMUserInput{ExternalID: "ext-rm", UserName: "rm@example.test", DisplayName: "Removed", Active: false, KeepRemoved: true})
	if err != nil || again.MemberStatus != "" || again.User.DisplayName != "Removed" {
		t.Fatalf("inactive update of a removed user = %+v err=%v", again, err)
	}
	groups, err := repository.SCIMUserGroupIDs(ctx, workspaceID, user.User.ID)
	if err != nil || len(groups) != 1 || groups[0] != group.ID {
		t.Fatalf("groups = %v err=%v", groups, err)
	}
	back, err := repository.UpdateSCIMUser(ctx, workspaceID, user.User.ID, SCIMUserInput{ExternalID: "ext-rm", UserName: "rm@example.test", Active: true, KeepRemoved: true})
	if err != nil || back.MemberStatus != "active" || !back.Active() {
		t.Fatalf("reactivated = %+v err=%v", back, err)
	}
	if err := repository.RevokeSCIMToken(ctx, workspaceID, token.ID); err != nil {
		t.Fatal(err)
	}
	if managed, _ := repository.IsSCIMManagedMember(ctx, workspaceID, user.User.ID); managed {
		t.Fatal("managed with every SCIM token revoked")
	}
}
