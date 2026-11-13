package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP15ReusedBootstrapPermissionDifferential(t *testing.T) {
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			member, _ := scaleP03Join(t, f, "Snapshot member", idgen.New("p15-reuse-")+"@example.test", "member")
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p15.reuse_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: "p15-reuse-public", Key: "P15R"}, domain.Team{ID: "p15-reuse-private", Key: "P15S", Private: true})
				data.TeamSettings["p15-reuse-public"] = domain.TeamSettings{TeamID: "p15-reuse-public", Access: "public"}
				data.TeamSettings["p15-reuse-private"] = domain.TeamSettings{TeamID: "p15-reuse-private", Access: "private"}
				for i := range 2048 {
					team, description := "p15-reuse-public", "public"
					if i%2 == 0 {
						team, description = "p15-reuse-private", "p15-reused-secret"
					}
					data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p15-reuse-%d", i), Name: "Snapshot project", TeamIDs: []string{team}, Description: description})
				}
				settings, preferences := data.UserSettings[f.data.Viewer.ID], data.NotificationPreferences[f.data.Viewer.ID]
				for _, user := range f.resources.users {
					settings.UserID, preferences.UserID = user.ID, user.ID
					data.UserSettings[user.ID], data.NotificationPreferences[user.ID] = settings, preferences
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, "p15-reuse-private", f.data.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				for _, client := range []*http.Client{f.admin, member} {
					for _, path := range []string{"/api/bootstrap", "/api/issue-records/bootstrap"} {
						t.Setenv("FLOW_RECORD_HYDRATION", "off")
						old := scaleP03Read(t, f, client, "", http.MethodGet, path, workspace)
						t.Setenv("FLOW_RECORD_HYDRATION", "on")
						got := scaleP03Read(t, f, client, "", http.MethodGet, path, workspace)
						var left, right any
						if err := json.Unmarshal(old.Body.Bytes(), &left); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(got.Body.Bytes(), &right); err != nil {
							t.Fatal(err)
						}
						if old.Code != http.StatusOK || got.Code != old.Code || !reflect.DeepEqual(left, right) {
							t.Fatalf("reused permission projection differs at %s: old=%d new=%d", path, old.Code, got.Code)
						}
						var data domain.Bootstrap
						if err := json.Unmarshal(got.Body.Bytes(), &data); err != nil {
							t.Fatal(err)
						}
						if client == member {
							for _, project := range data.Projects {
								if project.Description == "p15-reused-secret" {
									t.Fatal("reused buffers exposed the preceding viewer's private project")
								}
							}
						}
					}
				}
			}
		})
	}
}
