package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// codingAnchorFixture is a store with an application installation and
// delegation tasks ended in each way an AgentTask can end.
type codingAnchorFixture struct {
	t    *testing.T
	repo *SQLiteStore
}

func newCodingAnchorFixture(t *testing.T) codingAnchorFixture {
	t.Helper()
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "anchor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	return codingAnchorFixture{t: t, repo: repo}
}

func (f codingAnchorFixture) install(userID string, active bool) {
	f.t.Helper()
	raw := []byte(`{"id":"inst_` + userID + `","workspaceKey":"w","clientId":"builtin_` + userID + `","userId":"` + userID + `","builtin":true,"active":` + map[bool]string{true: "true", false: "false"}[active] + `}`)
	if _, err := f.repo.db.ExecContext(f.t.Context(), `INSERT INTO application_installations(id,workspace_key,client_id,user_id,data,webhook_secret) VALUES(?,?,?,?,?,'')`, "inst_"+userID, "w", "builtin_"+userID, userID, raw); err != nil {
		f.t.Fatal(err)
	}
}

func (f codingAnchorFixture) task(id string) domain.AgentTask {
	f.t.Helper()
	task, err := f.repo.AgentTask(f.t.Context(), "w", id)
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f codingAnchorFixture) append(id string, activity domain.AgentActivity) (domain.AgentTask, error) {
	return f.repo.AppendAgentActivity(f.t.Context(), "w", id, f.task(id).Version, activity)
}

