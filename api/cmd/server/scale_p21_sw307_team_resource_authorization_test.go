package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW307TeamResourceRouteDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP21SW215AuthMatrixSeed(t, repo)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw307-")
			data, found := repo.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("missing actual credential fixture")
			}
			private, child, publicKey := "", "", prefix+"PUBLIC"
			publicFound := false
			for _, team := range data.Teams {
				if team.ID == f.public {
					publicFound = true
				}
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			for id, settings := range data.TeamSettings {
				if settings.ParentTeamID == private {
					child = id
				}
			}
			if private == "" || child == "" || private == child || !publicFound || publicKey == f.public {
				t.Fatal("M fixture lacks private/inherited/raw-key distinctions")
			}
			empty := prefix + "empty"
			at := time.Date(2026, 11, 17, 1, 2, 3, 456, time.FixedZone("sw307-native", 7*3600+123))
			restore := store.ForceFullMutationsForTesting(true)
			err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw307_seed", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.Teams {
					if data.Teams[index].ID == f.public {
						data.Teams[index].Key = publicKey
					}
				}
				data.Teams = append(data.Teams,
					domain.Team{ID: empty, Key: prefix + "EMPTY"},
					domain.Team{ID: prefix + "visible-alias", Key: private},
				)
				for _, team := range []string{empty, prefix + "visible-alias"} {
					data.TeamSettings[team] = domain.TeamSettings{}
				}
				data.TeamResourceSections = nil
				data.TeamPinnedResources = nil
				for _, team := range []string{f.public, private, child} {
					// Repeated/empty IDs force legal inline arrays. Equal
					// positions must preserve the original order and fields.
					data.TeamResourceSections = append(data.TeamResourceSections,
						domain.TeamResourceSection{ID: prefix + "duplicate", TeamID: team, Name: "later", Position: 2, CreatedAt: at, UpdatedAt: at},
						domain.TeamResourceSection{ID: prefix + "duplicate", TeamID: team, Name: "first tie", Position: 1, CreatedAt: at, UpdatedAt: at},
						domain.TeamResourceSection{ID: "", TeamID: team, Name: "second tie", Position: 1, CreatedAt: at, UpdatedAt: at},
					)
					data.TeamPinnedResources = append(data.TeamPinnedResources,
						domain.TeamPinnedResource{ID: prefix + "duplicate", TeamID: team, ResourceType: "link", ResourceID: "literal-hidden-reference", Title: "later", URL: "https://example.test/<&>", Position: 2, CreatedAt: at, UpdatedAt: at},
						domain.TeamPinnedResource{ID: "", TeamID: team, ResourceType: "link", ResourceID: "literal-hidden-reference", Title: "first tie", Position: 1, CreatedAt: at, UpdatedAt: at},
						domain.TeamPinnedResource{ID: prefix + "duplicate", TeamID: team, ResourceType: "link", ResourceID: "literal-hidden-reference", Title: "second tie", Position: 1, CreatedAt: at, UpdatedAt: at},
					)
				}
				data.TeamResourceSections = append(data.TeamResourceSections, domain.TeamResourceSection{
					ID: prefix + "unrelated", TeamID: prefix + "not-selected", Name: strings.Repeat("unrelated section ", 50000),
				})
				return nil
			})
			restore()
			if err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				code int
				body string
				sql  int
			}
			totalPairs, legacySQL, boundedSQL, successes := 0, 0, 0, 0
			for _, reload := range []bool{false, true} {
				if reload {
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.foreign)
				if err != nil {
					t.Fatal(err)
				}
				for _, principal := range f.principals {
					for _, credential := range f.credentials[principal.user.ID] {
						for _, row := range []struct {
							name, ref, workspace string
							denied               bool
						}{
							{"public-id", f.public, workspace, false},
							{"raw-key", publicKey, workspace, true},
							{"private-id-before-visible-key", private, workspace, false},
							{"inherited-child", child, workspace, false},
							{"empty-arrays", empty, workspace, false},
							{"missing", prefix + "missing", workspace, true},
							{"foreign", f.public, f.foreign, true},
							{"default-workspace", f.public, "", false},
						} {
							for _, method := range []string{http.MethodGet, http.MethodHead} {
								name := fmt.Sprintf("reload=%t/%s/%s/%s/%s", reload, principal.name, credential.name, row.name, method)
								t.Run(name, func(t *testing.T) {
									var want outcome
									for _, source := range []*store.SQLiteStore{repo, view} {
										service := &server{store: source}
										mux := http.NewServeMux()
										var scope *requestResourceReads
										var handoff teamResourcesRead
										mux.HandleFunc("GET /api/teams/{id}/resources", func(w http.ResponseWriter, r *http.Request) {
											if source.BoundedMetadataEnabled() {
												scope, _ = r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
												handoff, _ = r.Context().Value(teamResourcesReadKey{}).(teamResourcesRead)
												if method == http.MethodHead && handoff.TeamID != "" {
													t.Fatal("HEAD acquired GET resource output")
												}
											}
											service.listTeamResources(w, r)
										})
										request := httptest.NewRequest(method, "/api/teams/"+row.ref+"/resources", nil)
										request.Header.Set("X-Workspace-Key", row.workspace)
										if credential.session != "" {
											request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: credential.session})
										} else {
											request.Header.Set("Authorization", "Bearer "+credential.bearer)
										}
										statements, resourceRows, discussionRows := 0, 0, 0
										request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(query string) {
											statements++
											if strings.Contains(query, "field='teamResourceSections'") || strings.Contains(query, "field='teamPinnedResources'") {
												resourceRows++
											}
											if strings.Contains(query, "FROM issue_records") || strings.Contains(query, "FROM workspace_content_records") {
												discussionRows++
											}
										}))
										recorder := httptest.NewRecorder()
										outputChecks := 0
										writer := &scaleP21SW215ColdOutputWriter{ResponseRecorder: recorder, check: func() {
											outputChecks++
											if source.BoundedMetadataEnabled() && handoff.TeamID != "" &&
												(scope == nil || scope.closed || source.MetadataWorkUsedForTesting() == 0) {
												t.Error("team resource output outlived its complete owner")
											}
										}}
										service.authenticate(mux).ServeHTTP(writer, request)
										got := outcome{recorder.Code, recorder.Body.String(), statements}
										if outputChecks == 0 {
											t.Fatal("actual route did not encode an outcome")
										}
										if method == http.MethodHead || row.denied || credential.name == "selected-empty" ||
											credential.name == "selected-set" && row.ref != f.public {
											if got.code == http.StatusOK {
												t.Fatal("literal denied route became successful", got.code)
											}
										}
										if got.code == http.StatusOK {
											successes++
											var rows teamResourcesRead
											if err := json.Unmarshal(recorder.Body.Bytes(), &rows); err != nil {
												t.Fatal(err)
											}
											if rows.Sections == nil || rows.Resources == nil {
												t.Fatal("empty resources lost array shape")
											}
											if len(rows.Sections) != 0 && (rows.Sections[0].Name != "first tie" || rows.Sections[1].Name != "second tie" ||
												rows.Resources[0].Title != "first tie" || rows.Resources[1].Title != "second tie") {
												t.Fatal("inline duplicate order changed")
											}
										}
										if !source.BoundedMetadataEnabled() {
											want = got
										} else {
											totalPairs++
											legacySQL, boundedSQL = legacySQL+want.sql, boundedSQL+got.sql
											if want.code != got.code || !bytes.Equal([]byte(want.body), []byte(got.body)) {
												t.Fatalf("actual route differs: legacy=%d/%s bounded=%d/%s", want.code, want.body, got.code, got.body)
											}
											if source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0 || scope != nil && !scope.closed ||
												resourceRows != 0 || discussionRows != 0 {
												t.Fatal("actual route retained output/source/work or decoded whole metadata")
											}
											for _, section := range handoff.Sections {
												if section.Name != "" {
													t.Fatal("request handoff retained owned strings after response close")
												}
											}
										}
									}
								})
							}
						}
					}
				}
			}
			if successes == 0 {
				t.Fatal("M route matrix contained no successful owned resource output")
			}
			t.Logf("actual team-resource route pairs=%d SQL legacy/bounded=%d/%d", totalPairs, legacySQL, boundedSQL)
		})
	}
}

