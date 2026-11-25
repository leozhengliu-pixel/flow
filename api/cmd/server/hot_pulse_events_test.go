package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotPulseEventsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		project, hidden := idgen.New("pulse-event-project-"), idgen.New("pulse-event-hidden-")
		initiative, parent := idgen.New("pulse-event-initiative-"), idgen.New("pulse-event-parent-")
		at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.FixedZone("producer-zone", 3600))
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.pulse_events_seed", "", nil, func(data *domain.Bootstrap) error {
			private := ""
			for _, item := range data.Projects {
				if item.ID == "m2-unrelated" && len(item.TeamIDs) != 0 {
					private = item.TeamIDs[0]
				}
			}
			if private == "" {
				return errors.New("fixture private team is missing")
			}
			data.Projects = append(data.Projects,
				domain.Project{ID: project, SlugID: project + "-slug", PreviousSlugIDs: []string{project + "-old"}, Name: "Visible", TeamIDs: []string{f.public}, DependencyIDs: []string{hidden}, Initiatives: []string{initiative}, IssueCount: 71, Description: "Complete event description", Lead: &data.Viewer},
				domain.Project{ID: hidden, Name: "Private", TeamIDs: []string{private}},
			)
			data.Initiatives = append(data.Initiatives,
				domain.Initiative{ID: initiative, Name: "Mixed", ProjectIDs: []string{project, hidden}, ContributingTeamIDs: []string{f.public, private}, Owner: &data.Viewer, Favorite: true, Subscribed: true},
				domain.Initiative{ID: parent, Name: "Parent", ProjectIDs: []string{project}},
			)
			data.InitiativeRelations = append(data.InitiativeRelations, domain.InitiativeRelation{ID: idgen.New("pulse-event-relation-"), Type: "parent", InitiativeID: initiative, RelatedInitiativeID: parent})
			data.ProjectUpdates[project] = []domain.ProjectUpdate{{ID: project + "-update", ProjectID: project, User: data.Viewer, Body: "Full update", BodyData: map[string]any{"at": at}, CreatedAt: at, Snapshot: &domain.PulseSnapshot{Status: "private snapshot"}, Comments: []domain.Comment{{ID: project + "-comment", User: data.Viewer, Body: "Full comment", CreatedAt: at}}}}
			data.InitiativeUpdates[initiative] = []domain.InitiativeUpdate{{ID: initiative + "-update", InitiativeID: initiative, User: data.Viewer, Body: "Initiative update", CreatedAt: at}}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			events := []domain.RealtimeEvent{
				{Type: "project.updated", AggregateID: project},
				{Type: "project.update_created", AggregateID: project},
				{Type: "project.updated", AggregateID: hidden},
				{Type: "project.updated", AggregateID: project + "-old"},
				{Type: "project.deleted", AggregateID: "missing"},
				{Type: "initiative.updated", AggregateID: initiative},
				{Type: "initiative.update_created", AggregateID: initiative},
				{Type: "initiative.deleted", AggregateID: "missing"},
			}
			for index := range events {
				events[index].ID = idgen.New("pulse-event-")
				events[index].CreatedAt = at
				events[index].Payload = json.RawMessage(`{"name":"mutation input","teamIds":["hidden"]}`)
			}
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				r := f.literalRequest(user, key, nil)
				data, err := repo.PagedWorkspaceMetadata(r.Context(), workspace, user.ID)
				if err != nil {
					t.Fatal(err)
				}
				filterBootstrapForAPIKey(&data, r)
				query := store.IssueRecordQuery{}
				if key != nil && apiKeyTeamRestrictionSelected(*key) {
					query.AllowedTeamIDs = append([]string{}, key.TeamIDs...)
				}
				old := &server{store: repo, realtime: newRealtimeHub()}
				bounded := &server{store: f.probe, realtime: newRealtimeHub()}
				for _, event := range events {
					want, wantVisible, wantDecided, wantErr := old.pagedPulseSourceEvent(r, data, event)
					got, visible, decided, err := bounded.pagedPulseSourceEvent(r, data, event)
					if wantErr != nil || err != nil || visible != wantVisible || decided != wantDecided {
						t.Fatalf("reload=%v event=%s visibility=%v/%v decided=%v/%v errors=%v/%v", reload, event.Type, wantVisible, visible, wantDecided, decided, wantErr, err)
					}
					if !reflect.DeepEqual(scaleP21M2JSON(t, want, false), scaleP21M2JSON(t, got, false)) {
						t.Fatalf("legacy realtime changed: %s", scaleP21M2Diff(want, got))
					}
					a, b := &EventSource{event: event}, &EventSource{event: event}
					errA := old.buildPulseEventSource(r.Context(), workspace, a)
					errB := bounded.buildPulseEventSource(r.Context(), workspace, b)
					if errA != nil || errB != nil {
						t.Fatalf("source build errors=%v/%v", errA, errB)
					}
					if b.bytes == 0 || b.bytes > eventSourceCacheBytes {
						t.Fatal("owned event source did not account for its complete rule graph", b.bytes)
					}
					want, wantVisible = a.pulseProjection(data, query)
					got, visible = b.pulseProjection(data, query)
					if visible != wantVisible || !reflect.DeepEqual(scaleP21M2JSON(t, want, false), scaleP21M2JSON(t, got, false)) {
						t.Fatalf("owned source projection changed: %s", scaleP21M2Diff(want, got))
					}
					if f.probe.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("event source retained temporary read admission")
					}
				}
			})
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		service := &server{store: f.probe, realtime: newRealtimeHub()}
		event := domain.RealtimeEvent{Type: "project.updated", AggregateID: project}
		err = service.buildPulseEventSource(ctx, workspace, &EventSource{event: event})
		if !errors.Is(err, context.Canceled) {
			t.Fatal("shared source swallowed cancellation", err)
		}
		r := f.literalRequest(f.data.Viewer, nil, nil).WithContext(ctx)
		_, _, decided, err := service.pagedPulseSourceEvent(r, domain.Bootstrap{}, event)
		if !decided || !errors.Is(err, context.Canceled) {
			t.Fatal("legacy event projection swallowed cancellation", err)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 || f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("event reads decoded full metadata or leaked work")
		}
	}
}
