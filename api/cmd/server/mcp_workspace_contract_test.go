package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// addMCPKey registers another API key on the contract fixture. A non-nil
// teamIDs slice makes the key team-restricted to exactly those teams.
func (f *mcpContractFixture) addMCPKey(t *testing.T, id, creatorID string, scopes, teamIDs []string) string {
	t.Helper()
	secret := "mcp-contract-" + id
	key := domain.APIKey{ID: id, SecretHash: secretHash(secret), CreatorID: creatorID, Scopes: scopes, CreatedAt: time.Now().UTC()}
	if teamIDs != nil {
		key.TeamRestriction, key.TeamIDs = "selected", teamIDs
	}
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "api_key.created", id, nil, func(data *domain.Bootstrap) error {
		data.APIKeys = append(data.APIKeys, key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return secret
}

func (f *mcpContractFixture) callWith(t *testing.T, secret, name string, args map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	request, _ := http.NewRequest(http.MethodPost, f.host.URL+"/mcp", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func mcpDeniedWith(t *testing.T, f *mcpContractFixture, secret, name string, args map[string]any, want ...string) {
	t.Helper()
	response := f.callWith(t, secret, name, args)
	message := ""
	if response["error"] != nil {
		raw, _ := json.Marshal(response["error"])
		message = string(raw)
	} else {
		result := response["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("%s unexpectedly succeeded: %v", name, result["structuredContent"])
		}
		message = result["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	for _, fragment := range want {
		if strings.Contains(message, fragment) {
			return
		}
	}
	t.Fatalf("%s error %q does not mention any of %q", name, message, want)
}

// guestSecret demotes the fixture's second member to guest and returns a key acting as them.
func (f *mcpContractFixture) guestSecret(t *testing.T) string {
	t.Helper()
	if err := f.repository.UpdateMemberRole(t.Context(), f.data.Workspace.ID, "usr_member", "guest"); err != nil {
		t.Fatal(err)
	}
	return f.addMCPKey(t, "guest-key", "usr_member", []string{"read", "write"}, nil)
}

func TestMCPSaveTeamCreatesLikeTheUIAndUpdates(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_team", map[string]any{
		"name": "Platform", "key": "plt", "description": "Infrastructure", "timezone": "Europe/Berlin", "members": []string{"Test member"},
		"triageEnabled": true, "cyclesEnabled": true, "cycleDurationWeeks": 3,
		"states": []map[string]any{{"name": "In Review", "type": "started"}}, "labels": []map[string]any{{"name": "infra", "color": "#112233"}},
	})
	if created["key"] != "PLT" || created["created"] != true || !strings.HasSuffix(created["url"].(string), "/team/PLT/all") {
		t.Fatalf("receipt: %v", created)
	}
	teamID := created["id"].(string)
	data := f.repository.Bootstrap()
	settings := data.TeamSettings[teamID]
	if settings.Description != "Infrastructure" || !settings.TriageEnabled || settings.Timezone != "Europe/Berlin" || settings.Access != "public" {
		t.Fatalf("settings: %+v", settings)
	}
	if cycles := data.CycleSettings[teamID]; !cycles.Enabled || cycles.DurationWeeks != 3 {
		t.Fatalf("cycles: %+v", cycles)
	}
	if !slices.ContainsFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == teamID && state.Name == "In Review" }) {
		t.Fatal("state not created")
	}
	if !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.Scope == teamID && label.Name == "infra" }) {
		t.Fatal("team label not created")
	}
	for userID, role := range map[string]string{"usr_admin": "owner", "usr_member": "member"} {
		membership, member, err := f.repository.TeamMembership(t.Context(), data.Workspace.ID, teamID, userID)
		if err != nil || !member || membership.Role != role {
			t.Fatalf("membership %s: %+v %v %v", userID, membership, member, err)
		}
	}
	updated := mcpObject(t, f, "save_team", map[string]any{"id": "PLT", "name": "Platform Eng", "removeMembers": []string{"Test member"}, "triageEnabled": false})
	if updated["name"] != "Platform Eng" || updated["created"] != false {
		t.Fatalf("update receipt: %v", updated)
	}
	if _, member, _ := f.repository.TeamMembership(t.Context(), data.Workspace.ID, teamID, "usr_member"); member {
		t.Fatal("member not removed")
	}
	if f.repository.Bootstrap().TeamSettings[teamID].TriageEnabled {
		t.Fatal("triage not disabled")
	}

	if message := mcpToolError(t, f, "save_team", map[string]any{"name": "No key"}); !strings.Contains(message, "name and key are required") {
		t.Fatalf("missing key: %s", message)
	}
	if message := mcpToolError(t, f, "save_team", map[string]any{"name": "Bad", "key": "BAD", "states": []map[string]any{{"name": "X", "type": "triage"}}}); !strings.Contains(message, "type") {
		t.Fatalf("invalid state type: %s", message)
	}
	if slices.ContainsFunc(f.repository.Bootstrap().Teams, func(team domain.Team) bool { return team.Key == "BAD" }) {
		t.Fatal("validation failure left a partial team")
	}
	restricted := f.addMCPKey(t, "restricted-team", "usr_admin", []string{"read", "write"}, []string{"team_test"})
	mcpDeniedWith(t, f, restricted, "save_team", map[string]any{"name": "Sneaky", "key": "SNK"}, "cannot create teams")
	mcpDeniedWith(t, f, restricted, "save_team", map[string]any{"id": "PLT", "name": "Hijacked"}, "not found")
	guest := f.guestSecret(t)
	mcpDeniedWith(t, f, guest, "save_team", map[string]any{"name": "Guest team", "key": "GST"}, "cannot perform this action", "Guests")
}

