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

func TestScaleP17PermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p17.seed", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: "p17-public", Key: "P17O"}, domain.Team{ID: "p17-private", Key: "P17P", Private: true}, domain.Team{ID: "p17-child", Key: "P17C"})
				data.TeamSettings["p17-public"] = domain.TeamSettings{TeamID: "p17-public", Access: "public"}
				data.TeamSettings["p17-private"] = domain.TeamSettings{TeamID: "p17-private", Access: "private"}
				data.TeamSettings["p17-child"] = domain.TeamSettings{TeamID: "p17-child", ParentTeamID: "p17-private", Access: "public"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, "p17-private", f.data.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			member, _ := scaleP03Join(t, f, "P17 member", idgen.New("p17-member-")+"@example.test", "member", "p17-public")
			admin, _ := scaleP03Join(t, f, "P17 outside admin", idgen.New("p17-admin-")+"@example.test", "admin", "p17-public")
			guest, guestUser := scaleP03Join(t, f, "P17 guest", idgen.New("p17-guest-")+"@example.test", "guest", "p17-public")
			inherited, _ := scaleP03Join(t, f, "P17 inherited", idgen.New("p17-inherited-")+"@example.test", "member", "p17-private")
			unrestricted := scaleP03Key(t, f, []string{"read", "write"}, nil)
			selected := scaleP03Key(t, f, []string{"read", "write"}, []string{"p17-public"})
			empty := idgen.New("p17-empty-")
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p17.seed", "", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "p17-empty", SecretHash: secretHash(empty), CreatorID: data.Viewer.ID, Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{}})
				data.Projects = append(data.Projects, domain.Project{ID: "p17-child-project", Name: "P17 inherited project", TeamIDs: []string{"p17-child"}})
				for i, teams := range [][]string{{"p17-public"}, {"p17-private"}, {}, {"p17-private"}} {
					id := []string{"p17-open", "p17-secret", "p17-inherited", "p17-closed"}[i]
					inheritedTeams := []string{}
					if i == 2 {
						inheritedTeams = []string{"p17-child"}
					}
					permissions := []domain.DocumentPermission{{ID: id + "-owner", SubjectType: "user", SubjectID: data.Viewer.ID, Role: "owner"}, {ID: id + "-workspace", SubjectType: "workspace", Role: "editor"}, {ID: id + "-guest", SubjectType: "user", SubjectID: guestUser.ID, Role: "commenter"}}
					if i == 3 {
						permissions = []domain.DocumentPermission{{ID: id + "-owner", SubjectType: "user", SubjectID: data.Viewer.ID, Role: "owner"}, {ID: id + "-team", SubjectType: "team", SubjectID: "p17-private", Role: "editor"}}
					}
					projects := []string{}
					if i == 2 {
						projects = []string{"p17-child-project"}
					}
					data.Documents = append(data.Documents, domain.Document{ID: id, SlugID: id + "-current", PreviousSlugIDs: []string{id + "-old"}, Title: id, Content: "current body", ContentState: "state", Creator: data.Viewer, TeamIDs: teams, ProjectIDs: projects, InheritedTeamIDs: inheritedTeams, Version: 3, CreatedAt: at, UpdatedAt: at, Permissions: permissions, Revisions: []domain.DocumentRevision{{ID: id + "-revision", DocumentID: id, Content: "history body", Author: data.Viewer, CreatedAt: at}}})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "P17 foreign", idgen.New("p17-other-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
			principals := []struct {
				name, token string
				client      *http.Client
			}{
				{"admin", "", f.admin}, {"admin-private-outsider", "", admin}, {"member-private-outsider", "", member}, {"guest-explicit", "", guest}, {"subteam-inheritance", "", inherited},
				{"key-unrestricted", unrestricted, nil}, {"key-selected-set", selected, nil}, {"key-selected-empty", empty, nil},
			}
			for _, principal := range principals {
				t.Run(principal.name, func(t *testing.T) {
					if principal.token != "" {
						t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
						_ = scaleP03Read(t, f, principal.client, principal.token, "GET", "/api/documents", workspace)
						deadline := time.Now().Add(2 * time.Second)
						for {
							metadata, _ := repo.WorkspaceMetadataFields(workspace, "apiKeys")
							settled := false
							for _, key := range metadata.APIKeys {
								settled = settled || key.SecretHash == secretHash(principal.token) && key.LastUsedAt != nil
							}
							if settled {
								break
							}
							if time.Now().After(deadline) {
								t.Fatal("credential telemetry did not settle")
							}
							time.Sleep(time.Millisecond)
						}
					}
					for _, path := range []string{"/api/documents", "/api/documents?first=2", "/api/documents?teamId=p17-public", "/api/documents?q=current", "/api/documents?q=history", "/api/documents/p17-open", "/api/documents/p17-open-old", "/api/documents/p17-secret", "/api/documents/p17-inherited", "/api/documents/p17-closed", "/api/documents/p17-open/permissions", "/api/documents/p17-open/comments", "/api/documents/p17-open/subscribers"} {
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
						old := scaleP03Read(t, f, principal.client, principal.token, "GET", path, workspace)
						t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
						got := scaleP03Read(t, f, principal.client, principal.token, "GET", path, workspace)
						scaleP17EqualResponse(t, path, old.Code, old.Body.Bytes(), got.Code, got.Body.Bytes())
					}
					for _, id := range []string{"p17-open", "p17-secret", "p17-inherited", "p17-closed"} {
						var previous *httptest.ResponseRecorder
						for _, mode := range []string{"off", "on"} {
							t.Setenv("FLOW_DOCUMENT_RECORDS", mode)
							r := httptest.NewRequest("PATCH", "/api/documents/"+id, strings.NewReader(`{"title":"`+id+`"}`))
							r.Header.Set("X-Workspace-Key", workspace)
							r.Header.Set("Content-Type", "application/json")
							r.Header.Set("X-Request-ID", "scale-p17-differential")
							if principal.token != "" {
								r.Header.Set("Authorization", "Bearer "+principal.token)
							} else {
								origin, _ := url.Parse(f.host.URL)
								for _, cookie := range principal.client.Jar.Cookies(origin) {
									r.AddCookie(cookie)
								}
							}
							w := httptest.NewRecorder()
							f.host.Config.Handler.ServeHTTP(w, r)
							if previous != nil {
								scaleP17EqualResponse(t, "PATCH "+id, previous.Code, previous.Body.Bytes(), w.Code, w.Body.Bytes())
							}
							previous = w
						}
					}
					for _, mode := range []string{"off", "on"} {
						t.Setenv("FLOW_DOCUMENT_RECORDS", mode)
						got := scaleP03Read(t, f, principal.client, principal.token, "GET", "/api/documents/p17-open", other.Workspace.URLKey)
						if got.Code == http.StatusOK {
							t.Fatal("cross-workspace document exposed")
						}
					}
				})
			}
		})
	}
}

func scaleP17EqualResponse(t *testing.T, label string, oldStatus int, old []byte, newStatus int, current []byte) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(old, &a); err != nil {
		t.Fatalf("%s legacy JSON: %v", label, err)
	}
	if err := json.Unmarshal(current, &b); err != nil {
		t.Fatalf("%s candidate JSON: %v", label, err)
	}
	if oldStatus != newStatus || !reflect.DeepEqual(a, b) {
		t.Fatalf("%s differential: old status=%d new status=%d; body equal=%v; old=%.500s new=%.500s", label, oldStatus, newStatus, reflect.DeepEqual(a, b), old, current)
	}
}
