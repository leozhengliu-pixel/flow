package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW215ColdAuthorizationUsesActualMuxWinner(t *testing.T) {
	mux := http.NewServeMux()
	for _, route := range scaleP21ColdRoutes {
		mux.HandleFunc(route.method+" "+route.path, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}
	// A synthetic COLD-only mux would incorrectly classify this exact HOT
	// route as PATCH /api/drafts/{id}.
	mux.HandleFunc("PATCH /api/drafts/sw215-hot", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	for _, test := range []struct {
		method, path, pattern string
		cold                  bool
		status                int
	}{
		{http.MethodGet, "/api/loop-config", "GET /api/loop-config", true, http.StatusNoContent},
		{http.MethodHead, "/api/loop-config", "GET /api/loop-config", true, http.StatusNoContent},
		{http.MethodPost, "/api/loop-config", "", false, http.StatusMethodNotAllowed},
		{http.MethodPatch, "/api/drafts/plain", "PATCH /api/drafts/{id}", true, http.StatusNoContent},
		{http.MethodPatch, "/api/drafts/a%2Fb", "PATCH /api/drafts/{id}", true, http.StatusNoContent},
		{http.MethodPatch, "/api/drafts/sw215-hot", "PATCH /api/drafts/sw215-hot", false, http.StatusAccepted},
		{http.MethodPatch, "/api/drafts/%73w215-hot", "PATCH /api/drafts/sw215-hot", false, http.StatusAccepted},
		{http.MethodPatch, "/api/drafts//sw215-hot", "PATCH /api/drafts/sw215-hot", false, http.StatusTemporaryRedirect},
		{http.MethodGet, "/api//loop-config", "GET /api/loop-config", true, http.StatusTemporaryRedirect},
		{http.MethodPatch, "/api/drafts/a/b", "", false, http.StatusNotFound},
		{http.MethodGet, "/api/loop-config/child", "", false, http.StatusNotFound},
		{http.MethodGet, "/api/workspace/loop-settings-extra", "", false, http.StatusNotFound},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, nil)
			r.Pattern = "PATCH /api/drafts/{id}" // Untrusted/stale Pattern must be ignored.
			r = r.WithContext(context.WithValue(r.Context(), coldAuthorizationMuxKey{}, mux))
			_, pattern := mux.Handler(r)
			if pattern != test.pattern {
				t.Fatalf("fixture's actual mux winner=%q want=%q", pattern, test.pattern)
			}
			route, cold := scaleP21ColdAuthorizationRoute(r)
			if cold != test.cold || cold && route.method+" "+route.path != test.pattern {
				t.Fatalf("actual HOT/HEAD/escaped/redirect winner misclassified: cold=%t route=%+v", cold, route)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("real mux outcome=%d want=%d", w.Code, test.status)
			}
		})
	}
	// No closed complete route authority means no COLD admission.
	r := httptest.NewRequest(http.MethodGet, "/api/loop-config", nil)
	r.Pattern = "GET /api/loop-config"
	if _, selected := scaleP21ColdAuthorizationRoute(r); selected {
		t.Fatal("Request.Pattern alone certified a cold route")
	}
}

