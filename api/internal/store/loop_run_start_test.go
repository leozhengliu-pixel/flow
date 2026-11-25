package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

var errScaleP21SW261Missing = errors.New("loop not found")

// scaleP21SW261Stamp is the server's run-start callback (ensureLoopVersion
// and the LastRunAt stamp) restated for the store package.
func scaleP21SW261Stamp(loopID string, now time.Time, captured *domain.LoopVersion, observe func(*domain.Bootstrap)) func(*domain.Bootstrap) error {
	return func(data *domain.Bootstrap) error {
		if observe != nil {
			observe(data)
		}
		var item *domain.Loop
		for index := range data.Loops {
			if data.Loops[index].ID == loopID {
				item = &data.Loops[index]
				break
			}
		}
		if item == nil {
			return errScaleP21SW261Missing
		}
		versions := []domain.LoopVersion{}
		for _, version := range data.LoopVersions {
			if version.LoopID == loopID {
				versions = append(versions, version)
			}
		}
		slices.SortFunc(versions, func(a, b domain.LoopVersion) int { return b.Version - a.Version })
		var version domain.LoopVersion
		switch {
		case len(versions) > 0:
			version = versions[0]
		case item.Status == "draft":
		default:
			at := item.CreatedAt
			if item.PublishedAt != nil {
				at = *item.PublishedAt
			}
			by := item.Creator
			for _, user := range data.Users {
				if user.ID == item.Creator.ID {
					by = user
				}
			}
			version = domain.LoopVersion{ID: fmt.Sprintf("%s_v%d", item.ID, 1), LoopID: item.ID, Version: 1, PublishedAt: at, PublishedBy: by,
				ChangeSummary: []string{"published"}, Definition: domain.LoopDefinition{Name: item.Name, Description: item.Description,
					TriggerType: item.TriggerType, TriggerConfig: item.TriggerConfig, Instructions: item.Instructions,
					InstructionsData: item.InstructionsData, ConnectorIDs: slices.Clone(item.ConnectorIDs), WebSearch: item.WebSearch}}
			data.LoopVersions = append(data.LoopVersions, version)
			item.Version, item.VersionID = version.Version, version.ID
		}
		*captured = version
		item.LastRunAt = &now
		return nil
	}
}

type scaleP21SW261Owner struct {
	mu     sync.Mutex
	closes []func()
	closed bool
}

func (o *scaleP21SW261Owner) context(ctx context.Context) context.Context {
	return WithMetadataOutputOwner(ctx, func(close func()) bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.closed {
			return false
		}
		o.closes = append(o.closes, close)
		return true
	})
}

func (o *scaleP21SW261Owner) Close() {
	o.mu.Lock()
	closes := o.closes
	o.closes, o.closed = nil, true
	o.mu.Unlock()
	for _, close := range closes {
		close()
	}
}

