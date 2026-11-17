package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21SW113Response struct {
	Code   int
	Header http.Header
	Body   []byte
}

type scaleP21SW113Writer struct {
	*httptest.ResponseRecorder
	t      *testing.T
	source *store.SQLiteStore
	writes int
}

func (w *scaleP21SW113Writer) checkOwner(code int) {
	if w.source.BoundedMetadataEnabled() && code == http.StatusOK && w.source.MetadataWorkUsedForTesting() == 0 {
		w.t.Fatal("permission response lost its retained read before output")
	}
}

func (w *scaleP21SW113Writer) WriteHeader(code int) {
	w.checkOwner(code)
	w.ResponseRecorder.WriteHeader(code)
}

func (w *scaleP21SW113Writer) Write(raw []byte) (int, error) {
	w.checkOwner(w.Code)
	w.writes++
	return w.ResponseRecorder.Write(raw)
}

func scaleP21SW113Serve(t *testing.T, f scaleP21M2Fixture, source *store.SQLiteStore, user domain.User, key *domain.APIKey, token, prefix, ref string, development bool) scaleP21SW113Response {
	t.Helper()
	return scaleP21SW113ServeMethod(t, f, source, user, key, token, prefix, ref, development, http.MethodGet)
}

// scaleP21SW113ServeMethod also serves HEAD, which the GET patterns accept.
func scaleP21SW113ServeMethod(t *testing.T, f scaleP21M2Fixture, source *store.SQLiteStore, user domain.User, key *domain.APIKey, token, prefix, ref string, development bool, method string) scaleP21SW113Response {
	t.Helper()
	path := prefix + ref + "/permissions"
	var request *http.Request
	if key == nil && !development {
		request = httptest.NewRequest(method, path, nil)
		request.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	} else {
		request = f.literalRequest(user, key, nil)
		request.Method, request.URL.Path = method, path
	}
	statements := 0
	request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
	decodes := source.WorkspaceMetadataDecodes()
	var retained *store.IssueWriteAuthorizationRead
	var handoff *issueRecordAuthorization
	w := &scaleP21SW113Writer{ResponseRecorder: httptest.NewRecorder(), t: t, source: source}
	func() {
		request, closeOutput := beginRequestResourceReads(request)
		defer closeOutput()
		defer closeOutput()
		service := &server{store: source, authDisabled: development}
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/issues/{id}/permissions", func(w http.ResponseWriter, r *http.Request) {
			retained, _ = r.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead)
			handoff, _ = r.Context().Value(issueRecordAuthorizationKey{}).(*issueRecordAuthorization)
			hadRead := retained != nil
			beforeHandler := statements
			service.listIssuePermissions(w, r)
			if source.BoundedMetadataEnabled() {
				retained, _ = r.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead)
				if retained == nil || r.Context().Value(issueRecordAuthorizationContext{}) != nil {
					t.Fatal("legacy permission handler lost its bounded read")
				}
				if _, err := retained.Context(r.Context()); err != nil {
					t.Fatal("legacy permission callback lost its open read", err)
				}
				if hadRead && statements != beforeHandler {
					t.Fatal("legacy permission handler reloaded an existing retained read")
				}
				for _, issue := range retained.Data.Issues {
					if issue.Description != "" || issue.DocumentContent != nil {
						t.Fatal("legacy permission read loaded a complete issue body")
					}
				}
			}
		})
		mux.HandleFunc("GET /api/issue-records/{id}/permissions", func(w http.ResponseWriter, r *http.Request) {
			var ok bool
			handoff, ok = r.Context().Value(issueRecordAuthorizationKey{}).(*issueRecordAuthorization)
			if !ok || handoff.taken.Load() {
				t.Fatal("middleware did not leave the permission alias an unused handoff")
			}
			beforeAlias := statements
			service.issueRecordAlias(func(w http.ResponseWriter, callback *http.Request) {
				if !handoff.taken.Load() || !reflect.DeepEqual(handoff.data, domain.Bootstrap{}) || statements != beforeAlias {
					t.Fatal("permission alias reloaded or failed to consume its one-shot handoff")
				}
				if source.BoundedMetadataEnabled() {
					retained, ok = callback.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead)
					if !ok || retained == nil || callback.Context().Value(issueRecordAuthorizationContext{}) != nil {
						t.Fatal("permission alias lost its bounded marker or advertised a complete snapshot")
					}
					if _, err := retained.Context(callback.Context()); err != nil {
						t.Fatal("permission callback lost its open read", err)
					}
					for _, issue := range retained.Data.Issues {
						if issue.Description != "" || issue.DocumentContent != nil {
							t.Fatal("permission authorization loaded the complete issue body")
						}
					}
				}
				beforeHandler := statements
				service.listIssuePermissions(w, callback)
				if source.BoundedMetadataEnabled() && statements != beforeHandler {
					t.Fatal("permission handler reloaded metadata instead of using retained read.Data")
				}
			})(w, r)
		})
		if development {
			// The isolated workspace owner replaces the global development account.
			if service.authorizeWorkspaceRequest(w, request, user) {
				mux.ServeHTTP(w, request)
			}
		} else if key == nil {
			service.authenticate(mux).ServeHTTP(w, request)
		} else if service.authorizeWorkspaceRequest(w, request, user) {
			mux.ServeHTTP(w, request)
		}
		if source.BoundedMetadataEnabled() && retained != nil && source.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("permission read released before the outer output owner closed")
		}
	}()
	if source.BoundedMetadataEnabled() {
		if source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != decodes {
			t.Fatal("permission GET leaked retained work or decoded complete workspace metadata")
		}
		if retained != nil {
			if !reflect.DeepEqual(retained.Data, domain.Bootstrap{}) {
				t.Fatal("closed permission read retained output data")
			}
			if _, err := retained.Context(request.Context()); err == nil {
				t.Fatal("closed permission read remained usable")
			}
		}
		if handoff != nil && (!handoff.taken.Load() || !reflect.DeepEqual(handoff.data, domain.Bootstrap{})) {
			t.Fatal("permission handoff escaped the request owner")
		}
	}
	if w.Code == http.StatusOK && w.writes == 0 {
		t.Fatal("permission GET did not write its response")
	}
	return scaleP21SW113Response{Code: w.Code, Header: w.Header().Clone(), Body: slices.Clone(w.Body.Bytes())}
}

