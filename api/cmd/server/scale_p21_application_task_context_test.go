package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21ApplicationTaskContextDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-task-context-")
		at := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.task_context_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Workspace.Name = "Task\x00workspace"
			data.WorkspaceSettings.AgentInstructions = "Workspace guidance"
			data.WorkspaceSettings.FeatureSettings.ProjectUpdateGuidance = "Project update guidance"
			data.WorkspaceSettings.FeatureSettings.InitiativeUpdateGuidance = "Initiative update guidance"
			data.Teams = append(data.Teams,
				domain.Team{ID: prefix + "parent", Key: "CTX", Name: "Context parent"},
				domain.Team{ID: prefix + "child", Key: "TASK", Name: "Context child"},
			)
			data.TeamSettings[prefix+"parent"] = domain.TeamSettings{AgentGuidance: "Parent task guidance"}
			data.TeamSettings[prefix+"child"] = domain.TeamSettings{ParentTeamID: prefix + "parent", AgentGuidance: "Child task guidance"}
			for _, principal := range f.principals {
				value := data.UserSettings[principal.user.ID]
				value.AgentInstructions = "Personal guidance " + principal.name
				data.UserSettings[principal.user.ID] = value
			}
			for index := range data.Users {
				if data.Users[index].ID == data.Viewer.ID {
					data.Users[index].Name = "Installed\x00actor"
				}
			}
			data.Projects = append(data.Projects,
				domain.Project{
					ID: prefix + "project", SlugID: prefix + "project-slug", Name: "Task\x00project", Description: "Project detail",
					TeamIDs: []string{f.public}, DependencyIDs: []string{"m2-unrelated", prefix + "project"}, IssueCount: 91,
					Creator:  &domain.User{ID: data.Viewer.ID, Name: "Stale actor"},
					Comments: []domain.Comment{{ID: prefix + "comment", Body: "Comment", User: domain.User{ID: data.Viewer.ID, Name: "Stale author"}, CreatedAt: at}},
				},
				domain.Project{ID: prefix + "unrelated", Name: "Unrelated project", Description: "Unrelated body", TeamIDs: []string{f.public}},
			)
			data.Documents = append(data.Documents,
				domain.Document{ID: prefix + "document", SlugID: prefix + "document-slug", PreviousSlugIDs: []string{prefix + "document-old"}, Title: "Task\x00document", Content: "Document detail", Creator: data.Viewer, TeamIDs: []string{f.public}, ProjectIDs: []string{prefix + "project"}, CreatedAt: at, UpdatedAt: at},
				domain.Document{ID: prefix + "grant", Title: "Explicit document", Creator: data.Viewer, Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: f.principals[2].user.ID, Role: "commenter"}}},
			)
			for index := range data.Issues {
				if data.Issues[index].ID == "issue_1" {
					data.Issues[index].Project = &domain.ProjectSummary{ID: prefix + "project"}
				}
			}
			data.Issues = append(data.Issues,
				domain.Issue{ID: prefix + "issue", Identifier: "CTX-29", Team: domain.Team{ID: f.public}, Project: &domain.ProjectSummary{ID: prefix + "project"}, Creator: data.Viewer, State: data.States[0], CreatedAt: at, UpdatedAt: at},
				domain.Issue{ID: prefix + "archived-issue", Identifier: "CTX-30", Team: domain.Team{ID: f.public}, Project: &domain.ProjectSummary{ID: prefix + "project"}, Creator: data.Viewer, State: data.States[0], ArchivedAt: &at, CreatedAt: at, UpdatedAt: at},
			)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		type principal struct {
			name        string
			user        domain.User
			key         *domain.APIKey
			development bool
		}
		principals := []principal{}
		for _, item := range f.principals {
			principals = append(principals, principal{name: item.name, user: item.user})
		}
		principals = append(principals,
			principal{name: "selected-empty", user: f.data.Viewer, key: &domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{}, Scopes: []string{"read"}}},
			principal{name: "selected", user: f.data.Viewer, key: &domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{f.public}, Scopes: []string{"read"}}},
			principal{name: "app-scope", user: f.data.Viewer, key: &domain.APIKey{Actor: "app", TeamRestriction: "selected", TeamIDs: []string{f.public}, Scopes: []string{"read"}}},
			principal{name: "development", user: f.principals[3].user, development: true},
			principal{name: "development-key", user: f.principals[3].user, development: true, key: &domain.APIKey{Actor: "app", TeamRestriction: "selected", TeamIDs: []string{}}},
		)
		comparisons := 0
		for _, state := range []string{"warm", "reloaded", "unknown-revision"} {
			if state == "reloaded" {
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
			for _, actor := range principals {
				for _, path := range []string{"/api/agent-tasks/task", "/api/realtime/changes", "/api/agent/tools"} {
					for _, task := range []domain.AgentTask{
						{ResourceType: "project", ResourceID: prefix + "project"},
						{ResourceType: "project", ResourceID: prefix + "project-slug"},
						{ResourceType: "document", ResourceID: prefix + "document"},
						{ResourceType: "document", ResourceID: prefix + "document-old"},
						{ResourceType: "document", ResourceID: prefix + "grant"},
						{ResourceType: codingEnvironmentAnchorResource, ResourceID: "environment"},
						{ResourceType: "document", ResourceID: "missing"},
					} {
						task.WorkspaceKey, task.TeamID, task.CreatorID = workspace, prefix+"child", f.data.Viewer.ID
						task.ID, task.Prompt = prefix+"task", "Inspect TASK-12 and CTX-29"
						for _, editing := range []bool{false, true} {
							var outcomes [][]byte
							for _, selected := range []*store.SQLiteStore{repo, f.probe} {
								service := &server{store: selected, authDisabled: actor.development}
								r := f.literalRequest(actor.user, actor.key, nil)
								r.Method, r.URL.Path = http.MethodGet, path
								ctx, closeOutputs := applicationOutputContext(r.Context())
								r = r.WithContext(ctx)
								data, err := service.applicationTaskContext(r, task, editing)
								var output any
								if err != nil {
									output = err.Error()
								} else {
									documents, projects := data.Documents, data.Projects
									if task.ResourceType != "document" {
										documents = nil
									}
									if task.ResourceType != "project" {
										projects = nil
									}
									body, _ := service.agentSessionEventBody(r.Context(), domain.ApplicationInstallation{}, task, data, agentSessionTarget{}, at)
									refs := findAgentMessageReferences(data, task.Prompt)
									output = []any{
										data.Workspace, data.Viewer, data.ViewerRole, documents, projects,
										workspaceAgentSystemPrompt(data, nil, nil, task.TeamID),
										agentGuidanceRules(data, task.TeamID), body, refs.IssueIdentifiers,
										agentWorkspacePolicy(data.WorkspaceSettings, data.ViewerRole), agentWebAccess(data.WorkspaceSettings),
									}
									if selected.BoundedMetadataEnabled() && selected.MetadataWorkUsedForTesting() == 0 && !slices.Contains([]string{"/api/realtime/changes"}, path) {
										t.Fatal("task context output was released before its consumers")
									}
								}
								raw, marshalErr := json.Marshal(output)
								if marshalErr != nil {
									t.Fatal(marshalErr)
								}
								outcomes = append(outcomes, raw)
								closeOutputs()
								if selected.BoundedMetadataEnabled() && (selected.MetadataWorkUsedForTesting() != 0 || selected.WorkspaceMetadataDecodes() != 0) {
									t.Fatal("task context retained work or decoded full metadata")
								}
							}
							if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
								t.Fatalf("state=%s actor=%s path=%s kind=%s ref=%s editing=%v\n%s\n%s", state, actor.name, path, task.ResourceType, task.ResourceID, editing, outcomes[0], outcomes[1])
							}
							comparisons++
						}
					}
				}
			}
		}
		t.Logf("exact task context downstream comparisons=%d", comparisons)
	}
}

func TestScaleP21ApplicationTaskContextOutputOwner(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		r := f.literalRequest(f.data.Viewer, nil, nil)
		r.Method, r.URL.Path = http.MethodGet, "/api/agent-tasks/task"
		_, err := (&server{store: f.probe}).applicationTaskContext(r, domain.AgentTask{ResourceType: codingEnvironmentAnchorResource}, false)
		if err == nil || f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal(fmt.Sprintf("unowned task context escaped: %v", err))
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		ctx, closeOutputs := applicationOutputContext(cancelled)
		defer closeOutputs()
		_, err = (&server{store: f.probe}).applicationTaskContext(r.WithContext(ctx), domain.AgentTask{ResourceType: codingEnvironmentAnchorResource}, false)
		if err == nil || f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cancelled task context retained work")
		}
	}
}
