package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW41LabelBaseline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.label_baseline", "", nil, func(data *domain.Bootstrap) error {
			data.Labels = []domain.IssueLabel{{ID: "label_sw41_baseline", Name: "Before", Scope: "Workspace", ResourceType: "issue"}}
			data.Projects = append(data.Projects, domain.Project{ID: "sw41-label-unrelated", Description: strings.Repeat("unrelated ", 40000)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		run := func() int {
			t.Helper()
			statements := 0
			request := scaleP21M3Request(workspace, f.data.Viewer, nil, `{"name":"Before"}`)
			request.SetPathValue("id", "label_sw41_baseline")
			request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
			response := httptest.NewRecorder()
			(&server{store: repo}).updateWorkspaceLabel(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("label baseline status=%d body=%s", response.Code, response.Body.String())
			}
			return statements
		}
		run()
		for sample := range 3 {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			sql := run()
			runtime.ReadMemStats(&after)
			t.Logf("legacy label noop sample=%d bytes=%d allocs=%d SQL=%d", sample, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, sql)
		}
	}
}

func TestScaleP21SW41LabelMeasurement(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.label_measurement", "", nil, func(data *domain.Bootstrap) error {
			data.Labels = []domain.IssueLabel{{ID: "label_sw41_baseline", Name: "Before", Scope: "Workspace", ResourceType: "issue"}}
			data.Projects = append(data.Projects, domain.Project{ID: "sw41-label-unrelated", Description: strings.Repeat("unrelated ", 40000)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		run := func(source *store.SQLiteStore, trace bool) int {
			t.Helper()
			statements := 0
			r := scaleP21M3Request(workspace, f.data.Viewer, nil, `{"name":"Before"}`)
			r.SetPathValue("id", "label_sw41_baseline")
			r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(statement string) {
				statements++
				if trace {
					t.Log("label noop SQL:", statement)
				}
			}))
			w := httptest.NewRecorder()
			(&server{store: source}).updateWorkspaceLabel(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("bounded=%v status=%d body=%s", source == probe, w.Code, w.Body.String())
			}
			return statements
		}
		run(repo, false)
		run(probe, false)
		for pair := range 3 {
			for _, source := range []*store.SQLiteStore{repo, probe} {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				statements := run(source, false)
				runtime.ReadMemStats(&after)
				t.Logf("label noop pair=%d bounded=%v bytes=%d allocs=%d SQL=%d", pair, source == probe, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, statements)
			}
		}
		run(probe, true)
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("label measurement fully decoded metadata")
		}
	}
}

func TestScaleP21SW41LabelPointDifferential(t *testing.T) {
	seed := func(data *domain.Bootstrap, actor domain.User) {
		data.Labels = []domain.IssueLabel{
			{ID: "label_sw41", Name: "Before", Scope: "Workspace", ResourceType: "issue", CreatorID: actor.ID, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{ID: "label_sw41_other", Name: "Retained", Description: strings.Repeat("unrelated label ", 25000), Scope: "Workspace"},
		}
		data.LabelGroups = []domain.LabelGroup{
			{ID: "sw41-group", Name: "Group", Scope: "Workspace", ResourceType: "issue"},
			{ID: "sw41-other-group", Name: "Retained", Description: strings.Repeat("unrelated group ", 25000), Scope: "Workspace", ResourceType: "issue"},
		}
	}
	other := []store.RecordKey{{Kind: "labels", ID: "label_sw41_other"}, {Kind: "labelGroups", ID: "sw41-other-group"}}
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{
		{name: "sw41-label-name", kind: "labels", id: "label_sw41", body: `{"name":" After "}`, seed: seed, handler: (*server).updateWorkspaceLabel, otherKeys: other},
		{name: "sw41-label-offset", kind: "labels", id: "label_sw41", body: `{"archivedAt":"2026-11-13T01:02:03.000000004+05:45"}`, seed: seed, handler: (*server).updateWorkspaceLabel, otherKeys: other},
		{name: "sw41-label-group", kind: "labels", id: "label_sw41", body: `{"groupId":" sw41-group "}`, seed: seed, handler: (*server).updateWorkspaceLabel, otherKeys: other},
		{name: "sw41-label-invalid-group", kind: "labels", id: "label_sw41", body: `{"groupId":"sw41-missing-group"}`, seed: seed, handler: (*server).updateWorkspaceLabel, otherKeys: other},
	})
}

