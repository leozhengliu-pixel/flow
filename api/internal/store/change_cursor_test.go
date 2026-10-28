package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// pausedCommitKey marks the context of a write whose commit a test pauses.
type pausedCommitKey struct{}

// pauseCommits pauses, at stage, the commits of transactions begun with a
// context made by the returned function: reached is signalled once a commit
// gets there, and it then waits until release is closed.
func pauseCommits(t *testing.T, repos []*SQLiteStore, stage string) (mark func(context.Context) context.Context, reached <-chan struct{}, release chan struct{}) {
	t.Helper()
	arrived := make(chan struct{}, 16)
	release = make(chan struct{})
	pause := func(tx *sqlTx) {
		// Only writes: a mutation's read transactions commit too.
		if tx.context().Value(pausedCommitKey{}) == nil || tx.writtenBytes == 0 {
			return
		}
		arrived <- struct{}{}
		<-release
	}
	hooks := &commitHooks{}
	switch stage {
	case "before":
		hooks.beforeCommit = pause
	case "after":
		hooks.afterCommit = pause
	case "sequenced":
		hooks.afterSequence = pause
	default:
		t.Fatalf("unknown commit stage %q", stage)
	}
	for _, repo := range repos {
		repo.db.commitHooks.Store(hooks)
	}
	t.Cleanup(func() {
		for _, repo := range repos {
			repo.db.commitHooks.Store(nil)
		}
	})
	return func(ctx context.Context) context.Context { return context.WithValue(ctx, pausedCommitKey{}, true) }, arrived, release
}

