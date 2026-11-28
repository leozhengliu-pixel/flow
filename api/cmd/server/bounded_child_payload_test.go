package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestBoundedChildMutationPayloads(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", "on")
	t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", "on")
	// The test-only mutation oracle sends point edits through the fallback.
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	for driver, repo := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := resourceAccessScaleFixture(t, repo)
			workspace := f.data.Workspace.URLKey
			const project, initiative, existing = "child-payload-project", "child-payload-initiative", "existing-child"
			at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
			err := repo.MutateWorkspace(workspaceFixtureContext(t.Context()), workspace, "child_payload.fixture", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects, domain.Project{
					ID: project, Name: "Roadmap", TeamIDs: []string{data.Teams[0].ID},
					Milestones: []domain.ProjectMilestone{
						{ID: "first", ProjectID: project, Name: "First", CreatedAt: at, UpdatedAt: at},
						{ID: existing, ProjectID: project, Name: "Before", CreatedAt: at, UpdatedAt: at},
					},
				})
				data.Initiatives = append(data.Initiatives, domain.Initiative{ID: initiative, Name: "Portfolio", ProjectIDs: []string{project}})
				data.ProjectUpdates[project] = []domain.ProjectUpdate{{
					ID: existing, ProjectID: project, Body: "Before", Health: "onTrack", User: data.Viewer, CreatedAt: at,
					Snapshot: &domain.PulseSnapshot{Status: "stored project snapshot"},
				}}
				data.InitiativeUpdates[initiative] = []domain.InitiativeUpdate{{
					ID: existing, InitiativeID: initiative, Body: "Before", Health: "onTrack", User: data.Viewer, CreatedAt: at,
					Snapshot: &domain.PulseSnapshot{Status: "stored initiative snapshot"},
				}}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			view, err := repo.BoundedMetadataViewForTesting(t.Context())
			if err != nil || !view.BoundedMetadataEnabled() {
				t.Fatalf("bounded metadata fixture: %v", err)
			}
			s := &server{store: view, uploadPath: t.TempDir()}
			handler := newTestHandler(t, s)
			if err := s.stopDeliveryScheduler(t.Context()); err != nil {
				t.Fatal(err)
			}
			var event domain.DomainEvent
			var realtime domain.RealtimeEvent
			var publishWork int
			view.SetWebhookSink(func(key string, value domain.DomainEvent) {
				if key == workspace && (value.AggregateID == project || value.AggregateID == initiative) {
					event = value
				}
			})
			view.SetRealtimeSink(func(key string, value domain.RealtimeEvent) {
				if key == workspace && (value.AggregateID == project || value.AggregateID == initiative) {
					realtime = value
					publishWork = view.MetadataWorkUsedForTesting()
				}
			})
			const rich = `"bodyData":{"content":[{"text":"\u003cnew\u003e","type":"text"}],"type":"doc"}`
			for _, family := range []struct {
				path, event, parent, childText string
				create, patch                  string
			}{
				{"/api/projects/" + project + "/milestones", "project.milestone", project, "name",
					`{"description":"\u003cnew\u003e","name":"Release"}`, `{"name":"Updated","targetDate":""}`},
				{"/api/projects/" + project + "/updates", "project.update", project, "body",
					`{"body":"Project update",` + rich + `,"health":""}`, `{"body":"Updated",` + rich + `}`},
				{"/api/initiatives/" + initiative + "/updates", "initiative.update", initiative, "body",
					`{"body":"Initiative update",` + rich + `,"health":""}`, `{"body":"Updated",` + rich + `}`},
			} {
				for _, operation := range []struct {
					method, suffix, input string
					status                int
				}{
					{http.MethodPost, "created", family.create, http.StatusCreated},
					{http.MethodPatch, "updated", family.patch, http.StatusOK},
					{http.MethodDelete, "deleted", "", http.StatusNoContent},
				} {
					kind := family.event + "_" + operation.suffix
					t.Run(kind, func(t *testing.T) {
						event, realtime = domain.DomainEvent{}, domain.RealtimeEvent{}
						publishWork = 0
						path := family.path
						if operation.method != http.MethodPost {
							path += "/" + existing
						}
						r := httptest.NewRequest(operation.method, f.host.URL+path, bytes.NewBufferString(operation.input))
						r.Header.Set("Content-Type", "application/json")
						r.Header.Set("X-Workspace-Key", workspace)
						for _, cookie := range f.admin.Jar.Cookies(r.URL) {
							r.AddCookie(cookie)
						}
						w := httptest.NewRecorder()
						handler.ServeHTTP(w, r)
						if w.Code != operation.status {
							t.Fatalf("%s %s = %d, want %d: %s", operation.method, path, w.Code, operation.status, w.Body.String())
						}
						id, wantInput := existing, operation.input
						if operation.method == http.MethodPost {
							var created struct{ ID string }
							if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
								t.Fatalf("response child id: %v", err)
							}
							id = created.ID
						} else if operation.method == http.MethodDelete {
							wantInput = `{"id":"` + id + `"}`
						}
						if event.Type != kind || event.AggregateID != family.parent || realtime.Type != kind || realtime.ID != event.ID {
							t.Fatalf("missing committed events for %s: webhook=%q realtime=%q", kind, event.Type, realtime.Type)
						}
						if family.event == "project.milestone" && operation.method != http.MethodDelete && publishWork == 0 {
							t.Fatal("point write did not retain publication work")
						}
						child, found, input := webhookChildOf(event.Payload)
						if !found || child.ID != id || string(input) != wantInput {
							t.Fatalf("committed input or child marker changed: %s", event.Payload)
						}
						if operation.method == http.MethodDelete {
							entity, _ := child.Entity.(map[string]any)
							if entity["id"] != id || entity[family.childText] == nil || entity["snapshot"] != nil ||
								family.childText == "name" && entity["sortOrder"] != float64(1) {
								t.Fatalf("missing or invalid deleted child: %s", event.Payload)
							}
						} else if child.Entity != nil {
							t.Fatalf("unexpected entity in live child marker: %s", event.Payload)
						}
						var payload map[string]json.RawMessage
						if err := json.Unmarshal(realtime.Payload, &payload); err != nil || len(payload["entity"]) == 0 ||
							!bytes.Equal(event.Snapshot, realtime.Payload) {
							t.Fatalf("realtime did not carry the committed entity: %v, %s", err, realtime.Payload)
						}
						if _, err := view.RecordJournalEventSeq(t.Context(), event.ID); err != nil {
							t.Fatalf("committed event was not journaled: %v", err)
						}
						if view.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("write leaked metadata work")
						}
					})
				}
			}
		})
	}
}

