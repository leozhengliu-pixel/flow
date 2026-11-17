package main

import (
	"bytes"
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

// scaleP21SW284InWrapper reports whether the calling goroutine is inside one
// of the closed store wrappers. The SQL observer runs on the goroutine that
// issues the statement, so a statement a wrapper causes is attributed to it.
func scaleP21SW284InWrapper() bool {
	pcs := make([]uintptr, 256)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, "(*SQLiteStore).SelectedDocumentMetadata") || strings.HasSuffix(frame.Function, "(*SQLiteStore).TrashRecordHeader") {
			return true
		}
		if !more {
			return false
		}
	}
}

type scaleP21SW284Outcome struct {
	Code   int
	Header http.Header
	Body   []byte
}

// scaleP21SW284Authorize runs the real workspace authorization (session
// cookies through authenticate, credentials and development mode directly)
// and answers 204 when the request would reach its handler. The COLD
// handlers themselves are not run: only the authorization that reaches the
// wrappers is compared. It returns the statements run inside a wrapper.
func scaleP21SW284Authorize(t *testing.T, f scaleP21M2Fixture, source *store.SQLiteStore, user domain.User, key *domain.APIKey, token, method, path string, body any, development bool) (scaleP21SW284Outcome, int) {
	t.Helper()
	var request *http.Request
	if key == nil && !development {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		request = httptest.NewRequest(method, path, bytes.NewReader(raw))
		request.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	} else {
		request = f.literalRequest(user, key, body)
		request.Method, request.URL.Path = method, path
	}
	inWrapper := 0
	request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) {
		if scaleP21SW284InWrapper() {
			inWrapper++
		}
	}))
	decodes := source.WorkspaceMetadataDecodes()
	w := httptest.NewRecorder()
	func() {
		request, closeOutput := beginRequestResourceReads(request)
		defer closeOutput()
		service := &server{store: source, authDisabled: development}
		reached := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		if key == nil && !development {
			service.authenticate(reached).ServeHTTP(w, request)
		} else if service.authorizeWorkspaceRequest(w, request, user) {
			reached.ServeHTTP(w, request)
		}
	}()
	if source.BoundedMetadataEnabled() {
		if inWrapper != 0 {
			t.Fatalf("%s %s ran %d statements inside a closed wrapper with the flag on", method, path, inWrapper)
		}
		if source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != decodes {
			t.Fatalf("%s %s leaked retained work or decoded the complete workspace", method, path)
		}
	}
	return scaleP21SW284Outcome{Code: w.Code, Header: w.Header().Clone(), Body: slices.Clone(w.Body.Bytes())}, inWrapper
}

type scaleP21SW284Route struct {
	name, method, path string
	body               any
}

