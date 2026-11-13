package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func apiKeyScopeFixture(t *testing.T, repo *store.SQLiteStore) scaleP03PlatformFixture {
	t.Helper()
	f := scaleP03Fixture(t, repo)
	err := repo.MutateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), f.data.Workspace.URLKey, "test.scope_fixture", "", nil, func(data *domain.Bootstrap) error {
		public := domain.Team{ID: "scope-public", Key: "SCP", Name: "Public"}
		private := domain.Team{ID: "scope-private", Key: "SCH", Name: "Private", Private: true}
		data.Teams = append(data.Teams, public, private)
		data.TeamSettings[public.ID] = domain.TeamSettings{TeamID: public.ID, Access: "public"}
		data.TeamSettings[private.ID] = domain.TeamSettings{TeamID: private.ID, Access: "private"}
		data.Labels = append(data.Labels, domain.IssueLabel{ID: "scope-label", Name: "Shared", Scope: "workspace"}, domain.IssueLabel{ID: "scope-hidden-label", Name: "Private label", Scope: private.ID})
		data.Projects = append(data.Projects,
			domain.Project{ID: "scope-project", Name: "Shared project", SlugID: "scope-current-0123456789ab", PreviousSlugIDs: []string{"scope-old-project"}, TeamIDs: []string{public.ID, private.ID}, DependencyIDs: []string{"scope-allowed-project", "scope-hidden-project"}, Initiatives: []string{"scope-initiative", "scope-hidden-initiative"}, LabelIDs: []string{"scope-label", "scope-hidden-label"}, Resources: []domain.ProjectResource{{ID: "scope-resource", ProjectID: "scope-project", PinnedTeamIDs: []string{public.ID, private.ID}}}},
			domain.Project{ID: "scope-allowed-project", Name: "Allowed project", TeamIDs: []string{public.ID}},
			domain.Project{ID: "scope-hidden-project", Name: "Hidden project", TeamIDs: []string{private.ID}},
		)
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: "scope-initiative", Name: "Shared initiative", ProjectIDs: []string{"scope-project", "scope-hidden-project"}, ContributingTeamIDs: []string{public.ID, private.ID}, ParentInitiativeIDs: []string{"scope-allowed-initiative", "scope-hidden-initiative"}, LabelIDs: []string{"scope-label", "scope-hidden-label"}},
			domain.Initiative{ID: "scope-allowed-initiative", Name: "Allowed initiative", ProjectIDs: []string{"scope-allowed-project"}},
			domain.Initiative{ID: "scope-hidden-initiative", Name: "Hidden initiative", ProjectIDs: []string{"scope-hidden-project"}},
		)
		data.ProjectRelations = append(data.ProjectRelations, domain.ProjectRelation{ID: "scope-project-relation", ProjectID: "scope-project", RelatedProjectID: "scope-hidden-project"})
		data.InitiativeRelations = append(data.InitiativeRelations, domain.InitiativeRelation{ID: "scope-initiative-relation", InitiativeID: "scope-initiative", RelatedInitiativeID: "scope-hidden-initiative", Type: "parent"})
		if data.ProjectUpdates == nil {
			data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
		}
		if data.InitiativeUpdates == nil {
			data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
		}
		data.ProjectUpdates["scope-project"] = []domain.ProjectUpdate{{ID: "scope-project-update", ProjectID: "scope-project", User: data.Viewer, Diff: &domain.PulseDiff{ProgressSince: &domain.PulseProgressChange{From: 0, To: 1}}}}
		data.InitiativeUpdates["scope-initiative"] = []domain.InitiativeUpdate{{ID: "scope-initiative-update", InitiativeID: "scope-initiative", User: data.Viewer, Diff: &domain.PulseDiff{Projects: &domain.PulseProjectsChange{Added: []domain.PulseRef{{ID: "scope-project"}, {ID: "scope-hidden-project"}}}, Initiatives: &domain.PulseProjectsChange{Added: []domain.PulseRef{{ID: "scope-allowed-initiative"}, {ID: "scope-hidden-initiative"}}}}}}
		data.Cycles = append(data.Cycles, domain.Cycle{ID: "scope-cycle", TeamID: public.ID, Name: "Allowed cycle"}, domain.Cycle{ID: "scope-hidden-cycle", TeamID: private.ID, Name: "Hidden cycle"})
		state := domain.WorkflowState{ID: "scope-state", Name: "Todo", Type: "unstarted"}
		data.States = append(data.States, state)
		now := time.Now().UTC()
		data.Issues = append(data.Issues,
			domain.Issue{ID: "scope-issue", Identifier: "SCP-1", Number: 1, Title: "Visible issue", Team: public, State: state, Creator: data.Viewer, ParentID: ptr("scope-hidden-issue"), SubIssueIDs: []string{"scope-child", "scope-hidden-issue"}, Relations: []domain.IssueRelation{{IssueID: "scope-issue", RelatedIssueID: "scope-hidden-issue", Type: "related"}}, Project: &domain.ProjectSummary{ID: "scope-hidden-project"}, ProjectMilestoneID: ptr("scope-hidden-milestone"), CycleID: ptr("scope-hidden-cycle"), Labels: slices.Clone(data.Labels), SuggestedProjectIDs: []string{"scope-hidden-project"}, SuggestedTeamIDs: []string{private.ID}, SuggestedDuplicateIDs: []string{"scope-hidden-issue"}, SuggestedRelatedIDs: []string{"scope-hidden-issue"}, CreatedAt: now, UpdatedAt: now},
			domain.Issue{ID: "scope-child", Identifier: "SCP-2", Number: 2, Title: "Visible child", Team: public, State: state, Creator: data.Viewer, Project: &domain.ProjectSummary{ID: "scope-project"}, Labels: []domain.IssueLabel{data.Labels[0]}, CreatedAt: now, UpdatedAt: now},
			domain.Issue{ID: "scope-hidden-issue", Identifier: "SCH-1", Number: 1, Title: "Hidden issue", Team: private, State: state, Creator: data.Viewer, Project: &domain.ProjectSummary{ID: "scope-project"}, Labels: []domain.IssueLabel{data.Labels[0]}, CreatedAt: now, UpdatedAt: now},
		)
		data.Documents = append(data.Documents, domain.Document{ID: "scope-document", Title: "Shared document", Creator: data.Viewer, TeamIDs: []string{public.ID, private.ID}, ProjectIDs: []string{"scope-project", "scope-hidden-project"}, IssueID: "scope-hidden-issue", IssueTeamID: private.ID, InheritedTeamIDs: []string{public.ID, private.ID}})
		data.Cycles[len(data.Cycles)-2].Status = "completed"
		for i := range data.Issues {
			if data.Issues[i].ID == "scope-issue" {
				data.Issues[i].Relations = append(data.Issues[i].Relations, domain.IssueRelation{IssueID: "scope-hidden-issue", RelatedIssueID: "scope-issue", Type: "related"})
			}
			if data.Issues[i].ID == "scope-child" || data.Issues[i].ID == "scope-hidden-issue" {
				data.Issues[i].State = domain.WorkflowState{ID: "scope-done", Type: "completed"}
				data.Issues[i].CycleID = ptr("scope-cycle")
			}
		}
		data.Customers = append(data.Customers, domain.Customer{ID: "scope-demand-customer", Name: "Private demand"})
		data.CustomerRequests = append(data.CustomerRequests, domain.CustomerRequest{ID: "scope-hidden-request", CustomerID: "scope-demand-customer", IssueID: "scope-hidden-issue"})
		data.Notifications = append(data.Notifications,
			domain.Notification{ID: "scope-notification", RecipientID: data.Viewer.ID, IssueID: "scope-issue", SourceType: "issue", SourceID: "scope-issue", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now},
			domain.Notification{ID: "scope-hidden-notification", RecipientID: data.Viewer.ID, IssueID: "scope-hidden-issue", SourceType: "issue", SourceID: "scope-hidden-issue", CreatedAt: now.Add(time.Second), UpdatedAt: now},
			domain.Notification{ID: "scope-notification-child", RecipientID: data.Viewer.ID, IssueID: "scope-child", SourceType: "issue", SourceID: "scope-child", CreatedAt: now, UpdatedAt: now},
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, team := range []string{"scope-public", "scope-private"} {
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, team, f.data.Viewer.ID, "member", true); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func apiKeyScopeCredential(t *testing.T, f scaleP03PlatformFixture, actor, restriction string, teams, scopes []string) (string, domain.APIKey) {
	t.Helper()
	secret := idgen.New("scope-test-token-")
	key := domain.APIKey{ID: idgen.New("scope-key-"), Name: "Scope test", CreatorID: f.data.Viewer.ID, Actor: actor, SecretHash: secretHash(secret), TeamRestriction: restriction, TeamIDs: teams, Scopes: scopes}
	if actor == "app" {
		installation := domain.ApplicationInstallation{ClientID: idgen.New("scope-app-"), Name: "Scope app", Active: true, InstalledBy: f.data.Viewer.ID, TeamIDs: []string{"scope-public", "scope-private"}, Scopes: scopes}
		var app domain.ApplicationInstallation
		err := scaleP03RetryWrite(t.Context(), func() error {
			var err error
			app, err = f.repository.InstallApplication(t.Context(), f.data.Workspace.URLKey, installation, "")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		f.resources.appUsers = append(f.resources.appUsers, struct{ workspace, id string }{f.data.Workspace.URLKey, app.UserID})
		key.SubjectUserID, key.OAuthClientID = app.UserID, app.ClientID
	}
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.scope_key", key.ID, nil, func(data *domain.Bootstrap) error {
		data.APIKeys = append(data.APIKeys, key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	auth, err := f.repository.AuthenticateAPIKeyRecord(t.Context(), f.data.Workspace.URLKey, secretHash(secret), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Settle the first usage write before comparing credential-bound reads.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		if err := f.repository.RecordAPIKeyUse(ctx, f.data.Workspace.URLKey, key.ID, ""); err != nil {
			t.Fatal(err)
		}
		data, ok := f.repository.BootstrapForContext(ctx, f.data.Workspace.URLKey)
		if ok && slices.ContainsFunc(data.APIKeys, func(item domain.APIKey) bool { return item.ID == key.ID && item.LastUsedAt != nil }) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("credential usage did not settle", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	return secret, auth.Key
}

func assertAPIKeyScopeProject(t *testing.T, item domain.Project, restricted, initiatives bool, count int) {
	t.Helper()
	if item.IssueCount != count {
		t.Errorf("project issueCount=%d, want %d", item.IssueCount, count)
	}
	if !slices.Contains(item.DependencyIDs, "scope-allowed-project") || slices.Contains(item.DependencyIDs, "scope-hidden-project") == restricted {
		t.Errorf("dependencyIds=%v", item.DependencyIDs)
	}
	if slices.Contains(item.TeamIDs, "scope-private") == restricted {
		t.Errorf("teamIds=%v", item.TeamIDs)
	}
	if slices.Contains(item.Initiatives, "scope-initiative") != initiatives || slices.Contains(item.Initiatives, "scope-hidden-initiative") != (!restricted && initiatives) {
		t.Errorf("initiatives=%v", item.Initiatives)
	}
	if restricted && slices.Contains(item.LabelIDs, "scope-hidden-label") {
		t.Errorf("labelIds=%v", item.LabelIDs)
	}
	if restricted && (len(item.IssueCountHistory)+len(item.ScopeHistory)+len(item.CompletedScopeHistory)+len(item.InProgressScopeHistory)+len(item.ProgressHistory) != 0) {
		t.Error("project history retains counts outside the credential scope")
	}
	for _, resource := range item.Resources {
		if slices.Contains(resource.PinnedTeamIDs, "scope-private") == restricted {
			t.Errorf("pinnedTeamIds=%v", resource.PinnedTeamIDs)
		}
	}
}

func TestScaleP03APIKeyTeamScopeReferencesAndCounts(t *testing.T) {
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := apiKeyScopeFixture(t, repo)
			read := func(token, path string, target any) {
				t.Helper()
				response := scaleP03Read(t, f, nil, token, http.MethodGet, path, f.data.Workspace.URLKey)
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), target) != nil {
					t.Fatalf("GET %s: status=%d", path, response.Code)
				}
			}
			ownerBefore := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/projects/scope-project", f.data.Workspace.URLKey)
			ownerIssueBefore := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/issue-records/scope-issue", f.data.Workspace.URLKey)
			for _, spec := range []struct {
				name, actor, restriction       string
				teams                          []string
				scopes                         []string
				restricted, empty, initiatives bool
			}{
				{"selected-set", "user", "selected", []string{"scope-public"}, []string{"read"}, true, false, true},
				{"selected-empty", "user", "selected", []string{}, []string{"read"}, true, true, true},
				{"unrestricted", "user", "all", nil, []string{"read"}, false, false, true},
				{"legacy-selected", "user", "", []string{"scope-public"}, []string{"read"}, true, false, true},
				{"app-selected", "app", "selected", []string{"scope-public"}, []string{"read", "initiative:read"}, true, false, true},
				{"app-selected-empty", "app", "selected", []string{}, []string{"read", "initiative:read"}, true, true, true},
				{"app-read", "app", "selected", []string{"scope-public"}, []string{"read"}, true, false, false},
				{"app-unrestricted-read", "app", "all", nil, []string{"read"}, false, false, false},
			} {
				t.Run(spec.name, func(t *testing.T) {
					secret, key := apiKeyScopeCredential(t, f, spec.actor, spec.restriction, spec.teams, spec.scopes)
					for _, mode := range []string{"off", "shadow", "on"} {
						t.Run(mode, func(t *testing.T) {
							t.Setenv("FLOW_RECORD_ACCESS", mode)
							var bootstrap domain.Bootstrap
							read(secret, "/api/bootstrap", &bootstrap)
							if spec.restricted {
								for _, item := range bootstrap.Issues {
									raw, _ := json.Marshal(item)
									if strings.Contains(string(raw), "scope-hidden-") {
										t.Error("streamed issue restores hidden references")
									}
								}
								for _, item := range bootstrap.Notifications {
									if item.ID == "scope-hidden-notification" {
										t.Error("streamed notification is outside the key's teams")
									}
								}
							}
							if spec.restricted {
								for _, user := range append(slices.Clone(bootstrap.Users), bootstrap.Viewer) {
									if slices.Contains(user.AppTeamIDs, "scope-private") {
										t.Error("application user exposes teams outside the key")
									}
								}
							}
							if spec.empty {
								if len(bootstrap.Projects)+len(bootstrap.Issues)+len(bootstrap.Cycles)+len(bootstrap.Documents)+len(bootstrap.Initiatives) != 0 {
									t.Fatal("selected-empty retains team resources")
								}
							} else {
								project := bootstrap.Projects[slices.IndexFunc(bootstrap.Projects, func(p domain.Project) bool { return p.ID == "scope-project" })]
								count := 2
								if spec.restricted {
									count = 1
								}
								assertAPIKeyScopeProject(t, project, spec.restricted, spec.initiatives, count)
							}
							for _, path := range []string{"/api/projects/scope-project", "/api/projects/scope-old-project", "/api/projects/stale-0123456789ab"} {
								response := scaleP03Read(t, f, nil, secret, http.MethodGet, path, f.data.Workspace.URLKey)
								if spec.empty {
									if response.Code != http.StatusForbidden && response.Code != http.StatusNotFound {
										t.Fatal("selected-empty reads a project")
									}
									continue
								}
								var project domain.Project
								if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &project) != nil {
									t.Fatalf("detail status=%d", response.Code)
								}
								assertAPIKeyScopeProject(t, project, spec.restricted, spec.initiatives, 0)
							}
							for _, path := range []string{"/api/projects?first=100", "/api/initiatives?first=100", "/api/documents?first=100", "/api/cycles?first=100", "/api/issues?first=100", "/api/issue-records/bootstrap"} {
								if path == "/api/initiatives?first=100" && !spec.initiatives {
									continue
								}
								var result any
								read(secret, path, &result)
								raw, _ := json.Marshal(result)
								if spec.restricted && strings.Contains(string(raw), "scope-hidden-") {
									t.Errorf("%s leaks a hidden reference", path)
								}
							}
							var directory projectCustomerPage
							read(secret, "/api/projects?includeTotal=true&limit=100", &directory)
							wantTotal := int64(3)
							if spec.restricted {
								wantTotal = 2
							}
							if spec.empty {
								wantTotal = 0
							}
							if directory.Total != wantTotal {
								t.Errorf("directory total=%d, want %d", directory.Total, wantTotal)
							}
							if spec.restricted {
								if len(directory.CustomerSummaries) != 0 {
									t.Error("directory counts private issue demand")
								}
								raw, _ := json.Marshal(directory)
								if strings.Contains(string(raw), "scope-hidden-") {
									t.Error("directory rows or customer demand leak")
								}
							}
							if !spec.empty {
								for _, path := range []string{"/api/projects/scope-project/updates", "/api/initiatives/scope-initiative/updates"} {
									if strings.Contains(path, "/initiatives/") && !spec.initiatives {
										continue
									}
									var updates any
									read(secret, path, &updates)
									raw, _ := json.Marshal(updates)
									if spec.restricted && strings.Contains(string(raw), "scope-hidden-") {
										t.Error("update diff leaks hidden references")
									}
									if spec.restricted && strings.Contains(path, "/projects/") && strings.Contains(string(raw), "progressSince") {
										t.Error("update diff leaks aggregate progress")
									}
								}
								var cycle domain.Cycle
								read(secret, "/api/cycles/scope-cycle", &cycle)
								wantCapacity := 2.0
								if spec.restricted {
									wantCapacity = 1
								}
								if cycle.CapacityAutomatic != wantCapacity {
									t.Errorf("cycle capacity=%g, want %g", cycle.CapacityAutomatic, wantCapacity)
								}
								var document domain.Document
								read(secret, "/api/documents/scope-document", &document)
								if spec.restricted && (document.IssueID != "" || document.IssueTeamID != "" || slices.Contains(document.InheritedTeamIDs, "scope-private") || slices.Contains(document.TeamIDs, "scope-private") || slices.Contains(document.ProjectIDs, "scope-hidden-project")) {
									t.Error("document links leak")
								}
								var issue domain.Issue
								read(secret, "/api/issue-records/scope-issue", &issue)
								if spec.restricted && (issue.ParentID != nil || issue.Project != nil || issue.ProjectMilestoneID != nil || issue.CycleID != nil || slices.Contains(issue.SubIssueIDs, "scope-hidden-issue") || len(issue.Relations) != 0) {
									t.Error("issue links leak")
								}
							}
						})
					}
					user, err := repo.UserByID(t.Context(), key.SubjectID())
					if err != nil {
						t.Fatal(err)
					}
					actor := mcpActor{User: user, APIKey: key, WorkspaceKey: f.data.Workspace.URLKey}
					for _, name := range []string{"list_projects", "list_initiatives", "list_documents", "list_issues", "count_issues", "list_notifications"} {
						result, err := f.service.runFlowTool(t.Context(), actor, name, map[string]any{"limit": 100})
						if err != nil {
							t.Fatalf("%s: %v", name, err)
						}
						raw, _ := json.Marshal(result)
						if spec.restricted && strings.Contains(string(raw), "scope-hidden-") {
							t.Errorf("MCP %s leaks a hidden reference", name)
						}
						if name == "count_issues" {
							var count struct{ Count int }
							_ = json.Unmarshal(raw, &count)
							want := 3
							if spec.restricted {
								want = 2
							}
							if spec.empty {
								want = 0
							}
							if count.Count != want {
								t.Errorf("MCP issue count=%d, want %d", count.Count, want)
							}
						}
						if name == "list_notifications" && spec.actor == "user" {
							var page struct{ UnreadCount int }
							_ = json.Unmarshal(raw, &page)
							want := 3
							if spec.restricted {
								want = 2
							}
							if spec.empty {
								want = 0
							}
							if page.UnreadCount != want {
								t.Errorf("MCP unread count=%d, want %d", page.UnreadCount, want)
							}
						}
					}
					if spec.actor == "user" {
						var page store.NotificationPage
						read(secret, "/api/notifications?limit=1", &page)
						want := 3
						if spec.restricted {
							want = 2
						}
						if spec.empty {
							want = 0
						}
						if page.UnreadCount != want || len(page.Notifications) != min(1, want) {
							t.Error("notification page or count is outside the credential scope")
						}
						if spec.restricted && !spec.empty {
							raw, err := base64.RawURLEncoding.DecodeString(page.NextCursor)
							if err != nil || strings.Contains(string(raw), "scope-hidden-") {
								t.Error("notification cursor reveals a hidden row")
							}
							cursor := page.NextCursor
							page = store.NotificationPage{}
							read(secret, "/api/notifications?limit=1&cursor="+cursor, &page)
							if len(page.Notifications) != 1 || page.Notifications[0].ID != "scope-notification-child" || page.NextCursor != "" {
								t.Error("notification paging lost an allowed record")
							}
						}
						request := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
						request.Header.Set("Authorization", "Bearer "+secret)
						request.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
						request.Header.Set("X-Flow-Projection", "project-list")
						response := httptest.NewRecorder()
						f.host.Config.Handler.ServeHTTP(response, request)
						var projected domain.Bootstrap
						if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &projected) != nil || len(projected.Notifications) != want {
							t.Error("project-list bootstrap changed notification access")
						}
					}
					if !spec.empty {
						for _, name := range []string{"get_issue", "get_project", "get_document"} {
							id := map[string]string{"get_issue": "scope-issue", "get_project": "scope-project", "get_document": "scope-document"}[name]
							result, err := f.service.runFlowTool(t.Context(), actor, name, map[string]any{"id": id, "query": id})
							if err != nil {
								t.Fatalf("%s: %v", name, err)
							}
							raw, _ := json.Marshal(result)
							if spec.restricted && strings.Contains(string(raw), "scope-hidden-") {
								t.Errorf("MCP %s leaks a hidden reference", name)
							}
						}
					}
				})
			}
			ownerAfter := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/projects/scope-project", f.data.Workspace.URLKey)
			if ownerBefore.Code != ownerAfter.Code || ownerBefore.Body.String() != ownerAfter.Body.String() {
				t.Error("credential reads changed the owner's projection")
			}
			ownerIssueAfter := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/issue-records/scope-issue", f.data.Workspace.URLKey)
			if ownerIssueBefore.Code != ownerIssueAfter.Code || ownerIssueBefore.Body.String() != ownerIssueAfter.Body.String() {
				t.Error("credential reads changed the owner's issue")
			}
		})
	}
}

