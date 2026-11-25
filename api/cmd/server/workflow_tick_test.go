package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW342WorkflowScheduleTickDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	_, database, issue := scaleP21SW44ChatFixture(t)
	repo, workspace := database.repo, database.key
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("owned workflow tick baseline missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	sqlFixture := scaleP21SW10Fixture{reader: database}
	sqlFixture.data.Workspace.URLKey = workspace
	prefix := idgen.New("sw342-")
	scheduledID, retryID := prefix+"scheduled", prefix+"retry"
	futureID, disabledID := prefix+"future", prefix+"disabled"
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(24*time.Hour)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	workflowIDs := []string{scheduledID, retryID, futureID, disabledID}
	for _, failFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("scheduled-fails=%t", failFirst), func(t *testing.T) {
			var expected any
			for _, mode := range []string{"legacy-warm", "bounded-warm", "legacy-reloaded", "bounded-reloaded"} {
				t.Run(mode, func(t *testing.T) {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					restore := store.ForceFullMutationsForTesting(true)
					err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw342_fixture", "", nil, func(data *domain.Bootstrap) error {
						var original domain.Bootstrap
						if err := json.Unmarshal(saved, &original); err != nil {
							return err
						}
						*data = original
						first := []domain.WorkflowAction{{Type: "notify", Config: map[string]string{}}}
						if failFirst {
							first = append(first, domain.WorkflowAction{Type: "assignIssue", Config: map[string]string{"userId": prefix + "missing"}})
						}
						definition := func(id, trigger string, enabled bool, due *time.Time, actions []domain.WorkflowAction) domain.WorkflowDefinition {
							return domain.WorkflowDefinition{
								ID: id, Name: id, Trigger: trigger, Schedule: "@every 15m",
								Enabled: enabled, NextRunAt: due, MaxAttempts: 8,
								CreatorID: baseline.Viewer.ID, CreatedAt: stamp, UpdatedAt: stamp, Actions: actions,
							}
						}
						data.WorkflowDefinitions = []domain.WorkflowDefinition{
							definition(scheduledID, "schedule", true, &past, first),
							definition(futureID, "schedule", true, &future, first),
							definition(disabledID, "schedule", false, &past, first),
							definition(retryID, "manual", true, nil, []domain.WorkflowAction{
								{Type: "setIssuePriority", Config: map[string]string{"priority": "1"}},
								{Type: "notify", Config: map[string]string{}},
							}),
						}
						prior := func(id, workflow, status string, attempt int, due *time.Time) domain.WorkflowRun {
							return domain.WorkflowRun{
								ID: prefix + id, WorkflowID: workflow, ResourceType: "issue", ResourceID: issue.ID,
								Status: status, Attempt: attempt, NextRetryAt: due, ScheduledAt: stamp, StartedAt: stamp,
							}
						}
						data.WorkflowRuns = []domain.WorkflowRun{
							prior("success", retryID, "succeeded", 1, nil),
							prior("same-workflow-retry", scheduledID, "failed", 3, &past),
							prior("due-retry", retryID, "failed", 2, &past),
							prior("future-retry", futureID, "failed", 5, &future),
							prior("disabled-retry", disabledID, "failed", 1, &past),
						}
						for index := range data.Issues {
							if data.Issues[index].ID == issue.ID {
								data.Issues[index].Priority, data.Issues[index].PriorityLabel = 4, priorityLabel(4)
							}
						}
						return nil
					})
					restore()
					if err != nil {
						t.Fatal(err)
					}
					reloaded := mode == "legacy-reloaded" || mode == "bounded-reloaded"
					if reloaded {
						scaleP21SW201ReloadSource(t, database, repo)
					}
					source := repo
					bounded := mode == "bounded-warm" || mode == "bounded-reloaded"
					if bounded {
						source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
						if reloaded {
							scaleP21SW201ReloadSource(t, database, source)
						}
					}
					service := &server{store: source}
					before := scaleP21SW59SQL(t, sqlFixture)
					priorIDs := map[string]bool{}
					for _, workflowID := range workflowIDs {
						events, err := repo.Events(t.Context(), workflowID)
						if err != nil {
							t.Fatal(err)
						}
						for _, event := range events {
							priorIDs[event.ID] = true
						}
					}
					got := scaleP21SW59Outcome{}
					var order []string
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, event))
						if event.Type == "workflow_run.executed" {
							order = append(order, event.AggregateID)
						}
					})
					captureRealtime := func(_ string, event domain.RealtimeEvent) {
						got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
					}
					source.SetRealtimeSink(captureRealtime)
					repo.SetRealtimeSink(captureRealtime)
					statements := 0
					ctx := store.WithSQLStatementObserver(t.Context(), func(string) { statements++ })
					started := time.Now().UTC()
					if bounded {
						service.boundedWorkflowScheduleTick(ctx, now)
					} else {
						// One owned workspace's literal scheduler body. Both
						// loops borrow the same pre-tick native snapshot.
						data, ok := source.WorkspaceMetadataFields(workspace, "workflowDefinitions", "workflowRuns")
						if !ok {
							t.Fatal("literal workflow tick snapshot missing")
						}
						for _, definition := range data.WorkflowDefinitions {
							if definition.Enabled && definition.Trigger == "schedule" && definition.NextRunAt != nil && !definition.NextRunAt.After(now) {
								_, _ = service.runWorkflow(ctx, workspace, definition.ID, "schedule", "", "", true)
							}
						}
						for _, run := range data.WorkflowRuns {
							if run.Status == "failed" && run.NextRetryAt != nil && !run.NextRetryAt.After(now) {
								_, _ = service.runWorkflow(ctx, workspace, run.WorkflowID, "retry", run.ResourceType, run.ResourceID, true)
							}
						}
					}
					finished := time.Now().UTC()
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					repo.SetRealtimeSink(nil)
					if bounded && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("workflow tick retained output work or hydrated full metadata", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
					}
					if !reflect.DeepEqual(order, []string{scheduledID, retryID}) {
						t.Fatal("tick lost definition-before-retry order or latest-generation due check", order)
					}
					rows := database.query(t, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='workflowRuns' ORDER BY collection_order,record_key`, workspace)
					var runs []domain.WorkflowRun
					for rows.Next() {
						var raw []byte
						var run domain.WorkflowRun
						if err := rows.Scan(&raw); err != nil || json.Unmarshal(raw, &run) != nil {
							rows.Close()
							t.Fatal("canonical workflow run", err)
						}
						runs = append(runs, run)
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					firstStatus := "succeeded"
					if failFirst {
						firstStatus = "failed"
					}
					if len(runs) != 7 || runs[5].WorkflowID != scheduledID || runs[5].Trigger != "schedule" ||
						runs[5].Attempt != 4 || runs[5].Status != firstStatus ||
						runs[6].WorkflowID != retryID || runs[6].Trigger != "retry" || runs[6].Attempt != 3 || runs[6].Status != "succeeded" {
						t.Fatal("tick changed required action results, controls or prior-run ordering", runs)
					}
					afterIssue, err := repo.IssueRecord(t.Context(), workspace, issue.ID)
					if err != nil || afterIssue.Priority != 1 {
						t.Fatal("later issue retry lost its successful action", afterIssue.Priority, err)
					}
					got.Rows = scaleP21SW59SQL(t, sqlFixture)
					notices := map[string]int{}
					for _, record := range got.Rows.Content {
						if record.Kind == "notification" {
							var notice domain.Notification
							if err := json.Unmarshal(record.Data, &notice); err != nil {
								t.Fatal(err)
							}
							if notice.SourceType == "workflow" {
								notices[notice.SourceID]++
							}
						}
					}
					if notices[scheduledID] != 1 || notices[retryID] != 1 || notices[futureID] != 0 || notices[disabledID] != 0 {
						t.Fatal("successful or partially failed tick lost its exact prior action", notices)
					}
					for _, workflowID := range workflowIDs {
						events, err := repo.Events(t.Context(), workflowID)
						if err != nil {
							t.Fatal(err)
						}
						for _, event := range events {
							if !priorIDs[event.ID] {
								got.Events = append(got.Events, event)
							}
						}
					}
					if len(got.Events) != 2 || len(got.Webhooks) == 0 || len(got.Realtime) == 0 {
						t.Fatal("tick lost committed events or publication", len(got.Events), len(got.Webhooks), len(got.Realtime))
					}
					got.Response, err = json.Marshal(scaleP21SW201Metadata(t, database))
					if err != nil {
						t.Fatal(err)
					}
					normalized := scaleP21SW201Normalize(t, scaleP21SW59Normalized(t, got, before, started, finished), "", started, finished)
					ids := map[string]string{runs[5].ID: "<tick-run:0>", runs[6].ID: "<tick-run:1>"}
					var normalizeRuns func(any) any
					normalizeRuns = func(value any) any {
						switch value := value.(type) {
						case string:
							if id, ok := ids[value]; ok {
								return id
							}
						case map[string]any:
							for key, child := range value {
								value[key] = normalizeRuns(child)
							}
						case []any:
							for index, child := range value {
								value[index] = normalizeRuns(child)
							}
						}
						return value
					}
					normalized = normalizeRuns(normalized)
					if !bounded {
						expected = normalized
					} else if !reflect.DeepEqual(expected, normalized) {
						t.Fatal("actual bounded tick changed canonical state, actions or publication", mode)
					}
					t.Logf("tick scheduled-fails=%t mode=%s committed-runs=2 SQL=%d", failFirst, mode, statements)
				})
			}
		})
	}
}
