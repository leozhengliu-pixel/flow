package main

import (
	"context"
	"encoding/json"
	"errors"
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

func TestScaleP21PulseRemainingChildDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("remaining-child-")
		project, initiative := prefix+"project", prefix+"initiative"
		at := time.Date(2026, 11, 14, 6, 0, 0, 0, time.UTC)
		body := strings.Repeat("Complete selected child. ", 14000)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.remaining_child_fixture", "", nil, func(data *domain.Bootstrap) error {
			data.ProjectUpdates[project] = []domain.ProjectUpdate{
				{ID: "selected", ProjectID: project, Body: body, CreatedAt: at, User: data.Viewer,
					BodyData: map[string]any{"type": "doc", "content": []any{map[string]any{"text": "Native body"}}},
					Snapshot: &domain.PulseSnapshot{Status: "private"}, Comments: []domain.Comment{{ID: "comment", Body: "Complete comment", User: data.Viewer}},
					Reactions: map[string][]string{"like": {data.Viewer.ID}}, Diff: &domain.PulseDiff{Status: &domain.PulseValueChange{From: "Planned", To: "Active"}}},
				{ID: "later", ProjectID: project, Body: "Later child", CreatedAt: at.Add(-time.Hour)},
				{ID: "unrelated", ProjectID: project, Body: strings.Repeat("Unrelated child ", 100000)},
			}
			data.InitiativeUpdates[initiative] = []domain.InitiativeUpdate{
				{ID: "selected", InitiativeID: initiative, Body: body, CreatedAt: at, User: data.Viewer,
					Snapshot: &domain.PulseSnapshot{Status: "private"}, Comments: []domain.Comment{}, Reactions: map[string][]string{}},
				{ID: "later", InitiativeID: initiative, Body: "Later child", CreatedAt: at.Add(-time.Hour)},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name, resource, source, id, key string
			entity                          any
		}{
			{"project", "project_updates", project, "selected", workspace, nil},
			{"initiative", "initiative_updates", initiative, "selected", workspace, nil},
			{"missing-child", "project_updates", project, "missing", workspace, nil},
			{"missing-source", "project_updates", "missing", "selected", workspace, nil},
			{"missing-workspace", "project_updates", project, "selected", prefix + "missing", nil},
			{"default-workspace", "project_updates", project, "selected", "", nil},
			{"removed-entity", "project_updates", project, "removed", workspace, map[string]any{"body": "Deleted exact child"}},
			{"milestone", "milestones", project, "milestone", workspace, nil},
		}
		parent := map[string]any{"slugId": "old/source", "milestones": []any{map[string]any{"id": "milestone", "name": "First"}, map[string]any{"id": "milestone", "name": "Last duplicate"}}}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				var outputs []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					ctx, close := beginMetadataOutputReads(t.Context())
					entity, page, err := (&server{store: source}).webhookChildEntity(ctx, test.key, domain.DomainEvent{AggregateID: test.source}, test.resource, webhookChild{ID: test.id, Entity: test.entity}, parent, "https://flow.example/workspace")
					if err != nil {
						close()
						t.Fatal(err)
					}
					if source == f.probe && test.entity == nil && test.resource != "milestones" && test.name != "missing-workspace" && source.MetadataWorkUsedForTesting() == 0 {
						t.Fatal("child result outlived output admission")
					}
					outputs = append(outputs, scaleP21M2JSON(t, []any{entity, page}, false))
					close()
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("child encoding leaked output admission")
					}
					if entity != nil && test.id == "selected" && (entity["body"] != body || entity["snapshot"] != nil) {
						t.Fatal("child lost selected content or exposed stored snapshot")
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("child differential", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			})
		}
		service := &server{store: f.probe}
		if _, _, err := service.webhookChildEntity(t.Context(), workspace, domain.DomainEvent{AggregateID: project}, "project_updates", webhookChild{ID: "selected"}, parent, ""); !errors.Is(err, store.ErrBoundedMetadataCoverage) {
			t.Fatal("child escaped without containing owner", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, err := service.webhookChildEntity(ctx, workspace, domain.DomainEvent{AggregateID: project}, "project_updates", webhookChild{ID: "selected"}, parent, ""); !errors.Is(err, context.Canceled) {
			t.Fatal("child swallowed cancellation", err)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 || f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("child adapter decoded complete metadata or leaked admission")
		}
	}
}

