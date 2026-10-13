package main

import (
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// agentMentionBody is a comment that mentions the agent.
func agentMentionBody(app domain.ApplicationInstallation) map[string]any {
	return map[string]any{"body": "@Agent what changed?", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": app.UserID, "label": "Agent"}}}}}}}
}

func secondMember(t *testing.T, data domain.Bootstrap) string {
	t.Helper()
	for _, user := range data.Users {
		if user.ID != data.Viewer.ID && !user.App {
			return user.ID
		}
	}
	t.Fatal("fixture has no second member")
	return ""
}

// checkAgentReplyThread drives an agent session through a reply, a read, a
// follow-up question and a muted thread, checking each recipient's single
// notification; check validates where the notification points.
func checkAgentReplyThread(t *testing.T, s *server, data domain.Bootstrap, app domain.ApplicationInstallation, task domain.AgentTask, other, muteURL string, check func(domain.Notification)) {
	t.Helper()
	workspace := data.Workspace.URLKey
	task, err := s.store.AppendAgentActivity(t.Context(), workspace, task.ID, task.Version, domain.AgentActivity{ActorID: app.UserID, Type: "response", Body: "**Two fields** changed.\n\n```suggestions\nShow the history\n```"})
	if err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []string{data.Viewer.ID, other} {
		items := agentThreadNotifications(t, s, workspace, recipient)
		if len(items) != 1 {
			t.Fatalf("recipient %s: want one agent notification, got %+v", recipient, items)
		}
		item := items[0]
		if item.ReadAt != nil || item.Actor.ID != app.UserID || item.Category != "comments" || item.OccurrenceCount != 1 || item.CommentID != task.CommentID || item.IssueID != "" {
			t.Fatalf("notification: %+v", item)
		}
		if item.Text != "Test agent replied: Two fields changed." || item.Payload == nil || item.Payload.AgentActivity != "response" || item.Payload.Excerpt != "Two fields changed." {
			t.Fatalf("text: %q payload=%+v", item.Text, item.Payload)
		}
		check(item)
	}
	first := agentThreadNotifications(t, s, workspace, data.Viewer.ID)[0]
	handler := newHandler(s)
	requestJSON[any](t, handler, "PATCH", "/api/notifications/"+first.ID, map[string]any{"read": true}, 200)
	if items := agentThreadNotifications(t, s, workspace, data.Viewer.ID); items[0].ReadAt == nil {
		t.Fatalf("notification not read: %+v", items[0])
	}
	// The agent asks a question: the same notification is updated in place and unread again.
	task, err = s.store.AppendAgentActivity(t.Context(), workspace, task.ID, task.Version, domain.AgentActivity{ActorID: data.Viewer.ID, Type: "prompt", Body: "Fix it"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.store.AppendAgentActivity(t.Context(), workspace, task.ID, task.Version, domain.AgentActivity{ActorID: app.UserID, Type: "elicitation", Body: "Which priority?", Signal: "select", SignalMetadata: map[string]any{"options": []any{map[string]any{"value": "High"}}}})
	if err != nil {
		t.Fatal(err)
	}
	items := agentThreadNotifications(t, s, workspace, data.Viewer.ID)
	if len(items) != 1 || items[0].ID != first.ID || items[0].ReadAt != nil || items[0].OccurrenceCount != 2 || items[0].Text != "Test agent is waiting for your input: Which priority?" || items[0].Payload.AgentActivity != "elicitation" {
		t.Fatalf("updated notification: %+v", items)
	}
	check(items[0])
	// Muting the thread stops them; the other participant still hears the next reply.
	requestJSON[any](t, handler, "PUT", muteURL, map[string]any{"state": "muted"}, 200)
	task, err = s.store.AppendAgentActivity(t.Context(), workspace, task.ID, task.Version, domain.AgentActivity{ActorID: other, Type: "prompt", Body: "High"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.AppendAgentActivity(t.Context(), workspace, task.ID, task.Version, domain.AgentActivity{ActorID: app.UserID, Type: "response", Body: "Set to High."}); err != nil {
		t.Fatal(err)
	}
	if items := agentThreadNotifications(t, s, workspace, data.Viewer.ID); len(items) != 1 || items[0].OccurrenceCount != 2 {
		t.Fatalf("muted thread still notified: %+v", items)
	}
	if items := agentThreadNotifications(t, s, workspace, other); len(items) != 1 || items[0].OccurrenceCount != 3 || items[0].Text != "Test agent replied: Set to High." || items[0].ReadAt != nil {
		t.Fatalf("participant: %+v", items)
	}
}

// An agent's replies in a document comment thread notify like issue threads:
// one notification per thread, owned by the document and linking to the comment.
func TestAgentRepliesInDocumentThreadNotifyOncePerThread(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	other := secondMember(t, data)
	doc := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "Agent document", "teamIds": app.TeamIDs}, 201)
	comment := requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+doc.ID+"/comments", agentMentionBody(app), 201)
	tasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=document&resourceId="+doc.ID, nil, 200)
	if len(tasks) != 1 || tasks[0].CommentID != comment.ID {
		t.Fatalf("mention did not start a document session: %+v", tasks)
	}
	// The second member takes part in the thread with a reply.
	err := s.store.MutateWorkspace(documentContentScope(t.Context(), doc.ID), data.Workspace.URLKey, "document.comment_created", doc.ID, nil, func(next *domain.Bootstrap) error {
		next.Comments[doc.ID] = append(next.Comments[doc.ID], domain.Comment{ID: "document_comment_reply_other", Version: 1, Body: "+1", ParentID: &comment.ID, User: *userByID(next, other), Reactions: map[string][]string{}, CreatedAt: time.Now().UTC()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checkAgentReplyThread(t, s, data, app, tasks[0], other, "/api/documents/"+doc.ID+"/comments/"+comment.ID+"/subscription", func(item domain.Notification) {
		t.Helper()
		if item.SourceType != "document" || item.SourceID != doc.ID || item.ProjectID != "" || item.Title != "Agent document" || item.Payload.DocumentSlugID != doc.SlugID {
			t.Fatalf("document notification: %+v payload=%+v", item, item.Payload)
		}
	})
}

// An agent's replies in a project comment thread notify like issue threads:
// one notification per thread, owned by the project and linking to the comment.
func TestAgentRepliesInProjectThreadNotifyOncePerThread(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	other := secondMember(t, data)
	project := data.Projects[0]
	app.TeamIDs = project.TeamIDs
	app.InstalledBy = data.Viewer.ID
	if _, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, app, ""); err != nil {
		t.Fatal(err)
	}
	comment := requestJSON[domain.Comment](t, handler, "POST", "/api/projects/"+project.ID+"/comments", agentMentionBody(app), 201)
	tasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=project&resourceId="+project.ID, nil, 200)
	if len(tasks) != 1 || tasks[0].CommentID != comment.ID {
		t.Fatalf("mention did not start a project session: %+v", tasks)
	}
	err := s.store.MutateWorkspace(t.Context(), data.Workspace.URLKey, "project.commented", project.ID, nil, func(next *domain.Bootstrap) error {
		target, err := fullProjectByID(next, project.ID)
		if err != nil {
			return err
		}
		target.Comments = append(target.Comments, domain.Comment{ID: "project_comment_reply_other", Version: 1, Body: "+1", ParentID: &comment.ID, User: *userByID(next, other), Reactions: map[string][]string{}, CreatedAt: time.Now().UTC()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checkAgentReplyThread(t, s, data, app, tasks[0], other, "/api/projects/"+project.ID+"/comments/"+comment.ID+"/subscription", func(item domain.Notification) {
		t.Helper()
		if item.SourceType != "project" || item.SourceID != project.ID || item.ProjectID != project.ID || item.Title != project.Name || item.Payload.DocumentSlugID != "" {
			t.Fatalf("project notification: %+v", item)
		}
	})
}

// A session started outside a thread (no comment) still reaches its creator,
// and a reply on a resource the session no longer resolves writes nothing.
func TestAgentReplyNotificationsWithoutThread(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	doc := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "", "teamIds": app.TeamIDs}, 201)
	requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+doc.ID+"/comments", agentMentionBody(app), 201)
	tasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=document&resourceId="+doc.ID, nil, 200)
	if len(tasks) != 1 {
		t.Fatalf("tasks: %+v", tasks)
	}
	task := tasks[0]
	task.CommentID = ""
	s.notifyAgentReply(t.Context(), task, domain.AgentActivity{ID: "activity_x", ActorID: app.UserID, Type: "error", Body: ""})
	items := agentThreadNotifications(t, s, data.Workspace.URLKey, data.Viewer.ID)
	if len(items) != 1 || items[0].Title != untitledDocumentTitle || items[0].Text != "Test agent ran into an error" || items[0].CommentID != "" {
		t.Fatalf("threadless notification: %+v", items)
	}
	task.ResourceID = "document_missing"
	task.ID = "mention_missing"
	s.notifyAgentReply(store.ContextWithActor(t.Context(), data.Viewer), task, domain.AgentActivity{ID: "activity_y", ActorID: app.UserID, Type: "response", Body: "Hi"})
	if items := agentThreadNotifications(t, s, data.Workspace.URLKey, data.Viewer.ID); len(items) != 1 {
		t.Fatalf("missing resource wrote a notification: %+v", items)
	}
}
