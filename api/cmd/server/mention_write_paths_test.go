package main

import (
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// mentionKinds lists a document's mentions as kind:id.
func mentionKinds(value any) []string {
	document, _ := value.(map[string]any)
	kinds := []string{}
	for _, mention := range loopMentionsIn(document) {
		kinds = append(kinds, mention["mentionType"].(string)+":"+mention["id"].(string))
	}
	return kinds
}

func TestMCPWritesStoreReferencesAsMentions(t *testing.T) {
	f := newMCPContractFixture(t)
	issue, project := f.data.Issues[0], f.data.Projects[0]
	root := "/" + f.data.Workspace.URLKey
	body := "Follow up on " + issue.Identifier + " with @Test member in [the plan](" + root + "/project/" + project.SlugID + "/overview). Not `" + issue.Identifier + "`."
	want := []string{"issue:" + issue.ID, "user:usr_member", "project:" + project.ID}
	check := func(surface string, value any) {
		t.Helper()
		if got := mentionKinds(value); !slices.Equal(got, want) {
			t.Fatalf("%s mentions = %v, want %v (%v)", surface, got, want, value)
		}
	}

	created := mcpObject(t, f, "save_document", map[string]any{"title": "References", "content": body, "team": "TST"})
	for _, document := range f.repository.Bootstrap().Documents {
		if document.ID == created["id"] {
			check("document", document.ContentData)
			if document.Content != body {
				t.Fatalf("document markdown changed: %q", document.Content)
			}
		}
	}
	template := mcpObject(t, f, "save_template", map[string]any{"type": "document", "name": "Refs template", "team": "TST", "content": body})
	for _, item := range f.repository.Bootstrap().DocumentTemplates {
		if item.ID == template["id"] {
			check("document template", item.ContentData)
		}
	}

	comment := mcpObject(t, f, "save_comment", map[string]any{"issueId": issue.ID, "body": body})
	check("comment", comment["bodyData"])
	persisted := f.repository.Bootstrap().Comments[issue.ID]
	index := slices.IndexFunc(persisted, func(item domain.Comment) bool { return item.ID == comment["id"] })
	if index < 0 {
		t.Fatal("comment not persisted")
	}
	check("persisted comment", persisted[index].BodyData)
	reply := mcpObject(t, f, "save_comment", map[string]any{"parentId": comment["id"], "body": "Agreed, " + issue.Identifier + " first."})
	if got := mentionKinds(reply["bodyData"]); !slices.Equal(got, []string{"issue:" + issue.ID}) {
		t.Fatalf("reply mentions = %v", got)
	}
	plain := mcpObject(t, f, "save_comment", map[string]any{"id": comment["id"], "body": "No references any more."})
	if plain["bodyData"] != nil {
		t.Fatalf("a body without references keeps markdown only: %v", plain["bodyData"])
	}
	supplied := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Editor body " + issue.Identifier}}}}}
	// Internal callers (the agent runtime) may pass the editor document themselves.
	actor := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	workspace, err := f.service.mcpWorkspaceData(t.Context(), actor)
	if err == nil {
		err = f.service.hydrateMCPIssueArguments(t.Context(), actor, &workspace, map[string]any{"id": issue.ID})
	}
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.service.mutateAnyComment(t.Context(), actor, workspace, issue.ID, "Editor body "+issue.Identifier, supplied, "create")
	if err != nil {
		t.Fatal(err)
	}
	if kept := result.(domain.Comment); len(mentionKinds(kept.BodyData)) != 0 || kept.BodyData["content"] == nil {
		t.Fatalf("caller bodyData was rewritten: %v", kept.BodyData)
	}

	update := mcpObject(t, f, "save_status_update", map[string]any{"type": "project", "project": project.ID, "body": body})
	for _, item := range f.repository.Bootstrap().ProjectUpdates[project.ID] {
		if item.ID == update["id"] {
			check("project update", item.BodyData)
		}
	}

	saved := mcpObject(t, f, "save_issue", map[string]any{"title": "Mentions", "team": "TST", "description": body})
	stored, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, saved["id"].(string))
	if err != nil || stored.DocumentContent == nil {
		t.Fatalf("issue: %+v %v", stored, err)
	}
	check("issue description", stored.DocumentContent.ContentData)

	draft := mcpObject(t, f, "save_draft", map[string]any{"title": "Drafted", "team": "TST", "body": body})
	description := draft["metadata"].(map[string]any)["description"].(map[string]any)
	check("draft description", description["document"])
}

func TestApplicationMentionsIgnoreResourceMentions(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Mention", "teamId": data.Teams[0].ID}, 201)
	// An issue mention whose id happens to look like an app user's is not a person.
	requestJSON[domain.Comment](t, handler, "POST", "/api/issues/"+issue.ID+"/comments", map[string]any{"body": "See it", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "issue", "id": app.UserID, "label": "DEV-1"}}}}}, 201)
	if tasks, err := s.store.ListAgentTasks(t.Context(), data.Workspace.URLKey, issue.ID, app.UserID); err != nil || len(tasks) != 0 {
		t.Fatalf("resource mention started a session: %+v %v", tasks, err)
	}
	requestJSON[domain.Comment](t, handler, "POST", "/api/issues/"+issue.ID+"/comments", map[string]any{"body": "@Agent look", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "user", "id": app.UserID, "label": "Agent"}}}}}, 201)
	if tasks, err := s.store.ListAgentTasks(t.Context(), data.Workspace.URLKey, issue.ID, app.UserID); err != nil || len(tasks) != 1 {
		t.Fatalf("user mention did not start a session: %+v %v", tasks, err)
	}
}
