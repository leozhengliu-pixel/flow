package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// keyTeamScopeServe runs one request through the real authentication (a
// session cookie, or an API key owned by user) and workspace authorization.
func keyTeamScopeServe(f scaleP21M2Fixture, source *store.SQLiteStore, token string, user domain.User, key *domain.APIKey, method, path string, body any) scaleP21DocRoutesOutcome {
	service := &server{store: source, realtime: newRealtimeHub(), uploadPath: f.testingT.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/documents", service.listDocuments)
	mux.HandleFunc("GET /api/documents/{id}", service.getDocument)
	mux.HandleFunc("PATCH /api/documents/{id}", service.updateDocument)
	mux.HandleFunc("DELETE /api/documents/{id}", service.deleteDocument)
	mux.HandleFunc("GET /api/search", service.searchWorkspace)
	mux.HandleFunc("POST /api/documents", service.createDocument)
	mux.HandleFunc("PATCH /api/views/{id}", service.updateSavedView)
	mux.HandleFunc("DELETE /api/views/{id}", service.deleteSavedView)
	mux.HandleFunc("POST /api/views/{id}/share", service.shareSavedView)
	mux.HandleFunc("PUT /api/favorites/{type}/{id}", service.addFavorite)
	mux.HandleFunc("PUT /api/subscriptions/{type}/{id}", service.addSubscription)
	mux.HandleFunc("POST /api/documents/{id}/comments", service.createDocumentComment)
	mux.HandleFunc("GET /api/documents/{id}/comments", service.listDocumentComments)
	mux.HandleFunc("GET /api/documents/{id}/subscribers", service.listDocumentSubscribers)
	mux.HandleFunc("GET /api/documents/{id}/permissions", service.listDocumentPermissions)
	mux.HandleFunc("POST /api/documents/{id}/restore", service.restoreDeletedDocument)
	mux.HandleFunc("POST /api/trash/{id}/restore", service.restoreTrashEntry)
	mux.HandleFunc("DELETE /api/trash/{id}", service.purgeTrashEntry)
	mux.HandleFunc("GET /api/issue-records", service.listIssueRecords)
	mux.HandleFunc("GET /api/issue-records/{id}", service.getIssueRecord)
	mux.HandleFunc("PATCH /api/issue-records/{id}", service.updateIssueRecord)
	mux.HandleFunc("DELETE /api/issue-records/{id}", service.issueRecordAlias(service.deleteIssue))
	mux.HandleFunc("POST /api/issue-records/{id}/comments", service.issueRecordAlias(service.createComment))
	mux.HandleFunc("PATCH /api/issues/{id}", service.updateIssue)
	mux.HandleFunc("DELETE /api/issues/{id}", service.deleteIssue)
	mux.HandleFunc("POST /api/issues/{id}/comments", service.createComment)
	w := httptest.NewRecorder()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	if key == nil {
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		r, closeOutput := beginRequestResourceReads(r)
		defer closeOutput()
		service.withColdMetadataRouteMux(mux, service.authenticate(mux)).ServeHTTP(w, r)
	} else {
		r := f.literalRequest(user, key, body)
		r.Method, r.URL.Path = method, path
		if i := strings.IndexByte(path, '?'); i >= 0 {
			r.URL.Path, r.URL.RawQuery = path[:i], path[i+1:]
		}
		r, closeOutput := beginRequestResourceReads(r)
		defer closeOutput()
		service.withColdMetadataRouteMux(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if service.authorizeWorkspaceRequest(w, r, user) {
				mux.ServeHTTP(w, r)
			}
		})).ServeHTTP(w, r)
	}
	out := normalizeDocRoutesBody(w.Body.String())
	if strings.HasPrefix(path, "/api/search") {
		var value map[string]any
		if json.Unmarshal([]byte(out), &value) == nil {
			delete(value, "history")
			raw, _ := json.Marshal(value)
			out = string(raw)
		}
	}
	if method == http.MethodPost {
		out = ""
	}
	if method == http.MethodPatch && w.Code == http.StatusOK {
		// Generated slugs and version ids differ between two runs.
		var doc domain.Document
		_ = json.Unmarshal(w.Body.Bytes(), &doc)
		out = "updated:" + doc.ID + ":" + doc.Title
	}
	return scaleP21DocRoutesOutcome{w.Code, out}
}