func scaleP21SW284Seed(t *testing.T, f scaleP21M2Fixture, repo *store.SQLiteStore) (private string, sessions map[string]string) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	stamp := time.Date(2025, time.March, 4, 5, 6, 7, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw284_seed", "", nil, func(data *domain.Bootstrap) error {
		for _, team := range data.Teams {
			if strings.HasPrefix(team.ID, "m2-private-") {
				private = team.ID
			}
		}
		archived := stamp
		data.Customers = append(data.Customers, domain.Customer{ID: "sw284-customer", Name: "SW284 customer"})
		data.Projects = append(data.Projects,
			domain.Project{ID: "sw284-public", Name: "Public", SlugID: "sw284-public-000000000284", TeamIDs: []string{f.public}, Initiatives: []string{"sw284-initiative"}},
			domain.Project{ID: "sw284-private", Name: "Private", SlugID: "sw284-private-000000000284", PreviousSlugIDs: []string{"sw284-old"}, TeamIDs: []string{private}},
			domain.Project{ID: "sw284-archived", Name: "Archived", TeamIDs: []string{f.public}, ArchivedAt: &archived},
			domain.Project{ID: "sw284-teamless", Name: "Teamless"},
		)
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: "sw284-initiative", Name: "Public initiative", LeadTeamID: f.public, ProjectIDs: []string{"sw284-public"}},
			domain.Initiative{ID: "sw284-private-initiative", Name: "Private initiative", LeadTeamID: private, ProjectIDs: []string{"sw284-private"}},
			domain.Initiative{ID: "sw284-empty-initiative", Name: "Empty initiative"},
		)
		data.ProjectRelations = append(data.ProjectRelations,
			domain.ProjectRelation{ID: "sw284-relation", ProjectID: "sw284-public", RelatedProjectID: "sw284-private", Type: "blocks", CreatedAt: stamp, UpdatedAt: stamp},
			domain.ProjectRelation{ID: "sw284-open-relation", ProjectID: "sw284-public", RelatedProjectID: "sw284-teamless", Type: "related", CreatedAt: stamp, UpdatedAt: stamp},
		)
		data.InitiativeRelations = append(data.InitiativeRelations,
			domain.InitiativeRelation{ID: "sw284-initiative-relation", InitiativeID: "sw284-initiative", RelatedInitiativeID: "sw284-private-initiative", Type: "related", CreatedAt: stamp, UpdatedAt: stamp},
		)
		project, err := json.Marshal(domain.Project{ID: "sw284-trashed", Name: "Trashed", TeamIDs: []string{private}})
		if err != nil {
			return err
		}
		document, err := json.Marshal(domain.Document{ID: "sw284-trashed-document", Title: "Trashed document", Creator: data.Viewer, TeamIDs: []string{f.public}})
		if err != nil {
			return err
		}
		secret, err := json.Marshal(domain.Document{ID: "sw284-secret-document", Title: "Secret document", Creator: data.Viewer, TeamIDs: []string{private}})
		if err != nil {
			return err
		}
		pipeline, err := json.Marshal(domain.ReleasePipeline{ID: "sw284-pipeline", Name: "Pipeline", TeamIDs: []string{f.public}})
		if err != nil {
			return err
		}
		data.Trash = append(data.Trash,
			domain.TrashEntry{ID: "sw284-trash-project", ResourceType: "project", ResourceID: "sw284-trashed", Title: "Trashed", Payload: project, TeamIDs: []string{private}, DeletedBy: data.Viewer, DeletedAt: stamp, ExpiresAt: stamp.Add(30 * 24 * time.Hour)},
			domain.TrashEntry{ID: "sw284-trash-document", ResourceType: "document", ResourceID: "sw284-trashed-document", Title: "Trashed document", Payload: document, TeamIDs: []string{f.public}, DeletedBy: data.Viewer, DeletedAt: stamp, ExpiresAt: stamp.Add(30 * 24 * time.Hour),
				Links: &domain.TrashLinks{ProjectResources: []domain.ProjectResource{{ID: "sw284-trashed-document", ProjectID: "sw284-public"}}}},
			domain.TrashEntry{ID: "sw284-trash-secret", ResourceType: "document", ResourceID: "sw284-secret-document", Title: "Secret document", Payload: secret, TeamIDs: []string{private}, DeletedBy: data.Viewer, DeletedAt: stamp, ExpiresAt: stamp.Add(30 * 24 * time.Hour)},
			domain.TrashEntry{ID: "sw284-trash-pipeline", ResourceType: "release_pipeline", ResourceID: "sw284-pipeline", Title: "Pipeline", Payload: pipeline, TeamIDs: []string{f.public}, DeletedBy: data.Viewer, DeletedAt: stamp, ExpiresAt: stamp.Add(30 * 24 * time.Hour)},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if private == "" {
		t.Fatal("private team missing")
	}
	sessions = map[string]string{}
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
	return private, sessions
}

