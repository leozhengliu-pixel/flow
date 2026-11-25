package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21TeamMemberRequestOutcome(t *testing.T, service *server, request *http.Request) any {
	t.Helper()
	request, finish := beginRequestResourceReads(request)
	defer finish()
	writer := httptest.NewRecorder()
	data, team, close, ok := service.teamForMemberRequest(writer, request)
	if close != nil {
		defer close()
	}
	if ok {
		writeJSON(writer, http.StatusOK, map[string]string{"workspaceId": data.Workspace.ID, "teamId": team.ID, "key": team.Key})
	}
	return []any{ok, writer.Code, scaleP21M3JSON(t, writer.Body.Bytes())}
}

func TestScaleP21HotTeamMemberRequestVisibilityAndCallers(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP21M2Setup(t, repo)
	workspace := f.data.Workspace.URLKey
	prefix := idgen.New("team_request_")
	private, child := "", ""
	policy, ok := repo.IssueAccessMetadata(t.Context(), workspace)
	if !ok {
		t.Fatal("missing policy")
	}
	for _, team := range policy.Teams {
		if team.Private {
			private = team.ID
		}
		if policy.TeamSettings[team.ID].ParentTeamID != "" {
			child = team.ID
		}
	}
	at := time.Date(2026, 11, 14, 1, 2, 3, 4, time.UTC)
	targets := []domain.Team{
		{ID: prefix + "-key-before-id", Key: prefix + "-exact"},
		{ID: prefix + "-exact", Key: "N\x00ATIVE", Name: strings.Repeat("unrelated selected name ", 30000)},
		{ID: prefix + "-first", Key: "DUP-REQUEST"},
		{ID: prefix + "-second", Key: "dup-request"},
		{ID: prefix + "-retired", Key: "RETIRED-REQUEST", RetiredAt: &at},
		{ID: prefix + "-archived", Key: "ARCHIVED-REQUEST", ArchivedAt: &at},
		{ID: prefix + "-external", Key: "EXTERNAL-REQUEST", ExternalSource: "hr:org:node:request"},
	}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "teamSettings", "teamMembers"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, targets...)
		data.TeamSettings[targets[1].ID] = domain.TeamSettings{AgentGuidance: strings.Repeat("ignored guidance ", 50000)}
		data.TeamMembers = append(data.TeamMembers, domain.TeamMember{TeamID: targets[2].ID, UserID: "native\x00member", Role: "owner", Origin: "sync", Managed: true, JoinedAt: at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, targets[4].ID, f.principals[2].user.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
		t.Fatal(err)
	}
	literal, bounded := &server{store: repo}, &server{store: f.probe}
	refs := []string{f.public, private, child, targets[1].ID, "N\x00ATIVE", "NATIVE", "dup-request", targets[4].ID, targets[5].ID, targets[6].ID, "missing"}
	f.matrix(t, func(t *testing.T, user domain.User, credential *domain.APIKey) {
		for _, ref := range refs {
			request := func() *http.Request {
				r := f.literalRequest(user, credential, nil)
				r.Method = http.MethodGet
				r.SetPathValue("id", ref)
				return r
			}
			want := scaleP21TeamMemberRequestOutcome(t, literal, request())
			got := scaleP21TeamMemberRequestOutcome(t, bounded, request())
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("team reference %q differs: %s", ref, scaleP21M2Diff(want, got))
			}
		}
	})
	for _, credential := range []*domain.APIKey{
		{ID: "legacy-selection", TeamIDs: []string{f.public}},
		{ID: "app-selection", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
	} {
		for _, ref := range []string{f.public, private} {
			request := func() *http.Request {
				r := f.literalRequest(f.data.Viewer, credential, nil)
				r.SetPathValue("id", ref)
				return r
			}
			want := scaleP21TeamMemberRequestOutcome(t, literal, request())
			got := scaleP21TeamMemberRequestOutcome(t, bounded, request())
			if !reflect.DeepEqual(want, got) {
				t.Fatal("legacy/application credential narrowing changed", scaleP21M2Diff(want, got))
			}
		}
	}
	installed, err := repo.InstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, domain.ApplicationInstallation{
		ClientID: idgen.New("team-request-client-"), Name: "Team request app", InstalledBy: f.data.Viewer.ID,
		Active: true, AllPublicTeams: true, TeamIDs: []string{f.public}, Scopes: []string{"read"},
	}, "team-request-secret")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.appUsers = append(f.resources.appUsers, struct{ workspace, id string }{workspace, installed.UserID})
	appUser, err := repo.UserByID(t.Context(), installed.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{f.public, private, child, "N\x00ATIVE"} {
		request := func() *http.Request {
			r := f.literalRequest(appUser, &domain.APIKey{ID: "installed-app-key", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "all"}, nil)
			r.SetPathValue("id", ref)
			return r
		}
		want := scaleP21TeamMemberRequestOutcome(t, literal, request())
		got := scaleP21TeamMemberRequestOutcome(t, bounded, request())
		if !reflect.DeepEqual(want, got) {
			t.Fatal("installed application's explicit/public team grants changed", ref, scaleP21M2Diff(want, got))
		}
	}
	// Exercise every direct caller on the same unchanged membership state.
	// DELETE/sync are dry runs; PUT repeats an existing member's role.
	for _, operation := range []struct {
		method string
		path   string
		input  any
		run    func(*server, http.ResponseWriter, *http.Request)
	}{
		{http.MethodGet, "/api/teams/members", nil, (*server).listTeamMemberships},
		{http.MethodPut, "/api/teams/members", domain.TeamMemberInput{Role: "member"}, (*server).putTeamMembership},
		{http.MethodDelete, "/api/teams/members?dryRun=true", nil, (*server).deleteTeamMembership},
		{http.MethodPost, "/api/teams/members/sync", domain.TeamMemberSyncInput{Members: []string{f.principals[2].user.ID}, DryRun: true}, (*server).syncTeamMemberships},
	} {
		var outcomes []any
		for _, service := range []*server{literal, bounded} {
			raw, err := json.Marshal(operation.input)
			if err != nil {
				t.Fatal(err)
			}
			r := f.literalRequest(f.data.Viewer, nil, nil)
			r.URL = httptest.NewRequest(operation.method, operation.path, nil).URL
			r.Method, r.Body = operation.method, http.NoBody
			if operation.input != nil {
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
			r.SetPathValue("id", f.public)
			r.SetPathValue("userId", f.principals[2].user.ID)
			r, close := beginRequestResourceReads(r)
			scope := r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
			writer := scaleP21MemberEncodingWriter{ResponseRecorder: httptest.NewRecorder(), check: func() {
				if service == bounded && (scope.closed || len(scope.closers) == 0) {
					t.Fatal("team request inputs released before caller encoding")
				}
			}}
			operation.run(service, writer, r)
			outcomes = append(outcomes, []any{writer.Code, scaleP21M3JSON(t, writer.Body.Bytes())})
			close()
		}
		if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
			t.Fatalf("direct %s caller differs: %s", operation.method, scaleP21M2Diff(outcomes[0], outcomes[1]))
		}
	}
	// A one-shot authorization projection remains the handler's source.
	for _, service := range []*server{literal, bounded} {
		r := f.literalRequest(f.data.Viewer, nil, nil)
		r.SetPathValue("id", "handoff-only")
		r, close := beginRequestResourceReads(r)
		handOffPagedMetadata(r, workspace, f.data.Viewer.ID, domain.Bootstrap{
			Workspace: f.data.Workspace, Teams: []domain.Team{{ID: "handoff-only", Key: "HANDOFF"}},
		})
		first := scaleP21TeamMemberRequestOutcome(t, service, r)
		second := scaleP21TeamMemberRequestOutcome(t, service, r)
		if first.([]any)[0] != true || second.([]any)[0] != false {
			close()
			t.Fatal("authorization handoff was ignored or consumed twice", first, second)
		}
		close()
	}
	if f.probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("team membership request decoded complete metadata")
	}
}

