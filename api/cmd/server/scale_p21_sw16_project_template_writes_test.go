package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW16ProjectTemplateWriteAggregateHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		id := idgen.New("sw16-template-collision-")
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		seed := func(deleting bool) {
			t.Helper()
			if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "sw16.template_collision", "", nil, func(data *domain.Bootstrap) error {
				data.ProjectTemplates = []domain.ProjectTemplate{{ID: id, Name: "Before", CreatedAt: stamp, UpdatedAt: stamp, Creator: data.Viewer}}
				data.IssueTemplates = nil
				if !deleting {
					data.IssueTemplates = []domain.IssueTemplate{{
						ID: id + "-issue-template", Name: "Earlier nested template", Creator: data.Viewer,
						FormFields: []domain.TemplateFormField{{ID: id, Label: "Earlier template form field"}},
					}}
				}
				data.Documents = []domain.Document{{
					ID: id + "-document", Title: "Earlier aggregate", Creator: data.Viewer,
					ContentData: map[string]any{"id": id, "marker": "earlier-template-reference", "hiddenCount": 37},
				}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		for _, deleting := range []bool{false, true} {
			seed(deleting)
			var event domain.RealtimeEvent
			repo.SetRealtimeSink(func(_ string, next domain.RealtimeEvent) { event = next })
			r, w := f.request(f.data.Viewer, nil, map[string]any{"name": "After"}), httptest.NewRecorder()
			r.SetPathValue("id", id)
			service := &server{store: repo}
			if deleting {
				service.deleteProjectTemplate(w, r)
			} else {
				service.updateProjectTemplate(w, r)
			}
			repo.SetRealtimeSink(nil)
			if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
				t.Fatal("real legacy template writer failed", w.Code, w.Body.String())
			}
			legacyStatus := w.Code
			if deleting && (!strings.Contains(string(event.Payload), `"marker":"earlier-template-reference"`) || !strings.Contains(string(event.Payload), `"hiddenCount":37`)) ||
				!deleting && !strings.Contains(string(event.Payload), `"label":"Earlier template form field"`) {
				t.Fatal("real legacy template write no longer uses its earlier aggregate closure", string(event.Payload))
			}
			if !deleting {
				// The update handler still uses the registered full writer.
				continue
			}
			// Deletion retains the exact earlier nested aggregate.
			seed(deleting)
			view, err := repo.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var bounded domain.RealtimeEvent
			view.SetRealtimeSink(func(_ string, next domain.RealtimeEvent) { bounded = next })
			r, w = f.request(f.data.Viewer, nil, map[string]any{"name": "After"}), httptest.NewRecorder()
			r.SetPathValue("id", id)
			if deleting {
				(&server{store: view}).deleteProjectTemplate(w, r)
			} else {
				(&server{store: view}).updateProjectTemplate(w, r)
			}
			if w.Code != legacyStatus {
				t.Fatal("bounded template writer failed", w.Code, string(bounded.Payload))
			}
			var left, right map[string]any
			if json.Unmarshal(event.Payload, &left) != nil || json.Unmarshal(bounded.Payload, &right) != nil {
				t.Fatal("aggregate witness payload was not valid JSON")
			}
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("template write effects differ deleting=%v: legacy=%s bounded=%s", deleting, event.Payload, bounded.Payload)
			}
			canonical, err := view.HydrateWorkspaceMetadata(t.Context(), f.data.Workspace.URLKey, store.MetadataRecordScope{
				Read: []store.RecordKey{{Kind: "projectTemplates", ID: id}},
			})
			if err != nil {
				t.Fatal("canonical template control", err)
			}
			if deleting && len(canonical.Data.ProjectTemplates) != 0 ||
				!deleting && (len(canonical.Data.ProjectTemplates) != 1 || canonical.Data.ProjectTemplates[0].Name != "After") {
				t.Fatal("bounded canonical template effect changed", canonical.Data.ProjectTemplates)
			}
		}
	}
}