func TestMCPSaveAndDeleteLabels(t *testing.T) {
	f := newMCPContractFixture(t)
	project := mcpObject(t, f, "save_label", map[string]any{"name": "Growth", "type": "project", "color": "#00aa00"})
	if project["type"] != "project" || project["name"] != "Growth" {
		t.Fatalf("project label: %v", project)
	}
	edited := mcpObject(t, f, "save_label", map[string]any{"id": "Growth", "description": "Growth work", "archived": true})
	if edited["description"] != "Growth work" || edited["archived"] != true {
		t.Fatalf("edit: %v", edited)
	}
	team := mcpObject(t, f, "save_label", map[string]any{"name": "Backend", "team": "TST"})
	if team["teamId"] != "team_test" {
		t.Fatalf("team label: %v", team)
	}
	if renamed := mcpObject(t, f, "save_label", map[string]any{"id": team["id"], "name": "Server"}); renamed["name"] != "Server" {
		t.Fatalf("team rename: %v", renamed)
	}
	if deleted := mcpObject(t, f, "delete_label", map[string]any{"id": team["id"]}); deleted["deleted"] != true {
		t.Fatalf("delete team label: %v", deleted)
	}
	mcpObject(t, f, "delete_label", map[string]any{"id": project["id"]})
	for _, label := range f.repository.Bootstrap().Labels {
		if label.ID == team["id"] || label.ID == project["id"] {
			t.Fatalf("label survived delete: %+v", label)
		}
	}
	if message := mcpToolError(t, f, "save_label", map[string]any{"color": "#ffffff"}); !strings.Contains(message, "name is required") {
		t.Fatalf("missing name: %s", message)
	}
	if message := mcpToolError(t, f, "save_label", map[string]any{"name": "Init", "type": "initiative", "team": "TST"}); !strings.Contains(message, "workspace-wide") {
		t.Fatalf("initiative team label: %s", message)
	}
	if message := mcpToolError(t, f, "delete_label", map[string]any{"id": "missing"}); !strings.Contains(message, "not found") {
		t.Fatalf("missing label: %s", message)
	}
	teamLabel := mcpObject(t, f, "save_label", map[string]any{"name": "Private", "team": "TST"})
	restricted := f.addMCPKey(t, "restricted-label", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "delete_label", map[string]any{"id": teamLabel["id"]}, "not found")
	guest := f.guestSecret(t)
	mcpDeniedWith(t, f, guest, "save_label", map[string]any{"name": "Guest label"}, "cannot perform this action")
}

func TestMCPSaveViewUsesTheFilterBarModel(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_view", map[string]any{
		"name": "Urgent defects", "team": "TST", "layout": "active", "shared": true,
		"filters": []map[string]any{
			{"field": "priority", "values": []string{"Urgent"}},
			{"field": "labels", "values": []string{"Defect"}},
			{"field": "status", "values": []string{"started"}},
			{"field": "assignee", "operator": "isNot", "values": []string{"none"}},
		},
	})
	if created["scope"] != "team" || created["shared"] != true || !strings.Contains(created["url"].(string), "/team/TST/view/") || !strings.Contains(created["shareUrl"].(string), "/shared/views/") {
		t.Fatalf("receipt: %v", created)
	}
	var view domain.SavedView
	for _, item := range f.repository.Bootstrap().SavedViews {
		if item.ID == created["id"] {
			view = item
		}
	}
	if view.TeamID != "team_test" || view.Resource != "issues" || view.View != "active" || view.OwnerID != "usr_admin" || view.ShareToken == "" {
		t.Fatalf("stored view: %+v", view)
	}
	var filters []map[string]any
	if err := json.Unmarshal(view.Filters, &filters); err != nil || len(filters) != 4 {
		t.Fatalf("filters: %s %v", view.Filters, err)
	}
	expect := map[string][2]string{"priority": {"is", "1"}, "labels": {"is", "label_type_defect"}, "status": {"is", "state_progress"}, "assignee": {"isNot", ""}}
	for _, filter := range filters {
		want := expect[filter["field"].(string)]
		if filter["operator"] != want[0] || filter["value"] != want[1] || filter["fieldLabel"] == "" || len(filter["values"].([]any)) == 0 {
			t.Fatalf("filter %v, want %v", filter, want)
		}
	}
	updated := mcpObject(t, f, "save_view", map[string]any{"id": created["id"], "name": "Renamed view", "shared": false, "filters": []map[string]any{}})
	if updated["name"] != "Renamed view" || updated["shared"] != false {
		t.Fatalf("update: %v", updated)
	}
	if message := mcpToolError(t, f, "save_view", map[string]any{"name": "Bad", "filters": []map[string]any{{"field": "nope", "values": []string{"x"}}}}); !strings.Contains(message, "enum") {
		t.Fatalf("bad field: %s", message)
	}
	if message := mcpToolError(t, f, "save_view", map[string]any{"name": "Bad", "filters": []map[string]any{{"field": "labels", "values": []string{"Missing"}}}}); !strings.Contains(message, "not found") {
		t.Fatalf("bad label: %s", message)
	}
	if message := mcpToolError(t, f, "save_view", map[string]any{"name": "Bad", "scope": "team"}); !strings.Contains(message, "team is required") {
		t.Fatalf("team scope: %s", message)
	}
	restricted := f.addMCPKey(t, "restricted-view", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "save_view", map[string]any{"id": created["id"], "name": "Hijack"}, "not found")
	guest := f.guestSecret(t)
	mcpDeniedWith(t, f, guest, "save_view", map[string]any{"name": "Guest view"}, "Guests cannot")
}

