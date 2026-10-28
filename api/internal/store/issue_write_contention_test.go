package store

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Two servers updating disjoint issue pairs whose counter rows they reach in
// opposite orders (each pair is written in ID order, one issue at a time) do
// not deadlock: a transaction writes the counters and the revision it shares
// with other writers last, all at once, in one order. MySQL and Postgres
// only: SQLite runs one writer at a time, and a second connection's write
// fails fast with SQLITE_BUSY instead.
func TestConcurrentIssueCountWritersDoNotDeadlock(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		if name == "sqlite" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			workspace, template := peerWorkspace(t, pair[0])
			stamp := time.Now().UnixNano()
			template.Team.ID = fmt.Sprintf("deadlock-team-%d", stamp)
			// Writer 0 toggles its first issue between priorities 0 and 1
			// and its second between 2 and 3; writer 1 the other way round.
			ids := [2][2]string{}
			for w := range ids {
				for k := range ids[w] {
					issue := template
					issue.Priority = []int{0, 2}[(w+k)%2]
					ids[w][k] = createPeerIssue(t, pair[1], workspace, fmt.Sprintf("deadlock-%d-%d-%d", stamp, w, k), issue).ID
				}
			}
			const rounds = 40
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for w := range ids {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for r := 0; r < rounds && errs[w] == nil; r++ {
						_, errs[w] = pair[w].UpdateIssueRecord(ctx, workspace, ids[w][0], nil, IssueMutationScope{RelatedIDs: []string{ids[w][1]}}, func(data *domain.Bootstrap, _ *domain.Issue) error {
							for i := range data.Issues {
								data.Issues[i].Priority ^= 1
							}
							return nil
						})
					}
				}(w)
			}
			wg.Wait()
			for w, err := range errs {
				if err != nil {
					t.Fatalf("writer %d: %v", w, err)
				}
			}
			// The counters still match the rows.
			query := IssueRecordQuery{Workspace: workspace, Archived: "all", GroupBy: "priority", TeamIDs: []string{template.Team.ID}}
			got, handled, err := pair[0].issueGroupsFromStats(ctx, query)
			if err != nil || !handled {
				t.Fatalf("counters: %v (handled %v)", err, handled)
			}
			want := []IssueRecordGroup{{Value: "0", Count: 2}, {Value: "2", Count: 2}}
			if !slices.Equal(got, want) {
				t.Fatalf("counters %v, want %v", got, want)
			}
		})
	}
}

// While one server moves issues from one status to another, a count read on
// either server after a move committed always includes it: the cache never
// serves a count older than a write its reader could observe.
func TestIssueGroupCountsNeverOlderThanCommittedWrites(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			workspace, template := peerWorkspace(t, pair[0])
			data, _ := pair[0].WorkspaceMetadata(workspace)
			states := []domain.WorkflowState{}
			for _, state := range data.States {
				if state.TeamID == template.Team.ID || state.TeamID == "" {
					states = append(states, state)
				}
			}
			if len(states) < 2 {
				t.Fatalf("fixture needs two states: %d", len(states))
			}
			stamp := time.Now().UnixNano()
			template.Team.ID = fmt.Sprintf("fresh-team-%d", stamp)
			template.State = states[0]
			const moves = 12
			ids := []string{}
			for k := 0; k < moves; k++ {
				ids = append(ids, createPeerIssue(t, pair[1], workspace, fmt.Sprintf("fresh-%d-%d", stamp, k), template).ID)
			}
			queries := []IssueRecordQuery{
				{Workspace: workspace, Archived: "false", GroupBy: "status", TeamIDs: []string{template.Team.ID}},
				{Workspace: workspace, Archived: "false", GroupBy: "status", TeamIDs: []string{template.Team.ID}, Nested: true},
				{Workspace: workspace, Archived: "false", GroupBy: "status", Filter: IssueFilter{Field: "teamId", Values: []string{template.Team.ID}}, Nested: true},
			}
			moved := func(repository *SQLiteStore, q IssueRecordQuery) int64 {
				groups, err := repository.QueryIssueGroups(ctx, q)
				if err != nil {
					t.Error(err)
					return -1
				}
				for _, group := range groups {
					if group.Value == states[1].ID {
						return group.Count
					}
				}
				return 0
			}
			var committed atomic.Int64
			done := make(chan struct{})
			var readers sync.WaitGroup
			for _, repository := range pair {
				readers.Add(1)
				go func(repository *SQLiteStore) {
					defer readers.Done()
					for {
						select {
						case <-done:
							return
						default:
						}
						for _, q := range queries {
							floor := committed.Load()
							if got := moved(repository, q); got < floor || got > moves {
								t.Errorf("read %d moved issues after %d moves committed (%+v)", got, floor, q)
								return
							}
						}
					}
				}(repository)
			}
			for _, id := range ids {
				if _, err := pair[1].UpdateIssueRecord(ctx, workspace, id, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
					issue.State = states[1]
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				committed.Add(1)
				time.Sleep(5 * time.Millisecond)
			}
			close(done)
			readers.Wait()
			for _, q := range queries {
				if got := moved(pair[0], q); got != moves {
					t.Fatalf("%d moved issues counted, want %d (%+v)", got, moves, q)
				}
			}
		})
	}
}

