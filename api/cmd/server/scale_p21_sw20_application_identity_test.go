package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// These tests certify the handlers' direct identity/role inputs. Application
// storage services and non-coding task resource contexts retain their own
// inventory entries; this fixture does not certify those downstream paths.
func TestScaleP21SW20ApplicationIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		archived := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw20.app_teams", "", nil, func(data *domain.Bootstrap) error {
			for index := range 75 {
				data.Teams = append(data.Teams, domain.Team{ID: fmt.Sprintf("sw20-team-%d", index), Key: fmt.Sprintf("sw20-%d", index), Private: index%2 == 0, ArchivedAt: &archived})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		services := []*server{{store: repo}, {store: probe}}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range []struct {
				name string
				body any
				id   string
				run  func(*server, http.ResponseWriter, *http.Request)
			}{
				{"list-empty", nil, "", (*server).listApplicationInstallations},
				{"configure-builtin-disabled", map[string]any{"builtin": true, "allPublicTeams": true}, "", (*server).configureApplicationInstallation},
				{"configure-missing", map[string]any{}, "sw20-missing-installation", (*server).configureApplicationInstallation},
				{"configure-invalid-scopes", map[string]any{"scopes": []string{"sw20-invalid"}}, "", (*server).configureApplicationInstallation},
				{"configure-unregistered", map[string]any{"clientId": "sw20-unregistered", "scopes": []string{}}, "", (*server).configureApplicationInstallation},
			} {
				t.Run(operation.name, func(t *testing.T) {
					var outcomes []any
					for _, service := range services {
						r, w := f.request(user, key, operation.body), httptest.NewRecorder()
						r.SetPathValue("id", operation.id)
						scaleP21M3Call(service, w, r, operation.run)
						outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("direct handler inputs changed: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
			t.Run("identity-inputs", func(t *testing.T) {
				legacy := services[0].workspaceData(f.request(user, key, nil))
				w := httptest.NewRecorder()
				bounded, ok := services[1].workspaceDataScoped(w, f.request(user, key, nil), store.MetadataRecordScope{})
				if !ok {
					t.Fatalf("bounded identity: %d %s", w.Code, w.Body.String())
				}
				before := []any{legacy.Workspace.URLKey, legacy.Viewer.ID, legacy.ViewerRole}
				after := []any{bounded.Workspace.URLKey, bounded.Viewer.ID, bounded.ViewerRole}
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("configured installation inputs changed: %v / %v", before, after)
				}
				if len(bounded.Projects) != 0 || len(bounded.Settings) != 0 {
					t.Fatal("identity input loaded unrelated metadata")
				}
			})
			t.Run("consent-directory", func(t *testing.T) {
				var outcomes []any
				role, _, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, user.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, service := range services {
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					query := r.URL.Query()
					query.Set("workspace", workspace)
					r.URL.RawQuery = query.Encode()
					// The OAuth helper owns authentication/session enforcement.
					// Supply its exact identity/role result at this boundary.
					ctx := context.WithValue(r.Context(), oauthWorkspaceAccessKey{}, oauthWorkspaceAccess{workspace: workspace, userID: user.ID, data: domain.Bootstrap{Workspace: f.data.Workspace, Viewer: user, ViewerRole: role}})
					service.applicationConsentTeams(w, r.WithContext(ctx))
					outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
					if workspaceAdminRole(role) {
						var teams []domain.Team
						if err := json.Unmarshal(w.Body.Bytes(), &teams); err != nil {
							t.Fatal(err)
						}
						if len(teams) < 75 {
							t.Fatal("admin team management directory was truncated")
						}
					}
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("consent teams changed: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		})
		for _, service := range services {
			service.authDisabled = true
			legacy := (&server{store: repo, authDisabled: true}).workspaceData(f.request(domain.User{}, nil, nil))
			w := httptest.NewRecorder()
			data, ok := service.workspaceDataScoped(w, f.request(domain.User{}, nil, nil), store.MetadataRecordScope{})
			if !ok || data.Workspace.URLKey != legacy.Workspace.URLKey || data.Viewer.ID != legacy.Viewer.ID || data.ViewerRole != legacy.ViewerRole {
				t.Fatal("development identity changed", w.Code, w.Body.String())
			}
			service.authDisabled = false
		}
		user := f.principals[2].user
		queued := f.request(user, nil, nil)
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, user.ID); err != nil {
			t.Fatal(err)
		}
		for _, service := range services {
			w := httptest.NewRecorder()
			scaleP21M3Call(service, w, queued.Clone(queued.Context()), (*server).listApplicationInstallations)
			if w.Code != http.StatusForbidden {
				t.Fatalf("revoked queued caller: %d %s", w.Code, w.Body.String())
			}
			r := f.request(f.data.Viewer, nil, nil)
			r.Header.Set("X-Workspace-Key", idgen.New("sw20-foreign-"))
			w = httptest.NewRecorder()
			scaleP21M3Call(service, w, r, (*server).listApplicationInstallations)
			if w.Code != http.StatusForbidden {
				t.Fatal("cross-workspace boundary changed")
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		w := httptest.NewRecorder()
		services[1].listApplicationInstallations(w, f.request(f.data.Viewer, nil, nil).WithContext(ctx))
		if w.Body.Len() != 0 {
			t.Fatal("cancelled bounded identity request published a response", w.Body.String())
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("application identity/teams decoded full metadata")
		}
	}
}

func TestScaleP21SW20ApplicationTaskIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		tasks := []domain.AgentTask{}
		for _, anchor := range []string{codingAnchorAgentChat, codingAnchorPullRequest, codingAnchorLoop} {
			task := domain.AgentTask{ID: idgen.New("mention_"), WorkspaceKey: workspace, AppUserID: f.data.Viewer.ID, CreatorID: f.principals[3].user.ID, ResourceType: anchor, CodingSessionID: "sw20-session"}
			if err := repo.CreateMentionTask(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			tasks = append(tasks, task)
		}
		services := []*server{{store: repo}, {store: f.probe}}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, binding := range []string{workspace, ""} {
				for _, task := range append(tasks, domain.AgentTask{ID: "sw20-missing-task"}) {
					for _, editing := range []bool{false, true} {
						var outcomes []any
						for _, service := range services {
							r := f.request(user, key, nil)
							r.Method = http.MethodGet
							r.Header.Set("X-Workspace-Key", binding)
							result, data, err := service.authorizedAgentTask(r, task.ID, editing)
							if task.ResourceType != "" && (user.ID == f.principals[0].user.ID || user.ID == f.principals[1].user.ID) && err != nil {
								t.Fatalf("admin coding-anchor success path failed: %v", err)
							}
							message := ""
							if err != nil {
								message = err.Error()
							}
							outcomes = append(outcomes, []any{result, data.Workspace.URLKey, data.Viewer.ID, data.ViewerRole, message})
						}
						if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
							t.Fatalf("task identity/anchor %s binding=%q edit=%v: %s", task.ResourceType, binding, editing, scaleP21M2Diff(outcomes[0], outcomes[1]))
						}
					}
				}
			}
		})
		for _, binding := range []string{workspace, ""} {
			for _, archived := range []bool{false, true} {
				var outcomes []any
				for _, service := range services {
					user := f.data.Viewer
					user.App = true
					r, w := f.request(user, nil, nil), httptest.NewRecorder()
					r.Method = http.MethodGet
					r.Header.Set("X-Workspace-Key", binding)
					if archived {
						r.URL.RawQuery = "includeArchived=true"
					}
					service.listApplicationAgentTasks(w, r)
					var listed []domain.AgentTask
					if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed) != len(tasks) {
						t.Fatalf("application task-list success: %d %s", w.Code, w.Body.String())
					}
					outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("app task list identity binding=%q archived=%v: %s", binding, archived, scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("coding task identity decoded full metadata")
		}
	}
}
