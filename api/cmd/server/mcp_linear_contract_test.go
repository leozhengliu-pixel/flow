package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func mcpObject(t *testing.T, f *mcpContractFixture, name string, args map[string]any) map[string]any {
	t.Helper()
	return mcpSuccess(t, f.call(t, name, args)).(map[string]any)
}

func mcpToolError(t *testing.T, f *mcpContractFixture, name string, args map[string]any) string {
	t.Helper()
	response := f.call(t, name, args)
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

func mcpItems(t *testing.T, value map[string]any) []map[string]any {
	t.Helper()
	var items []map[string]any
	if err := jsonClone(value["items"], &items); err != nil {
		t.Fatal(err)
	}
	return items
}

func TestMCPGetIssueReturnsLinearStyleDetail(t *testing.T) {
	f := newMCPContractFixture(t)
	team := f.data.Teams[0]
	parent := mcpObject(t, f, "save_issue", map[string]any{"team": team.Key, "title": "Parent issue"})
	blocked := mcpObject(t, f, "save_issue", map[string]any{"team": team.Key, "title": "Blocked issue"})
	created := mcpObject(t, f, "save_issue", map[string]any{
		"team": team.Key, "title": "Detailed issue", "description": "Body text", "priority": 2, "assignee": "me",
		"project": f.data.Projects[0].ID, "labels": []string{f.data.Labels[0].Name}, "parentId": parent["identifier"],
		"blocks": []string{blocked["identifier"].(string)}, "dueDate": "2026-12-01", "recurrence": "weekly",
	})
	issue := mcpObject(t, f, "get_issue", map[string]any{"id": created["identifier"]})
	if issue["identifier"] != created["identifier"] || issue["title"] != "Detailed issue" || issue["description"] != "Body text" || issue["priorityLabel"] != "High" || issue["priority"] != float64(2) || issue["dueDate"] != "2026-12-01" || issue["recurrence"] != "weekly" {
		t.Fatalf("core fields: %v", issue)
	}
	if url, _ := issue["url"].(string); !strings.HasSuffix(url, "/issue/"+created["identifier"].(string)) {
		t.Fatalf("url: %v", issue["url"])
	}
	if issue["assignee"].(map[string]any)["id"] != f.data.Viewer.ID || issue["project"].(map[string]any)["id"] != f.data.Projects[0].ID || issue["team"].(map[string]any)["key"] != team.Key {
		t.Fatalf("references: %v", issue)
	}
	if labels := issue["labels"].([]any); len(labels) != 1 || labels[0] != f.data.Labels[0].Name {
		t.Fatalf("labels: %v", labels)
	}
	if issue["parent"].(map[string]any)["identifier"] != parent["identifier"] {
		t.Fatalf("parent: %v", issue["parent"])
	}
	if blocks := issue["relations"].(map[string]any)["blocks"].([]any); len(blocks) != 1 || blocks[0] != blocked["identifier"] {
		t.Fatalf("relations: %v", issue["relations"])
	}
	parentDetail := mcpObject(t, f, "get_issue", map[string]any{"id": parent["id"]})
	if children := parentDetail["subIssues"].([]any); len(children) != 1 || children[0].(map[string]any)["identifier"] != created["identifier"] {
		t.Fatalf("sub-issues: %v", parentDetail["subIssues"])
	}
	if blockedDetail := mcpObject(t, f, "get_issue", map[string]any{"id": blocked["id"]}); len(blockedDetail["relations"].(map[string]any)["blockedBy"].([]any)) != 1 {
		t.Fatalf("inverse relation: %v", blockedDetail["relations"])
	}
	if message := mcpToolError(t, f, "get_issue", map[string]any{"id": "NOPE-999"}); !strings.Contains(message, "not found") {
		t.Fatalf("missing issue: %s", message)
	}
}

func TestMCPIssueHistoryDescribesChanges(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_issue", map[string]any{"team": f.data.Teams[0].ID, "title": "History issue"})
	mcpObject(t, f, "save_issue", map[string]any{"id": created["id"], "state": "In Progress", "priority": 1, "assignee": "me"})
	mcpObject(t, f, "save_issue", map[string]any{"id": created["id"], "description": "New description"})
	history := mcpObject(t, f, "list_issue_history", map[string]any{"issueId": created["identifier"]})
	if history["identifier"] != created["identifier"] {
		t.Fatalf("history identity: %v", history)
	}
	items := mcpItems(t, history)
	if len(items) < 3 {
		t.Fatalf("expected creation and two updates: %v", items)
	}
	if items[len(items)-1]["type"] != "issue.created" {
		t.Fatalf("history is not newest first: %v", items)
	}
	summaries := []string{}
	for _, item := range items {
		if item["actor"].(map[string]any)["id"] != f.data.Viewer.ID || item["createdAt"] == "" {
			t.Fatalf("event lacks actor or timestamp: %v", item)
		}
		summaries = append(summaries, item["summary"].(string))
		if changes, _ := item["changes"].(map[string]any); changes["descriptionBefore"] != nil {
			t.Fatal("history leaked description snapshots")
		}
	}
	joined := strings.Join(summaries, " | ")
	for _, want := range []string{"changed status to In Progress", "set priority to Urgent", "assigned to", "edited the description"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("history missing %q: %s", want, joined)
		}
	}
	page := mcpObject(t, f, "list_issue_history", map[string]any{"issueId": created["id"], "limit": 1})
	if len(mcpItems(t, page)) != 1 || page["nextCursor"] == "" {
		t.Fatalf("history pagination: %v", page)
	}
}