func TestScaleP03APIKeyTeamScopeWebhookReads(t *testing.T) {
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := apiKeyScopeFixture(t, repo)
			workspace := f.data.Workspace.URLKey
			if err := repo.MutateWorkspace(t.Context(), workspace, "test.scope_hooks", "", nil, func(data *domain.Bootstrap) error {
				data.Webhooks = append(data.Webhooks,
					domain.Webhook{ID: "scope-hook", CreatorID: f.data.Viewer.ID, TeamRestriction: "selected", TeamIDs: []string{"scope-public"}},
					domain.Webhook{ID: "scope-hidden-hook", CreatorID: f.data.Viewer.ID, TeamRestriction: "selected", TeamIDs: []string{"scope-private"}},
					domain.Webhook{ID: "scope-shared-hook", CreatorID: f.data.Viewer.ID, TeamRestriction: "selected", TeamIDs: []string{"scope-public", "scope-private"}},
					domain.Webhook{ID: "scope-global-hook", CreatorID: f.data.Viewer.ID, TeamRestriction: "all"},
				)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			data, _, err := repo.BootstrapForUser(t.Context(), workspace, f.data.Viewer.ID)
			if err != nil {
				t.Fatal(err)
			}
			project := data.Projects[slices.IndexFunc(data.Projects, func(p domain.Project) bool { return p.ID == "scope-project" })]
			payload, _ := json.Marshal(map[string]any{"type": "Project", "data": project, "updatedFrom": map[string]any{"dependencyIds": []string{"scope-hidden-project", "scope-allowed-project"}}})
			deliveryID := idgen.New("scope-delivery-")
			if _, err := repo.EnqueueWebhookDelivery(t.Context(), domain.WebhookDelivery{ID: deliveryID, WorkspaceKey: workspace, WebhookID: "scope-hook", URL: "https://example.invalid/hook", EventID: idgen.New("scope-event-"), EventType: "Project", Payload: payload}); err != nil {
				t.Fatal(err)
			}
			for _, spec := range []struct {
				name, restriction string
				teams             []string
				count             int
			}{
				{"selected-set", "selected", []string{"scope-public"}, 1},
				{"selected-empty", "selected", []string{}, 0},
				{"unrestricted", "all", nil, 4},
			} {
				t.Run(spec.name, func(t *testing.T) {
					secret, _ := apiKeyScopeCredential(t, f, "user", spec.restriction, spec.teams, []string{"read", "admin"})
					response := scaleP03Read(t, f, nil, secret, http.MethodGet, "/api/webhooks?first=100", workspace)
					var hooks connection[domain.Webhook]
					if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &hooks) != nil || len(hooks.Nodes) != spec.count {
						t.Fatalf("webhook list status=%d count=%d", response.Code, len(hooks.Nodes))
					}
					response = scaleP03Read(t, f, nil, secret, http.MethodGet, "/api/webhooks/scope-hook/deliveries/"+deliveryID, workspace)
					if spec.count == 0 {
						if response.Code != http.StatusNotFound {
							t.Fatalf("empty key delivery status=%d", response.Code)
						}
						return
					}
					if response.Code != http.StatusOK {
						t.Fatalf("delivery status=%d", response.Code)
					}
					if strings.Contains(response.Body.String(), "scope-hidden-") != (spec.restriction == "all") {
						t.Error("delivery snapshot or previous values have the wrong reference scope")
					}
					if !strings.Contains(response.Body.String(), "scope-allowed-project") {
						t.Error("delivery lost an allowed dependency")
					}
					for _, hook := range []string{"scope-shared-hook", "scope-global-hook"} {
						response = scaleP03Read(t, f, nil, secret, http.MethodGet, "/api/webhooks/"+hook+"/deliveries", workspace)
						if (response.Code == http.StatusOK) != (spec.restriction == "all") {
							t.Error("delivery log reaches a subscription outside the key's teams")
						}
					}
				})
			}
		})
	}
}
