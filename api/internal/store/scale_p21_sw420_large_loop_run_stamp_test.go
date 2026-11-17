package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// SW420: loop run starts (MutateLoopRunStart with the server's start read
// held, as startBoundedLoopRun holds it) for loops whose instructions reach
// the literal writer's transaction maxima, compared byte for byte with the
// literal start: first version of an empty version collection, a v1
// appended after other loops' versions, a stamp of a loop that already has
// a version, the size the literal writer refuses, and peers that commit to
// the loop row between the read and the stamp (merged, or conflicting and
// retried).

// scaleP21SW420Peak samples the admitted metadata work of store until stop.
type scaleP21SW420Peak struct {
	mu   sync.Mutex
	peak int
	done chan struct{}
	wait sync.WaitGroup
}

func scaleP21SW420Watch(source *SQLiteStore) *scaleP21SW420Peak {
	p := &scaleP21SW420Peak{done: make(chan struct{})}
	if source.metadataResidency == nil {
		close(p.done)
		return p
	}
	p.wait.Add(1)
	go func() {
		defer p.wait.Done()
		ticker := time.NewTicker(200 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-p.done:
				return
			case <-ticker.C:
				p.sample(source)
			}
		}
	}()
	return p
}

func (p *scaleP21SW420Peak) sample(source *SQLiteStore) {
	if source.metadataResidency == nil {
		return
	}
	used := source.MetadataWorkUsedForTesting()
	p.mu.Lock()
	p.peak = max(p.peak, used)
	p.mu.Unlock()
}