func TestScaleP21SW261LoopRunStampDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	prefix := idgen.New("sw261-")
	target, other := prefix+"target", prefix+"other"
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	now := time.Date(2026, 11, 17, 4, 5, 6, 7, time.UTC)
	base := func(id string) domain.Loop {
		return domain.Loop{ID: id, Name: "Loop " + id, Status: "published", Enabled: true, TriggerType: "schedule",
			TriggerConfig: map[string]any{"unit": "day", "interval": 1}, Instructions: "Reply.", Creator: f.data.Viewer,
			InstructionsData: map[string]any{"native": int64(9007199254740993)}, ConnectorIDs: []string{"c"}, CreatedAt: at, PublishedAt: &at}
	}
	body := strings.Repeat("retained immutable version body ", 6000)
	cases := []struct {
		name      string
		seed      func(*domain.Bootstrap)
		want      string
		wantErr   error
		maxWork   int
		mutate    func(*domain.Bootstrap)
		published bool
	}{
		{name: "create-v1", want: target + "_v1"},
		{name: "current-version-ties", want: target + "-seven-b", maxWork: 4 << 20, seed: func(data *domain.Bootstrap) {
			for index := range 40 {
				data.LoopVersions = append(data.LoopVersions, domain.LoopVersion{ID: fmt.Sprintf("%s-old-%02d", target, index), LoopID: target, Version: index + 1,
					Definition: domain.LoopDefinition{Instructions: body}})
			}
			data.LoopVersions = append(data.LoopVersions,
				domain.LoopVersion{ID: target + "-seven-a", LoopID: target, Version: 70, Definition: domain.LoopDefinition{Instructions: body}},
				domain.LoopVersion{ID: other + "_v1", LoopID: other, Version: 99},
				domain.LoopVersion{ID: target + "-seven-b", LoopID: target, Version: 70, Definition: domain.LoopDefinition{Instructions: body}})
		}},
		{name: "draft-without-version", want: "", seed: func(data *domain.Bootstrap) { data.Loops[1].Status = "draft" }},
		{name: "missing-loop", wantErr: errScaleP21SW261Missing, seed: func(data *domain.Bootstrap) { data.Loops = data.Loops[:1] }},
		{name: "duplicate-loop-occurrences", want: target + "_v1", seed: func(data *domain.Bootstrap) {
			second := base(target)
			second.Name = "second occurrence"
			data.Loops = append(data.Loops, second)
		}},
		{name: "v1-id-collision", want: target + "_v1", seed: func(data *domain.Bootstrap) {
			data.LoopVersions = []domain.LoopVersion{{ID: target + "_v1", LoopID: other, Version: 3}}
		}},
		{name: "large-loop-v1", want: target + "_v1", seed: func(data *domain.Bootstrap) {
			data.Loops[1].Instructions = strings.Repeat("legal large instructions <&> ", 9500)
		}},
		{name: "empty-collection-v1", want: target + "_v1", seed: func(data *domain.Bootstrap) {
			data.Loops = data.Loops[1:]
		}},
		{name: "callback-outside-closure", wantErr: ErrBoundedMetadataCoverage, mutate: func(data *domain.Bootstrap) { data.Loops[0].Name = "renamed" }},
	}
	reset := func(seed func(*domain.Bootstrap)) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "loops", "loopVersions"), workspace, "scale_p21.sw261_reset", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = []domain.Loop{base(other), base(target)}
			data.Loops[0].Instructions = strings.Repeat("unrelated loop body ", 20000)
			data.LoopVersions = nil
			if seed != nil {
				seed(data)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	capture := func(source *SQLiteStore, prior map[string]bool, hooks []domain.DomainEvent, live []domain.RealtimeEvent) string {
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
		raw, err := json.Marshal([]any{rows, json.RawMessage(root), written, published})
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
					owner := &scaleP21SW261Owner{}
					var version domain.LoopVersion
					peak := 0
					stamp := scaleP21SW261Stamp(target, now, &version, func(data *domain.Bootstrap) {
						if len(data.Users) != 0 || len(data.Documents) != 0 {
							t.Error("the run-start callback received unlisted metadata")
						}
						if source.metadataResidency != nil {
							peak = source.metadataResidency.work.used
						}
						if spec.mutate != nil {
							spec.mutate(data)
						}
					})
					err = source.MutateLoopRunStart(owner.context(t.Context()), workspace, target, map[string]any{"runId": "run", "trigger": "manual"}, stamp)
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					got := capture(source, prior, hooks, live)
					if source.metadataResidency != nil {
						if source.metadataResidency.work.used == 0 && err == nil {
							t.Error("the stamp's callback inputs were not owned by the output owner")
						}
						owner.Close()
						if used := source.metadataResidency.work.used; used != 0 {
							t.Fatalf("closed output owner left %d work bytes", used)
						}
						if spec.maxWork > 0 && peak > spec.maxWork {
							t.Fatalf("existing version bodies entered the stamp: %d work bytes", peak)
						}
						if source.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("bounded stamp decoded complete metadata")
						}
					}
					if source == f.SQLiteStore {
						want, wantVersion, wantErr = got, version, err
						if spec.mutate == nil && (!errors.Is(err, spec.wantErr) || err == nil && version.ID != spec.want) {
							t.Fatalf("literal stamp err=%v version=%q, want %v %q", err, version.ID, spec.wantErr, spec.want)
						}
						continue
					}
					if spec.mutate != nil {
						if !errors.Is(err, ErrBoundedMetadataCoverage) {
							t.Fatal("callback edit outside the closed stamp was accepted", err)
						}
						reset(spec.seed)
						continue
					}
					if !errors.Is(err, wantErr) || version.ID != wantVersion.ID || version.Version != wantVersion.Version {
						t.Fatalf("bounded stamp err=%v version=%+v, literal err=%v version=%+v", err, version, wantErr, wantVersion)
					}
					if got != want {
						t.Fatalf("bounded stamp effects differ\nliteral: %s\nbounded: %s", scaleP21SW261Clip(want), scaleP21SW261Clip(got))
					}
				}
			})
		}
	}
}

