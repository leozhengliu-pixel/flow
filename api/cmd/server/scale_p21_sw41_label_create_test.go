package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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

func scaleP21SW41OtherLabels(t *testing.T, repo *store.SQLiteStore, workspace, skip string) (string, int, string) {
	t.Helper()
	digest := sha256.New()
	count, last := 0, ""
	if err := repo.StreamMetadataRecords(t.Context(), workspace, "labels", func(key store.RecordKey, input io.Reader) error {
		count++
		last = key.ID
		if key.ID == skip {
			return nil
		}
		_, err := io.Copy(digest, input)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return string(digest.Sum(nil)), count, last
}

func scaleP21SW41CreationJSON(t *testing.T, value any, created domain.IssueLabel) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result := scaleP21M3JSON(t, raw)
	var normalize func(any) any
	normalize = func(value any) any {
		switch item := value.(type) {
		case map[string]any:
			for key, value := range item {
				item[key] = normalize(value)
			}
		case []any:
			for index, value := range item {
				item[index] = normalize(value)
			}
		case string:
			if item == created.ID {
				return "generated-label-id"
			}
			if item == created.CreatedAt.Format(time.RFC3339Nano) {
				return "generated-created-time"
			}
		}
		return value
	}
	return normalize(result)
}

func scaleP21SW41SeedLabelCreation(data *domain.Bootstrap) {
	data.Labels = nil
	for index := range 80 {
		data.Labels = append(data.Labels, domain.IssueLabel{
			ID: fmt.Sprintf("label_sw41_create_keep_%03d", index), Name: "Retained",
			Scope: "Workspace", ResourceType: "issue",
		})
	}
	data.Labels[len(data.Labels)-1].Description = strings.Repeat("large unrelated label ", 20000)
	data.LabelGroups = []domain.LabelGroup{
		{ID: "sw41-create-group", Name: "Group", Scope: "Workspace", ResourceType: "issue"},
		{ID: "sw41-create-unrelated-group", Description: strings.Repeat("large unrelated group ", 20000), Scope: "Workspace", ResourceType: "issue"},
	}
}

func TestScaleP21SW41WorkspaceLabelCreateBaseline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.label_create_baseline", "", nil, func(data *domain.Bootstrap) error {
			scaleP21SW41SeedLabelCreation(data)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		run := func() int {
			t.Helper()
			statements := 0
			r := scaleP21M3Request(workspace, f.data.Viewer, nil, `{"name":"New label","groupId":"sw41-create-group"}`)
			r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) { statements++ }))
			w := httptest.NewRecorder()
			(&server{store: repo}).createWorkspaceLabel(w, r)
			if w.Code != http.StatusCreated {
				t.Fatalf("label creation baseline status=%d body=%s", w.Code, w.Body.String())
			}
			return statements
		}
		run()
		for sample := range 3 {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			statements := run()
			runtime.ReadMemStats(&after)
			t.Logf("legacy label creation sample=%d bytes=%d allocs=%d SQL=%d", sample, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, statements)
		}
	}
}

func TestScaleP21SW41WorkspaceLabelCreateDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		principals := scaleP21M3Principals(t, f)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, principal := range principals {
			for _, credential := range scaleP21M3Credentials() {
				for _, body := range []string{
					`{"name":" New label "}`,
					`{"name":"Grouped","groupId":" sw41-create-group ","archivedAt":"invalid"}`,
					`{"name":"Archived","groupId":"sw41-create-group","archivedAt":"2026-11-13T01:02:03.000000004+05:45"}`,
					`{"name":"Invalid group","groupId":"sw41-create-missing"}`,
				} {
					t.Run(principal.name+"/"+credential.name+"/"+body, func(t *testing.T) {
						var outcomes []any
						for _, source := range []*store.SQLiteStore{repo, probe} {
							repo.SetWebhookSink(nil)
							repo.SetRealtimeSink(nil)
							if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
							if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.label_create_reset", "", nil, func(data *domain.Bootstrap) error {
								scaleP21SW41SeedLabelCreation(data)
								return nil
							}); err != nil {
								t.Fatal(err)
							}
							if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
								t.Fatal(err)
							}
							unchanged, count, _ := scaleP21SW41OtherLabels(t, repo, workspace, "")
							if count != 80 {
								t.Fatal("creation fixture does not exceed the generic record limit")
							}
							var webhook domain.DomainEvent
							var realtime domain.RealtimeEvent
							source.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhook = event })
							source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = event })
							r := scaleP21M3Request(workspace, principal.user, credential.key, body)
							w := httptest.NewRecorder()
							started := time.Now().UTC()
							scaleP21M3Call(&server{store: source}, w, r, (*server).createWorkspaceLabel)
							var created domain.IssueLabel
							var persisted any
							if strings.Contains(body, "Invalid group") {
								if w.Code != http.StatusBadRequest || webhook.Type != "" || realtime.Type != "" {
									t.Fatal("invalid group creation changed its failure boundary")
								}
							} else {
								if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.ID == "" ||
									created.CreatedAt.Before(started) || created.CreatedAt.After(time.Now().UTC()) || created.CreatorID != principal.user.ID {
									t.Fatalf("invalid created label: status=%d body=%s", w.Code, w.Body.String())
								}
								batch, err := repo.ReadCanonicalRecords(t.Context(), workspace, []store.RecordKey{{Kind: "labels", ID: created.ID}}, "detail")
								if err != nil || len(batch.Items) != 1 || batch.Items[0].Missing || batch.Items[0].Omitted {
									t.Fatal("created canonical label is unavailable", err)
								}
								persisted = []any{batch.Items[0].Key, batch.Items[0].Data}
								if webhook.AggregateID != created.ID || realtime.AggregateID != created.ID {
									t.Fatal("created label aggregate changed")
								}
							}
							after, total, last := scaleP21SW41OtherLabels(t, repo, workspace, created.ID)
							if after != unchanged || created.ID != "" && (total != 81 || last != created.ID) || created.ID == "" && total != 80 {
								t.Fatal("creation changed unrelated labels or collection order")
							}
							outcomes = append(outcomes, scaleP21SW41CreationJSON(t, []any{
								w.Code, scaleP21M3JSON(t, w.Body.Bytes()), persisted,
								[]any{webhook.Type, webhook.AggregateID, scaleP21M3JSON(t, webhook.Payload), scaleP21M3JSON(t, webhook.PreviousValues), scaleP21M3JSON(t, webhook.Snapshot), webhook.ActorID},
								[]any{realtime.Type, realtime.AggregateID, scaleP21M3JSON(t, realtime.Payload), realtime.ActorID},
							}, created))
						}
						if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
							t.Fatal(scaleP21M2Diff(outcomes[0], outcomes[1]))
						}
					})
				}
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("label creation fully decoded metadata")
		}
	}
}