// endedDelegation creates a delegation task for app "app" and ends it.
func (f codingAnchorFixture) endedDelegation(id, how string) domain.AgentTask {
	f.t.Helper()
	ctx := f.t.Context()
	if _, err := f.repo.CreateAgentInitiatedTask(ctx, domain.AgentTask{ID: id, WorkspaceKey: "w", IssueID: "issue1", AppUserID: "app", CreatorID: "requester"}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.append(id, domain.AgentActivity{ActorID: "app", Type: "thought", Body: "Looking at the issue"}); err != nil {
		f.t.Fatal(err)
	}
	var err error
	switch how {
	case "complete":
		_, err = f.append(id, domain.AgentActivity{ActorID: "app", Type: "response", Body: "Done."})
	case "error":
		_, err = f.append(id, domain.AgentActivity{ActorID: "app", Type: "error", Body: "Execution was interrupted."})
	case "canceledByPerson":
		_, err = f.append(id, domain.AgentActivity{ActorID: "requester", Type: "prompt", Signal: "stop"})
	case "canceledBySystem":
		// The application worker's revocation cancels carry no actor.
		_, err = f.append(id, domain.AgentActivity{Type: "canceled", Body: "Application access was revoked."})
	case "canceledByUndelegation":
		// syncApplicationTask and uninstall cancel in SQL, without an activity.
		_, err = f.repo.db.ExecContext(ctx, `UPDATE application_agent_tasks SET status='canceled',version=version+1 WHERE id=? AND workspace_key='w'`, id)
	default:
		f.t.Fatalf("unknown ending %q", how)
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return f.task(id)
}

func codingSessionOn(id, anchorID string, phase domain.CodingSessionPhase) domain.CodingSession {
	now := time.Now().UTC()
	return domain.CodingSession{ID: id, WorkspaceKey: "w", AgentSessionID: anchorID, IssueID: "issue1", CreatorID: "requester", Source: domain.CodingSourceIssue, State: domain.CodingSessionAwaitingInput, Phase: phase, PhaseStartedAt: now, CreatedAt: now, UpdatedAt: now}
}

// codingAnchor is the anchor a starter passes for a reused delegation task.
func codingAnchor(id string) domain.AgentTask {
	now := time.Now().UTC()
	return domain.AgentTask{ID: id, WorkspaceKey: "w", IssueID: "issue1", AppUserID: "app", CreatorID: "requester", Status: "pending", Version: 1, CreatedAt: now, UpdatedAt: now}
}

func countActivities(t *testing.T, repo *SQLiteStore, id string, match func(domain.AgentActivity) bool) int {
	t.Helper()
	activities, err := repo.AgentActivities(t.Context(), "w", id, "")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, activity := range activities {
		if match(activity) {
			count++
		}
	}
	return count
}

func reopenAudit(activity domain.AgentActivity) bool {
	_, ok := activity.Metadata["reopenedFrom"]
	return ok
}

// A coding session started on a delegation task that already ended reopens
// the task (with an audit activity) when the delegation still holds, so the
// approval elicitation and the session's progress reach the thread; a task
// whose access was withdrawn is refused instead of being revived.
func TestCreateCodingSessionOnEndedDelegationAnchor(t *testing.T) {
	for _, how := range []string{"complete", "error", "canceledByPerson"} {
		t.Run("reopens "+how, func(t *testing.T) {
			f := newCodingAnchorFixture(t)
			f.install("app", true)
			ended := f.endedDelegation("task1", how)
			created, isNew, err := f.repo.CreateCodingSession(t.Context(), codingSessionOn("cs1", "task1", domain.CodingPhaseAwaitingApproval), codingAnchor("task1"), "delegation:task1")
			if err != nil || !isNew || created.AgentSessionID != "task1" {
				t.Fatalf("create: %+v new=%v err=%v", created, isNew, err)
			}
			anchor := f.task("task1")
			if anchor.Status != "pending" || anchor.CodingSessionID != "cs1" || anchor.Version != ended.Version+1 || anchor.CreatorID != "requester" || anchor.IssueID != "issue1" {
				t.Fatalf("anchor after create: %+v (was %s v%d)", anchor, ended.Status, ended.Version)
			}
			if got := countActivities(t, f.repo, "task1", func(a domain.AgentActivity) bool {
				return reopenAudit(a) && a.Metadata["reopenedFrom"] == ended.Status && a.Metadata["reopenedBy"] == "requester" && a.Metadata["codingSessionId"] == "cs1"
			}); got != 1 {
				t.Fatalf("reopen audit activities = %d", got)
			}
			// The approval elicitation is accepted on the reopened anchor.
			if task, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "elicitation", Body: "Approve?", Signal: "select"}); err != nil || task.Status != "awaitingInput" {
				t.Fatalf("approval elicitation: status=%q err=%v", task.Status, err)
			}
			// A replayed create returns the same session and reopens nothing again.
			again, isNew, err := f.repo.CreateCodingSession(t.Context(), codingSessionOn("cs2", "task1", domain.CodingPhaseAwaitingApproval), codingAnchor("task1"), "delegation:task1")
			if err != nil || isNew || again.ID != "cs1" {
				t.Fatalf("replay: %+v new=%v err=%v", again, isNew, err)
			}
			if got := countActivities(t, f.repo, "task1", reopenAudit); got != 1 {
				t.Fatalf("reopen audits after replay = %d", got)
			}
		})
	}

	revoked := map[string]func(f codingAnchorFixture){
		"canceledBySystem": func(f codingAnchorFixture) { f.install("app", true); f.endedDelegation("task1", "canceledBySystem") },
		"canceledByUndelegation": func(f codingAnchorFixture) {
			f.install("app", true)
			f.endedDelegation("task1", "canceledByUndelegation")
		},
		"complete, app deactivated": func(f codingAnchorFixture) {
			f.install("app", false)
			f.endedDelegation("task1", "complete")
		},
		"error, app uninstalled": func(f codingAnchorFixture) { f.endedDelegation("task1", "error") },
	}
	for name, setup := range revoked {
		t.Run("refuses "+name, func(t *testing.T) {
			f := newCodingAnchorFixture(t)
			setup(f)
			before := f.task("task1")
			_, isNew, err := f.repo.CreateCodingSession(t.Context(), codingSessionOn("cs1", "task1", domain.CodingPhaseQueued), codingAnchor("task1"), "delegation:task1")
			if !errors.Is(err, ErrCodingAnchorRevoked) || isNew {
				t.Fatalf("create on a revoked anchor: new=%v err=%v", isNew, err)
			}
			if _, err := f.repo.CodingSession(t.Context(), "w", "cs1"); !errors.Is(err, ErrCodingNotFound) {
				t.Fatalf("a session was stored: %v", err)
			}
			after := f.task("task1")
			if after.Status != before.Status || after.Version != before.Version || after.CodingSessionID != "" {
				t.Fatalf("anchor changed: %+v → %+v", before, after)
			}
		})
	}

	t.Run("concurrent creates reopen once", func(t *testing.T) {
		f := newCodingAnchorFixture(t)
		f.install("app", true)
		f.endedDelegation("task1", "complete")
		var wg sync.WaitGroup
		var mu sync.Mutex
		ids, created := map[string]bool{}, 0
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				session, isNew, err := f.repo.CreateCodingSession(t.Context(), codingSessionOn("cs"+string(rune('a'+i)), "task1", domain.CodingPhaseAwaitingApproval), codingAnchor("task1"), "delegation:task1")
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					t.Errorf("create %d: %v", i, err)
					return
				}
				ids[session.ID] = true
				if isNew {
					created++
				}
			}(i)
		}
		wg.Wait()
		if created != 1 || len(ids) != 1 {
			t.Fatalf("created=%d sessions=%v", created, ids)
		}
		if got := countActivities(t, f.repo, "task1", reopenAudit); got != 1 {
			t.Fatalf("reopen audits = %d", got)
		}
	})
}

