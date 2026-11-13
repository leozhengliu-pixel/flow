package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW25AggregateCollisionEvidence(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		ids := []string{"migration_sw25_collision", "review_event_sw25_collision", "release_pipeline_sw25_collision"}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw25.collisions", "", nil, func(data *domain.Bootstrap) error {
			data.Drafts = nil
			for _, id := range ids {
				data.Drafts = append(data.Drafts, domain.Draft{
					ID: id + "-draft", ContentData: map[string]any{"id": id, "marker": id + "-earlier-reference", "hiddenCount": 37},
				})
			}
			data.Documents = append(data.Documents, domain.Document{
				ID: "sw25-release-collision-doc", Title: "Earlier reference", Creator: f.data.Viewer,
				ContentData: map[string]any{"id": ids[2], "marker": ids[2] + "-earlier-reference", "hiddenCount": 37},
			})
			data.MigrationJobs = []domain.MigrationJob{{ID: ids[0], UserID: f.data.Viewer.ID, Status: "mapping", Target: "before"}}
			data.Reviews = []domain.CodeReview{{
				ID: "review_sw25_collision", Events: []domain.ReviewEvent{{ID: ids[1], Type: "comment", Body: "Comment", Actor: f.data.Viewer}},
			}}
			data.ReleasePipelines = []domain.ReleasePipeline{{ID: ids[2], Name: "Pipeline", AccessKeyHash: secretHash("sw25-key")}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		data, ok := repo.WorkspaceMetadata(workspace)
		if !ok {
			t.Fatal("missing owned collision workspace")
		}
		service := &server{store: repo}
		for index, operation := range []func(){
			func() {
				r, w := f.request(f.data.Viewer, nil, map[string]any{"target": "flow"}), httptest.NewRecorder()
				r.SetPathValue("id", ids[0])
				service.updateMigrationMappings(w, r)
				if w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
			},
			func() {
				r := f.request(f.data.Viewer, nil, nil)
				if _, err := service.resolveMCPDiffThread(r.Context(), mcpActor{WorkspaceKey: workspace, User: f.data.Viewer}, data, map[string]any{"threadId": ids[1]}); err != nil {
					t.Fatal(err)
				}
			},
			func() {
				r := f.request(f.data.Viewer, nil, nil)
				service.touchReleaseAccessKey(r, workspace, ids[2])
			},
		} {
			var event domain.RealtimeEvent
			repo.SetRealtimeSink(func(_ string, next domain.RealtimeEvent) { event = next })
			operation()
			var payload struct {
				Entity struct {
					ID          string `json:"id"`
					Marker      string `json:"marker"`
					HiddenCount int    `json:"hiddenCount"`
				} `json:"entity"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil ||
				event.AggregateID != ids[index] || payload.Entity.ID != ids[index] ||
				payload.Entity.Marker != ids[index]+"-earlier-reference" || payload.Entity.HiddenCount != 37 {
				t.Fatalf("legacy aggregate closure changed for %s: %s, err=%v", ids[index], event.Payload, err)
			}
		}
		cycleID := "sw25-existing-cycle-without-prefix"
		if err := repo.MutateWorkspace(t.Context(), workspace, "cycle.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Cycles = []domain.Cycle{
				{ID: "cycle_sw25_earlier", Resources: []domain.CycleResource{{ID: cycleID, Title: "Earlier resource"}}},
				{ID: cycleID, CalendarToken: "flow_cycle_sw25_existing"},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var cycleEvent domain.RealtimeEvent
		repo.SetRealtimeSink(func(_ string, next domain.RealtimeEvent) { cycleEvent = next })
		r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", cycleID)
		service.cycleCalendarToken(w, r)
		var cyclePayload struct {
			Entity struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"entity"`
		}
		if err := json.Unmarshal(cycleEvent.Payload, &cyclePayload); err != nil || w.Code != http.StatusOK ||
			cyclePayload.Entity.ID != cycleID || cyclePayload.Entity.Title != "Earlier resource" {
			t.Fatal("nonprefix cycle aggregate no longer resolves its earlier resource", err)
		}
		current, ok := repo.WorkspaceMetadata(workspace)
		if !ok || current.MigrationJobs[0].Target != "flow" || !current.Reviews[0].Events[0].Resolved ||
			current.ReleasePipelines[0].AccessKeyLastUsedAt == nil || current.ReleasePipelines[0].AccessKeyLastUsedAt.After(time.Now().UTC()) {
			t.Fatal("collision fixture did not commit the intended target changes")
		}
	}
}