func TestScaleP21SW113IssuePermissionGETMiddlewareDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		privateTeam := ""
		metadata, ok := repo.WorkspaceMetadata(workspace)
		if !ok {
			t.Fatal("permission fixture workspace missing")
		}
		for _, project := range metadata.Projects {
			if project.ID == "m2-unrelated" {
				privateTeam = project.TeamIDs[0]
			}
		}
		privateAdmin := authClient(t)
		authRequest[domain.AuthSession](t, privateAdmin, http.MethodPost, f.host.URL+"/api/auth/login",
			map[string]string{"email": f.principals[1].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
		create := func(client *http.Client, title, team string, parent *string) domain.Issue {
			return authRequest[domain.Issue](t, client, http.MethodPost, f.host.URL+"/api/issue-records",
				map[string]any{"title": title, "teamId": team, "parentId": parent}, workspace, http.StatusCreated)
		}
		public := create(f.admin, "SW113 full permission output", f.public, nil)
		noGrants := create(f.admin, "SW113 nil permission output", f.public, nil)
		parent := create(privateAdmin, "SW113 private grant parent", privateTeam, nil)
		child := create(privateAdmin, "SW113 inherited permission access", privateTeam, &parent.ID)
		hidden := create(privateAdmin, "SW113 ungranted private permissions", privateTeam, nil)
		stamp := time.Date(2024, time.February, 3, 4, 5, 6, 700, time.UTC)
		publicPermissions := []domain.IssuePermission{
			{ID: "sw113-workspace", IssueID: public.ID, SubjectType: "workspace", SubjectID: workspace, Role: "viewer", CreatedAt: stamp, UpdatedAt: stamp.Add(time.Hour)},
			{ID: "sw113-user", IssueID: public.ID, SubjectType: "user", SubjectID: f.principals[2].user.ID, Role: "editor", CreatedAt: stamp.Add(time.Second), UpdatedAt: stamp.Add(2 * time.Hour)},
			{ID: "sw113-team", IssueID: public.ID, SubjectType: "team", SubjectID: f.public, Role: "commenter", CreatedAt: stamp.Add(2 * time.Second), UpdatedAt: stamp.Add(3 * time.Hour)},
		}
		parentPermissions := []domain.IssuePermission{
			{ID: "sw113-parent-guest", IssueID: parent.ID, SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "viewer", CreatedAt: stamp, UpdatedAt: stamp.Add(4 * time.Hour)},
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "issues"), workspace, "scale_p21.sw113_seed", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Issues {
				issue := &data.Issues[index]
				switch issue.ID {
				case public.ID:
					issue.Identifier = "SW113-113001"
					issue.PreviousIdentifiers = []string{"SWOLD-113001"}
					issue.Permissions = slices.Clone(publicPermissions)
				case parent.ID:
					issue.Permissions = slices.Clone(parentPermissions)
				case noGrants.ID, child.ID, hidden.ID:
					issue.Permissions = nil
				default:
					continue
				}
				issue.Description = strings.Repeat("permission GET must skip complete issue body ", 10000)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		sessions := map[string]string{}
		for _, principal := range f.principals {
			_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal("create real permission session", err)
			}
			sessions[principal.user.ID] = token
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		tests := []struct {
			name, ref string
			want      []domain.IssuePermission
			public    bool
			missing   bool
		}{
			{"public-full", public.ID, publicPermissions, true, false},
			{"public-nil", noGrants.ID, nil, true, false},
			{"private-parent", parent.ID, parentPermissions, false, false},
			{"private-inherited-child", child.ID, nil, false, false},
			{"private-ungranted", hidden.ID, nil, false, false},
			{"missing", "sw113-missing", nil, false, true},
			{"current-identifier", "SW113-113001", publicPermissions, true, false},
			{"previous-identifier", "swold-113001", publicPermissions, true, false},
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, prefix := range []string{"/api/issue-records/", "/api/issues/"} {
				for _, test := range tests {
					t.Run(prefix+test.name, func(t *testing.T) {
						old := scaleP21SW113Serve(t, f, repo, user, key, sessions[user.ID], prefix, test.ref, false)
						bounded := scaleP21SW113Serve(t, f, f.probe, user, key, sessions[user.ID], prefix, test.ref, false)
						if !reflect.DeepEqual(old, bounded) {
							t.Fatalf("actual permission GET middleware/status/header/body differs: literal=%+v bounded=%+v", old, bounded)
						}
						selectedEmpty := key != nil && key.TeamRestriction == "selected" && len(key.TeamIDs) == 0
						if prefix == "/api/issues/" && old.Code != http.StatusForbidden {
							t.Fatalf("authenticated legacy permission denial changed: status=%d body=%s", old.Code, old.Body)
						}
						if prefix == "/api/issue-records/" && test.public && !selectedEmpty && old.Code != http.StatusOK {
							t.Fatalf("visible public permissions status=%d body=%s", old.Code, old.Body)
						}
						if (test.missing || selectedEmpty) && old.Code != http.StatusForbidden {
							t.Fatalf("missing or selected-empty resource status=%d body=%s", old.Code, old.Body)
						}
						if old.Code == http.StatusOK {
							want, err := json.Marshal(test.want)
							if err != nil {
								t.Fatal(err)
							}
							want = append(want, '\n')
							if !bytes.Equal(old.Body, want) {
								t.Fatalf("permission fields, dates, order or null shape changed: got=%s want=%s", old.Body, want)
							}
						}
						// SW287: GET patterns serve HEAD; with the flag on it takes
						// the same retained read instead of the literal bootstrap.
						oldHead := scaleP21SW113ServeMethod(t, f, repo, user, key, sessions[user.ID], prefix, test.ref, false, http.MethodHead)
						boundedHead := scaleP21SW113ServeMethod(t, f, f.probe, user, key, sessions[user.ID], prefix, test.ref, false, http.MethodHead)
						if !reflect.DeepEqual(oldHead, boundedHead) || !reflect.DeepEqual(oldHead, old) {
							t.Fatalf("permission HEAD differs: literal=%+v bounded=%+v GET=%+v", oldHead, boundedHead, old)
						}
						t.Logf("permissions_GET ref=%s credential_selected=%t status=%d bytes=%d", test.name, key != nil && key.TeamRestriction == "selected", old.Code, len(old.Body))
					})
				}
			}
		})
		for _, test := range tests {
			t.Run("development/legacy/"+test.name, func(t *testing.T) {
				old := scaleP21SW113Serve(t, f, repo, f.data.Viewer, nil, "", "/api/issues/", test.ref, true)
				bounded := scaleP21SW113Serve(t, f, f.probe, f.data.Viewer, nil, "", "/api/issues/", test.ref, true)
				if !reflect.DeepEqual(old, bounded) {
					t.Fatalf("development legacy permission GET differs: literal=%+v bounded=%+v", old, bounded)
				}
				oldHead := scaleP21SW113ServeMethod(t, f, repo, f.data.Viewer, nil, "", "/api/issues/", test.ref, true, http.MethodHead)
				boundedHead := scaleP21SW113ServeMethod(t, f, f.probe, f.data.Viewer, nil, "", "/api/issues/", test.ref, true, http.MethodHead)
				if !reflect.DeepEqual(oldHead, boundedHead) || !reflect.DeepEqual(oldHead, old) {
					t.Fatalf("development permission HEAD differs: literal=%+v bounded=%+v GET=%+v", oldHead, boundedHead, old)
				}
				wantCode := http.StatusOK
				if test.missing {
					wantCode = http.StatusNotFound
				}
				if bounded.Code != wantCode {
					t.Fatalf("development permission GET status=%d want=%d body=%s", bounded.Code, wantCode, bounded.Body)
				}
				if wantCode == http.StatusOK {
					want, err := json.Marshal(test.want)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(bounded.Body, append(want, '\n')) {
						t.Fatalf("development permission output changed: got=%s want=%s", bounded.Body, want)
					}
				}
			})
		}
		// Issue JSON omits an empty permissions field. Exercise both in-memory
		// shapes on a real retained read without depending on serialization.
		for _, permissions := range [][]domain.IssuePermission{nil, {}} {
			t.Run(fmt.Sprintf("retained-empty=%t", permissions != nil), func(t *testing.T) {
				request := scaleP21SW47Request(f, f.data.Viewer, nil, http.MethodGet, "/api/issue-records/"+public.ID+"/permissions", public.ID, "", nil)
				request, closeOutput := beginRequestResourceReads(request)
				defer closeOutput()
				service := &server{store: f.probe}
				if _, handled, err := service.boundedIssueWriteAuthorizationMetadata(request); !handled || err != nil {
					t.Fatal("create retained permission read", handled, err)
				}
				read := request.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead)
				index := slices.IndexFunc(read.Data.Issues, func(issue domain.Issue) bool { return issue.ID == public.ID })
				if index < 0 {
					t.Fatal("retained permission fixture missing")
				}
				read.Data.Issues[index].Permissions = permissions
				w := &scaleP21SW113Writer{ResponseRecorder: httptest.NewRecorder(), t: t, source: f.probe}
				service.listIssuePermissions(w, request)
				want := "null\n"
				if permissions != nil {
					want = "[]\n"
				}
				if w.Code != http.StatusOK || w.Body.String() != want {
					t.Fatalf("retained permission collection shape: status=%d got=%s want=%s", w.Code, w.Body, want)
				}
				closeOutput()
				if f.probe.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("empty permission output retained work")
				}
			})
		}
		request := scaleP21SW47Request(f, f.data.Viewer, nil, http.MethodGet, "/api/issue-records/"+public.ID+"/permissions", public.ID, "", nil)
		missingReadSQL := 0
		request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { missingReadSQL++ }))
		beforeMissingDecodes := f.probe.WorkspaceMetadataDecodes()
		w := httptest.NewRecorder()
		(&server{store: f.probe}).listIssuePermissions(w, request)
		if w.Code != http.StatusNotFound || missingReadSQL != 0 || f.probe.WorkspaceMetadataDecodes() != beforeMissingDecodes || f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("bounded permissions GET without retained read did not fail closed")
		}
		t.Run("legacy-canceled-read", func(t *testing.T) {
			request := scaleP21SW47Request(f, f.data.Viewer, nil, http.MethodGet, "/api/issues/"+public.ID+"/permissions", public.ID, "", nil)
			ctx, cancel := context.WithCancel(request.Context())
			cancel()
			request = request.WithContext(ctx)
			request, closeOutput := beginRequestResourceReads(request)
			defer closeOutput()
			w := httptest.NewRecorder()
			(&server{store: f.probe, authDisabled: true}).listIssuePermissions(w, request)
			if w.Body.Len() != 0 || f.probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("canceled legacy permission read wrote a response or leaked work")
			}
		})
		t.Run("legacy-policy-budget", func(t *testing.T) {
			request := scaleP21SW47Request(f, f.data.Viewer, nil, http.MethodGet, "/api/issues/"+public.ID+"/permissions", public.ID, "", nil)
			request = request.WithContext(context.WithValue(request.Context(), issueRequestAccessKey{}, issueRequestAccess{
				workspace: workspace, userID: f.data.Viewer.ID, err: store.ErrMetadataWorkBudget,
			}))
			request, closeOutput := beginRequestResourceReads(request)
			defer closeOutput()
			w := httptest.NewRecorder()
			(&server{store: f.probe, authDisabled: true}).listIssuePermissions(w, request)
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "temporarily unavailable") || f.probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("legacy permission budget failure became empty permission output or leaked work")
			}
		})
	}
}

