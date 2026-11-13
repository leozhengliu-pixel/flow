package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21M2RecordWritesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range []struct {
				name string
				body any
				run  func(*server, http.ResponseWriter, *http.Request)
			}{
				{"project-history", nil, (*server).projectHistory},
				{"initiative-history", nil, (*server).initiativeHistory},
				{"project-relation-update", map[string]any{"type": "blocks"}, (*server).updateProjectRelation},
				{"project-relation-delete", nil, (*server).deleteProjectRelation},
				{"archive", nil, (*server).archiveCustomerNeed},
				{"note-update", map[string]any{"title": "After", "bodyData": map[string]any{"type": "doc"}}, (*server).updateReleaseNote},
				{"note-delete", nil, (*server).deleteReleaseNote},
				{"section-update", map[string]any{"name": "After", "position": 7}, (*server).updateTeamResourceSection},
				{"pin-update", map[string]any{"title": "After", "sectionId": "m2-section", "position": 3}, (*server).updateTeamResource},
				{"pin-delete", nil, (*server).deleteTeamResource},
			} {
				t.Run(operation.name, func(t *testing.T) {
					for _, missing := range []bool{false, true} {
						var effects, responses []any
						for _, s := range []*server{{store: repo}, {store: f.probe}} {
							if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
							if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.reset_records", "", nil, func(data *domain.Bootstrap) error {
								data.CustomerRequests = []domain.CustomerRequest{{ID: "m2-target"}, {ID: "m2-other"}}
								data.ReleaseNotes = []domain.ReleaseNote{{ID: "m2-note", ReleaseID: "m2-target", Title: "Before"}, {ID: "m2-other-note", ReleaseID: "other"}}
								data.TeamResourceSections = []domain.TeamResourceSection{{ID: "m2-section", TeamID: "m2-target", Name: "Before"}, {ID: "m2-other-section", TeamID: "other"}}
								data.TeamPinnedResources = []domain.TeamPinnedResource{{ID: "m2-pin", TeamID: "m2-target", Title: "Before"}, {ID: "m2-other-pin", TeamID: "other"}}
								data.AgentActivities = []domain.AgentActivity{{ID: "m2-target", Body: "Before"}, {ID: "m2-other-activity"}}
								data.ProjectRelations = []domain.ProjectRelation{{ID: "m2-relation", ProjectID: "m2-target", RelatedProjectID: "other", Type: "related"}, {ID: "m2-other-relation", ProjectID: "other"}}
								return nil
							}); err != nil {
								t.Fatal(err)
							}
							var event domain.DomainEvent
							var realtime domain.RealtimeEvent
							s.store.SetWebhookSink(func(_ string, e domain.DomainEvent) { event = e })
							s.store.SetRealtimeSink(func(_ string, e domain.RealtimeEvent) { realtime = e })
							r, w := f.request(user, key, operation.body), httptest.NewRecorder()
							r.SetPathValue("id", "m2-target")
							r.SetPathValue("noteId", "m2-note")
							r.SetPathValue("sectionId", "m2-section")
							r.SetPathValue("resourceId", "m2-pin")
							r.SetPathValue("relationId", "m2-relation")
							if missing {
								for _, field := range []string{"id", "noteId", "sectionId", "resourceId", "relationId"} {
									r.SetPathValue(field, "m2-missing")
								}
							}
							operation.run(s, w, r)
							responses = append(responses, []any{w.Code, scaleP21M2Response(t, w, false)})
							if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
							rows := scaleP21M2Canonical(t, repo, workspace,
								store.RecordKey{Kind: "customerRequests", ID: "m2-target"}, store.RecordKey{Kind: "customerRequests", ID: "m2-other"},
								store.RecordKey{Kind: "releaseNotes", ID: "m2-note"}, store.RecordKey{Kind: "releaseNotes", ID: "m2-other-note"},
								store.RecordKey{Kind: "teamResourceSections", ID: "m2-section"}, store.RecordKey{Kind: "teamResourceSections", ID: "m2-other-section"},
								store.RecordKey{Kind: "teamPinnedResources", ID: "m2-pin"}, store.RecordKey{Kind: "teamPinnedResources", ID: "m2-other-pin"},
								store.RecordKey{Kind: "agentActivities", ID: "m2-target"}, store.RecordKey{Kind: "agentActivities", ID: "m2-other-activity"},
								store.RecordKey{Kind: "projectRelations", ID: "m2-relation"}, store.RecordKey{Kind: "projectRelations", ID: "m2-other-relation"})
							effects = append(effects, scaleP21M2JSON(t, []any{rows, event.Type, event.AggregateID, event.Payload, event.PreviousValues, realtime.Payload}, false))
						}
						if !reflect.DeepEqual(responses[0], responses[1]) || !reflect.DeepEqual(effects[0], effects[1]) {
							t.Fatalf("point write missing=%v response: %s; effects: %s", missing, scaleP21M2Diff(responses[0], responses[1]), scaleP21M2Diff(effects[0], effects[1]))
						}
					}
				})
			}
		})
		// A background health edit has no principal; it still preserves other
		// hooks and the exact event/realtime envelope.
		var health []any
		for _, s := range []*server{{store: repo}, {store: f.probe}} {
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.reset_hook", "", nil, func(data *domain.Bootstrap) error {
				data.Webhooks[0].DisabledReason = ""
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			s.updateWebhookHealth(workspace, "m2-hook", func(hook *domain.Webhook) bool { hook.DisabledReason = "M2 health"; return true })
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			data, _ := repo.WorkspaceMetadata(workspace)
			health = append(health, scaleP21M2JSON(t, data.Webhooks, false))
		}
		if !reflect.DeepEqual(health[0], health[1]) {
			t.Fatalf("health differential: %v", health)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("record writes decoded full metadata")
		}
	}
}

