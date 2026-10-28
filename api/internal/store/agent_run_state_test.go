package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Two stores on one SQLite file stand for two API servers on one database.
func TestAgentRunStateIsSharedByStoresOnOneDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-run-state.db")
	open := func() *SQLiteStore {
		repo, err := OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repo.Close() })
		return repo
	}
	exerciseAgentRunState(t, open(), open())
}

// exerciseAgentRunState checks the per-run state tables through two stores on one database: concurrent ledger
// entries are all kept (a repeated key replaces its entry), concurrent compare-and-swap updates of one state lose
// none, and activities are stored once per (attempt, seq) and listed in seq order whatever order they came in.
func exerciseAgentRunState(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	ctx := t.Context()
	workspace := fmt.Sprintf("agent-run-state-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		for _, table := range []string{"agent_run_tool_calls", "agent_run_states", "agent_run_activities"} {
			_, _ = first.db.ExecContext(context.Background(), "DELETE FROM "+table+" WHERE workspace_key=?", workspace)
		}
	})
	run := AgentRun{ID: "run_" + workspace, WorkspaceKey: workspace}
	stores := []*SQLiteStore{first, second}
	parallel := func(count int, work func(index int, repo *SQLiteStore) error) {
		t.Helper()
		var group sync.WaitGroup
		errs := make([]error, count)
		for index := range count {
			group.Add(1)
			go func() {
				defer group.Done()
				errs[index] = work(index, stores[index%2])
			}()
		}
		group.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	// Ledger entries: one row per call, whichever store records it.
	parallel(40, func(index int, repo *SQLiteStore) error {
		return repo.RecordAgentRunToolCall(ctx, run, "call:"+strconv.Itoa(index), int64(1000+index), []byte(`{"n":`+strconv.Itoa(index)+`}`))
	})
	if err := second.RecordAgentRunToolCall(ctx, run, "call:3", 1, []byte(`{"n":"retried"}`)); err != nil {
		t.Fatal(err)
	}
	outcomes, err := first.AgentRunToolCalls(ctx, run.ID)
	if err != nil || len(outcomes) != 40 {
		t.Fatalf("ledger rows = %d %v", len(outcomes), err)
	}
	if string(outcomes[0]) != `{"n":0}` || string(outcomes[3]) != `{"n":"retried"}` || string(outcomes[39]) != `{"n":39}` {
		t.Fatalf("ledger order or retry: %s %s %s", outcomes[0], outcomes[3], outcomes[39])
	}
	next := AgentRun{ID: run.ID + "_resume", WorkspaceKey: workspace}
	if err := second.CopyAgentRunToolCalls(ctx, run.ID, next); err != nil {
		t.Fatal(err)
	}
	if err := second.CopyAgentRunToolCalls(ctx, run.ID, next); err != nil {
		t.Fatal(err)
	}
	if copied, err := first.AgentRunToolCalls(ctx, next.ID); err != nil || len(copied) != 40 || string(copied[3]) != `{"n":"retried"}` {
		t.Fatalf("copied ledger = %d %v", len(copied), err)
	}
	if err := first.DeleteAgentRunToolCalls(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if left, err := second.AgentRunToolCalls(ctx, next.ID); err != nil || len(left) != 0 {
		t.Fatalf("deleted ledger = %d %v", len(left), err)
	}

	// A versioned state: concurrent increments through both stores all count.
	parallel(40, func(_ int, repo *SQLiteStore) error {
		for range 400 {
			raw, version, found, err := repo.AgentRunState(ctx, run.ID, "counter")
			if err != nil {
				return err
			}
			count := 0
			if found {
				_ = json.Unmarshal(raw, &count)
			}
			swapped, err := repo.SwapAgentRunState(ctx, run, "counter", version, []byte(strconv.Itoa(count+1)))
			if err != nil || swapped {
				return err
			}
			time.Sleep(time.Millisecond)
		}
		return fmt.Errorf("the counter kept changing")
	})
	raw, version, found, err := second.AgentRunState(ctx, run.ID, "counter")
	if err != nil || !found || string(raw) != "40" || version != 40 {
		t.Fatalf("counter = %s v%d %v %v", raw, version, found, err)
	}
	if swapped, err := first.SwapAgentRunState(ctx, run, "counter", 39, []byte("0")); err != nil || swapped {
		t.Fatalf("a stale swap = %v %v", swapped, err)
	}
	if err := first.ReplaceAgentRunState(ctx, run, "counter", []byte("null")); err != nil {
		t.Fatal(err)
	}
	if swapped, err := second.SwapAgentRunState(ctx, run, "counter", 40, []byte("41")); err != nil || swapped {
		t.Fatalf("a swap over a replaced state = %v %v", swapped, err)
	}
	if raw, version, _, err := second.AgentRunState(ctx, run.ID, "counter"); err != nil || string(raw) != "null" || version != 41 {
		t.Fatalf("replaced counter = %s v%d %v", raw, version, err)
	}
	if _, _, found, err := first.AgentRunState(ctx, run.ID, "missing"); err != nil || found {
		t.Fatalf("missing state = %v %v", found, err)
	}

	// Activities: stored once per seq, listed in seq order.
	for _, seq := range []int64{3, 1, 2} {
		if stored, err := stores[seq%2].SaveAgentRunActivity(ctx, run, 1, seq, []byte(`{"seq":`+strconv.FormatInt(seq, 10)+`}`)); err != nil || !stored {
			t.Fatalf("save %d: %v %v", seq, stored, err)
		}
	}
	if stored, err := second.SaveAgentRunActivity(ctx, run, 1, 2, []byte(`{"seq":"again"}`)); err != nil || stored {
		t.Fatalf("a repeated seq = %v %v", stored, err)
	}
	activities, err := first.AgentRunActivities(ctx, run.ID, 1, 0)
	if err != nil || len(activities) != 3 || activities[0].Seq != 1 || activities[2].Seq != 3 || string(activities[1].Data) != `{"seq":2}` {
		t.Fatalf("activities = %+v %v", activities, err)
	}
	if after, err := second.AgentRunActivities(ctx, run.ID, 1, 2); err != nil || len(after) != 1 || after[0].Seq != 3 {
		t.Fatalf("activities after 2 = %+v %v", after, err)
	}
	// Unnumbered posts are numbered on arrival, each with its own seq, in another attempt's sequence.
	var mu sync.Mutex
	seqs := []int64{}
	parallel(20, func(index int, repo *SQLiteStore) error {
		seq, err := repo.AppendAgentRunActivity(ctx, run, 2, []byte(`{"n":`+strconv.Itoa(index)+`}`))
		mu.Lock()
		seqs = append(seqs, seq)
		mu.Unlock()
		return err
	})
	slices.Sort(seqs)
	if len(slices.Compact(slices.Clone(seqs))) != 20 || seqs[0] != 1 || seqs[19] != 20 {
		t.Fatalf("appended seqs = %v", seqs)
	}
	if again, err := first.AgentRunActivities(ctx, run.ID, 1, 0); err != nil || len(again) != 3 {
		t.Fatalf("the first attempt's activities = %d %v", len(again), err)
	}
}
