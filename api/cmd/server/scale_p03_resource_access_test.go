package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP03Fixture(t *testing.T, repo *store.SQLiteStore) scaleP03PlatformFixture {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	svc := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(svc))
	t.Cleanup(host.Close)
	return scaleP03CreateFixture(t, repo, svc, host)
}

func scaleP03Join(t *testing.T, f scaleP03PlatformFixture, name, email, role string, teams ...string) (*http.Client, domain.User) {
	t.Helper()
	client, user := scaleP03RegisteredClient(t, f.resources, f.host.URL, name, email)
	invites := authRequest[[]domain.Invitation](t, f.admin, http.MethodPost, f.host.URL+"/api/workspaces/"+f.data.Workspace.URLKey+"/invitations", map[string]any{"emails": []string{user.Email}, "role": role, "teamIds": teams}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, f.host.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	return client, user
}

func scaleP03Key(t *testing.T, f scaleP03PlatformFixture, scopes, teamIDs []string) string {
	t.Helper()
	input := map[string]any{"name": fmt.Sprintf("P03 key %d", time.Now().UnixNano()), "scopes": scopes}
	if teamIDs != nil {
		input["teamIds"] = teamIDs
	}
	return authRequest[struct{ Secret string }](t, f.admin, http.MethodPost, f.host.URL+"/api/api-keys", input, f.data.Workspace.URLKey, http.StatusCreated).Secret
}

func scaleP03Read(t *testing.T, f scaleP03PlatformFixture, client *http.Client, token, method, path, workspace string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("X-Workspace-Key", workspace)
	r.Header.Set("X-Request-ID", "scale-p03-differential")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if client != nil {
		origin, _ := url.Parse(f.host.URL)
		for _, cookie := range client.Jar.Cookies(origin) {
			r.AddCookie(cookie)
		}
	}
	w := httptest.NewRecorder()
	f.host.Config.Handler.ServeHTTP(w, r)
	return w
}

func TestScaleP03PermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	for name, repo := range scaleP03ServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			ctx := context.Background()
			suffix := fmt.Sprint(time.Now().UnixNano())
			public := "p03-public-" + suffix
			private := "p03-private-" + suffix
			child := "p03-child-" + suffix
			retired := "p03-retired-" + suffix
			archived := "p03-archived-" + suffix
			now := time.Now().UTC()
			err := repo.MutateWorkspace(ctx, workspace, "scale_p03.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: public, Key: "P3O"}, domain.Team{ID: private, Key: "P3P", Private: true}, domain.Team{ID: child, Key: "P3C"}, domain.Team{ID: retired, Key: "P3R", RetiredAt: &now}, domain.Team{ID: archived, Key: "P3A", ArchivedAt: &now})
				data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
				data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
				data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
				data.Projects = append(data.Projects,
					domain.Project{ID: "p03-open", Name: "Open", SlugID: "renamed-000000000003", PreviousSlugIDs: []string{"old-name"}, TeamIDs: []string{public}, DependencyIDs: []string{"p03-secret", "P03-SECRET", "p03-mixed", "missing"}, Initiatives: []string{"p03-hidden"}, IssueCount: 99, Lead: &data.Viewer, Comments: []domain.Comment{{ID: "comment", Body: "Full body", User: data.Viewer, Reactions: map[string][]string{"like": {data.Viewer.ID}}}}, DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: "revision", Description: "Earlier body", Author: data.Viewer}}},
					domain.Project{ID: "p03-secret", Name: "Secret", SlugID: "secret-000000000004", TeamIDs: []string{private}},
					domain.Project{ID: "p03-mixed", TeamIDs: []string{public, private}},
					domain.Project{ID: "p03-unscoped", TeamIDs: []string{}, DependencyIDs: []string{}},
					domain.Project{ID: "p03-child", TeamIDs: []string{child}},
					domain.Project{ID: "p03-retired", TeamIDs: []string{retired}},
					domain.Project{ID: "p03-archived-team", TeamIDs: []string{archived}},
					domain.Project{ID: "p03-archived", TeamIDs: []string{public}, ArchivedAt: &now},
					domain.Project{ID: "p03-alias", SlugID: "project-alias"},
					domain.Project{ID: "p03-alias-selected", PreviousSlugIDs: []string{"project-alias"}, TeamIDs: []string{public}},
				)
				data.Initiatives = append(data.Initiatives,
					domain.Initiative{ID: "p03-linked", Name: "Linked", SlugID: "linked-000000000005", PreviousSlugIDs: []string{"old-initiative"}, ProjectIDs: []string{"p03-open", "p03-secret", "missing"}, ParentInitiativeIDs: []string{"p03-hidden", "p03-parent", "missing"}, ContributingTeamIDs: []string{public, private}, Creator: data.Viewer, Owner: &data.Viewer, Comments: []domain.Comment{{ID: "ic", Body: "Initiative discussion", User: data.Viewer}}, DescriptionHistory: []domain.InitiativeDescriptionRevision{{ID: "ih", Description: "Prior initiative", Editor: data.Viewer}}},
					domain.Initiative{ID: "p03-hidden", ProjectIDs: []string{"p03-secret"}},
					domain.Initiative{ID: "p03-parent", ProjectIDs: []string{"p03-open"}},
					domain.Initiative{ID: "p03-shell", ProjectIDs: []string{}, ParentInitiativeIDs: []string{}},
					domain.Initiative{ID: "p03-lead", LeadTeamID: private, ProjectIDs: []string{"p03-open"}},
					domain.Initiative{ID: "p03-contributing", ContributingTeamIDs: []string{public}},
					domain.Initiative{ID: "p03-unscoped-project", ProjectIDs: []string{"p03-unscoped"}},
					domain.Initiative{ID: "p03-alias", SlugID: "initiative-alias"},
					domain.Initiative{ID: "p03-alias-selected", PreviousSlugIDs: []string{"initiative-alias"}, ProjectIDs: []string{"p03-open"}},
				)
				data.InitiativeRelations = append(data.InitiativeRelations, domain.InitiativeRelation{ID: "legacy-parent", Type: "parent", InitiativeID: "p03-linked", RelatedInitiativeID: "p03-shell"})
				data.Favorites = append(data.Favorites, domain.Favorite{ID: "p03-favorite", UserID: data.Viewer.ID, ResourceType: "initiative", ResourceID: "p03-linked"})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, private, f.data.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			member, memberUser := scaleP03Join(t, f, "P03 member", "p03-member-"+suffix+"@example.com", "member", public)
			outsideAdmin, outsideAdminUser := scaleP03Join(t, f, "P03 outside admin", "p03-admin-"+suffix+"@example.com", "admin", public)
			guest, _ := scaleP03Join(t, f, "P03 guest", "p03-guest-"+suffix+"@example.com", "guest", public, private)
			inherited, _ := scaleP03Join(t, f, "P03 inherited", "p03-inherited-"+suffix+"@example.com", "member", private)
			sharedGuest, sharedUser := scaleP03Join(t, f, "P03 shared guest", "p03-shared-"+suffix+"@example.com", "guest", public)
			issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "P03 shared private issue", "teamId": private, "projectId": "p03-secret"}, workspace, http.StatusCreated)
			authRequest[[]domain.IssuePermission](t, f.admin, http.MethodPut, f.host.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": sharedUser.ID, "role": "viewer"}}}, workspace, http.StatusOK)
			otherWorkspace := "p03-other-" + suffix
			if _, err := repo.CreateWorkspace(store.ContextWithActor(ctx, f.data.Viewer), "P03 other", otherWorkspace, "us"); err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, otherWorkspace)
			if err := repo.ReplaceTeamDefaultFavorites(ctx, workspace, public, []domain.TeamDefaultFavorite{{ResourceType: "initiative", ResourceID: "p03-parent"}}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamDefaultFavoriteOverride(ctx, workspace, memberUser.ID, "initiative", "p03-parent", true); err != nil {
				t.Fatal(err)
			}
			unrestricted := scaleP03Key(t, f, []string{"read"}, nil)
			legacyScopes := scaleP03Key(t, f, nil, nil)
			emptyScopes := scaleP03Key(t, f, []string{}, nil)
			selected := scaleP03Key(t, f, []string{"read"}, []string{public})
			empty := "p03-selected-empty-" + suffix
			if err := repo.MutateWorkspace(ctx, workspace, "scale_p03.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "p03-selected-empty", Name: "P03 persisted selected empty", SecretHash: secretHash(empty), CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			app := authRequest[domain.OAuthApplication](t, f.admin, http.MethodPost, f.host.URL+"/api/oauth-applications", map[string]any{"name": "P03 application", "scopes": []string{"read", "initiative:read"}, "supportsClientCredentials": true}, workspace, http.StatusCreated)
			appRead := applicationFormToken(t, f.host, app, "read", http.StatusOK).Token
			appToken := applicationFormToken(t, f.host, app, "read initiative:read", http.StatusOK)
			appInitiatives := appToken.Token
			f.resources.appUsers = append(f.resources.appUsers, struct{ workspace, id string }{workspace, appToken.Subject})
			principals := []struct {
				name   string
				client *http.Client
				token  string
			}{
				{"admin-member", f.admin, ""}, {"admin-private-outsider", outsideAdmin, ""}, {"member-private-outsider", member, ""},
				{"guest-explicit-teams", guest, ""}, {"subteam-inheritance", inherited, ""},
				{"guest-explicit-issue-grant", sharedGuest, ""},
				{"key-unrestricted", nil, unrestricted}, {"key-selected-set", nil, selected}, {"key-selected-empty", nil, empty},
				{"key-legacy-nil-scopes", nil, legacyScopes}, {"key-empty-scopes", nil, emptyScopes},
				{"app-read", nil, appRead}, {"app-initiative-read", nil, appInitiatives},
			}
			paths := []string{}
			for _, id := range []string{"p03-open", "renamed-000000000003", "stale-000000000003", "000000000003", "old-name", "p03-secret", "p03-mixed", "p03-unscoped", "p03-child", "p03-retired", "p03-archived-team", "p03-archived", "project-alias", "missing"} {
				paths = append(paths, "/api/projects/"+id)
			}
			for _, id := range []string{"p03-linked", "linked-000000000005", "stale-000000000005", "old-initiative", "p03-hidden", "p03-parent", "p03-shell", "p03-lead", "p03-contributing", "p03-unscoped-project", "initiative-alias", "missing"} {
				paths = append(paths, "/api/initiatives/"+id)
			}
			paths = append(paths, "/api/projects/p03-open%2Fmissing")
			// Exercise the certified installed-copy path as well as the
			// canonical fallback used after credential telemetry writes.
			if err := repo.ReloadWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			for _, principal := range principals {
				t.Run(principal.name, func(t *testing.T) {
					for _, method := range []string{http.MethodGet, http.MethodHead} {
						for _, path := range paths {
							t.Setenv("FLOW_RECORD_ACCESS", "off")
							old := scaleP03Read(t, f, principal.client, principal.token, method, path, workspace)
							for _, mode := range []string{"on", "shadow", "off"} {
								t.Setenv("FLOW_RECORD_ACCESS", mode)
								got := scaleP03Read(t, f, principal.client, principal.token, method, path, workspace)
								var oldJSON, gotJSON any
								_ = json.Unmarshal(old.Body.Bytes(), &oldJSON)
								_ = json.Unmarshal(got.Body.Bytes(), &gotJSON)
								if got.Code != old.Code || !reflect.DeepEqual(gotJSON, oldJSON) {
									t.Fatalf("%s %s mode=%s: got %d %s; legacy %d %s", method, path, mode, got.Code, got.Body, old.Code, old.Body)
								}
							}
						}
					}
				})
			}
			for _, mode := range []string{"off", "shadow", "on"} {
				t.Setenv("FLOW_RECORD_ACCESS", mode)
				for _, path := range []string{"/api/projects/p03-secret", "/api/projects/p03-child", "/api/initiatives/p03-hidden"} {
					if got := scaleP03Read(t, f, outsideAdmin, "", http.MethodGet, path, workspace); got.Code != http.StatusForbidden {
						t.Fatalf("admin outside private team read %s: %d", path, got.Code)
					}
				}
				got := scaleP03Read(t, f, member, "", http.MethodGet, "/api/projects/p03-open", workspace)
				project := decodeInto[domain.Project](t, got.Body.Bytes())
				if got.Code != http.StatusOK || project.IssueCount != 0 || slices.Contains(project.DependencyIDs, "p03-secret") || len(project.Comments) != 1 || len(project.DescriptionRevisions) != 1 {
					t.Fatal("project count, references or full detail changed")
				}
				if got := scaleP03Read(t, f, member, "", http.MethodGet, "/api/projects/p03-open", "does-not-exist"); got.Code != http.StatusNotFound {
					t.Fatalf("cross-workspace request: %d", got.Code)
				}
				if got := scaleP03Read(t, f, member, "", http.MethodGet, "/api/projects/p03-open", otherWorkspace); got.Code != http.StatusForbidden {
					t.Fatalf("existing foreign workspace: %d", got.Code)
				}
			}
			if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, private, outsideAdminUser.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			t.Run("admin-after-private-team-grant", func(t *testing.T) {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					for _, path := range paths {
						t.Setenv("FLOW_RECORD_ACCESS", "off")
						old := scaleP03Read(t, f, outsideAdmin, "", method, path, workspace)
						for _, mode := range []string{"on", "shadow", "off"} {
							t.Setenv("FLOW_RECORD_ACCESS", mode)
							got := scaleP03Read(t, f, outsideAdmin, "", method, path, workspace)
							if got.Code != old.Code || got.Body.String() != old.Body.String() {
								t.Fatalf("joined admin %s %s mode=%s differs from legacy", method, path, mode)
							}
						}
					}
				}
			})
		})
	}
}