func TestScaleP21SW215ColdAuthorizationFullHTTPHandoff(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	for name, repo := range scaleP03ServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			for _, reload := range []bool{false, true} {
				if reload {
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				var wantCode int
				var wantBody []byte
				for _, source := range []*store.SQLiteStore{repo, view} {
					service := &server{store: source}
					mux := http.NewServeMux()
					var read *store.ColdMetadataRead
					var scope *requestResourceReads
					var handoff *pagedMetadataHandoff
					var facade *store.SQLiteStore
					var outputChecks int
					mux.HandleFunc("GET /api/loop-config", func(w http.ResponseWriter, r *http.Request) {
						if source.BoundedMetadataEnabled() {
							read, _ = r.Context().Value(coldAuthorizationReadKey{}).(*store.ColdMetadataRead)
							scope, _ = r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
							handoff, _ = r.Context().Value(pagedMetadataHandoffKey{}).(*pagedMetadataHandoff)
							if read == nil || read.Store() == nil || scope == nil || scope.closed || handoff == nil || handoff.taken.Load() {
								t.Fatal("authenticate cloning lost the cold context, output owner or original GET handoff")
							}
							facade = read.Store()
							if facade == source || facade.BoundedMetadataEnabled() || service.store != source {
								t.Fatal("authorization substituted the server receiver")
							}
							service.withColdMetadataRequest(w, r, "server/loop_settings.go:getLoopConfig", "", func(borrowed *store.SQLiteStore, nested *http.Request) {
								if borrowed != facade || nested.Context() != r.Context() {
									t.Fatal("handler bridge did not borrow auth facade/context/slot")
								}
								service.getLoopConfig(w, nested)
							})
						} else {
							service.getLoopConfig(w, r)
						}
					})
					handler := service.withColdMetadataRouteMux(mux, service.authenticate(mux))
					request := httptest.NewRequest(http.MethodGet, "/api/loop-config", nil)
					request.Header.Set("X-Workspace-Key", workspace)
					origin, _ := url.Parse(f.host.URL)
					for _, cookie := range f.admin.Jar.Cookies(origin) {
						request.AddCookie(cookie)
					}
					ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
					request = request.WithContext(ctx)
					recorder := httptest.NewRecorder()
					writer := &scaleP21SW215ColdOutputWriter{ResponseRecorder: recorder, check: func() {
						outputChecks++
						if source.BoundedMetadataEnabled() && (read == nil || read.Store() != facade || scope.closed || handoff.data.Workspace.ID == "") {
							t.Error("cold graph/Paged handoff released before response encoding")
						}
					}}
					handler.ServeHTTP(writer, request)
					cancel()
					if recorder.Code != http.StatusOK || outputChecks == 0 {
						t.Fatal("full authenticated cold route failed", recorder.Code, recorder.Body.String())
					}
					if !source.BoundedMetadataEnabled() {
						wantCode, wantBody = recorder.Code, append([]byte(nil), recorder.Body.Bytes()...)
					} else {
						if recorder.Code != wantCode || !bytes.Equal(recorder.Body.Bytes(), wantBody) || read.Store() != nil ||
							!scope.closed || handoff.data.Workspace.ID != "" || service.store != source {
							t.Fatal("full HTTP differential or outer output cleanup changed", recorder.Body.String())
						}
						next, err := source.BeginColdMetadataRead(t.Context(), "server/loop_settings.go:getLoopConfig")
						if err != nil || next.Store() == nil || next.Store() == facade {
							t.Fatal("HTTP output scope retained its replica slot", err)
						}
						next.Close()
					}
				}
			}
		})
	}
}

func TestScaleP21SW215ColdAuthorizationEarlyRefusalIsLazy(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for name, repo := range scaleP03ServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
			if err != nil {
				t.Fatal(err)
			}
			held, err := view.BeginColdMetadataRead(t.Context(), "server/loop_settings.go:getLoopConfig")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			service := &server{store: view}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/loop-config", func(http.ResponseWriter, *http.Request) { t.Error("unauthenticated handler ran") })
			handler := service.withColdMetadataRouteMux(mux, service.authenticate(mux))
			request := httptest.NewRequest(http.MethodGet, "/api/loop-config", nil)
			request.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
			ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
			defer cancel()
			request = request.WithContext(ctx)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, request)
			if w.Code != http.StatusUnauthorized || ctx.Err() != nil || held.Store() == nil || !strings.Contains(w.Body.String(), "Sign in required") {
				t.Fatal("an early auth refusal acquired/waited for another request's cold slot", w.Code, w.Body.String(), ctx.Err())
			}
		})
	}
}

