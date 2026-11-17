package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// scaleP21SW274InWrapper reports whether the calling goroutine is inside one
// of the compatibility wrappers or the literal permission lookup. The SQL
// observer runs on the goroutine that issues the statement, so any statement a
// wrapper causes is attributed to it.
func scaleP21SW274InWrapper() bool {
	pcs := make([]uintptr, 256)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for {
		frame, more := frames.Next()
		for _, name := range []string{"(*server).workspaceData", "(*server).mcpWorkspaceData", "(*server).findIssueForPermissions"} {
			if strings.HasSuffix(frame.Function, name) {
				return true
			}
		}
		if !more {
			return false
		}
	}
}

type scaleP21SW274Outcome struct {
	Code   int
	Header http.Header
	Body   []byte
}

func scaleP21SW274Serve(t *testing.T, f scaleP21M2Fixture, source *store.SQLiteStore, user domain.User, key *domain.APIKey, token, method, path string, development bool, prepare func(*http.Request) *http.Request) scaleP21SW274Outcome {
	t.Helper()
	var request *http.Request
	if key == nil && !development {
		request = httptest.NewRequest(method, path, nil)
		request.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	} else {
		request = f.literalRequest(user, key, nil)
		request.Method, request.URL.Path = method, path
	}
	inWrapper, statements := 0, 0
	request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) {
		statements++
		if scaleP21SW274InWrapper() {
			inWrapper++
		}
	}))
	if prepare != nil {
		request = prepare(request)
	}
	decodes := source.WorkspaceMetadataDecodes()
	w := httptest.NewRecorder()
	func() {
		request, closeOutput := beginRequestResourceReads(request)
		defer closeOutput()
		service := &server{store: source, authDisabled: development}
		mux := http.NewServeMux()
		// The production bindings: a GET pattern also serves HEAD.
		mux.HandleFunc("GET /api/issue-records/{id}/permissions", service.issueRecordAlias(service.listIssuePermissions))
		mux.HandleFunc("GET /api/issues/{id}/permissions", service.listIssuePermissions)
		switch {
		case development:
			if service.authorizeWorkspaceRequest(w, request, user) {
				mux.ServeHTTP(w, request)
			}
		case key == nil:
			service.authenticate(mux).ServeHTTP(w, request)
		default:
			if service.authorizeWorkspaceRequest(w, request, user) {
				mux.ServeHTTP(w, request)
			}
		}
	}()
	if source.BoundedMetadataEnabled() {
		if inWrapper != 0 {
			t.Fatalf("%s %s ran %d statements inside a compatibility wrapper with the flag on", method, path, inWrapper)
		}
		if source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != decodes {
			t.Fatalf("%s %s leaked retained work or decoded the complete workspace", method, path)
		}
	}
	return scaleP21SW274Outcome{Code: w.Code, Header: w.Header().Clone(), Body: slices.Clone(w.Body.Bytes())}
}