func scaleP21SW261Clip(text string) string {
	if len(text) > 4000 {
		return text[:2000] + " ... " + text[len(text)-2000:]
	}
	return text
}

// The stamp selects the versions current in its own transaction, not those
// of the installed generation the start read pinned earlier.
func TestScaleP21SW261LoopRunStampReadsCurrentVersions(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	loop := domain.Loop{ID: "sw261-race", Status: "published", Enabled: true, Instructions: "Reply."}
	write := func(versions ...domain.LoopVersion) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "loops", "loopVersions"), workspace, "scale_p21.sw261_publish", "", nil, func(data *domain.Bootstrap) error {
			data.Loops, data.LoopVersions = []domain.Loop{loop}, versions
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
	}
	for _, initial := range [][]domain.LoopVersion{nil, {{ID: "sw261-race_v1", LoopID: loop.ID, Version: 1}}} {
		write(initial...)
		read, found, err := view.BeginLoopRunStartRead(t.Context(), workspace, loop.ID)
		if err != nil || !found || len(read.Data.Loops) != 1 {
			t.Fatal("start read failed", found, err)
		}
		write(domain.LoopVersion{ID: "sw261-race_v1", LoopID: loop.ID, Version: 1}, domain.LoopVersion{ID: "sw261-race_v2", LoopID: loop.ID, Version: 2})
		ctx, err := read.MutationContext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var version domain.LoopVersion
		if err := view.MutateLoopRunStart(ctx, workspace, loop.ID, nil, scaleP21SW261Stamp(loop.ID, time.Now().UTC(), &version, nil)); err != nil {
			t.Fatal(err)
		}
		read.Close()
		if version.ID != "sw261-race_v2" || view.metadataResidency.work.used != 0 {
			t.Fatal("the stamp used the start read's versions or kept its work", version.ID, view.metadataResidency.work.used)
		}
		data, _ := f.WorkspaceMetadataFields(workspace, "loopVersions")
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		data, _ = f.WorkspaceMetadataFields(workspace, "loopVersions")
		if len(data.LoopVersions) != 2 {
			t.Fatal("the stamp appended a v1 next to a concurrently published version", data.LoopVersions)
		}
	}
}

