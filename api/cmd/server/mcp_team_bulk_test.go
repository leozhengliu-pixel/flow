package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
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
	hint, _ := receipt["inviteHint"].(string)
	for _, want := range []string{"Settings → Administration → Members → Invite", "new.hire@bulk.test, ana@bulk.test, ghost@bulk.test", "Send invites", "add them to Platform", "Nobody Here"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("the hint names the people to invite and where (%q): %q", want, hint)
		}
	}
	if strings.Contains(hint, "Alex Kim") {
		t.Fatalf("an ambiguous name is not someone to invite: %q", hint)
	}
	if receipt["invited"] != nil || len(f.repository.Bootstrap().Invitations) > 42 {
		t.Fatalf("nobody is invited: %v", receipt["invited"])
	}

	// The follow-up: the ambiguous name by email; the people outside the workspace are still only reported.
	updated := mcpObject(t, f, "save_team", map[string]any{"id": "PLAT", "members": []string{"alex.kim2@bulk.test", "new.hire@bulk.test"}})
	if added, _ := updated["addedMembers"].([]any); len(added) != 1 || added[0] != ids["alex.kim2@bulk.test"] {
		t.Fatalf("the chosen Alex Kim is added: %v", updated["addedMembers"])
	}
	if hint, _ := updated["inviteHint"].(string); !strings.Contains(hint, "new.hire@bulk.test") || strings.Contains(hint, "alex.kim2") {
		t.Fatalf("the hint lists only who is still missing: %q", hint)
	}
	assertNoInvitations(t, f.repository, "new.hire@bulk.test", "ana@bulk.test", "ghost@bulk.test")
}

func assertNoInvitations(t *testing.T, repository *store.SQLiteStore, emails ...string) {
	t.Helper()
	invitations, err := repository.ListInvitations(t.Context(), repository.Bootstrap().Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range emails {
		if slices.ContainsFunc(invitations, func(item domain.Invitation) bool { return strings.EqualFold(item.Email, email) }) {
			t.Fatalf("%s was invited: save_team cannot send workspace invitations", email)
		}
	}
}

// save_team cannot invite: the public /mcp endpoint rejects the old `inviteMissing` argument as not in the schema,
// and the built-in agent (which calls the tool without schema validation) has it ignored — either way no
// invitation is created and the people are reported with the settings path instead.
func TestMCPSaveTeamCannotInvite(t *testing.T) {
	f := newMCPContractFixture(t)
	message := mcpToolError(t, f, "save_team", map[string]any{"name": "Guests", "key": "GST", "members": []string{"outsider@bulk.test"}, "inviteMissing": true})
	if !strings.Contains(message, "Invalid arguments for save_team") || !strings.Contains(message, "inviteMissing") {
		t.Fatalf("an MCP client is told the argument does not exist: %s", message)
	}
	if _, err := mcpFindTeam(f.repository.Bootstrap(), "GST"); err == nil {
		t.Fatal("a rejected call creates nothing")
	}
	assertNoInvitations(t, f.repository, "outsider@bulk.test")

	repository, actor, ctx := newMCPToolTestContext(t)
	service := &server{store: repository, uploadPath: t.TempDir()}
	result, err := service.callFlowTool(ctx, actor, "save_team", map[string]any{"name": "Invite Test", "key": "INV", "members": []any{"qa-invite-1@example.com", "qa-invite-2@example.com", "qa-invite-3@example.com"}, "inviteMissing": true})
	if err != nil {
		t.Fatal(err)
	}
	receipt := result.(map[string]any)
	if receipt["created"] != true || receipt["key"] != "INV" || receipt["memberCount"] != 1 {
		t.Fatalf("the team is created with its creator: %v", receipt)
	}
	unresolved, _ := receipt["unresolved"].([]map[string]any)
	if len(unresolved) != 3 || unresolved[0]["status"] != mcpUserNotFound {
		t.Fatalf("each person outside the workspace is reported: %v", receipt)
	}
	hint, _ := receipt["inviteHint"].(string)
	want := "Not added, because they are not members of this workspace: qa-invite-1@example.com, qa-invite-2@example.com, qa-invite-3@example.com. Workspace invitations can't be sent from here. To finish, go to Settings → Administration → Members → Invite, paste qa-invite-1@example.com, qa-invite-2@example.com, qa-invite-3@example.com, and click Send invites; once they accept, add them to Invite Test in Settings → Administration → Teams → Invite Test → Members (or ask again)."
	if hint != want {
		t.Fatalf("inviteHint:\n got %q\nwant %q", hint, want)
	}
	if note, _ := receipt["unresolvedNote"].(string); strings.Contains(note, "inviteMissing") || !strings.Contains(note, "inviteHint") {
		t.Fatalf("the note points at the hint, not an invite option: %q", note)
	}
	for _, key := range []string{"invited", "alreadyInvited", "inviteErrors"} {
		if receipt[key] != nil {
			t.Fatalf("no invitation result (%s): %v", key, receipt)
		}
	}
	assertNoInvitations(t, repository, "qa-invite-1@example.com", "qa-invite-2@example.com", "qa-invite-3@example.com")
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