func TestMCPArchiveAndDeleteIssue(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_issue", map[string]any{"team": "TST", "title": "Archive me"})
	archived := mcpObject(t, f, "save_issue", map[string]any{"id": created["identifier"], "archived": true})
	if archived["archivedAt"] == nil {
		t.Fatalf("archive receipt: %v", archived)
	}
	if issue, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, created["id"].(string)); err != nil || issue.ArchivedAt == nil {
		t.Fatalf("not archived: %v", err)
	}
	mcpObject(t, f, "save_issue", map[string]any{"id": created["identifier"], "archived": false})
	if issue, _ := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, created["id"].(string)); issue.ArchivedAt != nil {
		t.Fatal("not unarchived")
	}
	if message := mcpToolError(t, f, "save_issue", map[string]any{"team": "TST", "title": "x", "archived": true}); !strings.Contains(message, "existing issue") {
		t.Fatalf("archive on create: %s", message)
	}

	deleted := mcpObject(t, f, "delete_issue", map[string]any{"id": created["identifier"]})
	if deleted["deleted"] != true || deleted["identifier"] != created["identifier"] {
		t.Fatalf("delete receipt: %v", deleted)
	}
	if _, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, created["id"].(string)); err == nil {
		t.Fatal("issue still readable after delete")
	}
	if !slices.ContainsFunc(f.repository.TrashSnapshot(f.data.Workspace.URLKey), func(item domain.TrashEntry) bool { return item.ResourceID == created["id"] }) {
		t.Fatal("deleted issue not in trash")
	}
	if message := mcpToolError(t, f, "delete_issue", map[string]any{"id": "TST-999999"}); !strings.Contains(message, "not found") {
		t.Fatalf("missing issue: %s", message)
	}
	restricted := f.addMCPKey(t, "restricted-issue", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "delete_issue", map[string]any{"id": "TST-1"}, "not found")
	mcpDeniedWith(t, f, restricted, "save_issue", map[string]any{"id": "TST-1", "archived": true}, "not found")
	createOnly := f.addMCPKey(t, "create-only", "usr_admin", []string{"read", "create_issues"}, nil)
	mcpDeniedWith(t, f, createOnly, "delete_issue", map[string]any{"id": "TST-1"}, "write scope")
	if _, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, "issue_1"); err != nil {
		t.Fatal("denied delete removed the issue")
	}
}

func TestMCPSaveReactionIsIdempotent(t *testing.T) {
	f := newMCPContractFixture(t)
	issue := f.data.Issues[0]
	for range 2 {
		result := mcpObject(t, f, "save_reaction", map[string]any{"issueId": issue.Identifier, "emoji": "👍"})
		if result["reacted"] != true || result["type"] != "issue" {
			t.Fatalf("add: %v", result)
		}
	}
	stored, _ := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, issue.ID)
	if got := stored.Reactions["👍"]; len(got) != 1 || got[0] != "usr_admin" {
		t.Fatalf("reactions after double add: %v", stored.Reactions)
	}
	mcpObject(t, f, "save_reaction", map[string]any{"issueId": issue.ID, "emoji": "👍", "remove": true})
	if stored, _ = f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, issue.ID); len(stored.Reactions["👍"]) != 0 {
		t.Fatalf("reaction not removed: %v", stored.Reactions)
	}
	comment := mcpObject(t, f, "save_comment", map[string]any{"issueId": issue.ID, "body": "React to me"})
	if result := mcpObject(t, f, "save_reaction", map[string]any{"commentId": comment["id"], "emoji": "🎉"}); result["type"] != "issueComment" || result["reacted"] != true {
		t.Fatalf("comment reaction: %v", result)
	}
	update := mcpObject(t, f, "save_status_update", map[string]any{"type": "project", "project": f.data.Projects[0].ID, "body": "On track"})
	if result := mcpObject(t, f, "save_reaction", map[string]any{"statusUpdateId": update["id"], "emoji": "🚀"}); result["type"] != "projectUpdate" || len(result["reactions"].(map[string]any)["🚀"].([]any)) != 1 {
		t.Fatalf("update reaction: %v", result)
	}
	if message := mcpToolError(t, f, "save_reaction", map[string]any{"emoji": "👍"}); !strings.Contains(message, "exactly one") {
		t.Fatalf("no target: %s", message)
	}
	if message := mcpToolError(t, f, "save_reaction", map[string]any{"emoji": "👍", "commentId": "comment_missing"}); !strings.Contains(message, "not found") {
		t.Fatalf("missing comment: %s", message)
	}
	restricted := f.addMCPKey(t, "restricted-reaction", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "save_reaction", map[string]any{"issueId": issue.ID, "emoji": "👎"}, "not found")
	mcpDeniedWith(t, f, restricted, "save_reaction", map[string]any{"statusUpdateId": update["id"], "emoji": "👎"}, "not found", "outside your teams")
}

