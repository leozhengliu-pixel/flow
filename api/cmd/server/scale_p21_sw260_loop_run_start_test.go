package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

var (
	scaleP21SW260Clock   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
	scaleP21SW260Millis  = regexp.MustCompile(`"(createdAt|updatedAt|nextAttemptAt|heartbeatAt|leaseUntil|started|finishedAt)":(\d{13})`)
	scaleP21SW260LeaseTT = time.Hour
)

// scaleP21SW260Normalize replaces generated IDs and clocks in the encoded
// effects, keeping every other byte (stored JSON keeps its own key order).
func scaleP21SW260Normalize(raw []byte, ids map[string]string, started, finished time.Time) string {
	text := string(raw)
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j]) || len(keys[i]) == len(keys[j]) && keys[i] < keys[j]
	})
	for _, id := range keys {
		text = strings.ReplaceAll(text, id, ids[id])
	}
	text = scaleP21SW260Clock.ReplaceAllStringFunc(text, func(value string) string {
		clock, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return value
		}
		switch {
		case !clock.Before(started) && !clock.After(finished):
			return "<clock>"
		case !clock.Add(-scaleP21SW260LeaseTT).Before(started) && !clock.Add(-scaleP21SW260LeaseTT).After(finished):
			return "<clock+lease>"
		}
		return value
	})
	return scaleP21SW260Millis.ReplaceAllStringFunc(text, func(value string) string {
		match := scaleP21SW260Millis.FindStringSubmatch(value)
		millis, _ := strconv.ParseInt(match[2], 10, 64)
		if millis >= started.UnixMilli() && millis <= finished.UnixMilli() {
			return `"` + match[1] + `":"<ms>"`
		}
		return value
	})
}

type scaleP21SW260Case struct {
	name string
	// mode is manual (startLoopRun), schedule (runDueLoops), event
	// (dispatchLoopTriggers) or concurrent (two manual starts at once).
	mode   string
	event  domain.DomainEvent
	entity [2]string
	loopID string
	seed   func(data *domain.Bootstrap)
	// local runs in process; async marks a local run whose execution
	// continues after the start, so only start effects are compared.
	local, async                bool
	agentOff, guard             bool
	workerBudget, localBudget   bool
	rejectCreate, rejectEnqueue bool
	wantRuns                    int
	wantErr                     error
	wantVersion                 string
}