// TestScaleP21SW274IssuePermissionListHeadAndGetDifferential drives the HOT
// permission list routes, GET and the HEAD their GET patterns also serve,
// through the real middleware for every principal and credential. The literal
// flag-off answer, the bounded answer and the bounded HEAD answer must agree
// byte for byte, and with the flag on no statement may run inside
// workspaceData or findIssueForPermissions (before this change a bounded HEAD
// hydrated the workspace through them).
func TestScaleP21SW274IssuePermissionListHeadAndGetDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		metadata, ok := repo.WorkspaceMetadata(workspace)
		if !ok {
			t.Fatal("permission fixture workspace missing")
		}
		privateTeam := ""
		for _, project := range metadata.Projects {
			if project.ID == "m2-unrelated" {
				privateTeam = project.TeamIDs[0]
			}
		}
		privateAdmin := authClient(t)
		authRequest[domain.AuthSession](t, privateAdmin, http.MethodPost, f.host.URL+"/api/auth/login",
			map[string]string{"email": f.principals[1].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
		create := func(client *http.Client, title, team string) domain.Issue {
			return authRequest[domain.Issue](t, client, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": title, "teamId": team}, workspace, http.StatusCreated)
		}
		public := create(f.admin, "SW274 public grants", f.public)
		empty := create(f.admin, "SW274 public without grants", f.public)
		private := create(privateAdmin, "SW274 private grant", privateTeam)
		hidden := create(privateAdmin, "SW274 private without grant", privateTeam)
		stamp := time.Date(2025, time.March, 4, 5, 6, 7, 800, time.UTC)
		grants := map[string][]domain.IssuePermission{
			public.ID: {
				{ID: "sw274-workspace", IssueID: public.ID, SubjectType: "workspace", SubjectID: workspace, Role: "viewer", CreatedAt: stamp, UpdatedAt: stamp.Add(time.Hour)},
				{ID: "sw274-user", IssueID: public.ID, SubjectType: "user", SubjectID: f.principals[2].user.ID, Role: "editor", CreatedAt: stamp, UpdatedAt: stamp},
				{ID: "sw274-team", IssueID: public.ID, SubjectType: "team", SubjectID: f.public, Role: "commenter", CreatedAt: stamp.Add(time.Second), UpdatedAt: stamp.Add(2 * time.Hour)},
			},
			private.ID: {{ID: "sw274-guest", IssueID: private.ID, SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "viewer", CreatedAt: stamp, UpdatedAt: stamp}},
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "issues"), workspace, "scale_p21.sw274_seed", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Issues {
				issue := &data.Issues[index]
				switch issue.ID {
				case public.ID:
					issue.Identifier = "SW274-274001"
				case empty.ID, private.ID, hidden.ID:
				default:
					continue
				}
				issue.Permissions = slices.Clone(grants[issue.ID])
				issue.Description = strings.Repeat("permission list never loads the issue body ", 5000)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		sessions := map[string]string{}
		for _, principal := range f.principals {
			_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			sessions[principal.user.ID] = token
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		refs := []struct{ name, ref string }{
			{"public-grants", public.ID}, {"public-empty", empty.ID}, {"private-grant", private.ID},
			{"private-hidden", hidden.ID}, {"missing", "sw274-missing"}, {"identifier", "SW274-274001"},
		}
		ok200 := 0
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, prefix := range []string{"/api/issue-records/", "/api/issues/"} {
				for _, ref := range refs {
					t.Run(prefix+ref.name, func(t *testing.T) {
						path := prefix + ref.ref + "/permissions"
						get := scaleP21SW274Serve(t, f, repo, user, key, sessions[user.ID], http.MethodGet, path, false, nil)
						for _, method := range []string{http.MethodGet, http.MethodHead} {
							old := scaleP21SW274Serve(t, f, repo, user, key, sessions[user.ID], method, path, false, nil)
							bounded := scaleP21SW274Serve(t, f, f.probe, user, key, sessions[user.ID], method, path, false, nil)
							if !reflect.DeepEqual(old, bounded) {
								t.Fatalf("%s differs: literal=%d %v %s bounded=%d %v %s", method, old.Code, old.Header, old.Body, bounded.Code, bounded.Header, bounded.Body)
							}
							if !reflect.DeepEqual(old, get) {
								t.Fatalf("%s answer differs from GET: %d %s vs %d %s", method, old.Code, old.Body, get.Code, get.Body)
							}
						}
						if get.Code == http.StatusOK {
							ok200++
							want, _ := json.Marshal(grants[ref.ref])
							if ref.name == "identifier" {
								want, _ = json.Marshal(grants[public.ID])
							}
							if !bytes.Equal(get.Body, append(want, '\n')) {
								t.Fatalf("permission output changed: got=%s want=%s", get.Body, want)
							}
						}
					})
				}
			}
		})
		if ok200 == 0 {
			t.Fatal("no principal saw a permission list; the differential compared only refusals")
		}
		for _, ref := range refs {
			t.Run("development/"+ref.name, func(t *testing.T) {
				path := "/api/issues/" + ref.ref + "/permissions"
				get := scaleP21SW274Serve(t, f, repo, f.data.Viewer, nil, "", http.MethodGet, path, true, nil)
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					old := scaleP21SW274Serve(t, f, repo, f.data.Viewer, nil, "", method, path, true, nil)
					bounded := scaleP21SW274Serve(t, f, f.probe, f.data.Viewer, nil, "", method, path, true, nil)
					if !reflect.DeepEqual(old, bounded) || !reflect.DeepEqual(old, get) {
						t.Fatalf("development %s differs: literal=%d %s bounded=%d %s get=%d %s", method, old.Code, old.Body, bounded.Code, bounded.Body, get.Code, get.Body)
					}
				}
				want := http.StatusOK
				if ref.name == "missing" {
					want = http.StatusNotFound
				}
				if get.Code != want {
					t.Fatalf("development permission status=%d want=%d body=%s", get.Code, want, get.Body)
				}
			})
		}
		// A refused budget stays the same bounded refusal for HEAD as for GET.
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run("budget/"+method, func(t *testing.T) {
				outcome := scaleP21SW274Serve(t, f, f.probe, f.data.Viewer, nil, "", method, "/api/issues/"+public.ID+"/permissions", true, func(r *http.Request) *http.Request {
					return r.WithContext(context.WithValue(r.Context(), issueRequestAccessKey{}, issueRequestAccess{
						workspace: workspace, userID: f.data.Viewer.ID, err: store.ErrMetadataWorkBudget,
					}))
				})
				if outcome.Code != http.StatusServiceUnavailable || !strings.Contains(string(outcome.Body), "temporarily unavailable") {
					t.Fatalf("%s budget refusal status=%d body=%s", method, outcome.Code, outcome.Body)
				}
			})
		}
	}
}

