package main

import (
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

func TestScaleP21SW350HistoryAuthorizationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	foreign := scaleP03Fixture(t, repo)
	prefix := idgen.New("sw350-")
	projectID, initiativeID, privateInitiative := prefix+"project", prefix+"initiative", prefix+"private-initiative"
	oldProject, oldInitiative := prefix+"old-project", prefix+"old-initiative"
	data, found := repo.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("owned history workspace missing")
	}
	privateTeam := ""
	for _, project := range data.Projects {
		if project.ID == "m2-unrelated" && len(project.TeamIDs) == 1 {
			privateTeam = project.TeamIDs[0]
		}
	}
	if privateTeam == "" {
		t.Fatal("existing M2 private-team control missing")
	}
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw350_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{
			ID: projectID, SlugID: prefix + "project-current", PreviousSlugIDs: []string{oldProject}, Name: "History project", TeamIDs: []string{f.public},
		})
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: initiativeID, SlugID: prefix + "initiative-current", PreviousSlugIDs: []string{oldInitiative}, Name: "History initiative", ProjectIDs: []string{projectID}, ContributingTeamIDs: []string{f.public}},
			domain.Initiative{ID: privateInitiative, Name: "Private history initiative", LeadTeamID: privateTeam, ProjectIDs: []string{"m2-unrelated"}},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	type historyTarget struct{ kind, id, old, private string }
	targets := []historyTarget{
		{"projects", projectID, oldProject, "m2-unrelated"},
		{"initiatives", initiativeID, oldInitiative, privateInitiative},
	}
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, target := range targets {
		kind := strings.TrimSuffix(target.kind, "s")
		if err := repo.AppendAuditEntries(t.Context(), f.data.Workspace.ID,
			domain.AuditLogEntry{ID: prefix + kind + "-public-audit", Actor: f.data.Viewer, Action: "updated", ResourceType: kind, ResourceID: target.id, Metadata: map[string]any{"title": "Exact public history"}, CreatedAt: clock},
			domain.AuditLogEntry{ID: prefix + kind + "-private-audit", Actor: f.principals[1].user, Action: "updated", ResourceType: kind, ResourceID: target.private, Metadata: map[string]any{"title": "Exact private history"}, CreatedAt: clock.Add(time.Second)},
		); err != nil {
			t.Fatal(err)
		}
		if err := repo.AppendAuditEntries(t.Context(), foreign.data.Workspace.ID,
			domain.AuditLogEntry{ID: prefix + kind + "-foreign-audit", Actor: foreign.data.Viewer, Action: "updated", ResourceType: kind, ResourceID: target.id, Metadata: map[string]any{"title": "Foreign history must stay hidden"}, CreatedAt: clock},
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	f.reader.key = workspace
	tokens := map[string]string{}
	for _, user := range []domain.User{f.principals[0].user, f.principals[1].user, f.principals[2].user} {
		_, token, err := repo.Login(t.Context(), user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		tokens[user.ID] = token
	}
	type outcome struct {
		Allowed                bool
		Code                   int
		Body, Pattern, PathRef string
	}
	basePairs, controlPairs, successes, refusals := 0, 0, 0, 0
	runPair := func(t *testing.T, target historyTarget, ref, method, phase string, user domain.User, key *domain.APIKey, foreignWorkspace, required bool, wantTotal int) {
		t.Helper()
		var want outcome
		legacySQL := 0
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			if source.BoundedMetadataEnabled() {
				if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "reloaded" {
				scaleP21SW201ReloadSource(t, f.reader, source)
			}
			request := f.literalRequest(user, key, nil)
			request.Method = method
			var err error
			request.URL, err = url.Parse("/api/" + target.kind + "/" + url.PathEscape(ref) + "/history")
			if err != nil {
				t.Fatal(err)
			}
			if foreignWorkspace {
				request.Header.Set("X-Workspace-Key", foreign.data.Workspace.URLKey)
			}
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
			request, release := beginRequestResourceReads(request)
			service := &server{store: source}
			statements := 0
			request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
			recorder := httptest.NewRecorder()
			allowed := service.authorizeWorkspaceRequest(recorder, request, user)
			pattern, pathRef := "", ""
			if allowed {
				mux := http.NewServeMux()
				mux.HandleFunc("GET /api/projects/{id}/history", func(w http.ResponseWriter, r *http.Request) {
					pathRef = r.PathValue("id")
					service.projectHistory(w, r)
				})
				mux.HandleFunc("GET /api/initiatives/{id}/history", func(w http.ResponseWriter, r *http.Request) {
					pathRef = r.PathValue("id")
					service.initiativeHistory(w, r)
				})
				_, pattern = mux.Handler(request)
				mux.ServeHTTP(recorder, request)
			}
			got := outcome{allowed, recorder.Code, recorder.Body.String(), pattern, pathRef}
			expectedCode := http.StatusForbidden
			if required {
				expectedCode = http.StatusOK
			}
			if allowed != required || got.Code != expectedCode || required && (pattern != "GET /api/"+target.kind+"/{id}/history" || pathRef != ref) {
				release()
				t.Fatalf("history source authorization/actual route changed: required=%t got=%+v", required, got)
			}
			if required {
				var page struct {
					Nodes      []domain.AuditLogEntry `json:"nodes"`
					NextCursor string                 `json:"nextCursor"`
					Total      int                    `json:"total"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil || page.Total != wantTotal || len(page.Nodes) != wantTotal || page.NextCursor != "" {
					release()
					t.Fatalf("required exact history output missing: total=%d nodes=%d want=%d error=%v", page.Total, len(page.Nodes), wantTotal, err)
				}
				for _, entry := range page.Nodes {
					if entry.ResourceType != strings.TrimSuffix(target.kind, "s") || entry.ResourceID != ref ||
						entry.Metadata["title"] == "Foreign history must stay hidden" {
						release()
						t.Fatal("history returned a hidden actor/resource or foreign-workspace row", entry)
					}
				}
			}
			if !source.BoundedMetadataEnabled() {
				want, legacySQL = got, statements
			} else {
				if !reflect.DeepEqual(want, got) {
					release()
					t.Fatalf("actual P16 history output differs: literal=%+v bounded=%+v", want, got)
				}
				if allowed {
					successes++
				} else {
					refusals++
				}
				if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
					release()
					t.Fatal("bounded history authorizer created a complete paged handoff")
				}
				t.Logf("history=%s ref=%s method=%s phase=%s SQL literal/bounded=%d/%d increase=%d", target.kind, ref, method, phase, legacySQL, statements, statements-legacySQL)
			}
			release()
			release()
			if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
				t.Fatal("history authorization/output retained work or decoded full metadata")
			}
		}
	}
	for index, target := range targets {
		t.Run(target.kind, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_ACCESS", []string{"off", "shadow"}[index])
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, phase := range []string{"warm", "reloaded"} {
					t.Run(method+"/"+phase, func(t *testing.T) {
						runPair(t, target, target.id, method, phase, f.principals[0].user, nil, false, true, 1)
						basePairs++
					})
				}
			}
		})
	}
	t.Run("representative-source-and-leaf-controls", func(t *testing.T) {
		t.Setenv("FLOW_RECORD_ACCESS", "on")
		selectedEmpty := &domain.APIKey{
			ID: "sw350-selected-empty", Scopes: []string{"read", "write", "admin"},
			TeamRestriction: "selected", TeamIDs: []string{},
		}
		for _, target := range targets {
			for _, row := range []struct {
				name, ref string
				user      domain.User
				key       *domain.APIKey
				foreign   bool
				allowed   bool
				total     int
			}{
				{"private-outsider", target.private, f.principals[0].user, nil, false, false, 0},
				{"private-member-admin", target.private, f.principals[1].user, nil, false, true, 1},
				{"selected-empty", target.id, f.principals[0].user, selectedEmpty, false, false, 0},
				{"foreign-workspace", target.id, f.principals[0].user, nil, true, false, 0},
				// The literal handler queries this unmodified old-slug ID and
				// therefore returns no audit rows after successful source ACL.
				{"old-slug", target.old, f.principals[0].user, nil, false, true, 0},
				{"nonadmin-empty-history", target.id, f.principals[2].user, nil, false, true, 0},
			} {
				t.Run(target.kind+"/"+row.name, func(t *testing.T) {
					runPair(t, target, row.ref, http.MethodGet, "warm", row.user, row.key, row.foreign, row.allowed, row.total)
					controlPairs++
				})
			}
		}
	})
	if basePairs != 8 || controlPairs != 12 || successes != 14 || refusals != 6 {
		t.Fatal("history table lost required source/leaf controls", basePairs, controlPairs, successes, refusals)
	}
}