func scaleP21SW284Credentials(f scaleP21M2Fixture, visit func(name string, user domain.User, key *domain.APIKey)) {
	for _, principal := range f.principals {
		for _, credential := range []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"unrestricted", &domain.APIKey{ID: "sw284-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
			{"selected-empty", &domain.APIKey{ID: "sw284-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}},
			{"selected-set", &domain.APIKey{ID: "sw284-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
			{"app", &domain.APIKey{ID: "sw284-app", Actor: "app", Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
		} {
			visit(principal.name+"/"+credential.name, principal.user, credential.key)
		}
	}
}

// TestScaleP21SW284ProjectRecordAuthorizationDifferential drives the P16
// project and initiative write authorization, the only caller that reached
// the arbitrary selector with the flag on, through the real middleware for
// every principal and credential and development mode. With the flag on the
// P16 record flags must change nothing: the answer equals the literal
// authorization with them off, no statement runs inside the selector and the
// workspace is never decoded. With the flag off the selector still serves
// the flags (a positive control for the statement probe) with the answer the
// flags-off authorization gives. HOT reads (GET/HEAD) are compared in every
// FLOW_RECORD_ACCESS mode.
func TestScaleP21SW284ProjectRecordAuthorizationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		_, sessions := scaleP21SW284Seed(t, f, repo)
		writes := []scaleP21SW284Route{
			{"create-project", http.MethodPost, "/api/projects", map[string]any{"name": "SW284", "teamIds": []string{f.public}, "initiatives": []string{"sw284-private-initiative"}, "customers": []string{"sw284-customer"}, "dependencyIds": []string{"sw284-private"}}},
			{"edit-public", http.MethodPatch, "/api/projects/sw284-public", map[string]any{"name": "Renamed"}},
			{"edit-private", http.MethodPatch, "/api/projects/sw284-private", map[string]any{"name": "Renamed"}},
			{"edit-old-slug", http.MethodPatch, "/api/projects/sw284-old", map[string]any{"name": "Renamed"}},
			{"edit-slug", http.MethodPatch, "/api/projects/sw284-public-000000000284", map[string]any{"initiatives": []string{"sw284-private-initiative"}}},
			{"edit-archived", http.MethodPatch, "/api/projects/sw284-archived", map[string]any{"name": "Renamed"}},
			{"edit-teamless", http.MethodPatch, "/api/projects/sw284-teamless", map[string]any{"name": "Renamed"}},
			{"delete-trashed", http.MethodDelete, "/api/projects/sw284-trashed", nil},
			{"delete-missing", http.MethodDelete, "/api/projects/sw284-missing", nil},
			{"comment", http.MethodPost, "/api/projects/sw284-public/comments", map[string]any{"body": "hello"}},
			{"relation-create", http.MethodPost, "/api/projects/sw284-public/relations", map[string]any{"relatedProjectId": "sw284-private", "type": "blocks"}},
			{"relation-update", http.MethodPatch, "/api/projects/sw284-public/relations/sw284-relation", map[string]any{"type": "related"}},
			{"relation-delete-open", http.MethodDelete, "/api/projects/sw284-public/relations/sw284-open-relation", nil},
			{"create-initiative", http.MethodPost, "/api/initiatives", map[string]any{"name": "SW284", "projectIds": []string{"sw284-private"}, "parentInitiativeIds": []string{"sw284-initiative"}}},
			{"edit-initiative", http.MethodPatch, "/api/initiatives/sw284-initiative", map[string]any{"projectIds": []string{"sw284-public", "sw284-private"}}},
			{"edit-private-initiative", http.MethodPatch, "/api/initiatives/sw284-private-initiative", map[string]any{"name": "Renamed"}},
			{"delete-empty-initiative", http.MethodDelete, "/api/initiatives/sw284-empty-initiative", nil},
			{"initiative-relation-create", http.MethodPost, "/api/initiatives/sw284-initiative/relations", map[string]any{"relatedInitiativeId": "sw284-private-initiative", "type": "related"}},
			{"initiative-relation-delete", http.MethodDelete, "/api/initiatives/sw284-initiative/relations/sw284-initiative-relation", nil},
		}
		reads := []scaleP21SW284Route{
			{"read-public", http.MethodGet, "/api/projects/sw284-public", nil},
			{"read-private", http.MethodGet, "/api/projects/sw284-private", nil},
			{"read-old-slug", http.MethodHead, "/api/projects/sw284-old", nil},
			{"read-initiative", http.MethodGet, "/api/initiatives/sw284-private-initiative", nil},
			{"read-missing", http.MethodGet, "/api/initiatives/sw284-missing", nil},
		}
		flags := func(t *testing.T, value string) {
			t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", value)
			t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", value)
		}
		compare := func(t *testing.T, user domain.User, key *domain.APIKey, development bool, route scaleP21SW284Route) (allowed bool) {
			t.Helper()
			token := sessions[user.ID]
			flags(t, "off")
			literal, _ := scaleP21SW284Authorize(t, f, repo, user, key, token, route.method, route.path, route.body, development)
			bounded, _ := scaleP21SW284Authorize(t, f, f.probe, user, key, token, route.method, route.path, route.body, development)
			flags(t, "on")
			literalRecords, selected := scaleP21SW284Authorize(t, f, repo, user, key, token, route.method, route.path, route.body, development)
			boundedRecords, _ := scaleP21SW284Authorize(t, f, f.probe, user, key, token, route.method, route.path, route.body, development)
			flags(t, "off")
			if !reflect.DeepEqual(bounded, boundedRecords) {
				t.Fatalf("%s %s: bounded answer depends on the P16 record flags: off=%d %s on=%d %s", route.method, route.path, bounded.Code, bounded.Body, boundedRecords.Code, boundedRecords.Body)
			}
			if !reflect.DeepEqual(literal, literalRecords) {
				t.Fatalf("%s %s: literal answer depends on the P16 record flags: off=%d %s on=%d %s", route.method, route.path, literal.Code, literal.Body, literalRecords.Code, literalRecords.Body)
			}
			// The probe sees the selector with the flag off whenever the P16
			// selection applies (non-GET, authorized membership, resource
			// authorization reached).
			if !development && route.method != http.MethodGet && route.method != http.MethodHead && literal.Code == http.StatusNoContent && selected == 0 {
				t.Fatalf("%s %s: the flag-off P16 selector was not observed; the statement probe is blind", route.method, route.path)
			}
			return literal.Code == http.StatusNoContent
		}
		allowed := 0
		scaleP21SW284Credentials(f, func(name string, user domain.User, key *domain.APIKey) {
			t.Run(name, func(t *testing.T) {
				for _, route := range writes {
					if compare(t, user, key, false, route) {
						allowed++
					}
				}
				for _, mode := range []string{"on", "shadow", "off"} {
					t.Setenv("FLOW_RECORD_ACCESS", mode)
					for _, route := range reads {
						compare(t, user, key, false, route)
					}
				}
			})
		})
		if allowed == 0 {
			t.Fatal("no literal write authorization was allowed; the differential compared only refusals")
		}
		t.Run("development", func(t *testing.T) {
			for _, route := range append(slices.Clone(writes), reads...) {
				compare(t, f.data.Viewer, nil, true, route)
			}
		})
	}
}

// TestScaleP21SW285TrashRestoreHeaderDifferential drives the trash restore
// authorization, the only caller of the resident trash header, for document,
// private document, project and release-pipeline entries and for paths of
// the restore shape the mux does not bind. With the flag on the resident copy
// holds no trash, so the header lookup never selected; the bounded answer is
// the FLOW_TRASH_RECORDS=off answer, no statement runs inside the wrappers
// and the workspace is never decoded. With the flag off the header still
// serves the record mode with the flags-off answer.
func TestScaleP21SW285TrashRestoreHeaderDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		_, sessions := scaleP21SW284Seed(t, f, repo)
		workspace := f.data.Workspace.URLKey
		for _, id := range []string{"sw284-trash-document", "sw284-trash-secret", "sw284-trash-project", "sw284-trash-pipeline"} {
			literal, found := repo.TrashRecordHeader(workspace, id)
			if !found || literal.Payload != nil || literal.Links != nil {
				t.Fatalf("literal header %s found=%t payload=%s links=%v", id, found, literal.Payload, literal.Links)
			}
			// The bounded resident copy is policy only: before this change
			// the bounded lookup never selected, which is what it now skips.
			if _, found := f.probe.TrashRecordHeader(workspace, id); found {
				t.Fatalf("bounded resident copy holds trash entry %s", id)
			}
			if trash := f.probe.TrashSnapshot(workspace); len(trash) != 0 {
				t.Fatalf("bounded resident copy holds %d trash entries", len(trash))
			}
		}
		routes := []scaleP21SW284Route{
			{"restore-document", http.MethodPost, "/api/trash/sw284-trash-document/restore", nil},
			{"restore-secret", http.MethodPost, "/api/trash/sw284-trash-secret/restore", nil},
			{"restore-project", http.MethodPost, "/api/trash/sw284-trash-project/restore", nil},
			{"restore-pipeline", http.MethodPost, "/api/trash/sw284-trash-pipeline/restore", nil},
			{"restore-missing", http.MethodPost, "/api/trash/sw284-missing/restore", nil},
			{"restore-empty", http.MethodPost, "/api/trash//restore", nil},
			{"get-restore-shape", http.MethodGet, "/api/trash/sw284-trash-document/restore", nil},
			{"purge-document", http.MethodDelete, "/api/trash/sw284-trash-document", nil},
		}
		compare := func(t *testing.T, user domain.User, key *domain.APIKey, development bool, route scaleP21SW284Route) bool {
			t.Helper()
			token := sessions[user.ID]
			t.Setenv("FLOW_TRASH_RECORDS", "off")
			literal, _ := scaleP21SW284Authorize(t, f, repo, user, key, token, route.method, route.path, route.body, development)
			bounded, _ := scaleP21SW284Authorize(t, f, f.probe, user, key, token, route.method, route.path, route.body, development)
			t.Setenv("FLOW_TRASH_RECORDS", "on")
			literalRecords, _ := scaleP21SW284Authorize(t, f, repo, user, key, token, route.method, route.path, route.body, development)
			boundedRecords, _ := scaleP21SW284Authorize(t, f, f.probe, user, key, token, route.method, route.path, route.body, development)
			t.Setenv("FLOW_TRASH_RECORDS", "off")
			if !reflect.DeepEqual(bounded, boundedRecords) {
				t.Fatalf("%s %s: bounded answer depends on FLOW_TRASH_RECORDS: off=%d %s on=%d %s", route.method, route.path, bounded.Code, bounded.Body, boundedRecords.Code, boundedRecords.Body)
			}
			if !reflect.DeepEqual(literal, literalRecords) {
				t.Fatalf("%s %s: literal answer depends on FLOW_TRASH_RECORDS: off=%d %s on=%d %s", route.method, route.path, literal.Code, literal.Body, literalRecords.Code, literalRecords.Body)
			}
			return literal.Code == http.StatusNoContent
		}
		allowed := 0
		scaleP21SW284Credentials(f, func(name string, user domain.User, key *domain.APIKey) {
			t.Run(name, func(t *testing.T) {
				for _, route := range routes {
					if compare(t, user, key, false, route) {
						allowed++
					}
				}
			})
		})
		if allowed == 0 {
			t.Fatal("no literal restore authorization was allowed; the differential compared only refusals")
		}
		t.Run("development", func(t *testing.T) {
			for _, route := range routes {
				compare(t, f.data.Viewer, nil, true, route)
			}
		})
	}
}
