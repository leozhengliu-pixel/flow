package store

import (
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A delegation task reused as a coding anchor is completed by the delegating
// agent's own reply while the session's first turn is starting; that turn's
// progress must still reach the thread (it reopens the anchor), but progress
// outside a turn and plain agent tasks keep the terminal guard.
func TestCompletedCodingAnchorReopensForTurnProgress(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "anchor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	ctx := t.Context()
	now := time.Now().UTC()
	insertTask := func(id string) {
		raw := []byte(`{"id":"` + id + `","workspaceKey":"w","issueId":"issue1","appUserId":"app","creatorId":"u","status":"active","trigger":"delegation","version":1}`)
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at) VALUES(?,?,?,?,?,1,?,?)`, id, "w", "issue1", "app", "active", raw, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	appendActivity := func(id string, activity domain.AgentActivity) (domain.AgentTask, error) {
		task, err := repo.AgentTask(ctx, "w", id)
		if err != nil {
			t.Fatal(err)
		}
		return repo.AppendAgentActivity(ctx, "w", id, task.Version, activity)
	}
	setPhase := func(phase domain.CodingSessionPhase) {
		if _, err := repo.UpdateCodingSession(ctx, "w", "cs1", func(current *domain.CodingSession) error {
			current.Phase = phase
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	insertTask("task1")
	delegation := domain.AgentTask{ID: "task1", WorkspaceKey: "w", IssueID: "issue1", AppUserID: "app", CreatorID: "u", Status: "active", Trigger: "delegation", Version: 1, CreatedAt: now, UpdatedAt: now}
	session := domain.CodingSession{ID: "cs1", WorkspaceKey: "w", AgentSessionID: "task1", IssueID: "issue1", CreatorID: "u", Source: domain.CodingSourceIssue, State: "pending", Phase: domain.CodingPhaseStarting, PhaseStartedAt: now, CreatedAt: now, UpdatedAt: now}
	if _, created, err := repo.CreateCodingSession(ctx, session, delegation, ""); err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	// The delegating agent replies after it started the session.
	if task, err := appendActivity("task1", domain.AgentActivity{Type: "response", ActorID: "app", Body: "Started a coding session."}); err != nil || task.Status != "complete" {
		t.Fatalf("agent reply: status=%q err=%v", task.Status, err)
	}
	setPhase(domain.CodingPhaseRunning)
	task, err := appendActivity("task1", domain.AgentActivity{Type: "thought", ActorID: "app", Body: "Reading src/math.js"})
	if err != nil || task.Status != "active" {
		t.Fatalf("turn progress on a completed anchor: status=%q err=%v", task.Status, err)
	}
	if task, err = appendActivity("task1", domain.AgentActivity{Type: "response", ActorID: "app", Body: "Done."}); err != nil || task.Status != "complete" {
		t.Fatalf("turn response: status=%q err=%v", task.Status, err)
	}

	// Between turns (idle) a late event does not reopen the anchor.
	setPhase(domain.CodingPhaseIdle)
	if _, err := appendActivity("task1", domain.AgentActivity{Type: "thought", ActorID: "app", Body: "late"}); err == nil {
		t.Fatal("a late thought reopened an idle session's anchor")
	}

	// A plain completed agent task stays terminal.
	insertTask("task2")
	if _, err := appendActivity("task2", domain.AgentActivity{Type: "response", ActorID: "app", Body: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := appendActivity("task2", domain.AgentActivity{Type: "action", ActorID: "app", Body: "x", Metadata: map[string]any{"action": "x"}}); err == nil {
		t.Fatal("a completed agent task accepted an action")
	}
}
