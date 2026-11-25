package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW338RecurringFourMiBDescriptionRequiredSuccess(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW182Setup(t)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)
	body := strings.Repeat("x", 4<<20)
	test := scaleP21SW206Case{
		name: "required-four-MiB-description", want: 4,
		edit: func(f scaleP21SW182Fixture, data *domain.Bootstrap, _ time.Time) {
			for index := range data.Issues {
				if data.Issues[index].ID == f.sw206ID("source") {
					data.Issues[index].Description = body
					return
				}
			}
			t.Fatal("owned recurring source missing")
		},
	}
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprintf("sql-reloaded=%t", reload), func(t *testing.T) {
			var expected string
			for _, bounded := range []bool{false, true} {
				scaleP21SW206Seed(t, f, test, now, false)
				source := f.repository
				if reload {
					scaleP21SW209ReloadSource(t, f.scaleP21SW10Fixture, source)
				}
				if bounded {
					source = f.probe
					if reload {
						var err error
						source, err = f.repository.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
						if err != nil {
							t.Fatal(err)
						}
						// The constructor captures installed native values.
						// Advance again to force the bounded SQL loader too.
						scaleP21SW209ReloadSource(t, f.scaleP21SW10Fixture, source)
					}
				}
				got, started := scaleP21SW206Run(t, f, source, test, now, scaleP21SW206Call(f, test, now))
				created, ok := got.Created.(*domain.Issue)
				if got.Error != "" || !ok || created == nil || created.Description != body || len(created.SubIssueIDs) != test.want-1 {
					t.Fatalf("bounded=%t: required 4 MiB recurring family success failed: error=%q created=%t",
						bounded, got.Error, ok && created != nil)
				}
				if len(got.Events) != 1 || len(got.Webhooks) != 1 || len(got.Realtime) < 1+test.want {
					t.Fatalf("bounded=%t: required recurring publication missing: events=%d webhooks=%d realtime=%d",
						bounded, len(got.Events), len(got.Webhooks), len(got.Realtime))
				}
				complete := map[string]bool{f.sw206ID("source"): false, created.ID: false}
				for _, value := range got.Rows.Issues {
					row, ok := value.([]any)
					if !ok || len(row) != 4 {
						t.Fatal("unexpected existing SW206 canonical issue tuple")
					}
					id, ok := row[0].(string)
					if !ok {
						t.Fatal("canonical issue tuple lost its physical ID")
					}
					if _, selected := complete[id]; !selected {
						continue
					}
					raw, ok := row[3].(json.RawMessage)
					if !ok {
						t.Fatal("canonical issue tuple lost its complete raw body")
					}
					var issue domain.Issue
					if err := json.Unmarshal(raw, &issue); err != nil {
						t.Fatal(err)
					}
					if issue.Description != body {
						t.Fatalf("bounded=%t: canonical recurring description was lost for %s", bounded, id)
					}
					complete[id] = true
				}
				for id, found := range complete {
					if !found {
						t.Fatalf("bounded=%t: required complete canonical row missing for %s", bounded, id)
					}
				}
				normalized := scaleP21SW206Normalize(t, got, started)
				if !bounded {
					expected = normalized
				} else if normalized != expected {
					t.Fatalf("required 4 MiB recurring output/canonical/publication differs: %s", scaleP21M2Diff(expected, normalized))
				}
			}
		})
	}
}