func TestChildPublicationWireGolden(t *testing.T) {
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	milestone := domain.ProjectMilestone{ID: "m", ProjectID: "p", Name: "<M>", CreatedAt: at, UpdatedAt: at}
	payload := childEventPayload{ID: &milestone.ID, Child: &webhookChild{
		ID: milestone.ID, Entity: struct {
			domain.ProjectMilestone
			SortOrder int `json:"sortOrder"`
		}{milestone, 2},
	}}
	raw := mustJSON(t, payload)
	const entity = `{"id":"m","projectId":"p","name":"\u003cM\u003e","description":"","createdAt":"2026-11-10T12:00:00Z","updatedAt":"2026-11-10T12:00:00Z","sortOrder":2}`
	const original = `{"id":"m","webhookChild":{"id":"m","entity":` + entity + `}}`
	if string(raw) != original {
		t.Fatalf("stored mutation payload changed\n got %s\nwant %s", raw, original)
	}
	child, found, stripped := webhookChildOf(raw)
	if !found {
		t.Fatal("child marker is missing")
	}
	childData, page, err := (&server{}).webhookChildEntity(t.Context(), "w", domain.DomainEvent{}, "milestones", child, map[string]any{"slugId": "project"}, "https://flow.example/w")
	if err != nil {
		t.Fatal(err)
	}
	event := webhookEvent{
		eventID: "e", eventType: "project.milestone_deleted", resource: "milestones", action: "remove", legacy: "projects",
		createdAt: at, payload: stripped, data: mustJSON(t, childData), url: page, noVersion1URL: true,
	}
	const normalized = `{"createdAt":"2026-11-10T12:00:00Z","description":"","id":"m","name":"\u003cM\u003e","projectId":"p","sortOrder":2,"updatedAt":"2026-11-10T12:00:00Z"}`
	for _, check := range []struct {
		version int
		want    string
	}{
		{1, `{"id":"e","type":"projects","action":"milestone_deleted","data":{"id":"m"},"createdAt":"2026-11-10T12:00:00Z","actor":null,"organizationId":"w","webhookId":"h","eventType":"project.milestone_deleted"}`},
		{2, `{"action":"remove","actor":null,"createdAt":"2026-11-10T12:00:00.000Z","data":` + normalized + `,"eventId":"e","eventType":"project.milestone_deleted","organizationId":"w","type":"ProjectMilestone","url":"https://flow.example/w/project/project/overview#m","webhookId":"h"}`},
	} {
		got, err := webhookEnvelopeBody(domain.Webhook{ID: "h", PayloadVersion: check.version}, "w", event)
		if err != nil || string(got) != check.want {
			t.Fatalf("webhook v%d: %v\n got %s\nwant %s", check.version, err, got, check.want)
		}
	}
	recorder, budget := httptest.NewRecorder(), newRealtimeBudget()
	writer := &realtimeBudgetWriter{ResponseWriter: recorder, budget: budget}
	realtime := domain.RealtimeEvent{ID: "e", Type: event.eventType, AggregateID: "p", Payload: raw, CreatedAt: at}
	const frame = "id: e~node\ndata: " +
		`{"id":"e","type":"project.milestone_deleted","aggregateId":"p","createdAt":"2026-11-10T12:00:00Z","payload":` + original + "}\n\n"
	if !writeSSE(writer, realtime, "node") || recorder.Body.String() != frame {
		t.Fatalf("SSE changed\n got %s\nwant %s", recorder.Body.String(), frame)
	}
	if !flushRealtime(writer) || budget.used.Load() != 0 {
		t.Fatal("SSE leaked frame work")
	}
}