func TestScaleP21SW215ColdAuthorizationHandoffThenFreshAccessor(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	for name, repo := range scaleP03ServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			var want []byte
			for _, bounded := range []bool{false, true} {
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw215_before", "", nil, func(data *domain.Bootstrap) error {
					data.Workspace.Name = "before authorization"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				source := repo
				if bounded {
					var err error
					source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
				}
				service := &server{store: source}
				mux := http.NewServeMux()
				mux.HandleFunc("GET /api/loop-config", func(w http.ResponseWriter, r *http.Request) {
					user := authUser(r)
					first, handed := takePagedMetadata(r, workspace, user.ID)
					if !handed || first.Workspace.Name != "before authorization" {
						t.Fatal("first accessor did not consume exact auth handoff")
					}
					// Another same-version writer publishes between the
					// handed-off projection and the next legacy accessor.
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw215_after", "", nil, func(data *domain.Bootstrap) error {
						data.Workspace.Name = "after authorization"
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if bounded {
						if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
					}
					service.withColdMetadataRequest(w, r, "server/loop_settings.go:getLoopConfig", "", func(borrowed *store.SQLiteStore, request *http.Request) {
						if err := borrowed.RefreshColdMetadataRead(request.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						fresh, err := borrowed.PagedWorkspaceMetadata(request.Context(), workspace, user.ID)
						if err != nil || fresh.Workspace.Name != "after authorization" || first.Workspace.Name != "before authorization" {
							t.Fatal("fresh accessor stayed pinned or changed earlier output", err)
						}
						writeJSON(w, http.StatusOK, map[string]string{"first": first.Workspace.Name, "fresh": fresh.Workspace.Name})
					})
				})
				request := httptest.NewRequest(http.MethodGet, "/api/loop-config", nil)
				request.Header.Set("X-Workspace-Key", workspace)
				origin, _ := url.Parse(f.host.URL)
				for _, cookie := range f.admin.Jar.Cookies(origin) {
					request.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				service.withColdMetadataRouteMux(mux, service.authenticate(mux)).ServeHTTP(w, request)
				if w.Code != http.StatusOK {
					t.Fatal("authenticated accessor timing route failed", w.Code, w.Body.String())
				}
				if bounded {
					if !bytes.Equal(w.Body.Bytes(), want) {
						t.Fatal("legacy/bounded repeated accessor timing differs", w.Body.String())
					}
				} else {
					want = append([]byte(nil), w.Body.Bytes()...)
				}
			}
		})
	}
}

func TestScaleP21SW215ColdQueryCacheAndDraftFreshAccessors(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for name, repo := range scaleP03ServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			setName := func(value string, addDocument bool) {
				t.Helper()
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw215_query_seed", "", nil, func(data *domain.Bootstrap) error {
					data.Workspace.Name = value
					if addDocument {
						data.Documents = append(data.Documents, domain.Document{ID: "document_sw215_fresh", Title: "Newly visible", Creator: f.data.Viewer})
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			setName("before query", false)
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			read, err := view.BeginColdMetadataRead(t.Context(), "server/advanced_features.go:createDraft")
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			facade := read.Store()
			service := &server{store: view}
			request := func(ctx context.Context) *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/api/drafts", nil)
				r.Header.Set("X-Workspace-Key", workspace)
				return r.WithContext(context.WithValue(ctx, authUserContextKey{}, f.data.Viewer))
			}
			setName("after query", false)
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			cached := issueRequestAccess{
				workspace: workspace, userID: f.data.Viewer.ID,
				metadata: domain.Bootstrap{Workspace: domain.Workspace{ID: f.data.Workspace.ID, URLKey: workspace, Name: "cached query"}},
			}
			hit := request(context.WithValue(read.Context(), issueRequestAccessKey{}, cached))
			data, _, err := service.requestIssueQueryAccessWithStore(hit, facade)
			pinned, _ := facade.WorkspaceMetadata(workspace)
			if err != nil || data.Workspace.Name != "cached query" || pinned.Workspace.Name != "before query" {
				t.Fatal("cache hit refreshed or replaced its original authority cut", err)
			}
			miss := request(read.Context())
			data, _, err = service.requestIssueQueryAccessWithStore(miss, facade)
			if err != nil || data.Workspace.Name != "after query" {
				t.Fatal("cache-miss accessor retained auth-time facade", err)
			}
			// draftRecordValidator first reuses this cached query, then its
			// literal Paged read must observe the next installed generation.
			setName("fresh draft projection", true)
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			validate, err := service.draftRecordValidator(miss, facade)
			if err != nil {
				t.Fatal(err)
			}
			if err := validate(nil, domain.Draft{Type: "document", ResourceID: "document_sw215_fresh"}); err != nil {
				t.Fatal("fresh Paged draft projection missed newly installed resource", err)
			}
			canceled, cancel := context.WithCancel(miss.Context())
			cancel()
			invalid := miss.WithContext(canceled)
			validate, err = service.draftRecordValidator(invalid, facade)
			if !errors.Is(err, context.Canceled) || validate != nil {
				t.Fatal("fresh accessor failure became an empty successful validator", err)
			}
			// A cache-miss refresh failure likewise propagates/caches its
			// actual error and never runs the legacy projection afterward.
			badCtx, cancel := context.WithCancel(read.Context())
			cancel()
			failed := request(badCtx)
			_, _, err = service.requestIssueQueryAccessWithStore(failed, facade)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cache-miss refresh failure was swallowed", err)
			}
			if stored, ok := failed.Context().Value(issueRequestAccessKey{}).(issueRequestAccess); !ok || !errors.Is(stored.err, context.Canceled) {
				t.Fatal("new read failure changed existing request cache semantics")
			}
		})
	}
}

