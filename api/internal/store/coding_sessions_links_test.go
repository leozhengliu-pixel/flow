package store

import (
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestCodingInboxDedupesTurnsAndLinksReusedAnchors(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "coding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	ctx := t.Context()
	now := time.Now().UTC()
	var events []domain.RealtimeEvent
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { events = append(events, event) })
	// An existing delegation task becomes the anchor (track L reuses it).
	delegation := domain.AgentTask{ID: "task1", WorkspaceKey: "w", IssueID: "issue1", AppUserID: "app", CreatorID: "u", Status: "pending", Trigger: "delegation", Version: 1, CreatedAt: now, UpdatedAt: now}
	raw := []byte(`{"id":"task1","workspaceKey":"w","issueId":"issue1","appUserId":"app","creatorId":"u","status":"pending","trigger":"delegation","version":1}`)
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at) VALUES(?,?,?,?,?,1,?,?)`, "task1", "w", "issue1", "app", "pending", raw, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	session := domain.CodingSession{ID: "cs1", WorkspaceKey: "w", AgentSessionID: "task1", IssueID: "issue1", CreatorID: "u", Source: domain.CodingSourceIssue, State: "pending", Phase: domain.CodingPhaseQueued, PhaseStartedAt: now, CreatedAt: now, UpdatedAt: now}
	if _, created, err := repo.CreateCodingSession(ctx, session, delegation, ""); err != nil || !created {
		t.Fatalf("create on a reused anchor: created=%v err=%v", created, err)
	}
	var data []byte
	var nextAttempt string
	if err := repo.db.QueryRowContext(ctx, `SELECT data,next_attempt FROM application_agent_tasks WHERE id='task1'`).Scan(&data, &nextAttempt); err != nil {
		t.Fatal(err)
	}
	if string(data) == string(raw) || nextAttempt != agentSessionNeverDue {
		t.Fatalf("reused anchor not linked/parked: %s next=%q", data, nextAttempt)
	}
	if codingInboxTurnID(data) != "" {
		t.Fatal("sanity: an agent task is not an inbox item")
	}
	if task, err := repo.AgentTask(ctx, "w", "task1"); err == nil && task.CodingSessionID != "cs1" {
		t.Fatalf("codingSessionId = %q", task.CodingSessionID)
	}

	if _, err := repo.UpdateCodingSession(ctx, "w", "cs1", func(current *domain.CodingSession) error {
		current.Phase = domain.CodingPhaseProvisioning
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	codingEvents := []string{}
	for _, event := range events {
		if event.AggregateID == "cs1" {
			codingEvents = append(codingEvents, event.Type)
		}
	}
	if len(codingEvents) != 2 || codingEvents[0] != "coding_session.created" || codingEvents[1] != "coding_session.updated" {
		t.Fatalf("coding_session realtime events = %v", codingEvents)
	}

	first, err := repo.EnqueueCodingInbox(ctx, "w", "cs1", "turn", []byte(`{"turn":{"id":"turn_a","prompt":"go"}}`))
	if err != nil {
		t.Fatal(err)
	}
	again, err := repo.EnqueueCodingInbox(ctx, "w", "cs1", "turn", []byte(`{"turn":{"id":"turn_a","prompt":"go (redelivered)"}}`))
	if err != nil || again != first {
		t.Fatalf("duplicate turn enqueued: %d vs %d (%v)", again, first, err)
	}
	other, err := repo.EnqueueCodingInbox(ctx, "w", "cs1", "turn", []byte(`{"turn":{"id":"turn_b","prompt":"next"}}`))
	if err != nil || other == first {
		t.Fatalf("distinct turn: %d %v", other, err)
	}
	stop1, _ := repo.EnqueueCodingInbox(ctx, "w", "cs1", "stop", []byte(`{"data":{}}`))
	stop2, _ := repo.EnqueueCodingInbox(ctx, "w", "cs1", "stop", []byte(`{"data":{}}`))
	if stop1 == stop2 {
		t.Fatal("items without a turn id are never deduplicated")
	}
	items, _ := repo.CodingInbox(ctx, "cs1", 0)
	if len(items) != 4 {
		t.Fatalf("inbox has %d items, want 4", len(items))
	}
}