// TestKeyTeamScopeDocuments: a key restricted to some teams never reaches a
// document outside them through a grant its owner holds as a person (creator,
// workspace admin, team member), while the owner's session and an unrestricted
// key keep every grant. The flag-off and bounded paths answer identically.
func TestKeyTeamScopeDocuments(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("keyscope-")
		ownerGrant := func(user domain.User) []domain.DocumentPermission {
			return []domain.DocumentPermission{{ID: "document_permission_" + user.ID, SubjectType: "user", SubjectID: user.ID, Role: "owner"}}
		}
		// Every principal created one document in the private team (and one
		// that was then deleted); the key is restricted to the public team.
		seed := func() {
			if err := repo.MutateWorkspace(t.Context(), workspace, "key_scope.seed", "", nil, func(data *domain.Bootstrap) error {
				private := ""
				for _, team := range data.Teams {
					if strings.HasPrefix(team.ID, "m2-private-") {
						private = team.ID
					}
				}
				data.Trash, data.Documents = nil, []domain.Document{{ID: prefix + "open", Title: "Open", Content: "secretword", Creator: data.Viewer, Version: 3, TeamIDs: []string{f.public}, Permissions: ownerGrant(data.Viewer)}}
				for _, p := range f.principals {
					data.Documents = append(data.Documents, domain.Document{ID: prefix + p.user.ID, Title: "Private " + p.name, Content: "secretword", Creator: p.user, Version: 3, TeamIDs: []string{private}, Permissions: ownerGrant(p.user)})
					gone := domain.Document{ID: prefix + "gone" + p.user.ID, Title: "Gone " + p.name, Content: "secretword", Creator: p.user, Version: 3, TeamIDs: []string{private}, Permissions: ownerGrant(p.user)}
					raw, _ := json.Marshal(gone)
					data.Trash = append(data.Trash, domain.TrashEntry{ID: prefix + "trash" + p.user.ID, ResourceType: "document", ResourceID: gone.ID, Payload: raw, DeletedBy: p.user, DeletedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
		}
		seed()
		tokens := map[string]string{}
		for _, p := range f.principals {
			_, token, err := repo.Login(t.Context(), p.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			tokens[p.user.ID] = token
		}
		credentials := []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"unrestricted", &domain.APIKey{ID: prefix + "all", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
			{"restricted", &domain.APIKey{ID: prefix + "selected", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
			{"restricted-empty", &domain.APIKey{ID: prefix + "empty", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}},
		}
		for _, owner := range f.principals {
			for _, doc := range []string{owner.user.ID, "gone" + owner.user.ID} {
				id := prefix + doc
				for _, op := range []struct {
					name, method, path string
					body               any
				}{
					{"detail", http.MethodGet, "/api/documents/" + id, nil},
					{"list", http.MethodGet, "/api/documents", nil},
					{"search", http.MethodGet, "/api/search?q=secretword", nil},
					{"update", http.MethodPatch, "/api/documents/" + id, map[string]any{"title": "Changed"}},
					{"comment", http.MethodPost, "/api/documents/" + id + "/comments", map[string]any{"body": "hello"}},
					{"comments", http.MethodGet, "/api/documents/" + id + "/comments", nil},
					{"subscribers", http.MethodGet, "/api/documents/" + id + "/subscribers", nil},
					{"permissions", http.MethodGet, "/api/documents/" + id + "/permissions", nil},
					{"restore", http.MethodPost, "/api/documents/" + id + "/restore", nil},
					{"trash-restore", http.MethodPost, "/api/trash/" + prefix + "trash" + owner.user.ID + "/restore", nil},
					{"trash-purge", http.MethodDelete, "/api/trash/" + prefix + "trash" + owner.user.ID, nil},
					{"favorite", http.MethodPut, "/api/favorites/document/" + id, nil},
					{"subscribe", http.MethodPut, "/api/subscriptions/document/" + id, nil},
					{"delete", http.MethodDelete, "/api/documents/" + id, nil},
				} {
					trashOp := strings.HasPrefix(op.name, "trash-") || op.name == "restore"
					if strings.HasPrefix(doc, "gone") != trashOp && op.name != "detail" {
						continue
					}
					outcomes := map[string]scaleP21DocRoutesOutcome{}
					for _, credential := range credentials {
						run := func(source *store.SQLiteStore) scaleP21DocRoutesOutcome {
							seed()
							return keyTeamScopeServe(f, source, tokens[owner.user.ID], owner.user, credential.key, op.method, op.path, op.body)
						}
						want, got := run(repo), run(f.probe)
						if want != got {
							t.Errorf("%s/%s/%s/%s differs: literal=%d %.200s bounded=%d %.200s", owner.name, doc, op.name, credential.name, want.code, want.body, got.code, got.body)
						}
						outcomes[credential.name] = want
						if strings.HasPrefix(credential.name, "restricted") {
							for mode, outcome := range map[string]scaleP21DocRoutesOutcome{"literal": want, "bounded": got} {
								reached := outcome.code < 400 && op.name != "list" && op.name != "search"
								if (op.name == "list" || op.name == "search") && strings.Contains(outcome.body, id) {
									reached = true
								}
								if reached {
									t.Errorf("%s/%s/%s/%s/%s: restricted key reached an out-of-scope document: %d %.200s", owner.name, doc, op.name, credential.name, mode, outcome.code, outcome.body)
								}
							}
						}
					}
					// An unrestricted key keeps its owner's grants exactly: it
					// answers as the owner's session does.
					if outcomes["session"].code != outcomes["unrestricted"].code {
						t.Errorf("%s/%s/%s: unrestricted key differs from the session: %d %.120s vs %d %.120s", owner.name, doc, op.name, outcomes["unrestricted"].code, outcomes["unrestricted"].body, outcomes["session"].code, outcomes["session"].body)
					}
					// The creator keeps the creator grant as a person.
					if want := map[string]int{"detail": http.StatusOK, "update": http.StatusOK, "delete": http.StatusNoContent}[op.name]; want != 0 && owner.name != "guest-explicit" && outcomes["session"].code != want {
						t.Errorf("%s/%s/%s: the owner's session lost its creator grant: %d %.120s", owner.name, doc, op.name, outcomes["session"].code, outcomes["session"].body)
					}
				}
			}
		}
	}
}

// TestKeyTeamScopeIssues: the same rule for issues, whose person-level grants
// are the creator, workspace admins and team owners.
func TestKeyTeamScopeIssues(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("keyissue-")
		restore := store.ForceFullMutationsForTesting(true)
		seed := func() {
			if err := repo.MutateWorkspace(t.Context(), workspace, "key_scope.issue_seed", "", nil, func(data *domain.Bootstrap) error {
				var private domain.Team
				for _, team := range data.Teams {
					if strings.HasPrefix(team.ID, "m2-private-") {
						private = team
					}
				}
				data.Issues = slices.DeleteFunc(data.Issues, func(issue domain.Issue) bool { return strings.HasPrefix(issue.ID, prefix) })
				for index, p := range f.principals {
					data.Issues = append(data.Issues, domain.Issue{ID: prefix + p.user.ID, Identifier: fmt.Sprintf("KI-%d", index+1), Number: 900 + index, Title: "secretword " + p.name, Team: private, Creator: p.user, State: data.States[0], Permissions: []domain.IssuePermission{{ID: "perm_" + p.user.ID, IssueID: prefix + p.user.ID, SubjectType: "user", SubjectID: p.user.ID, Role: "editor"}}})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
		}
		seed()
		restore()
		tokens := map[string]string{}
		for _, p := range f.principals {
			_, token, err := repo.Login(t.Context(), p.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			tokens[p.user.ID] = token
		}
		credentials := []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"unrestricted", &domain.APIKey{ID: prefix + "all", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
			{"restricted", &domain.APIKey{ID: prefix + "selected", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
		}
		for _, owner := range f.principals {
			id := prefix + owner.user.ID
			for _, op := range []struct {
				name, method, path string
				body               any
			}{
				{"detail", http.MethodGet, "/api/issue-records/" + id, nil},
				{"list", http.MethodGet, "/api/issue-records", nil},
				{"search", http.MethodGet, "/api/search?q=secretword", nil},
				{"update", http.MethodPatch, "/api/issue-records/" + id, map[string]any{"title": "Changed"}},
				{"delete", http.MethodDelete, "/api/issue-records/" + id, nil},
				{"comment", http.MethodPost, "/api/issue-records/" + id + "/comments", map[string]any{"body": "hello"}},
				{"legacy-update", http.MethodPatch, "/api/issues/" + id, map[string]any{"title": "Changed"}},
				{"legacy-delete", http.MethodDelete, "/api/issues/" + id, nil},
				{"legacy-comment", http.MethodPost, "/api/issues/" + id + "/comments", map[string]any{"body": "hello"}},
			} {
				outcomes := map[string]scaleP21DocRoutesOutcome{}
				for _, credential := range credentials {
					run := func(source *store.SQLiteStore) scaleP21DocRoutesOutcome {
						restore := store.ForceFullMutationsForTesting(true)
						seed()
						restore()
						return keyTeamScopeServe(f, source, tokens[owner.user.ID], owner.user, credential.key, op.method, op.path, op.body)
					}
					want, got := run(repo), run(f.probe)
					if want != got {
						t.Fatalf("%s/%s/%s differs: literal=%d %.200s bounded=%d %.200s", owner.name, op.name, credential.name, want.code, want.body, got.code, got.body)
					}
					outcomes[credential.name] = want
					if credential.name == "restricted" {
						reached := want.code < 400 && op.name != "list" && op.name != "search" || strings.Contains(want.body, id) && (op.name == "list" || op.name == "search")
						if reached {
							t.Errorf("%s/%s: restricted key reached an out-of-scope issue: %d %.200s", owner.name, op.name, want.code, want.body)
						}
					}
				}
				if outcomes["session"].code != outcomes["unrestricted"].code {
					t.Errorf("%s/%s: unrestricted key differs from the session: %d vs %d", owner.name, op.name, outcomes["unrestricted"].code, outcomes["session"].code)
				}
				if op.name == "detail" && owner.name != "guest-explicit" && owner.name != "admin-private-outsider" && outcomes["session"].code != http.StatusOK {
					t.Errorf("%s/detail: the creator's session lost access: %d %.120s", owner.name, outcomes["session"].code, outcomes["session"].body)
				}
			}
		}
	}
}

// TestKeyTeamScopeDocumentSubRoutesParity compares the nested document routes
// (comments, subscribers, permissions) of the flag-off and bounded stores for
// every fixture role and credential, on a public-team and a private-team
// document the caller did not create. (An earlier version of
// TestKeyTeamScopeDocuments deleted a document and re-seeded the same id
// before these routes: the bounded store keeps the deleted id's canonical
// record, so the re-seeded document was missing from the bounded read and the
// routes answered 403. Real ids are never re-used, so that was a harness
// artifact; these documents are never deleted.)
func TestKeyTeamScopeDocumentSubRoutesParity(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("keysub-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "key_scope.sub_seed", "", nil, func(data *domain.Bootstrap) error {
			private := ""
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			owner := []domain.DocumentPermission{{ID: "perm_" + data.Viewer.ID, SubjectType: "user", SubjectID: data.Viewer.ID, Role: "owner"}}
			data.Documents = []domain.Document{
				{ID: prefix + "public", Title: "Public", Creator: data.Viewer, Version: 1, TeamIDs: []string{f.public}, Permissions: owner},
				{ID: prefix + "private", Title: "Private", Creator: data.Viewer, Version: 1, TeamIDs: []string{private}, Permissions: owner},
			}
			for _, p := range f.principals {
				data.Documents = append(data.Documents,
					domain.Document{ID: prefix + "own-" + p.user.ID, Title: "Own", Creator: p.user, Version: 1, TeamIDs: []string{private}, Permissions: []domain.DocumentPermission{{ID: "perm_" + p.user.ID, SubjectType: "user", SubjectID: p.user.ID, Role: "owner"}}},
					domain.Document{ID: prefix + "own-public-" + p.user.ID, Title: "Own public", Creator: p.user, Version: 1, TeamIDs: []string{f.public}, Permissions: []domain.DocumentPermission{{ID: "perm_" + p.user.ID, SubjectType: "user", SubjectID: p.user.ID, Role: "owner"}}})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		tokens := map[string]string{}
		for _, p := range f.principals {
			_, token, err := repo.Login(t.Context(), p.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			tokens[p.user.ID] = token
		}
		// A document without a team, created through the API as the app does
		// (the store gives it its stored grants).
		if created := keyTeamScopeServe(f, repo, tokens[f.data.Viewer.ID], f.data.Viewer, nil, http.MethodPost, "/api/documents", map[string]any{"title": "Unscoped " + prefix}); created.code != http.StatusCreated {
			t.Fatalf("create document: %d %s", created.code, created.body)
		}
		stored, _ := repo.BootstrapFor(workspace)
		unscoped := ""
		for _, document := range stored.Documents {
			if document.Title == "Unscoped "+prefix {
				unscoped = document.ID
			}
		}
		if unscoped == "" {
			t.Fatal("the created document is missing")
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		credentials := []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"unrestricted", &domain.APIKey{ID: prefix + "all", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
			{"restricted", &domain.APIKey{ID: prefix + "selected", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
		}
		compared, allowed := 0, 0
		for _, caller := range f.principals {
			refs := []string{prefix + "public", prefix + "private", unscoped, prefix + "own-" + caller.user.ID, prefix + "own-public-" + caller.user.ID, prefix + "missing"}
			for _, id := range refs {
				for _, op := range []struct{ method, path string }{
					{http.MethodGet, "/api/documents/" + id},
					{http.MethodGet, "/api/documents/" + id + "/comments"},
					{http.MethodGet, "/api/documents/" + id + "/subscribers"},
					{http.MethodGet, "/api/documents/" + id + "/permissions"},
					{http.MethodPost, "/api/documents/" + id + "/comments"},
				} {
					for _, credential := range credentials {
						want := keyTeamScopeServe(f, repo, tokens[caller.user.ID], caller.user, credential.key, op.method, op.path, map[string]any{"body": "hello"})
						got := keyTeamScopeServe(f, f.probe, tokens[caller.user.ID], caller.user, credential.key, op.method, op.path, map[string]any{"body": "hello"})
						if op.method == http.MethodGet && strings.HasSuffix(op.path, "/comments") {
							want.body, got.body = "", ""
						}
						if want != got {
							t.Errorf("%s/%s %s %s differs: literal=%d %.3000s bounded=%d %.3000s", caller.name, credential.name, op.method, op.path, want.code, want.body, got.code, got.body)
						}
						compared++
						if want.code < 300 {
							allowed++
						}
					}
				}
			}
		}
		if allowed == 0 {
			t.Fatal("no request was allowed: the fixture does not exercise the routes")
		}
		t.Logf("compared=%d allowed=%d", compared, allowed)
	}
}

// TestKeyTeamScopeViews: a team-restricted key never reaches its owner's
// personal view bound to a project outside the key's teams. The owner's
// session and an unrestricted key keep it; both stores answer alike.
func TestKeyTeamScopeViews(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("keyview-")
		private := ""
		if err := repo.MutateWorkspace(t.Context(), workspace, "key_scope.view_seed", "", nil, func(data *domain.Bootstrap) error {
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			data.Projects = append(data.Projects,
				domain.Project{ID: prefix + "private-project", Name: "Private project", TeamIDs: []string{private}},
				domain.Project{ID: prefix + "public-project", Name: "Public project", TeamIDs: []string{f.public}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		tokens := map[string]string{}
		for _, p := range f.principals {
			_, token, err := repo.Login(t.Context(), p.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			tokens[p.user.ID] = token
		}
		credentials := []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"unrestricted", &domain.APIKey{ID: prefix + "all", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
			{"restricted", &domain.APIKey{ID: prefix + "selected", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
		}
		counter, refusedCount := 0, 0
		for _, owner := range f.principals {
			for _, project := range []string{"private-project", "public-project", ""} {
				for _, op := range []struct {
					name, method, path string
					body               any
				}{
					{"update", http.MethodPatch, "/api/views/%s", map[string]any{"name": "Renamed"}},
					{"share", http.MethodPost, "/api/views/%s/share", nil},
					{"favorite", http.MethodPut, "/api/favorites/view/%s", nil},
					{"subscribe", http.MethodPut, "/api/subscriptions/view/%s", nil},
					{"delete", http.MethodDelete, "/api/views/%s", nil},
				} {
					outcomes := map[string]scaleP21DocRoutesOutcome{}
					for _, credential := range credentials {
						run := func(source *store.SQLiteStore) scaleP21DocRoutesOutcome {
							counter++
							id := fmt.Sprintf("%sview-%d", prefix, counter)
							if err := repo.MutateWorkspace(t.Context(), workspace, "key_scope.view_add", "", nil, func(data *domain.Bootstrap) error {
								data.SavedViews = append(data.SavedViews, domain.SavedView{ID: id, SlugID: id, Name: "Mine", Resource: "issues", Scope: "personal", OwnerID: owner.user.ID, ProjectID: map[bool]string{true: prefix + project, false: ""}[project != ""], View: "list", Filters: json.RawMessage("{}"), Display: json.RawMessage("{}"), Insights: json.RawMessage("{}")})
								return nil
							}); err != nil {
								t.Fatal(err)
							}
							if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
								t.Fatal(err)
							}
							return keyTeamScopeServe(f, source, tokens[owner.user.ID], owner.user, credential.key, op.method, fmt.Sprintf(op.path, id), op.body)
						}
						want, got := run(repo), run(f.probe)
						if want.code != got.code {
							t.Errorf("%s/%s/%s/%s differs: literal=%d %.120s bounded=%d %.120s", owner.name, project, op.name, credential.name, want.code, want.body, got.code, got.body)
						}
						outcomes[credential.name] = want
						if credential.name == "restricted" && project == "private-project" {
							if want.code < 400 || got.code < 400 {
								t.Errorf("%s/%s: restricted key reached a personal view bound to an out-of-scope project: %d / %d", owner.name, op.name, want.code, got.code)
							}
							refusedCount++
						}
					}
					if outcomes["session"].code != outcomes["unrestricted"].code {
						t.Errorf("%s/%s/%s: unrestricted key differs from the session: %d vs %d", owner.name, project, op.name, outcomes["unrestricted"].code, outcomes["session"].code)
					}
				}
			}
		}
		if refusedCount == 0 {
			t.Fatal("fixture does not exercise the restriction")
		}
	}
}

// TestKeyTeamScopeLegacyGrantlessDocuments: documents stored before the owner
// grant existed (no permissions at all). Every load of the workspace
// (normalize in the store) adds the creator's owner grant, in the flag-off
// image after a (re)load and in the bounded reads alike, so both answer
// alike for every role and credential. Before the first reload of a process
// that wrote the raw rows, the flag-off image has not been normalized yet.
func TestKeyTeamScopeLegacyGrantlessDocuments(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("keylegacy-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "key_scope.legacy_seed", "", nil, func(data *domain.Bootstrap) error {
			private := ""
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			data.Documents = []domain.Document{
				{ID: prefix + "unscoped", Title: "Legacy unscoped", Creator: data.Viewer, Version: 1},
				{ID: prefix + "public", Title: "Legacy public", Creator: data.Viewer, Version: 1, TeamIDs: []string{f.public}},
				{ID: prefix + "private", Title: "Legacy private", Creator: data.Viewer, Version: 1, TeamIDs: []string{private}},
			}
			for _, p := range f.principals {
				data.Documents = append(data.Documents,
					domain.Document{ID: prefix + "own-" + p.user.ID, Title: "Legacy own", Creator: p.user, Version: 1, TeamIDs: []string{private}},
					domain.Document{ID: prefix + "own-open-" + p.user.ID, Title: "Legacy own open", Creator: p.user, Version: 1})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		tokens := map[string]string{}
		for _, p := range f.principals {
			_, token, err := repo.Login(t.Context(), p.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			tokens[p.user.ID] = token
		}
		credentials := []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"unrestricted", &domain.APIKey{ID: prefix + "all", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
			{"restricted", &domain.APIKey{ID: prefix + "selected", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
		}
		for _, state := range []string{"raw", "reloaded", "migrated"} {
			switch state {
			case "reloaded":
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			case "migrated":
				if err := repo.MigrateDocumentOwnerGrantsForTesting(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			differ, compared := 0, 0
			for _, caller := range f.principals {
				for _, ref := range []string{"unscoped", "public", "private", "own-" + caller.user.ID, "own-open-" + caller.user.ID} {
					id := prefix + ref
					for _, op := range []struct{ method, path string }{
						{http.MethodGet, "/api/documents/" + id},
						{http.MethodGet, "/api/documents/" + id + "/comments"},
						{http.MethodGet, "/api/documents/" + id + "/subscribers"},
						{http.MethodGet, "/api/documents/" + id + "/permissions"},
						{http.MethodPost, "/api/documents/" + id + "/comments"},
						{http.MethodGet, "/api/documents"},
					} {
						for _, credential := range credentials {
							want := keyTeamScopeServe(f, repo, tokens[caller.user.ID], caller.user, credential.key, op.method, op.path, map[string]any{"body": "hello"})
							got := keyTeamScopeServe(f, f.probe, tokens[caller.user.ID], caller.user, credential.key, op.method, op.path, map[string]any{"body": "hello"})
							if strings.HasSuffix(op.path, "/comments") && op.method == http.MethodGet || op.path == "/api/documents" {
								want.body, got.body = fmt.Sprint(len(want.body) > 3), fmt.Sprint(len(got.body) > 3)
							}
							compared++
							if want != got {
								differ++
								if state == "migrated" {
									t.Errorf("%s/%s %s %s differs: literal=%d %.200s bounded=%d %.200s", caller.name, credential.name, op.method, op.path, want.code, want.body, got.code, got.body)
								}
							}
						}
					}
				}
			}
			t.Logf("state=%s compared=%d differ=%d", state, compared, differ)
			if state == "reloaded" && differ == 0 {
				t.Fatal("the legacy rows do not exercise the difference the migration closes")
			}
		}
	}
}
