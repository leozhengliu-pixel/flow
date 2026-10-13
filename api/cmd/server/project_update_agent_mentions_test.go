package main

import (
	"testing"

	"flow/api/internal/domain"
)

// Mentioning an agent in a project update's comment starts a project session
// on that comment, which answers inside the update's thread.
func TestProjectUpdateCommentMentionStartsProjectSession(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	project := data.Projects[0]
	app.TeamIDs = project.TeamIDs
	app.InstalledBy = data.Viewer.ID
	if _, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, app, ""); err != nil {
		t.Fatal(err)
	}
	update := requestJSON[domain.ProjectUpdate](t, handler, "POST", "/api/projects/"+project.ID+"/updates", map[string]any{"body": "Shipping next week", "health": "onTrack"}, 201)
	requestJSON[domain.ProjectUpdate](t, handler, "POST", "/api/projects/"+project.ID+"/updates/"+update.ID+"/comments", map[string]any{"body": "No mention here"}, 201)
	if tasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=project&resourceId="+project.ID, nil, 200); len(tasks) != 0 {
		t.Fatalf("a comment without a mention started a session: %+v", tasks)
	}
	body := map[string]any{"body": "@Agent summarize this update", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": app.UserID, "label": "Agent"}}}}}}}
	commented := requestJSON[domain.ProjectUpdate](t, handler, "POST", "/api/projects/"+project.ID+"/updates/"+update.ID+"/comments", body, 201)
	comment := commented.Comments[len(commented.Comments)-1]
	tasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=project&resourceId="+project.ID, nil, 200)
	if len(tasks) != 1 || tasks[0].ResourceType != "project" || tasks[0].ResourceID != project.ID || tasks[0].CommentID != comment.ID || tasks[0].Trigger != "mention" {
		t.Fatalf("project update comment session: %+v (comment %s)", tasks, comment.ID)
	}
	// Another comment on the same update leaves the first session alone.
	requestJSON[domain.ProjectUpdate](t, handler, "POST", "/api/projects/"+project.ID+"/updates/"+update.ID+"/comments", map[string]any{"body": "Thanks"}, 201)
	if tasks := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=project&resourceId="+project.ID, nil, 200); len(tasks) != 1 {
		t.Fatalf("sessions after a plain comment: %+v", tasks)
	}
}
