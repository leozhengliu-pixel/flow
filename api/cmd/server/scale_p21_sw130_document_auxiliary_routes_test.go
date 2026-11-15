package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW130Writer struct {
	*httptest.ResponseRecorder
	t         *testing.T
	source    *store.SQLiteStore
	held      int
	sql       int
	preflight int
}

func (w *scaleP21SW130Writer) sample(code int) {
	work := w.source.MetadataWorkUsedForTesting()
	w.held = max(w.held, work)
	if w.source.BoundedMetadataEnabled() && code == http.StatusOK && work == 0 {
		w.t.Fatal("document auxiliary response lost its output owner")
	}
}

func (w *scaleP21SW130Writer) WriteHeader(code int) {
	w.sample(code)
	w.ResponseRecorder.WriteHeader(code)
}

func (w *scaleP21SW130Writer) Write(raw []byte) (int, error) {
	w.sample(w.Code)
	return w.ResponseRecorder.Write(raw)
}

func scaleP21SW130Key(prefix string, user domain.User, key *domain.APIKey) *domain.APIKey {
	value := scaleP21SW104StoredKey(prefix, user, key)
	if value != nil {
		value.SecretHash = secretHash("sw130-" + value.ID)
	}
	return value
}

func TestScaleP21SW130DocumentAuxiliaryRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW115Fixture(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw130-")
		stamp := time.Date(2026, time.November, 15, 5, 6, 7, 123456789, time.UTC)
		live, deleted, blocked := prefix+"live", prefix+"deleted", prefix+"blocked"
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw130_seed", "", nil, func(data *domain.Bootstrap) error {
			author := f.principals[1].user
			revisions := []domain.DocumentRevision{
				{ID: prefix + "revision-first", DocumentID: live, Title: "First stored occurrence", Content: "inline history", Author: author, AuthorIDs: []string{author.ID, author.ID, ""}, CreatedAt: stamp},
				{ID: prefix + "revision-second", DocumentID: live, Title: "Second stored occurrence", Content: strings.Repeat("selected offloaded history ", 12000), ContentData: map[string]any{"native": int64(9007199254740993)}, Author: f.data.Viewer, CreatedAt: stamp.Add(-time.Hour)},
				{ID: prefix + "revision-third", DocumentID: live, Title: "Third stored occurrence", ContentState: "history-state", ContentData: map[string]any{}, Author: author, CreatedAt: stamp.Add(time.Hour)},
			}
			data.Documents = append(data.Documents,
				domain.Document{ID: live, SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"}, Title: "Live auxiliary", Creator: author, TeamIDs: []string{f.public}, Revisions: revisions,
					Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: author.ID, Role: "owner"}, {SubjectType: "workspace", Role: "viewer"}}},
				domain.Document{ID: blocked, Creator: author, TeamIDs: []string{f.public}, Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: author.ID, Role: "owner"}}, Revisions: []domain.DocumentRevision{{ID: prefix + "blocked-live-version", Title: "Existing live document wins", Author: author, CreatedAt: stamp}}},
				domain.Document{ID: prefix + "nil-history", Creator: author, TeamIDs: []string{f.public}, Revisions: nil},
				domain.Document{ID: prefix + "empty-history", Creator: author, TeamIDs: []string{f.public}, Revisions: []domain.DocumentRevision{}},
			)
			for _, principal := range f.principals {
				data.DocumentContentDrafts = append(data.DocumentContentDrafts,
					domain.DocumentContentDraft{ID: prefix + principal.user.ID + "-first", DocumentID: live, UserID: principal.user.ID, Content: "first owned draft", ContentData: map[string]any{"native": int64(9007199254740993), "mutable": []any{"original"}}, Version: 7, CreatedAt: stamp, UpdatedAt: stamp.Add(time.Hour)},
					domain.DocumentContentDraft{ID: prefix + principal.user.ID + "-second", DocumentID: live, UserID: principal.user.ID, Content: "second owned draft", ContentData: map[string]any{}, Version: 3, CreatedAt: stamp.Add(-time.Hour), UpdatedAt: stamp},
					domain.DocumentContentDraft{ID: prefix + principal.user.ID + "-old", DocumentID: prefix + "old", UserID: principal.user.ID, Content: "raw old-slug draft", Version: 2, CreatedAt: stamp, UpdatedAt: stamp},
				)
				for _, key := range []domain.APIKey{
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"},
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}},
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
				} {
					data.APIKeys = append(data.APIKeys, *scaleP21SW130Key(prefix, principal.user, &key))
				}
			}
			data.DocumentContentDrafts = append(data.DocumentContentDrafts, domain.DocumentContentDraft{
				ID: prefix + "unrelated-draft", DocumentID: prefix + "other-document", UserID: f.data.Viewer.ID,
				Content: strings.Repeat("SW130_EXCLUDED_UNRELATED_DRAFT ", 10000),
			})
			for _, doc := range []domain.Document{
				{ID: deleted, SlugID: prefix + "deleted-current", PreviousSlugIDs: []string{prefix + "deleted-old"}, Creator: author, TeamIDs: []string{f.public}, Permissions: []domain.DocumentPermission{{SubjectType: "workspace", Role: "viewer"}}, Revisions: revisions},
				{ID: blocked, Creator: author, Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: f.principals[4].user.ID, Role: "viewer"}}, Revisions: []domain.DocumentRevision{{ID: prefix + "must-not-fallback", Title: "Wrong fallback after live role denial", Author: author, CreatedAt: stamp}}},
			} {
				raw, err := json.Marshal(doc)
				if err != nil {
					return err
				}
				data.Trash = append(data.Trash, domain.TrashEntry{ID: doc.ID + "-trash", ResourceType: "document", ResourceID: doc.ID, Payload: raw, DeletedBy: f.principals[4].user, DeletedAt: stamp, ExpiresAt: stamp.AddDate(0, 0, 30), Links: &domain.TrashLinks{}})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW130 other", prefix+"other", "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
		for _, key := range []string{workspace, other.Workspace.URLKey} {
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, key); err != nil {
				t.Fatal(err)
			}
		}
		for _, sourceState := range []string{"warm", "reloaded"} {
			t.Run(sourceState, func(t *testing.T) {
				if sourceState == "reloaded" {
					for _, key := range []string{workspace, other.Workspace.URLKey} {
						if err := repo.ReloadWorkspace(t.Context(), key); err != nil {
							t.Fatal(err)
						}
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, key); err != nil {
							t.Fatal(err)
						}
					}
				}
				services := []*server{{store: repo}, {store: f.probe}}
				muxes := make([]*http.ServeMux, len(services))
				for index, service := range services {
					muxes[index] = http.NewServeMux()
					muxes[index].HandleFunc("GET /api/documents/{id}/drafts", service.listDocumentDrafts)
					muxes[index].HandleFunc("GET /api/documents/{id}/history", service.documentContentHistory)
				}
				// The registered handler boundary uses live fixture credentials and
				// identities. Account/authenticate and their independent guards remain
				// outside this two-route consumer proof.
				call := func(t *testing.T, index int, user domain.User, key *domain.APIKey, target, path string, outer, development bool) *scaleP21SW130Writer {
					t.Helper()
					request := f.literalRequest(user, key, nil)
					statements, preflights := 0, 0
					request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(query string) {
						statements++
						if strings.HasPrefix(query, "SELECT COUNT(*),COALESCE(SUM(0+") {
							preflights++
						}
					}))
					request.Method = http.MethodGet
					var err error
					request.URL, err = url.Parse(path)
					if err != nil {
						t.Fatal(err)
					}
					request.Header.Set("X-Workspace-Key", target)
					request.SetPathValue("workspaceKey", target)
					request.SetPathValue("workspace", target)
					closeOutput := func() {}
					if outer {
						var ctx context.Context
						ctx, closeOutput = beginMetadataOutputReads(request.Context())
						request = request.WithContext(ctx)
					} else {
						request, closeOutput = beginRequestResourceReads(request)
					}
					defer closeOutput()
					services[index].authDisabled = development
					beforeDecodes := services[index].store.WorkspaceMetadataDecodes()
					writer := &scaleP21SW130Writer{ResponseRecorder: httptest.NewRecorder(), t: t, source: services[index].store}
					muxes[index].ServeHTTP(writer, request)
					writer.sql, writer.preflight = statements, preflights
					if index == 1 && writer.Code == http.StatusOK && (writer.held == 0 || f.probe.MetadataWorkUsedForTesting() == 0) {
						t.Fatal("auxiliary response released before outer output close")
					}
					closeOutput()
					closeOutput()
					if services[index].store.MetadataWorkUsedForTesting() != 0 || services[index].store.WorkspaceMetadataDecodes() != beforeDecodes {
						t.Fatal("auxiliary route leaked work or decoded complete workspace metadata")
					}
					return writer
				}
				pairs, legacySQL, boundedSQL, maximumLegacySQL, maximumBoundedSQL, maximumPreflights := 0, 0, 0, 0, 0, 0
				compare := func(t *testing.T, user domain.User, key *domain.APIKey, target, path string, outer, development bool) int {
					t.Helper()
					old := call(t, 0, user, key, target, path, outer, development)
					bounded := call(t, 1, user, key, target, path, outer, development)
					pairs++
					legacySQL, boundedSQL = legacySQL+old.sql, boundedSQL+bounded.sql
					maximumLegacySQL, maximumBoundedSQL = max(maximumLegacySQL, old.sql), max(maximumBoundedSQL, bounded.sql)
					maximumPreflights = max(maximumPreflights, bounded.preflight)
					t.Logf("legacy_sql=%d bounded_sql=%d delta=%d bounded_aggregate_preflights=%d", old.sql, bounded.sql, bounded.sql-old.sql, bounded.preflight)
					if old.Code != bounded.Code || !reflect.DeepEqual(old.Header(), bounded.Header()) || !bytes.Equal(old.Body.Bytes(), bounded.Body.Bytes()) {
						t.Fatalf("document auxiliary route differs path=%s development=%t status=%d/%d\nliteral=%s\nbounded=%s", path, development, old.Code, bounded.Code, old.Body, bounded.Body)
					}
					if bytes.Contains(bounded.Body.Bytes(), []byte("SW130_EXCLUDED_UNRELATED_DRAFT")) {
						t.Fatal("auxiliary route exposed an unrelated draft")
					}
					return bounded.Code
				}
				routes := []struct{ name, path string }{
					{"drafts-id", "/api/documents/" + live + "/drafts"},
					{"drafts-current", "/api/documents/" + prefix + "current/drafts"},
					{"drafts-old", "/api/documents/" + prefix + "old/drafts"},
					{"history-id", "/api/documents/" + live + "/history"},
					{"history-current", "/api/documents/" + prefix + "current/history"},
					{"history-old", "/api/documents/" + prefix + "old/history"},
					{"history-trash-id", "/api/documents/" + deleted + "/history"},
					{"history-trash-current", "/api/documents/" + prefix + "deleted-current/history"},
					{"history-trash-old", "/api/documents/" + prefix + "deleted-old/history"},
					{"live-role-blocks-trash", "/api/documents/" + blocked + "/history"},
					{"nil-history", "/api/documents/" + prefix + "nil-history/history"},
					{"empty-history", "/api/documents/" + prefix + "empty-history/history"},
					{"missing-drafts", "/api/documents/" + prefix + "missing/drafts"},
					{"missing-history", "/api/documents/" + prefix + "missing/history"},
				}
				f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
					key = scaleP21SW130Key(prefix, user, key)
					if key != nil {
						credential, err := repo.AuthenticateAPIKeyRecord(t.Context(), workspace, key.SecretHash, nil)
						if err != nil || credential.User.ID != user.ID || credential.Key.ID != key.ID {
							t.Fatal("auxiliary test key is not live", err)
						}
						key = &credential.Key
					}
					for _, route := range routes {
						t.Run(route.name, func(t *testing.T) {
							code := compare(t, user, key, workspace, route.path, false, false)
							if strings.HasPrefix(route.name, "missing") && code != http.StatusNotFound ||
								user.ID == f.principals[4].user.ID && key == nil && route.name == "live-role-blocks-trash" && code != http.StatusNotFound {
								t.Fatal("auxiliary lookup/fallback precedence changed", route.name, code)
							}
						})
					}
				})
				for _, query := range []string{"?limit=1", "?cursor=1&limit=1", "?cursor=-2&limit=500"} {
					for _, route := range []int{0, 3} {
						t.Run("pagination/"+routes[route].name+"/"+query, func(t *testing.T) {
							compare(t, f.data.Viewer, nil, workspace, routes[route].path+query, false, false)
						})
					}
				}
				for _, route := range []int{0, 3, 6} {
					t.Run("outer/"+routes[route].name, func(t *testing.T) {
						compare(t, f.data.Viewer, nil, workspace, routes[route].path, true, false)
					})
				}
				developmentKey := scaleP21SW130Key(prefix, f.data.Viewer, &domain.APIKey{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}})
				credential, err := repo.AuthenticateAPIKeyRecord(t.Context(), workspace, developmentKey.SecretHash, nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, key := range []*domain.APIKey{nil, &credential.Key} {
					mode := "development-session/"
					if key != nil {
						mode = "development-selected-empty/"
					}
					for _, route := range []int{0, 2, 3, 6} {
						t.Run(mode+routes[route].name, func(t *testing.T) {
							if code := compare(t, f.data.Viewer, key, workspace, routes[route].path, false, true); code != http.StatusOK {
								t.Fatal("development auxiliary fixture refused", code)
							}
						})
					}
				}
				for _, route := range []int{0, 3} {
					t.Run("foreign/"+routes[route].name, func(t *testing.T) {
						if code := compare(t, f.principals[2].user, nil, other.Workspace.URLKey, routes[route].path, false, false); code != http.StatusNotFound {
							t.Fatal("direct auxiliary handler changed its literal foreign-workspace refusal", code)
						}
					})
				}
				t.Logf("source_state=%s route_pairs=%d legacy_sql_total=%d bounded_sql_total=%d delta_total=%d maximum_legacy_sql=%d maximum_bounded_sql=%d maximum_bounded_aggregate_preflights=%d", sourceState, pairs, legacySQL, boundedSQL, boundedSQL-legacySQL, maximumLegacySQL, maximumBoundedSQL, maximumPreflights)
			})
		}
	}
}
