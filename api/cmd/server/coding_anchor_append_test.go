package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The built-in agent's reply after start_coding_session must not be dropped
// when the coding session already wrote to the (reused) delegation task.
func TestAppendBuiltinActivityFollowsCodingAnchor(t *testing.T) {
	repo, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	ctx := t.Context()
	agentTask := func(id string) domain.AgentTask {
		task, err := repo.CreateAgentInitiatedTask(ctx, domain.AgentTask{ID: id, WorkspaceKey: "w", IssueID: "issue1", AppUserID: "app"})
		if err != nil {
			t.Fatal(err)
		}
		if task, err = repo.AppendAgentActivity(ctx, "w", id, task.Version, domain.AgentActivity{ActorID: "app", Type: "action", Body: "get_issue", Metadata: map[string]any{"action": "get_issue"}}); err != nil {
			t.Fatal(err)
		}
		return task
	}

	stale := agentTask("task1")
	now := time.Now().UTC()
	session := domain.CodingSession{ID: "cs1", WorkspaceKey: "w", AgentSessionID: "task1", IssueID: "issue1", CreatorID: "u", Source: domain.CodingSourceIssue, State: "pending", Phase: domain.CodingPhaseRunning, PhaseStartedAt: now, CreatedAt: now, UpdatedAt: now}
	if _, _, err := repo.CreateCodingSession(ctx, session, stale, ""); err != nil {
		t.Fatal(err)
	}
	current, _ := repo.AgentTask(ctx, "w", "task1")
	if _, err := repo.AppendAgentActivity(ctx, "w", "task1", current.Version, domain.AgentActivity{ActorID: "app", Type: "thought", Body: "Reading the repo"}); err != nil {
		t.Fatal(err)
	}
	reply, err := appendBuiltinActivity(ctx, repo, stale, domain.AgentActivity{ActorID: "app", Type: "response", Body: "Started a coding session."})
	if err != nil || reply.Status != "complete" {
		t.Fatalf("reply on a coding anchor: status=%q err=%v", reply.Status, err)
	}

	// A plain task keeps its optimistic version check.
	other := agentTask("task2")
	if _, err := repo.AppendAgentActivity(ctx, "w", "task2", other.Version, domain.AgentActivity{ActorID: "app", Type: "thought", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := appendBuiltinActivity(ctx, repo, other, domain.AgentActivity{ActorID: "app", Type: "response", Body: "late"}); !errors.Is(err, store.ErrIssueVersion) {
		t.Fatalf("stale write on a plain task: %v", err)
	}
}
