package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// mcpSessionFixture adds agent sessions on the fixture's first issue: one the
// member started (a reply, a reply deleted from the thread, a step) and one
// the owner started (failed). usr_member is a plain member with a key.
func mcpSessionFixture(t *testing.T) (*mcpContractFixture, string, string, string) {
	t.Helper()
	f := newMCPContractFixture(t)
	if err := f.repository.UpdateMemberRole(t.Context(), f.data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	member := f.addMCPKey(t, "member-sessions", "usr_member", []string{"read", "write"}, nil)
	issue := f.data.Issues[0]
	add := func(id, creator string, activities ...domain.AgentActivity) {
		t.Helper()
		if err := f.repository.CreateMentionTask(t.Context(), domain.AgentTask{ID: id, WorkspaceKey: f.data.Workspace.URLKey, IssueID: issue.ID, ResourceType: "issue", ResourceID: issue.ID, AppUserID: "app_mcp", CreatorID: creator, CommentID: "comment_" + id, Trigger: "mention", Prompt: "Investigate the crash"}); err != nil {
			t.Fatal(err)
		}
		task, err := f.repository.AgentTask(t.Context(), f.data.Workspace.URLKey, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, activity := range activities {
			if task, err = f.repository.AppendAgentActivity(t.Context(), f.data.Workspace.URLKey, id, task.Version, activity); err != nil {
				t.Fatalf("%s: %v", activity.Type, err)
			}
		}
	}
	add("mention_member", "usr_member",
		domain.AgentActivity{ActorID: "app_mcp", Type: "thought", Body: "Reading the stack trace"},
		domain.AgentActivity{ActorID: "app_mcp", Type: "response", Body: "First answer"},
		domain.AgentActivity{ActorID: "usr_member", Type: "prompt", Body: "Look again"},
		domain.AgentActivity{ActorID: "app_mcp", Type: "response", Body: "Found it"},
		domain.AgentActivity{ActorID: "usr_member", Type: "deleted", Metadata: map[string]any{"activityId": "00000000000000000003"}},
	)
	add("mention_owner", "usr_admin", domain.AgentActivity{ActorID: "app_mcp", Type: "error", Body: "The model failed"})
	return f, member, issue.ID, issue.Identifier
}

func mcpSessionItems(t *testing.T, value map[string]any) map[string]map[string]any {
	t.Helper()
	items := map[string]map[string]any{}
	for _, item := range mcpItems(t, value) {
		items[item["id"].(string)] = item
	}
	return items
}

// list_agent_sessions and get_agent_session read sessions the way the
// activity shows them; archive_agent_session applies the UI's rule (the
// person who started it or an admin, never an agent run), keeps the log, and
// changes nothing the second time.
func TestMCPAgentSessionsListReadAndArchive(t *testing.T) {
	f, member, issueID, identifier := mcpSessionFixture(t)

	listed := f.objectWith(t, member, "list_agent_sessions", map[string]any{"issueId": identifier})
	sessions := mcpSessionItems(t, listed)
	if len(sessions) != 2 || sessions["mention_member"]["status"] != "complete" || sessions["mention_owner"]["status"] != "error" || sessions["mention_member"]["archived"] != false {
		t.Fatalf("sessions: %v", listed)
	}
	if initiator := sessions["mention_member"]["initiator"].(map[string]any); initiator["id"] != "usr_member" || sessions["mention_member"]["commentId"] != "comment_mention_member" || sessions["mention_member"]["request"] != "Investigate the crash" {
		t.Fatalf("session summary: %v", sessions["mention_member"])
	}
	if url, _ := sessions["mention_member"]["url"].(string); !strings.HasSuffix(url, "#agent-session-mention_member") {
		t.Fatalf("session url: %v", sessions["mention_member"]["url"])
	}

	read := f.objectWith(t, member, "get_agent_session", map[string]any{"id": "mention_member"})
	messages := read["messages"].([]any)
	types := []string{}
	for _, raw := range messages {
		message := raw.(map[string]any)
		types = append(types, message["type"].(string))
		if message["body"] == "First answer" && message["deleted"] != true {
			t.Fatalf("a deleted reply is not marked: %v", message)
		}
	}
	if !slices.Equal(types, []string{"response", "prompt", "response"}) {
		t.Fatalf("messages without steps: %v", types)
	}
	withSteps := f.objectWith(t, member, "get_agent_session", map[string]any{"id": "mention_member", "includeSteps": true, "limit": 2})
	if steps := withSteps["messages"].([]any); len(steps) != 2 || steps[0].(map[string]any)["type"] != "thought" || withSteps["nextCursor"] == nil {
		t.Fatalf("first page with steps: %v", withSteps)
	}
	next := f.objectWith(t, member, "get_agent_session", map[string]any{"id": "mention_member", "includeSteps": true, "cursor": withSteps["nextCursor"]})
	if rest := next["messages"].([]any); len(rest) != 2 || next["nextCursor"] != nil {
		t.Fatalf("second page: %v", next)
	}

	// Who may archive: not another person's session, not with a read-only key, not from an agent run.
	mcpDeniedWith(t, f, member, "archive_agent_session", map[string]any{"id": "mention_owner"}, "Only the person who started this agent session or a workspace admin")
	readOnly := f.addMCPKey(t, "member-read-only", "usr_member", []string{"read"}, nil)
	mcpDeniedWith(t, f, readOnly, "archive_agent_session", map[string]any{"id": "mention_member"}, "write scope", "scopes do not allow")
	run := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}, Run: &agentRunGrant{}}
	if _, err := f.service.archiveMCPAgentSession(t.Context(), run, f.data, map[string]any{"id": "mention_owner"}); err == nil || !strings.Contains(err.Error(), "agent runs cannot archive") {
		t.Fatalf("agent run archive: %v", err)
	}
	if task, _ := f.repository.AgentTask(t.Context(), f.data.Workspace.URLKey, "mention_owner"); task.ArchivedAt != nil {
		t.Fatal("a refused archive archived the session")
	}

	archived := f.objectWith(t, member, "archive_agent_session", map[string]any{"id": "mention_member"})
	if archived["archived"] != true || archived["alreadyArchived"] != false || archived["stopped"] != false || !strings.Contains(archived["note"].(string), "kept") {
		t.Fatalf("receipt: %v", archived)
	}
	if session := archived["session"].(map[string]any); session["archived"] != true || session["archivedBy"].(map[string]any)["id"] != "usr_member" || session["issueIdentifier"] != identifier {
		t.Fatalf("receipt session: %v", session)
	}
	before, _ := f.repository.AgentTask(t.Context(), f.data.Workspace.URLKey, "mention_member")
	again := f.objectWith(t, f.secret, "archive_agent_session", map[string]any{"id": "mention_member"})
	if again["alreadyArchived"] != true || !strings.Contains(again["note"].(string), "already archived") {
		t.Fatalf("second archive: %v", again)
	}
	if after, _ := f.repository.AgentTask(t.Context(), f.data.Workspace.URLKey, "mention_member"); after.Version != before.Version || !after.ArchivedAt.Equal(*before.ArchivedAt) {
		t.Fatal("the second archive wrote")
	}

	// The activity no longer lists it; the log is still there.
	if sessions := mcpSessionItems(t, f.objectWith(t, member, "list_agent_sessions", map[string]any{"issueId": issueID})); len(sessions) != 1 || sessions["mention_owner"] == nil {
		t.Fatalf("after archiving: %v", sessions)
	}
	all := mcpSessionItems(t, f.objectWith(t, member, "list_agent_sessions", map[string]any{"issueId": issueID, "includeArchived": true}))
	if len(all) != 2 || all["mention_member"]["archived"] != true || all["mention_member"]["url"] != nil {
		t.Fatalf("includeArchived: %v", all)
	}
	if kept := f.objectWith(t, member, "get_agent_session", map[string]any{"id": "mention_member"}); len(kept["messages"].([]any)) != 3 {
		t.Fatalf("archived log: %v", kept)
	}

	// A guest cannot archive someone else's session; the admin archives their own failed one.
	guest := f.guestSecret(t)
	mcpDeniedWith(t, f, guest, "archive_agent_session", map[string]any{"id": "mention_owner"}, "not found", "Only the person who started")
	if owner := f.objectWith(t, f.secret, "archive_agent_session", map[string]any{"id": "mention_owner"}); owner["archived"] != true || owner["previousStatus"] != "error" {
		t.Fatalf("owner archive: %v", owner)
	}
	if left := mcpSessionItems(t, f.objectWith(t, f.secret, "list_agent_sessions", map[string]any{"issueId": issueID})); len(left) != 0 {
		t.Fatalf("archived sessions listed: %v", left)
	}
}

