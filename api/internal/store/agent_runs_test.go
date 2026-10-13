package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func openAgentRunStore(t *testing.T) *SQLiteStore {
	t.Helper()
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "agent-runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

func enqueueTestRun(t *testing.T, repo *SQLiteStore, workspace, session, kind string) AgentRun {
	t.Helper()
	run, created, err := repo.EnqueueAgentRun(t.Context(), AgentRunInput{WorkspaceKey: workspace, SessionID: session, Kind: kind, Payload: json.RawMessage(`{"prompt":"hi"}`)})
	if err != nil || !created {
		t.Fatalf("enqueue: %v created=%v", err, created)
	}
	return run
}

func TestAgentRunEnqueueIsIdempotentAndValidated(t *testing.T) {
	repo := openAgentRunStore(t)
	ctx := t.Context()
	input := AgentRunInput{WorkspaceKey: "acme", SessionID: "task_1", Kind: AgentRunKindThread, IdempotencyKey: "task_1:prompt:3", Payload: json.RawMessage(`{"a":1}`)}
	first, created, err := repo.EnqueueAgentRun(ctx, input)
	if err != nil || !created || first.Status != AgentRunQueued || first.Priority != 1 || string(first.Payload) != `{"a":1}` || string(first.Principal) != `{}` {
		t.Fatalf("first enqueue: %+v created=%v err=%v", first, created, err)
	}
	again, created, err := repo.EnqueueAgentRun(ctx, input)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("same key enqueued twice: %+v created=%v err=%v", again, created, err)
	}
	// The key is unique per workspace only, and runs without a key never collide.
	input.WorkspaceKey = "other"
	if other, created, err := repo.EnqueueAgentRun(ctx, input); err != nil || !created || other.ID == first.ID {
		t.Fatalf("key leaked across workspaces: %+v %v %v", other, created, err)
	}
	for range 2 {
		if _, created, err := repo.EnqueueAgentRun(ctx, AgentRunInput{WorkspaceKey: "acme", SessionID: "task_2", Kind: AgentRunKindChat}); err != nil || !created {
			t.Fatalf("keyless enqueue: %v %v", created, err)
		}
	}
	for _, bad := range []AgentRunInput{
		{WorkspaceKey: "acme", SessionID: "s", Kind: "coding"},
		{WorkspaceKey: "", SessionID: "s", Kind: AgentRunKindChat},
		{WorkspaceKey: "acme", SessionID: "s", Kind: AgentRunKindChat, Payload: json.RawMessage(`{`)},
	} {
		if _, _, err := repo.EnqueueAgentRun(ctx, bad); !errors.Is(err, ErrAgentRunInput) {
			t.Errorf("bad input %+v accepted: %v", bad, err)
		}
	}
}