func TestMCPSaveSubscription(t *testing.T) {
	f := newMCPContractFixture(t)
	issue := f.data.Issues[0]
	result := mcpObject(t, f, "save_subscription", map[string]any{"issueId": issue.Identifier, "user": "Test member", "subscribed": true})
	if result["subscribed"] != true || result["identifier"] != issue.Identifier {
		t.Fatalf("issue subscribe: %v", result)
	}
	stored, _ := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, issue.ID)
	if !slices.Contains(stored.SubscriberIDs, "usr_member") {
		t.Fatalf("subscriber missing: %v", stored.SubscriberIDs)
	}
	mcpObject(t, f, "save_subscription", map[string]any{"issueId": issue.ID, "user": "usr_member", "subscribed": false})
	if stored, _ = f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, issue.ID); slices.Contains(stored.SubscriberIDs, "usr_member") {
		t.Fatal("subscriber not removed")
	}
	project := f.data.Projects[0]
	mcpObject(t, f, "save_subscription", map[string]any{"projectId": project.Name, "subscribed": true})
	subscribed := func() bool {
		return slices.ContainsFunc(f.repository.Bootstrap().Subscriptions, func(item domain.Subscription) bool {
			return item.ResourceType == "project" && item.ResourceID == project.ID && item.UserID == "usr_admin"
		})
	}
	if !subscribed() {
		t.Fatal("project subscription missing")
	}
	mcpObject(t, f, "save_subscription", map[string]any{"projectId": project.ID, "subscribed": false})
	if subscribed() {
		t.Fatal("project subscription not removed")
	}
	if message := mcpToolError(t, f, "save_subscription", map[string]any{"subscribed": true}); !strings.Contains(message, "exactly one") {
		t.Fatalf("no target: %s", message)
	}
	if message := mcpToolError(t, f, "save_subscription", map[string]any{"projectId": project.ID, "user": "Test member", "subscribed": true}); !strings.Contains(message, "issue subscriptions") {
		t.Fatalf("user on project: %s", message)
	}
	restricted := f.addMCPKey(t, "restricted-subscription", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "save_subscription", map[string]any{"projectId": project.ID, "subscribed": true}, "not found")
	mcpDeniedWith(t, f, restricted, "save_subscription", map[string]any{"issueId": issue.ID, "subscribed": true}, "not found")
}

func TestMCPSaveDocument(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Launch spec", "content": "# Goals\n\nShip **fast** with `care`.\n\n- [ ] draft\n- [x] review\n\n1. one\n2. two", "project": f.data.Projects[0].Name, "team": "TST"})
	if created["title"] != "Launch spec" || !strings.Contains(created["url"].(string), "/document/") {
		t.Fatalf("receipt: %v", created)
	}
	find := func() domain.Document {
		for _, item := range f.repository.Bootstrap().Documents {
			if item.ID == created["id"] {
				return item
			}
		}
		t.Fatal("document missing")
		return domain.Document{}
	}
	document := find()
	nodes, _ := document.ContentData["content"].([]any)
	if !strings.HasPrefix(document.Content, "# Goals") || len(nodes) != 4 || nodes[0].(map[string]any)["type"] != "heading" || nodes[2].(map[string]any)["type"] != "taskList" || nodes[3].(map[string]any)["type"] != "orderedList" {
		t.Fatalf("content: %q %v", document.Content, document.ContentData)
	}
	if !slices.Equal(document.ProjectIDs, []string{f.data.Projects[0].ID}) || !slices.Equal(document.TeamIDs, []string{"team_test"}) {
		t.Fatalf("parents: %v %v", document.ProjectIDs, document.TeamIDs)
	}
	mcpObject(t, f, "save_document", map[string]any{"id": created["slugId"], "patch": []map[string]any{{"op": "replace", "old_string": "fast", "new_string": "safely"}}, "project": nil})
	if document = find(); !strings.Contains(document.Content, "**safely**") || len(document.ProjectIDs) != 0 {
		t.Fatalf("patch/detach: %q %v", document.Content, document.ProjectIDs)
	}
	if message := mcpToolError(t, f, "save_document", map[string]any{"content": "body"}); !strings.Contains(message, "title is required") {
		t.Fatalf("missing title: %s", message)
	}
	if message := mcpToolError(t, f, "save_document", map[string]any{"id": created["id"], "content": "a", "patch": []map[string]any{{"op": "append", "text": "b"}}}); !strings.Contains(message, "not both") {
		t.Fatalf("content+patch: %s", message)
	}
	restricted := f.addMCPKey(t, "restricted-document", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "save_document", map[string]any{"title": "Scoped", "team": "TST"}, "not found")
	mcpDeniedWith(t, f, restricted, "save_document", map[string]any{"id": created["id"], "title": "Hijack"}, "not found")
}

