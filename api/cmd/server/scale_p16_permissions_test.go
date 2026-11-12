package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP16PermissionDifferential(t *testing.T) {
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			ctx := t.Context()
			if err := repo.MutateWorkspace(ctx, workspace, "scale_p16.seed", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: "p16-open", Key: "P16O"}, domain.Team{ID: "p16-private", Key: "P16P", Private: true}, domain.Team{ID: "p16-child", Key: "P16C"})
				data.TeamSettings["p16-private"] = domain.TeamSettings{TeamID: "p16-private", Access: "private"}
				data.TeamSettings["p16-child"] = domain.TeamSettings{TeamID: "p16-child", ParentTeamID: "p16-private", Access: "public"}
				data.Projects = append(data.Projects,
					domain.Project{ID: "p16-public-project", Name: "Public", PreviousSlugIDs: []string{"p16-old-slug"}, TeamIDs: []string{"p16-open"}, DependencyIDs: []string{"p16-secret-project"}},
					domain.Project{ID: "p16-secret-project", Name: "Secret", TeamIDs: []string{"p16-private"}},
					domain.Project{ID: "p16-child-project", Name: "Inherited", TeamIDs: []string{"p16-child"}})
				data.Initiatives = append(data.Initiatives,
					domain.Initiative{ID: "p16-public-initiative", Name: "Public", PreviousSlugIDs: []string{"p16-old-initiative"}, ProjectIDs: []string{"p16-public-project"}, LeadTeamID: "p16-open"},
					domain.Initiative{ID: "p16-secret-initiative", Name: "Secret", ProjectIDs: []string{"p16-secret-project"}, LeadTeamID: "p16-private"},
					domain.Initiative{ID: "p16-child-initiative", Name: "Inherited", ProjectIDs: []string{"p16-child-project"}, LeadTeamID: "p16-child"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, "p16-private", f.data.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			member, memberUser := scaleP03Join(t, f, "P16 member", idgen.New("p16-member-")+"@example.test", "member", "p16-open")
			admin, _ := scaleP03Join(t, f, "P16 outside admin", idgen.New("p16-admin-")+"@example.test", "admin", "p16-open")
			guest, guestUser := scaleP03Join(t, f, "P16 guest", idgen.New("p16-guest-")+"@example.test", "guest", "p16-open")
			inherited, _ := scaleP03Join(t, f, "P16 inherited", idgen.New("p16-inherited-")+"@example.test", "member", "p16-private")
			issue := authRequest[domain.Issue](t, f.admin, "POST", f.host.URL+"/api/issue-records", map[string]string{"title": "Granted", "teamId": "p16-private", "projectId": "p16-secret-project"}, workspace, 201)
			authRequest[[]domain.IssuePermission](t, f.admin, "PUT", f.host.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": guestUser.ID, "role": "viewer"}}}, workspace, 200)
			unrestricted := scaleP03Key(t, f, []string{"read", "write"}, nil)
			selected := scaleP03Key(t, f, []string{"read", "write"}, []string{"p16-open"})
			empty := idgen.New("p16-empty-")
			if err := repo.MutateWorkspace(ctx, workspace, "scale_p16.seed", "", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: idgen.New("p16-key-"), Name: "Selected empty", SecretHash: secretHash(empty), CreatorID: data.Viewer.ID, Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{}})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			other, err := repo.CreateWorkspace(store.ContextWithActor(ctx, f.data.Viewer), "P16 foreign", idgen.New("p16-other-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
			for _, principal := range []struct {
				name, token string
				client      *http.Client
			}{
				{"admin", "", f.admin}, {"admin-private-outsider", "", admin}, {"member-private-outsider", "", member},
				{"guest-explicit-grant", "", guest}, {"subteam-inheritance", "", inherited},
				{"key-unrestricted", unrestricted, nil}, {"key-selected-set", selected, nil}, {"key-selected-empty", empty, nil},
			} {
				t.Run(principal.name, func(t *testing.T) {
					for _, key := range []string{workspace, other.Workspace.URLKey} {
						for _, path := range []string{"projects/p16-public-project", "projects/p16-old-slug", "projects/p16-secret-project", "projects/p16-child-project", "initiatives/p16-public-initiative", "initiatives/p16-old-initiative", "initiatives/p16-secret-initiative", "initiatives/p16-child-initiative"} {
							var oldStatus int
							var old any
							for _, mode := range []string{"off", "on"} {
								t.Setenv("FLOW_RECORD_HYDRATION", "on")
								t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", mode)
								t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", mode)
								request, _ := http.NewRequest("PATCH", f.host.URL+"/api/"+path, bytes.NewBufferString(`{"summary":""}`))
								request.Header.Set("X-Workspace-Key", key)
								request.Header.Set("X-Request-ID", "scale-p16-permission")
								request.Header.Set("Content-Type", "application/json")
								client := principal.client
								if principal.token != "" {
									request.Header.Set("Authorization", "Bearer "+principal.token)
									client = http.DefaultClient
								}
								response, err := client.Do(request)
								if err != nil {
									t.Fatal(err)
								}
								raw, err := io.ReadAll(response.Body)
								response.Body.Close()
								if err != nil {
									t.Fatal(err)
								}
								var value any
								if err := json.Unmarshal(raw, &value); err != nil {
									t.Fatal(err)
								}
								if principal.name == "admin" && key == workspace && path == "projects/p16-public-project" && response.StatusCode != http.StatusOK {
									t.Fatalf("owner mutation did not reach the callback: status=%d", response.StatusCode)
								}
								if mode == "off" {
									oldStatus, old = response.StatusCode, value
								} else if oldStatus != response.StatusCode || !reflect.DeepEqual(old, value) {
									t.Fatalf("%s workspace=%s: old status=%d new=%d, response differs=%t", path, key, oldStatus, response.StatusCode, !reflect.DeepEqual(old, value))
								}
							}
						}
					}
				})
			}
			if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, "p16-private", memberUser.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, "p16-private", memberUser.ID, "member", false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScaleP16QueuedAndReplayRevocation(t *testing.T) {
	t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", "on")
	t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", "on")
	t.Run("permission-and-replay", TestScaleP15PermissionDifferential)
}
