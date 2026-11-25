package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21ResourceRelationListDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("relation-list-")
		target, other, hidden := prefix+"target", prefix+"other", prefix+"hidden"
		at := time.Date(2026, 11, 14, 1, 2, 3, 4, time.UTC)
		private := ""
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.relation_list_fixture", "", nil, func(data *domain.Bootstrap) error {
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			data.Projects = []domain.Project{
				{ID: target, Name: "Public target", SlugID: prefix + "alias", TeamIDs: []string{f.public}, Description: strings.Repeat("Unrelated project body ", 20000)},
				{ID: other, Name: "Other public project", TeamIDs: []string{f.public}},
				{ID: hidden, Name: "Hidden project", TeamIDs: []string{private}},
				{ID: prefix + "unscoped", Name: "Unscoped project"},
			}
			data.Initiatives = []domain.Initiative{
				{ID: target, Name: "Public initiative", SlugID: prefix + "alias", LeadTeamID: f.public, ProjectIDs: []string{target}},
				{ID: other, Name: "Other initiative", ProjectIDs: []string{other}, Description: strings.Repeat("Unrelated initiative body ", 20000)},
				{ID: hidden, Name: "Hidden initiative", LeadTeamID: private, ProjectIDs: []string{hidden}},
				{ID: prefix + "unscoped", Name: "Workspace initiative"},
			}
			data.ProjectRelations, data.InitiativeRelations = []domain.ProjectRelation{}, []domain.InitiativeRelation{}
			for index := range 65 {
				from, to := target, other
				if index%2 == 1 {
					from, to = to, from
				}
				id := fmt.Sprintf("%sedge-%03d", prefix, index)
				if index == 3 || index == 61 {
					id = prefix + "duplicate"
				}
				created := at.Add(time.Duration(index%4) * time.Minute)
				data.ProjectRelations = append(data.ProjectRelations, domain.ProjectRelation{
					ID: id, ProjectID: from, RelatedProjectID: to, Type: "blocks", ProjectMilestoneID: "source\x00milestone",
					RelatedProjectMilestoneID: "other milestone", CreatedAt: created, UpdatedAt: at,
				})
				data.InitiativeRelations = append(data.InitiativeRelations, domain.InitiativeRelation{
					ID: id, InitiativeID: from, RelatedInitiativeID: to, Type: "related", SortOrder: float64(index % 4), CreatedAt: created, UpdatedAt: at,
				})
			}
			for _, endpoint := range []string{hidden, prefix + "unscoped", prefix + "dangling", ""} {
				data.ProjectRelations = append(data.ProjectRelations, domain.ProjectRelation{ID: prefix + "edge-" + endpoint, ProjectID: target, RelatedProjectID: endpoint, Type: "related", CreatedAt: at, UpdatedAt: at})
				data.InitiativeRelations = append(data.InitiativeRelations, domain.InitiativeRelation{ID: prefix + "edge-" + endpoint, InitiativeID: endpoint, RelatedInitiativeID: target, Type: "parent", SortOrder: -1, CreatedAt: at, UpdatedAt: at})
			}
			data.Documents = []domain.Document{{ID: prefix + "document", Content: strings.Repeat("Unrelated document ", 20000)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		type requestCase struct {
			name, workspace string
			user            domain.User
			key             *domain.APIKey
			dev, handoff    bool
			canceled, gate  bool
		}
		var cases []requestCase
		for _, principal := range f.principals {
			for _, credential := range []struct {
				name string
				key  *domain.APIKey
			}{
				{"session", nil},
				{"unrestricted", &domain.APIKey{ID: prefix + "all", Scopes: []string{"read"}}},
				{"selected-empty", &domain.APIKey{ID: prefix + "empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
				{"selected-set", &domain.APIKey{ID: prefix + "selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
				{"app-read", &domain.APIKey{ID: prefix + "app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
				{"app-initiative", &domain.APIKey{ID: prefix + "app-init", Actor: "app", Scopes: []string{"read", "initiative:read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
			} {
				cases = append(cases, requestCase{name: principal.name + "/" + credential.name, workspace: workspace, user: principal.user, key: credential.key})
			}
		}
		cases = append(cases,
			requestCase{name: "dev", workspace: workspace, dev: true},
			requestCase{name: "dev-default", dev: true},
			requestCase{name: "dev-canceled", workspace: workspace, dev: true, canceled: true},
			requestCase{name: "dev-key-bypass", workspace: workspace, dev: true, key: &domain.APIKey{Actor: "app", Scopes: []string{}, TeamRestriction: "selected", TeamIDs: []string{}}},
			requestCase{name: "ignored-handoff", workspace: workspace, user: f.data.Viewer, handoff: true},
			requestCase{name: "ignored-gate", workspace: workspace, user: f.data.Viewer, gate: true},
			requestCase{name: "canceled", workspace: workspace, user: f.data.Viewer, canceled: true},
			requestCase{name: "missing", workspace: prefix + "missing", user: f.data.Viewer},
			requestCase{name: "missing-dev", workspace: prefix + "missing", dev: true},
			requestCase{name: "missing-principal", workspace: workspace},
			requestCase{name: "default-workspace", user: f.data.Viewer},
		)
		comparisons := 0
		for _, reload := range []bool{false, true} {
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			for _, test := range cases {
				for _, kind := range []string{"projects", "initiatives"} {
					for _, ref := range []string{target, prefix + "alias", prefix + "missing", ""} {
						for _, query := range []string{"", "limit=2&cursor=1", "limit=201&cursor=-9", "limit=0&cursor=9999"} {
							serve := func(selected *store.SQLiteStore) *scaleP21PublicResourceRecordWriter {
								service := &server{store: selected, authDisabled: test.dev}
								r := f.literalRequest(test.user, test.key, nil)
								r.Method, r.URL.Path, r.URL.RawQuery = http.MethodGet, "/api/"+kind+"/"+ref+"/relations", query
								r.SetPathValue("id", ref)
								r.Header.Set("X-Workspace-Key", test.workspace)
								r.SetPathValue("workspaceKey", test.workspace)
								r.SetPathValue("workspace", test.workspace)
								if test.handoff {
									handOffPagedMetadata(r, workspace, test.user.ID, domain.Bootstrap{
										Workspace:           f.data.Workspace,
										ProjectRelations:    []domain.ProjectRelation{{ID: "wrong handoff", ProjectID: ref}},
										InitiativeRelations: []domain.InitiativeRelation{{ID: "wrong handoff", InitiativeID: ref}},
									})
								}
								if test.canceled {
									ctx, cancel := context.WithCancel(r.Context())
									cancel()
									r = r.WithContext(ctx)
								}
								if test.gate {
									r = r.WithContext(context.WithValue(r.Context(), projectionHoldKey{}, &projectionHold{gate: &metadataReadGate{}, retryAfter: time.Second}))
								}
								w := &scaleP21PublicResourceRecordWriter{ResponseRecorder: httptest.NewRecorder(), repo: selected}
								if kind == "projects" {
									service.listProjectRelations(w, r)
								} else {
									service.listInitiativeRelations(w, r)
								}
								return w
							}
							want, got := serve(repo), serve(f.probe)
							if want.Code != got.Code || want.Header().Get("Content-Type") != got.Header().Get("Content-Type") || want.Body.String() != got.Body.String() {
								t.Fatalf("relations reload=%v principal=%s kind=%s ref=%q query=%q\nlegacy=%d %s\nbounded=%d %s",
									reload, test.name, kind, ref, query, want.Code, want.Body.String(), got.Code, got.Body.String())
							}
							if !test.canceled && test.user.ID != "" && test.workspace != prefix+"missing" && got.held == 0 {
								t.Fatal("relation output was released before encoding")
							}
							if test.name == "guest-explicit/session" && kind == "initiatives" && ref == target && query == "" {
								var value struct {
									Total int `json:"total"`
								}
								if err := json.Unmarshal(got.Body.Bytes(), &value); err != nil || value.Total == 0 {
									t.Fatal("guest pre-clear initiative relations disappeared", err)
								}
							}
							if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
								t.Fatal("relation handler leaked ownership or decoded full metadata")
							}
							comparisons++
						}
					}
				}
			}
		}
		t.Logf("exact relation list comparisons=%d", comparisons)
	}
}

func TestScaleP21ResourceRelationListBorrowedSQLPoolOne(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := scaleP03DatabaseConfig(t)
	config.MaxOpenConns = 1
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("relation-http-snapshot-")
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.relation_http_snapshot", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = []domain.Project{
			{ID: prefix + "target", Name: "Target", TeamIDs: []string{data.Teams[0].ID}},
			{ID: prefix + "other", Name: "Other", TeamIDs: []string{data.Teams[0].ID}},
		}
		data.Initiatives = []domain.Initiative{
			{ID: prefix + "target", Name: "Target", ProjectIDs: []string{prefix + "target"}},
			{ID: prefix + "other", Name: "Other", ProjectIDs: []string{prefix + "other"}},
		}
		data.ProjectRelations = []domain.ProjectRelation{{ID: prefix + "project-edge", ProjectID: prefix + "target", RelatedProjectID: prefix + "other"}}
		data.InitiativeRelations = []domain.InitiativeRelation{{ID: prefix + "initiative-edge", InitiativeID: prefix + "target", RelatedInitiativeID: prefix + "other", Type: "related"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	snapshot, release, err := repo.BeginWorkspaceRead(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, kind := range []string{"projects", "initiatives"} {
		serve := func(selected *store.SQLiteStore) *scaleP21PublicResourceRecordWriter {
			service := &server{store: selected}
			r := httptest.NewRequest(http.MethodGet, "/api/"+kind+"/"+prefix+"target/relations", nil)
			r.Header.Set("X-Workspace-Key", workspace)
			r.SetPathValue("workspaceKey", workspace)
			r.SetPathValue("workspace", workspace)
			r.SetPathValue("id", prefix+"target")
			r = r.WithContext(context.WithValue(snapshot, authUserContextKey{}, f.data.Viewer))
			w := &scaleP21PublicResourceRecordWriter{ResponseRecorder: httptest.NewRecorder(), repo: selected}
			if kind == "projects" {
				service.listProjectRelations(w, r)
			} else {
				service.listInitiativeRelations(w, r)
			}
			return w
		}
		want, got := serve(repo), serve(view)
		if want.Code != http.StatusOK || got.Code != want.Code || want.Body.String() != got.Body.String() {
			t.Fatal("borrowed SQL pool-one handler output differs", kind, want.Code, got.Code, want.Body.String(), got.Body.String())
		}
		if !strings.Contains(got.Body.String(), prefix) || got.held == 0 || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("borrowed SQL handler omitted result or leaked ownership", kind)
		}
	}
}