type scaleP21SW215ColdOutputWriter struct {
	*httptest.ResponseRecorder
	check func()
}

func (w *scaleP21SW215ColdOutputWriter) WriteHeader(code int) {
	w.check()
	w.ResponseRecorder.WriteHeader(code)
}

func (w *scaleP21SW215ColdOutputWriter) Write(raw []byte) (int, error) {
	w.check()
	return w.ResponseRecorder.Write(raw)
}

type scaleP21SW215RealCredential struct {
	name, session, bearer string
}

type scaleP21SW215AuthMatrixFixture struct {
	scaleP21M2Fixture
	foreign, foreignIssue, grantIssue, publicIssue, childIssue, hiddenIssue string
	publicProject, childProject, hiddenProject, oldSlug, hiddenMarker       string
	grantPermissions                                                        []domain.IssuePermission
	credentials                                                             map[string][]scaleP21SW215RealCredential
}

type scaleP21SW215AuthProjection struct {
	Workspace string                `json:"workspace"`
	Viewer    string                `json:"viewer"`
	Role      string                `json:"role"`
	Projects  []domain.Project      `json:"projects"`
	Documents []domain.Document     `json:"documents"`
	Issues    store.IssueRecordPage `json:"issues"`
	OldSlugID string                `json:"oldSlugId"`
}

type scaleP21SW215AuthMatrixOutput struct {
	Before scaleP21SW215AuthProjection  `json:"before"`
	Fresh  *scaleP21SW215AuthProjection `json:"fresh,omitempty"`
}

