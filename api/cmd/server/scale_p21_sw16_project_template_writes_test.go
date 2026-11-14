package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
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
			if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
				t.Fatal("real legacy template writer failed", w.Code, w.Body.String())
			}
			if deleting && (!strings.Contains(string(event.Payload), `"marker":"earlier-template-reference"`) || !strings.Contains(string(event.Payload), `"hiddenCount":37`)) ||
				!deleting && !strings.Contains(string(event.Payload), `"label":"Earlier template form field"`) {
				t.Fatal("real legacy template write no longer uses its earlier aggregate closure", string(event.Payload))
			}
			if !deleting {
				continue
			}
			// Existing scoped delete writes the same canonical target but
			// lacks the earlier aggregate source after removing the template.
			seed(deleting)
			view, err := repo.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var bounded domain.RealtimeEvent
			view.SetRealtimeSink(func(_ string, next domain.RealtimeEvent) { bounded = next })
			r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", id)
			(&server{store: view}).deleteProjectTemplate(w, r)
			if w.Code != http.StatusNoContent || strings.Contains(string(bounded.Payload), `"marker":"earlier-template-reference"`) {
				t.Fatal("scoped deletion evidence unexpectedly changed", w.Code, string(bounded.Payload))
			}
			var decoded map[string]any
			if json.Unmarshal(event.Payload, &decoded) != nil || json.Unmarshal(bounded.Payload, &decoded) != nil {
				t.Fatal("aggregate witness payload was not valid JSON")
			}
			t.Log("actual template deletion: same canonical target removed; legacy aggregate has earlier document marker, scoped path omits it")
		}
	}
}