func TestScaleP03FlagAndNarrowRoutes(t *testing.T) {
	for value, want := range map[string]string{"": "on", "unknown": "on", "shadow": "shadow", "on": "on", " ON ": "on", "off": "off", "0": "off", "false": "off"} {
		t.Setenv("FLOW_RECORD_ACCESS", value)
		if got := recordAccessMode(); got != want {
			t.Fatalf("flag %q: %q, want %q", value, got, want)
		}
	}
	for _, check := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/api/projects/id", true}, {http.MethodHead, "/api/initiatives/slug", true},
		{http.MethodPatch, "/api/projects/id", false}, {http.MethodPost, "/api/initiatives/id", false},
		{http.MethodGet, "/api/projects", false}, {http.MethodGet, "/api/projects/id/comments", false},
		{http.MethodGet, "/api/project-milestones/id", false}, {http.MethodGet, "/api/documents/id", false},
	} {
		if _, _, got := recordAccessRoute(httptest.NewRequest(check.method, check.path, nil)); got != check.want {
			t.Fatalf("route %s %s = %v", check.method, check.path, got)
		}
	}
}

func TestScaleP03RequestAuthorizationMatchesLegacyDuringRevocation(t *testing.T) {
	for name, repo := range scaleP03ServerTestStores(t) {
		for _, mode := range []string{"off", "on"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				t.Setenv("FLOW_RECORD_ACCESS", mode)
				f := scaleP03Fixture(t, repo)
				workspace := f.data.Workspace.URLKey
				ctx := context.Background()
				err := repo.MutateWorkspace(ctx, workspace, "scale_p03.seeded", "", nil, func(data *domain.Bootstrap) error {
					data.Teams = append(data.Teams, domain.Team{ID: "p03-revoke", Private: true})
					data.TeamSettings["p03-revoke"] = domain.TeamSettings{TeamID: "p03-revoke", Access: "private"}
					data.Projects = append(data.Projects, domain.Project{ID: "p03-revoke", TeamIDs: []string{"p03-revoke"}})
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				member, user := scaleP03Join(t, f, "P03 revoke", fmt.Sprintf("p03-revoke-%d@example.com", time.Now().UnixNano()), "member", "p03-revoke")
				r := httptest.NewRequest(http.MethodGet, "/api/projects/p03-revoke", nil)
				r.SetPathValue("id", "p03-revoke")
				r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, user))
				r.Header.Set("X-Workspace-Key", workspace)
				if !f.service.resourceAllowed(r, workspace, user.ID) {
					t.Fatal("initial membership denied")
				}
				if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, "p03-revoke", user.ID, "member", false); err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				f.service.getProjectRecord(w, r)
				// Both paths finish the read already authorized in this request.
				// The next request must observe the revoked membership.
				if w.Code != http.StatusOK {
					t.Fatalf("authorized request was re-resolved: %d %s", w.Code, w.Body)
				}
				if next := scaleP03Read(t, f, member, "", http.MethodGet, "/api/projects/p03-revoke", workspace); next.Code != http.StatusForbidden {
					t.Fatalf("next request survived revocation: %d", next.Code)
				}
			})
		}
	}
}

