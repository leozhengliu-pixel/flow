package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW20ImportedTeamAggregateCollision(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		targetID := idgen.New("sw20-imported-")
		stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for _, providerJob := range []bool{false, true} {
			var outcomes []any
			for _, current := range []*store.SQLiteStore{repo, f.probe} {
				repo.SetWebhookSink(nil)
				repo.SetRealtimeSink(nil)
				f.probe.SetWebhookSink(nil)
				f.probe.SetRealtimeSink(nil)
				if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
					t.Fatal(err)
				}
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "documents", "users", "providerJobs"), f.data.Workspace.URLKey, "scale_p21_sw20.reset", "", nil, func(data *domain.Bootstrap) error {
					data.Teams = []domain.Team{{ID: targetID, Key: "IMPORTED", Name: "Imported", ArchivedAt: &stamp, UpdatedAt: &stamp}}
					data.Users = append(slices.DeleteFunc(data.Users, func(user domain.User) bool { return user.ID == targetID }), domain.User{
						ID: targetID, Name: "Earlier imported user", DisplayName: "Earlier imported user", Active: true,
						Username: "retained", UserID: "external-user", AppScopes: []string{"read"}, AppTeamIDs: []string{"retained-private-reference"}, OutOfOfficeUntil: &stamp,
					})
					data.ProviderJobs = nil
					if providerJob {
						data.ProviderJobs = []domain.ProviderJob{{ID: targetID, Provider: "collision", Status: "retained", UpdatedAt: stamp}}
					}
					data.Documents = []domain.Document{{ID: idgen.New("doc_sw20_imported_"), Title: "Earlier collision", ContentData: map[string]any{"id": targetID, "retained": "imported team collision"}, Creator: f.data.Viewer}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				var event domain.DomainEvent
				var realtime domain.RealtimeEvent
				current.SetWebhookSink(func(_ string, value domain.DomainEvent) { event = value })
				current.SetRealtimeSink(func(_ string, value domain.RealtimeEvent) { realtime = value })
				beforeUser := scaleP21SW6RecordHash(t, repo, f.data.Workspace.URLKey, "users", targetID)
				beforeSettings := scaleP21SW20Rows(t, repo, f.data.Workspace.URLKey, store.RecordKey{Kind: "userSettings", ID: targetID})
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.SetPathValue("teamId", targetID)
				(&server{store: current}).restoreDeletedTeam(w, r)
				if w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
				if scaleP21SW6RecordHash(t, repo, f.data.Workspace.URLKey, "users", targetID) != beforeUser ||
					!reflect.DeepEqual(beforeSettings, scaleP21SW20Rows(t, repo, f.data.Workspace.URLKey, store.RecordKey{Kind: "userSettings", ID: targetID})) {
					t.Fatal("aggregate read changed user or virtual settings defaults")
				}
				var payload, previous any
				if err := json.Unmarshal(realtime.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if len(event.PreviousValues) > 0 {
					if err := json.Unmarshal(event.PreviousValues, &previous); err != nil {
						t.Fatal(err)
					}
				}
				if object, ok := payload.(map[string]any); ok {
					if entity, ok := object["entity"].(map[string]any); ok {
						delete(entity, "updatedAt")
					}
				}
				outcomes = append(outcomes, []any{payload, previous})
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("imported team aggregate differs providerJob=%t: %s", providerJob, scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
		}
	}
}

func TestScaleP21SW20RestoreTeamTypedPrefixes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		baseline, ok := repo.WorkspaceMetadata(workspace)
		if !ok {
			t.Fatal("missing workspace")
		}
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		fields := []string{"teams", "users", "providerJobs", "states", "labels", "projects", "cycles", "teamResourceSections", "teamPinnedResources"}
		for _, spec := range []struct{ prefix, kind string }{
			{"team_sw20_restore_", ""},
			{"usr_sw20_restore_", "users"},
			{"state_sw20_restore_", "states"},
			{"label_sw20_restore_", "labels"},
			{"project_sw20_restore_", "projects"},
			{"cycle_sw20_restore_", "cycles"},
			{"team_resource_section_sw20_restore_", "teamResourceSections"},
			{"team_resource_sw20_restore_", "teamPinnedResources"},
			{"team_resource_sw20_missing_", ""},
			{"issue_sw20_restore_", ""},
		} {
			t.Run(spec.prefix, func(t *testing.T) {
				id := idgen.New(spec.prefix)
				f.matrix(t, func(t *testing.T, actor domain.User, key *domain.APIKey) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						f.probe.SetWebhookSink(nil)
						f.probe.SetRealtimeSink(nil)
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), fields...), workspace, "team.unarchived", "", nil, func(data *domain.Bootstrap) error {
							data.Teams = append(slices.Clone(baseline.Teams), domain.Team{ID: id, Name: "Imported restore", Key: "IMPORTED", ArchivedAt: &stamp, UpdatedAt: &stamp})
							data.Users = slices.Clone(baseline.Users)
							data.ProviderJobs = nil
							data.States, data.Labels = slices.Clone(baseline.States), slices.Clone(baseline.Labels)
							data.Projects, data.Cycles = slices.Clone(baseline.Projects), slices.Clone(baseline.Cycles)
							data.TeamResourceSections = slices.Clone(baseline.TeamResourceSections)
							data.TeamPinnedResources = slices.Clone(baseline.TeamPinnedResources)
							switch spec.kind {
							case "users":
								data.Users = append(data.Users, domain.User{ID: id, Name: "Collision user", DisplayName: "Collision user", AppScopes: []string{"read"}, AppTeamIDs: []string{"hidden-team"}, Active: true})
							case "states":
								data.States = append(data.States, domain.WorkflowState{ID: id, TeamID: "hidden-team", Name: "Collision state", Type: "started"})
							case "labels":
								data.Labels = append(data.Labels, domain.IssueLabel{ID: id, Name: "Collision label", Scope: "hidden-team", IssueCount: 77, CreatedAt: stamp})
							case "projects":
								lead, creator := actor, f.principals[1].user
								lead.Name, lead.DisplayName = "Stale lead", "Stale lead"
								creator.Name, creator.DisplayName = "Stale creator", "Stale creator"
								data.Projects = append(data.Projects, domain.Project{ID: id, Name: "Collision project", Lead: &lead, Creator: &creator, TeamIDs: []string{"hidden-team"}, CreatedAt: stamp, UpdatedAt: stamp})
							case "cycles":
								data.Cycles = append(data.Cycles, domain.Cycle{ID: id, Name: "Collision cycle", TeamID: "hidden-team", Resources: []domain.CycleResource{{ID: id + "-nested", Title: "Nested", DocumentID: "hidden-doc"}}})
							case "teamResourceSections":
								data.TeamResourceSections = append(data.TeamResourceSections, domain.TeamResourceSection{ID: id, Name: "Collision section", TeamID: "hidden-team"})
							case "teamPinnedResources":
								data.TeamPinnedResources = append(data.TeamPinnedResources, domain.TeamPinnedResource{ID: id, Title: "Collision resource", TeamID: "hidden-team", ResourceID: "hidden-document", ResourceType: "document"})
							}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						collisionBefore := [32]byte{}
						if spec.kind != "" {
							collisionBefore = scaleP21SW6RecordHash(t, repo, workspace, spec.kind, id)
						}
						var events []domain.DomainEvent
						var realtime []domain.RealtimeEvent
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
						r, w := f.request(actor, key, nil), httptest.NewRecorder()
						r.SetPathValue("teamId", id)
						started := time.Now().UTC()
						scaleP21M3Call(&server{store: source}, w, r, (*server).restoreDeletedTeam)
						if w.Code != http.StatusOK || len(events) != 1 || len(realtime) != 1 {
							t.Fatal("typed team restore", w.Code, len(events), len(realtime), w.Body.String())
						}
						if spec.kind != "" && collisionBefore != scaleP21SW6RecordHash(t, repo, workspace, spec.kind, id) {
							t.Fatal("typed aggregate enrichment persisted read-only row", spec.kind)
						}
						var response map[string]any
						if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
						generated := map[string]string{response["updatedAt"].(string): "<clock>"}
						audits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started, ResourceTypes: []string{"team"}, ResourceID: id})
						if err != nil || len(audits) != 1 {
							t.Fatal("typed team audit", err, len(audits))
						}
						generated[audits[0].ID], generated[audits[0].CreatedAt.Format(time.RFC3339Nano)] = "<audit>", "<clock>"
						audits[0].Hash, audits[0].Seq = "", 0
						outcomes = append(outcomes, scaleP21SW6GeneratedJSON(t, []any{
							response,
							scaleP21SW20Rows(t, repo, workspace, store.RecordKey{Kind: "teams", ID: id}, store.RecordKey{Kind: "users", ID: actor.ID}),
							audits, scaleP21SW20Effects(events, realtime),
						}, generated))
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("typed restore differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("typed team restore loaded complete metadata")
		}
	}
}
