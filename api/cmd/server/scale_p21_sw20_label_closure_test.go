package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW20TeamLabelAggregateClosure(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		baseline, ok := repo.WorkspaceMetadata(workspace)
		if !ok {
			t.Fatal("missing workspace")
		}
		issues, err := repo.QueryIssueRecords(t.Context(), store.IssueRecordQuery{Workspace: workspace, Limit: 1})
		if err != nil || len(issues.Items) != 1 {
			t.Fatal("missing owned issue fixture", err)
		}
		seedFields := []string{"providerJobs", "users", "teams", "customers", "states", "labels", "cycles", "projects", "teamResourceSections", "teamPinnedResources"}
		type collision struct {
			name, prefix string
			kinds        []string
			aggregate    string
		}
		for _, spec := range []collision{
			{"provider-job", "sw20-imported-label-", []string{"providerJobs"}, "providerJobs"},
			{"user", "sw20-imported-label-", []string{"users"}, "users"},
			{"team", "sw20-imported-label-", []string{"teams"}, "teams"},
			{"customer", "sw20-imported-label-", []string{"customers"}, "customers"},
			{"state", "sw20-imported-label-", []string{"states"}, "states"},
			{"predecessor-order", "sw20-imported-label-", []string{"providerJobs", "users", "teams", "customers", "states"}, "providerJobs"},
			{"core-workspace", "", nil, "workspace"},
			{"core-viewer", "", nil, "users"},
			{"label-prefix", "label_sw20_", []string{"providerJobs", "users", "teams", "customers", "states"}, "labels"},
			{"label-prefix-large-hidden-predecessor", "label_sw20_", []string{"providerJobs"}, "labels"},
			{"user-prefix", "usr_sw20_", []string{"providerJobs", "users", "teams", "customers", "states"}, "users"},
			{"team-prefix", "team_sw20_", []string{"providerJobs", "users", "teams", "customers", "states"}, "teams"},
			{"state-prefix", "state_sw20_", []string{"providerJobs", "users", "teams", "customers", "states"}, "states"},
			{"project-prefix", "project_sw20_", []string{"providerJobs", "users", "teams", "customers", "states", "projects"}, "projects"},
			{"project-prefix-stale-lead", "project_sw20_", []string{"projects"}, "projects"},
			{"cycle-prefix", "cycle_sw20_", []string{"providerJobs", "users", "teams", "customers", "states", "cycles"}, "cycles"},
			{"resource-section-prefix", "team_resource_section_sw20_", []string{"providerJobs", "users", "teams", "customers", "states", "teamResourceSections"}, "teamResourceSections"},
			{"resource-prefix", "team_resource_sw20_", []string{"providerJobs", "users", "teams", "customers", "states", "teamPinnedResources"}, "teamPinnedResources"},
			{"missing-resource-prefix", "team_resource_sw20_", []string{"providerJobs", "users", "teams", "customers", "states"}, ""},
			{"missing-project-prefix", "project_sw20_", []string{"users"}, "users"},
			{"missing-cycle-prefix", "cycle_sw20_", []string{"customers"}, "customers"},
			{"issue-prefix-metadata-only", "issue_sw20_", []string{"states"}, "states"},
		} {
			t.Run(spec.name, func(t *testing.T) {
				id := idgen.New(spec.prefix)
				if spec.name == "core-workspace" {
					id = f.data.Workspace.ID
				}
				if spec.name == "core-viewer" {
					id = f.data.Viewer.ID
				}
				if spec.name == "issue-prefix-metadata-only" {
					id = issues.Items[0].ID
				}
				label := domain.IssueLabel{ID: id, Scope: f.public, ResourceType: "issue", Name: "Before", Color: "#112233", IssueCount: 73, CreatedAt: stamp}
				reset := func() {
					t.Helper()
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					f.probe.SetWebhookSink(nil)
					f.probe.SetRealtimeSink(nil)
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), seedFields...), workspace, "issue_label.updated", "", nil, func(data *domain.Bootstrap) error {
						data.ProviderJobs = slices.Clone(baseline.ProviderJobs)
						data.Users, data.Teams = slices.Clone(baseline.Users), slices.Clone(baseline.Teams)
						data.Customers, data.States = slices.Clone(baseline.Customers), slices.Clone(baseline.States)
						data.Labels, data.Cycles = append(slices.Clone(baseline.Labels), label), slices.Clone(baseline.Cycles)
						data.Projects = slices.Clone(baseline.Projects)
						data.TeamResourceSections = slices.Clone(baseline.TeamResourceSections)
						data.TeamPinnedResources = slices.Clone(baseline.TeamPinnedResources)
						for _, kind := range spec.kinds {
							switch kind {
							case "providerJobs":
								job := domain.ProviderJob{ID: id, Provider: "sw20-provider", UserID: "hidden-actor", Status: "running", UpdatedAt: stamp}
								if spec.name == "label-prefix-large-hidden-predecessor" {
									job.URL = strings.Repeat("large hidden value ", 20000)
								}
								data.ProviderJobs = append(data.ProviderJobs, job)
							case "users":
								data.Users = append(data.Users, domain.User{ID: id, Name: "Imported user", DisplayName: "Imported display", Username: "preserved", Active: true, AppScopes: []string{"read"}, AppTeamIDs: []string{"hidden-team"}})
							case "teams":
								data.Teams = append(data.Teams, domain.Team{ID: id, Key: "IMPORTED", Name: "Imported team", Private: true, CreatedAt: &stamp})
							case "customers":
								data.Customers = append(data.Customers, domain.Customer{ID: id, Name: "Imported customer", Domains: []string{"sw20.example.test"}, OwnerID: "hidden-owner", ExternalIDs: []string{"preserved-id"}, CreatedAt: stamp, UpdatedAt: stamp})
							case "states":
								data.States = append(data.States, domain.WorkflowState{ID: id, TeamID: "hidden-team", Name: "Imported state", Type: "started", Position: 37})
							case "projects":
								lead := f.data.Viewer
								if spec.name == "project-prefix-stale-lead" {
									lead = f.principals[1].user
									lead.Name, lead.DisplayName = "Stored stale name", "Stored stale display"
								}
								data.Projects = append(data.Projects, domain.Project{ID: id, Name: "Imported project", TeamIDs: []string{"hidden-team"}, Lead: &lead, IssueCount: 73, Description: "Complete aggregate", CreatedAt: stamp, UpdatedAt: stamp})
							case "cycles":
								data.Cycles = append(data.Cycles, domain.Cycle{ID: id, Name: "Imported cycle", TeamID: "hidden-team"})
							case "teamResourceSections":
								data.TeamResourceSections = append(data.TeamResourceSections, domain.TeamResourceSection{ID: id, TeamID: "hidden-team", Name: "Imported section"})
							case "teamPinnedResources":
								data.TeamPinnedResources = append(data.TeamPinnedResources, domain.TeamPinnedResource{ID: id, TeamID: "hidden-team", Title: "Imported pinned resource"})
							}
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset()
					keys := []store.RecordKey{{Kind: "labels", ID: id}}
					before := map[string][32]byte{}
					for _, kind := range spec.kinds {
						key := store.RecordKey{Kind: kind, ID: id}
						if spec.name != "label-prefix-large-hidden-predecessor" {
							keys = append(keys, key)
						}
						before[kind] = scaleP21SW6RecordHash(t, repo, workspace, kind, id)
					}
					var events []domain.DomainEvent
					var realtime []domain.RealtimeEvent
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
					r, w := f.request(f.data.Viewer, nil, map[string]any{"name": "After", "description": "New", "color": "#334455"}), httptest.NewRecorder()
					r.SetPathValue("id", f.public)
					r.SetPathValue("labelId", id)
					scaleP21M3Call(&server{store: source}, w, r, (*server).updateTeamLabel)
					if w.Code != http.StatusOK || len(events) != 1 || len(realtime) != 1 {
						t.Fatal("label edit status/publication", w.Code, len(events), len(realtime), w.Body.String())
					}
					for _, kind := range spec.kinds {
						if before[kind] != scaleP21SW6RecordHash(t, repo, workspace, kind, id) {
							t.Fatal("enrichment changed read-only collision row", kind)
						}
					}
					var payload map[string]any
					if err := json.Unmarshal(realtime[0].Payload, &payload); err != nil {
						t.Fatal(err)
					}
					if spec.aggregate == "" && payload["entity"] != nil {
						t.Fatal("special missing resource prefix gained a fallback entity")
					}
					if spec.name == "issue-prefix-metadata-only" {
						entity, _ := payload["entity"].(map[string]any)
						if entity["name"] != "Imported state" || entity["title"] != nil {
							t.Fatal("metadata-only label writer hydrated an issue aggregate")
						}
					}
					outcomes = append(outcomes, []any{w.Code, scaleP21SW10Response(t, w, false), scaleP21SW20Rows(t, repo, workspace, keys...), scaleP21SW20Effects(events, realtime)})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("label aggregate closure differs: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("label closure loaded full metadata")
		}
	}
}

func TestScaleP21SW20TeamLabelProjectReferences(t *testing.T) {
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
		id, inactiveID := idgen.New("project_sw20_refs_"), idgen.New("usr_sw20_inactive_")
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		f.matrix(t, func(t *testing.T, principal domain.User, key *domain.APIKey) {
			for _, variant := range []string{"actor-lead", "foreign-lead", "missing-lead", "inactive-lead"} {
				t.Run(variant, func(t *testing.T) {
					actor := principal
					actor.Name, actor.DisplayName = principal.Name+" request", principal.DisplayName+" request"
					lead := principal
					switch variant {
					case "foreign-lead":
						lead = f.principals[1].user
						if lead.ID == principal.ID {
							lead = f.data.Viewer
						}
					case "missing-lead":
						lead = domain.User{ID: id + "-missing-user", Active: true}
					case "inactive-lead":
						lead = domain.User{ID: inactiveID, Active: false}
					}
					lead.Name, lead.DisplayName, lead.Email = "Stale lead name", "Stale lead display", "stale@example.test"
					creator := f.data.Viewer
					creator.Name, creator.DisplayName = "Stale creator name", "Stale creator display"
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						f.probe.SetWebhookSink(nil)
						f.probe.SetRealtimeSink(nil)
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "users", "labels", "projects"), workspace, "issue_label.updated", "", nil, func(data *domain.Bootstrap) error {
							data.Users = append(slices.Clone(baseline.Users), domain.User{ID: inactiveID, Name: "Current inactive", DisplayName: "Current inactive", Email: "inactive@example.test", Active: false})
							data.Labels = append(slices.Clone(baseline.Labels), domain.IssueLabel{ID: id, Scope: f.public, Name: "Before", Color: "#112233", ResourceType: "issue", CreatedAt: stamp})
							data.Projects = append(slices.Clone(baseline.Projects), domain.Project{ID: id, Name: "Selected project", Lead: &lead, Creator: &creator, TeamIDs: []string{"hidden-team"}, CreatedAt: stamp, UpdatedAt: stamp})
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						projectBefore := scaleP21SW6RecordHash(t, repo, workspace, "projects", id)
						var events []domain.DomainEvent
						var realtime []domain.RealtimeEvent
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
						r, w := f.request(actor, key, map[string]any{"name": "After"}), httptest.NewRecorder()
						r.SetPathValue("id", f.public)
						r.SetPathValue("labelId", id)
						scaleP21M3Call(&server{store: source}, w, r, (*server).updateTeamLabel)
						if w.Code != http.StatusOK || len(events) != 1 || len(realtime) != 1 {
							t.Fatal("reference-aware label edit", w.Code, w.Body.String())
						}
						if scaleP21SW6RecordHash(t, repo, workspace, "projects", id) != projectBefore {
							t.Fatal("event-only display refresh persisted project fields")
						}
						outcomes = append(outcomes, []any{
							w.Code, scaleP21SW10Response(t, w, false),
							scaleP21SW20Rows(t, repo, workspace, store.RecordKey{Kind: "labels", ID: id}, store.RecordKey{Kind: "projects", ID: id}, store.RecordKey{Kind: "users", ID: principal.ID}),
							scaleP21SW20Effects(events, realtime),
						})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("project reference differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("project aggregate reference resolution decoded full metadata")
		}
	}
}