func TestScaleP21SW41LabelOffsetLifecycleAndAggregate(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		shadow := "Earlier\x00warm " + strings.Repeat("large earlier name ", 18000)
		var outcomes []any
		for _, source := range []*store.SQLiteStore{repo, probe} {
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.label_source", "", nil, func(data *domain.Bootstrap) error {
				data.Customers = []domain.Customer{{ID: "sw41-label", Name: shadow}}
				data.Labels = []domain.IssueLabel{{ID: "sw41-label", Name: "Before\x00warm", Scope: "Workspace", ResourceType: "issue", CreatorID: f.data.Viewer.ID}}
				data.LabelGroups = []domain.LabelGroup{{ID: "sw41-group", Name: "Before", Scope: "Workspace", ResourceType: "issue"}}
				data.Webhooks = []domain.Webhook{{ID: "sw41-label-hook", URL: "https://example.test/sw41", Enabled: true}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			unchanged := scaleP21M3Fingerprint(t, repo, workspace, "customers")
			var events []any
			var last domain.DomainEvent
			source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
				last = event
				events = append(events, []any{event.Type, event.AggregateID, scaleP21M3JSON(t, event.Payload), scaleP21M3JSON(t, event.PreviousValues), scaleP21M3JSON(t, event.Snapshot), event.ActorID})
			})
			source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
				events = append(events, []any{event.Type, event.AggregateID, scaleP21M3JSON(t, event.Payload), event.ActorID})
			})
			var native time.Time
			var responses []any
			for index, body := range []string{
				`{"archivedAt":"2026-11-13T01:02:03.000000004+05:45"}`,
				`{"name":"After","groupId":"sw41-group"}`,
				`{"archivedAt":"invalid"}`,
				`{"groupId":""}`,
				`{"archivedAt":""}`,
			} {
				r := scaleP21M3Request(workspace, f.data.Viewer, nil, body)
				r.SetPathValue("id", "sw41-label")
				w := httptest.NewRecorder()
				(&server{store: source}).updateWorkspaceLabel(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("bounded=%v step=%d status=%d body=%s", source == probe, index, w.Code, w.Body.String())
				}
				responses = append(responses, scaleP21M3JSON(t, w.Body.Bytes()))
				var data domain.Bootstrap
				if source == probe {
					data, _, err = source.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "labels", ID: "sw41-label"}}})
				} else {
					data, _ = source.WorkspaceMetadata(workspace)
				}
				if err != nil || len(data.Labels) != 1 {
					t.Fatal("successful label producer left its logical target unavailable", err)
				}
				if index == 0 {
					if data.Labels[0].ArchivedAt == nil || data.Labels[0].ArchivedAt.Format(time.RFC3339Nano) != "2026-11-13T01:02:03.000000004+05:45" {
						t.Fatal("offset or precision changed")
					}
					native = *data.Labels[0].ArchivedAt
				} else if index < 4 && (data.Labels[0].ArchivedAt == nil || *data.Labels[0].ArchivedAt != native) {
					t.Fatal("subsequent label edit or ignored syntax changed native time identity")
				} else if index == 4 && data.Labels[0].ArchivedAt != nil {
					t.Fatal("empty archivedAt did not clear the time")
				}
				if index == 0 || index == 1 || index == 3 || index == 4 {
					var snapshot struct {
						Entity map[string]any `json:"entity"`
					}
					if len(last.Snapshot) <= store.MetadataRecordPageMaxBytes || json.Unmarshal(last.Snapshot, &snapshot) != nil || snapshot.Entity["name"] != shadow {
						t.Fatal("large earlier warm aggregate did not retain precedence")
					}
				}
			}
			if scaleP21M3Fingerprint(t, repo, workspace, "customers") != unchanged {
				t.Fatal("label callback changed its unrelated earlier aggregate")
			}
			outcomes = append(outcomes, []any{responses, events, scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "labels", ID: "sw41-label"})})
		}
		if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
			t.Fatal(scaleP21M2Diff(outcomes[0], outcomes[1]))
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("label update fully decoded metadata")
		}
	}
}

func TestScaleP21SW41LabelValidationOrderAndCancellation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.label_validation", "", nil, func(data *domain.Bootstrap) error {
			data.Labels = []domain.IssueLabel{{ID: "label_sw41_order", Name: "Before", Scope: "Workspace", ResourceType: "issue", GroupID: "sw41-large-group"}}
			data.LabelGroups = []domain.LabelGroup{{ID: "sw41-large-group", Scope: "Workspace", ResourceType: "issue", Description: strings.Repeat("large group ", 30000)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "labels")
		for _, test := range []struct {
			id, body string
			code     int
		}{
			{"label_sw41_missing", `{"resourceType":"invalid","archivedAt":"invalid"}`, http.StatusNotFound},
			{"label_sw41_order", `{"resourceType":"invalid","archivedAt":"2026-11-13T01:02:03+05:45"}`, http.StatusBadRequest},
		} {
			var responses []any
			for _, source := range []*store.SQLiteStore{repo, probe} {
				r := scaleP21M3Request(workspace, f.data.Viewer, nil, test.body)
				r.SetPathValue("id", test.id)
				w := httptest.NewRecorder()
				(&server{store: source}).updateWorkspaceLabel(w, r)
				if w.Code != test.code {
					t.Fatalf("target/input ordering changed: bounded=%v status=%d body=%s", source == probe, w.Code, w.Body.String())
				}
				responses = append(responses, scaleP21M3JSON(t, w.Body.Bytes()))
			}
			if !reflect.DeepEqual(responses[0], responses[1]) {
				t.Fatal("validation response differs")
			}
		}
		published := 0
		probe.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
		probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
		r := scaleP21M3Request(workspace, f.data.Viewer, nil, `{"name":"After"}`)
		r.SetPathValue("id", "label_sw41_order")
		w := httptest.NewRecorder()
		(&server{store: probe}).updateWorkspaceLabel(w, r)
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) || published != 0 ||
			scaleP21M3Fingerprint(t, repo, workspace, "labels") != before {
			t.Fatalf("oversized selected group was not rejected atomically: status=%d body=%s published=%d", w.Code, w.Body.String(), published)
		}
		r = scaleP21M3Request(workspace, f.data.Viewer, nil, `{"archivedAt":"2026-11-13T01:02:03+05:45"}`)
		r.SetPathValue("id", "label_sw41_order")
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		r = r.WithContext(ctx)
		w = httptest.NewRecorder()
		(&server{store: probe}).updateWorkspaceLabel(w, r)
		if w.Code >= 200 && w.Code < 300 || published != 0 || scaleP21M3Fingerprint(t, repo, workspace, "labels") != before {
			t.Fatal("canceled timestamp producer changed canonical state")
		}
	}
}
