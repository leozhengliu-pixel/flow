package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// seedMCPMembers adds workspace members to the contract fixture the way people join: they register, verify their
// email, and accept an invitation. It returns their user ids by email.
func seedMCPMembers(t *testing.T, f *mcpContractFixture, people [][2]string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, person := range people {
		user, token, err := f.repository.Register(t.Context(), person[0], person[1], "bulk-member-pass")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.repository.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		invitation, err := f.repository.Invite(t.Context(), f.data.Workspace.ID, "usr_admin", person[1], "member", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.repository.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
			t.Fatal(err)
		}
		ids[person[1]] = user.ID
	}
	return ids
}

// fortyPeople are 40 members ("Person 01" … "Person 40", person01@bulk.test …) plus two members who share the name
// Alex Kim.
func fortyPeople() [][2]string {
	people := [][2]string{}
	for index := 1; index <= 40; index++ {
		people = append(people, [2]string{fmt.Sprintf("Person %02d", index), fmt.Sprintf("person%02d@bulk.test", index)})
	}
	return append(people, [2]string{"Alex Kim", "alex.kim@bulk.test"}, [2]string{"Alex Kim", "alex.kim2@bulk.test"})
}

// A team of 40 is one save_team call: everyone who is a member is added, and the people it could not add — not in
// the workspace, or a name two members share — are listed with the reason instead of failing the whole call.
func TestMCPSaveTeamAddsFortyMembersInOneCall(t *testing.T) {
	f := newMCPContractFixture(t)
	ids := seedMCPMembers(t, f, fortyPeople())
	members := []string{}
	for index := 1; index <= 36; index++ {
		switch {
		case index%3 == 0:
			members = append(members, fmt.Sprintf("person%02d@bulk.test", index)) // an email
		case index%3 == 1:
			members = append(members, fmt.Sprintf("Person %02d", index)) // a name
		default:
			members = append(members, fmt.Sprintf("%02d  Person", index)) // the name in the other order, extra space
		}
	}
	members = append(members, "Person 01", "Alex Kim", "Nobody Here", "new.hire@bulk.test", "Ana <ana@bulk.test>")

	receipt := mcpObject(t, f, "save_team", map[string]any{"name": "Platform", "key": "PLAT", "members": members, "owners": []string{"ghost@bulk.test"}})
	if receipt["created"] != true {
		t.Fatalf("the team is created even though some people were not found: %v", receipt)
	}
	teamID := receipt["id"].(string)
	added, _ := receipt["addedMembers"].([]any)
	if len(added) != 36 {
		t.Fatalf("all 36 members found are added once (Person 01 is listed twice): %d %v", len(added), added)
	}
	if receipt["memberCount"] != float64(37) {
		t.Fatalf("the team has the 36 members and its creator: %v", receipt["memberCount"])
	}
	for index := 1; index <= 36; index++ {
		if _, member, err := f.repository.TeamMembership(t.Context(), f.data.Workspace.ID, teamID, ids[fmt.Sprintf("person%02d@bulk.test", index)]); err != nil || !member {
			t.Fatalf("Person %02d is on the team: %v %v", index, member, err)
		}
	}
	var unresolved []map[string]any
	if err := jsonClone(receipt["unresolved"], &unresolved); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, item := range unresolved {
		statuses[item["field"].(string)+":"+item["query"].(string)] = item["status"].(string)
	}
	want := map[string]string{
		"members:Alex Kim": mcpUserAmbiguous, "members:Nobody Here": mcpUserNotFound, "members:new.hire@bulk.test": mcpUserNotFound,
		"members:Ana <ana@bulk.test>": mcpUserNotFound, "owners:ghost@bulk.test": mcpUserNotFound,
	}
	if len(statuses) != len(want) {
		t.Fatalf("unresolved people: %v", unresolved)
	}
	for key, status := range want {
		if statuses[key] != status {
			t.Fatalf("%s is %q, want %q: %v", key, statuses[key], status, unresolved)
		}
	}
	ambiguous := unresolved[slices.IndexFunc(unresolved, func(item map[string]any) bool { return item["query"] == "Alex Kim" })]
	if candidates, _ := ambiguous["candidates"].([]any); len(candidates) != 2 {
		t.Fatalf("an ambiguous name lists the members it fits: %v", ambiguous)
	}
	if receipt["invited"] != nil || len(f.repository.Bootstrap().Invitations) > 42 {
		t.Fatalf("nobody is invited unless the call asks for it: %v", receipt["invited"])
	}

	// The follow-up: the ambiguous name by email, and invitations for the people given by email.
	updated := mcpObject(t, f, "save_team", map[string]any{"id": "PLAT", "members": []string{"alex.kim2@bulk.test", "new.hire@bulk.test", "Ana <ana@bulk.test>", "Nobody Here"}, "owners": []string{"ghost@bulk.test"}, "inviteMissing": true})
	if added, _ := updated["addedMembers"].([]any); len(added) != 1 || added[0] != ids["alex.kim2@bulk.test"] {
		t.Fatalf("the chosen Alex Kim is added: %v", updated["addedMembers"])
	}
	invited, _ := updated["invited"].([]any)
	if len(invited) != 3 || !slices.Contains(invited, any("new.hire@bulk.test")) || !slices.Contains(invited, any("ana@bulk.test")) || !slices.Contains(invited, any("ghost@bulk.test")) {
		t.Fatalf("people given by email are invited, a name is not: %v", updated)
	}
	invitations, err := f.repository.ListInvitations(t.Context(), f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"new.hire@bulk.test", "ana@bulk.test", "ghost@bulk.test"} {
		index := slices.IndexFunc(invitations, func(item domain.Invitation) bool { return item.Email == email && item.Status == "pending" })
		if index < 0 || !slices.Equal(invitations[index].TeamIDs, []string{teamID}) || invitations[index].Role != "member" {
			t.Fatalf("%s is invited to the workspace and the team: %+v", email, invitations)
		}
	}
	again := mcpObject(t, f, "save_team", map[string]any{"id": "PLAT", "members": []string{"new.hire@bulk.test"}, "inviteMissing": true})
	if already, _ := again["alreadyInvited"].([]any); len(already) != 1 || again["invited"] != nil {
		t.Fatalf("a pending invitation is not sent twice: %v", again)
	}
}

