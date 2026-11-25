package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21PublicResourceRecordWriter struct {
	*httptest.ResponseRecorder
	repo      *store.SQLiteStore
	held      int
	sql       int
	team      int
	canonical int
}

func (w *scaleP21PublicResourceRecordWriter) Write(raw []byte) (int, error) {
	w.held = w.repo.MetadataWorkUsedForTesting()
	return w.ResponseRecorder.Write(raw)
}

func TestScaleP21PublicResourceRecordHandlerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-public-point-")
		archived := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.public_point_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = append(data.Projects, domain.Project{
				ID: prefix + "project", SlugID: "point-project-000000000031", PreviousSlugIDs: []string{prefix + "old-project", prefix + "gate-project-alias"},
				Name: "Point\x00project", Description: "Full detail", TeamIDs: []string{f.public}, DependencyIDs: []string{"m2-unrelated", "missing"},
				IssueCount: 83, ArchivedAt: &archived, Creator: &domain.User{ID: data.Viewer.ID, Name: "Stored actor"},
				Comments: []domain.Comment{{ID: prefix + "comment", Body: "Full comment", User: data.Viewer}},
			})
			data.Projects = append(data.Projects, domain.Project{ID: prefix + "outside-project", SlugID: prefix + "gate-project-alias", Name: "Exact outside selected teams", TeamIDs: []string{f.data.Teams[0].ID}})
			data.Initiatives = append(data.Initiatives,
				domain.Initiative{
					ID: prefix + "initiative", SlugID: "point-initiative-000000000032", PreviousSlugIDs: []string{prefix + "old-initiative", prefix + "gate-initiative-alias"},
					Name: "Point\x00initiative", Description: "Full detail", ProjectIDs: []string{prefix + "project", "m2-unrelated"},
					Favorite: true, Creator: domain.User{ID: data.Viewer.ID, Name: "Stored actor"},
					Comments: []domain.Comment{{ID: prefix + "initiative-comment", Body: "Full comment", User: data.Viewer}},
				},
				domain.Initiative{ID: prefix + "secret", Name: "Private initiative", ProjectIDs: []string{"m2-unrelated"}},
				domain.Initiative{ID: prefix + "outside-initiative", SlugID: prefix + "gate-initiative-alias", Name: "Exact outside selected teams", ProjectIDs: []string{prefix + "outside-project"}},
			)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		type principalCase struct {
			name, workspace string
			user            domain.User
			key             *domain.APIKey
			development     bool
			cancelled       bool
			gateRefused     bool
			handoff         bool
			authorize       bool
		}
		cases := []principalCase{}
		for _, principal := range f.principals {
			cases = append(cases, principalCase{name: principal.name, workspace: workspace, user: principal.user})
		}
		for _, credential := range []struct {
			name string
			key  *domain.APIKey
		}{
			{"unrestricted", &domain.APIKey{ID: "unrestricted", Scopes: []string{"read"}}},
			{"selected-empty", &domain.APIKey{ID: "empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
			{"selected-set", &domain.APIKey{ID: "selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
			{"application-read", &domain.APIKey{ID: "app-read", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
			{"application-initiative", &domain.APIKey{ID: "app-initiative", Actor: "app", Scopes: []string{"read", "initiative:read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
		} {
			cases = append(cases, principalCase{name: credential.name, workspace: workspace, user: f.data.Viewer, key: credential.key})
		}
		cases = append(cases,
			principalCase{name: "missing-binding", user: f.data.Viewer},
			principalCase{name: "missing-workspace", workspace: "missing-" + prefix, user: f.data.Viewer},
			principalCase{name: "gate-refused", workspace: workspace, user: f.data.Viewer, gateRefused: true},
			principalCase{name: "existing-projection-handoff", workspace: workspace, user: f.data.Viewer, handoff: true},
			principalCase{name: "development", workspace: workspace, user: f.data.Viewer, development: true},
			principalCase{name: "development-default", user: f.principals[3].user, development: true},
			principalCase{name: "development-credential-bypass", workspace: workspace, user: f.principals[3].user, key: &domain.APIKey{Actor: "app", Scopes: []string{}, TeamRestriction: "selected", TeamIDs: []string{}}, development: true},
			principalCase{name: "development-missing", workspace: "missing-" + prefix, user: f.data.Viewer, development: true},
			principalCase{name: "development-cancelled", workspace: workspace, user: f.data.Viewer, development: true, cancelled: true},
			principalCase{name: "selected-hidden-exact-visible-alias-gate", workspace: workspace, user: f.data.Viewer, key: &domain.APIKey{ID: "selected-gate", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}, authorize: true},
		)
		comparisons := 0
		for _, state := range []string{"warm", "reloaded-known-current", "unknown-revision"} {
			if state == "reloaded-known-current" {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if state == "unknown-revision" {
				if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"", "on", "off", "shadow"} {
				t.Setenv("FLOW_RECORD_ACCESS", mode)
				for _, principal := range cases {
					for _, resource := range []struct {
						kind string
						refs []string
					}{
						{"projects", []string{prefix + "project", "point-project-000000000031", prefix + "old-project", "m2-unrelated", "missing"}},
						{"initiatives", []string{prefix + "initiative", "point-initiative-000000000032", prefix + "old-initiative", prefix + "secret", "missing"}},
					} {
						refs := resource.refs
						if principal.authorize {
							ref := prefix + "gate-project-alias"
							if resource.kind == "initiatives" {
								ref = prefix + "gate-initiative-alias"
							}
							refs = []string{ref}
						}
						for _, ref := range refs {
							serve := func(selected *store.SQLiteStore) *scaleP21PublicResourceRecordWriter {
								service := &server{store: selected, authDisabled: principal.development}
								r := f.literalRequest(principal.user, principal.key, nil)
								r.Method, r.URL.Path = http.MethodGet, "/api/"+resource.kind+"/"+ref
								r.SetPathValue("id", ref)
								r.Header.Set("X-Workspace-Key", principal.workspace)
								r.SetPathValue("workspaceKey", principal.workspace)
								r.SetPathValue("workspace", principal.workspace)
								if principal.cancelled {
									ctx, cancel := context.WithCancel(r.Context())
									cancel()
									r = r.WithContext(ctx)
								}
								if principal.gateRefused {
									r = r.WithContext(context.WithValue(r.Context(), projectionHoldKey{}, &projectionHold{gate: &metadataReadGate{}, retryAfter: time.Second}))
								}
								queries, teamQueries, canonicalQueries := 0, 0, 0
								r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(query string) {
									queries++
									if strings.Contains(query, "field='teams'") {
										teamQueries++
									}
									if strings.Contains(query, "field='projectStatuses'") || strings.Contains(query, "field='users'") {
										canonicalQueries++
									}
								}))
								if selected.BoundedMetadataEnabled() {
									var close func()
									r, close = beginRequestResourceReads(r)
									defer close()
								}
								if principal.handoff {
									var data domain.Bootstrap
									if selected.BoundedMetadataEnabled() {
										access, err := selected.ResolveAccess(r.Context(), workspace, principal.user.ID, principal.key)
										if err != nil {
											t.Fatal(err)
										}
										read, err := selected.BeginProjectedInstalledResourcePointRead(r.Context(), access, resource.kind, ref)
										if err != nil || !registerRequestReadClose(r.Context(), read.Close) {
											t.Fatal("could not own the existing handoff", err)
										}
										data = read.Data
									} else {
										var err error
										data, err = selected.PagedWorkspaceMetadata(r.Context(), workspace, principal.user.ID)
										if err != nil {
											t.Fatal(err)
										}
									}
									for index := range data.Projects {
										data.Projects[index].Name = "Earlier handed-off project"
									}
									for index := range data.Initiatives {
										data.Initiatives[index].Name = "Earlier handed-off initiative"
									}
									handOffPagedMetadata(r, workspace, principal.user.ID, data)
								}
								w := &scaleP21PublicResourceRecordWriter{ResponseRecorder: httptest.NewRecorder(), repo: selected}
								if principal.authorize && !service.authorizeWorkspaceRequest(w, r, principal.user) {
									w.sql, w.team, w.canonical = queries, teamQueries, canonicalQueries
									return w
								}
								if resource.kind == "projects" {
									service.getProjectRecord(w, r)
								} else {
									service.getInitiativeRecord(w, r)
								}
								w.sql = queries
								w.team = teamQueries
								w.canonical = canonicalQueries
								return w
							}
							want, got := serve(repo), serve(f.probe)
							if got.Code != want.Code || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") || got.Header().Get("Retry-After") != want.Header().Get("Retry-After") || got.Body.String() != want.Body.String() {
								t.Fatalf("state=%s mode=%q principal=%s resource=%s/%s\nwant=%d %s\n got=%d %s", state, mode, principal.name, resource.kind, ref, want.Code, want.Body.String(), got.Code, got.Body.String())
							}
							if got.Code == http.StatusOK && got.held == 0 {
								t.Fatal("public resource output was released before encoding")
							}
							if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
								t.Fatal("public resource handler retained admission or decoded full metadata")
							}
							if principal.authorize {
								t.Logf("state=%s mode=%q kind=%s selected hidden exact/visible alias authorization legacy=%d bounded=%d", state, mode, resource.kind, want.Code, got.Code)
							}
							if principal.name == "admin-private-outsider" && (ref == prefix+"project" || ref == prefix+"initiative") {
								t.Logf("state=%s mode=%q kind=%s SQL_off=%d SQL_bounded=%d team_policy_off=%d team_policy_bounded=%d canonical_actor_status_off=%d canonical_actor_status_bounded=%d", state, mode, resource.kind, want.sql, got.sql, want.team, got.team, want.canonical, got.canonical)
							}
							comparisons++
						}
					}
				}
			}
		}
		t.Logf("public project/initiative exact route comparisons=%d", comparisons)
	}
}

func TestScaleP21PublicResourceRecordNULIdentityDifferential(t *testing.T) {
	if driver := os.Getenv("FLOW_TEST_DATABASE_DRIVER"); driver == "postgres" || driver == "postgresql" {
		t.Skip("PostgreSQL text parameters cannot retain the NUL record key")
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-point-nul-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.point_nul_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: prefix + "\x00team"})
			data.Projects = append(data.Projects, domain.Project{ID: prefix + "project", Name: "NUL policy identity", TeamIDs: []string{prefix + "\x00team"}})
			data.Initiatives = append(data.Initiatives, domain.Initiative{ID: prefix + "initiative", Name: "NUL policy identity", ProjectIDs: []string{prefix + "project"}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"projects", "initiatives"} {
			ref := prefix + "project"
			if kind == "initiatives" {
				ref = prefix + "initiative"
			}
			serve := func(selected *store.SQLiteStore) *httptest.ResponseRecorder {
				service := &server{store: selected}
				r := f.literalRequest(f.data.Viewer, nil, nil)
				r.Method, r.URL.Path = http.MethodGet, "/api/"+kind+"/"+ref
				r.SetPathValue("id", ref)
				w := httptest.NewRecorder()
				if kind == "projects" {
					service.getProjectRecord(w, r)
				} else {
					service.getInitiativeRecord(w, r)
				}
				return w
			}
			want, got := serve(repo), serve(f.probe)
			if want.Code != http.StatusOK || got.Code != want.Code || got.Body.String() != want.Body.String() || f.probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatalf("NUL identity output differs: kind=%s legacy=%d bounded=%d\n%s\n%s", kind, want.Code, got.Code, want.Body.String(), got.Body.String())
			}
			t.Logf("native NUL identity: kind=%s legacy=%d bounded=%d", kind, want.Code, got.Code)
		}
	}
}