func TestMCPProjectActivityAndStatusUpdates(t *testing.T) {
	f := newMCPContractFixture(t)
	project := f.data.Projects[0]
	mcpObject(t, f, "save_project", map[string]any{"id": project.ID, "name": "Renamed for activity"})
	update := mcpObject(t, f, "save_status_update", map[string]any{"type": "project", "project": project.ID, "body": "Slipping a week", "health": "atRisk"})
	if update["health"] != "atRisk" || update["id"] == nil {
		t.Fatalf("status update: %v", update)
	}
	edited := mcpObject(t, f, "save_status_update", map[string]any{"type": "project", "project": project.ID, "id": update["id"], "health": "offTrack"})
	if edited["health"] != "offTrack" {
		t.Fatalf("status update edit: %v", edited)
	}
	stored, err := mcpFindProject(f.repository.Bootstrap(), project.ID)
	if err != nil || stored.Health != "offTrack" {
		t.Fatalf("project health not updated: %v %v", stored.Health, err)
	}
	if message := mcpToolError(t, f, "save_status_update", map[string]any{"type": "project", "project": project.ID}); !strings.Contains(message, "body is required") {
		t.Fatalf("empty update: %s", message)
	}
	activity := mcpObject(t, f, "list_project_activity", map[string]any{"project": project.ID})
	if activity["name"] != "Renamed for activity" {
		t.Fatalf("activity subject: %v", activity["name"])
	}
	types, summaries := []string{}, []string{}
	for _, item := range mcpItems(t, activity) {
		types = append(types, item["type"].(string))
		summaries = append(summaries, item["summary"].(string))
	}
	if !slices.Contains(types, "project.update_posted") || !slices.Contains(types, "project.created") || !strings.Contains(strings.Join(summaries, "|"), "changed name to Renamed for activity") {
		t.Fatalf("project activity: %v %v", types, summaries)
	}
	initiative := mcpObject(t, f, "save_initiative", map[string]any{"name": "Status initiative"})
	if saved := mcpObject(t, f, "save_status_update", map[string]any{"type": "initiative", "initiative": initiative["id"], "body": "On track", "health": "onTrack"}); saved["health"] != "onTrack" {
		t.Fatalf("initiative update: %v", saved)
	}
}