// Inviting through save_team needs the viewer's permission to invite, like Settings → Members.
func TestMCPSaveTeamInviteMissingRespectsInvitePermission(t *testing.T) {
	f := newMCPContractFixture(t)
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "workspace_settings.updated", "invite", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.InvitePermission = "admins"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.UpdateMemberRole(t.Context(), f.data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	member := f.addMCPKey(t, "member-key", "usr_member", []string{"read", "write"}, nil)
	receipt := f.objectWith(t, member, "save_team", map[string]any{"name": "Guests", "key": "GST", "members": []string{"outsider@bulk.test"}, "inviteMissing": true})
	if receipt["invited"] != nil {
		t.Fatalf("a member without invite permission invites nobody: %v", receipt)
	}
	failures, _ := receipt["inviteErrors"].([]any)
	if len(failures) != 1 || !strings.Contains(fmt.Sprint(failures[0]), "cannot perform this action") {
		t.Fatalf("the refusal is reported per person: %v", receipt)
	}
	invitations, _ := f.repository.ListInvitations(t.Context(), f.data.Workspace.ID)
	if slices.ContainsFunc(invitations, func(item domain.Invitation) bool { return item.Email == "outsider@bulk.test" }) {
		t.Fatal("no invitation was created")
	}
}

// get_user looks up many people in one call and says for each whether it was found, missing, or ambiguous.
func TestMCPGetUserBulkQueries(t *testing.T) {
	f := newMCPContractFixture(t)
	ids := seedMCPMembers(t, f, fortyPeople())
	queries := []string{"me"}
	for index := 1; index <= 40; index++ {
		queries = append(queries, fmt.Sprintf("Person %02d", index))
	}
	queries = append(queries, "Alex Kim", "missing@bulk.test", "@person07")
	result := mcpObject(t, f, "get_user", map[string]any{"queries": queries})
	if result["found"] != float64(42) || result["notFound"] != float64(1) || result["ambiguous"] != float64(1) {
		t.Fatalf("counts: found=%v notFound=%v ambiguous=%v", result["found"], result["notFound"], result["ambiguous"])
	}
	var results []map[string]any
	if err := jsonClone(result["results"], &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(queries) || results[1]["user"].(map[string]any)["id"] != ids["person01@bulk.test"] {
		t.Fatalf("results follow the queries: %v", results[:2])
	}
	if last := results[len(results)-1]; last["status"] != mcpUserFound || last["user"].(map[string]any)["id"] != ids["person07@bulk.test"] {
		t.Fatalf("an email's local part finds its member: %v", last)
	}
	if single := mcpObject(t, f, "get_user", map[string]any{"query": "Person 05"}); single["id"] != ids["person05@bulk.test"] {
		t.Fatalf("a single query still returns the user: %v", single)
	}
	if message := mcpToolError(t, f, "get_user", map[string]any{}); !strings.Contains(message, "provide query") {
		t.Fatalf("an empty lookup says what to pass: %s", message)
	}
	tooMany := make([]string, mcpMaxUserQueries+1)
	if message := mcpToolError(t, f, "get_user", map[string]any{"queries": tooMany}); !strings.Contains(message, "250") {
		t.Fatalf("a lookup is bounded: %s", message)
	}
}