// The coding session's activity flows back to an anchor the delegating
// agent ended after the session started (a reply or an error), and an
// approval decided outside the thread is recorded on an anchor that waits
// for input; a person's stop, idle and done sessions and plain tasks keep
// the terminal guard.
func TestCodingAnchorActivityAfterDelegationEnds(t *testing.T) {
	setPhase := func(f codingAnchorFixture, phase domain.CodingSessionPhase) {
		t.Helper()
		if _, err := f.repo.UpdateCodingSession(t.Context(), "w", "cs1", func(session *domain.CodingSession) error {
			session.Phase = phase
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	start := func(t *testing.T, phase domain.CodingSessionPhase) codingAnchorFixture {
		f := newCodingAnchorFixture(t)
		f.install("app", true)
		if _, err := f.repo.CreateAgentInitiatedTask(t.Context(), domain.AgentTask{ID: "task1", WorkspaceKey: "w", IssueID: "issue1", AppUserID: "app", CreatorID: "requester"}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.repo.CreateCodingSession(t.Context(), codingSessionOn("cs1", "task1", phase), codingAnchor("task1"), "delegation:task1"); err != nil {
			t.Fatal(err)
		}
		return f
	}

	t.Run("error by the delegating agent mid-turn", func(t *testing.T) {
		f := start(t, domain.CodingPhaseRunning)
		if _, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "error", Body: "Execution was interrupted."}); err != nil {
			t.Fatal(err)
		}
		task, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "thought", Body: "Running the tests"})
		if err != nil || task.Status != "active" {
			t.Fatalf("turn progress on an errored anchor: status=%q err=%v", task.Status, err)
		}
	})
	t.Run("approval re-requested after the delegating agent replied", func(t *testing.T) {
		f := start(t, domain.CodingPhaseAwaitingApproval)
		if _, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "response", Body: "Started a coding session."}); err != nil {
			t.Fatal(err)
		}
		task, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "elicitation", Body: "Approve?", Signal: "select"})
		if err != nil || task.Status != "awaitingInput" {
			t.Fatalf("approval elicitation on a completed anchor: status=%q err=%v", task.Status, err)
		}
	})
	t.Run("approval decided outside the thread", func(t *testing.T) {
		f := start(t, domain.CodingPhaseAwaitingApproval)
		if _, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "elicitation", Body: "Approve?", Signal: "select"}); err != nil {
			t.Fatal(err)
		}
		// Still awaiting approval: the anchor keeps waiting for the answer.
		if _, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "action", Body: "Coding session approved."}); !errors.Is(err, ErrIssueVersion) {
			t.Fatalf("decision before the session moved on: %v", err)
		}
		setPhase(f, domain.CodingPhaseQueued)
		task, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "action", Body: "Coding session approved."})
		if err != nil || task.Status != "active" {
			t.Fatalf("decision on an anchor waiting for input: status=%q err=%v", task.Status, err)
		}
	})
	t.Run("a person's stop keeps the anchor closed", func(t *testing.T) {
		f := start(t, domain.CodingPhaseRunning)
		if _, err := f.append("task1", domain.AgentActivity{ActorID: "requester", Type: "prompt", Signal: "stop"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "thought", Body: "late"}); !errors.Is(err, ErrIssueVersion) {
			t.Fatalf("progress reopened a stopped anchor: %v", err)
		}
	})
	t.Run("done sessions keep the anchor closed", func(t *testing.T) {
		f := start(t, domain.CodingPhaseRunning)
		if _, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: "error", Body: "boom"}); err != nil {
			t.Fatal(err)
		}
		setPhase(f, domain.CodingPhaseDone)
		for _, kind := range []string{"thought", "action", "elicitation"} {
			if _, err := f.append("task1", domain.AgentActivity{ActorID: "app", Type: kind, Body: "late", Metadata: map[string]any{"action": "late"}}); !errors.Is(err, ErrIssueVersion) {
				t.Fatalf("%s reopened a done session's anchor: %v", kind, err)
			}
		}
	})
	t.Run("plain tasks keep their guards", func(t *testing.T) {
		f := newCodingAnchorFixture(t)
		f.endedDelegation("plain", "error")
		if _, err := f.append("plain", domain.AgentActivity{ActorID: "app", Type: "thought", Body: "x"}); !errors.Is(err, ErrIssueVersion) {
			t.Fatalf("an errored task accepted a thought: %v", err)
		}
		if _, err := f.repo.CreateAgentInitiatedTask(t.Context(), domain.AgentTask{ID: "asking", WorkspaceKey: "w", IssueID: "issue1", AppUserID: "app"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.append("asking", domain.AgentActivity{ActorID: "app", Type: "elicitation", Body: "Which one?"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.append("asking", domain.AgentActivity{ActorID: "app", Type: "action", Body: "x", Metadata: map[string]any{"action": "x"}}); !errors.Is(err, ErrIssueVersion) {
			t.Fatalf("a task waiting for input accepted an action: %v", err)
		}
	})
}