func scaleP21SW215AuthMatrixSeed(t *testing.T, repo *store.SQLiteStore) scaleP21SW215AuthMatrixFixture {
	t.Helper()
	restore := store.ForceFullMutationsForTesting(true)
	defer restore()
	f := scaleP21M2Setup(t, repo)
	prefix := idgen.New("sw215-auth-")
	result := scaleP21SW215AuthMatrixFixture{
		scaleP21M2Fixture: f, publicIssue: prefix + "public-issue", childIssue: prefix + "child-issue",
		hiddenIssue: prefix + "hidden-issue", foreignIssue: prefix + "foreign-issue",
		publicProject: prefix + "public-project", childProject: prefix + "child-project",
		hiddenProject: prefix + "hidden-project", oldSlug: prefix + "old-project",
		hiddenMarker: prefix + "hidden-actor-and-body", credentials: map[string][]scaleP21SW215RealCredential{},
	}
	current, found := repo.BootstrapFor(f.data.Workspace.URLKey)
	if !found {
		t.Fatal("auth matrix workspace missing")
	}
	private, child := "", ""
	for _, team := range current.Teams {
		if strings.HasPrefix(team.ID, "m2-private-") {
			private = team.ID
		}
	}
	for id, settings := range current.TeamSettings {
		if settings.ParentTeamID == private {
			child = id
		}
	}
	for _, issue := range current.Issues {
		if issue.Title == "M2 private grant" {
			result.grantIssue, result.grantPermissions = issue.ID, slices.Clone(issue.Permissions)
		}
	}
	if private == "" || child == "" || result.grantIssue == "" || len(result.grantPermissions) == 0 {
		t.Fatal("auth matrix lacks private boundary, inherited child or actual issue grant")
	}
	_, outsideAdmin := scaleP03Join(t, f.scaleP03PlatformFixture, "SW215 SQL outside admin", idgen.New("sw215-admin-")+"@example.test", "admin", f.public)
	role, status, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, outsideAdmin.ID)
	if err != nil || role != "admin" || status != "active" || outsideAdmin.ID == f.data.Viewer.ID {
		t.Fatal("auth matrix outside admin lacks a distinct active SQL admin membership", role, status, err)
	}
	members, err := repo.ListTeamMembers(t.Context(), f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	publicMember := false
	for _, member := range members {
		if member.UserID != outsideAdmin.ID {
			continue
		}
		if member.TeamID == private || member.TeamID == child {
			t.Fatal("auth matrix outside admin unexpectedly joined the private boundary")
		}
		publicMember = publicMember || member.TeamID == f.public
	}
	if !publicMember {
		t.Fatal("auth matrix outside admin lacks the requested public team membership")
	}
	f.principals = append(f.principals, scaleP21M2Principal{name: "sql-admin-private-outsider", user: outsideAdmin})
	result.scaleP21M2Fixture = f
	foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW215 foreign", prefix+"foreign", "us")
	if err != nil {
		t.Fatal(err)
	}
	result.foreign = foreign.Workspace.URLKey
	f.resources.workspaces = append(f.resources.workspaces, result.foreign)
	at := time.Date(2026, time.November, 16, 1, 2, 3, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), result.foreign, "scale_p21.sw215_foreign_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = append(data.Issues, domain.Issue{
			ID: result.foreignIssue, Identifier: prefix + "FOREIGN", Title: "Foreign workspace issue",
			Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: at, UpdatedAt: at,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var keys []domain.APIKey
	for _, principal := range f.principals {
		_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal("mint real fixture session", err)
		}
		credentials := []scaleP21SW215RealCredential{{name: "session", session: token}}
		for _, kind := range []string{"unrestricted", "selected-empty", "selected-set"} {
			secret := idgen.New("sw215-bearer-")
			restriction, teams := "all", []string(nil)
			if kind != "unrestricted" {
				restriction, teams = "selected", []string{}
				if kind == "selected-set" {
					teams = []string{f.public}
				}
			}
			keys = append(keys, domain.APIKey{
				ID: idgen.New("sw215-key-"), Name: "SW215 actual bearer " + kind, CreatorID: principal.user.ID,
				SecretHash: secretHash(secret), Scopes: []string{"read"}, TeamRestriction: restriction,
				TeamIDs: teams, CreatedAt: at,
			})
			credentials = append(credentials, scaleP21SW215RealCredential{name: kind, bearer: secret})
		}
		result.credentials[principal.user.ID] = credentials
	}
	if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw215_auth_matrix_seed", "", nil, func(data *domain.Bootstrap) error {
		team := func(id string) domain.Team {
			for _, value := range data.Teams {
				if value.ID == id {
					return value
				}
			}
			return domain.Team{}
		}
		hiddenActor := domain.User{ID: prefix + "hidden-actor", Name: result.hiddenMarker}
		owner := data.Viewer
		data.APIKeys = append(data.APIKeys, keys...)
		data.Projects = append(data.Projects,
			domain.Project{
				ID: result.publicProject, Name: "Visible public project", SlugID: prefix + "current-project",
				PreviousSlugIDs: []string{result.oldSlug}, TeamIDs: []string{f.public},
				DependencyIDs: []string{result.hiddenProject}, IssueCount: 99, Creator: &owner,
			},
			domain.Project{ID: result.childProject, Name: "Inherited child project", TeamIDs: []string{child}, IssueCount: 77, Creator: &owner},
			domain.Project{
				ID: result.hiddenProject, Name: "Private project", Description: result.hiddenMarker,
				TeamIDs: []string{private}, IssueCount: 66, Creator: &hiddenActor, Lead: &hiddenActor,
			},
		)
		data.Documents = append(data.Documents, domain.Document{
			ID: prefix + "document", Title: "Visible document", TeamIDs: []string{f.public},
			ProjectIDs: []string{result.publicProject, result.hiddenProject}, Creator: owner,
		})
		for _, issue := range []struct {
			id, team, project string
			actor             domain.User
		}{
			{result.publicIssue, f.public, result.publicProject, owner},
			{result.childIssue, child, result.childProject, owner},
			{result.hiddenIssue, private, result.hiddenProject, hiddenActor},
		} {
			data.Issues = append(data.Issues, domain.Issue{
				ID: issue.id, Identifier: issue.id, Title: issue.id, Team: team(issue.team), State: data.States[0],
				Project: &domain.ProjectSummary{ID: issue.project, Name: issue.project}, Creator: issue.actor,
				CreatedAt: at, UpdatedAt: at,
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := result.restoreGrant(ctx, repo, false); err != nil {
			t.Errorf("restore owned SW215 issue grant: %v", err)
		}
	})
	return result
}