func (p *scaleP21SW420Peak) stop() int {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	p.wait.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

// scaleP21SW420PeerLoop commits edit to the canonical loop row directly,
// as a writer in another process would, after both copies were installed.
func scaleP21SW420PeerLoop(t *testing.T, f scaleP09Fixture, workspace, id string, edit func(*domain.Loop)) {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var raw []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='loops' AND record_key=?`, workspace, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var loop domain.Loop
	if err := json.Unmarshal(raw, &loop); err != nil {
		t.Fatal(err)
	}
	edit(&loop)
	scaleP21SW296Put(t, tx, workspace, "loops", id, loop, scaleP21SW296Order(t, tx, workspace, "loops", id))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW420LargeLoopRunStampDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if metadataWorkBytes != 256<<20 || f.db.maxTransactionBytes != scaleP21SW296Tx {
		t.Fatal("SW420 sizes assume the production work and transaction budgets", f.db.maxTransactionBytes)
	}
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	prefix := idgen.New("sw420-")
	target, other := prefix+"target", prefix+"other"
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	now := time.Date(2026, 11, 17, 4, 5, 6, 7, time.UTC)
	peerAt := now.Add(-time.Hour)
	// The literal writer's maxima for this body (SW296): a stamp that also
	// writes the v1 snapshot fits at 11 MiB and is refused at 13 MiB; a
	// stamp of a loop that already has a version fits at 15 MiB.
	stampMax, stampOver, touchMax := 11<<20, 13<<20, 15<<20
	base := func(id string) domain.Loop {
		return domain.Loop{ID: id, Name: "Loop " + id, Status: "published", Enabled: true, TriggerType: "schedule", Level: "workspace",
			TriggerConfig: map[string]any{"unit": "day", "interval": 1}, Instructions: "Reply.", Creator: f.data.Viewer,
			InstructionsData: map[string]any{"type": "doc", "native": int64(9007199254740993)}, ConnectorIDs: []string{"c"},
			Description: "Small description", CreatedAt: at, UpdatedAt: at, PublishedAt: &at}
	}
	instructions := func(size int) func(*domain.Bootstrap) {
		return func(data *domain.Bootstrap) {
			data.Loops[1].Instructions = scaleP21SW296Body("legal large loop instructions", size)
		}
	}
	otherVersion := func(seed func(*domain.Bootstrap)) func(*domain.Bootstrap) {
		return func(data *domain.Bootstrap) {
			seed(data)
			data.LoopVersions = []domain.LoopVersion{{ID: other + "_v1", LoopID: other, Version: 1, PublishedAt: at,
				Definition: domain.LoopDefinition{Name: "Other", Instructions: "Other body."}}}
		}
	}
	ownVersion := func(seed func(*domain.Bootstrap)) func(*domain.Bootstrap) {
		return func(data *domain.Bootstrap) {
			seed(data)
			data.LoopVersions = []domain.LoopVersion{{ID: target + "_v3", LoopID: target, Version: 3, PublishedAt: at,
				Definition: domain.LoopDefinition{Name: "Old", Instructions: "Old body."}}}
			data.Loops[1].Version, data.Loops[1].VersionID = 3, target+"_v3"
		}
	}
	cases := []struct {
		name    string
		seed    func(*domain.Bootstrap)
		peer    func(*domain.Loop)
		want    string
		refused bool
	}{
		{name: "first-version-7mib", seed: instructions(7 << 20), want: target + "_v1"},
		{name: "first-version-at-max", seed: instructions(stampMax), want: target + "_v1"},
		{name: "appended-v1-at-max", seed: otherVersion(instructions(stampMax)), want: target + "_v1"},
		{name: "existing-version-at-max", seed: ownVersion(instructions(touchMax)), want: target + "_v3"},
		{name: "first-version-over", seed: instructions(stampOver), refused: true},
		{name: "appended-v1-over", seed: otherVersion(instructions(stampOver)), refused: true},
		{name: "peer-renamed", seed: instructions(4 << 20), want: target + "_v1",
			peer: func(loop *domain.Loop) { loop.Name = "Peer renamed loop" }},
		{name: "peer-stamped-conflict", seed: instructions(4 << 20), want: target + "_v1",
			peer: func(loop *domain.Loop) { loop.LastRunAt = &peerAt }},
		{name: "peer-instructions-conflict", seed: otherVersion(instructions(4 << 20)), want: target + "_v1",
			peer: func(loop *domain.Loop) {
				loop.Instructions = scaleP21SW296Body("peer replaced instructions", 4<<20)
			}},
		{name: "peer-existing-version-stamped", seed: ownVersion(instructions(6 << 20)), want: target + "_v3",
			peer: func(loop *domain.Loop) { loop.LastRunAt = &peerAt }},
	}
	reset := func(seed func(*domain.Bootstrap)) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "loops", "loopVersions"), workspace, "scale_p21.sw420_reset", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = []domain.Loop{base(other), base(target)}
			data.LoopVersions = nil
			if seed != nil {
				seed(data)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	capture := func(prior map[string]bool, hooks []domain.DomainEvent, live []domain.RealtimeEvent) string {
		t.Helper()
		var rows []any
		result, err := f.db.QueryContext(t.Context(), `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('loops','loopVersions') ORDER BY field,collection_order,record_key`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		for result.Next() {
			var field, key string
			var order int
			var raw []byte
			if err := result.Scan(&field, &key, &order, &raw); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, []any{field, key, order, json.RawMessage(raw)})
		}
		if err := result.Err(); err != nil {
			t.Fatal(err)
		}
		_ = result.Close()
		var search []any
		result, err = f.db.QueryContext(t.Context(), `SELECT field,record_key,content FROM metadata_search_documents WHERE workspace_key=? AND field IN ('loops','loopVersions') ORDER BY field,record_key`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		for result.Next() {
			var field, key, content string
			if err := result.Scan(&field, &key, &content); err != nil {
				t.Fatal(err)
			}
			search = append(search, []any{field, key, content})
		}
		if err := result.Err(); err != nil {
			t.Fatal(err)
		}
		_ = result.Close()
		var root []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&root); err != nil {
			t.Fatal(err)
		}
		events, err := f.Events(t.Context(), target)
		if err != nil {
			t.Fatal(err)
		}
		var written []any
		for _, event := range events {
			if !prior[event.ID] {
				written = append(written, []any{event.Type, event.AggregateID, event.Payload, event.PreviousValues})
			}
		}
		var published []any
		for _, event := range hooks {
			published = append(published, []any{event.Type, event.AggregateID, event.Payload, event.PreviousValues})
		}
		for _, event := range live {
			published = append(published, []any{event.Type, event.AggregateID, event.ActorID, event.Payload})
		}
		raw, err := json.Marshal([]any{rows, search, json.RawMessage(root), written, published})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	for _, spec := range cases {
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reload=%t", spec.name, reload), func(t *testing.T) {
				var want string
				var wantVersion domain.LoopVersion
				var wantErr error
				for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					reset(spec.seed)
					if reload {
						if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
						t.Fatal(err)
					}
					bounded := source.BoundedMetadataEnabled()
					// The server's start read stays open over the stamp.
					var read *LoopRunStartRead
					ctx := t.Context()
					owner := &scaleP21SW261Owner{}
					peak := scaleP21SW420Watch(source)
					if bounded {
						var found bool
						read, found, err = source.BeginLoopRunStartRead(t.Context(), workspace, target)
						if err != nil || !found || len(read.Data.Loops) != 1 {
							peak.stop()
							t.Fatal("start read failed", found, err)
						}
						if ctx, err = read.MutationContext(t.Context()); err != nil {
							t.Fatal(err)
						}
					} else {
						ctx = owner.context(ctx)
					}
					if spec.peer != nil {
						scaleP21SW420PeerLoop(t, f, workspace, target, spec.peer)
					}
					prior := map[string]bool{}
					events, err := f.Events(t.Context(), target)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						prior[event.ID] = true
					}
					var hooks []domain.DomainEvent
					var live []domain.RealtimeEvent
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) { hooks = append(hooks, event) })
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { live = append(live, event) })
					var version domain.LoopVersion
					attempts := 0
					stamp := scaleP21SW261Stamp(target, now, &version, func(*domain.Bootstrap) {
						attempts++
						peak.sample(source)
					})
					ctx = WithSQLStatementObserver(ctx, func(string) { peak.sample(source) })
					err = source.MutateLoopRunStart(ctx, workspace, target, map[string]any{"runId": "run", "trigger": "manual"}, stamp)
					worst := peak.stop()
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					if read != nil {
						read.Close()
					}
					owner.Close()
					got := capture(prior, hooks, live)
					if bounded {
						if used := source.MetadataWorkUsedForTesting(); used != 0 {
							t.Fatalf("bounded stamp left %d work bytes", used)
						}
						if source.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("bounded stamp decoded complete metadata")
						}
					}
					t.Logf("%s reload=%t bounded=%t err=%v attempts=%d peak_work=%d", spec.name, reload, bounded, err, attempts, worst)
					if !bounded {
						want, wantVersion, wantErr = got, version, err
						if spec.refused != (err != nil && errors.Is(err, ErrTransactionWriteBudget)) || !spec.refused && (err != nil || version.ID != spec.want) {
							t.Fatalf("literal stamp err=%v version=%q, want refused=%t %q", err, version.ID, spec.refused, spec.want)
						}
						if spec.peer != nil && attempts < 1 {
							t.Fatal("literal stamp did not run")
						}
						continue
					}
					if (err == nil) != (wantErr == nil) || err != nil && (err.Error() != wantErr.Error() || !errors.Is(err, ErrTransactionWriteBudget)) {
						t.Fatalf("bounded stamp err=%v, literal err=%v", err, wantErr)
					}
					if version.ID != wantVersion.ID || version.Version != wantVersion.Version {
						t.Fatalf("bounded version %+v, literal %+v", version.ID, wantVersion.ID)
					}
					if got != want {
						t.Fatalf("bounded stamp effects differ\nliteral: %s\nbounded: %s", scaleP21SW420Diff(want, got), scaleP21SW420Diff(got, want))
					}
				}
			})
		}
	}
}

// scaleP21SW420Diff clips text around its first difference from other.
func scaleP21SW420Diff(text, other string) string {
	index := 0
	for index < len(text) && index < len(other) && text[index] == other[index] {
		index++
	}
	start := max(index-400, 0)
	return fmt.Sprintf("@%d/%d: %s", index, len(text), strings.ToValidUTF8(text[start:min(index+400, len(text))], "?"))
}
