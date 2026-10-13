package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// agentAttributionFixture runs agent tool calls the way the in-app chat does
// (executeAgentTool as the signed-in person) and the way the built-in "Flow"
// application does in comment threads (as its app user, for the invoker).
type agentAttributionFixture struct {
	t       *testing.T
	s       *server
	handler http.Handler
	data    domain.Bootstrap
	app     domain.ApplicationInstallation
}

func newAgentAttributionFixture(t *testing.T) agentAttributionFixture {
	t.Helper()
	s, handler, data, _ := applicationFixture(t)
	s.agent.Enabled, s.agent.ToolsEnabled, s.agent.WriteTools = true, true, true
	app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: store.BuiltinApplicationClientID, Name: "Flow", AvatarURL: builtinAgentAvatarURL, Builtin: true, Active: true, InstalledBy: data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable", "app:mentionable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return agentAttributionFixture{t: t, s: s, handler: handler, data: data, app: app}
}

// chat calls a tool as the in-app chat does.
func (f agentAttributionFixture) chat(name string, args map[string]any) json.RawMessage {
	f.t.Helper()
	ctx := context.WithValue(context.Background(), authUserContextKey{}, f.data.Viewer)
	ctx = store.ContextWithActor(ctx, f.data.Viewer)
	request := httptest.NewRequest("POST", "/api/agent?workspace="+f.data.Workspace.URLKey, nil).WithContext(ctx)
	raw, _ := json.Marshal(args)
	result, err := f.s.executeAgentTool(request, f.data, domain.AgentToolCall{Name: name, Arguments: raw})
	if err != nil {
		f.t.Fatalf("%s: %v", name, err)
	}
	return result
}

// thread calls a tool as the built-in application does for a comment thread
// started by the signed-in person on issue.
func (f agentAttributionFixture) thread(issueID, name string, args map[string]any) json.RawMessage {
	f.t.Helper()
	current, err := f.s.store.ApplicationByUser(f.t.Context(), f.app.UserID)
	if err != nil {
		f.t.Fatal(err)
	}
	task := domain.AgentTask{WorkspaceKey: f.data.Workspace.URLKey, IssueID: issueID, CreatorID: f.data.Viewer.ID, AppUserID: f.app.UserID, TeamID: f.data.Teams[0].ID}
	key := domain.APIKey{CreatorID: current.UserID, SubjectUserID: current.UserID, Actor: "app", Scopes: current.Scopes, TeamRestriction: "selected", TeamIDs: current.AccessTeamIDs(), OAuthClientID: current.ClientID}
	request := httptest.NewRequest("POST", "/internal", nil).WithContext(context.WithValue(context.Background(), apiKeyContextKey{}, key))
	request, scoped, err := f.s.builtinApplicationContext(request, current, task)
	if err != nil {
		f.t.Fatal(err)
	}
	raw, _ := json.Marshal(args)
	result, err := f.s.executeAgentTool(request, scoped, domain.AgentToolCall{Name: name, Arguments: raw})
	if err != nil {
		f.t.Fatalf("%s: %v", name, err)
	}
	return result
}

func (f agentAttributionFixture) assertAgent(what string, bot *domain.ActorBot, actorID string, actor domain.User) {
	f.t.Helper()
	if bot == nil || bot.Type != domain.ActorBotTypeAgent || bot.Name != "Flow" || bot.OnBehalfOfID != f.data.Viewer.ID || bot.OnBehalfOfName == "" {
		f.t.Fatalf("%s: want Flow on behalf of %s, got %+v", what, f.data.Viewer.ID, bot)
	}
	if actor.ID != actorID {
		f.t.Fatalf("%s: actor %s, want %s", what, actor.ID, actorID)
	}
}

func receiptID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var receipt struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.ID == "" {
		t.Fatalf("tool returned no id: %s", raw)
	}
	return receipt.ID
}

func (f agentAttributionFixture) issue(raw json.RawMessage) domain.Issue {
	f.t.Helper()
	issue, err := f.s.store.IssueRecord(f.t.Context(), f.data.Workspace.URLKey, receiptID(f.t, raw))
	if err != nil {
		f.t.Fatal(err)
	}
	return issue
}