func TestScaleP21SW262LoopRunStartReadLifetimes(t *testing.T) {
	wide := strings.Repeat("captured instructions ", 40000)
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	data := domain.Bootstrap{
		Workspace:         domain.Workspace{ID: "workspace-id", URLKey: "physical", Name: strings.Repeat("unrelated ", 1000)},
		WorkspaceSettings: domain.WorkspaceSettings{FeatureFlags: map[string]bool{"loops": true, "other": false}, AgentInstructions: wide},
		Loops: []domain.Loop{
			{ID: "other", Instructions: wide},
			{ID: "target", Name: "First", Status: "published", Enabled: true, Instructions: wide, TriggerConfig: map[string]any{"interval": int(3), "native": int64(9007199254740993)}, CreatedAt: at, NextRunAt: &at},
			{ID: "target", Name: "Second", Instructions: "not captured"},
		},
		Projects: []domain.Project{{ID: "project", Name: "Project <&>", Description: wide}, {ID: "project", Name: "Duplicate"}},
		Teams:    []domain.Team{{ID: "team", Name: "Team", Key: "TM"}},
		Cycles:   []domain.Cycle{{ID: "cycle", Number: 7, Name: "", TeamID: "team"}},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	view.lastWorkspaceKey = "physical"
	for _, key := range []string{"physical", ""} {
		read, found, err := view.BeginLoopRunStartRead(t.Context(), key, "target")
		if err != nil || !found {
			t.Fatal("start read failed", key, found, err)
		}
		if read.Data.Workspace.URLKey != "physical" || read.Data.Workspace.Name != "" || !reflect.DeepEqual(read.Data.WorkspaceSettings, domain.WorkspaceSettings{FeatureFlags: data.WorkspaceSettings.FeatureFlags}) {
			t.Fatal("workspace inputs changed", read.Data.Workspace, read.Data.WorkspaceSettings)
		}
		if len(read.Data.Loops) != 1 || !reflect.DeepEqual(read.Data.Loops[0], data.Loops[1]) {
			t.Fatal("first native loop occurrence not captured whole", read.Data.Loops)
		}
		for _, entity := range []struct{ kind, id string }{{"project", "project"}, {"team", "team"}, {"cycle", "cycle"}, {"release", "missing"}, {"issue", "ignored"}, {"", ""}} {
			if err := read.LoadEntity(t.Context(), entity.kind, entity.id); err != nil {
				t.Fatal(entity, err)
			}
		}
		if len(read.Data.Projects) != 1 || read.Data.Projects[0].Name != "Project <&>" || read.Data.Projects[0].Description != "" ||
			len(read.Data.Teams) != 1 || read.Data.Teams[0].Key != "" || read.Data.Teams[0].Name != "Team" ||
			len(read.Data.Cycles) != 1 || read.Data.Cycles[0].Number != 7 || read.Data.Cycles[0].TeamID != "" || len(read.Data.Releases) != 0 {
			t.Fatal("entity label inputs changed", read.Data.Projects, read.Data.Teams, read.Data.Cycles)
		}
		if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used == 0 {
			t.Fatal("start read decoded complete metadata or is unowned")
		}
		var leases []func()
		var wait sync.WaitGroup
		var mu sync.Mutex
		for range 8 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				release, err := read.ExecutionLease()
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				leases = append(leases, release)
				mu.Unlock()
			}()
		}
		wait.Wait()
		loop := read.Data.Loops[0]
		read.Close()
		read.Close()
		if _, err := read.ExecutionLease(); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("late execution lease after close", err)
		}
		if _, err := read.MutationContext(t.Context()); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("mutation context after close", err)
		}
		if err := read.LoadEntity(t.Context(), "project", "project"); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("entity read after close", err)
		}
		if view.metadataResidency.work.used == 0 || loop.Instructions != wide {
			t.Fatal("captured loop lost its allowance while an execution lease is held")
		}
		for index, release := range leases {
			wait.Add(1)
			go func() {
				defer wait.Done()
				release()
				release()
			}()
			if index == len(leases)-1 {
				wait.Wait()
			}
		}
		if used := view.metadataResidency.work.used; used != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
			t.Fatal("final lease did not release the start read", used)
		}
	}
	read, found, err := view.BeginLoopRunStartRead(t.Context(), "physical", "absent")
	if err != nil || !found || len(read.Data.Loops) != 0 {
		t.Fatal("missing loop changed", found, err)
	}
	read.Close()
	for _, key := range []string{"logical", "missing"} {
		if _, found, err := view.BeginLoopRunStartRead(t.Context(), key, "target"); err != nil || found {
			t.Fatal("physical/missing workspace selection changed", key, found, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginLoopRunStartRead(ctx, "physical", "target"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not propagated", err)
	}
	oldLimit := view.metadataResidency.work.limit
	for _, limit := range []int{1, 200 << 10} {
		view.metadataResidency.work.limit = limit
		if _, _, err := view.BeginLoopRunStartRead(t.Context(), "physical", "target"); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("start read exceeded its quota", limit, err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("failed start read leaked quota", limit)
		}
	}
	view.metadataResidency.work.limit = oldLimit
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("start reads decoded complete metadata")
	}
}
