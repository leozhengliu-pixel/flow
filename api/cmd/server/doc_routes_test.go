package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21DocRoutesOutcome struct {
	code int
	body string
}

// scaleP21DocRoutesServe runs one request through the real authentication and
// workspace authorization (a session cookie, or an API key) and the mux, as
// the server does.
func scaleP21DocRoutesServe(f scaleP21M2Fixture, source *store.SQLiteStore, token string, user domain.User, key *domain.APIKey, method, path string, body any) scaleP21DocRoutesOutcome {
	service := &server{store: source, realtime: newRealtimeHub(), uploadPath: f.testingT.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/documents/{id}", service.getDocument)
	mux.HandleFunc("PATCH /api/documents/{id}", service.updateDocument)
	mux.HandleFunc("PATCH /api/projects/{id}", service.updateProject)
	w := httptest.NewRecorder()
	if key == nil {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		r, closeOutput := beginRequestResourceReads(r)
		defer closeOutput()
		service.withColdMetadataRouteMux(mux, service.authenticate(mux)).ServeHTTP(w, r)
	} else {
		r := f.literalRequest(user, key, body)
		r.Method, r.URL.Path = method, path
		r, closeOutput := beginRequestResourceReads(r)
		defer closeOutput()
		service.withColdMetadataRouteMux(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if service.authorizeWorkspaceRequest(w, r, user) {
				mux.ServeHTTP(w, r)
			}
		})).ServeHTTP(w, r)
	}
	return scaleP21DocRoutesOutcome{w.Code, normalizeDocRoutesBody(w.Body.String())}
}

// normalizeDocRoutesBody drops the request id and the clock fields of a
// successful write response; permission answers and bodies compare exactly.
func normalizeDocRoutesBody(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, item := range t {
				switch k {
				case "requestId", "updatedAt", "createdAt":
					delete(t, k)
				default:
					walk(item)
				}
			}
		case []any:
			for _, item := range t {
				walk(item)
			}
		}
	}
	walk(value)
	out, _ := json.Marshal(value)
	return string(out)
}

