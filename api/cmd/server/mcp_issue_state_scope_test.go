package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func (f *mcpContractFixture) objectWith(t *testing.T, secret, name string, args map[string]any) map[string]any {
	t.Helper()
	return mcpSuccess(t, f.callWith(t, secret, name, args)).(map[string]any)
}

func (f *mcpContractFixture) statusesWith(t *testing.T, secret, team string) map[string]string {
	t.Helper()
	response := f.callWith(t, secret, "list_issue_statuses", map[string]any{"team": team})
	var items []struct{ ID, Name string }
	if err := json.Unmarshal([]byte(mcpResultText(t, response)), &items); err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, item := range items {
		result[item.Name] = item.ID
	}
	return result
}

func mcpDeniedMessage(t *testing.T, f *mcpContractFixture, secret, name string, args map[string]any) string {
	t.Helper()
	response := f.callWith(t, secret, name, args)
	if response["error"] != nil {
		raw, _ := json.Marshal(response["error"])
		return string(raw)
	}
	result := response["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("%s unexpectedly succeeded: %v", name, result["structuredContent"])
	}
	return result["content"].([]any)[0].(map[string]any)["text"].(string)
}

// A member saves issues in a private team that has its own workflow, and in a team that inherits its parent's.
// Listing, name/ID/type resolution and the store's validation must agree on one effective state set.
func TestMCPSaveIssueResolvesStatesInTheTargetTeamsEffectiveWorkflow(t *testing.T) {
	f := newMCPContractFixture(t)
	if err := f.repository.UpdateMemberRole(t.Context(), f.data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	member := f.addMCPKey(t, "member-key", "usr_member", []string{"read", "write"}, nil)
	admin := f.secret
	custom := []map[string]any{{"name": "Intake", "type": "backlog"}, {"name": "Doing", "type": "started"}, {"name": "Shipped", "type": "completed"}, {"name": "Dropped", "type": "canceled"}, {"name": "Shared name", "type": "started"}}
	f.objectWith(t, member, "save_team", map[string]any{"name": "Secret", "key": "SEC", "private": true, "states": custom})
	f.objectWith(t, admin, "save_team", map[string]any{"name": "Hidden", "key": "HID", "private": true, "states": custom})
	f.objectWith(t, member, "save_team", map[string]any{"name": "Plain", "key": "PLN"})
	f.objectWith(t, admin, "save_team", map[string]any{"name": "Child", "key": "CHD", "parentTeam": "SEC", "members": []string{"Test member"}})
	stored := func(id string) string {
		issue, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, id)
		if err != nil {
			t.Fatal(err)
		}
		return issue.State.ID + "|" + issue.Team.Key
	}

	secret := f.statusesWith(t, member, "SEC")
	for name, stateID := range secret {
		if stateID == "state_todo" || stateID == "state_backlog" {
			t.Fatalf("a team with its own workflow lists workspace state %s %s", name, stateID)
		}
	}
	for _, name := range []string{"Intake", "Doing", "Shipped", "Dropped", "Shared name"} {
		if secret[name] == "" {
			t.Fatalf("custom state %q is not listed: %v", name, secret)
		}
	}
	plain := f.statusesWith(t, member, "PLN")
	if plain["Todo"] == "" || plain["Doing"] != "" {
		t.Fatalf("a team on the default workflow lists %v", plain)
	}

	issue := f.objectWith(t, member, "save_issue", map[string]any{"title": "private work", "team": "SEC"})
	id := issue["id"].(string)
	// Name, exact ID and type resolve to the team's own state, and every read path agrees.
	for _, tc := range []struct{ state, want string }{{"Doing", "Doing"}, {secret["Shipped"], "Shipped"}, {"canceled", "Canceled"}, {"Dropped", "Dropped"}, {"intake", "Intake"}} {
		f.objectWith(t, member, "save_issue", map[string]any{"id": id, "state": tc.state})
		if got := stored(id); got != secret[tc.want]+"|SEC" {
			t.Fatalf("state %q: persisted %s, want %s", tc.state, got, secret[tc.want])
		}
		if raw, _ := json.Marshal(f.objectWith(t, member, "get_issue", map[string]any{"id": id})); !strings.Contains(string(raw), tc.want) {
			t.Fatalf("state %q: get_issue does not show %s: %s", tc.state, tc.want, raw)
		}
	}
	created := f.objectWith(t, member, "save_issue", map[string]any{"title": "born doing", "team": "SEC", "state": "Doing"})
	if got := stored(created["id"].(string)); got != secret["Doing"]+"|SEC" {
		t.Fatalf("created issue: %s", got)
	}

	// The same name in another team never resolves to that team's ID.
	hidden := f.statusesWith(t, admin, "HID")
	if hidden["Shared name"] == secret["Shared name"] {
		t.Fatal("fixture: ids collide")
	}
	if message := mcpDeniedMessage(t, f, member, "save_issue", map[string]any{"id": id, "state": hidden["Shared name"]}); !strings.Contains(message, "state") {
		t.Fatalf("another team's state ID: %q", message)
	}
	f.objectWith(t, member, "save_issue", map[string]any{"id": id, "state": "Shared name"})
	if got := stored(id); got != secret["Shared name"]+"|SEC" {
		t.Fatalf("same-named state persisted %s, want %s", got, secret["Shared name"])
	}

	// Invalid and workspace-default states are rejected, never replaced by a default.
	for _, state := range []string{"Nope", plain["Todo"]} {
		if message := mcpDeniedMessage(t, f, member, "save_issue", map[string]any{"id": id, "state": state}); !strings.Contains(message, "state") {
			t.Fatalf("state %q: %q", state, message)
		}
	}
	if got := stored(id); got != secret["Shared name"]+"|SEC" {
		t.Fatalf("a rejected state changed the issue to %s", got)
	}

	// A team the member cannot see.
	mcpDeniedWith(t, f, member, "list_issue_statuses", map[string]any{"team": "HID"}, "not found")
	mcpDeniedWith(t, f, member, "save_issue", map[string]any{"title": "x", "team": "HID", "state": "Doing"}, "not found")

	// A child team inherits its parent's workflow.
	child := f.statusesWith(t, member, "CHD")
	if child["Doing"] != secret["Doing"] || len(child) != len(secret) {
		t.Fatalf("inherited statuses %v, parent %v", child, secret)
	}
	inherited := f.objectWith(t, member, "save_issue", map[string]any{"title": "child work", "team": "CHD", "state": "Doing"})
	if got := stored(inherited["id"].(string)); got != secret["Doing"]+"|CHD" {
		t.Fatalf("inherited: %s", got)
	}

	// Moving across teams with a state resolves in the destination.
	if message := mcpDeniedMessage(t, f, member, "save_issue", map[string]any{"id": id, "team": "PLN", "state": "Doing"}); !strings.Contains(message, "state") {
		t.Fatalf("source team's state in destination: %q", message)
	}
	f.objectWith(t, member, "save_issue", map[string]any{"id": id, "team": "PLN", "state": "Todo"})
	if got := stored(id); got != plain["Todo"]+"|PLN" {
		t.Fatalf("moved issue: %s, want %s", got, plain["Todo"])
	}
}