func TestMCPSaveTemplates(t *testing.T) {
	f := newMCPContractFixture(t)
	issue := mcpObject(t, f, "save_template", map[string]any{"type": "issue", "name": "Bug report", "team": "TST", "title": "Bug: ", "body": "## Steps", "priority": 2, "labels": []string{"Defect"}, "state": "Todo"})
	if issue["teamId"] != "team_test" || issue["created"] != true {
		t.Fatalf("issue template: %v", issue)
	}
	renamed := mcpObject(t, f, "save_template", map[string]any{"type": "issue", "id": "Bug report", "name": "Bug"})
	if renamed["name"] != "Bug" || renamed["id"] != issue["id"] {
		t.Fatalf("rename: %v", renamed)
	}
	project := mcpObject(t, f, "save_template", map[string]any{"type": "project", "name": "Launch", "projectName": "New launch", "teams": []string{"TST"}, "labels": []string{"Product"}})
	if project["type"] != "project" {
		t.Fatalf("project template: %v", project)
	}
	document := mcpObject(t, f, "save_template", map[string]any{"type": "document", "name": "RFC", "team": "TST", "content": "## Summary"})
	data := f.repository.Bootstrap()
	index := slices.IndexFunc(data.IssueTemplates, func(item domain.IssueTemplate) bool { return item.ID == issue["id"] })
	if index < 0 || data.IssueTemplates[index].Body != "## Steps" || data.IssueTemplates[index].StateID != "state_todo" || !slices.Equal(data.IssueTemplates[index].LabelIDs, []string{"label_type_defect"}) {
		t.Fatalf("stored issue template: %+v", data.IssueTemplates)
	}
	docIndex := slices.IndexFunc(data.DocumentTemplates, func(item domain.DocumentTemplate) bool { return item.ID == document["id"] })
	if docIndex < 0 || data.DocumentTemplates[docIndex].Content != "## Summary" || data.DocumentTemplates[docIndex].ContentData["content"].([]any)[0].(map[string]any)["type"] != "heading" {
		t.Fatalf("stored document template: %+v", data.DocumentTemplates)
	}
	if message := mcpToolError(t, f, "save_template", map[string]any{"type": "document", "name": "No team"}); !strings.Contains(message, "team is required") {
		t.Fatalf("document without team: %s", message)
	}
	if message := mcpToolError(t, f, "save_template", map[string]any{"type": "issue"}); !strings.Contains(message, "name is required") {
		t.Fatalf("missing name: %s", message)
	}
	guest := f.guestSecret(t)
	mcpDeniedWith(t, f, guest, "save_template", map[string]any{"type": "issue", "name": "Guest"}, "cannot perform this action")
	restricted := f.addMCPKey(t, "restricted-template", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "save_template", map[string]any{"type": "issue", "id": issue["id"], "name": "Hijack"}, "not found")
}

