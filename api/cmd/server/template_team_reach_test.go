package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const templateReachIntakeToken = "tsc-intake-token"

// templateReachSetup is teamScopedSeed plus the public team's intake address
// configured with the private team's template (as it could be before the
// address's team was enforced), and sessions for every principal.
func templateReachSetup(t *testing.T, repo *store.SQLiteStore) (teamScopedFixture, map[string]string) {
	t.Helper()
	f := teamScopedSeed(t, repo)
	if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "template_reach.seed", "", nil, func(data *domain.Bootstrap) error {
		// Every member of the public team manages its templates.
		settings := domain.TeamSettings{TeamID: f.public, Access: "public", TemplatePermission: "allMembers"}
		for _, state := range data.States {
			if state.TeamID == "" && settings.DefaultStateID == "" {
				settings.DefaultStateID = state.ID
			}
		}
		data.TeamSettings[f.public] = settings
		for index := range data.EmailIntakeAddresses {
			if data.EmailIntakeAddresses[index].ID == "tsc-intake-pub" {
				data.EmailIntakeAddresses[index].TemplateID = "tsc-tpl-priv"
				data.EmailIntakeAddresses[index].InboundTokenHash = secretHash(templateReachIntakeToken)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, principal := range f.principals {
		_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		tokens[principal.name] = token
	}
	return f, tokens
}

func templateReachCall(t *testing.T, handler http.Handler, workspace, token, method, path string, body any) (int, string) {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", workspace)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// TestTemplateTeamReach: issue and document templates are used, edited and
// deleted only within the teams the actor reaches, with the bounded reads off
// and on; the same requests within reach keep working.
func TestTemplateTeamReach(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, bounded := range []bool{false, true} {
		for _, repo := range scaleServerTestStores(t) {
			f, tokens := templateReachSetup(t, repo)
			workspace := f.data.Workspace.URLKey
			handler := f.host.Config.Handler
			if bounded {
				handler = newHandler(&server{store: f.probe, uploadPath: t.TempDir()})
			}
			mode := map[bool]string{false: "literal", true: "bounded"}[bounded]
			public, private, child := f.teams["pub"], f.teams["priv"], f.teams["child"]
			type step struct {
				name, principal, method, path string
				body                          any
				ok                            bool
				status                        int
			}
			steps := []step{
				// Issue creation from a template (POST /api/issues).
				{"issue from private template", "member-private-outsider", http.MethodPost, "/api/issues", map[string]any{"title": "Leak", "teamId": public, "templateId": "tsc-tpl-priv"}, false, http.StatusForbidden},
				{"issue from template narrowed to a private team", "member-private-outsider", http.MethodPost, "/api/issues", map[string]any{"title": "Leak", "teamId": public, "templateId": "tsc-tpl-narrowed-priv"}, false, http.StatusForbidden},
				{"issue from template with private sub-issue", "member-private-outsider", http.MethodPost, "/api/issues", map[string]any{"title": "Leak", "teamId": public, "templateId": "tsc-tpl-subissue-priv"}, false, http.StatusForbidden},
				{"issue from workspace template defaulting to a private team", "member-private-outsider", http.MethodPost, "/api/issues", map[string]any{"title": "Leak", "teamId": public, "templateId": "tsc-tpl-wsdefault-priv"}, false, http.StatusForbidden},
				{"issue from another team's template", "sub-team-inheritance", http.MethodPost, "/api/issues", map[string]any{"title": "Mismatch", "teamId": public, "templateId": "tsc-tpl-priv"}, false, http.StatusForbidden},
				{"admin outside the private team: private sub-issue", "admin-private-outsider", http.MethodPost, "/api/issues", map[string]any{"title": "Leak", "teamId": public, "templateId": "tsc-tpl-subissue-priv"}, false, http.StatusForbidden},
				{"issue from workspace template", "member-private-outsider", http.MethodPost, "/api/issues", map[string]any{"title": "Fine", "teamId": public, "templateId": "tsc-tpl-ws"}, true, http.StatusCreated},
				{"issue from own team's template", "member-private-outsider", http.MethodPost, "/api/issues", map[string]any{"title": "Fine", "teamId": public, "templateId": "tsc-tpl-pub"}, true, http.StatusCreated},
				{"sub-team issue from parent team's template", "sub-team-inheritance", http.MethodPost, "/api/issues", map[string]any{"title": "Inherited", "teamId": child, "templateId": "tsc-tpl-priv"}, true, http.StatusCreated},
				{"private member: private sub-issue", "admin", http.MethodPost, "/api/issues", map[string]any{"title": "Fine", "teamId": public, "templateId": "tsc-tpl-subissue-priv"}, true, http.StatusCreated},
				// Asks.
				{"ask with private template", "member-private-outsider", http.MethodPost, "/api/asks", map[string]any{"title": "Ask", "teamId": public, "templateId": "tsc-tpl-priv"}, false, http.StatusNotFound},
				{"ask with own team's template", "member-private-outsider", http.MethodPost, "/api/asks", map[string]any{"title": "Ask", "teamId": public, "templateId": "tsc-tpl-pub"}, true, http.StatusCreated},
				// Email intake configuration.
				{"intake address with another team's template", "admin", http.MethodPost, "/api/teams/" + public + "/email-intake-addresses", map[string]any{"type": "team", "templateId": "tsc-tpl-priv"}, false, http.StatusBadRequest},
				{"intake address with own team's template", "admin", http.MethodPost, "/api/teams/" + public + "/email-intake-addresses", map[string]any{"type": "team", "templateId": "tsc-tpl-pub"}, true, http.StatusCreated},
				// Issue template configuration.
				{"workspace route edits a team template", "member-private-outsider", http.MethodPatch, "/api/issue-templates/tsc-tpl-priv", map[string]any{"name": "Taken"}, false, http.StatusNotFound},
				{"workspace route edits own team's template", "member-private-outsider", http.MethodPatch, "/api/issue-templates/tsc-tpl-pub", map[string]any{"name": "Taken"}, false, http.StatusNotFound},
				{"workspace route deletes a team template", "member-private-outsider", http.MethodDelete, "/api/issue-templates/tsc-tpl-priv", nil, false, http.StatusNotFound},
				{"workspace template named after a private team", "member-private-outsider", http.MethodPost, "/api/issue-templates", map[string]any{"name": "Private default", "teamId": private}, false, http.StatusBadRequest},
				{"workspace template narrowed to a private team", "member-private-outsider", http.MethodPost, "/api/issue-templates", map[string]any{"name": "Private narrowed", "visibilityTeamId": private}, false, http.StatusBadRequest},
				{"team template with a private sub-issue", "member-private-outsider", http.MethodPost, "/api/teams/" + public + "/templates", map[string]any{"name": "Private child", "subIssues": []map[string]any{{"title": "Child", "teamId": private}}}, false, http.StatusBadRequest},
				{"workspace route edits a workspace template", "member-private-outsider", http.MethodPatch, "/api/issue-templates/tsc-tpl-ws", map[string]any{"name": "Renamed"}, true, http.StatusOK},
				{"team template in own team", "member-private-outsider", http.MethodPost, "/api/teams/" + public + "/templates", map[string]any{"name": "Own", "subIssues": []map[string]any{{"title": "Child", "teamId": public}}}, true, http.StatusCreated},
				{"admin narrows a workspace template to a private team", "admin", http.MethodPost, "/api/issue-templates", map[string]any{"name": "Admin narrowed", "visibilityTeamId": private}, true, http.StatusCreated},
				// Documents from templates.
				{"document from private team's template", "member-private-outsider", http.MethodPost, "/api/documents", map[string]any{"title": "Leak", "templateId": "tsc-doctpl-priv"}, false, http.StatusBadRequest},
				{"admin outside: document filed in the private team", "admin-private-outsider", http.MethodPost, "/api/documents", map[string]any{"title": "Leak", "templateId": "tsc-doctpl-priv"}, false, http.StatusForbidden},
				{"document from own team's template", "member-private-outsider", http.MethodPost, "/api/documents", map[string]any{"title": "Fine", "templateId": "tsc-doctpl-pub"}, true, http.StatusCreated},
				{"document from workspace template", "member-private-outsider", http.MethodPost, "/api/documents", map[string]any{"title": "Fine", "templateId": "tsc-doctpl-ws"}, true, http.StatusCreated},
				{"private member: document from private template", "sub-team-inheritance", http.MethodPost, "/api/documents", map[string]any{"title": "Fine", "templateId": "tsc-doctpl-priv"}, true, http.StatusCreated},
				// Document template configuration.
				{"edit private team's document template", "member-private-outsider", http.MethodPatch, "/api/document-templates/tsc-doctpl-priv", map[string]any{"name": "Taken"}, false, http.StatusNotFound},
				{"delete private team's document template", "member-private-outsider", http.MethodDelete, "/api/document-templates/tsc-doctpl-priv", nil, false, http.StatusNotFound},
				{"move a document template into a private team", "member-private-outsider", http.MethodPatch, "/api/document-templates/tsc-doctpl-pub", map[string]any{"teamId": private}, false, http.StatusBadRequest},
				{"create a document template in a private team", "member-private-outsider", http.MethodPost, "/api/document-templates", map[string]any{"name": "Private", "teamId": private}, false, http.StatusBadRequest},
				{"edit own team's document template", "member-private-outsider", http.MethodPatch, "/api/document-templates/tsc-doctpl-pub", map[string]any{"name": "Renamed"}, true, http.StatusOK},
				{"edit workspace document template", "member-private-outsider", http.MethodPatch, "/api/document-templates/tsc-doctpl-ws", map[string]any{"name": "Renamed"}, true, http.StatusOK},
				{"private member edits private document template", "sub-team-inheritance", http.MethodPatch, "/api/document-templates/tsc-doctpl-priv", map[string]any{"name": "Renamed"}, true, http.StatusOK},
				{"admin outside edits private document template", "admin-private-outsider", http.MethodPatch, "/api/document-templates/tsc-doctpl-child", map[string]any{"name": "Renamed"}, true, http.StatusOK},
			}
			for _, step := range steps {
				code, body := templateReachCall(t, handler, workspace, tokens[step.principal], step.method, step.path, step.body)
				if code != step.status {
					t.Errorf("%s: %s: %d %.300s, want %d", mode, step.name, code, body, step.status)
				}
			}
			// Email from the address configured with the private team's
			// template lands in the address's team without the template.
			code, body := templateReachCall(t, handler, workspace, "", http.MethodPost, "/api/email-intake/"+templateReachIntakeToken+"/receive", map[string]any{"messageId": "tsc-mail-" + mode, "from": "customer@example.test", "subject": "Hello", "text": "Body"})
			var received domain.Issue
			if code != http.StatusCreated || json.Unmarshal([]byte(body), &received) != nil {
				t.Errorf("%s: email intake: %d %.300s", mode, code, body)
			} else if received.Team.ID != public || received.Title != "Hello" {
				t.Errorf("%s: email intake created the issue in team %s (want the address's team %s)", mode, received.Team.ID, public)
			}
			// A team's template permission applies to its document templates
			// whatever the workspace-wide template permission.
			if err := repo.MutateWorkspace(t.Context(), workspace, "template_reach.owners_only", "", nil, func(data *domain.Bootstrap) error {
				settings := data.TeamSettings[public]
				settings.TeamID, settings.TemplatePermission = public, "owners"
				data.TeamSettings[public] = settings
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if bounded {
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
			}
			if code, body := templateReachCall(t, handler, workspace, tokens["member-private-outsider"], http.MethodPatch, "/api/document-templates/tsc-doctpl-pub", map[string]any{"name": "Owners only"}); code != http.StatusForbidden {
				t.Errorf("%s: team template permission ignored: %d %.300s", mode, code, body)
			}
			if code, body := templateReachCall(t, handler, workspace, tokens["member-private-outsider"], http.MethodDelete, "/api/document-templates/tsc-doctpl-pub", nil); code != http.StatusForbidden {
				t.Errorf("%s: team template permission ignored on delete: %d %.300s", mode, code, body)
			}
			if code, body := templateReachCall(t, handler, workspace, tokens["admin"], http.MethodPatch, "/api/document-templates/tsc-doctpl-pub", map[string]any{"name": "Owners only"}); code != http.StatusOK {
				t.Errorf("%s: admin lost the team's document template: %d %.300s", mode, code, body)
			}
		}
	}
}

// TestReleaseNotesStayInPipelineScope: notes generated when a release
// completes list only the issues within its pipeline's scope.
func TestReleaseNotesStayInPipelineScope(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	public := domain.Team{ID: "rn-public", Key: "PUB"}
	private := domain.Team{ID: "rn-private", Key: "PRV", Private: true}
	child := domain.Team{ID: "rn-child", Key: "CHD"}
	other := domain.Team{ID: "rn-other", Key: "OTH"}
	done := domain.WorkflowState{ID: "rn-done", Type: "completed"}
	data := domain.Bootstrap{
		Teams:        []domain.Team{public, private, child, other},
		TeamSettings: map[string]domain.TeamSettings{private.ID: {TeamID: private.ID, Access: "private"}, child.ID: {TeamID: child.ID, ParentTeamID: private.ID}},
		Issues: []domain.Issue{
			{ID: "rn-1", Identifier: "PUB-1", Title: "Public fix", Team: public, State: done},
			{ID: "rn-2", Identifier: "PRV-1", Title: "Secret fix", Team: private, State: done},
			{ID: "rn-3", Identifier: "CHD-1", Title: "Secret child fix", Team: child, State: done},
			{ID: "rn-4", Identifier: "OTH-1", Title: "Other team fix", Team: other, State: done},
		},
	}
	for _, test := range []struct {
		name          string
		teams         []string
		want, without []string
	}{
		{"pipeline of one team", []string{public.ID}, []string{"Public fix"}, []string{"Secret fix", "Secret child fix", "Other team fix"}},
		{"pipeline of the private team", []string{private.ID}, []string{"Secret fix"}, []string{"Public fix", "Secret child fix", "Other team fix"}},
		{"workspace pipeline", nil, []string{"Public fix", "Other team fix"}, []string{"Secret fix", "Secret child fix"}},
	} {
		release := domain.Release{ID: "rn-release-" + test.name, Name: "Release", IssueIDs: []string{"rn-1", "rn-2", "rn-3", "rn-4"}}
		pipeline := domain.ReleasePipeline{ID: "rn-pipeline", TeamIDs: test.teams}
		data := data
		data.ReleaseNotes, data.ReleaseHistory = nil, nil
		generateReleaseNotesOnCompletion(&data, &pipeline, &release, now)
		for _, title := range test.want {
			if !strings.Contains(release.ReleaseNotes, title) {
				t.Errorf("%s: notes miss %q: %q", test.name, title, release.ReleaseNotes)
			}
		}
		for _, title := range test.without {
			if strings.Contains(release.ReleaseNotes, title) || slices.ContainsFunc(data.ReleaseNotes, func(note domain.ReleaseNote) bool { return strings.Contains(note.Body, title) }) {
				t.Errorf("%s: notes leak %q: %q", test.name, title, release.ReleaseNotes)
			}
		}
	}
}