// TestScaleP21DocRoutesDifferential compares GET /api/documents/{id},
// PATCH /api/documents/{id} (allowed, refused, conflicting, missing) and
// PATCH /api/projects/{id} with the flag on against the literal flag-off
// answer for every principal and credential: same status and same body. A
// refused edit must also write nothing.
func TestScaleP21DocRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("docroutes-")
		var private string
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.doc_routes_seed", "", nil, func(data *domain.Bootstrap) error {
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			data.Documents = []domain.Document{
				{ID: prefix + "team", Title: "Team document", Content: "body", Creator: data.Viewer, Version: 3, TeamIDs: []string{f.public}, ProjectIDs: []string{prefix + "project", prefix + "listed"}},
				{ID: prefix + "editor", Title: "Editor grant", Content: "body", Creator: data.Viewer, Version: 3, TeamIDs: []string{f.public}, Permissions: []domain.DocumentPermission{
					{SubjectType: "user", SubjectID: f.principals[1].user.ID, Role: "editor"},
					{SubjectType: "user", SubjectID: f.principals[2].user.ID, Role: "viewer"},
					{SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "commenter"},
				}},
				{ID: prefix + "private", Title: "Private document", Content: "body", Creator: data.Viewer, Version: 3, TeamIDs: []string{private}},
				{ID: prefix + "workspace", Title: "Workspace viewer", Content: "body", Creator: data.Viewer, Version: 3, Permissions: []domain.DocumentPermission{{SubjectType: "workspace", Role: "viewer"}}},
				{ID: prefix + "inherited", Title: "Inherited", Content: "body", Creator: data.Viewer, Version: 3, InheritedTeamIDs: []string{private}},
			}
			data.Projects = append(data.Projects,
				domain.Project{ID: prefix + "project", Name: "Doc routes project", TeamIDs: []string{f.public}, Summary: "before"},
				domain.Project{ID: prefix + "listed", Name: "Listed project", TeamIDs: []string{private}, Resources: []domain.ProjectResource{{ID: prefix + "team", Title: "Team document"}}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		tokens := map[string]string{}
		for _, principal := range f.principals {
			_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			tokens[principal.user.ID] = token
		}
		serve := func(source *store.SQLiteStore, user domain.User, key *domain.APIKey, method, path string, body any) scaleP21DocRoutesOutcome {
			return scaleP21DocRoutesServe(f, source, tokens[user.ID], user, key, method, path, body)
		}
		keys := []*domain.APIKey{nil,
			{ID: prefix + "all", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"},
			{ID: prefix + "selected", Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
			{ID: prefix + "empty", Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{}},
			{ID: prefix + "readonly", Scopes: []string{"read"}, TeamRestriction: "all"},
		}
		refs := []string{"team", "editor", "private", "workspace", "inherited", "missing"}
		cases, refused := 0, 0
		compare := func(label string, user domain.User, key *domain.APIKey, method, path string, body any) scaleP21DocRoutesOutcome {
			t.Helper()
			want := serve(repo, user, key, method, path, body)
			got := serve(f.probe, user, key, method, path, body)
			if want != got {
				t.Fatalf("%s differs for %s key=%+v\nliteral=%d %s\nbounded=%d %s", label, user.ID, key, want.code, want.body, got.code, got.body)
			}
			cases++
			return got
		}
		// Reads and refused/conflicting edits never change the documents, so
		// the literal and bounded answers see identical state.
		for _, principal := range f.principals {
			for _, key := range keys {
				for _, ref := range refs {
					id := prefix + ref
					compare("detail", principal.user, key, http.MethodGet, "/api/documents/"+id, nil)
					outcome := compare("conflicting edit", principal.user, key, http.MethodPatch, "/api/documents/"+id, map[string]any{"title": "Conflict", "expectedVersion": 99})
					if outcome.code == http.StatusForbidden {
						refused++
					}
				}
			}
		}
		if refused == 0 {
			t.Fatal("no edit was refused: the fixture does not exercise the refusal")
		}
		// PATCH /api/projects/{id} (a COLD route with the flag on): the same
		// answers for every principal and credential, refused and conflicting
		// or no-op edits changing nothing.
		for _, principal := range f.principals {
			for _, key := range keys {
				compare("project edit", principal.user, key, http.MethodPatch, "/api/projects/"+prefix+"project", map[string]any{"summary": "before"})
				compare("project missing", principal.user, key, http.MethodPatch, "/api/projects/"+prefix+"nope", map[string]any{"summary": "x"})
				compare("project invalid", principal.user, key, http.MethodPatch, "/api/projects/"+prefix+"project", map[string]any{"statusId": prefix + "nostatus"})
			}
		}
		// A refused edit writes nothing, in either mode.
		before, _ := repo.BootstrapFor(workspace)
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			for _, principal := range f.principals {
				serve(source, principal.user, nil, http.MethodPatch, "/api/documents/"+prefix+"team", map[string]any{"title": "Refused " + principal.name})
			}
		}
		after, _ := repo.BootstrapFor(workspace)
		for index := range before.Documents {
			if before.Documents[index].ID == prefix+"private" && after.Documents[index].Title != before.Documents[index].Title {
				t.Fatal("an unrelated document changed")
			}
		}
		// Allowed edits: the same status and the same resulting document.
		for _, principal := range f.principals {
			id := prefix + "editor"
			title := "Edited by " + principal.name
			want := serve(repo, principal.user, nil, http.MethodPatch, "/api/documents/"+id, map[string]any{"title": title})
			wantDoc := mustDocument(t, repo, workspace, id)
			serve(repo, f.data.Viewer, nil, http.MethodPatch, "/api/documents/"+id, map[string]any{"title": "Reset"})
			got := serve(f.probe, principal.user, nil, http.MethodPatch, "/api/documents/"+id, map[string]any{"title": title})
			gotDoc := mustDocument(t, repo, workspace, id)
			serve(repo, f.data.Viewer, nil, http.MethodPatch, "/api/documents/"+id, map[string]any{"title": "Reset"})
			if want.code != got.code || wantDoc.Title != gotDoc.Title {
				t.Fatalf("allowed edit differs for %s: literal=%d %q bounded=%d %q", principal.name, want.code, wantDoc.Title, got.code, gotDoc.Title)
			}
		}
		for _, principal := range f.principals {
			literal := serve(repo, principal.user, nil, http.MethodPatch, "/api/projects/"+prefix+"project", map[string]any{"summary": "by " + principal.name})
			wantProject := mustProject(t, repo, workspace, prefix+"project")
			serve(repo, f.data.Viewer, nil, http.MethodPatch, "/api/projects/"+prefix+"project", map[string]any{"summary": "reset"})
			bounded := serve(f.probe, principal.user, nil, http.MethodPatch, "/api/projects/"+prefix+"project", map[string]any{"summary": "by " + principal.name})
			gotProject := mustProject(t, repo, workspace, prefix+"project")
			serve(repo, f.data.Viewer, nil, http.MethodPatch, "/api/projects/"+prefix+"project", map[string]any{"summary": "reset"})
			if literal != bounded || wantProject.Summary != gotProject.Summary {
				t.Fatalf("project edit differs for %s: literal=%d %s %q bounded=%d %s %q", principal.name, literal.code, literal.body, wantProject.Summary, bounded.code, bounded.body, gotProject.Summary)
			}
		}
		t.Logf("document route cases=%d refused=%d", cases, refused)
	}
}

func mustDocument(t *testing.T, repo *store.SQLiteStore, workspace, id string) domain.Document {
	t.Helper()
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	data, _ := repo.BootstrapFor(workspace)
	for _, document := range data.Documents {
		if document.ID == id {
			return document
		}
	}
	t.Fatalf("document %s missing", id)
	return domain.Document{}
}

func mustProject(t *testing.T, repo *store.SQLiteStore, workspace, id string) domain.Project {
	t.Helper()
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	data, _ := repo.BootstrapFor(workspace)
	for _, project := range data.Projects {
		if project.ID == id {
			return project
		}
	}
	t.Fatalf("project %s missing", id)
	return domain.Project{}
}
