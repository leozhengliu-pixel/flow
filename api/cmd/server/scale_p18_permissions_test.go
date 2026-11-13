package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP18MCPHTTPVisibility(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		public, private, child := "p18-public", "p18-private", "p18-child"
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p18.seeded", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: public, Key: "P18", Name: "Public"}, domain.Team{ID: private, Key: "P18P", Name: "Private", Private: true}, domain.Team{ID: child, Key: "P18C", Name: "Child"})
			data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
			data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
			actor := data.Viewer
			actor.AppTeamIDs = []string{public, private}
			data.Projects = []domain.Project{
				{ID: "p18-open", SlugID: "renamed-000000000018", PreviousSlugIDs: []string{"old-p18"}, Name: "Open", TeamIDs: []string{public}, DependencyIDs: []string{"p18-secret"}, Initiatives: []string{"p18-hidden"}, Lead: &actor, Comments: []domain.Comment{{ID: "p18-comment", User: actor}}},
				{ID: "p18-secret", Name: "Secret", TeamIDs: []string{private}},
				{ID: "p18-child", Name: "Child", TeamIDs: []string{child}},
				{ID: "p18-mixed", Name: "Mixed", TeamIDs: []string{public, private}, IssueCount: 99},
				{ID: "p18-workspace", Name: "Workspace"},
			}
			data.Projects[0].Initiatives = append(data.Projects[0].Initiatives, "p18-visible")
			data.Initiatives = []domain.Initiative{{ID: "p18-hidden", Name: "Secret initiative", LeadTeamID: private, ProjectIDs: []string{"p18-secret"}}, {ID: "p18-visible", Name: "Visible initiative", LeadTeamID: public, ProjectIDs: []string{"p18-open"}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, private, f.data.Viewer.ID, "member", true); err != nil {
			t.Fatal(err)
		}
		principals := []domain.User{f.data.Viewer}
		for _, spec := range []struct {
			role  string
			teams []string
		}{{"admin", []string{public}}, {"member", []string{public}}, {"guest", []string{public}}, {"member", []string{private}}} {
			_, user := scaleP03Join(t, f, "P18 "+spec.role, idgen.New("p18-")+"@example.test", spec.role, spec.teams...)
			principals = append(principals, user)
		}
		for index, user := range principals {
			for _, restriction := range []string{"unrestricted", "selected-empty", "selected-set"} {
				t.Run(fmt.Sprintf("principal%d/%s", index, restriction), func(t *testing.T) {
					key := domain.APIKey{ID: idgen.New("p18-key-"), CreatorID: user.ID, Scopes: []string{"read", "write"}, CreatedAt: time.Now().UTC()}
					secret := idgen.New("p18-token-")
					key.SecretHash = secretHash(secret)
					if restriction != "unrestricted" {
						key.TeamRestriction, key.TeamIDs = "selected", []string{}
						if restriction == "selected-set" {
							key.TeamIDs = []string{public}
						}
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "api_key.created", key.ID, nil, func(data *domain.Bootstrap) error {
						data.APIKeys = append(data.APIKeys, key)
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					// Settle usage telemetry before comparing revision-bound reads.
					usage, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					for {
						if err := repo.RecordAPIKeyUse(usage, workspace, key.ID, ""); err != nil {
							t.Fatal(err)
						}
						data, ok := repo.WorkspaceMetadataFields(workspace, "apiKeys")
						if ok && slices.ContainsFunc(data.APIKeys, func(item domain.APIKey) bool { return item.ID == key.ID && item.LastUsedAt != nil }) {
							break
						}
						select {
						case <-usage.Done():
							t.Fatal("credential usage did not settle", usage.Err())
						case <-time.After(time.Millisecond):
						}
					}
					actor := mcpActor{WorkspaceKey: workspace, User: user, APIKey: key}
					ctx := context.WithValue(t.Context(), authUserContextKey{}, user)
					var previous []byte
					for _, mode := range []string{"off", "on"} {
						t.Setenv("FLOW_MCP_PROJECT_RECORDS", mode)
						result, err := f.service.callFlowTool(ctx, actor, "list_projects", map[string]any{"limit": float64(250)})
						if err != nil {
							t.Fatal(err)
						}
						raw, _ := json.Marshal(result)
						if previous != nil && string(previous) != string(raw) {
							t.Fatalf("off/on differ: off=%s on=%s", previous, raw)
						}
						previous = raw
						var page struct {
							Items []domain.Project `json:"items"`
						}
						if err := json.Unmarshal(raw, &page); err != nil {
							t.Fatal(err)
						}
						visible := map[string]bool{}
						for _, project := range page.Items {
							visible[project.ID] = true
							httpResult := scaleP03Read(t, f, nil, secret, http.MethodGet, "/api/projects/"+project.ID, workspace)
							if httpResult.Code != http.StatusOK {
								t.Fatalf("%s MCP visible but HTTP=%d", project.ID, httpResult.Code)
							}
							var want domain.Project
							if err := json.Unmarshal(httpResult.Body.Bytes(), &want); err != nil {
								t.Fatal(err)
							}
							if !reflect.DeepEqual(project, want) {
								t.Fatalf("%s MCP/HTTP fields differ: MCP=%+v HTTP=%+v", project.ID, project, want)
							}
						}
						for _, id := range []string{"p18-open", "p18-secret", "p18-child", "p18-mixed", "p18-workspace"} {
							httpResult := scaleP03Read(t, f, nil, secret, http.MethodGet, "/api/projects/"+id, workspace)
							if (httpResult.Code == http.StatusOK) != visible[id] {
								t.Fatalf("%s MCP=%v HTTP=%d", id, visible[id], httpResult.Code)
							}
						}
						if restriction != "unrestricted" && strings.Contains(string(raw), `"appTeamIds":["p18-public","p18-private"]`) {
							t.Fatal("actor leaked hidden team")
						}
						for _, filter := range []map[string]any{{"initiative": "p18-visible"}, {"team": private}} {
							filtered, err := f.service.callFlowTool(ctx, actor, "list_projects", filter)
							if err != nil {
								t.Fatal(err)
							}
							items := filtered.(map[string]any)["items"].([]domain.Project)
							if restriction != "unrestricted" && filter["team"] == private && len(items) != 0 {
								t.Fatal("filter selected a hidden team")
							}
							if index != 3 && restriction == "selected-set" && filter["initiative"] == "p18-visible" && len(items) != 1 {
								t.Fatalf("visible initiative filter returned %d projects", len(items))
							}
						}
					}
				})
			}
		}
	}
}
