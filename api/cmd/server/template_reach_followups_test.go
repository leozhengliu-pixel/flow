package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A chat binding's templates create issues as the group's members: the person
// saving the binding may only choose a template they could create issues from
// in the binding's team.
func TestChatBindingTemplatesFollowTemplateReach(t *testing.T) {
	provider := "feishu"
	f := newChatPermissionFixture(t, provider)
	f.authorizeTenant(provider)
	f.addGroup("oc_internal")
	base := "/api/integrations/" + provider + "/chat"
	if err := f.s.store.MutateWorkspace(workspaceFixtureContext(t.Context()), "test-workspace", "template.created", "chat_tpl_private_child", nil, func(data *domain.Bootstrap) error {
		data.IssueTemplates = append(data.IssueTemplates,
			domain.IssueTemplate{ID: "chat_tpl_private_child", Name: "Private follow-up", TeamID: f.publicTeam, Scope: "team", LabelIDs: []string{}, SubIssues: []domain.TemplateSubIssue{{ID: "chat_sub", Title: "Vault work", TeamID: f.privateTeam}}},
			domain.IssueTemplate{ID: "chat_tpl_public", Name: "Public", TeamID: f.publicTeam, Scope: "team", LabelIDs: []string{}},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	binding := domain.ChatBinding{ChatID: "oc_internal", ChatName: "Room", TeamID: f.publicTeam, Events: []string{"issue.created"}, Enabled: true, AllowCreate: true}
	// The workspace admin is not a member of the private team: they cannot
	// file its sub-issue, neither from the create form nor from requests.
	refused := binding
	refused.TemplateID = "chat_tpl_private_child"
	f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(refused), 400)
	refused = binding
	refused.Requests = &domain.ChatBindingRequests{Enabled: true, TemplateID: "chat_tpl_private_child"}
	f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(refused), 400)
	// The team's plain template stays usable.
	allowed := binding
	allowed.TemplateID = "chat_tpl_public"
	allowed.Requests = &domain.ChatBindingRequests{Enabled: true, TemplateID: "chat_tpl_public"}
	f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(allowed), 200)
}

