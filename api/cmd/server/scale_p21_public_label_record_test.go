package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21PublicLabelRecordDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("label-record-")
		at := time.Date(2025, 3, 4, 5, 6, 7, 8, time.UTC)
		private := ""
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.label_record_fixture", "", nil, func(data *domain.Bootstrap) error {
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			data.LabelGroups = []domain.LabelGroup{{ID: prefix + "public-group", Scope: f.public}, {ID: prefix + "private-group", Scope: private}}
			data.Labels = []domain.IssueLabel{
				{ID: prefix + "duplicate", Name: "First\x00label", Scope: f.public, GroupID: prefix + "private-group", ResourceType: "project", Description: strings.Repeat("Complete description ", 2000), IssueCount: 91, CreatedAt: at},
				{ID: prefix + "duplicate", Name: "Later duplicate", Scope: f.public, GroupID: prefix + "public-group", Description: "Later body", CreatedAt: at},
				{ID: prefix + "private", Name: "Private label", Scope: private, CreatedAt: at},
				{ID: prefix + "workspace", Name: "Workspace label", Scope: " WorkSpace ", IssueCount: 92, CreatedAt: at},
				{ID: prefix + "archived", Name: "Archived project label", Scope: f.public, ResourceType: "project", ArchivedAt: &at, CreatedAt: at},
				{ID: prefix + "hidden-first", Name: "Hidden first occurrence", Scope: private, CreatedAt: at},
				{ID: prefix + "hidden-first", Name: "Visible later occurrence", Scope: f.public, CreatedAt: at},
			}
			data.Projects = []domain.Project{
				{ID: prefix + "public-project", Name: "Public project", TeamIDs: []string{f.public}, LabelIDs: []string{prefix + "duplicate", prefix + "duplicate", prefix + "workspace"}, Description: strings.Repeat("Unrelated project body ", 20000)},
				{ID: prefix + "private-project", Name: "Private project", TeamIDs: []string{private}, LabelIDs: []string{prefix + "duplicate", prefix + "workspace"}},
				{ID: prefix + "public-project", Name: "Duplicate project occurrence", TeamIDs: []string{f.public}, LabelIDs: []string{prefix + "duplicate"}},
			}
			data.Initiatives = []domain.Initiative{
				{ID: prefix + "initiative", Name: "Public initiative", ProjectIDs: []string{prefix + "public-project"}, LabelIDs: []string{prefix + "duplicate", prefix + "workspace"}},
				{ID: prefix + "private-initiative", Name: "Private initiative", LeadTeamID: private, ProjectIDs: []string{prefix + "private-project"}, LabelIDs: []string{prefix + "duplicate"}},
			}
			data.Documents = []domain.Document{{ID: prefix + "unrelated", Content: strings.Repeat("Unrelated document body ", 20000)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		type requestCase struct {
			name, workspace string
			user            domain.User
			key             *domain.APIKey
			development     bool
			handoff         bool
			cancelled       bool
			refused         bool
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
				{"app", &domain.APIKey{ID: prefix + "app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
			} {
				cases = append(cases, requestCase{name: principal.name + "/" + credential.name, workspace: workspace, user: principal.user, key: credential.key})
			}
		}
		cases = append(cases,
			requestCase{name: "missing-workspace", workspace: prefix + "missing", user: f.data.Viewer},
			requestCase{name: "missing-binding", user: f.data.Viewer},
			requestCase{name: "projection-refused", workspace: workspace, user: f.data.Viewer, refused: true},
			requestCase{name: "existing-handoff", workspace: workspace, user: f.data.Viewer, handoff: true},
			requestCase{name: "canceled", workspace: workspace, user: f.data.Viewer, cancelled: true},
			requestCase{name: "development", workspace: workspace, user: f.principals[3].user, development: true, key: &domain.APIKey{Actor: "app", Scopes: []string{}, TeamRestriction: "selected", TeamIDs: []string{}}},
			requestCase{name: "development-default", user: f.data.Viewer, development: true},
			requestCase{name: "development-canceled", workspace: workspace, user: f.data.Viewer, development: true, cancelled: true},
			requestCase{name: "development-missing", workspace: prefix + "missing", user: f.data.Viewer, development: true},
		)
		comparisons := 0
		for _, state := range []string{"warm", "reloaded"} {
			if state == "reloaded" {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			for _, test := range cases {
				for _, ref := range []string{prefix + "duplicate", prefix + "private", prefix + "workspace", prefix + "archived", prefix + "hidden-first", "First\x00label", prefix + "missing"} {
					serve := func(selected *store.SQLiteStore) *scaleP21PublicResourceRecordWriter {
						service := &server{store: selected, authDisabled: test.development}
						r := f.literalRequest(test.user, test.key, nil)
						r.Method, r.URL.Path = http.MethodGet, "/api/issue-labels/record"
						r.URL.RawQuery = "resourceType=issue&includeArchived=false&teamId=unrelated&includeWorkspace=false"
						r.SetPathValue("id", ref)
						r.Header.Set("X-Workspace-Key", test.workspace)
						r.SetPathValue("workspaceKey", test.workspace)
						r.SetPathValue("workspace", test.workspace)
						if test.handoff {
							data, err := repo.PagedWorkspaceMetadata(r.Context(), workspace, test.user.ID)
							if err != nil {
								t.Fatal(err)
							}
							for index := range data.Labels {
								data.Labels[index].Name = "Earlier handoff label"
							}
							handOffPagedMetadata(r, workspace, test.user.ID, data)
						}
						if test.cancelled {
							ctx, cancel := context.WithCancel(r.Context())
							cancel()
							r = r.WithContext(ctx)
						}
						if test.refused {
							r = r.WithContext(context.WithValue(r.Context(), projectionHoldKey{}, &projectionHold{gate: &metadataReadGate{}, retryAfter: time.Second}))
						}
						w := &scaleP21PublicResourceRecordWriter{ResponseRecorder: httptest.NewRecorder(), repo: selected}
						service.getIssueLabelRecord(w, r)
						return w
					}
					want, got := serve(repo), serve(f.probe)
					if got.Code != want.Code || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") || got.Header().Get("Retry-After") != want.Header().Get("Retry-After") || got.Body.String() != want.Body.String() {
						t.Fatalf("label record state=%s case=%s ref=%q\nlegacy=%d %s\nbounded=%d %s", state, test.name, ref, want.Code, want.Body.String(), got.Code, got.Body.String())
					}
					if got.Code == http.StatusOK && got.held == 0 {
						t.Fatal("label output was released before encoding")
					}
					if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("label record leaked work or decoded full metadata")
					}
					comparisons++
				}
			}
		}
		t.Logf("exact label detail comparisons=%d", comparisons)
	}
}