func TestScaleP21SW260LoopRunStartDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	workspace, public := f.data.Workspace.URLKey, f.data.Teams[0].ID
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	reader := &scaleP04Database{config: config}
	t.Cleanup(func() {
		if reader.reader != nil {
			_ = reader.reader.Close()
		}
	})
	prefix := idgen.New("sw260-")
	target, second, third, held := prefix+"target", prefix+"second", prefix+"third", prefix+"held"
	unrelated, other := prefix+"unrelated", prefix+"other"
	private, project, privateProject, issue := prefix+"private-team", prefix+"project", prefix+"private-project", prefix+"issue"
	loopIDs := []string{target, second, third, unrelated, other, held}
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	due := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	tick := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	daily := map[string]any{"unit": "day", "interval": 1, "time": "09:00", "timezone": "UTC"}

	// Teams, projects and the issue are not written by a run start.
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw260_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: private, Key: "SWP", Name: "Private loops", Private: true})
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.Projects = append([]domain.Project{
			{ID: project, Name: "Large project <&>", TeamIDs: []string{public}, Description: strings.Repeat("legal large project description ", 32000)},
			{ID: privateProject, Name: "Private project", TeamIDs: []string{private}},
		}, data.Projects...)
		data.Issues = append(data.Issues, domain.Issue{ID: issue, Identifier: "SWL-1", Title: "Loop trigger", Team: domain.Team{ID: public}, State: domain.WorkflowState{Type: "backlog"}, CreatedAt: at, UpdatedAt: at})
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: prefix + "hook", Enabled: true, URL: "https://example.test/sw260"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	loop := func(id, trigger string, config map[string]any) domain.Loop {
		return domain.Loop{ID: id, Name: "Loop " + id, Status: "published", Enabled: true, Level: "workspace",
			OwnerID: f.data.Viewer.ID, Creator: f.data.Viewer, Instructions: "Summarize the work and reply.",
			InstructionsData: map[string]any{"type": "doc", "native": int64(9007199254740993)},
			TriggerType:      trigger, TriggerConfig: config, ConnectorIDs: []string{prefix + "connector"},
			ExpectedOutputs: []string{"document"}, WebSearch: true, CreatedAt: at, UpdatedAt: at, PublishedAt: &at,
			OutputContract: &domain.LoopOutputContract{Source: "person"}}
	}
	versions := func(loopID string) []domain.LoopVersion {
		definition := domain.LoopDefinition{Name: "Old", Instructions: strings.Repeat("retained version body ", 4000)}
		return []domain.LoopVersion{
			{ID: loopID + "_v2", LoopID: loopID, Version: 2, PublishedAt: at, Definition: definition},
			{ID: other + "_v1", LoopID: other, Version: 9, PublishedAt: at, Definition: definition},
			{ID: loopID + "-v7-first", LoopID: loopID, Version: 7, PublishedAt: at, Definition: definition},
			{ID: loopID + "-v7-second", LoopID: loopID, Version: 7, PublishedAt: at, Definition: definition},
		}
	}
	scheduled := func(id string) domain.Loop {
		item := loop(id, "schedule", daily)
		item.NextRunAt = &due
		return item
	}
	projectLoop := func(id string, config map[string]any) domain.Loop { return loop(id, "project", config) }
	cases := []scaleP21SW260Case{
		{name: "manual-create-v1", mode: "manual", wantRuns: 1, wantVersion: target + "_v1"},
		{name: "manual-current-version-ties", mode: "manual", wantRuns: 1, seed: func(data *domain.Bootstrap) {
			data.LoopVersions = versions(target)
		}},
		{name: "manual-long-instructions-v1", mode: "manual", wantRuns: 1, wantVersion: target + "_v1", seed: func(data *domain.Bootstrap) {
			data.Loops[1].Instructions = strings.Repeat("legal long loop instructions <&> ", 8500)
		}},
		{name: "manual-on-project-entity", mode: "manual", entity: [2]string{"project", project}, wantRuns: 1},
		{name: "manual-on-team-entity", mode: "manual", entity: [2]string{"team", private}, wantRuns: 1},
		{name: "manual-missing-entity", mode: "manual", entity: [2]string{"cycle", prefix + "missing-cycle"}, wantRuns: 1},
		{name: "paused", mode: "manual", wantErr: errLoopUnavailable, seed: func(data *domain.Bootstrap) { data.Loops[1].Enabled = false }},
		{name: "draft", mode: "manual", wantErr: errLoopUnavailable, seed: func(data *domain.Bootstrap) { data.Loops[1].Status = "draft" }},
		{name: "loops-disabled", mode: "manual", wantErr: errLoopUnavailable, seed: func(data *domain.Bootstrap) {
			data.WorkspaceSettings.FeatureFlags["loops"] = false
		}},
		{name: "agent-disabled", mode: "manual", agentOff: true, wantErr: errLoopUnavailable},
		{name: "missing-loop", mode: "manual", loopID: prefix + "missing", wantErr: errNotFound},
		{name: "guard-held", mode: "manual", guard: true, wantErr: errConflict},
		{name: "worker-budget-exhausted", mode: "manual", workerBudget: true, wantRuns: 2},
		{name: "local-budget-exhausted", mode: "manual", local: true, localBudget: true, wantRuns: 1},
		{name: "local-handoff", mode: "manual", local: true, async: true, wantRuns: 1},
		{name: "local-create-failure", mode: "manual", local: true, rejectCreate: true},
		{name: "worker-enqueue-failure", mode: "manual", rejectEnqueue: true, wantRuns: 1},
		{name: "concurrent-starts", mode: "concurrent", wantRuns: 1},
		{name: "duplicate-loop-occurrences", mode: "manual", wantRuns: 1, seed: func(data *domain.Bootstrap) {
			copy := data.Loops[1]
			copy.Name, copy.Instructions = "Second occurrence", "Must not be stamped"
			data.Loops = append(data.Loops, copy)
		}},
		{name: "v1-id-collision", mode: "manual", wantRuns: 1, seed: func(data *domain.Bootstrap) {
			data.LoopVersions = []domain.LoopVersion{{ID: target + "_v1", LoopID: other, Version: 4, PublishedAt: at}}
		}},
		{name: "nested-aggregate-collision", mode: "manual", wantRuns: 1, seed: func(data *domain.Bootstrap) {
			data.Documents = []domain.Document{{ID: prefix + "document", Title: "Witness", ContentData: map[string]any{
				"nested": map[string]any{"id": target, "name": "Earlier nested aggregate"},
			}}}
		}},
		{name: "schedule-one", mode: "schedule", wantRuns: 1, seed: func(data *domain.Bootstrap) {
			data.Loops[1] = scheduled(target)
		}},
		{name: "schedule-several", mode: "schedule", wantRuns: 2, seed: func(data *domain.Bootstrap) {
			future := tick.Add(time.Hour)
			data.Loops[1] = scheduled(target)
			later := scheduled(third)
			later.NextRunAt = &future
			data.Loops = append(data.Loops, scheduled(second), later)
			data.LoopVersions = versions(second)
		}},
		{name: "event-project-one", mode: "event", event: domain.DomainEvent{Type: "project.updated", AggregateID: project}, wantRuns: 1, seed: func(data *domain.Bootstrap) {
			data.Loops[1] = projectLoop(target, map[string]any{"event": "updated"})
		}},
		{name: "event-project-several", mode: "event", event: domain.DomainEvent{Type: "project.updated", AggregateID: project}, wantRuns: 2, seed: func(data *domain.Bootstrap) {
			data.Loops[1] = projectLoop(target, map[string]any{"event": "updated"})
			data.Loops = append(data.Loops, projectLoop(second, map[string]any{"event": "updated"}), projectLoop(third, map[string]any{"event": "created"}))
			data.LoopVersions = versions(second)
		}},
		{name: "event-issue-created", mode: "event", event: domain.DomainEvent{Type: "issue.created", AggregateID: issue}, wantRuns: 2, seed: func(data *domain.Bootstrap) {
			data.Loops[1] = loop(target, "issue", map[string]any{"event": "created"})
			data.Loops = append(data.Loops, loop(second, "issue", map[string]any{"event": "created"}), loop(third, "issue", map[string]any{"event": "comment"}))
		}},
		{name: "event-private-team-entity", mode: "event", event: domain.DomainEvent{Type: "project.updated", AggregateID: privateProject}, wantRuns: 1, seed: func(data *domain.Bootstrap) {
			scoped := projectLoop(target, map[string]any{"event": "updated", "teamIds": []string{private}})
			scoped.Level, scoped.TeamID, scoped.TeamAccess = "team", private, "selected"
			outsider := projectLoop(second, map[string]any{"event": "updated", "teamIds": []string{public}})
			outsider.Level, outsider.TeamID = "team", public
			data.Loops[1] = scoped
			data.Loops = append(data.Loops, outsider)
		}},
		{name: "event-paused-loop", mode: "event", event: domain.DomainEvent{Type: "project.updated", AggregateID: project}, seed: func(data *domain.Bootstrap) {
			data.Loops[1] = projectLoop(target, map[string]any{"event": "updated"})
			data.Loops[1].Enabled = false
		}},
	}
	provider := &fakeLoopProvider{}
	upstream := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(upstream.Close)
	exec := func(t *testing.T, statement string) {
		t.Helper()
		rows := reader.query(t, `SELECT workspace_key FROM workspace_states WHERE workspace_key=?`, workspace)
		_ = rows.Close()
		if _, err := reader.reader.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	clearGuards := func() {
		loopGuards.mu.Lock()
		defer loopGuards.mu.Unlock()
		for key := range loopGuards.inFlight {
			if strings.HasPrefix(key, prefix) {
				delete(loopGuards.inFlight, key)
			}
		}
		for key := range loopGuards.recent {
			if strings.HasPrefix(key, prefix) {
				delete(loopGuards.recent, key)
			}
		}
	}
	for _, spec := range cases {
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reload=%t", spec.name, reload), func(t *testing.T) {
				if (spec.rejectCreate || spec.rejectEnqueue) && config.Driver != "sqlite" {
					t.Skip("row rejection uses a SQLite trigger; the other cases cover the configured database")
				}
				var want string
				for _, source := range []*store.SQLiteStore{repo, probe} {
					bounded := source.BoundedMetadataEnabled()
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					for _, id := range loopIDs {
						if err := repo.DeleteLoopRuns(t.Context(), workspace, id); err != nil {
							t.Fatal(err)
						}
					}
					clearGuards()
					resetCtx := store.WithMetadataFields(t.Context(), "loops", "loopVersions", "documents", "workspaceSettings")
					if err := repo.MutateWorkspace(resetCtx, workspace, "scale_p21.sw260_reset", "", nil, func(data *domain.Bootstrap) error {
						big := loop(unrelated, "issue", map[string]any{"event": "comment"})
						big.Instructions = strings.Repeat("unrelated loop instructions ", 16000)
						data.Loops = []domain.Loop{big, loop(target, "schedule", daily)}
						data.LoopVersions = nil
						data.Documents = nil
						if data.WorkspaceSettings.FeatureFlags == nil {
							data.WorkspaceSettings.FeatureFlags = map[string]bool{}
						}
						data.WorkspaceSettings.FeatureFlags["loops"] = true
						if spec.seed != nil {
							spec.seed(data)
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if reload {
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					service := &server{store: source, agent: appconfig.AgentConfig{Enabled: !spec.agentOff, Runtime: appconfig.AgentRuntimeWorker,
						WorkerSecret: testWorkerSecret, Protocol: "openai-responses", BaseURL: upstream.URL, Model: "flow-test", MaxOutputTokens: 256}, agentClient: upstream.Client()}
					if spec.local {
						service.agent.Runtime, service.agent.WorkerSecret = "", ""
					}
					limits := testLoopLimits()
					limits.MaxConcurrent, limits.MaxConcurrentPerWorkspace, limits.LeaseTTL = 0, 4, scaleP21SW260LeaseTT
					if spec.localBudget {
						limits.MaxConcurrentPerWorkspace = 1
						registry := service.loopRunRegistry()
						registry.total, registry.perWorkspace[workspace] = 1, 1
					}
					if spec.workerBudget {
						limits.MaxConcurrentPerWorkspace = 1
						if err := repo.CreateLoopRun(t.Context(), workspace, domain.LoopRun{ID: prefix + "held-run", LoopID: held, Status: "running", StartedAt: at},
							store.LoopRunLease{Owner: prefix + "elsewhere", ExpiresAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
							t.Fatal(err)
						}
					}
					service.loopRunLimits.Store(limits)
					loopID := firstNonEmpty(spec.loopID, target)
					guardKey := loopID + "|" + spec.entity[1]
					if spec.guard && !loopGuards.claim(guardKey, false, time.Now()) {
						t.Fatal("fixture did not claim the run guard")
					}
					rejection := strings.ReplaceAll(prefix, "-", "_") + "reject"
					dropRejection := func() { exec(t, `DROP TRIGGER IF EXISTS "`+rejection+`"`) }
					if spec.rejectCreate || spec.rejectEnqueue {
						t.Cleanup(dropRejection)
					}
					if spec.rejectCreate {
						exec(t, `CREATE TRIGGER "`+rejection+`" BEFORE INSERT ON loop_run_records WHEN NEW.workspace_key=`+quote(workspace)+` AND NEW.loop_id=`+quote(target)+` BEGIN SELECT RAISE(ABORT,'SW260 injected run record failure'); END`)
					}
					if spec.rejectEnqueue {
						exec(t, `CREATE TRIGGER "`+rejection+`" BEFORE INSERT ON agent_runs WHEN NEW.workspace_key=`+quote(workspace)+` BEGIN SELECT RAISE(ABORT,'SW260 injected queue failure'); END`)
					}
					prior := map[string]bool{}
					for _, id := range loopIDs {
						events, err := repo.Events(t.Context(), id)
						if err != nil {
							t.Fatal(err)
						}
						for _, event := range events {
							prior[event.ID] = true
						}
					}
					var mu sync.Mutex
					var webhooks []domain.DomainEvent
					var realtime []domain.RealtimeEvent
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						mu.Lock()
						webhooks = append(webhooks, event)
						mu.Unlock()
					})
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						mu.Lock()
						realtime = append(realtime, event)
						mu.Unlock()
					})
					decodes := source.WorkspaceMetadataDecodes()
					started := time.Now().UTC()
					type outcome struct {
						Run   domain.LoopRun
						Error string
					}
					var results []outcome
					start := func() outcome {
						trigger := loopTrigger{Kind: "manual", EntityType: spec.entity[0], EntityID: spec.entity[1]}
						run, err := service.startLoopRun(workspace, loopID, trigger, f.data.Viewer.ID)
						switch {
						case spec.wantErr != nil && !errors.Is(err, spec.wantErr):
							t.Errorf("start error %v, want %v", err, spec.wantErr)
						case spec.wantErr == nil && spec.mode != "concurrent" && !spec.rejectCreate && err != nil:
							t.Errorf("start failed: %v", err)
						}
						result := outcome{Run: run}
						if err != nil {
							result.Error = err.Error()
						}
						return result
					}
					switch spec.mode {
					case "manual":
						results = append(results, start())
					case "concurrent":
						var wait sync.WaitGroup
						gate := make(chan struct{})
						results = make([]outcome, 2)
						for index := range results {
							wait.Add(1)
							go func() {
								defer wait.Done()
								<-gate
								results[index] = start()
							}()
						}
						close(gate)
						wait.Wait()
						sort.Slice(results, func(i, j int) bool { return results[i].Error < results[j].Error })
						if results[0].Error != "" || !strings.Contains(results[1].Error, errConflict.Error()) {
							t.Fatalf("concurrent starts did not refuse exactly one: %+v", results)
						}
					case "schedule":
						service.runDueLoops(workspace, tick)
					case "event":
						event := spec.event
						event.ID, event.Payload, event.CreatedAt = prefix+"event", json.RawMessage(`{}`), started
						service.dispatchLoopTriggers(workspace, event)
					}
					if spec.rejectCreate || spec.rejectEnqueue {
						dropRejection()
					}
					if spec.async {
						if bounded && source.MetadataWorkUsedForTesting() == 0 {
							t.Error("the local run released its captured loop allowance before it finished")
						}
						deadline := time.Now().Add(20 * time.Second)
						for {
							loopGuards.mu.Lock()
							running := loopGuards.inFlight[guardKey]
							loopGuards.mu.Unlock()
							if !running && (!bounded || source.MetadataWorkUsedForTesting() == 0) {
								break
							}
							if time.Now().After(deadline) {
								t.Fatal("local run did not finish and release its allowance")
							}
							time.Sleep(20 * time.Millisecond)
						}
					}
					if spec.guard {
						loopGuards.release(guardKey)
					}
					if spec.rejectCreate {
						registry := service.loopRunRegistry()
						registry.mu.Lock()
						total, reserved := registry.total, registry.perWorkspace[workspace]
						registry.mu.Unlock()
						loopGuards.mu.Lock()
						inFlight := loopGuards.inFlight[guardKey]
						loopGuards.mu.Unlock()
						if results[0].Error == "" || total != 0 || reserved != 0 || inFlight {
							t.Fatal("failed run record did not unwind", results[0].Error, total, reserved, inFlight)
						}
					}
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					finished := time.Now().UTC()

					ids := map[string]string{service.loopInstanceID(): "<instance>", prefix: "<prefix>-"}
					type runEffects struct {
						Runs   []domain.LoopRun
						Leases [][]any
						Queue  []store.AgentRun
					}
					runs := map[string]runEffects{}
					for _, id := range loopIDs {
						items, err := repo.ListLoopRuns(t.Context(), workspace, id, 100)
						if err != nil {
							t.Fatal(err)
						}
						effects := runEffects{Runs: items}
						for index, item := range items {
							ids[item.ID] = fmt.Sprintf("<run:%s:%d>", strings.TrimPrefix(id, prefix), index)
							queued, err := repo.SessionAgentRuns(t.Context(), workspace, item.ID, 100)
							if err != nil {
								t.Fatal(err)
							}
							for _, agentRun := range queued {
								ids[agentRun.ID] = fmt.Sprintf("<agent:%s:%d>", strings.TrimPrefix(id, prefix), len(effects.Queue))
								effects.Queue = append(effects.Queue, agentRun)
								effects.Leases = append(effects.Leases, []any{"principal", agentRun.Principal})
							}
						}
						rows := reader.query(t, `SELECT id,loop_id,status,lease_owner,lease_expires_at,heartbeat_at,cancel_requested FROM loop_run_records WHERE workspace_key=? ORDER BY loop_id,started_at,id`, workspace)
						for rows.Next() {
							var runID, runLoop, status, owner, expiry, heartbeat string
							var cancelled int
							if err := rows.Scan(&runID, &runLoop, &status, &owner, &expiry, &heartbeat, &cancelled); err != nil {
								t.Fatal(err)
							}
							if runLoop == id {
								effects.Leases = append(effects.Leases, []any{runID, status, owner, expiry, heartbeat, cancelled})
							}
						}
						if err := rows.Err(); err != nil {
							t.Fatal(err)
						}
						_ = rows.Close()
						if spec.async {
							// The run's own execution updates its record after the handoff.
							effects = runEffects{Runs: nil, Leases: [][]any{{len(items)}}}
						}
						runs[strings.TrimPrefix(id, prefix)] = effects
					}
					var events []domain.DomainEvent
					for _, id := range loopIDs {
						all, err := repo.Events(t.Context(), id)
						if err != nil {
							t.Fatal(err)
						}
						for _, event := range all {
							if prior[event.ID] {
								continue
							}
							ids[event.ID] = fmt.Sprintf("<event:%d>", len(events))
							var payload struct {
								RunID string `json:"runId"`
							}
							if json.Unmarshal(event.Payload, &payload) == nil && payload.RunID != "" && ids[payload.RunID] == "" {
								ids[payload.RunID] = fmt.Sprintf("<unpersisted-run:%d>", len(events))
							}
							events = append(events, event)
						}
					}
					mu.Lock()
					hooks, live := slices.Clone(webhooks), slices.Clone(realtime)
					mu.Unlock()
					if spec.async {
						hooks = slices.DeleteFunc(hooks, func(event domain.DomainEvent) bool { return !strings.HasPrefix(event.Type, "loop.") })
						live = slices.DeleteFunc(live, func(event domain.RealtimeEvent) bool { return !strings.HasPrefix(event.Type, "loop.") })
					}
					for index, event := range live {
						if ids[event.ID] == "" {
							ids[event.ID] = fmt.Sprintf("<realtime:%d>", index)
						}
					}
					var metadata []any
					rows := reader.query(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('loops','loopVersions','documents') ORDER BY field,collection_order,record_key`, workspace)
					for rows.Next() {
						var field, key string
						var order int
						var raw []byte
						if err := rows.Scan(&field, &key, &order, &raw); err != nil {
							t.Fatal(err)
						}
						metadata = append(metadata, []any{field, key, order, json.RawMessage(raw)})
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					_ = rows.Close()
					rows = reader.query(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)
					for rows.Next() {
						var raw []byte
						if err := rows.Scan(&raw); err != nil {
							t.Fatal(err)
						}
						metadata = append(metadata, []any{"root", json.RawMessage(raw)})
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					_ = rows.Close()
					encoded, err := json.Marshal(map[string]any{
						"results": results, "runs": runs, "events": events, "webhooks": hooks, "realtime": live, "metadata": metadata,
					})
					if err != nil {
						t.Fatal(err)
					}
					got := scaleP21SW260Normalize(encoded, ids, started, finished)
					if !bounded {
						want = got
						total := 0
						for _, effects := range runs {
							total += len(effects.Runs)
							if spec.async {
								total += effects.Leases[0][0].(int)
							}
						}
						if total != spec.wantRuns {
							t.Fatalf("literal fixture started %d runs, want %d", total, spec.wantRuns)
						}
						if spec.wantVersion != "" && !strings.Contains(got, `"versionId":"`+strings.ReplaceAll(spec.wantVersion, prefix, "<prefix>-")+`"`) {
							t.Fatal("literal fixture did not record the expected version", spec.wantVersion)
						}
						continue
					}
					if got != want {
						t.Fatalf("bounded loop run start differs from the literal start\n%s", scaleP21SW260FirstDiff(want, got))
					}
					if used := source.MetadataWorkUsedForTesting(); used != 0 {
						t.Fatalf("bounded start retained %d bytes of metadata work", used)
					}
					if source.WorkspaceMetadataDecodes() != decodes {
						t.Fatal("bounded start decoded complete workspace metadata")
					}
				}
			})
		}
	}
}

// scaleP21SW260FirstDiff shows both texts around their first difference.
func scaleP21SW260FirstDiff(want, got string) string {
	index := 0
	for index < len(want) && index < len(got) && want[index] == got[index] {
		index++
	}
	start := max(index-600, 0)
	end := func(text string) string { return text[start:min(index+600, len(text))] }
	return fmt.Sprintf("offset %d\nliteral: %s\nbounded: %s", index, end(want), end(got))
}