func TestMCPTriageIssueActions(t *testing.T) {
	f := newMCPContractFixture(t)
	if message := mcpToolError(t, f, "triage_issue", map[string]any{"id": "TST-1", "action": "accept"}); !strings.Contains(message, "does not use triage") {
		t.Fatalf("triage disabled: %s", message)
	}
	mcpObject(t, f, "save_team", map[string]any{"id": "TST", "triageEnabled": true})
	newTriage := func(title string) map[string]any {
		return mcpObject(t, f, "save_issue", map[string]any{"team": "TST", "title": title, "state": "Backlog"})
	}
	accepted := mcpObject(t, f, "triage_issue", map[string]any{"id": newTriage("Accept me")["identifier"], "action": "accept", "priority": 2, "comment": "Looks valid"})
	if accepted["state"].(map[string]any)["name"] != "Todo" || accepted["priority"] != float64(2) {
		t.Fatalf("accept: %v", accepted)
	}
	stored, _ := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, accepted["id"].(string))
	if stored.TriagedAt == nil {
		t.Fatal("accept did not mark the issue triaged")
	}
	comments := mcpObject(t, f, "list_comments", map[string]any{"issueId": accepted["id"]})
	if !strings.Contains(string(mustJSON(t, comments)), "Looks valid") {
		t.Fatalf("triage comment missing: %v", comments)
	}
	if declined := mcpObject(t, f, "triage_issue", map[string]any{"id": newTriage("Decline me")["id"], "action": "decline"}); declined["state"].(map[string]any)["name"] != "Canceled" {
		t.Fatalf("decline: %v", declined)
	}
	duplicate := mcpObject(t, f, "triage_issue", map[string]any{"id": newTriage("Dupe me")["id"], "action": "duplicate", "duplicateOf": "TST-1"})
	if duplicate["state"].(map[string]any)["name"] != "Duplicate" || duplicate["duplicateOf"].(map[string]any)["identifier"] != "TST-1" {
		t.Fatalf("duplicate: %v", duplicate)
	}
	if dupe, _ := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, duplicate["id"].(string)); !slices.ContainsFunc(dupe.Relations, func(relation domain.IssueRelation) bool { return relation.Type == "duplicate" }) {
		t.Fatalf("duplicate relation missing: %v", dupe.Relations)
	}
	if snoozed := mcpObject(t, f, "triage_issue", map[string]any{"id": newTriage("Snooze me")["id"], "action": "snooze", "snoozedUntil": "P1W"}); snoozed["snoozedUntil"] == nil {
		t.Fatalf("snooze: %v", snoozed)
	}
	if message := mcpToolError(t, f, "triage_issue", map[string]any{"id": "TST-1", "action": "accept"}); !strings.Contains(message, "not in triage") {
		t.Fatalf("not in triage: %s", message)
	}
	if message := mcpToolError(t, f, "triage_issue", map[string]any{"id": newTriage("No target")["id"], "action": "duplicate"}); !strings.Contains(message, "duplicateOf is required") {
		t.Fatalf("duplicate without target: %s", message)
	}
	pending := newTriage("Restricted")
	restricted := f.addMCPKey(t, "restricted-triage", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "triage_issue", map[string]any{"id": pending["id"], "action": "decline"}, "not found")
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMCPUpdateNotification(t *testing.T) {
	f := newMCPContractFixture(t)
	now := time.Now().UTC()
	issue := f.data.Issues[0]
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.notifications", issue.ID, nil, func(next *domain.Bootstrap) error {
		for _, item := range []domain.Notification{
			{ID: "inbox-one", RecipientID: "usr_admin", Type: "issueComment", SourceType: "issue", SourceID: issue.ID, IssueID: issue.ID, Actor: next.Viewer, CreatedAt: now, UpdatedAt: now},
			{ID: "inbox-two", RecipientID: "usr_admin", Type: "issueComment", SourceType: "issue", SourceID: issue.ID, IssueID: issue.ID, Actor: next.Viewer, CreatedAt: now, UpdatedAt: now},
			{ID: "inbox-other", RecipientID: "usr_member", Type: "issueComment", SourceType: "issue", SourceID: issue.ID, IssueID: issue.ID, Actor: next.Viewer, CreatedAt: now, UpdatedAt: now},
		} {
			next.Notifications = append(next.Notifications, item)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := mcpObject(t, f, "update_notification", map[string]any{"id": "inbox-one", "read": true})
	if read["read"] != true {
		t.Fatalf("read: %v", read)
	}
	snoozed := mcpObject(t, f, "update_notification", map[string]any{"id": "inbox-one", "snoozedUntil": "PT3H", "archived": true})
	if snoozed["snoozedUntil"] == nil || snoozed["archivedAt"] == nil {
		t.Fatalf("snooze/archive: %v", snoozed)
	}
	if unsnoozed := mcpObject(t, f, "update_notification", map[string]any{"id": "inbox-one", "snoozedUntil": nil}); unsnoozed["snoozedUntil"] != nil {
		t.Fatalf("unsnooze: %v", unsnoozed)
	}
	if all := mcpObject(t, f, "update_notification", map[string]any{"markAllRead": true}); all["markedAllRead"] != true {
		t.Fatalf("mark all: %v", all)
	}
	record, err := f.repository.NotificationRecord(t.Context(), f.data.Workspace.URLKey, "usr_admin", "inbox-two")
	if err != nil || record.ReadAt == nil {
		t.Fatalf("mark all read missed inbox-two: %+v %v", record, err)
	}
	if other, _ := f.repository.NotificationRecord(t.Context(), f.data.Workspace.URLKey, "usr_member", "inbox-other"); other.ReadAt != nil {
		t.Fatal("mark all read touched another user's inbox")
	}
	if message := mcpToolError(t, f, "update_notification", map[string]any{"id": "inbox-one"}); !strings.Contains(message, "provide read") {
		t.Fatalf("empty update: %s", message)
	}
	if message := mcpToolError(t, f, "update_notification", map[string]any{"read": true}); !strings.Contains(message, "id is required") {
		t.Fatalf("missing id: %s", message)
	}
	if message := mcpToolError(t, f, "update_notification", map[string]any{"id": "inbox-other", "read": true}); !strings.Contains(message, "outside your teams") && !strings.Contains(message, "not found") {
		t.Fatalf("another user's notification: %s", message)
	}
}

func TestMCPSaveAgentSkill(t *testing.T) {
	f := newMCPContractFixture(t)
	personal := mcpObject(t, f, "save_agent_skill", map[string]any{"name": "Release notes", "instructions": "Summarize shipped issues."})
	if personal["scope"] != "personal" || personal["created"] != true {
		t.Fatalf("personal: %v", personal)
	}
	edited := mcpObject(t, f, "save_agent_skill", map[string]any{"id": "Release notes", "instructions": "Summarize shipped issues by team."})
	if edited["id"] != personal["id"] || edited["name"] != "Release notes" || edited["instructions"] != "Summarize shipped issues by team." {
		t.Fatalf("edit: %v", edited)
	}
	team := mcpObject(t, f, "save_agent_skill", map[string]any{"team": "TST", "name": "Triage", "instructions": "Label new bugs."})
	disabled := mcpObject(t, f, "save_agent_skill", map[string]any{"team": "TST", "id": team["id"], "enabled": false})
	if disabled["enabled"] != false || disabled["created"] != false {
		t.Fatalf("team disable: %v", disabled)
	}
	data := f.repository.Bootstrap()
	if skills := data.TeamSettings["team_test"].AgentSkills; len(skills) != 1 || skills[0].Name != "Triage" || skills[0].Enabled {
		t.Fatalf("team skills: %+v", skills)
	}
	if !slices.ContainsFunc(data.AgentSkills, func(skill domain.PersonalAgentSkill) bool {
		return skill.ID == personal["id"] && skill.UserID == "usr_admin"
	}) {
		t.Fatal("personal skill not stored")
	}
	if message := mcpToolError(t, f, "save_agent_skill", map[string]any{"name": "Empty"}); !strings.Contains(message, "name and instructions are required") {
		t.Fatalf("missing instructions: %s", message)
	}
	if message := mcpToolError(t, f, "save_agent_skill", map[string]any{"name": "x", "instructions": "y", "enabled": true}); !strings.Contains(message, "team skills") {
		t.Fatalf("enabled on personal: %s", message)
	}
	guest := f.guestSecret(t)
	mcpDeniedWith(t, f, guest, "save_agent_skill", map[string]any{"team": "TST", "name": "Guest", "instructions": "Nope"}, "Guests", "cannot", "not found")
	restricted := f.addMCPKey(t, "restricted-skill", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "save_agent_skill", map[string]any{"team": "TST", "name": "Scoped", "instructions": "Nope"}, "not found")
}

func TestMCPSaveLoop(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_loop", map[string]any{"name": "Weekly triage", "instructions": "Review the triage inbox.", "interval": 1, "unit": "week", "time": "09:30", "timezone": "Europe/Berlin"})
	config := created["triggerConfig"].(map[string]any)
	if created["trigger"] != "schedule" || config["unit"] != "week" || config["time"] != "09:30" || config["timezone"] != "Europe/Berlin" || created["enabled"] != false || created["status"] != "draft" || config["startDate"] == nil {
		t.Fatalf("created: %v", created)
	}
	updated := mcpObject(t, f, "save_loop", map[string]any{"id": "Weekly triage", "trigger": "issue", "action": "created", "teams": []string{"TST"}, "enabled": false})
	config = updated["triggerConfig"].(map[string]any)
	if updated["trigger"] != "issue" || config["event"] != "created" || config["unit"] != nil || updated["enabled"] != false || len(config["teamIds"].([]any)) != 1 {
		t.Fatalf("updated: %v", updated)
	}
	published := mcpObject(t, f, "save_loop", map[string]any{"id": "Weekly triage", "event": "status", "value": "started", "filters": []any{map[string]any{"field": "assignee", "operator": "is", "value": nil}}, "publish": true})
	config = published["triggerConfig"].(map[string]any)
	if published["status"] != "published" || published["enabled"] != true || published["published"] != true || config["event"] != "status" || config["value"] != "started" || len(config["filters"].([]any)) != 1 {
		t.Fatalf("published: %v", published)
	}
	if message := mcpToolError(t, f, "save_loop", map[string]any{"id": "Weekly triage", "status": "draft"}); !strings.Contains(message, "back to draft") {
		t.Fatalf("unpublish: %s", message)
	}
	if loops := f.repository.Bootstrap().Loops; len(loops) != 1 || loops[0].Instructions != "Review the triage inbox." || loops[0].TriggerType != "issue" {
		t.Fatalf("stored loops: %+v", loops)
	}
	if message := mcpToolError(t, f, "save_loop", map[string]any{"id": created["id"], "time": "10:00"}); !strings.Contains(message, "schedule triggers") {
		t.Fatalf("schedule key on event trigger: %s", message)
	}
	if message := mcpToolError(t, f, "save_loop", map[string]any{"name": "Bad", "time": "25:99"}); !strings.Contains(message, "HH:MM") {
		t.Fatalf("bad time: %s", message)
	}
	if message := mcpToolError(t, f, "save_loop", map[string]any{"instructions": "No name"}); !strings.Contains(message, "name is required") {
		t.Fatalf("missing name: %s", message)
	}
	guest := f.guestSecret(t)
	mcpDeniedWith(t, f, guest, "save_loop", map[string]any{"name": "Guest loop"}, "cannot perform this action")
}

func TestMCPRouteAuthorizationMatchesHTTP(t *testing.T) {
	f := newMCPContractFixture(t)
	actor := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer, APIKey: domain.APIKey{ID: "restricted-route", Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{}}}
	_, err := f.service.invokeMCPRoute(t.Context(), actor, http.MethodPatch, "/api/teams/team_test/settings", map[string]string{"id": "team_test"}, map[string]any{"description": "hijack"}, f.service.updateStructuredTeamSettings)
	if err == nil || !strings.Contains(err.Error(), "outside your teams") {
		t.Fatalf("restricted key reached a team outside its scope: %v", err)
	}
	actor.APIKey = domain.APIKey{ID: "read-only-route", Scopes: []string{"read"}}
	if _, err := f.service.invokeMCPRoute(t.Context(), actor, http.MethodPost, "/api/labels", nil, map[string]any{"name": "x"}, f.service.createWorkspaceLabel); err == nil {
		t.Fatal("read-only key wrote a label")
	}
	if f.repository.Bootstrap().TeamSettings["team_test"].Description == "hijack" {
		t.Fatal("denied request mutated settings")
	}
}

