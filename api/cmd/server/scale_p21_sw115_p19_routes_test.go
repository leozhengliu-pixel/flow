package main

import (
	"context"
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

type scaleP21SW115Writer struct {
	*httptest.ResponseRecorder
	store *store.SQLiteStore
	work  int
}

func (w *scaleP21SW115Writer) Write(body []byte) (int, error) {
	w.work = max(w.work, w.store.MetadataWorkUsedForTesting())
	return w.ResponseRecorder.Write(body)
}

func scaleP21SW115Key(prefix string, user domain.User, key *domain.APIKey) *domain.APIKey {
	value := scaleP21SW104StoredKey(prefix, user, key)
	if value != nil {
		value.SecretHash = secretHash("sw115-" + value.ID)
	}
	return value
}

func scaleP21SW115Fixture(t *testing.T, repo *store.SQLiteStore) scaleP21M2Fixture {
	t.Helper()
	f := scaleP03Fixture(t, repo)
	prefix := idgen.New("sw115-policy-")
	public, private, child := prefix+"public", prefix+"private", prefix+"child"
	if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw115_policy", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private}
		data.Projects = append(data.Projects, domain.Project{ID: "m2-unrelated", Name: "Hidden policy fixture", TeamIDs: []string{private}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	principals := []scaleP21M2Principal{{"admin-private-outsider", f.data.Viewer}}
	var privateAdmin *http.Client
	for _, spec := range []struct {
		name, role string
		teams      []string
	}{
		{"admin", "admin", []string{private}},
		{"member-private-outsider", "member", []string{public}},
		{"guest-explicit", "guest", []string{public}},
		{"sub-team-inheritance", "member", []string{private}},
	} {
		client, user := scaleP03Join(t, f, spec.name, idgen.New("sw115-person-")+"@example.test", spec.role, spec.teams...)
		if spec.name == "admin" {
			privateAdmin = client
		}
		principals = append(principals, scaleP21M2Principal{spec.name, user})
	}
	issue := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "P19 explicit guest grant", "teamId": private}, f.data.Workspace.URLKey, http.StatusCreated)
	authRequest[[]domain.IssuePermission](t, privateAdmin, http.MethodPut, f.host.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": principals[3].user.ID, "role": "viewer"}}}, f.data.Workspace.URLKey, http.StatusOK)
	probe, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return scaleP21M2Fixture{scaleP03PlatformFixture: f, probe: probe, principals: principals, public: public, testingT: t}
}

func TestScaleP21SW115P19RoutesDifferential(t *testing.T) {
	scaleP19Options(t)
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW115Fixture(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw115-p19-")
		at := time.Date(2026, 11, 15, 12, 0, 0, 123, time.UTC)
		project, hidden, childProject := prefix+"project", prefix+"hidden", prefix+"child-project"
		parent, child, hiddenParent := prefix+"parent", prefix+"child", prefix+"hidden-parent"
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw115_seed", "", nil, func(data *domain.Bootstrap) error {
			privateTeam, childTeam := "", ""
			for _, item := range data.Projects {
				if item.ID == "m2-unrelated" {
					privateTeam = item.TeamIDs[0]
				}
			}
			for id, settings := range data.TeamSettings {
				if settings.ParentTeamID == privateTeam {
					childTeam = id
				}
			}
			if privateTeam == "" || childTeam == "" {
				t.Fatal("P19 private and inherited fixture teams missing")
			}
			actor := f.principals[1].user
			if data.ProjectUpdates == nil {
				data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
			}
			if data.InitiativeUpdates == nil {
				data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
			}
			data.Projects = append(data.Projects,
				domain.Project{ID: project, SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"}, Name: "Public",
					TeamIDs: []string{f.public}, DependencyIDs: []string{hidden}, Initiatives: []string{parent, hiddenParent},
					Lead: &actor, Creator: &actor, MemberIDs: []string{actor.ID}, IssueCount: 97,
					Status: data.ProjectStatuses[0], Priority: 1, Health: "onTrack", CreatedAt: at, UpdatedAt: at},
				domain.Project{ID: hidden, Name: "Hidden", TeamIDs: []string{privateTeam}, Lead: &actor,
					Status: data.ProjectStatuses[0], IssueCount: 53, Priority: 2, CreatedAt: at},
				domain.Project{ID: childProject, Name: "Inherited", TeamIDs: []string{childTeam}, Lead: &actor,
					Status: data.ProjectStatuses[0], IssueCount: 11, CreatedAt: at},
			)
			data.Initiatives = append(data.Initiatives,
				domain.Initiative{ID: parent, Name: "Visible parent", Status: "active", Creator: actor, Owner: &actor,
					ProjectIDs: []string{project, hidden}, ParentInitiativeIDs: []string{hiddenParent},
					Description: "omitted body", Comments: []domain.Comment{{Body: "omitted comment", User: actor}}, CreatedAt: at},
				domain.Initiative{ID: child, Name: "Visible child", Status: "active", Creator: actor,
					ProjectIDs: []string{childProject}, ParentInitiativeIDs: []string{parent}, CreatedAt: at},
				domain.Initiative{ID: hiddenParent, Name: "Hidden parent", Status: "active", Creator: actor, Owner: &actor,
					LeadTeamID: privateTeam, ProjectIDs: []string{hidden}, CreatedAt: at},
			)
			data.ProjectUpdates[project] = []domain.ProjectUpdate{
				{ID: prefix + "update", ProjectID: project, Body: "not in the window", User: actor, Health: "offTrack", CreatedAt: at},
				{ID: prefix + "old-update", ProjectID: project, User: actor, Health: "onTrack", CreatedAt: at.Add(-time.Hour)},
			}
			data.InitiativeUpdates[parent] = []domain.InitiativeUpdate{{ID: prefix + "initiative-update", InitiativeID: parent, User: actor, Body: "omitted update body", CreatedAt: at}}
			for _, principal := range f.principals {
				settings := data.UserSettings[principal.user.ID]
				settings.UpdatedAt = at
				data.UserSettings[principal.user.ID] = settings
				for _, key := range []domain.APIKey{
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"},
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}},
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
				} {
					data.APIKeys = append(data.APIKeys, *scaleP21SW115Key(prefix, principal.user, &key))
				}
				data.Favorites = append(data.Favorites,
					domain.Favorite{ID: prefix + principal.user.ID + "-public", UserID: principal.user.ID, ResourceType: "project", ResourceID: project, CreatedAt: at},
					domain.Favorite{ID: prefix + principal.user.ID + "-hidden", UserID: principal.user.ID, ResourceType: "project", ResourceID: hidden, CreatedAt: at},
				)
			}
			data.Documents = append(data.Documents, domain.Document{ID: prefix + "document", Title: "Linked",
				Creator: actor, ProjectIDs: []string{project, hidden}, Content: "omitted document body", CreatedAt: at})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "P19 other", prefix+"other", "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
		for _, selected := range []string{workspace, other.Workspace.URLKey} {
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, selected); err != nil {
				t.Fatal(err)
			}
		}
		services := []*server{{store: repo, realtime: newRealtimeHub()}, {store: f.probe, realtime: newRealtimeHub()}}
		handlers := make([]*http.ServeMux, len(services))
		for i, service := range services {
			handlers[i] = http.NewServeMux()
			service.registerSyncRoutes(handlers[i])
		}
		query := scaleP19ProjectQuery()
		grouped := query
		grouped.Display.Grouping, grouped.Display.SubGrouping = "Priority", "Status"
		grouped.Display.ShowEmptyGroups = true
		byID, byCurrent, byOld := query, query, query
		byID.Filters = []store.ProjectDirectoryFilter{{Field: "project", Operator: "is", Values: []string{project}}}
		byCurrent.Filters = []store.ProjectDirectoryFilter{{Field: "project", Operator: "is", Values: []string{prefix + "current"}}}
		byOld.Filters = []store.ProjectDirectoryFilter{{Field: "project", Operator: "is", Values: []string{prefix + "old"}}}
		collapsed := query
		collapsed.View, collapsed.Collapsed = "active", []string{parent}
		routes := []struct{ name, path string }{
			{"manifest-projects", "/api/sync/manifest?surface=projects-page"},
			{"manifest-initiatives", "/api/sync/manifest?surface=initiatives-page"},
			{"projects", scaleP19Path("projects", query, "", 100)},
			{"projects-grouped", scaleP19Path("projects", grouped, "", 100)},
			{"projects-exact-id", scaleP19Path("projects", byID, "", 100)},
			{"projects-current-slug-filter", scaleP19Path("projects", byCurrent, "", 100)},
			{"projects-old-slug-filter", scaleP19Path("projects", byOld, "", 100)},
			{"initiatives-hierarchy", scaleP19Path("initiatives", query, "", 100)},
			{"initiatives-collapsed", scaleP19Path("initiatives", collapsed, "", 100)},
		}
		// Exercise the registered sync dispatch with authenticated fixture
		// identities and real stored credentials. Authentication is already
		// covered by P03; avoiding usage telemetry keeps paired revisions exact.
		call := func(t *testing.T, i int, user domain.User, key *domain.APIKey, targetWorkspace, path string, outer bool) *scaleP21SW115Writer {
			t.Helper()
			request := f.literalRequest(user, key, nil)
			request.Method = http.MethodGet
			request.URL, err = url.Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("X-Workspace-Key", targetWorkspace)
			request.SetPathValue("workspaceKey", targetWorkspace)
			request.SetPathValue("workspace", targetWorkspace)
			closeOutput := func() {}
			if outer {
				var ctx context.Context
				ctx, closeOutput = beginMetadataOutputReads(request.Context())
				request = request.WithContext(ctx)
			} else if services[i].store.BoundedMetadataEnabled() {
				request, closeOutput = beginRequestResourceReads(request)
			}
			defer closeOutput()
			writer := &scaleP21SW115Writer{ResponseRecorder: httptest.NewRecorder(), store: services[i].store}
			handlers[i].ServeHTTP(writer, request)
			if i == 1 && writer.Code == http.StatusOK &&
				(writer.work == 0 || f.probe.MetadataWorkUsedForTesting() == 0) {
				t.Fatal("P19 output was released before response encoding completed")
			}
			closeOutput()
			if services[i].store.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("P19 route retained metadata output after response close")
			}
			return writer
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			key = scaleP21SW115Key(prefix, user, key)
			if key != nil {
				auth, err := repo.AuthenticateAPIKeyRecord(t.Context(), workspace, key.SecretHash, nil)
				if err != nil || auth.User.ID != user.ID || auth.Key.ID != key.ID {
					t.Fatal("P19 matrix key is not a live stored credential", err)
				}
				key = &auth.Key
			}
			for _, outer := range []bool{false, true} {
				mode := "request-output"
				if outer {
					mode = "outer-output"
				}
				for _, route := range routes {
					t.Run(mode+"/"+route.name, func(t *testing.T) {
						want := call(t, 0, user, key, workspace, route.path, outer)
						got := call(t, 1, user, key, workspace, route.path, outer)
						var oldJSON, boundedJSON any
						if err := json.Unmarshal(want.Body.Bytes(), &oldJSON); err != nil {
							t.Fatal("legacy route returned invalid JSON", err)
						}
						if err := json.Unmarshal(got.Body.Bytes(), &boundedJSON); err != nil {
							t.Fatal("bounded route returned invalid JSON", err)
						}
						if want.Code != http.StatusOK || got.Code != want.Code ||
							!reflect.DeepEqual(want.Header(), got.Header()) || !reflect.DeepEqual(oldJSON, boundedJSON) {
							t.Fatalf("P19 %s differs: status=%d/%d\nold=%s\nbounded=%s", route.name, want.Code, got.Code, want.Body, got.Body)
						}
						if strings.Contains(route.name, "slug-filter") && boundedJSON.(map[string]any)["total"] != float64(0) {
							t.Fatal("P19 filter unexpectedly resolved a current/previous slug")
						}
					})
				}
			}
		})
		member := f.principals[2].user
		for _, outer := range []bool{false, true} {
			path := scaleP19Path("projects", query, "", 100)
			want := call(t, 0, member, nil, other.Workspace.URLKey, path, outer)
			got := call(t, 1, member, nil, other.Workspace.URLKey, path, outer)
			if want.Code != http.StatusForbidden || got.Code != want.Code || !reflect.DeepEqual(want.Body.Bytes(), got.Body.Bytes()) {
				t.Fatalf("P19 cross-workspace denial differs: status=%d/%d", want.Code, got.Code)
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("P19 routes decoded whole workspace metadata")
		}
	}
}