func (f agentAttributionFixture) project(raw json.RawMessage) domain.Project {
	f.t.Helper()
	id := receiptID(f.t, raw)
	for _, project := range f.s.store.Bootstrap().Projects {
		if project.ID == id && project.Creator != nil {
			return project
		}
	}
	f.t.Fatalf("project %s was not created", id)
	return domain.Project{}
}

func (f agentAttributionFixture) initiative(raw json.RawMessage) domain.Initiative {
	f.t.Helper()
	id := receiptID(f.t, raw)
	for _, initiative := range f.s.store.Bootstrap().Initiatives {
		if initiative.ID == id {
			return initiative
		}
	}
	f.t.Fatalf("initiative %s was not created", id)
	return domain.Initiative{}
}

func (f agentAttributionFixture) document(raw json.RawMessage) domain.Document {
	f.t.Helper()
	return requestJSON[domain.Document](f.t, f.handler, "GET", "/api/documents/"+receiptID(f.t, raw), nil, 200)
}

func issueHistory(f agentAttributionFixture, id string) []domain.ActivityEvent {
	f.t.Helper()
	page := requestJSON[struct {
		Activities []domain.ActivityEvent `json:"activities"`
	}](f.t, f.handler, "GET", "/api/issue-records/"+id+"/history", nil, 200)
	return page.Activities
}

func auditHistory(f agentAttributionFixture, path string) []domain.AuditLogEntry {
	f.t.Helper()
	return requestJSON[struct {
		Nodes []domain.AuditLogEntry `json:"nodes"`
	}](f.t, f.handler, "GET", path, nil, 200).Nodes
}

func TestAgentChatToolCallsAttributeHistoryToFlowOnBehalfOfTheUser(t *testing.T) {
	f := newAgentAttributionFixture(t)
	viewer := f.data.Viewer.ID
	team := f.data.Teams[0].ID

	// Issues: creation, field changes, sub-issues, relations and comments.
	parent := f.issue(f.chat("save_issue", map[string]any{"team": team, "title": "QA fix – attribution parent"}))
	f.assertAgent("issue creation", parent.BotActor, viewer, parent.Creator)
	f.chat("save_issue", map[string]any{"id": parent.ID, "priority": 1, "estimate": 3})
	child := f.issue(f.chat("save_issue", map[string]any{"team": team, "title": "QA fix – attribution child", "parentId": parent.ID}))
	f.assertAgent("sub-issue creation", child.BotActor, viewer, child.Creator)
	f.chat("save_issue", map[string]any{"id": parent.ID, "relatedTo": []string{child.ID}})
	f.chat("save_comment", map[string]any{"issueId": parent.ID, "body": "QA fix – agent comment"})
	events := issueHistory(f, parent.ID)
	kinds := map[string]bool{}
	for _, event := range events {
		f.assertAgent("issue history "+event.Type, event.BotActor, viewer, event.Actor)
		kinds[event.Type] = true
	}
	for _, kind := range []string{"issue.created", "issue.updated", "issue.relation_added"} {
		if !kinds[kind] {
			t.Fatalf("issue history lacks %s: %+v", kind, events)
		}
	}
	for _, event := range issueHistory(f, child.ID) {
		f.assertAgent("sub-issue history "+event.Type, event.BotActor, viewer, event.Actor)
	}
	comments := requestJSON[struct {
		Comments []domain.Comment `json:"comments"`
	}](t, f.handler, "GET", "/api/issue-records/"+parent.ID+"/history", nil, 200).Comments
	if len(comments) != 1 {
		t.Fatalf("comments: %+v", comments)
	}
	f.assertAgent("issue comment", comments[0].BotActor, viewer, comments[0].User)

	// Projects: creation, property changes and milestones.
	project := f.project(f.chat("save_project", map[string]any{"name": "QA fix – attribution project", "setTeams": []string{team}}))
	f.assertAgent("project creation", project.BotActor, viewer, *project.Creator)
	f.chat("save_project", map[string]any{"id": project.ID, "priority": 2})
	f.chat("save_milestone", map[string]any{"project": project.ID, "name": "QA fix – beta"})
	actions := map[string]bool{}
	for _, entry := range auditHistory(f, "/api/projects/"+project.ID+"/history") {
		f.assertAgent("project history "+entry.Action, entry.BotActor, viewer, entry.Actor)
		actions[entry.Action] = true
	}
	if !actions["updated"] || !actions["milestone_created"] {
		t.Fatalf("project history: %v", actions)
	}

	// Initiatives: creation and property changes.
	initiative := f.initiative(f.chat("save_initiative", map[string]any{"name": "QA fix – attribution initiative"}))
	f.assertAgent("initiative creation", initiative.BotActor, viewer, initiative.Creator)
	f.chat("save_initiative", map[string]any{"id": initiative.ID, "status": "planned"})
	actions = map[string]bool{}
	for _, entry := range auditHistory(f, "/api/initiatives/"+initiative.ID+"/history") {
		f.assertAgent("initiative history "+entry.Action, entry.BotActor, viewer, entry.Actor)
		actions[entry.Action] = true
	}
	if !actions["created"] || !actions["updated"] {
		t.Fatalf("initiative history: %v", actions)
	}

	// Documents: creation and the version history ("Last edit by … via Flow").
	document := f.document(f.chat("save_document", map[string]any{"title": "QA fix – attribution doc", "project": project.ID}))
	f.assertAgent("document creation", document.BotActor, viewer, document.Creator)
	f.chat("save_document", map[string]any{"id": document.ID, "content": "Written by the agent."})
	revisions := auditRevisions(f, document.ID)
	if len(revisions) == 0 {
		t.Fatal("document edit recorded no version")
	}
	f.assertAgent("document version", revisions[0].BotActor, viewer, revisions[0].Author)

	// A person's own edit afterwards is theirs alone.
	requestJSON[domain.Issue](t, f.handler, "PATCH", "/api/issue-records/"+parent.ID, map[string]any{"priority": 3}, 200)
	events = issueHistory(f, parent.ID)
	if last := events[len(events)-1]; last.BotActor != nil {
		t.Fatalf("a person's own change was attributed to the agent: %+v", last)
	}
}