func TestMCPSearchIssuesMergesQueries(t *testing.T) {
	f := newMCPContractFixture(t)
	team := f.data.Teams[0].ID
	importing := mcpObject(t, f, "save_issue", map[string]any{"team": team, "title": "Import customer spreadsheets", "description": "Bring data in from CSV"})
	migrating := mcpObject(t, f, "save_issue", map[string]any{"team": team, "title": "Database migration runbook"})
	result := mcpObject(t, f, "search_issues", map[string]any{"queries": []string{"import data", "data migration"}})
	found := map[string]map[string]any{}
	for _, item := range mcpItems(t, result) {
		found[item["identifier"].(string)] = item
		if item["url"] == "" || item["status"] == "" || item["score"] == nil {
			t.Fatalf("search result fields: %v", item)
		}
	}
	first, second := found[importing["identifier"].(string)], found[migrating["identifier"].(string)]
	if first == nil || second == nil {
		t.Fatalf("search missed issues: %v", found)
	}
	if !slices.Contains(first["matchedQueries"].([]any), any("import data")) || !slices.Contains(second["matchedQueries"].([]any), any("data migration")) {
		t.Fatalf("matched queries: %v %v", first["matchedQueries"], second["matchedQueries"])
	}
	scoped := mcpObject(t, f, "search_issues", map[string]any{"queries": []string{"migration"}, "project": f.data.Projects[0].ID})
	for _, item := range mcpItems(t, scoped) {
		if item["identifier"] == migrating["identifier"] {
			t.Fatal("project filter ignored")
		}
	}
	if message := mcpToolError(t, f, "search_issues", map[string]any{"queries": []string{"a", "b", "c", "d", "e", "f"}}); message == "" {
		t.Fatal("more than five queries accepted")
	}
}