func TestScaleP21SW113PermissionReadDispatchIsExact(t *testing.T) {
	for _, test := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/api/issue-records/id/permissions", true},
		{http.MethodGet, "/api/issue-records/OLD-113/permissions", true},
		{http.MethodGet, "/api/issue-records/id/permissions/", false},
		{http.MethodGet, "/api/issue-records//permissions", false},
		{http.MethodGet, "/api/issue-records/id/permissions/grant", false},
		{http.MethodGet, "/api/issues/id/permissions", true},
		{http.MethodGet, "/api/issues/id/permissions/", false},
		{http.MethodGet, "/api/issues/id/permissions/grant", false},
		{http.MethodPut, "/api/issues/id/permissions", false},
		{http.MethodDelete, "/api/issues/id/permissions/grant", false},
		{http.MethodHead, "/api/issue-records/id/permissions", true},
		{http.MethodHead, "/api/issues/id/permissions", true},
		{http.MethodHead, "/api/issues/id/permissions/grant", false},
		{http.MethodOptions, "/api/issues/id/permissions", false},
		{http.MethodPut, "/api/issue-records/id/permissions", false},
		{http.MethodPost, "/api/issue-records/id/permissions", false},
		{http.MethodPatch, "/api/issue-records/id/permissions/grant", false},
		{http.MethodDelete, "/api/issue-records/id/permissions/grant", false},
		{http.MethodPatch, "/api/issue-records/id", false},
		{http.MethodPost, "/api/issue-records", false},
		{http.MethodPost, "/api/issue-records/id/comments", false},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		if got := boundedIssuePermissionReadRequest(request); got != test.want {
			t.Errorf("%s %s permission read dispatch=%t want=%t", test.method, test.path, got, test.want)
		}
		if test.want && boundedIssueChildWriteRequest(request) {
			t.Errorf("%s %s changed the child-write classifier", test.method, test.path)
		}
	}
	if !boundedIssueChildWriteRequest(httptest.NewRequest(http.MethodPost, "/api/issue-records/id/comments", nil)) {
		t.Fatal("existing child-write dispatch changed")
	}
}
