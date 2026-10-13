//go:build integration

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestExternalDatabaseAgentRunQueue runs the agent run queue on MySQL or
// PostgreSQL (FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL): the schema
// is created twice, two store instances claim concurrently through FOR UPDATE
// SKIP LOCKED without sharing a run, and leases, tokens, change sets, keys and
// session state round-trip.
func TestExternalDatabaseAgentRunQueue(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Skip("external database not configured")
	}
	open := func() *SQLiteStore {
		repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repo.Close() })
		return repo
	}
	first, second := open(), open()
	if err := first.ensureAgentRuns(t.Context()); err != nil {
		t.Fatalf("repeat schema: %v", err)
	}
	ctx := t.Context()
	workspace := fmt.Sprintf("agent-runs-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		for _, table := range []string{"agent_runs", "agent_run_tokens", "agent_run_changes", "agent_run_keys", "agent_session_states"} {
			_, _ = first.db.ExecContext(context.Background(), "DELETE FROM "+table+" WHERE workspace_key=?", workspace)
		}
	})
	ids := map[string]bool{}
	for index := range 10 {
		run, created, err := first.EnqueueAgentRun(ctx, AgentRunInput{WorkspaceKey: workspace, SessionID: fmt.Sprintf("s%d", index), Kind: AgentRunKindThread, IdempotencyKey: fmt.Sprintf("k%d", index), Payload: json.RawMessage(`{"n":1}`)})
		if err != nil || !created {
			t.Fatalf("enqueue: %v", err)
		}
		ids[run.ID] = true
	}
	if again, created, err := second.EnqueueAgentRun(ctx, AgentRunInput{WorkspaceKey: workspace, SessionID: "s0", Kind: AgentRunKindThread, IdempotencyKey: "k0"}); err != nil || created || !ids[again.ID] {
		t.Fatalf("idempotent enqueue: %+v %v %v", again, created, err)
	}
	var mu sync.Mutex
	owners := map[string]string{}
	var group sync.WaitGroup
	for index, repo := range []*SQLiteStore{first, second, first, second} {
		group.Add(1)
		go func(owner string, repo *SQLiteStore) {
			defer group.Done()
			for range 5 {
				runs, err := repo.ClaimAgentRuns(context.Background(), AgentRunClaim{Owner: owner, Free: map[int]int{1: 1}, Lease: time.Minute})
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				for _, run := range runs {
					if !ids[run.ID] {
						continue
					}
					if previous, ok := owners[run.ID]; ok {
						t.Errorf("run %s claimed by %s and %s", run.ID, previous, owner)
					}
					owners[run.ID] = owner
				}
				mu.Unlock()
			}
		}(fmt.Sprintf("worker-%d", index), repo)
	}
	group.Wait()
	if len(owners) != len(ids) {
		t.Fatalf("claimed %d of %d runs", len(owners), len(ids))
	}
	var held AgentRun
	for id, owner := range owners {
		var err error
		held, err = second.HeartbeatAgentRun(ctx, id, owner, time.Minute, time.Now())
		if err != nil || held.Status != AgentRunRunning {
			t.Fatalf("heartbeat: %+v %v", held, err)
		}
		break
	}
	if err := first.CreateAgentRunToken(ctx, held, "hash-"+workspace, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if run, err := second.AgentRunForToken(ctx, "hash-"+workspace, time.Now()); err != nil || run.ID != held.ID {
		t.Fatalf("token: %+v %v", run, err)
	}
	if seq, err := first.AppendAgentRunChange(ctx, held, "save_issue", json.RawMessage(`{"id":"x"}`), domain.AgentChange{Action: "updated", EntityType: "issue"}); err != nil || seq != 1 {
		t.Fatalf("change: %d %v", seq, err)
	}
	if err := first.SaveAgentRunKey(ctx, held, "mcp:1", []byte("result")); err != nil {
		t.Fatal(err)
	}
	if value, found, err := second.AgentRunKey(ctx, held.ID, "mcp:1"); err != nil || !found || string(value) != "result" {
		t.Fatalf("key: %q %v %v", value, found, err)
	}
	for _, blob := range []string{"one", "two"} {
		if err := first.SaveAgentSessionState(ctx, AgentSessionState{WorkspaceKey: workspace, SessionID: held.SessionID, RunID: held.ID, Data: []byte(blob)}); err != nil {
			t.Fatal(err)
		}
	}
	if state, err := second.AgentSessionStateFor(ctx, workspace, held.SessionID); err != nil || string(state.Data) != "two" {
		t.Fatalf("state: %+v %v", state, err)
	}
	if _, err := first.FinishAgentRun(ctx, held.ID, owners[held.ID], AgentRunResult{Status: AgentRunDone, Usage: json.RawMessage(`{"tokens":1}`)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	requeued, interrupted, err := first.ReleaseExpiredAgentRuns(ctx, time.Now().Add(5*time.Minute), 2)
	if err != nil {
		t.Fatal(err)
	}
	released := 0
	for _, run := range append(requeued, interrupted...) {
		if ids[run.ID] {
			released++
		}
	}
	if released != len(ids)-1 {
		t.Fatalf("released %d of %d expired runs", released, len(ids)-1)
	}
}