// The session tools are MCP tools for people: the in-process agent (an
// application) is not offered them.
func TestMCPAgentSessionToolsAreNotOfferedToTheInProcessAgent(t *testing.T) {
	s := &server{}
	s.agent.ToolsEnabled, s.agent.WriteTools = true, true
	tools, err := s.agentToolDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if strings.Contains(tool.Name, "agent_session") {
			t.Fatalf("%s is offered to the in-process agent", tool.Name)
		}
	}
	var inventory []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_agent_sessions", "get_agent_session", "archive_agent_session"} {
		index := slices.IndexFunc(inventory, func(tool flowMCPTool) bool { return tool.Name == "mcp__flow."+name })
		if index < 0 {
			t.Fatalf("%s is not an MCP tool", name)
		}
		if description := inventory[index].Description; !strings.Contains(description, "session") || !strings.Contains(description, "comment") {
			t.Fatalf("%s does not tell sessions from comments: %s", name, description)
		}
	}
	archive := inventory[slices.IndexFunc(inventory, func(tool flowMCPTool) bool { return tool.Name == "mcp__flow.archive_agent_session" })]
	for _, phrase := range []string{"whole agent session", "delete_comment", "kept", "person who started"} {
		if !strings.Contains(archive.Description, phrase) {
			t.Fatalf("archive_agent_session does not say %q", phrase)
		}
	}
}