func TestScaleP21SW41WorkspaceLabelCreateDeniedAndRollback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.label_create_denied", "", nil, func(data *domain.Bootstrap) error {
			scaleP21SW41SeedLabelCreation(data)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		_, member := scaleP03Join(t, f, "SW41 label member", workspace+"-member@example.test", "member")
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW41 label foreign", workspace+"-foreign", "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"cross-workspace", "missing-workspace", "queued-revocation", "replay-revocation"} {
			key := workspace
			switch phase {
			case "cross-workspace":
				key = foreign.Workspace.URLKey
			case "missing-workspace":
				key = workspace + "-missing"
			case "queued-revocation":
				if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
					t.Fatal(err)
				}
			}
			before := scaleP21M3Fingerprint(t, repo, workspace, "labels")
			for _, credential := range scaleP21M3Credentials() {
				for _, source := range []*store.SQLiteStore{repo, probe} {
					published := 0
					source.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
					source.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
					w := httptest.NewRecorder()
					r := scaleP21M3Request(key, member, credential.key, `{"name":"Denied"}`)
					scaleP21M3Call(&server{store: source}, w, r, (*server).createWorkspaceLabel)
					if w.Code != http.StatusForbidden || published != 0 || scaleP21M3Fingerprint(t, repo, workspace, "labels") != before {
						t.Fatalf("%s bounded=%v admitted denied creation", phase, source == probe)
					}
				}
			}
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "labels")
		for _, body := range []string{
			`{"name":"Canceled","archivedAt":"2026-11-13T01:02:03+05:45"}`,
			`{"name":"Oversized","description":"` + strings.Repeat("large label ", 30000) + `"}`,
		} {
			published := 0
			probe.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
			probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
			r := scaleP21M3Request(workspace, f.data.Viewer, nil, body)
			if strings.Contains(body, "Canceled") {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			(&server{store: probe}).createWorkspaceLabel(w, r)
			if w.Code != http.StatusInternalServerError || published != 0 || scaleP21M3Fingerprint(t, repo, workspace, "labels") != before {
				t.Fatal("failed creation changed canonical state or published")
			}
			if strings.Contains(body, "Oversized") && !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) {
				t.Fatalf("oversized creation did not return its explicit budget error: %s", w.Body.String())
			}
		}
	}
}

func TestScaleP21SW41WorkspaceLabelCreateMeasurement(t *testing.T) {
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
		for pair := range 3 {
			for _, source := range []*store.SQLiteStore{repo, probe} {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.label_create_measurement", "", nil, func(data *domain.Bootstrap) error {
					scaleP21SW41SeedLabelCreation(data)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				statements := 0
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				r := scaleP21M3Request(workspace, f.data.Viewer, nil, `{"name":"New label","groupId":"sw41-create-group"}`)
				r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) { statements++ }))
				w := httptest.NewRecorder()
				(&server{store: source}).createWorkspaceLabel(w, r)
				runtime.ReadMemStats(&after)
				if w.Code != http.StatusCreated {
					t.Fatalf("bounded=%v creation status=%d body=%s", source == probe, w.Code, w.Body.String())
				}
				t.Logf("label creation pair=%d bounded=%v bytes=%d allocs=%d SQL=%d", pair, source == probe, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, statements)
			}
		}
		r := scaleP21M3Request(workspace, f.data.Viewer, nil, `{"name":"Trace label","groupId":"sw41-create-group"}`)
		r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(statement string) { t.Log("label creation SQL:", statement) }))
		w := httptest.NewRecorder()
		(&server{store: probe}).createWorkspaceLabel(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("label creation trace status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
