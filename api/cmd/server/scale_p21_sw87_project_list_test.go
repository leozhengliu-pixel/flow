package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW87ProjectListWriter struct {
	*httptest.ResponseRecorder
	source *store.SQLiteStore
	held   int
}

func (w *scaleP21SW87ProjectListWriter) Write(raw []byte) (int, error) {
	w.held = w.source.MetadataWorkUsedForTesting()
	return w.ResponseRecorder.Write(raw)
}

func TestScaleP21SW87ProjectListDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw87-list-")
		project, customer, label := prefix+"project", prefix+"customer", prefix+"label"
		at := time.Date(2026, 11, 1, 12, 0, 0, 123, time.FixedZone("list-source", 3600))
		target, start := "2099-02-03", "2026-01-01"
		currentSlug, previousSlug := "current-list-000000000087", "previous-list-000000000087"
		private := ""
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw87_project_list", "", nil, func(data *domain.Bootstrap) error {
			for _, item := range data.Projects {
				if item.ID == "m2-unrelated" && len(item.TeamIDs) > 0 {
					private = item.TeamIDs[0]
				}
			}
			if private == "" {
				t.Fatal("private project fixture missing")
			}
			wide := strings.Repeat("excluded project detail ", 50000)
			data.Labels = append(data.Labels, domain.IssueLabel{ID: label, Scope: f.public, Name: "List label"})
			data.Projects = append(data.Projects,
				domain.Project{
					ID: project, Name: "SW87 main project", SlugID: currentSlug, PreviousSlugIDs: []string{previousSlug},
					Summary: "only-summary-needle", Description: wide, Priority: 2, PriorityLabel: "High",
					Status: domain.ProjectStatus{ID: prefix + "status", Name: "List status", Type: "started"},
					Health: "atRisk", TeamIDs: []string{f.public, private}, MemberIDs: []string{f.principals[2].user.ID},
					LabelIDs: []string{label}, DependencyIDs: []string{"m2-unrelated", "missing"}, Initiatives: []string{},
					Customers: []string{customer}, Lead: &data.Viewer, Creator: &data.Viewer, CreatorID: data.Viewer.ID,
					StartDate: &start, TargetDate: &target, CreatedAt: at, UpdatedAt: at, Progress: 0.625, IssueCount: 81,
					Milestones: []domain.ProjectMilestone{{
						ID: prefix + "milestone", ProjectID: project, Name: "SW87 checkpoint", Description: wide,
						TargetDate: &target, CreatedAt: at, UpdatedAt: at,
						DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: wide}},
					}},
					Resources:            []domain.ProjectResource{{ID: prefix + "resource", Title: wide, URL: "https://example.test/list-detail"}},
					Comments:             []domain.Comment{{ID: prefix + "comment", Body: wide, User: data.Viewer, CreatedAt: at}},
					DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: wide}},
				},
				domain.Project{ID: prefix + "second", Name: "SW87 second", TeamIDs: []string{f.public}, MemberIDs: []string{}, Milestones: []domain.ProjectMilestone{}, CreatedAt: at, UpdatedAt: at},
				domain.Project{ID: prefix + "unscoped-nil", Name: "SW87 nil teams", TeamIDs: nil, Milestones: nil, CreatedAt: at, UpdatedAt: at},
				domain.Project{ID: prefix + "unscoped-empty", Name: "SW87 empty teams", TeamIDs: []string{}, Milestones: []domain.ProjectMilestone{}, CreatedAt: at, UpdatedAt: at},
				domain.Project{ID: prefix + "archived", Name: "SW87 archived", TeamIDs: []string{f.public}, ArchivedAt: &at, CreatedAt: at, UpdatedAt: at},
			)
			data.Customers = append(data.Customers, domain.Customer{
				ID: customer, Name: "SW87 customer", OwnerID: data.Viewer.ID, Status: "active", Tier: "enterprise",
				AnnualRevenue: 12345.5, Size: 42, Domains: []string{}, CreatedAt: at, UpdatedAt: at,
			})
			data.CustomerRequests = append(data.CustomerRequests, domain.CustomerRequest{
				ID: prefix + "request", ProjectID: project, CustomerID: customer, Priority: 1, Creator: data.Viewer, CreatedAt: at, UpdatedAt: at,
			})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		type queryCase struct {
			name   string
			values url.Values
		}
		cases := []queryCase{
			{"default", nil},
			{"total-first-page", url.Values{"limit": {"1"}, "includeTotal": {"true"}}},
			{"offset-page", url.Values{"limit": {"1"}, "includeTotal": {"true"}, "cursor": {base64.RawURLEncoding.EncodeToString([]byte(`{"offset":1}`))}}},
			{"bad-cursor", url.Values{"cursor": {"not-a-cursor"}}},
			{"negative-limit", url.Values{"limit": {"-1"}}},
			{"capped-limit", url.Values{"limit": {"101"}}},
			{"name-search", url.Values{"q": {" SW87 MAIN "}}},
			{"summary-search", url.Values{"q": {"only-summary-needle"}}},
			{"slug-search", url.Values{"q": {currentSlug}}},
			{"team-query", url.Values{"teamId": {f.public + "," + private}}},
			{"archived", url.Values{"archived": {"true"}}},
			{"all-archived-states", url.Values{"archived": {"all"}}},
			{"invalid-json-filter", url.Values{"filter": {"{"}}},
		}
		for _, filter := range []struct {
			name, field, operator string
			values                []string
		}{
			{"status", "status", "is", []string{prefix + "status"}},
			{"priority", "priority", "is", []string{"high"}},
			{"lead", "lead", "is", []string{f.data.Viewer.ID}},
			{"members", "members", "is", []string{f.principals[2].user.ID}},
			{"health", "health", "is", []string{"at-risk"}},
			{"dates", "dates", "is", []string{"has-target"}},
			{"milestone-id", "milestones", "is", []string{prefix + "milestone"}},
			{"milestone-name", "milestones", "is", []string{"SW87 checkpoint"}},
			{"labels", "labels", "is", []string{label}},
			{"teams", "teams", "is", []string{f.public}},
			{"project-current-slug", "project", "is", []string{currentSlug}},
			{"project-previous-slug", "project", "is", []string{previousSlug}},
			{"not-project", "project", "isNot", []string{project}},
			{"customer-id", "customers", "is", []string{"customer:" + customer}},
			{"customer-count", "customers", "gte", []string{"customer-count:1"}},
			{"invalid-customer-number", "customers", "gte", []string{"customer-count:nan"}},
			{"unknown-field", "unknown", "is", []string{"x"}},
			{"empty-values", "status", "is", []string{}},
			{"unsupported-operator", "status", "gte", []string{"x"}},
		} {
			raw, err := json.Marshal([]store.ProjectDirectoryFilter{{Field: filter.field, Operator: filter.operator, Values: filter.values}})
			if err != nil {
				t.Fatal(err)
			}
			cases = append(cases, queryCase{filter.name, url.Values{"filter": {string(raw)}, "includeTotal": {"true"}}})
		}
		compare := func(t *testing.T, user domain.User, key *domain.APIKey) {
			t.Helper()
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					var outputs []*scaleP21SW87ProjectListWriter
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						service := &server{store: source}
						r := f.literalRequest(user, key, nil)
						r.Method, r.URL.Path, r.URL.RawQuery = http.MethodGet, "/api/projects", test.values.Encode()
						output := &scaleP21SW87ProjectListWriter{ResponseRecorder: httptest.NewRecorder(), source: source}
						scaleP21CallWithOutputOwner(service, output, r, func(s *server, w http.ResponseWriter, r *http.Request) {
							s.listProjectRecords(w, r)
						})
						outputs = append(outputs, output)
						if source.BoundedMetadataEnabled() && output.Code == http.StatusOK && output.held == 0 {
							t.Fatal("bounded project list wrote a successful page without retaining its read")
						}
						if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("bounded project list retained work after response completion")
						}
					}
					want, got := outputs[0], outputs[1]
					if want.Code != got.Code || want.Header().Get("Content-Type") != got.Header().Get("Content-Type") ||
						!reflect.DeepEqual(scaleP21M2JSON(t, json.RawMessage(want.Body.Bytes()), false), scaleP21M2JSON(t, json.RawMessage(got.Body.Bytes()), false)) {
						t.Fatalf("project list changed: status=%d/%d\nold=%s\nbounded=%s", want.Code, got.Code, want.Body, got.Body)
					}
				})
			}
		}
		for _, indexed := range []string{"off", "on"} {
			t.Run("indexed-"+indexed, func(t *testing.T) {
				t.Setenv("FLOW_INDEXED_READS", indexed)
				f.matrix(t, compare)
				for _, credential := range []struct {
					name string
					key  domain.APIKey
				}{
					{"app-unrestricted", domain.APIKey{ID: prefix + "app-all", Actor: "app", TeamRestriction: "all", Scopes: []string{"read"}}},
					{"app-selected", domain.APIKey{ID: prefix + "app-selected", Actor: "app", TeamRestriction: "selected", TeamIDs: []string{f.public}, Scopes: []string{"read", "customer:read"}}},
					{"app-selected-empty", domain.APIKey{ID: prefix + "app-empty", Actor: "app", TeamRestriction: "selected", TeamIDs: []string{}, Scopes: []string{"read", "customer:read"}}},
				} {
					t.Run(credential.name, func(t *testing.T) { compare(t, f.data.Viewer, &credential.key) })
				}
			})
		}
		selected := &domain.APIKey{ID: prefix + "edge-selected", TeamRestriction: "selected", TeamIDs: []string{f.public}, Scopes: []string{"read"}}
		application := &domain.APIKey{ID: prefix + "edge-app", Actor: "app", TeamRestriction: "all", Scopes: []string{"read", "customer:read"}}
		for _, edge := range []struct {
			name, workspace                   string
			key                               *domain.APIKey
			handoff, gate, cancelled, develop bool
		}{
			{name: "selected-handoff", workspace: workspace, key: selected, handoff: true},
			{name: "app-handoff", workspace: workspace, key: application, handoff: true},
			{name: "selected-gate-refused", workspace: workspace, key: selected, gate: true},
			{name: "app-gate-refused", workspace: workspace, key: application, gate: true},
			{name: "missing-workspace-binding", key: selected},
			{name: "missing-workspace", workspace: prefix + "missing", key: selected},
			{name: "cancelled", workspace: workspace, key: selected, cancelled: true},
			{name: "development-selected", workspace: workspace, key: selected, develop: true},
			{name: "development-default", key: application, develop: true},
		} {
			t.Run(edge.name, func(t *testing.T) {
				var outputs []*scaleP21SW87ProjectListWriter
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					service := &server{store: source, authDisabled: edge.develop}
					r := f.literalRequest(f.data.Viewer, edge.key, nil)
					r.Method, r.URL.Path = http.MethodGet, "/api/projects"
					r.Header.Set("X-Workspace-Key", edge.workspace)
					r.SetPathValue("workspaceKey", edge.workspace)
					r.SetPathValue("workspace", edge.workspace)
					if edge.gate {
						r = r.WithContext(context.WithValue(r.Context(), projectionHoldKey{}, &projectionHold{gate: &metadataReadGate{}, retryAfter: time.Second}))
					}
					if edge.handoff {
						data, err := repo.PagedWorkspaceMetadata(r.Context(), workspace, f.data.Viewer.ID)
						if err != nil {
							t.Fatal(err)
						}
						for index := range data.Projects {
							data.Projects[index].Name = "Earlier handed-off list project"
						}
						handOffPagedMetadata(r, workspace, f.data.Viewer.ID, data)
					}
					if edge.cancelled {
						ctx, cancel := context.WithCancel(r.Context())
						cancel()
						r = r.WithContext(ctx)
					}
					output := &scaleP21SW87ProjectListWriter{ResponseRecorder: httptest.NewRecorder(), source: source}
					scaleP21CallWithOutputOwner(service, output, r, func(s *server, w http.ResponseWriter, r *http.Request) {
						s.listProjectRecords(w, r)
					})
					outputs = append(outputs, output)
					if source.BoundedMetadataEnabled() && output.Code == http.StatusOK && output.held == 0 {
						t.Fatal("bounded edge response was written without retaining its read")
					}
					if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("bounded edge response leaked work")
					}
				}
				want, got := outputs[0], outputs[1]
				if want.Code != got.Code || !reflect.DeepEqual(want.Header(), got.Header()) ||
					!reflect.DeepEqual(scaleP21M2JSON(t, json.RawMessage(want.Body.Bytes()), false), scaleP21M2JSON(t, json.RawMessage(got.Body.Bytes()), false)) {
					t.Fatalf("project list edge changed: status=%d/%d\nold=%s\nbounded=%s", want.Code, got.Code, want.Body, got.Body)
				}
			})
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw87_disable_customers", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.FeatureFlags["customer-requests"] = false
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		cases = []queryCase{
			{"customers-disabled-default", nil},
			{"customers-disabled-filter", url.Values{"filter": {`[{"field":"customers","operator":"gte","values":["customer-count:1"]}]`}}},
		}
		t.Run("customers-feature-disabled", func(t *testing.T) { compare(t, f.principals[2].user, nil) })
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded project list decoded a complete workspace")
		}
	}
}