func (f scaleP21SW215AuthMatrixFixture) restoreGrant(ctx context.Context, repo *store.SQLiteStore, revoke bool) error {
	if err := repo.ReloadWorkspace(ctx, f.data.Workspace.URLKey); err != nil {
		return err
	}
	release := store.ForceFullMutationsForTesting(true)
	defer release()
	return scaleP03RetryWrite(ctx, func() error {
		return repo.MutateWorkspace(ctx, f.data.Workspace.URLKey, "scale_p21.sw215_grant_change", f.grantIssue, nil, func(data *domain.Bootstrap) error {
			for index := range data.Issues {
				if data.Issues[index].ID == f.grantIssue {
					data.Issues[index].Permissions = slices.Clone(f.grantPermissions)
					if revoke {
						data.Issues[index].Permissions = nil
					}
					return nil
				}
			}
			return fmt.Errorf("owned grant issue missing")
		})
	})
}

func scaleP21SW215AuthMatrixServe(t *testing.T, f scaleP21SW215AuthMatrixFixture, source *store.SQLiteStore, credential scaleP21SW215RealCredential, workspace string, between func()) (*httptest.ResponseRecorder, scaleP21SW215AuthMatrixOutput) {
	t.Helper()
	service := &server{store: source}
	mux := http.NewServeMux()
	var held *store.ColdMetadataRead
	var scope *requestResourceReads
	mux.HandleFunc("GET /api/loop-config", func(w http.ResponseWriter, r *http.Request) {
		if source.BoundedMetadataEnabled() {
			held, _ = r.Context().Value(coldAuthorizationReadKey{}).(*store.ColdMetadataRead)
			scope, _ = r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
			if held == nil || held.Store() == nil || scope == nil || scope.closed {
				t.Fatal("real credentials lost lazy cold ownership before handler entry")
			}
		}
		service.withColdMetadataRequest(w, r, "server/loop_settings.go:getLoopConfig", "", func(repo *store.SQLiteStore, request *http.Request) {
			first, handed := takePagedMetadata(request, workspaceKey(request), authUser(request).ID)
			if !handed {
				t.Fatal("real authentication did not hand off its exact viewer projection")
			}
			project := func(data domain.Bootstrap) scaleP21SW215AuthProjection {
				t.Helper()
				filterBootstrapForAPIKey(&data, request)
				_, query, err := service.issueRecordsQueryWithStore(request, repo)
				if err != nil {
					t.Fatal("real credential issue access", err)
				}
				query.Filter = store.IssueFilter{Field: "id", Values: []string{f.publicIssue, f.childIssue, f.hiddenIssue, f.grantIssue, f.foreignIssue}}
				query.Archived, query.Sort, query.Direction, query.Limit, query.IncludeTotal = "all", "createdAt", "asc", 100, true
				page, err := repo.QueryIssueRecords(request.Context(), query)
				if err != nil {
					t.Fatal("real credential issue page", err)
				}
				out := scaleP21SW215AuthProjection{
					Workspace: data.Workspace.ID, Viewer: authUser(request).ID, Role: data.ViewerRole,
					Projects: data.Projects, Documents: data.Documents, Issues: page,
				}
				if index := domain.ProjectIndexBySlugRef(data.Projects, f.oldSlug); index >= 0 {
					out.OldSlugID = data.Projects[index].ID
				}
				return out
			}
			output := scaleP21SW215AuthMatrixOutput{Before: project(first)}
			if between != nil {
				original, err := json.Marshal(output.Before)
				if err != nil {
					t.Fatal(err)
				}
				between()
				if err := repo.RefreshColdMetadataRead(request.Context(), workspaceKey(request)); err != nil {
					t.Fatal("fresh read after grant revocation", err)
				}
				fresh, err := repo.PagedWorkspaceMetadata(request.Context(), workspaceKey(request), authUser(request).ID)
				if err != nil {
					t.Fatal("fresh projection after grant revocation", err)
				}
				updated := project(fresh)
				output.Fresh = &updated
				if actual, err := json.Marshal(output.Before); err != nil || !bytes.Equal(actual, original) {
					t.Fatal("refresh/revocation mutated the earlier captured output", err)
				}
			}
			writeJSON(w, http.StatusOK, output)
		})
	})
	request := httptest.NewRequest(http.MethodGet, "/api/loop-config", nil)
	request.Header.Set("X-Workspace-Key", workspace)
	if credential.session != "" {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: credential.session})
	}
	if credential.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+credential.bearer)
	}
	recorder := httptest.NewRecorder()
	writer := &scaleP21SW215ColdOutputWriter{ResponseRecorder: recorder, check: func() {
		if source.BoundedMetadataEnabled() && recorder.Code == http.StatusOK && held != nil &&
			(held.Store() == nil || scope == nil || scope.closed) {
			t.Error("real credential output lost its cold owner before encoding")
		}
	}}
	service.withColdMetadataRouteMux(mux, service.authenticate(mux)).ServeHTTP(writer, request)
	if source.BoundedMetadataEnabled() && (held != nil && held.Store() != nil || scope != nil && !scope.closed || source.MetadataWorkUsedForTesting() != 0) {
		t.Fatal("real credential request retained cold ownership")
	}
	var output scaleP21SW215AuthMatrixOutput
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &output); err != nil {
			t.Fatal(err)
		}
	}
	return recorder, output
}