// TestScaleP21SW274SnapshotReservationDifferential compares the bounded
// estimator branch with the store estimator it replaces: a bounded store's
// estimate is the fixed maximum for every key, a literal store's estimate is
// still the store's own measurement, and the storeless fallbacks are
// unchanged. The admission middleware then admits a bounded bootstrap.
func TestScaleP21SW274SnapshotReservationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			for _, key := range []string{f.data.Workspace.URLKey, "", "sw274-missing"} {
				request := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
				if key != "" {
					request.Header.Set("X-Workspace-Key", key)
				}
				got := (&server{store: source}).hydratedLegacySnapshotReservation(request)
				if want := source.WorkspaceReadSnapshotReservation(key); got != want {
					t.Fatalf("bounded=%t key=%q estimate=%d store=%d", source.BoundedMetadataEnabled(), key, got, want)
				}
				if source.BoundedMetadataEnabled() && got != store.HydratedSnapshotReservationMaxBytes {
					t.Fatalf("bounded estimate=%d", got)
				}
			}
		}
		if literal := (&server{store: repo}).hydratedLegacySnapshotReservation(httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)); literal == store.HydratedSnapshotReservationMaxBytes {
			t.Log("literal fixture estimate reached the maximum; the measured branch is still compared above")
		}
		request := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
		if got := (&server{}).hydratedLegacySnapshotReservation(request); got != store.HydratedSnapshotReservationMaxBytes {
			t.Fatalf("storeless default estimate=%d", got)
		}
		request = request.WithContext(context.WithValue(request.Context(), hydratedLegacyEstimateKey{}, int64(12345)))
		if got := (&server{}).hydratedLegacySnapshotReservation(request); got != 12345 {
			t.Fatalf("storeless context estimate=%d", got)
		}
		service := &server{store: f.probe}
		served := false
		handler := boundHydratedLegacyDownloads(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			served = r.Context().Value(hydratedLegacyDownloadKey{}) == true
			w.WriteHeader(http.StatusNoContent)
		}), service.hydratedLegacySnapshotReservation)
		w := httptest.NewRecorder()
		bootstrap := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
		bootstrap.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		handler.ServeHTTP(w, bootstrap)
		if w.Code != http.StatusNoContent || !served {
			t.Fatalf("bounded bootstrap admission status=%d admitted=%t", w.Code, served)
		}
	}
}