// Issue template events reach a stream by the bootstrap's template rule: a
// template narrowed to a private team, or one whose sub-issue lands in it,
// is the private team's business, whatever its own team.
func TestRealtimeIssueTemplateEventsFollowTemplateTeams(t *testing.T) {
	fixture := newRealtimePolicyFixture(t)
	key := "test-workspace"
	write := func(id string, template domain.IssueTemplate) {
		if err := fixture.repo.MutateWorkspace(context.Background(), key, "issue_template.created", id, map[string]any{"name": "input"}, func(data *domain.Bootstrap) error {
			data.IssueTemplates = append(data.IssueTemplates, template)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	write("rt_tpl_narrowed", domain.IssueTemplate{ID: "rt_tpl_narrowed", Name: "Narrowed", TeamID: "team_test", VisibilityTeamID: fixture.private.ID, Scope: "team", LabelIDs: []string{}})
	write("rt_tpl_subissue", domain.IssueTemplate{ID: "rt_tpl_subissue", Name: "Sub-issue", TeamID: "team_test", Scope: "team", LabelIDs: []string{}, SubIssues: []domain.TemplateSubIssue{{ID: "rt_sub", Title: "Private", TeamID: fixture.private.ID}}})
	write("rt_tpl_public", domain.IssueTemplate{ID: "rt_tpl_public", Name: "Public", TeamID: "team_test", Scope: "team", LabelIDs: []string{}})
	waitForEvents(t, func() bool {
		return fixture.insiderSSE.has("issue_template.created", "rt_tpl_narrowed") && fixture.insiderSSE.has("issue_template.created", "rt_tpl_subissue") &&
			fixture.outsiderSSE.has("issue_template.created", "rt_tpl_public")
	})
	assertRealtimeEventsHide(t, "outsider", fixture.outsiderSSE.snapshot(), []string{"rt_tpl_narrowed", "rt_tpl_subissue"}, nil)
	assertRealtimeEventsHide(t, "guest", fixture.guestSSE.snapshot(), []string{"rt_tpl_narrowed", "rt_tpl_subissue"}, nil)
}

// A team's default templates name only templates the viewer sees, in the
// bootstrap and the team settings read, with the bounded reads off and on.
func TestTeamDefaultTemplatesFollowTemplateVisibility(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := teamScopedSeed(t, repo)
		at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "team_defaults.seed", "", nil, func(data *domain.Bootstrap) error {
			data.ProjectTemplates = append(data.ProjectTemplates, domain.ProjectTemplate{ID: "tsc-ptpl-priv", Name: "Private project template", TeamIDs: []string{f.teams["priv"]}, CreatedAt: at, UpdatedAt: at})
			settings := data.TeamSettings[f.public]
			settings.TeamID = f.public
			settings.DefaultIssueTemplateForMembersID = "tsc-tpl-narrowed-priv"
			settings.DefaultIssueTemplateForNonMembersID = "tsc-tpl-pub"
			settings.DefaultProjectTemplateID = "tsc-ptpl-priv"
			data.TeamSettings[f.public] = settings
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		type defaults struct{ members, nonMembers, project string }
		for _, test := range []struct {
			principal string
			key       *domain.APIKey
			want      defaults
		}{
			{"member-private-outsider", nil, defaults{"", "tsc-tpl-pub", ""}},
			{"admin", nil, defaults{"tsc-tpl-narrowed-priv", "tsc-tpl-pub", "tsc-ptpl-priv"}},
			{"admin", &domain.APIKey{ID: "tsc-defaults-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}, defaults{"", "tsc-tpl-pub", ""}},
		} {
			var user domain.User
			for _, principal := range f.principals {
				if principal.name == test.principal {
					user = principal.user
				}
			}
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				label := test.principal + map[bool]string{false: "", true: "/key"}[test.key != nil] + map[bool]string{false: "", true: "/bounded"}[source.BoundedMetadataEnabled()]
				for _, path := range []string{"/api/bootstrap", "/api/issue-records/bootstrap", "settings"} {
					var got domain.TeamSettings
					if path == "settings" {
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
							t.Fatal(err)
						}
						r := f.literalRequest(user, test.key, nil)
						r.Method, r.URL.Path = http.MethodGet, "/api/teams/"+f.public+"/settings"
						r.SetPathValue("id", f.public)
						r, closeOutput := beginRequestResourceReads(r)
						w := httptest.NewRecorder()
						(&server{store: source}).getTeamSettings(w, r)
						closeOutput()
						if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil {
							t.Fatalf("%s: team settings %d %.300s", label, w.Code, w.Body.String())
						}
					} else {
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
							t.Fatal(err)
						}
						service := &server{store: source}
						r := f.literalRequest(user, test.key, nil)
						r.Method, r.URL.Path = http.MethodGet, path
						r, closeOutput := beginRequestResourceReads(r)
						w := httptest.NewRecorder()
						mux := http.NewServeMux()
						mux.Handle("GET /api/bootstrap", serializeLegacyBootstrap(http.HandlerFunc(service.bootstrap)))
						mux.HandleFunc("GET /api/issue-records/bootstrap", service.issueRecordsBootstrap)
						service.signedFileURLs(mux).ServeHTTP(w, r)
						closeOutput()
						var data struct {
							TeamSettings map[string]domain.TeamSettings `json:"teamSettings"`
						}
						if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil {
							t.Fatalf("%s %s: %d %.300s", label, path, w.Code, w.Body.String())
						}
						got = data.TeamSettings[f.public]
					}
					if have := (defaults{got.DefaultIssueTemplateForMembersID, got.DefaultIssueTemplateForNonMembersID, got.DefaultProjectTemplateID}); have != test.want {
						t.Errorf("%s %s: defaults %+v, want %+v", label, path, have, test.want)
					}
				}
			}
		}
	}
}

// A team without stored settings shows the default template permission (all
// members); the team template routes and the document template check apply
// that same default, and a stored permission keeps applying as stored.
func TestTeamTemplatePermissionDefault(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, bounded := range []bool{false, true} {
		for _, repo := range scaleP03ServerTestStores(t) {
			f := scaleP21M2Setup(t, repo)
			workspace := f.data.Workspace.URLKey
			if settings, found := repo.TeamSettingsFor(workspace, f.public); found && settings.TemplatePermission != "" {
				t.Fatalf("fixture's public team has a stored template permission %q", settings.TemplatePermission)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			handler := f.host.Config.Handler
			if bounded {
				handler = newHandler(&server{store: f.probe, uploadPath: t.TempDir()})
			}
			var member domain.User
			for _, principal := range f.principals {
				if principal.name == "member-private-outsider" {
					member = principal.user
				}
			}
			_, token, err := repo.Login(t.Context(), member.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			mode := map[bool]string{false: "literal", true: "bounded"}[bounded]
			if code, body := templateReachCall(t, handler, workspace, token, http.MethodPost, "/api/teams/"+f.public+"/templates", map[string]any{"name": "Default permission"}); code != http.StatusCreated {
				t.Errorf("%s: team template with the default permission: %d %.300s", mode, code, body)
			}
			if code, body := templateReachCall(t, handler, workspace, token, http.MethodPost, "/api/document-templates", map[string]any{"name": "Default permission", "teamId": f.public}); code != http.StatusCreated {
				t.Errorf("%s: document template with the default permission: %d %.300s", mode, code, body)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "template_permission.owners", "", nil, func(data *domain.Bootstrap) error {
				data.TeamSettings[f.public] = domain.TeamSettings{TeamID: f.public, Access: "public", TemplatePermission: "owners"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			if code, body := templateReachCall(t, handler, workspace, token, http.MethodPost, "/api/teams/"+f.public+"/templates", map[string]any{"name": "Owners only"}); code != http.StatusForbidden {
				t.Errorf("%s: stored owners-only permission ignored by the team route: %d %.300s", mode, code, body)
			}
			if code, body := templateReachCall(t, handler, workspace, token, http.MethodPost, "/api/document-templates", map[string]any{"name": "Owners only", "teamId": f.public}); code != http.StatusForbidden {
				t.Errorf("%s: stored owners-only permission ignored by document templates: %d %.300s", mode, code, body)
			}
			// Narrowing a workspace template to the team is managing the
			// team's templates.
			if code, body := templateReachCall(t, handler, workspace, token, http.MethodPost, "/api/issue-templates", map[string]any{"name": "Narrowed", "visibilityTeamId": f.public}); code != http.StatusForbidden {
				t.Errorf("%s: narrowing to an owners-only team: %d %.300s", mode, code, body)
			}
			if code, body := templateReachCall(t, handler, workspace, token, http.MethodPost, "/api/issue-templates", map[string]any{"name": "Workspace-wide"}); code != http.StatusCreated {
				t.Errorf("%s: workspace template: %d %.300s", mode, code, body)
			}
		}
	}
}
