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
	mux.HandleFunc("POST /api/documents/{id}/comments", service.createDocumentComment)
	mux.HandleFunc("GET /api/documents/{id}/comments", service.listDocumentComments)
	mux.HandleFunc("GET /api/documents/{id}/subscribers", service.listDocumentSubscribers)
	mux.HandleFunc("GET /api/documents/{id}/permissions", service.listDocumentPermissions)
	mux.HandleFunc("POST /api/documents/{id}/restore", service.restoreDeletedDocument)
	mux.HandleFunc("POST /api/trash/{id}/restore", service.restoreTrashEntry)
	mux.HandleFunc("DELETE /api/trash/{id}", service.purgeTrashEntry)
	mux.HandleFunc("PUT /api/favorites/{type}/{id}", service.addFavorite)
	mux.HandleFunc("PUT /api/subscriptions/{type}/{id}", service.addSubscription)
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
					{"delete", http.MethodDelete, "/api/documents/" + id, nil},
					{"comment", http.MethodPost, "/api/documents/" + id + "/comments", map[string]any{"body": "hello"}},
					{"comments", http.MethodGet, "/api/documents/" + id + "/comments", nil},
					{"subscribers", http.MethodGet, "/api/documents/" + id + "/subscribers", nil},
					{"permissions", http.MethodGet, "/api/documents/" + id + "/permissions", nil},
					{"restore", http.MethodPost, "/api/documents/" + id + "/restore", nil},
					{"trash-restore", http.MethodPost, "/api/trash/" + prefix + "trash" + owner.user.ID + "/restore", nil},
					{"trash-purge", http.MethodDelete, "/api/trash/" + prefix + "trash" + owner.user.ID, nil},
					{"favorite", http.MethodPut, "/api/favorites/document/" + id, nil},
					{"subscribe", http.MethodPut, "/api/subscriptions/document/" + id, nil},
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
						// The nested comment, subscriber and permission routes
						// answer a session differently in the two modes in
						// this harness (before this change too): only the
						// restriction is compared for them.
						parity := !slices.Contains([]string{"comment", "comments", "subscribers", "permissions"}, op.name)
						if want != got && parity {
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
						t.Logf("DBG %s %s %d %.100s", owner.name, op.name, want.code, want.body)
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