func TestAgentRunClaimHonoursLanesPriorityAndWorkspaceCap(t *testing.T) {
	repo := openAgentRunStore(t)
	ctx := t.Context()
	loop := enqueueTestRun(t, repo, "acme", "loop_1", AgentRunKindLoop)
	chatA1 := enqueueTestRun(t, repo, "acme", "chat_1", AgentRunKindChat)
	chatA2 := enqueueTestRun(t, repo, "acme", "chat_2", AgentRunKindChat)
	chatB := enqueueTestRun(t, repo, "beta", "chat_3", AgentRunKindChat)
	delayed, _, err := repo.EnqueueAgentRun(ctx, AgentRunInput{WorkspaceKey: "beta", SessionID: "chat_4", Kind: AgentRunKindChat, NotBefore: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// Only chat slots: chats only, fairly across workspaces, never more than one per workspace.
	runs, err := repo.ClaimAgentRuns(ctx, AgentRunClaim{Owner: "worker-a", Free: map[int]int{0: 5}, MaxPerWorkspace: 1, Lease: time.Minute, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, run := range runs {
		ids[run.ID] = true
		if run.Status != AgentRunLeased || run.LeaseOwner != "worker-a" || run.Attempts != 1 || run.LeaseUntil != now.Add(time.Minute).UnixMilli() || string(run.Payload) != `{"prompt":"hi"}` {
			t.Fatalf("claimed run %+v", run)
		}
	}
	if len(runs) != 2 || !ids[chatA1.ID] || !ids[chatB.ID] || ids[chatA2.ID] || ids[loop.ID] || ids[delayed.ID] {
		t.Fatalf("claimed %v", ids)
	}
	if count, err := repo.CountActiveAgentRuns(ctx, "acme"); err != nil || count != 1 {
		t.Fatalf("active acme runs = %d %v", count, err)
	}
	// The cap holds across claims and workers; the loop lane is served when asked for.
	runs, err = repo.ClaimAgentRuns(ctx, AgentRunClaim{Owner: "worker-b", Free: map[int]int{0: 5, 2: 1}, MaxPerWorkspace: 2, Lease: time.Minute, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != chatA2.ID {
		t.Fatalf("second claim = %+v", runs)
	}
	runs, err = repo.ClaimAgentRuns(ctx, AgentRunClaim{Owner: "worker-b", Free: map[int]int{2: 1}, MaxPerWorkspace: 3, Now: now})
	if err != nil || len(runs) != 1 || runs[0].ID != loop.ID {
		t.Fatalf("loop claim = %+v %v", runs, err)
	}
	if runs, err := repo.ClaimAgentRuns(ctx, AgentRunClaim{Owner: "worker-b", Free: map[int]int{0: 1, 1: 1, 2: 1, 3: 1}, Now: now}); err != nil || len(runs) != 0 {
		t.Fatalf("nothing should be due: %+v %v", runs, err)
	}
	if _, err := repo.ClaimAgentRuns(ctx, AgentRunClaim{Free: map[int]int{0: 1}}); !errors.Is(err, ErrAgentRunInput) {
		t.Fatalf("claim without owner: %v", err)
	}
}

func TestAgentRunConcurrentClaimsNeverShareARun(t *testing.T) {
	repo := openAgentRunStore(t)
	for index := range 12 {
		enqueueTestRun(t, repo, "acme", "session_"+string(rune('a'+index)), AgentRunKindThread)
	}
	var mu sync.Mutex
	seen := map[string]string{}
	var group sync.WaitGroup
	for worker := range 4 {
		group.Add(1)
		go func(owner string) {
			defer group.Done()
			for range 6 {
				runs, err := repo.ClaimAgentRuns(context.Background(), AgentRunClaim{Owner: owner, Free: map[int]int{1: 2}, Lease: time.Minute})
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				for _, run := range runs {
					if previous, ok := seen[run.ID]; ok {
						t.Errorf("run %s claimed by %s and %s", run.ID, previous, owner)
					}
					seen[run.ID] = owner
				}
				mu.Unlock()
			}
		}("worker-" + string(rune('a'+worker)))
	}
	group.Wait()
	if len(seen) != 12 {
		t.Fatalf("claimed %d of 12 runs", len(seen))
	}
}

func TestAgentRunHeartbeatFinishAndCancel(t *testing.T) {
	repo := openAgentRunStore(t)
	ctx := t.Context()
	run := enqueueTestRun(t, repo, "acme", "task_1", AgentRunKindThread)
	now := time.Now()
	if _, err := repo.ClaimAgentRuns(ctx, AgentRunClaim{Owner: "w1", Free: map[int]int{1: 1}, Lease: time.Minute, Now: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.HeartbeatAgentRun(ctx, run.ID, "w2", time.Minute, now); !errors.Is(err, ErrAgentRunLeaseLost) {
		t.Fatalf("foreign heartbeat: %v", err)
	}
	if _, err := repo.HeartbeatAgentRun(ctx, "missing", "w1", time.Minute, now); !errors.Is(err, ErrAgentRunNotFound) {
		t.Fatalf("missing heartbeat: %v", err)
	}
	beat, err := repo.HeartbeatAgentRun(ctx, run.ID, "w1", 2*time.Minute, now.Add(time.Second))
	if err != nil || beat.Status != AgentRunRunning || beat.LeaseUntil != now.Add(time.Second+2*time.Minute).UnixMilli() || beat.CancelRequested {
		t.Fatalf("heartbeat: %+v %v", beat, err)
	}
	if canceled, err := repo.CancelAgentRun(ctx, run.ID); err != nil || canceled.Status != AgentRunRunning || !canceled.CancelRequested {
		t.Fatalf("cancel of a running run: %+v %v", canceled, err)
	}
	if beat, err = repo.HeartbeatAgentRun(ctx, run.ID, "w1", time.Minute, now.Add(2*time.Second)); err != nil || !beat.CancelRequested {
		t.Fatalf("heartbeat after cancel: %+v %v", beat, err)
	}
	if err := repo.CreateAgentRunToken(ctx, beat, "hash-1", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FinishAgentRun(ctx, run.ID, "w1", AgentRunResult{Status: "bogus"}, now); !errors.Is(err, ErrAgentRunInput) {
		t.Fatalf("bad status: %v", err)
	}
	done, err := repo.FinishAgentRun(ctx, run.ID, "w1", AgentRunResult{Status: AgentRunCanceled, Usage: json.RawMessage(`{"inputTokens":12}`)}, now.Add(3*time.Second))
	if err != nil || done.Status != AgentRunCanceled || done.LeaseOwner != "" || done.FinishedAt == 0 || string(done.Usage) != `{"inputTokens":12}` {
		t.Fatalf("finish: %+v %v", done, err)
	}
	if _, err := repo.AgentRunForToken(ctx, "hash-1", now); !errors.Is(err, ErrAgentRunTokenInvalid) {
		t.Fatalf("token survived the run: %v", err)
	}
	if _, err := repo.FinishAgentRun(ctx, run.ID, "w1", AgentRunResult{Status: AgentRunDone}, now); !errors.Is(err, ErrAgentRunLeaseLost) {
		t.Fatalf("finished twice: %v", err)
	}
	queued := enqueueTestRun(t, repo, "acme", "task_2", AgentRunKindThread)
	if canceled, err := repo.CancelAgentRun(ctx, queued.ID); err != nil || canceled.Status != AgentRunCanceled {
		t.Fatalf("cancel of a queued run: %+v %v", canceled, err)
	}
}

func TestAgentRunExpiredLeasesRetryOrInterrupt(t *testing.T) {
	repo := openAgentRunStore(t)
	ctx := t.Context()
	clean := enqueueTestRun(t, repo, "acme", "task_clean", AgentRunKindThread)
	wrote := enqueueTestRun(t, repo, "acme", "task_wrote", AgentRunKindThread)
	start := time.Now()
	if runs, err := repo.ClaimAgentRuns(ctx, AgentRunClaim{Owner: "w1", Free: map[int]int{1: 2}, Lease: time.Minute, Now: start}); err != nil || len(runs) != 2 {
		t.Fatalf("claim: %v %v", runs, err)
	}
	if err := repo.MarkAgentRunWrote(ctx, wrote.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateAgentRunToken(ctx, clean, "hash-clean", start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AgentRunForToken(ctx, "hash-clean", start); err != nil {
		t.Fatalf("token of a held run: %v", err)
	}
	// Nothing is released before the lease expires.
	if requeued, interrupted, err := repo.ReleaseExpiredAgentRuns(ctx, start.Add(30*time.Second), 2); err != nil || len(requeued)+len(interrupted) != 0 {
		t.Fatalf("early release: %v %v %v", requeued, interrupted, err)
	}
	wake := repo.AgentRunWake()
	requeued, interrupted, err := repo.ReleaseExpiredAgentRuns(ctx, start.Add(2*time.Minute), 2)
	if err != nil || len(requeued) != 1 || requeued[0].ID != clean.ID || requeued[0].Status != AgentRunQueued || len(interrupted) != 1 || interrupted[0].ID != wrote.ID || interrupted[0].Status != AgentRunInterrupted {
		t.Fatalf("release: %+v %+v %v", requeued, interrupted, err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("a requeued run did not wake waiting claims")
	}
	if _, err := repo.AgentRunForToken(ctx, "hash-clean", start); !errors.Is(err, ErrAgentRunTokenInvalid) {
		t.Fatalf("token survived the expired lease: %v", err)
	}
	// The retry is the run's second and last attempt.
	if runs, err := repo.ClaimAgentRuns(ctx, AgentRunClaim{Owner: "w2", Free: map[int]int{1: 1}, Now: start.Add(2 * time.Minute)}); err != nil || len(runs) != 1 || runs[0].Attempts != 2 {
		t.Fatalf("reclaim: %+v %v", runs, err)
	}
	if requeued, interrupted, err := repo.ReleaseExpiredAgentRuns(ctx, start.Add(10*time.Minute), 2); err != nil || len(requeued) != 0 || len(interrupted) != 1 {
		t.Fatalf("second expiry: %+v %+v %v", requeued, interrupted, err)
	}
}

func TestAgentRunChangesKeysAndSessionState(t *testing.T) {
	repo := openAgentRunStore(t)
	ctx := t.Context()
	run := enqueueTestRun(t, repo, "acme", "task_1", AgentRunKindThread)
	for index, entity := range []string{"issue", "project"} {
		seq, err := repo.AppendAgentRunChange(ctx, run, "save_"+entity, json.RawMessage(`{"title":"x"}`), domain.AgentChange{Action: "created", EntityType: entity, EntityID: entity + "_1", Undoable: true})
		if err != nil || seq != int64(index+1) {
			t.Fatalf("append change %d: %d %v", index, seq, err)
		}
	}
	changes, err := repo.AgentRunChanges(ctx, run.ID)
	if err != nil || len(changes) != 2 || changes[1].Change.EntityType != "project" || string(changes[0].Arguments) != `{"title":"x"}` {
		t.Fatalf("changes: %+v %v", changes, err)
	}
	changes[0].Change.State = "undone"
	if err := repo.UpdateAgentRunChange(ctx, run.ID, 1, changes[0].Change); err != nil {
		t.Fatal(err)
	}
	if changes, _ = repo.AgentRunChanges(ctx, run.ID); changes[0].Change.State != "undone" {
		t.Fatalf("update lost: %+v", changes[0])
	}
	if err := repo.UpdateAgentRunChange(ctx, run.ID, 9, domain.AgentChange{}); !errors.Is(err, ErrAgentRunNotFound) {
		t.Fatalf("missing change: %v", err)
	}

	if _, found, err := repo.AgentRunKey(ctx, run.ID, "activity:a"); err != nil || found {
		t.Fatalf("empty key: %v %v", found, err)
	}
	for _, value := range []string{"first", "second"} {
		if err := repo.SaveAgentRunKey(ctx, run, "activity:a", []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if value, found, err := repo.AgentRunKey(ctx, run.ID, "activity:a"); err != nil || !found || string(value) != "first" {
		t.Fatalf("key: %q %v %v", value, found, err)
	}

	if _, err := repo.AgentSessionStateFor(ctx, "acme", "task_1"); !errors.Is(err, ErrAgentRunNotFound) {
		t.Fatalf("empty state: %v", err)
	}
	for _, blob := range []string{"state-1", "state-2"} {
		if err := repo.SaveAgentSessionState(ctx, AgentSessionState{WorkspaceKey: "acme", SessionID: "task_1", RunID: run.ID, ContentType: "application/json", Data: []byte(blob)}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := repo.AgentSessionStateFor(ctx, "acme", "task_1")
	if err != nil || string(state.Data) != "state-2" || state.ContentType != "application/json" || state.RunID != run.ID {
		t.Fatalf("state: %+v %v", state, err)
	}
	if err := repo.SaveAgentSessionState(ctx, AgentSessionState{WorkspaceKey: "acme", SessionID: "task_1", Data: make([]byte, AgentSessionStateMaxBytes+1)}); !errors.Is(err, ErrAgentRunInput) {
		t.Fatalf("oversized state: %v", err)
	}
	if err := repo.DeleteAgentSessionState(ctx, "acme", "task_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AgentSessionStateFor(ctx, "acme", "task_1"); !errors.Is(err, ErrAgentRunNotFound) {
		t.Fatalf("deleted state: %v", err)
	}
}
