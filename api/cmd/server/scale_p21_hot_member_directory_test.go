package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21MemberNameScalars(names teamMemberNames) teamMemberNames {
	result := teamMemberNames{users: map[string]domain.User{}, teams: map[string]domain.Team{}}
	for id, user := range names.users {
		result.users[id] = domain.User{ID: user.ID, Name: user.Name, DisplayName: user.DisplayName, Email: user.Email}
	}
	for id, team := range names.teams {
		result.teams[id] = domain.Team{ID: team.ID, Name: team.Name, Key: team.Key}
	}
	return result
}

func TestScaleP21HotMemberDirectoryNamesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP21M2Setup(t, repo)
	workspace := f.data.Workspace.URLKey
	for _, development := range []bool{false, true} {
		literal, bounded := &server{store: repo, authDisabled: development}, &server{store: f.probe, authDisabled: development}
		f.matrix(t, func(t *testing.T, user domain.User, credential *domain.APIKey) {
			request, close := beginRequestResourceReads(f.literalRequest(user, credential, nil))
			defer close()
			want, got := literal.teamMemberNames(request.Context(), workspace), bounded.teamMemberNames(request.Context(), workspace)
			defer want.closeUnscoped()
			defer got.closeUnscoped()
			if got.err != nil || !reflect.DeepEqual(scaleP21MemberNameScalars(want), scaleP21MemberNameScalars(got)) {
				t.Fatal("trusted names depend on principal/key or lost display scalars", got.err)
			}
			changes := []domain.TeamMemberChange{
				{TeamID: f.public, UserID: f.data.Viewer.ID, Action: domain.TeamMemberRemoved, RoleBefore: "member"},
				{TeamID: f.public, UserID: f.data.Viewer.ID, TeamKey: "first key", UserName: "first snapshot", Action: domain.TeamMemberAdded, RoleAfter: "member"},
				{TeamID: "missing", UserID: "missing"},
			}
			left, right := append([]domain.TeamMemberChange(nil), changes...), append([]domain.TeamMemberChange(nil), changes...)
			want.decorate(left)
			got.decorate(right)
			if !reflect.DeepEqual(left, right) || right[1].UserName != "first snapshot" || right[1].TeamKey != "first key" {
				t.Fatal("in-place decoration or first-snapshot precedence changed")
			}
			actor := requestTeamMemberActor(request, "sync")
			before, after := domain.Bootstrap{Viewer: f.data.Viewer}, domain.Bootstrap{Viewer: f.data.Viewer}
			appendTeamMemberAudit(&before, actor, want, teamMemberReasonTeamSync, left, nil)
			appendTeamMemberAudit(&after, actor, got, teamMemberReasonTeamSync, right, nil)
			if len(before.AuditLog) != len(after.AuditLog) {
				t.Fatal("audit entries changed")
			}
			for index := range before.AuditLog {
				if !reflect.DeepEqual(before.AuditLog[index].Metadata, after.AuditLog[index].Metadata) {
					t.Fatal("audit user email/name/team metadata changed")
				}
			}
			if got.close != nil {
				t.Fatal("request-owned names released locally")
			}
		})
	}
	if f.probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("name directory decoded complete metadata")
	}
}

type scaleP21MemberEncodingWriter struct {
	*httptest.ResponseRecorder
	check func()
}

func (w scaleP21MemberEncodingWriter) Write(body []byte) (int, error) {
	w.check()
	return w.ResponseRecorder.Write(body)
}

func TestScaleP21HotMemberDirectoryResponseLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, userID := f.data.Workspace.URLKey, f.data.Viewer.ID
	seed := func(display string) {
		t.Helper()
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "users"), workspace, "scale_p21.member_response_names", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Users {
				if data.Users[index].ID == userID {
					data.Users[index].Name, data.Users[index].DisplayName = display, display
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("first snapshot")
	view, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bounded := &server{store: view, authDisabled: true}
	request, close := beginRequestResourceReads(httptest.NewRequest("DELETE", "/api/teams/member-names", nil))
	defer close()
	scope := request.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
	changes := []domain.TeamMemberChange{{UserID: userID, TeamID: f.data.Teams[0].ID, Action: domain.TeamMemberRemoved}}
	alias := changes
	// The unchanged audit writer remains an independent migration blocker.
	// Even its best-effort failure must leave the original response slice
	// decorated, with the naming read still owned by the outer response.
	bounded.recordTeamMemberAudit(request.Context(), workspace, teamMemberAuditActor{actor: f.data.Viewer}, "", changes, nil)
	if alias[0].UserName != "first snapshot" || len(scope.closers) != 1 || scope.closed {
		t.Fatal("audit helper copied changes or released response strings early")
	}
	seed("second snapshot")
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
		t.Fatal(err)
	}
	names := bounded.teamMemberNames(request.Context(), workspace)
	if names.err != nil {
		t.Fatal(names.err)
	}
	names.decorate(changes)
	second := []domain.TeamMemberChange{{UserID: userID, TeamID: f.data.Teams[0].ID}}
	names.decorate(second)
	if changes[0].UserName != "first snapshot" || second[0].UserName != "second snapshot" {
		t.Fatal("second names read replaced the audit's first-snapshot response value")
	}
	names.closeUnscoped()
	if scope.closed || len(scope.closers) != 2 {
		t.Fatal("local cleanup released the outer response's owners")
	}
	writer := scaleP21MemberEncodingWriter{ResponseRecorder: httptest.NewRecorder(), check: func() {
		if scope.closed || len(scope.closers) != 2 {
			t.Fatal("member names admission ended before response encoding")
		}
	}}
	writeJSON(writer, 200, map[string]any{"removed": changes, "kept": second})
	if !bytes.Contains(writer.Body.Bytes(), []byte("first snapshot")) || !bytes.Contains(writer.Body.Bytes(), []byte("second snapshot")) {
		t.Fatal("decorated response strings did not reach encoding")
	}
	close()
	if !scope.closed || len(scope.closers) != 0 {
		t.Fatal("response did not release member name owners")
	}
	// Unscoped audit/background callers consume no decorated output after
	// recording. Their local owner remains explicit and closes at that boundary.
	local := bounded.teamMemberNames(context.Background(), workspace)
	if local.err != nil || local.close == nil {
		t.Fatal("unscoped naming read has no local release", local.err)
	}
	local.closeUnscoped()
}
