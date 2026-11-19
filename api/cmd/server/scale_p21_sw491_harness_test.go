package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// The SW491 harness compares the actual authorization middleware (and the
// bare resourceAllowed decision) literal (flag off) against bounded warm and
// bounded SQL-reloaded sources: status, body, headers and the selected mux
// leaf must be byte-identical for every principal and credential.

type scaleP21SW491Principal struct {
	name string
	user domain.User
}

type scaleP21SW491Row struct {
	name, method, pattern, path, body string
}

type scaleP21SW491Control struct {
	key     string
	allowed bool
}

type scaleP21SW491Credential struct {
	name string
	key  *domain.APIKey
}

type scaleP21SW491Fixture struct {
	f                                                 scaleP21SW10Fixture
	repo                                              *store.SQLiteStore
	foreign                                           scaleP03PlatformFixture
	workspace, prefix, public, privateTeam, childTeam string
	publicKey                                         string
	archived                                          time.Time
	ids                                               map[string]string
	principals                                        []scaleP21SW491Principal
	tokens                                            map[string]string
	credentials                                       []scaleP21SW491Credential
}

func scaleP21SW491Setup(t *testing.T, seed func(*scaleP21SW491Fixture, *domain.Bootstrap)) *scaleP21SW491Fixture {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	fx := &scaleP21SW491Fixture{f: f, repo: f.repository, workspace: f.data.Workspace.URLKey, prefix: idgen.New("sw491-"), public: f.public}
	fx.foreign = scaleP03Fixture(t, fx.repo)
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), fx.repo, fx.foreign.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	data, found := fx.repo.WorkspaceMetadata(fx.workspace)
	if !found {
		t.Fatal("M workspace missing")
	}
	for _, project := range data.Projects {
		if project.ID == "m2-unrelated" && len(project.TeamIDs) == 1 {
			fx.privateTeam = project.TeamIDs[0]
		}
	}
	for id, settings := range data.TeamSettings {
		if settings.ParentTeamID == fx.privateTeam && fx.privateTeam != "" {
			fx.childTeam = id
		}
	}
	if fx.privateTeam == "" || fx.childTeam == "" {
		t.Fatal("M private/child team missing")
	}
	prefix := fx.prefix
	fx.publicKey = prefix + "PUB"
	fx.ids = map[string]string{
		"pub-project": prefix + "pub-project", "child-project": prefix + "child-project", "unscoped-project": prefix + "unscoped-project",
		"archived-project": prefix + "archived-project", "pub-initiative": prefix + "pub-initiative", "private-initiative": prefix + "private-initiative",
		"unscoped-initiative": prefix + "unscoped-initiative", "retired-team": prefix + "retired-team", "archived-team": prefix + "archived-team",
	}
	fx.archived = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	archived := fx.archived
	ids := fx.ids
	if err := fx.repo.MutateWorkspace(t.Context(), fx.workspace, "scale_p21.sw491_seed", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			if data.Teams[index].ID == f.public {
				data.Teams[index].Key = fx.publicKey
			}
		}
		data.Teams = append(data.Teams,
			domain.Team{ID: ids["retired-team"], Key: ids["retired-team"], RetiredAt: &archived},
			domain.Team{ID: ids["archived-team"], Key: ids["archived-team"], ArchivedAt: &archived},
		)
		data.Projects = append(data.Projects,
			domain.Project{ID: ids["pub-project"], SlugID: prefix + "pub-current", PreviousSlugIDs: []string{prefix + "pub-old"}, TeamIDs: []string{f.public}},
			domain.Project{ID: ids["child-project"], TeamIDs: []string{fx.childTeam}},
			domain.Project{ID: ids["unscoped-project"], TeamIDs: []string{}},
			domain.Project{ID: ids["archived-project"], TeamIDs: []string{f.public}, ArchivedAt: &archived},
		)
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: ids["pub-initiative"], SlugID: prefix + "init-current", PreviousSlugIDs: []string{prefix + "init-old"}, ProjectIDs: []string{ids["pub-project"]}, ContributingTeamIDs: []string{f.public}},
			domain.Initiative{ID: ids["private-initiative"], LeadTeamID: fx.privateTeam},
			domain.Initiative{ID: ids["unscoped-initiative"], ProjectIDs: []string{}, ContributingTeamIDs: []string{}},
		)
		data.ProjectRelations = append(data.ProjectRelations,
			domain.ProjectRelation{ID: prefix + "prel-private", ProjectID: ids["pub-project"], RelatedProjectID: "m2-unrelated", Type: "blocks"},
			domain.ProjectRelation{ID: prefix + "prel-public", ProjectID: ids["pub-project"], RelatedProjectID: ids["unscoped-project"], Type: "blocks"},
		)
		data.InitiativeRelations = append(data.InitiativeRelations,
			domain.InitiativeRelation{ID: prefix + "irel-private", InitiativeID: ids["pub-initiative"], RelatedInitiativeID: ids["private-initiative"], Type: "blocks"},
			domain.InitiativeRelation{ID: prefix + "irel-public", InitiativeID: ids["pub-initiative"], RelatedInitiativeID: ids["unscoped-initiative"], Type: "blocks"},
		)
		data.Reviews = append(data.Reviews,
			domain.CodeReview{ID: prefix + "review-open", SlugID: prefix + "review-open-slug", Provider: "github", Title: "open"},
			domain.CodeReview{ID: prefix + "review-hidden", SlugID: prefix + "review-hidden-slug", Provider: "github", Title: "hidden", IssueIDs: []string{prefix + "missing-issue"}},
		)
		data.Trash = append(data.Trash, domain.TrashEntry{ID: prefix + "trash", ResourceType: "project", ResourceID: prefix + "trashed-project", Title: "trashed", Payload: json.RawMessage(`{}`), DeletedAt: archived, ExpiresAt: archived.AddDate(1, 0, 0)})
		if seed != nil {
			seed(fx, data)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.reader.key = fx.workspace
	for _, item := range f.principals {
		fx.principals = append(fx.principals, scaleP21SW491Principal{item.name, item.user})
	}
	_, suspended := scaleP03Join(t, f.scaleP03PlatformFixture, "sw491-suspended", idgen.New("sw491-user-")+"@example.test", "member", f.public)
	fx.principals = append(fx.principals, scaleP21SW491Principal{"suspended", suspended}, scaleP21SW491Principal{"outsider", fx.foreign.data.Viewer})
	fx.tokens = map[string]string{}
	for _, item := range fx.principals {
		_, token, err := fx.repo.Login(t.Context(), item.user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal(item.name, err)
		}
		fx.tokens[item.user.ID] = token
	}
	if err := fx.repo.SuspendMember(t.Context(), f.data.Workspace.ID, suspended.ID); err != nil {
		t.Fatal(err)
	}
	fx.credentials = []scaleP21SW491Credential{
		{"session", nil},
		{"unrestricted", &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
		{"selected-empty", &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}},
		{"selected-set", &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
	}
	return fx
}

func (fx *scaleP21SW491Fixture) run(t *testing.T, label string, rows []scaleP21SW491Row, controls []scaleP21SW491Control) {
	t.Helper()
	fx.runPart(t, label, rows, controls, 0, 1)
}

// runPart runs the rows whose index is part modulo parts, so a long table
// runs in several top-level tests that the CI shards spread. A row keeps its
// index in the whole table (and so its FLOW_RECORD_ACCESS mode), and only the
// controls of the selected rows apply.
func (fx *scaleP21SW491Fixture) runPart(t *testing.T, label string, rows []scaleP21SW491Row, controls []scaleP21SW491Control, part, parts int) {
	t.Helper()
	selected, count := map[string]bool{}, 0
	for index, item := range rows {
		if index%parts == part {
			selected[item.name] = true
			count++
		}
	}
	if parts > 1 {
		label = fmt.Sprintf("%s part %d/%d", label, part+1, parts)
	}
	type outcome struct {
		Allowed  bool
		Resource bool
		Status   int
		Body     string
		Header   http.Header
		Pattern  string
	}
	f, repo, workspace := fx.f, fx.repo, fx.workspace
	mux := http.NewServeMux()
	registered := map[string]bool{}
	for _, item := range rows {
		if registered[item.pattern] {
			continue
		}
		registered[item.pattern] = true
		pattern := item.pattern
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			values := map[string]string{}
			for _, name := range []string{"id", "type", "relationId", "commentId", "milestoneId", "workspaceKey", "teamId", "userId", "stateId", "templateId", "sectionId", "resourceId", "noteId", "attachmentId"} {
				if value := r.PathValue(name); value != "" {
					values[name] = value
				}
			}
			w.Header().Set("X-SW491-Leaf", pattern)
			writeJSON(w, http.StatusOK, values)
		})
	}
	pairs, successes, refusals, legacySQL, boundedSQL := 0, 0, 0, 0, 0
	allowedBy := map[string]bool{}
	for index, item := range rows {
		if index%parts != part {
			continue
		}
		t.Run(item.name, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_ACCESS", []string{"off", "shadow", "on"}[index%3])
			for _, phase := range []string{"warm", "reloaded"} {
				t.Run(phase, func(t *testing.T) {
					for _, who := range fx.principals {
						for _, credential := range fx.credentials {
							t.Run(who.name+"/"+credential.name, func(t *testing.T) {
								var want outcome
								beforeSQL := 0
								for _, source := range []*store.SQLiteStore{repo, f.probe} {
									if source.BoundedMetadataEnabled() {
										if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
											t.Fatal(err)
										}
									}
									if phase == "reloaded" {
										scaleP21SW201ReloadSource(t, f.reader, source)
									}
									build := func() (*http.Request, func(), *projectionHold) {
										request := f.literalRequest(who.user, credential.key, nil)
										parsed, err := url.Parse(item.path)
										if err != nil {
											t.Fatal(err)
										}
										request.URL, request.Method = parsed, item.method
										request.Body = http.NoBody
										if item.body != "" {
											request.Body = io.NopCloser(strings.NewReader(item.body))
											request.ContentLength = int64(len(item.body))
										}
										request.SetPathValue("workspaceKey", "")
										request.SetPathValue("workspace", "")
										request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fx.tokens[who.user.ID]})
										hold := &projectionHold{gate: newMetadataReadGate(64, 64, time.Minute, 0)}
										request = request.WithContext(context.WithValue(request.Context(), projectionHoldKey{}, hold))
										request, release := beginRequestResourceReads(request)
										return request, release, hold
									}
									service := &server{store: source}
									statements := 0
									// The bare decision with the wrapper's explicit arguments.
									bare, releaseBare, bareHold := build()
									resource := service.resourceAllowed(bare, workspace, who.user.ID)
									releaseBare()
									if bareHold.held {
										<-bareHold.gate.slots
										if source.BoundedMetadataEnabled() {
											t.Fatal("bounded decision took a whole-workspace projection slot")
										}
									}
									request, release, hold := build()
									if !source.BoundedMetadataEnabled() {
										if _, handled := service.boundedFallbackAuthorization(request, workspace, who.user.ID); handled {
											release()
											t.Fatal("fallback adapter changed the flag-off source")
										}
									}
									request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
									recorder := httptest.NewRecorder()
									allowed := service.authorizeWorkspaceRequest(recorder, request, who.user)
									pattern := ""
									if allowed {
										_, pattern = mux.Handler(request)
										mux.ServeHTTP(recorder, request)
									}
									if hold.held {
										<-hold.gate.slots
										if source.BoundedMetadataEnabled() {
											release()
											t.Fatal("bounded middleware took a whole-workspace projection slot")
										}
									}
									got := outcome{allowed, resource, recorder.Code, recorder.Body.String(), recorder.Header().Clone(), pattern}
									if allowed && (recorder.Code != http.StatusOK && recorder.Code != http.StatusNotFound || recorder.Code == http.StatusOK && pattern != item.pattern) || !allowed && recorder.Code != http.StatusForbidden && recorder.Code != http.StatusNotFound {
										release()
										t.Fatalf("unexpected middleware result %+v", got)
									}
									if !source.BoundedMetadataEnabled() {
										want, beforeSQL = got, statements
									} else {
										pairs++
										legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+statements
										if !reflect.DeepEqual(want, got) {
											release()
											t.Fatalf("literal/bounded authorization differs:\nliteral=%+v\nbounded=%+v", want, got)
										}
										if allowed {
											successes++
											allowedBy[item.name+"/"+who.name+"/"+credential.name] = true
										} else {
											refusals++
										}
										if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
											release()
											t.Fatal("bounded authorization created a complete paged handoff")
										}
									}
									release()
									if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
										t.Fatal("bounded authorization leaked work or decoded complete metadata")
									}
								}
							})
						}
					}
				})
			}
		})
	}
	// Independent policy controls, so equality cannot hold vacuously.
	for _, required := range controls {
		if !selected[strings.SplitN(required.key, "/", 2)[0]] {
			continue
		}
		if allowedBy[required.key] != required.allowed {
			t.Error("independent policy control failed", required.key, required.allowed)
		}
	}
	if want := count * 2 * len(fx.principals) * len(fx.credentials); pairs != want || successes == 0 || refusals == 0 {
		t.Fatal(label+" table lost cells", pairs, want, successes, refusals)
	}
	t.Logf("%s fallback policy pairs=%d SQL literal/bounded=%d/%d success/refusal=%d/%d", label, pairs, legacySQL, boundedSQL, successes, refusals)
}
