package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW156Rows(t *testing.T, reader *scaleP04Database, workspace string) scaleP21SW59Rows {
	t.Helper()
	result := scaleP21SW59Rows{}
	rows := reader.query(t, `SELECT id,version,collection_order,data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace)
	defer rows.Close()
	for rows.Next() {
		var id string
		var version, order int64
		var raw []byte
		if err := rows.Scan(&id, &version, &order, &raw); err != nil {
			t.Fatal(err)
		}
		result.Issues = append(result.Issues, []any{id, version, order, json.RawMessage(raw)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21SW156RecurringNormalizationTickerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	for _, zone := range []string{"America/New_York", "Asia/Tokyo"} {
		if _, err := time.LoadLocation(zone); err != nil {
			t.Skip("recurrence fixture requires tzdata", zone)
		}
	}
	f := newRecurringFixture(t)
	repo, workspace := f.repository, f.key
	probe, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reader := &scaleP04Database{config: store.DatabaseConfig{Driver: "sqlite", Path: f.path}}
	t.Cleanup(func() {
		if reader.reader != nil {
			_ = reader.reader.Close()
		}
	})
	baseline, ok := repo.BootstrapFor(workspace)
	if !ok || len(baseline.Issues) < 2 {
		t.Fatal("recurring fixture requires two issues")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	prefix := idgen.New("sw156-")
	// Keep the captured tick outside generated event-clock normalization.
	tick := time.Date(2026, 10, 1, 10, 11, 12, 345678000, time.UTC)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	instant := func(value string) *time.Time {
		t.Helper()
		if value == "" {
			return nil
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		return &parsed
	}
	type schedule struct {
		due, next, zone, wantDue, wantNext string
		clear, changed                     bool
	}
	tests := []struct {
		name                        string
		schedules                   []schedule
		changeZone, missing, cancel bool
		missingTeamSettings         bool
	}{
		{name: "legacy-without-due", schedules: []schedule{{next: "2026-10-05T00:00:00Z", zone: "UTC", wantDue: "2026-10-05", wantNext: "2026-10-06T00:01:00Z", changed: true}}},
		{name: "legacy-existing-due", schedules: []schedule{{due: "2026-09-28", next: "2026-10-05T00:00:00Z", zone: "UTC", wantDue: "2026-09-28", wantNext: "2026-09-29T00:01:00Z", changed: true}}},
		{name: "spring-dst", schedules: []schedule{{due: "2026-03-08", next: "2026-03-08T05:00:00Z", zone: "America/New_York", wantDue: "2026-03-08", wantNext: "2026-03-09T04:01:00Z", changed: true}}},
		{name: "fall-dst", schedules: []schedule{{due: "2026-11-01", next: "2026-11-01T04:00:00Z", zone: "America/New_York", wantDue: "2026-11-01", wantNext: "2026-11-02T05:01:00Z", changed: true}}},
		{name: "timezone-change-after-noop", changeZone: true, schedules: []schedule{{due: "2026-03-08", next: "2026-03-09T00:01:00Z", zone: "UTC", wantDue: "2026-03-08", wantNext: "2026-03-09T04:01:00Z", changed: true}}},
		{name: "multiple-teams", schedules: []schedule{
			{due: "2026-03-08", next: "2026-03-08T00:00:00Z", zone: "America/New_York", wantDue: "2026-03-08", wantNext: "2026-03-09T04:01:00Z", changed: true},
			{due: "2026-03-08", next: "2026-03-08T00:00:00Z", zone: "Asia/Tokyo", wantDue: "2026-03-08", wantNext: "2026-03-08T15:01:00Z", changed: true},
		}},
		{name: "already-current-noop", schedules: []schedule{{due: "2026-03-08", next: "2026-03-09T04:01:00Z", zone: "America/New_York", wantDue: "2026-03-08", wantNext: "2026-03-09T04:01:00Z"}}},
		{name: "cleared-recurrence-indexed-next", schedules: []schedule{{due: "2026-10-05", next: "2026-10-05T00:00:00Z", zone: "UTC", wantDue: "2026-10-05", clear: true, changed: true}}},
		{name: "missing-team-settings-utc", missingTeamSettings: true, schedules: []schedule{{due: "2026-10-05", next: "2026-10-05T00:00:00Z", zone: "UTC", wantDue: "2026-10-05", wantNext: "2026-10-06T00:01:00Z", changed: true}}},
		{name: "missing-workspace", missing: true, schedules: []schedule{{next: "2026-10-05T00:00:00Z", zone: "UTC", changed: true}}},
		{name: "canceled", cancel: true, schedules: []schedule{{next: "2026-10-05T00:00:00Z", zone: "UTC", changed: true}}},
	}
	mutate := func(t *testing.T, change func(*domain.Bootstrap)) {
		t.Helper()
		for _, source := range []*store.SQLiteStore{repo, probe} {
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
		}
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw156_fixture", "", nil, func(data *domain.Bootstrap) error {
			change(data)
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
	}
	pairs := 0
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcomes := []any{}
			for _, source := range []*store.SQLiteStore{repo, probe} {
				mutate(t, func(data *domain.Bootstrap) {
					var restored domain.Bootstrap
					if err := json.Unmarshal(saved, &restored); err != nil {
						t.Fatal(err)
					}
					*data = restored
					for index := range data.Issues {
						data.Issues[index].Recurrence, data.Issues[index].NextOccurrenceAt = "", nil
					}
					for index, input := range test.schedules {
						target := &data.Issues[index]
						if index > 0 {
							team := target.Team
							team.ID, team.Key, team.Name = prefix+"second-team", prefix+"second-team", "Second recurrence team"
							data.Teams = append(data.Teams, team)
							target.Team = team
						}
						target.Version, target.UpdatedAt = 40, stamp
						target.DueDate = nil
						if input.due != "" {
							value := input.due
							target.DueDate = &value
						}
						target.Recurrence, target.NextOccurrenceAt = "daily", instant(input.next)
						if input.clear {
							target.Recurrence = ""
						}
						target.ArchivedAt = nil
						settings := teamSettings(data, target.Team.ID)
						settings.Timezone = input.zone
						data.TeamSettings[target.Team.ID] = settings
						if test.missingTeamSettings {
							delete(data.TeamSettings, target.Team.ID)
						}
					}
				})
				service := &server{store: source}
				if test.changeZone {
					beforeWarm := scaleP21SW156Rows(t, reader, workspace)
					if err := service.normalizeLegacyRecurringIssues(t.Context(), workspace, tick.Add(-time.Minute)); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(beforeWarm, scaleP21SW156Rows(t, reader, workspace)) {
						t.Fatal("UTC schedule was not a no-op before timezone change")
					}
					mutate(t, func(data *domain.Bootstrap) {
						settings := teamSettings(data, data.Issues[0].Team.ID)
						settings.Timezone = "America/New_York"
						data.TeamSettings[data.Issues[0].Team.ID] = settings
					})
				}
				before := scaleP21SW156Rows(t, reader, workspace)
				preimage := f.issues()
				priorIDs := map[string]bool{}
				for _, issue := range preimage {
					events, err := repo.Events(t.Context(), issue.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						priorIDs[event.ID] = true
					}
				}
				got := scaleP21SW59Outcome{}
				checkPublication := func() {
					if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() == 0 {
						t.Error("recurrence publication lost mutation output ownership")
					}
				}
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
					checkPublication()
					got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, event))
				})
				source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
					checkPublication()
					got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
				})
				var output bytes.Buffer
				started := time.Now().UTC()
				var callErr error
				statements := 0
				measureSQL := test.name == "legacy-without-due"
				func() {
					originalOutput, originalFlags, originalPrefix := log.Writer(), log.Flags(), log.Prefix()
					log.SetOutput(&output)
					log.SetFlags(0)
					log.SetPrefix("")
					defer log.SetOutput(originalOutput)
					defer log.SetFlags(originalFlags)
					defer log.SetPrefix(originalPrefix)
					ctx := t.Context()
					if measureSQL {
						ctx = store.WithSQLStatementObserver(ctx, func(string) { statements++ })
					}
					if test.cancel {
						canceled, cancel := context.WithCancel(ctx)
						cancel()
						ctx = canceled
					}
					key := workspace
					if test.missing {
						key = prefix + "missing-workspace"
					}
					callErr = service.normalizeLegacyRecurringIssues(ctx, key, tick)
				}()
				finished := time.Now().UTC()
				source.SetWebhookSink(nil)
				source.SetRealtimeSink(nil)
				if test.cancel && !errors.Is(callErr, context.Canceled) || !test.cancel && callErr != nil {
					t.Fatal("recurrence ticker error changed", callErr)
				}
				got.Rows = scaleP21SW156Rows(t, reader, workspace)
				updatedCount := 0
				for index, previous := range preimage {
					current := f.issue(previous.ID)
					expected := previous
					if index < len(test.schedules) {
						input := test.schedules[index]
						if input.changed && !test.missing && !test.cancel {
							updatedCount++
							expected.DueDate = nil
							if input.wantDue != "" {
								value := input.wantDue
								expected.DueDate = &value
							}
							expected.NextOccurrenceAt, expected.Version, expected.UpdatedAt = instant(input.wantNext), previous.Version+1, tick
							next := "none"
							if expected.NextOccurrenceAt != nil {
								next = expected.NextOccurrenceAt.Format(time.RFC3339)
							}
							line := fmt.Sprintf("Recurring issue workspace=%s issue=%s upgraded: due %s next %s\n", workspace, previous.ID, optionalID(expected.DueDate), next)
							if !strings.Contains(output.String(), line) {
								t.Fatal("recurrence upgrade log lost exact issue/due/next values", output.String(), line)
							}
						}
					}
					if !reflect.DeepEqual(current, expected) {
						t.Fatal("normalization changed fields beyond exact due/next/version/captured tick", scaleP21M2Diff(expected, current))
					}
					events, err := repo.Events(t.Context(), previous.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						if !priorIDs[event.ID] {
							if event.Type != "issue.updated" || event.AggregateID != previous.ID ||
								!reflect.DeepEqual(scaleP21SW10JSON(t, event.Payload), scaleP21SW10JSON(t, map[string]string{"automation": "recurring"})) {
								t.Fatal("normalization changed original event type/aggregate/payload", event)
							}
							got.Events = append(got.Events, event)
						}
					}
				}
				if len(got.Rows.Issues) != len(before.Issues) || len(got.Events) != updatedCount || len(got.Webhooks) != updatedCount || len(got.Realtime) != updatedCount ||
					strings.Count(output.String(), "upgraded:") != updatedCount {
					t.Fatal("normalization changed issue/event/publication/log count", updatedCount, len(got.Events), len(got.Webhooks), len(got.Realtime), output.String())
				}
				if updatedCount == 0 && !reflect.DeepEqual(before, got.Rows) {
					t.Fatal("no-op/missing/canceled normalization changed issue SQL rows")
				}
				if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("normalization retained bounded work or decoded complete metadata")
				}
				if measureSQL {
					t.Logf("representative_recurrence_normalization bounded=%t observed_SQL=%d", source.BoundedMetadataEnabled(), statements)
				}
				outcomes = append(outcomes, map[string]any{"effects": scaleP21SW59Normalized(t, got, before, started, finished), "log": output.String()})
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("actual normalization ticker SQL/event/publication/log differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
			pairs++
		})
	}
	t.Logf("actual recurrence-normalization ticker pairs=%d; captured tick clock exact; no occurrence creation invoked", pairs)
}
