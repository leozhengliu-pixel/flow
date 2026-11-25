package main

import (
	"bytes"
	"context"
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

type scaleP21SW120Output struct {
	Viewer domain.User
	OAuth  domain.User
	Via    *domain.ActorBot
	Audit  store.AuditRequest
}

type scaleP21SW120Result struct {
	Code   int
	Header http.Header
	Body   []byte
	Called bool
}

func scaleP21SW120NativeUser(user domain.User, suffix string) domain.User {
	stamp := time.Date(2026, time.November, 15, 1, 2, 3, 456789123, time.FixedZone("sw120", 20700))
	user.Active, user.Deleted, user.App, user.BuiltinAgent = false, true, true, true
	user.UserID, user.OAuthClientID = idgen.New("sw120-native-user-"), idgen.New("sw120-client-")
	user.Name, user.DisplayName, user.Username = "Native\x00"+suffix, "Display "+suffix, "handle-"+suffix
	user.AppScopes = []string{"read", "", "write", "write", "\xffnative"}
	user.AppTeamIDs = []string{idgen.New("sw120-team-"), "", "same", "same"}
	user.AppAllPublicTeams = true
	user.OutOfOfficeUntil = &stamp
	user.Via = &domain.ActorBot{
		ID: idgen.New("sw120-bot-"), Type: domain.ActorBotTypeAgent,
		Name: "Native bot", AvatarURL: "native-avatar", UserDisplayName: "External",
		UserAvatarURL: "external-avatar", OnBehalfOfID: user.ID, OnBehalfOfName: user.DisplayName,
	}
	return user
}

func scaleP21SW120AssertUser(t *testing.T, got, want domain.User) {
	t.Helper()
	if !reflect.DeepEqual(got, want) || got.Via != want.Via || got.OutOfOfficeUntil != want.OutOfOfficeUntil {
		t.Fatalf("request viewer differs from its literal source: got=%#v want=%#v", got, want)
	}
	for _, pair := range [][2][]string{{got.AppScopes, want.AppScopes}, {got.AppTeamIDs, want.AppTeamIDs}} {
		if cap(pair[0]) != cap(pair[1]) || len(pair[0]) > 0 && &pair[0][0] != &pair[1][0] {
			t.Fatal("request viewer changed native scope/team slice ownership")
		}
	}
}

func scaleP21SW120Authenticate(t *testing.T, source *store.SQLiteStore, want, decoy domain.User, path, workspace, authorization string, canceled bool) scaleP21SW120Result {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "198.51.100.42:1234"
	request.Header.Set("User-Agent", "sw120-fixture")
	request.Header.Set("X-Client-ID", " sw120-client ")
	request.Header.Set("X-Workspace-Key", workspace)
	request.Header.Set("Authorization", authorization)
	request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, decoy))
	if canceled {
		ctx, cancel := context.WithCancel(request.Context())
		cancel()
		request = request.WithContext(ctx)
	}
	statements, countStatements := 0, 0
	// Account count reads use Background; this observes request SQL only.
	// SW118 separately proves the scalar getter's native/no-SQL contract.
	request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(query string) {
		statements++
		if bytes.Contains([]byte(query), []byte("issue_collection_counts")) {
			countStatements++
		}
	}))
	beforeDecodes := source.WorkspaceMetadataDecodes()
	beforeWork := source.MetadataWorkUsedForTesting()
	service := &server{store: source, authDisabled: true}
	w := httptest.NewRecorder()
	result := scaleP21SW120Result{}
	var scope *requestResourceReads
	service.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result.Called = true
		viewer := authUser(r)
		scaleP21SW120AssertUser(t, viewer, want)
		oauth := service.oauthRequestUser(r)
		scaleP21SW120AssertUser(t, oauth, want)
		audit, ok := store.AuditRequestFrom(r.Context())
		if !ok || audit.UserID != want.ID || audit.ActorID != want.ID || audit.ActorType != store.AuditActorUser || audit.IP != "198.51.100.0" {
			t.Fatalf("development viewer lost audit context: %+v", audit)
		}
		scope, _ = r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
		if source.BoundedMetadataEnabled() && (scope == nil || scope.closed) {
			t.Fatal("bounded authentication did not retain an open request output scope")
		}
		writeJSON(w, http.StatusOK, scaleP21SW120Output{Viewer: viewer, OAuth: oauth, Via: viewer.Via, Audit: audit})
	})).ServeHTTP(w, request)
	if source.BoundedMetadataEnabled() {
		if source.WorkspaceMetadataDecodes() != beforeDecodes || source.MetadataWorkUsedForTesting() != beforeWork {
			t.Fatal("default viewer authentication decoded full metadata or retained work")
		}
		if scope != nil && !scope.closed {
			t.Fatal("authentication left its request output scope open")
		}
		if workspace == "" && (statements != 0 || countStatements != 0) {
			t.Fatalf("viewer-only authentication observed SQL=%d count_SQL=%d", statements, countStatements)
		}
	}
	result.Code, result.Header, result.Body = w.Code, w.Header().Clone(), slices.Clone(w.Body.Bytes())
	t.Logf("bounded=%t bound_workspace=%t canceled=%t observed_request_SQL=%d observed_count_SQL=%d status=%d handler=%t",
		source.BoundedMetadataEnabled(), workspace != "", canceled, statements, countStatements, result.Code, result.Called)
	return result
}