func TestMCPMarkdownDocument(t *testing.T) {
	document := mcpMarkdownDocument("## Title\n\nA [link](https://flow.test) and *em* ~~gone~~\nnext line\n\n> quoted\n\n```go\nfmt.Println()\n```\n\n---\n\n- one\n- two")
	raw := string(mustJSON(t, document))
	for _, want := range []string{`"level":2`, `"href":"https://flow.test"`, `"type":"italic"`, `"type":"strike"`, `"type":"hardBreak"`, `"type":"blockquote"`, `"language":"go"`, `"type":"horizontalRule"`, `"type":"bulletList"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("markdown document missing %s: %s", want, raw)
		}
	}
	if empty := mcpMarkdownDocument(""); len(empty["content"].([]any)) != 1 {
		t.Fatalf("empty document: %v", empty)
	}
}

// The editor offers Heading 1-4 (Linear's text-style menu); deeper Markdown
// headings clamp to 4 and Heading 4 round-trips back to "####".
func TestMCPMarkdownDocumentHeadingFour(t *testing.T) {
	levels := []int{}
	for _, block := range mcpMarkdownDocument("#### Four\n\n###### Six")["content"].([]any) {
		levels = append(levels, block.(map[string]any)["attrs"].(map[string]any)["level"].(int))
	}
	if len(levels) != 2 || levels[0] != 4 || levels[1] != 4 {
		t.Fatalf("heading levels = %v, want [4 4]", levels)
	}
	var decoded map[string]any
	if err := json.Unmarshal(mustJSON(t, mcpMarkdownDocument("#### Four")), &decoded); err != nil {
		t.Fatal(err)
	}
	if text := proseMirrorPlainText(decoded); !strings.Contains(text, "#### Four") {
		t.Fatalf("plain text = %q, want #### Four", text)
	}
}

// save_team's `members` only adds people: listing the current owner among the
// members must not demote them (it used to fail with "a team needs at least
// one owner", or silently demote when the team had several owners). Owner
// changes are explicit via `owners`/`removeOwners`.
func TestMCPSaveTeamMembersKeepOwnersAndOwnerChangesAreExplicit(t *testing.T) {
	f := newMCPContractFixture(t)
	teamID := mcpObject(t, f, "save_team", map[string]any{"name": "Delivery", "key": "DLV"})["id"].(string)
	workspaceID := f.repository.Bootstrap().Workspace.ID
	role := func(userID string) string {
		t.Helper()
		membership, member, err := f.repository.TeamMembership(t.Context(), workspaceID, teamID, userID)
		if err != nil || !member {
			return ""
		}
		return membership.Role
	}
	receipt := mcpObject(t, f, "save_team", map[string]any{"id": "DLV", "members": []string{"me", "Test member"}})
	if role("usr_admin") != "owner" || role("usr_member") != "member" {
		t.Fatalf("roles after adding members: admin=%q member=%q", role("usr_admin"), role("usr_member"))
	}
	if added, _ := receipt["addedMembers"].([]any); len(added) != 1 || added[0] != "usr_member" {
		t.Fatalf("addedMembers should list only newly added people: %v", receipt["addedMembers"])
	}
	// Listing everyone again is a no-op, even with two owners.
	mcpObject(t, f, "save_team", map[string]any{"id": "DLV", "owners": []string{"Test member"}})
	mcpObject(t, f, "save_team", map[string]any{"id": "DLV", "members": []string{"me", "Test member"}})
	if role("usr_admin") != "owner" || role("usr_member") != "owner" {
		t.Fatalf("members re-listed demoted an owner: admin=%q member=%q", role("usr_admin"), role("usr_member"))
	}
	// Hand-over: promote first, then demote, in one call.
	mcpObject(t, f, "save_team", map[string]any{"id": "DLV", "removeOwners": []string{"me"}})
	if role("usr_admin") != "member" || role("usr_member") != "owner" {
		t.Fatalf("removeOwners: admin=%q member=%q", role("usr_admin"), role("usr_member"))
	}
	if message := mcpToolError(t, f, "save_team", map[string]any{"id": "DLV", "removeOwners": []string{"Test member"}}); !strings.Contains(message, "owner") {
		t.Fatalf("removing the last owner should explain why: %s", message)
	}
	if message := mcpToolError(t, f, "save_team", map[string]any{"name": "Delivery 2", "key": "DLV"}); !strings.Contains(message, "already exists") {
		t.Fatalf("duplicate key should say so: %s", message)
	}
}

func TestMCPMarkdownInlineKeepsIntrawordUnderscores(t *testing.T) {
	nodes := mcpMarkdownInlineNodes("use get_issue on each issue, then finish_run with _emphasis_ and __bold__ and snake_case_name")
	var plain strings.Builder
	marked := map[string]string{}
	for _, node := range nodes {
		item := node.(map[string]any)
		text := item["text"].(string)
		plain.WriteString(text)
		if marks, ok := item["marks"].([]any); ok {
			marked[text] = marks[0].(map[string]any)["type"].(string)
		}
	}
	if got := plain.String(); got != "use get_issue on each issue, then finish_run with emphasis and bold and snake_case_name" {
		t.Fatalf("text = %q", got)
	}
	if len(marked) != 2 || marked["emphasis"] != "italic" || marked["bold"] != "bold" {
		t.Fatalf("marks = %v", marked)
	}
}