func TestScaleP21M2WebhookDeliveryDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }))
	defer host.Close()
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		for _, mode := range []string{"enabled", "disabled", "missing", "lifecycle"} {
			var outcomes []any
			for _, s := range []*server{{store: repo, authDisabled: true}, {store: f.probe, authDisabled: true}} {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.delivery_hook", "", nil, func(data *domain.Bootstrap) error {
					data.Webhooks[0].Enabled, data.Webhooks[0].URL = mode != "disabled", host.URL
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC().Truncate(time.Millisecond)
				delivery := domain.WebhookDelivery{ID: idgen.New("m2-delivery-"), WorkspaceKey: workspace, WebhookID: "m2-hook", Kind: domain.WebhookDeliveryKindEvent, EventID: "m2-event", EventType: "M2", URL: host.URL, Payload: json.RawMessage(`{"m2":true}`), MaxAttempts: 1, CreatedAt: now, NextAttemptAt: &now}
				if mode == "missing" {
					delivery.WebhookID = "m2-missing"
				}
				if mode == "lifecycle" {
					delivery.Kind = domain.WebhookDeliveryKindLifecycle
				}
				if _, err := repo.EnqueueWebhookDelivery(t.Context(), delivery); err != nil {
					t.Fatal(err)
				}
				claimed, ok, err := repo.ClaimWebhookDelivery(t.Context(), delivery.ID, now, time.Minute)
				if err != nil || !ok {
					t.Fatal("delivery claim failed", err)
				}
				result, err := s.attemptWebhookDelivery(t.Context(), claimed, now)
				if err != nil {
					t.Fatal(err)
				}
				result.ID, result.DurationMS = "", 0
				result.CompletedAt = nil
				for i := range result.Attempts {
					result.Attempts[i].DurationMS = 0
				}
				outcomes = append(outcomes, scaleP21M2JSON(t, result, false))
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("delivery %s changed: %v", mode, outcomes)
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("delivery decoded full metadata")
		}
	}
}

func TestScaleP21M2LoopFeedbackDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.CreateLoopRun(t.Context(), workspace, domain.LoopRun{ID: "m2-run", LoopID: "m2-loop", Status: "completed", StartedAt: time.Unix(123456, 0).UTC()}, store.LoopRunLease{}); err != nil {
			t.Fatal(err)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, id := range []string{"m2-loop", "m2-wrong-loop"} {
				var outcomes []any
				for _, s := range []*server{{store: repo}, {store: f.probe}} {
					if _, err := repo.UpdateLoopRun(t.Context(), workspace, "m2-run", nil, func(run *domain.LoopRun) error {
						run.Feedback = nil
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					r, w := f.request(user, key, map[string]string{"rating": "up", "comment": "M2"}), httptest.NewRecorder()
					r.SetPathValue("id", id)
					r.SetPathValue("runId", "m2-run")
					s.setLoopRunFeedback(w, r)
					outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("feedback differential: %v", outcomes)
				}
			}
		})
	}
}