func TestScaleP21SW120DefaultViewerAuthenticationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		// Each source's literal Account viewer is the oracle; no default identity
		// is assumed or changed. All new workspace/identity resources are owned.
		literal := repo.Account().Viewer
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		boundedLiteral := view.Account().Viewer
		if !reflect.DeepEqual(literal, boundedLiteral) {
			t.Fatal("paired default viewer oracles differ")
		}
		decoy := scaleP21SW120NativeUser(f.data.Viewer, "decoy")
		for _, test := range []struct {
			name, path, workspace, authorization string
			canceled                             bool
			code                                 int
			called                               bool
		}{
			{"viewer-only", "/api/sw120-viewer", "", "", false, http.StatusOK, true},
			{"viewer-only-credential-bypass", "/api/sw120-viewer", "", "Bearer sw120-unrelated", false, http.StatusOK, true},
			{"workspace-access-status", "/api/workspaces/" + f.data.Workspace.URLKey + "/access-status", f.data.Workspace.URLKey, "", false, http.StatusOK, true},
			{"unrelated-fixture-workspace", "/api/sw120-viewer", f.data.Workspace.URLKey, "", false, http.StatusForbidden, false},
			{"missing-workspace", "/api/sw120-viewer", idgen.New("sw120-missing-"), "", false, http.StatusNotFound, false},
			{"canceled-viewer-only", "/api/sw120-viewer", "", "", true, http.StatusServiceUnavailable, false},
		} {
			t.Run(test.name, func(t *testing.T) {
				old := scaleP21SW120Authenticate(t, repo, literal, decoy, test.path, test.workspace, test.authorization, test.canceled)
				bounded := scaleP21SW120Authenticate(t, view, boundedLiteral, decoy, test.path, test.workspace, test.authorization, test.canceled)
				if !reflect.DeepEqual(old, bounded) {
					t.Fatalf("actual development authentication/context/output differs: literal=%+v bounded=%+v", old, bounded)
				}
				if old.Code != test.code || old.Called != test.called {
					t.Fatalf("development authorization status=%d handler=%t want=%d/%t body=%s", old.Code, old.Called, test.code, test.called, old.Body)
				}
			})
		}
	}
}

func TestScaleP21SW120OAuthRequestUserLiteralDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		native := scaleP21SW120NativeUser(f.data.Viewer, "oauth")
		for _, source := range []*store.SQLiteStore{repo, view} {
			wantDefault := source.Account().Viewer
			for _, development := range []bool{false, true} {
				request := httptest.NewRequest(http.MethodGet, "/api/oauth/authorization-request", nil)
				request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, native))
				statements := 0
				request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
				beforeDecodes := source.WorkspaceMetadataDecodes()
				beforeWork := source.MetadataWorkUsedForTesting()
				service := &server{store: source, authDisabled: development}
				want := native
				if development {
					want = wantDefault
				}
				got := service.oauthRequestUser(request)
				scaleP21SW120AssertUser(t, got, want)
				if statements != 0 || source.WorkspaceMetadataDecodes() != beforeDecodes || source.MetadataWorkUsedForTesting() != beforeWork {
					t.Fatal("OAuth viewer helper observed request SQL, decoded full metadata, or retained work")
				}
				// The native branch must keep inactive/deleted state, scopes and
				// non-JSON Via attribution without normalizing or copying them.
				if !development && (got.Active || !got.Deleted || got.Via == nil || got.AppScopes[4] != "\xffnative") {
					t.Fatal("OAuth native user lost inactive or attribution/scope fields")
				}
			}
		}
	}
}
