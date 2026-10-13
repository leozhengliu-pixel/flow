package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A project's or document's agent session pushes agent_task.updated to the
// people viewing it, like an issue's session, so its comment thread shows the
// agent working and its reply without a reload.
func TestAgentTaskPushesReachProjectAndDocumentViewers(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	project := data.Projects[0]
	app.TeamIDs = project.TeamIDs
	app.InstalledBy = data.Viewer.ID
	if _, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, app, ""); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"body": "@Agent look", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": app.UserID, "label": "Agent"}}}}}}}
	requestJSON[domain.Comment](t, handler, "POST", "/api/projects/"+project.ID+"/comments", body, 201)
	doc := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "Agent document", "teamIds": app.TeamIDs}, 201)
	requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+doc.ID+"/comments", body, 201)
	projectTasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=project&resourceId="+project.ID, nil, 200)
	documentTasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=document&resourceId="+doc.ID, nil, 200)
	if len(projectTasks) != 1 || len(documentTasks) != 1 {
		t.Fatalf("sessions: %+v %+v", projectTasks, documentTasks)
	}
	host := httptest.NewServer(handler)
	defer host.Close()
	response, err := http.Get(host.URL + "/api/realtime/events?workspace=" + data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	lines := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	if line := <-lines; !strings.Contains(line, "connected") {
		t.Fatalf("handshake: %s", line)
	}
	for _, task := range []domain.AgentTask{projectTasks[0], documentTasks[0]} {
		if _, err := s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{ActorID: app.UserID, Type: "thought", Body: "Looking"}); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]bool{project.ID: false, doc.ID: false}
	deadline := time.After(3 * time.Second)
	for !want[project.ID] || !want[doc.ID] {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed: %+v", want)
			}
			for id := range want {
				if strings.Contains(line, `"agent_task.updated"`) && strings.Contains(line, `"aggregateId":"`+id+`"`) {
					want[id] = true
				}
			}
		case <-deadline:
			t.Fatalf("pushes not delivered: %+v", want)
		}
	}
}

// Mentioning an agent in a workspace document (no team or project) or in a document attached to an issue starts a
// session; before, such documents had no team and the mention was silently dropped.
func TestAgentMentionInWorkspaceAndIssueDocumentsStartsSession(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	app.TeamIDs = []string{data.Issues[0].Team.ID}
	app.InstalledBy = data.Viewer.ID
	if _, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, app, ""); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"body": "@Agent look", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": app.UserID, "label": "Agent"}}}}}}}
	workspaceDoc := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "QA agent – workspace doc"}, 201)
	issueDoc := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "QA agent – issue doc", "issueId": data.Issues[0].ID}, 201)
	for _, doc := range []domain.Document{workspaceDoc, issueDoc} {
		requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+doc.ID+"/comments", body, 201)
		tasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=document&resourceId="+doc.ID, nil, 200)
		if len(tasks) != 1 || tasks[0].TeamID != data.Issues[0].Team.ID {
			t.Fatalf("%s: sessions = %+v", doc.Title, tasks)
		}
	}
}