func TestScaleP21PulseRemainingRequestDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, project, initiative := f.data.Workspace.URLKey, idgen.New("remaining-project-"), idgen.New("remaining-initiative-")
		at := time.Date(2026, 11, 14, 6, 0, 0, 0, time.UTC)
		start, target, milestoneDate := "2026-01-01", "2027-01-01", "2026-12-01"
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.remaining_fixture", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.FeatureFlags["pulse"] = true
			data.Projects = append(data.Projects, domain.Project{
				ID: project, Name: "First project", TeamIDs: []string{f.public}, Lead: &data.Viewer,
				MemberIDs: []string{f.principals[2].user.ID}, Description: strings.Repeat("Unrelated description ", 30000),
				Status: domain.ProjectStatus{ID: "active", Name: "Active"}, Priority: 2, PriorityLabel: "High",
				StartDate: &start, TargetDate: &target,
				Milestones: []domain.ProjectMilestone{{ID: "milestone", Name: "Complete milestone", TargetDate: &milestoneDate}},
			})
			data.Projects = append(data.Projects, domain.Project{ID: project, Name: "Later duplicate", TeamIDs: []string{f.public}})
			data.Initiatives = append(data.Initiatives, domain.Initiative{ID: initiative, Name: "Initiative", ProjectIDs: []string{project}, Owner: &data.Viewer})
			data.ProjectUpdates[project] = []domain.ProjectUpdate{{ID: "first", ProjectID: project, Body: strings.Repeat("Unrequested update ", 30000), CreatedAt: at}}
			data.Subscriptions = append(data.Subscriptions,
				domain.Subscription{ID: "remaining-explicit", UserID: data.Viewer.ID, ResourceType: "project", ResourceID: project, OptOutEvents: []string{pulseSubscriptionEvent}},
				domain.Subscription{ID: "remaining-initiative", UserID: data.Viewer.ID, ResourceType: pulseInitiativeProjectUpdatesType, ResourceID: initiative, Events: []string{pulseSubscriptionEvent}},
			)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		routes := []struct {
			name, kind, id string
			handler        func(*server, http.ResponseWriter, *http.Request)
		}{
			{"project", "project", project, (*server).getPulseSubscription},
			{"initiative", "initiative", initiative, (*server).getPulseSubscription},
			{"team", "team", f.public, (*server).getPulseSubscription},
			{"missing", "project", "missing", (*server).getPulseSubscription},
			{"initiative-projects", "", initiative, (*server).getInitiativeProjectUpdatesSubscription},
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, route := range routes {
				t.Run(route.name, func(t *testing.T) {
					var outputs []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						r := f.literalRequest(user, key, nil)
						r.SetPathValue("type", route.kind)
						r.SetPathValue("id", route.id)
						w := &scaleP21PulseSummaryResponse{ResponseRecorder: httptest.NewRecorder(), t: t}
						if source == f.probe {
							w.bounded = source
						}
						scaleP21CallWithOutputOwner(&server{store: source}, w, r, route.handler)
						outputs = append(outputs, []any{w.Code, scaleP21M2Response(t, w.ResponseRecorder, false)})
						if source.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("request result leaked admission")
						}
					}
					if !reflect.DeepEqual(outputs[0], outputs[1]) {
						t.Fatal("request differential", scaleP21M2Diff(outputs[0], outputs[1]))
					}
				})
			}
		})
		// Compare the compact pre-read's exact snapshot inputs at a fixed
		// clock, including the first duplicate project and indexed progress.
		var snapshots []*domain.PulseSnapshot
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			service := &server{store: source}
			if source.BoundedMetadataEnabled() {
				read, found, err := source.BeginPulseRead(t.Context(), workspace, false)
				if err != nil || !found {
					t.Fatal("snapshot source", found, err)
				}
				snapshots = append(snapshots, service.projectPulseSnapshot(t.Context(), read.Snapshot.Workspace.URLKey, *pulseProjectIn(read.Snapshot, project), at))
				read.Close()
			} else {
				feed, found := source.PulseFeed(workspace)
				if !found {
					t.Fatal("legacy snapshot source missing")
				}
				snapshots = append(snapshots, service.projectPulseSnapshot(t.Context(), feed.Workspace.URLKey, *pulseProjectIn(feed, project), at))
			}
		}
		if !reflect.DeepEqual(snapshots[0], snapshots[1]) {
			a, _ := json.Marshal(snapshots[0])
			b, _ := json.Marshal(snapshots[1])
			t.Fatalf("project snapshot differential: %s != %s", a, b)
		}
		if snapshots[1].Status != "Active" || snapshots[1].LeadID != f.data.Viewer.ID ||
			snapshots[1].StartDate != start || snapshots[1].TargetDate != target ||
			len(snapshots[1].Milestones) != 1 || snapshots[1].Milestones[0].TargetDate != milestoneDate {
			t.Fatal("snapshot fixture lost first project, lead, dates or milestone inputs")
		}
		t.Run("create-cancelled", func(t *testing.T) {
			var outputs []any
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r := f.literalRequest(f.data.Viewer, nil, domain.ProjectUpdateCreateInput{Body: "Cancelled update"})
				r.SetPathValue("id", project)
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				w := httptest.NewRecorder()
				scaleP21CallWithOutputOwner(&server{store: source}, w, r.WithContext(ctx), (*server).createProjectUpdate)
				if w.Code != http.StatusInternalServerError {
					t.Fatal("cancelled creation changed failure behavior", w.Code, w.Body.String())
				}
				outputs = append(outputs, []any{w.Code, scaleP21M2Response(t, w, false)})
			}
			if !reflect.DeepEqual(outputs[0], outputs[1]) {
				t.Fatal("cancelled creation differential", scaleP21M2Diff(outputs[0], outputs[1]))
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 || f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("request adapters decoded complete metadata or leaked admission")
		}
	}
}
