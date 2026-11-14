package main

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotLoopTickScheduleStamps(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace := f.data.Workspace.URLKey
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	prefix := idgen.New("hot-loop-tick-")
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	due, future := now.Add(-time.Hour), now.Add(time.Hour)
	config := map[string]any{"unit": "day", "interval": float64(1), "time": "10:00", "timezone": "UTC"}
	next := nextLoopRun(config, now.AddDate(0, 0, -1), now)
	target, document := prefix+"stamp", prefix+"document"
	seed := func() {
		t.Helper()
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops", "documents", "webhooks"), workspace, "scale_p21.loop_tick_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = []domain.Loop{
				{ID: target, Name: "Stamp target", TriggerType: "schedule", Enabled: false, NextRunAt: &due},
				{ID: prefix + "due", TriggerType: "schedule", Status: "published", Enabled: true, NextRunAt: &due, TriggerConfig: config, CreatedAt: now.AddDate(0, 0, -1)},
				{ID: prefix + "empty", TriggerType: "schedule", Status: "published", Enabled: true, TriggerConfig: config, CreatedAt: now.AddDate(0, 0, -1)},
				{ID: prefix + "future", TriggerType: "schedule", Status: "published", Enabled: true, NextRunAt: &future, TriggerConfig: config},
				{ID: prefix + "draft", TriggerType: "schedule", Status: "draft", Enabled: true, NextRunAt: &due},
				{ID: prefix + "unrelated", TriggerType: "issue", Instructions: strings.Repeat("unrelated loop body ", 50000)},
			}
			data.Documents = []domain.Document{{ID: document, Title: "Aggregate witness", ContentData: map[string]any{
				"nested": map[string]any{"id": target, "name": "Earlier nested aggregate", "native": int64(9007199254740993)},
			}}}
			data.Webhooks = []domain.Webhook{{ID: prefix + "hook", Enabled: true, URL: "https://example.test/loop-hook"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	canonical := func(source *store.SQLiteStore, field, id string) []byte {
		t.Helper()
		var body []byte
		err := source.StreamMetadataRecords(t.Context(), workspace, field, func(key store.RecordKey, input io.Reader) error {
			if key.ID != id {
				return nil
			}
			var err error
			body, err = io.ReadAll(input)
			return err
		})
		if err != nil || len(body) == 0 {
			t.Fatal("canonical record missing", field, id, err)
		}
		raw, err := json.Marshal([]json.RawMessage{body})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	type effects struct {
		Target, Unrelated, Document []byte
		Payload, Previous, Realtime json.RawMessage
	}
	for _, reload := range []bool{false, true} {
		var expected effects
		for _, bounded := range []bool{false, true} {
			seed()
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			source := repo
			if bounded {
				if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				source = probe
			}
			unrelated, beforeDocument := canonical(repo, "loops", prefix+"unrelated"), canonical(repo, "documents", document)
			var event domain.DomainEvent
			var realtime domain.RealtimeEvent
			source.SetWebhookSink(func(_ string, actual domain.DomainEvent) { event = actual })
			source.SetRealtimeSink(func(_ string, actual domain.RealtimeEvent) { realtime = actual })
			(&server{store: source}).setLoopNextRun(workspace, target, next)
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
			got := effects{canonical(repo, "loops", target), canonical(repo, "loops", prefix+"unrelated"), canonical(repo, "documents", document), event.Payload, event.PreviousValues, realtime.Payload}
			if !bytes.Equal(unrelated, got.Unrelated) || !bytes.Equal(beforeDocument, got.Document) {
				t.Fatal("schedule stamp changed unrelated canonical records")
			}
			var loop []domain.Loop
			if err := json.Unmarshal(got.Target, &loop); err != nil || len(loop) != 1 || loop[0].NextRunAt == nil || !loop[0].NextRunAt.Equal(next) {
				t.Fatal("actual schedule stamp failed", bounded, err, string(got.Target))
			}
			var payload struct {
				Entity map[string]any `json:"entity"`
			}
			if err := json.Unmarshal(realtime.Payload, &payload); err != nil || payload.Entity["name"] != "Earlier nested aggregate" {
				t.Fatal("reflective aggregate precedence changed", bounded, err, string(realtime.Payload))
			}
			if !bounded {
				expected = got
			} else if !reflect.DeepEqual(got, expected) || source.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("bounded schedule stamp hooks/canonical records differ", reload, string(expected.Realtime), string(got.Realtime))
			}

			// This checks scheduling up to the separate startLoopRun boundary.
			// Actual model-run dispatch remains independently unmigrated.
			(&server{store: source}).runDueLoops(workspace, now)
			for _, suffix := range []string{"due", "empty", "future", "draft"} {
				var records []domain.Loop
				if err := json.Unmarshal(canonical(repo, "loops", prefix+suffix), &records); err != nil || len(records) != 1 {
					t.Fatal("scheduled loop missing", suffix, err)
				}
				want := next
				if suffix == "future" {
					want = future
				} else if suffix == "draft" {
					want = due
				}
				if records[0].NextRunAt == nil || !records[0].NextRunAt.Equal(want) {
					t.Fatal("due-loop next occurrence changed", reload, bounded, suffix, records[0].NextRunAt, want)
				}
			}
			if bounded && source.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("bounded scheduling decoded complete metadata")
			}
		}
	}
}

func TestScaleP21HotLoopTickTriggerSelection(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, public := f.data.Workspace.URLKey, f.data.Teams[0].ID
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	prefix, private := idgen.New("hot-loop-event-"), idgen.New("hot-loop-private-")
	now := time.Now().UTC()
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.loop_trigger_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: private, Name: "Private", Private: true})
		settings := data.TeamSettings[private]
		settings.TeamID, settings.Access, settings.TriageEnabled = private, "private", true
		data.TeamSettings[private] = settings
		data.WorkspaceSettings.ExternalLoopTriggers = true
		data.WorkspaceSettings.TrustedSourcesMode = "allowlist"
		data.WorkspaceSettings.TrustedSourcesAllowlist = []string{"integration:slack"}
		data.WorkspaceSettings.AgentInstructions = strings.Repeat("unrelated guidance ", 50000)
		data.Loops = nil
		for _, entity := range []string{"issue", "project", "initiative", "release", "team", "cycle"} {
			action := "updated"
			if entity == "issue" {
				action = "created"
			} else if entity == "cycle" {
				action = "started"
			}
			data.Loops = append(data.Loops, domain.Loop{ID: prefix + entity, TriggerType: entity, Status: "published", Enabled: true, TeamAccess: "selected", TeamID: private,
				TriggerConfig: map[string]any{"event": action, "teamIds": []string{public, private}}, Instructions: strings.Repeat("unrelated instructions ", 50000)})
		}
		data.Loops = append(data.Loops,
			domain.Loop{ID: prefix + "priority", TriggerType: "issue", Enabled: true, TeamAccess: "selected", TeamID: private, TriggerConfig: map[string]any{"event": "priority", "value": int(3), "filters": []any{map[string]any{"field": "status", "value": "triage"}}}},
			domain.Loop{ID: prefix + "comment", TriggerType: "issue", Enabled: true, TeamAccess: "selected", TeamID: private, TriggerConfig: map[string]any{"event": "comment"}},
			domain.Loop{ID: prefix + "request", TriggerType: "issue", Enabled: true, TeamAccess: "selected", TeamID: private, TriggerConfig: map[string]any{"event": "customerRequest"}},
			domain.Loop{ID: prefix + "denied-private", TriggerType: "issue", Enabled: true, TeamID: public, TriggerConfig: map[string]any{"event": "created"}},
		)
		data.Issues = []domain.Issue{
			{ID: prefix + "issue", Identifier: "HLT-1", Team: domain.Team{ID: private, Name: "Private"}, State: domain.WorkflowState{Type: "backlog"}, Priority: 3, PriorityLabel: "High", CreatedAt: now, UpdatedAt: now},
			{ID: prefix + "from-loop", Identifier: "HLT-2", Team: domain.Team{ID: private}, State: domain.WorkflowState{Type: "backlog"}, CreatedAt: now, UpdatedAt: now},
		}
		data.Asks = []domain.Ask{{ID: prefix + "ask", IssueID: prefix + "issue", Source: "slack"}, {ID: prefix + "loop-ask", IssueID: prefix + "from-loop", Source: "loop"}}
		data.CustomerRequests = []domain.CustomerRequest{{ID: prefix + "request", IssueID: prefix + "issue"}}
		data.Projects = []domain.Project{{ID: prefix + "project", Name: "Target project", TeamIDs: []string{private}, Description: strings.Repeat("unrelated project ", 50000)}}
		data.Initiatives = []domain.Initiative{{ID: prefix + "initiative", Name: "Target initiative"}}
		data.Releases = []domain.Release{{ID: prefix + "release", Name: "Target release"}}
		data.Cycles = []domain.Cycle{{ID: prefix + "cycle", Name: "Target cycle", TeamID: private}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	type selected struct {
		ID      string
		Trigger loopTrigger
	}
	selectTriggers := func(source *store.SQLiteStore, event domain.DomainEvent) []selected {
		t.Helper()
		s := &server{store: source}
		var data domain.Bootstrap
		var read *store.LoopScheduleRead
		if source.BoundedMetadataEnabled() {
			var found bool
			var err error
			read, found, err = source.BeginLoopScheduleRead(t.Context(), workspace, "event")
			if err != nil || !found {
				t.Fatal("event read failed", found, err)
			}
			defer read.Close()
			if err := read.LoadTriggerEntity(t.Context(), event); err != nil {
				t.Fatal(err)
			}
			data = read.Data
		} else {
			data, _ = source.WorkspaceMetadata(workspace)
		}
		resolved, found := s.resolveLoopEvent(workspace, data, event)
		if !found {
			return nil
		}
		if read != nil {
			if err := read.LoadTriggerContext(t.Context(), resolved.Issue, resolved.Kind, resolved.Previous); err != nil {
				t.Fatal(err)
			}
			data = read.Data
		}
		sourceKey, fromLoop := loopEventSource(data, resolved.Issue, resolved.Kind)
		if fromLoop {
			return nil
		}
		var result []selected
		for _, loop := range data.Loops {
			if !loopLive(loop) || loop.TriggerType != resolved.EntityType {
				continue
			}
			matches, reason := matchLoopEvent(data, loop, resolved)
			if !matches || !loopScopeMatches(data, loop, loopEntityTeams(data, resolved.EntityType, resolved.EntityID, resolved.Issue)) || sourceKey != "" && !loopSourceTrusted(data.WorkspaceSettings, loop, sourceKey) {
				continue
			}
			result = append(result, selected{loop.ID, loopTrigger{Kind: "event", EventType: event.Type, EntityType: resolved.EntityType, EntityID: resolved.EntityID, SourceKey: sourceKey, Label: reason.label(loopEventEntityName(data, resolved)), Reason: reason}})
		}
		return result
	}
	cases := []domain.DomainEvent{
		{Type: "issue.created", AggregateID: prefix + "issue"},
		{Type: "issue.updated", AggregateID: prefix + "issue", PreviousValues: json.RawMessage(`{"priority":2}`)},
		{Type: "comment.created", AggregateID: prefix + "issue"},
		{Type: "customer_request.created", AggregateID: prefix + "request"},
		{Type: "project.updated", AggregateID: prefix + "project"},
		{Type: "initiative.updated", AggregateID: prefix + "initiative"},
		{Type: "release.updated", AggregateID: prefix + "release"},
		{Type: "team.updated", AggregateID: private},
		{Type: "cycle.started", AggregateID: prefix + "cycle"},
		{Type: "issue.created", AggregateID: prefix + "from-loop"},
	}
	for _, reload := range []bool{false, true} {
		if reload {
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		for _, event := range cases {
			want, got := selectTriggers(repo, event), selectTriggers(probe, event)
			if !reflect.DeepEqual(got, want) || probe.WorkspaceMetadataDecodes() != 0 {
				t.Fatalf("trigger pre-dispatch differs reload=%t event=%s\nliteral=%+v\nbounded=%+v", reload, event.Type, want, got)
			}
			if !reload && event.AggregateID != prefix+"from-loop" && len(got) == 0 {
				t.Fatal("literal trigger fixture did not select a loop", event.Type)
			}
			if event.AggregateID == prefix+"from-loop" && len(got) != 0 {
				t.Fatal("loop-produced issue recursively triggered a loop")
			}
			for _, item := range got {
				if item.ID == prefix+"denied-private" {
					t.Fatal("private content triggered a public-only loop")
				}
			}
		}
	}
}