func TestScaleP03PeerCompatibility(t *testing.T) {
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	peers := newScaleP03PeerServers(t)
	f := scaleP03CreateFixture(t, peers.stores[0], peers.servers[0], peers.first)
	admin := f.admin
	suffix := fmt.Sprint(time.Now().UnixNano())
	workspace := f.data.Workspace.URLKey
	team := authRequest[domain.Team](t, admin, http.MethodPost, peers.first.URL+"/api/workspaces/"+workspace+"/teams", map[string]any{"name": "P03 peer private", "key": "P03", "private": true}, "", http.StatusCreated)
	project := authRequest[domain.Project](t, admin, http.MethodPost, peers.first.URL+"/api/projects", map[string]any{"name": "P03 peer project", "teamIds": []string{team.ID}}, workspace, http.StatusCreated)
	member, user := scaleP03Join(t, f, "P03 peer member", "p03-peer-member-"+suffix+"@example.com", "member", team.ID)
	for _, name := range []string{"P03 peer first edit", "P03 peer second edit"} {
		t.Setenv("FLOW_RECORD_ACCESS", "off")
		authRequest[domain.Project](t, admin, http.MethodPatch, peers.first.URL+"/api/projects/"+project.ID, map[string]any{"name": name}, workspace, http.StatusOK)
		old := authRequest[domain.Project](t, member, http.MethodGet, peers.first.URL+"/api/projects/"+project.ID, nil, workspace, http.StatusOK)
		t.Setenv("FLOW_RECORD_ACCESS", "on")
		got := authRequest[domain.Project](t, member, http.MethodGet, peers.second.URL+"/api/projects/"+project.ID, nil, workspace, http.StatusOK)
		if !reflect.DeepEqual(got, old) {
			t.Fatal("peer detail differs from the legacy writer's detail")
		}
	}
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	authRequest[any](t, admin, http.MethodPut, peers.first.URL+"/api/workspaces/"+workspace+"/teams/"+team.ID+"/members/"+user.ID, map[string]any{"member": false, "role": "member"}, "", http.StatusNoContent)
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	authRequest[any](t, member, http.MethodGet, peers.second.URL+"/api/projects/"+project.ID, nil, workspace, http.StatusForbidden)
}