func TestScaleP21SW215ColdAuthenticationPermissionMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP21SW215AuthMatrixSeed(t, repo)
			workspace := f.data.Workspace.URLKey
			hasIssue := func(data scaleP21SW215AuthProjection, id string) bool {
				return slices.ContainsFunc(data.Issues.Items, func(issue domain.Issue) bool { return issue.ID == id })
			}
			for _, reload := range []bool{false, true} {
				if reload {
					for _, key := range []string{workspace, f.foreign} {
						if err := repo.ReloadWorkspace(t.Context(), key); err != nil {
							t.Fatal(err)
						}
					}
				}
				// The constructor registers this residency with repo; fixture Close releases its logical sources.
				view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.foreign)
				if err != nil {
					t.Fatal(err)
				}
				for _, principal := range f.principals {
					for _, credential := range f.credentials[principal.user.ID] {
						t.Run(fmt.Sprintf("reload=%t/%s/%s", reload, principal.name, credential.name), func(t *testing.T) {
							for _, key := range []string{workspace, f.foreign} {
								expected, want := scaleP21SW215AuthMatrixServe(t, f, repo, credential, key, nil)
								actual, got := scaleP21SW215AuthMatrixServe(t, f, view, credential, key, nil)
								if actual.Code != expected.Code || !bytes.Equal(actual.Body.Bytes(), expected.Body.Bytes()) {
									t.Fatal("actual-cookie/Bearer legacy/bounded auth or projection differs", actual.Code, expected.Code)
								}
								if key == f.foreign {
									code := http.StatusForbidden
									if credential.bearer != "" {
										code = http.StatusUnauthorized
									} else if principal.user.ID == f.data.Viewer.ID {
										code = http.StatusOK
									}
									if expected.Code != code || code == http.StatusOK && !hasIssue(want.Before, f.foreignIssue) {
										t.Fatal("foreign workspace credential boundary was not exercised", expected.Code, code)
									}
									continue
								}
								if expected.Code != http.StatusOK || got.Before.Viewer != principal.user.ID || hasIssue(got.Before, f.foreignIssue) {
									t.Fatal("main workspace identity or foreign issue scope changed", expected.Code)
								}
								if credential.name == "selected-empty" {
									if len(got.Before.Projects) != 0 || len(got.Before.Issues.Items) != 0 || got.Before.OldSlugID != "" {
										t.Fatal("real selected-empty key read protected content")
									}
									continue
								}
								if got.Before.OldSlugID != f.publicProject {
									t.Fatal("actual credential lost the old project slug")
								}
								if principal.name == "guest-explicit" && credential.name != "selected-set" && !hasIssue(got.Before, f.grantIssue) {
									t.Fatal("real guest explicit private issue grant was not read")
								}
								if principal.name == "sub-team-inheritance" && credential.name != "selected-set" && !hasIssue(got.Before, f.childIssue) {
									t.Fatal("real parent membership did not inherit the child issue")
								}
								if credential.name == "selected-set" && (hasIssue(got.Before, f.childIssue) || hasIssue(got.Before, f.hiddenIssue) || hasIssue(got.Before, f.grantIssue)) {
									t.Fatal("real selected-public key escaped its explicit team restriction")
								}
								private := slices.ContainsFunc(got.Before.Projects, func(project domain.Project) bool { return project.ID == f.hiddenProject })
								if !private {
									if bytes.Contains(actual.Body.Bytes(), []byte(f.hiddenMarker)) {
										t.Fatal("hidden actor/body escaped the viewer projection")
									}
									// The literal paged projection reports zero here, not the stored count of 99.
									for _, project := range got.Before.Projects {
										if slices.Contains(project.DependencyIDs, f.hiddenProject) || project.ID == f.publicProject && project.IssueCount != 0 {
											t.Fatal("hidden project references or raw issue counts escaped the projection", project.IssueCount)
										}
									}
									documentFound := false
									for _, document := range got.Before.Documents {
										if !slices.Contains(document.ProjectIDs, f.publicProject) {
											continue
										}
										documentFound = true
										// The literal projection retains linked IDs for session/unrestricted credentials.
										if slices.Contains(document.ProjectIDs, f.hiddenProject) != (credential.name != "selected-set") {
											t.Fatal("document project references changed literal credential behavior")
										}
									}
									if !documentFound {
										t.Fatal("visible document projection was not exercised")
									}
								}
							}
						})
					}
				}
				guest := f.principals[3]
				if guest.name != "guest-explicit" {
					t.Fatal("revocation fixture no longer selects the explicit guest")
				}
				credential := f.credentials[guest.user.ID][1] // Actual unrestricted Bearer.
				var expected []byte
				for _, source := range []*store.SQLiteStore{repo, view} {
					restore := func(revoke bool) {
						t.Helper()
						if err := f.restoreGrant(t.Context(), repo, revoke); err != nil {
							t.Fatal(err)
						}
						if source.BoundedMetadataEnabled() {
							if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
								t.Fatal(err)
							}
						}
					}
					restore(false)
					response, output := scaleP21SW215AuthMatrixServe(t, f, source, credential, workspace, func() { restore(true) })
					if response.Code != http.StatusOK || !hasIssue(output.Before, f.grantIssue) ||
						output.Fresh == nil || hasIssue(*output.Fresh, f.grantIssue) {
						t.Fatal("capture/fresh-use revocation was not enforced", response.Code)
					}
					if source == repo {
						expected = bytes.Clone(response.Body.Bytes())
					} else if !bytes.Equal(response.Body.Bytes(), expected) {
						t.Fatal("legacy/bounded capture-to-use grant revocation differs")
					}
					next, nextOutput := scaleP21SW215AuthMatrixServe(t, f, source, credential, workspace, nil)
					if next.Code != http.StatusOK || hasIssue(nextOutput.Before, f.grantIssue) {
						t.Fatal("next authenticated request retained the revoked issue grant", next.Code)
					}
					restore(false)
				}
			}
		})
	}
}