// changeCursorStores returns stores sharing one database: the SQLite pair,
// and for an external database (FLOW_TEST_DATABASE_DRIVER/URL) servers
// stores, one writer each, as replicas.
func changeCursorStores(t *testing.T, servers int) map[string][]*SQLiteStore {
	t.Helper()
	sets := map[string][]*SQLiteStore{}
	for name, pair := range peerStoreSets(t) {
		repos := []*SQLiteStore{pair[0], pair[1]}
		if name != "sqlite" {
			for len(repos) < servers {
				repo, err := OpenDatabase(DatabaseConfig{Driver: os.Getenv("FLOW_TEST_DATABASE_DRIVER"), URL: os.Getenv("FLOW_TEST_DATABASE_URL"), FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { repo.Close() })
				repos = append(repos, repo)
			}
		}
		sets[name] = repos
	}
	return sets
}

// While several servers update issues of one workspace at once, readers that
// poll the change log from a cursor, resuming after the last seq they read,
// see every committed event exactly once, in gap-free seq order. Seqs are
// assigned after commit on MySQL and PostgreSQL, so this is what keeps a
// reader from resuming past an event that committed after a later one.
func TestChangeCursorReadersSeeEveryEventOnce(t *testing.T) {
	for name, stores := range changeCursorStores(t, 8) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			// SQLite takes one writer at a time: a second store's write
			// fails with SQLITE_BUSY, so its writers share one store.
			repos := stores
			if name == "sqlite" {
				repos = []*SQLiteStore{stores[0], stores[0], stores[0]}
			}
			workspace, template := peerWorkspace(t, repos[0])
			data, _ := repos[0].WorkspaceMetadata(workspace)
			workspaceID := data.Workspace.ID
			stamp := time.Now().UnixNano()
			ids := make([]string, len(repos))
			for w := range repos {
				ids[w] = createPeerIssue(t, repos[w], workspace, fmt.Sprintf("cursor-%d-%d", stamp, w), template).ID
			}
			start, err := repos[0].WorkspaceChangeHead(ctx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			const updates = 25
			var writing sync.WaitGroup
			var stop atomic.Bool
			errs := make([]error, len(repos))
			for w := range repos {
				writing.Add(1)
				go func(w int) {
					defer writing.Done()
					for k := 0; k < updates && errs[w] == nil; k++ {
						_, errs[w] = repos[w].UpdateIssueRecord(ctx, workspace, ids[w], nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
							issue.Title = fmt.Sprintf("cursor %d", k)
							return nil
						})
					}
				}(w)
			}
			type reading struct {
				seen  map[string]int64
				order []int64
				err   error
			}
			read := func(repo *SQLiteStore, state *reading, cursor *int64) bool {
				page, err := repo.WorkspaceChanges(ctx, workspaceID, *cursor, 7)
				if err != nil || page.ResyncRequired {
					state.err = fmt.Errorf("read after %d: %v (resync %v)", *cursor, err, page.ResyncRequired)
					return false
				}
				for _, record := range page.Records {
					if record.Seq != *cursor+1 {
						state.err = fmt.Errorf("read seq %d after %d", record.Seq, *cursor)
						return false
					}
					if previous, ok := state.seen[record.Event.ID]; ok {
						state.err = fmt.Errorf("event %s read at seq %d and %d", record.Event.ID, previous, record.Seq)
						return false
					}
					state.seen[record.Event.ID] = record.Seq
					*cursor = record.Seq
				}
				return page.HasMore
			}
			readers := make([]*reading, 3)
			var polling sync.WaitGroup
			for r := range readers {
				readers[r] = &reading{seen: map[string]int64{}}
				polling.Add(1)
				go func(state *reading, repo *SQLiteStore) {
					defer polling.Done()
					cursor := start
					for state.err == nil {
						more := read(repo, state, &cursor)
						if !more && stop.Load() {
							// Drain to the head once the writers are done.
							for state.err == nil && read(repo, state, &cursor) {
							}
							return
						}
						if !more {
							time.Sleep(time.Millisecond)
						}
					}
				}(readers[r], stores[r%len(stores)])
			}
			writing.Wait()
			stop.Store(true)
			polling.Wait()
			for w, err := range errs {
				if err != nil {
					t.Fatalf("writer %d: %v", w, err)
				}
			}
			// Every event written since start has a seq, and every reader
			// read each of them once.
			var unnumbered int
			if err := repos[0].db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE workspace_id=? AND seq IS NULL`, workspaceID).Scan(&unnumbered); err != nil {
				t.Fatal(err)
			}
			if unnumbered != 0 {
				t.Fatalf("%d events left without a cursor", unnumbered)
			}
			want := map[string]int64{}
			rows, err := repos[0].db.QueryContext(ctx, `SELECT id,seq FROM domain_events WHERE workspace_id=? AND seq>?`, workspaceID, start)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id string
				var seq int64
				if err := rows.Scan(&id, &seq); err != nil {
					t.Fatal(err)
				}
				want[id] = seq
			}
			rows.Close()
			if len(want) < len(repos)*updates {
				t.Fatalf("%d events logged, want at least %d", len(want), len(repos)*updates)
			}
			for r, state := range readers {
				if state.err != nil {
					t.Fatalf("reader %d: %v", r, state.err)
				}
				if len(state.seen) != len(want) {
					t.Fatalf("reader %d read %d events, %d were logged", r, len(state.seen), len(want))
				}
				for id, seq := range want {
					if state.seen[id] != seq {
						t.Fatalf("reader %d: event %s read at seq %d, logged at %d", r, id, state.seen[id], seq)
					}
				}
			}
		})
	}
}

// The classic hazard of cursors assigned before commit: writer A takes its
// place, writer B takes the next, B commits first, a reader reads B and
// resumes past A. Here A is held just before its commit while B commits and
// is read; once A commits, the reader still finds it after its cursor. With A
// held after its commit (not yet numbered), B's numbering numbers A's event
// too, ahead of B's own.
func TestChangeCursorNeverSkipsAnEventCommittedLate(t *testing.T) {
	for _, stage := range []string{"before", "after"} {
		for name, repos := range changeCursorStores(t, 2) {
			if name == "sqlite" {
				continue // one writer at a time: B would wait for A
			}
			t.Run(name+"/"+stage, func(t *testing.T) {
				ctx := t.Context()
				workspace, template := peerWorkspace(t, repos[0])
				data, _ := repos[0].WorkspaceMetadata(workspace)
				workspaceID := data.Workspace.ID
				stamp := time.Now().UnixNano()
				a := createPeerIssue(t, repos[0], workspace, fmt.Sprintf("late-a-%d", stamp), template).ID
				b := createPeerIssue(t, repos[1], workspace, fmt.Sprintf("late-b-%d", stamp), template).ID
				cursor, err := repos[0].WorkspaceChangeHead(ctx, workspace)
				if err != nil {
					t.Fatal(err)
				}
				mark, reached, release := pauseCommits(t, repos, stage)
				var aDone sync.WaitGroup
				aDone.Add(1)
				var aErr error
				go func() {
					defer aDone.Done()
					_, aErr = repos[0].UpdateIssueRecord(mark(ctx), workspace, a, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
						issue.Title = "late A"
						return nil
					})
				}()
				select {
				case <-reached:
				case <-time.After(10 * time.Second):
					t.Fatal("writer A never reached its commit")
				}
				if _, err := repos[1].UpdateIssueRecord(ctx, workspace, b, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
					issue.Title = "early B"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				seen := map[string]int64{}
				readAll := func() {
					for {
						page, err := repos[1].WorkspaceChanges(ctx, workspaceID, cursor, 100)
						if err != nil || page.ResyncRequired {
							t.Fatalf("read: %v (resync %v)", err, page.ResyncRequired)
						}
						for _, record := range page.Records {
							if record.Seq != cursor+1 {
								t.Fatalf("read seq %d after %d", record.Seq, cursor)
							}
							seen[record.Event.AggregateID] = record.Seq
							cursor = record.Seq
						}
						if !page.HasMore {
							return
						}
					}
				}
				readAll()
				if _, ok := seen[b]; !ok {
					t.Fatalf("B's committed update is not in the log: %v", seen)
				}
				close(release)
				aDone.Wait()
				if aErr != nil {
					t.Fatal(aErr)
				}
				readAll()
				if _, ok := seen[a]; !ok {
					t.Fatalf("A's update, committed after B was read, was skipped: %v", seen)
				}
				if stage == "after" && seen[a] > seen[b] {
					t.Fatalf("A committed first but was numbered after B (%d > %d)", seen[a], seen[b])
				}
			})
		}
	}
}

// A write's events are numbered before its realtime fan-out, which stamps
// each pushed event with its seq (stampChangeCursor in cmd/server).
func TestChangeCursorIsAssignedBeforeFanOut(t *testing.T) {
	for name, repos := range changeCursorStores(t, 2) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			workspace, template := peerWorkspace(t, repos[0])
			id := createPeerIssue(t, repos[0], workspace, fmt.Sprintf("fanout-%d", time.Now().UnixNano()), template).ID
			var seqs []int64
			repos[0].SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
				if event.AggregateID == id {
					seqs = append(seqs, repos[1].DomainEventSeq(ctx, event.ID))
				}
			})
			t.Cleanup(func() { repos[0].SetRealtimeSink(nil) })
			if _, err := repos[0].UpdateIssueRecord(ctx, workspace, id, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
				issue.Title = "fanned out"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(seqs) != 1 || seqs[0] == 0 {
				t.Fatalf("seqs at fan-out: %v", seqs)
			}
		})
	}
}

// An event whose writer committed but never numbered it (it died in
// between) is numbered by the next reader once it is stale, and by the next
// numbering of its workspace before that.
func TestChangeCursorRescuesEventsLeftUnnumbered(t *testing.T) {
	for name, repos := range changeCursorStores(t, 2) {
		if name == "sqlite" {
			continue // numbered inside the writing transaction
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			workspace, _ := peerWorkspace(t, repos[0])
			data, _ := repos[0].WorkspaceMetadata(workspace)
			workspaceID := data.Workspace.ID
			cursor, err := repos[0].WorkspaceChangeHead(ctx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			orphan := idgen.New("evt_")
			created := time.Now().UTC()
			if _, err := repos[0].db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at,workspace_id) VALUES(?,?,?,?,?,?)`, orphan, "issue.updated", "orphan", []byte(`{}`), created.Format(time.RFC3339Nano), workspaceID); err != nil {
				t.Fatal(err)
			}
			if _, err := repos[0].db.ExecContext(ctx, `INSERT INTO domain_event_sequencing(workspace_id,event_id,created_ms) VALUES(?,?,?)`, workspaceID, orphan, created.Add(-time.Minute).UnixMilli()); err != nil {
				t.Fatal(err)
			}
			page, err := repos[1].WorkspaceChanges(ctx, workspaceID, cursor, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 1 || page.Records[0].Event.ID != orphan || page.Records[0].Seq != cursor+1 {
				t.Fatalf("orphaned event not rescued: %+v", page.Records)
			}
		})
	}
}