func TestScaleP21HotTeamMemberRequestDevelopmentAndErrors(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	view, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace, target := f.data.Workspace.URLKey, idgen.New("team_request_development_")
	when := time.Date(2026, 11, 14, 1, 2, 3, 456789123, time.FixedZone("native development zone", 7*3600+123))
	archived := when.UTC()
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "teamMembers"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: target, Key: "DEV\x00KEY", ArchivedAt: &archived})
		data.TeamMembers = append(data.TeamMembers,
			domain.TeamMember{TeamID: target, UserID: "native\x00user", Role: "owner", Origin: "sync", Managed: true, JoinedAt: when},
			domain.TeamMember{TeamID: target + "-other", UserID: strings.Repeat("unused member ", 100000), JoinedAt: when},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The lifecycle timestamp is not a selected membership input. Its
	// presence is sufficient for auth-disabled visibility, which includes it.
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{workspace, "", workspace + "-missing"} {
		for _, ref := range []string{target, "DEV\x00KEY", "DEVKEY", "missing"} {
			var outcomes []any
			for _, source := range []*store.SQLiteStore{repo, view} {
				r := httptest.NewRequest(http.MethodGet, "/api/teams/members", nil)
				r.Header.Set("X-Workspace-Key", key)
				r.SetPathValue("id", ref)
				r = r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{}}))
				writer := httptest.NewRecorder()
				(&server{store: source, authDisabled: true}).listTeamMemberships(writer, r)
				outcomes = append(outcomes, []any{writer.Code, writer.Body.String()})
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("development source key=%q ref=%q differs: %s", key, ref, scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
		}
	}
	for _, key := range []string{"", workspace + "-missing"} {
		var outcomes []any
		for _, source := range []*store.SQLiteStore{repo, view} {
			r := httptest.NewRequest(http.MethodGet, "/api/teams/members", nil)
			r.Header.Set("X-Workspace-Key", key)
			r.SetPathValue("id", target)
			r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, f.data.Viewer))
			outcomes = append(outcomes, scaleP21TeamMemberRequestOutcome(t, &server{store: source}, r))
		}
		if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
			t.Fatal("authenticated missing-workspace error changed", key, scaleP21M2Diff(outcomes[0], outcomes[1]))
		}
	}
}
