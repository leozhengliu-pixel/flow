package main

import (
	"errors"
	"net/http"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP09GatesDisabledAuthorityUsesLegacyReads(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "off")
	t.Setenv("FLOW_SYNC_V2", "off")
	t.Setenv("FLOW_SYNC_MANIFEST", "off")
	t.Setenv("FLOW_INDEXED_READS", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := apiKeyScopeFixture(t, repo)
	workspace, principal := f.data.Workspace.URLKey, f.data.Viewer.ID
	access, err := repo.ResolveAccess(t.Context(), workspace, principal, nil)
	if err != nil {
		t.Fatal(err)
	}
	type compatViewer struct {
		client *http.Client
		token  string
	}
	viewers := []compatViewer{{client: f.admin}}
	for _, restriction := range []string{"all", "selected"} {
		for _, teams := range [][]string{nil, {"scope-public"}} {
			token, _ := apiKeyScopeCredential(t, f, "user", restriction, teams, []string{"read"})
			viewers = append(viewers, compatViewer{token: token})
		}
	}
	if err := repo.MutateWorkspace(t.Context(), workspace, "test.compat_child", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "scope-child", Key: "SCC"})
		data.TeamSettings["scope-child"] = domain.TeamSettings{TeamID: "scope-child", ParentTeamID: "scope-private", Access: "public"}
		data.Projects = append(data.Projects, domain.Project{ID: "scope-child-project", TeamIDs: []string{"scope-child"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, viewer := range []struct {
		name, role string
		teams      []string
	}{
		{"admin-private-outsider", "admin", []string{"scope-public"}},
		{"member-private-outsider", "member", []string{"scope-public"}},
		{"guest-explicit-teams", "guest", []string{"scope-public", "scope-private"}},
		{"subteam-inheritance", "member", []string{"scope-private"}},
		{"guest-explicit-grant", "guest", []string{"scope-public"}},
	} {
		client, user := scaleP03Join(t, f, viewer.name, idgen.New("p09-compat-")+"@example.test", viewer.role, viewer.teams...)
		viewers = append(viewers, compatViewer{client: client})
		if viewer.name == "guest-explicit-grant" {
			authRequest[[]domain.IssuePermission](t, f.admin, http.MethodPut, f.host.URL+"/api/issues/scope-hidden-issue/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": user.ID, "role": "viewer"}}}, workspace, http.StatusOK)
		}
	}
	foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Compatibility foreign workspace", idgen.New("p09-foreign-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
	// An older replica starting with the flag off disables database-wide
	// epoch authority, although this process retains its local ready bit.
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	older, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = older.Close() })
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	if !repo.RecordJournalReady() {
		t.Fatal("fixture lost the newer replica's local ready state")
	}
	if _, err := repo.AuthzEpoch(t.Context(), f.data.Workspace.ID); !errors.Is(err, store.ErrRecordJournalUnavailable) {
		t.Fatalf("disabled authority was accepted: %v", err)
	}
	if _, err := repo.ResolveAccess(t.Context(), workspace, principal, nil); !errors.Is(err, store.ErrRecordJournalUnavailable) {
		t.Fatalf("disabled authority unexpectedly resolved a journal context: %v", err)
	}
	if err := repo.ValidateAccess(t.Context(), access); !errors.Is(err, store.ErrRecordJournalUnavailable) {
		t.Fatalf("queued access survived disabled authority: %v", err)
	}
	comparisons, matched := 0, 0
	for _, viewer := range viewers {
		for _, path := range []string{
			"/api/projects/scope-allowed-project",
			"/api/projects/scope-hidden-project",
			"/api/projects/scope-project",
			"/api/projects/scope-old-project",
			"/api/projects/scope-child-project",
			"/api/initiatives/scope-initiative",
			"/api/initiatives/scope-hidden-initiative",
		} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, target := range []string{workspace, foreign.Workspace.URLKey} {
					t.Setenv("FLOW_RECORD_ACCESS", "off")
					legacy := scaleP03Read(t, f, viewer.client, viewer.token, method, path, target)
					t.Setenv("FLOW_RECORD_ACCESS", "on")
					current := scaleP03Read(t, f, viewer.client, viewer.token, method, path, target)
					comparisons++
					if current.Code != legacy.Code || current.Body.String() != legacy.Body.String() {
						t.Errorf("disabled-authority read differs from legacy: method=%s path=%s status=%d want=%d", method, path, current.Code, legacy.Code)
					} else {
						matched++
					}
				}
			}
		}
	}
	t.Logf("disabled-authority legacy comparisons=%d matched=%d", comparisons, matched)
	if err := older.SetTeamMembership(t.Context(), f.data.Workspace.ID, "scope-private", principal, "member", false); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"off", "on"} {
		t.Setenv("FLOW_RECORD_ACCESS", mode)
		response := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/projects/scope-hidden-project", workspace)
		if response.Code != http.StatusForbidden {
			t.Fatalf("old replica's private-team revocation was ignored: mode=%s status=%d", mode, response.Code)
		}
	}
	if _, err := repo.ReadCommittedChanges(t.Context(), f.data.Workspace.ID, "0", 10, 2048); err == nil {
		t.Fatal("legacy read fallback enabled an uncovered journal consumer")
	}
}