func TestMCPNotificationsAndReminders(t *testing.T) {
	f := newMCPContractFixture(t)
	issue := f.data.Issues[0]
	now := time.Now().UTC()
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.notifications", issue.ID, nil, func(data *domain.Bootstrap) error {
		data.Notifications = append(data.Notifications,
			domain.Notification{ID: "inbox-unread", RecipientID: data.Viewer.ID, Type: "issueComment", SourceType: "issue", SourceID: issue.ID, IssueID: issue.ID, Actor: data.Viewer, Category: "comments", CreatedAt: now, UpdatedAt: now},
			domain.Notification{ID: "inbox-read", RecipientID: data.Viewer.ID, Type: "issueAssignedToYou", SourceType: "issue", SourceID: issue.ID, IssueID: issue.ID, Actor: data.Viewer, Category: "assignments", ReadAt: &now, CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
			domain.Notification{ID: "someone-else", RecipientID: "another-user", Type: "issueComment", SourceType: "issue", SourceID: issue.ID, IssueID: issue.ID, Actor: data.Viewer, CreatedAt: now, UpdatedAt: now},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	all := mcpObject(t, f, "list_notifications", nil)
	ids := []string{}
	for _, item := range mcpItems(t, all) {
		ids = append(ids, item["id"].(string))
		if item["id"] == "inbox-unread" {
			entity := item["entity"].(map[string]any)
			if entity["identifier"] != issue.Identifier || item["read"] != false || item["actor"] == nil {
				t.Fatalf("notification view: %v", item)
			}
		}
	}
	if !slices.Contains(ids, "inbox-unread") || !slices.Contains(ids, "inbox-read") || slices.Contains(ids, "someone-else") {
		t.Fatalf("inbox scope: %v", ids)
	}
	unread := mcpObject(t, f, "list_notifications", map[string]any{"unreadOnly": true})
	for _, item := range mcpItems(t, unread) {
		if item["read"] != false {
			t.Fatalf("unreadOnly returned read item: %v", item)
		}
	}
	reminder := mcpObject(t, f, "create_reminder", map[string]any{"issue": issue.Identifier, "remindAt": "PT2H"})
	if reminder["target"].(map[string]any)["identifier"] != issue.Identifier {
		t.Fatalf("reminder target: %v", reminder)
	}
	remindAt, err := time.Parse(time.RFC3339, reminder["remindAt"].(string))
	if err != nil || remindAt.Before(now.Add(110*time.Minute)) {
		t.Fatalf("reminder time: %v %v", reminder["remindAt"], err)
	}
	snoozed := mcpObject(t, f, "list_notifications", map[string]any{"includeSnoozed": true})
	if !slices.ContainsFunc(mcpItems(t, snoozed), func(item map[string]any) bool { return item["id"] == reminder["id"] && item["type"] == "issueReminder" }) {
		t.Fatal("pending reminder not listed with includeSnoozed")
	}
	if slices.ContainsFunc(mcpItems(t, mcpObject(t, f, "list_notifications", nil)), func(item map[string]any) bool { return item["id"] == reminder["id"] }) {
		t.Fatal("snoozed reminder listed before it is due")
	}
	mcpObject(t, f, "create_reminder", map[string]any{"project": f.data.Projects[0].ID, "remindAt": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)})
	if message := mcpToolError(t, f, "create_reminder", map[string]any{"issue": issue.ID, "remindAt": "2020-01-01"}); !strings.Contains(message, "future") {
		t.Fatalf("past reminder: %s", message)
	}
	if message := mcpToolError(t, f, "create_reminder", map[string]any{"issue": issue.ID, "project": f.data.Projects[0].ID, "remindAt": "P1D"}); !strings.Contains(message, "exactly one") {
		t.Fatalf("two targets: %s", message)
	}
}

func TestMCPViewsTemplatesAndCustomersRespectVisibility(t *testing.T) {
	f := newMCPContractFixture(t)
	team := f.data.Teams[0]
	now := time.Now().UTC()
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.catalog", "", nil, func(data *domain.Bootstrap) error {
		data.SavedViews = append(data.SavedViews,
			domain.SavedView{ID: "view-team", Name: "Team bugs", Resource: "issues", Scope: "team", TeamID: team.ID, View: "list", Filters: json.RawMessage(`{"labels":["Bug"]}`), CreatedAt: now, UpdatedAt: now},
			domain.SavedView{ID: "view-mine", Name: "My focus", Resource: "issues", Scope: "personal", OwnerID: data.Viewer.ID, View: "list", CreatedAt: now, UpdatedAt: now},
			domain.SavedView{ID: "view-other", Name: "Private to someone else", Resource: "issues", Scope: "personal", OwnerID: "another-user", View: "list", CreatedAt: now, UpdatedAt: now},
		)
		data.IssueTemplates = append(data.IssueTemplates, domain.IssueTemplate{ID: "template-bug", TeamID: team.ID, Name: "Bug report", Title: "Bug: ", LabelIDs: []string{}, CreatedAt: now, UpdatedAt: now})
		data.ProjectTemplates = append(data.ProjectTemplates, domain.ProjectTemplate{ID: "template-launch", Name: "Launch", TeamIDs: []string{team.ID}, LabelIDs: []string{}, Milestones: []domain.TemplateMilestone{{ID: "m1", Name: "Beta"}}, CreatedAt: now, UpdatedAt: now})
		data.Customers = append(data.Customers, domain.Customer{ID: "customer-acme", Name: "Acme Corp", Status: "active", Tier: "enterprise", OwnerID: data.Viewer.ID, Domains: []string{"acme.test"}, CreatedAt: now, UpdatedAt: now})
		data.CustomerRequests = append(data.CustomerRequests, domain.CustomerRequest{ID: "request-import", CustomerID: "customer-acme", Body: "Needs CSV import", Source: "manual", Creator: data.Viewer, IssueID: f.data.Issues[0].ID, Attachments: []domain.Attachment{}, CreatedAt: now, UpdatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	views := mcpItems(t, mcpObject(t, f, "list_views", nil))
	names := []string{}
	for _, view := range views {
		names = append(names, view["name"].(string))
	}
	if !slices.Contains(names, "Team bugs") || !slices.Contains(names, "My focus") || slices.Contains(names, "Private to someone else") {
		t.Fatalf("views: %v", names)
	}
	if filtered := mcpItems(t, mcpObject(t, f, "list_views", map[string]any{"team": team.Key, "query": "bugs"})); len(filtered) != 1 || filtered[0]["filters"] == nil {
		t.Fatalf("view filters: %v", filtered)
	}
	templates := mcpItems(t, mcpObject(t, f, "list_templates", nil))
	if !slices.ContainsFunc(templates, func(item map[string]any) bool { return item["id"] == "template-bug" && item["type"] == "issue" }) || !slices.ContainsFunc(templates, func(item map[string]any) bool { return item["id"] == "template-launch" && item["type"] == "project" }) {
		t.Fatalf("templates: %v", templates)
	}
	for _, item := range mcpItems(t, mcpObject(t, f, "list_templates", map[string]any{"type": "project"})) {
		if item["type"] != "project" {
			t.Fatalf("type filter: %v", item)
		}
	}
	customers := mcpItems(t, mcpObject(t, f, "list_customers", map[string]any{"query": "acme", "includeRequests": true}))
	if len(customers) != 1 || customers[0]["requestCount"] != float64(1) || customers[0]["tier"] != "enterprise" {
		t.Fatalf("customers: %v", customers)
	}
	requests := customers[0]["requests"].([]any)
	if len(requests) != 1 || requests[0].(map[string]any)["issue"].(map[string]any)["identifier"] != f.data.Issues[0].Identifier {
		t.Fatalf("customer requests: %v", requests)
	}
}

func TestMCPSaveDraftCreatesPrivateComposerDrafts(t *testing.T) {
	f := newMCPContractFixture(t)
	team := f.data.Teams[0]
	draft := mcpObject(t, f, "save_draft", map[string]any{"title": "Drafted issue", "body": "Line one\nLine two", "team": team.Key, "priority": 2, "assignee": "me", "labels": []string{f.data.Labels[0].Name}})
	metadata := draft["metadata"].(map[string]any)
	if draft["type"] != "issue" || draft["title"] != "Drafted issue" || metadata["teamId"] != team.ID || metadata["priority"] != float64(2) || metadata["assigneeId"] != f.data.Viewer.ID {
		t.Fatalf("issue draft: %v", draft)
	}
	if description := metadata["description"].(map[string]any); description["markdown"] != "Line one\nLine two" || len(description["document"].(map[string]any)["content"].([]any)) != 2 {
		t.Fatalf("composer description: %v", metadata["description"])
	}
	updated := mcpObject(t, f, "save_draft", map[string]any{"id": draft["id"], "title": "Renamed draft"})
	if updated["id"] != draft["id"] || updated["title"] != "Renamed draft" || updated["metadata"].(map[string]any)["teamId"] != team.ID {
		t.Fatalf("draft update lost fields: %v", updated)
	}
	update := mcpObject(t, f, "save_draft", map[string]any{"type": "projectUpdate", "project": f.data.Projects[0].ID, "body": "Draft update", "health": "atRisk"})
	if update["type"] != "project_update" || update["resourceId"] != f.data.Projects[0].ID || update["metadata"].(map[string]any)["health"] != "atRisk" {
		t.Fatalf("project update draft: %v", update)
	}
	stored := f.repository.Bootstrap().Drafts
	if !slices.ContainsFunc(stored, func(item domain.Draft) bool { return item.ID == draft["id"] && item.UserID == f.data.Viewer.ID }) {
		t.Fatal("draft not persisted for the viewer")
	}
	if message := mcpToolError(t, f, "save_draft", map[string]any{"type": "projectUpdate", "body": "No project"}); !strings.Contains(message, "project is required") {
		t.Fatalf("project update draft without project: %s", message)
	}
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.draft", "", nil, func(data *domain.Bootstrap) error {
		data.Drafts = append(data.Drafts, domain.Draft{ID: "draft-other", UserID: "another-user", Type: "issue", Title: "Theirs"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if message := mcpToolError(t, f, "save_draft", map[string]any{"id": "draft-other", "title": "Mine now"}); !strings.Contains(message, "not found") {
		t.Fatalf("edited another user's draft: %s", message)
	}
}

func TestMCPSaveIssueDelegatesWithInstructions(t *testing.T) {
	f := newMCPContractFixture(t)
	app, err := f.repository.InstallApplication(t.Context(), f.data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "instructed-agent", Name: "Instructed agent", Active: true, InstalledBy: f.data.Viewer.ID, TeamIDs: []string{f.data.Teams[0].ID}, Scopes: []string{"read", "app:assignable"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	created := mcpObject(t, f, "save_issue", map[string]any{"team": f.data.Teams[0].ID, "title": "Delegated work"})
	if message := mcpToolError(t, f, "save_issue", map[string]any{"id": created["id"], "delegateInstructions": "Do it"}); !strings.Contains(message, "delegateInstructions requires") {
		t.Fatalf("instructions without delegate: %s", message)
	}
	saved := mcpObject(t, f, "save_issue", map[string]any{"id": created["id"], "delegate": app.UserID, "delegateInstructions": "Reproduce on staging first."})
	if saved["delegate"].(map[string]any)["id"] != app.UserID {
		t.Fatalf("delegate not set: %v", saved)
	}
	tasks, err := f.repository.ListAgentTasks(t.Context(), f.data.Workspace.URLKey, created["id"].(string), "")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("agent task: %v %v", tasks, err)
	}
	if !strings.Contains(tasks[0].Prompt, "Reproduce on staging first.") || !strings.Contains(tasks[0].Prompt, "Delegated work") || tasks[0].Trigger != "delegation" {
		t.Fatalf("instructions not in task prompt: %+v", tasks[0])
	}
	if message := mcpToolError(t, f, "save_issue", map[string]any{"id": created["id"], "delegate": app.UserID, "delegateInstructions": "Again"}); !strings.Contains(message, "new agent") {
		t.Fatalf("instructions for unchanged delegate: %s", message)
	}
	detail := mcpObject(t, f, "get_issue", map[string]any{"id": created["id"]})
	if detail["delegate"].(map[string]any)["app"] != true {
		t.Fatalf("get_issue delegate: %v", detail["delegate"])
	}
}

func TestMCPLinearReadToolsHonorTeamRestrictedKeys(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	service := &server{store: repository, uploadPath: t.TempDir()}
	data := repository.Bootstrap()
	issue := data.Issues[0]
	now := time.Now().UTC()
	if err := repository.MutateWorkspace(ctx, actor.WorkspaceKey, "test.notifications", issue.ID, nil, func(next *domain.Bootstrap) error {
		next.Notifications = append(next.Notifications, domain.Notification{ID: "restricted-inbox", RecipientID: next.Viewer.ID, Type: "issueComment", SourceType: "issue", SourceID: issue.ID, IssueID: issue.ID, Actor: next.Viewer, CreatedAt: now, UpdatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.callFlowTool(ctx, actor, "get_issue", map[string]any{"id": issue.Identifier}); err != nil {
		t.Fatalf("unrestricted get_issue: %v", err)
	}
	actor.APIKey = domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}
	if _, err := service.callFlowTool(ctx, actor, "get_issue", map[string]any{"id": issue.Identifier}); err == nil {
		t.Fatal("team-restricted key read an issue outside its teams")
	}
	if _, err := service.callFlowTool(ctx, actor, "list_issue_history", map[string]any{"issueId": issue.ID}); err == nil {
		t.Fatal("team-restricted key read issue history outside its teams")
	}
	result, err := service.callFlowTool(ctx, actor, "search_issues", map[string]any{"queries": []any{issue.Title}})
	if err != nil {
		t.Fatal(err)
	}
	if items := result.(map[string]any)["items"].([]map[string]any); len(items) != 0 {
		t.Fatalf("team-restricted search leaked issues: %v", items)
	}
	inbox, err := service.callFlowTool(ctx, actor, "list_notifications", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range inbox.(map[string]any)["items"].([]map[string]any) {
		if item["id"] == "restricted-inbox" {
			t.Fatal("team-restricted inbox leaked an issue notification")
		}
	}
}
