package store

import (
	"context"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
)

// Task panels refresh on the agent_task push for their resource, so the event must name it even for tasks
// that are not on an issue (an agent @-mentioned in a document comment).
func TestAgentTaskRealtimeEventNamesItsResource(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	workspace := repo.Bootstrap().Workspace.URLKey
	var events []domain.RealtimeEvent
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { events = append(events, event) })

	for _, task := range []domain.AgentTask{
		{ID: "mention_issue", WorkspaceKey: workspace, IssueID: "issue-1", ResourceType: "issue", ResourceID: "issue-1", AppUserID: "app"},
		{ID: "mention_document", WorkspaceKey: workspace, ResourceType: "document", ResourceID: "document-1", AppUserID: "app"},
	} {
		if err := repo.CreateMentionTask(ctx, task); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.AppendAgentActivity(ctx, workspace, task.ID, 1, domain.AgentActivity{Type: "thought", Body: "Looking"}); err != nil {
			t.Fatal(err)
		}
		last := events[len(events)-1]
		if last.Type != "agent_task.updated" || last.AggregateID != task.ResourceID {
			t.Fatalf("%s: event = %#v, want agent_task.updated for %s", task.ID, last, task.ResourceID)
		}
	}
}