// TestIssueWriteContention times concurrent issue updates in one workspace
// from FLOW_WRITE_CONTENTION_SERVERS stores (one writer each, as replicas
// sharing the database: a store runs its own writes one at a time) on
// MySQL or Postgres (FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL).
// Every writer updates its own issues, so the rows they share are the
// workspace's: its event sequence, issue revision and counters.
func TestIssueWriteContention(t *testing.T) {
	servers, _ := strconv.Atoi(os.Getenv("FLOW_WRITE_CONTENTION_SERVERS"))
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if servers <= 0 || driver == "" {
		t.Skip("set FLOW_WRITE_CONTENTION_SERVERS and an external database to time concurrent issue writes")
	}
	updates := 50
	if n, _ := strconv.Atoi(os.Getenv("FLOW_WRITE_CONTENTION_UPDATES")); n > 0 {
		updates = n
	}
	ctx := context.Background()
	open := func() *SQLiteStore {
		repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 2, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		return repository
	}
	first := open()
	defer first.Close()
	workspace := "write-contention"
	_ = first.DeleteWorkspace(ctx, workspace)
	data, err := first.CreateWorkspace(ctx, "Write contention", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	defer first.DeleteWorkspace(ctx, workspace)
	if len(data.States) < 2 {
		t.Fatal("workspace needs two states")
	}
	now := time.Now().UTC()
	const perWriter = 4
	issues := []domain.Issue{}
	for i := 0; i < servers*perWriter; i++ {
		issues = append(issues, domain.Issue{ID: fmt.Sprintf("wc-%d", i), Identifier: fmt.Sprintf("WC-%d", i+1), Number: i + 1, Title: "contention", Team: data.Teams[0], State: data.States[i%2], Creator: data.Viewer, Priority: i % 4, Version: 1, SubIssueIDs: []string{}, CreatedAt: now, UpdatedAt: now})
	}
	if err := first.ImportIssues(ctx, workspace, issues); err != nil {
		t.Fatal(err)
	}
	stores := []*SQLiteStore{first}
	for len(stores) < servers {
		repository := open()
		defer repository.Close()
		stores = append(stores, repository)
	}
	lockStats := func() (waits, waitMillis, deadlocks int64) {
		switch driver {
		case "mysql":
			rows, err := first.db.QueryContext(ctx, `SELECT variable_name, variable_value FROM performance_schema.global_status WHERE variable_name IN ('Innodb_row_lock_waits','Innodb_row_lock_time')`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var name string
				var value int64
				if err := rows.Scan(&name, &value); err != nil {
					t.Fatal(err)
				}
				if name == "Innodb_row_lock_waits" {
					waits = value
				} else {
					waitMillis = value
				}
			}
			rows.Close()
			if err := first.db.QueryRowContext(ctx, `SELECT count FROM information_schema.INNODB_METRICS WHERE name='lock_deadlocks'`).Scan(&deadlocks); err != nil {
				t.Fatal(err)
			}
		case "postgres":
			if err := first.db.QueryRowContext(ctx, `SELECT deadlocks FROM pg_stat_database WHERE datname=current_database()`).Scan(&deadlocks); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	run := func(mode string) {
		// Lock waits sampled every 5ms (Postgres keeps no counter).
		waiting := "SELECT COUNT(*) FROM performance_schema.data_lock_waits"
		if driver == "postgres" {
			waiting = "SELECT COUNT(*) FROM pg_locks WHERE NOT granted"
		}
		var samples, waitingSamples atomic.Int64
		stop := make(chan struct{})
		sampled := make(chan struct{})
		go func() {
			defer close(sampled)
			for {
				select {
				case <-stop:
					return
				case <-time.After(5 * time.Millisecond):
				}
				var n int64
				if err := first.db.QueryRowContext(ctx, waiting).Scan(&n); err == nil {
					samples.Add(1)
					waitingSamples.Add(n)
				}
			}
		}()
		waits0, millis0, deadlocks0 := lockStats()
		latencies := make([][]time.Duration, servers)
		var failures atomic.Int64
		var failure atomic.Value
		var wg sync.WaitGroup
		started := time.Now()
		for w := 0; w < servers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				repository := stores[w]
				for k := 0; k < updates; k++ {
					id := fmt.Sprintf("wc-%d", w*perWriter+k%perWriter)
					begin := time.Now()
					_, err := repository.UpdateIssueRecord(ctx, workspace, id, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
						if mode == "status" {
							next := data.States[0]
							if issue.State.ID == next.ID {
								next = data.States[1]
							}
							issue.State = next
						} else {
							issue.Title = fmt.Sprintf("contention %d", k)
						}
						return nil
					})
					latencies[w] = append(latencies[w], time.Since(begin))
					if err != nil {
						failures.Add(1)
						failure.Store(err.Error())
					}
				}
			}(w)
		}
		wg.Wait()
		elapsed := time.Since(started)
		close(stop)
		<-sampled
		waits1, millis1, deadlocks1 := lockStats()
		all := []time.Duration{}
		for _, l := range latencies {
			all = append(all, l...)
		}
		sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
		pct := func(p float64) float64 {
			return float64(all[min(len(all)-1, int(p*float64(len(all))))].Microseconds()) / 1000
		}
		meanWaiting := 0.0
		if samples.Load() > 0 {
			meanWaiting = float64(waitingSamples.Load()) / float64(samples.Load())
		}
		line := []string{
			fmt.Sprintf("%s %s: %d servers x %d updates", driver, mode, servers, updates),
			fmt.Sprintf("throughput %.0f/s", float64(len(all))/elapsed.Seconds()),
			fmt.Sprintf("p50 %.1fms p99 %.1fms max %.1fms", pct(0.5), pct(0.99), float64(all[len(all)-1].Microseconds())/1000),
			fmt.Sprintf("waiting locks (mean of samples) %.2f", meanWaiting),
			fmt.Sprintf("row lock waits %d (%d ms)", waits1-waits0, millis1-millis0),
			fmt.Sprintf("deadlocks %d", deadlocks1-deadlocks0),
			fmt.Sprintf("errors %d", failures.Load()),
		}
		if failures.Load() > 0 {
			line = append(line, fmt.Sprint(failure.Load()))
		}
		t.Log(strings.Join(line, " | "))
	}
	// A warm-up round loads every store's connections and statements.
	saved := updates
	updates = 4
	run("title")
	updates = saved
	for _, mode := range strings.Split(envOr("FLOW_WRITE_CONTENTION_MODES", "title,status"), ",") {
		run(mode)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