func auditRevisions(f agentAttributionFixture, documentID string) []domain.DocumentRevision {
	f.t.Helper()
	return requestJSON[struct {
		Nodes []domain.DocumentRevision `json:"nodes"`
	}](f.t, f.handler, "GET", "/api/documents/"+documentID+"/history", nil, 200).Nodes
}

func TestAgentThreadToolCallsAttributeHistoryToFlowOnBehalfOfTheInvoker(t *testing.T) {
	f := newAgentAttributionFixture(t)
	issue := requestJSON[domain.Issue](t, f.handler, "POST", "/api/issues", map[string]any{"title": "QA fix – thread attribution", "teamId": f.data.Teams[0].ID}, 201)
	f.thread(issue.ID, "save_issue", map[string]any{"id": issue.ID, "priority": 1, "estimate": 3})
	changed := map[string]bool{}
	for _, event := range issueHistory(f, issue.ID) {
		if event.Type != "issue.updated" {
			continue
		}
		f.assertAgent("thread issue change", event.BotActor, f.app.UserID, event.Actor)
		if event.BotActor.ID != store.BuiltinApplicationClientID || event.BotActor.AvatarURL != builtinAgentAvatarURL {
			t.Fatalf("thread attribution names the wrong agent: %+v", event.BotActor)
		}
		for key := range event.Metadata {
			changed[key] = true
		}
	}
	if !changed["priority"] || !changed["estimate"] {
		t.Fatalf("thread changes missing from the history: %v", changed)
	}
	project := f.project(f.thread(issue.ID, "save_project", map[string]any{"name": "QA fix – thread project", "setTeams": []string{f.data.Teams[0].ID}}))
	f.assertAgent("thread project creation", project.BotActor, f.app.UserID, *project.Creator)
}