func TestScaleP21SW307AuthorizationCancellationAndBoundaries(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		request := httptest.NewRequest(http.MethodGet, "/api/teams/"+f.public+"/resources", nil).WithContext(cancelled)
		request, release := beginRequestResourceReads(request)
		if _, handled := (&server{store: repo}).boundedTeamResourceAuthorization(request, f.data.Workspace.URLKey, f.data.Viewer.ID); handled {
			t.Fatal("team authorization helper changed the literal flag-off path")
		}
		if allowed, handled := (&server{store: f.probe}).boundedTeamResourceAuthorization(request, f.data.Workspace.URLKey, f.data.Viewer.ID); allowed || !handled {
			t.Fatal("cancelled team authorization succeeded or escaped its route boundary")
		}
		release()
		release()
		if f.probe.MetadataWorkUsedForTesting() != 0 || request.Context().Value(teamResourcesReadKey{}) != nil {
			t.Fatal("cancelled team authorization retained a partial output")
		}
		for _, path := range []string{"/api/teams", "/api/teams/x/members", "/api/teams/x/resource-sections", "/api/teams/x/resources/y"} {
			if _, selected := scaleP21SW307TeamResourceAuthorizationRequest(httptest.NewRequest(http.MethodGet, path, nil)); selected {
				t.Fatal("team resource helper captured another authorization predicate", path)
			}
		}
		for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
			if _, selected := scaleP21SW307TeamResourceAuthorizationRequest(httptest.NewRequest(method, "/api/teams/x/resources", nil)); selected {
				t.Fatal("team resource helper captured a writer", method)
			}
		}
	}
}
