package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW5SettingsPointDifferential(t *testing.T) {
	issueSeed := func(data *domain.Bootstrap, actor domain.User) {
		data.IssueTemplates = []domain.IssueTemplate{
			{ID: "sw5-issue-template", Name: "Target", Scope: "workspace", Creator: actor},
			{ID: "sw5-other-issue-template", Name: "Retained", Scope: "team", TeamID: data.Teams[0].ID, Body: strings.Repeat("unrelated ", 40000)},
		}
	}
	documentSeed := func(data *domain.Bootstrap, actor domain.User) {
		data.DocumentTemplates = []domain.DocumentTemplate{
			{ID: "sw5-document-template", Name: "Before", Title: "Before", Creator: actor, ContentData: map[string]any{"retained": true}, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{ID: "sw5-other-document-template", Name: "Retained", Content: strings.Repeat("unrelated ", 40000)},
		}
	}
	integrationSeed := func(data *domain.Bootstrap, _ domain.User) {
		data.IntegrationConnections = []domain.IntegrationConnection{
			{ID: "sw5-connection", Provider: "slack", Name: "Before", Status: "configured", Config: map[string]string{"retained": "keep", "apiToken": "fixture-private"}, Scopes: []string{"read"}, Channels: []string{"before"}},
			{ID: "sw5-other-connection", Provider: "github", Name: "Retained", Config: map[string]string{"unrelated": strings.Repeat("unrelated ", 40000)}},
		}
	}
	integrationHandler := func(s *server, w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("provider", "slack")
		s.updateIntegration(w, r)
	}
	operations := []scaleP21M3PointOperation{
		{
			name: "sw5-issue-template-delete", kind: "issueTemplates", id: "sw5-issue-template", body: `{}`,
			seed: issueSeed, handler: (*server).deleteWorkspaceIssueTemplate,
			otherKeys: []store.RecordKey{{Kind: "issueTemplates", ID: "sw5-other-issue-template"}},
		},
		{
			name: "sw5-team-template-empty-visibility-delete", kind: "issueTemplates", id: "sw5-issue-template", body: `{}`,
			seed: func(data *domain.Bootstrap, actor domain.User) {
				issueSeed(data, actor)
				data.IssueTemplates[0].Scope, data.IssueTemplates[0].TeamID = "team", data.Teams[0].ID
			},
			handler:   (*server).deleteWorkspaceIssueTemplate,
			otherKeys: []store.RecordKey{{Kind: "issueTemplates", ID: "sw5-other-issue-template"}},
		},
		{
			name: "sw5-document-template-delete", kind: "documentTemplates", id: "sw5-document-template", body: `{}`,
			seed: documentSeed, handler: (*server).deleteDocumentTemplate,
			otherKeys: []store.RecordKey{{Kind: "documentTemplates", ID: "sw5-other-document-template"}},
		},
		{
			name: "sw5-oauth-application-delete", kind: "oauthApplications", id: "sw5-application", body: `{}`,
			seed: func(data *domain.Bootstrap, actor domain.User) {
				data.OAuthApplications = []domain.OAuthApplication{
					{ID: "sw5-application", ClientID: "sw5-client-" + data.Workspace.ID, Name: "Target", CreatorID: actor.ID},
					{ID: "sw5-other-application", ClientID: "sw5-other-client-" + data.Workspace.ID, Name: "Retained", Description: strings.Repeat("unrelated ", 40000)},
				}
			},
			handler:   (*server).deleteOAuthApplication,
			otherKeys: []store.RecordKey{{Kind: "oauthApplications", ID: "sw5-other-application"}},
		},
		{
			name: "sw5-integration-update", kind: "integrationConnections", id: "sw5-connection",
			body: `{"name":"After","config":{"retained":"updated","new":"value","oauthAccessToken":"ignored"},"status":"paused","scopes":["WRITE","write","read"],"channels":[" after ","after"],"linkbackEnabled":true}`,
			seed: integrationSeed, handler: integrationHandler,
			otherKeys: []store.RecordKey{{Kind: "integrationConnections", ID: "sw5-other-connection"}},
		},
		{
			name: "sw5-integration-noop", kind: "integrationConnections", id: "sw5-connection", body: `{}`,
			seed: integrationSeed, handler: integrationHandler,
			otherKeys: []store.RecordKey{{Kind: "integrationConnections", ID: "sw5-other-connection"}},
		},
		{
			name: "sw5-document-template-update", kind: "documentTemplates", id: "sw5-document-template",
			body: `{"name":"After","description":"After","title":"Title","content":"Body","contentState":"state","contentData":{"retained":false},"icon":"memo","color":"#123456"}`,
			seed: documentSeed, handler: (*server).updateDocumentTemplate,
			otherKeys: []store.RecordKey{{Kind: "documentTemplates", ID: "sw5-other-document-template"}},
		},
		{
			name: "sw5-document-template-noop", kind: "documentTemplates", id: "sw5-document-template", body: `{}`,
			seed: documentSeed, handler: (*server).updateDocumentTemplate,
			otherKeys: []store.RecordKey{{Kind: "documentTemplates", ID: "sw5-other-document-template"}},
		},
		{
			name: "sw5-document-template-invalid-name", kind: "documentTemplates", id: "sw5-document-template", body: `{"name":""}`,
			seed: documentSeed, handler: (*server).updateDocumentTemplate,
			otherKeys: []store.RecordKey{{Kind: "documentTemplates", ID: "sw5-other-document-template"}},
		},
		{
			name: "sw5-document-template-invalid-team", kind: "documentTemplates", id: "sw5-document-template", body: `{"teamId":"sw5-unknown-team"}`,
			seed: documentSeed, handler: (*server).updateDocumentTemplate,
			otherKeys: []store.RecordKey{{Kind: "documentTemplates", ID: "sw5-other-document-template"}},
		},
	}
	var teamID string
	for _, clear := range []bool{false, true} {
		name := "sw5-document-template-team"
		if clear {
			name += "-clear"
		}
		operations = append(operations, scaleP21M3PointOperation{
			name: name, kind: "documentTemplates", id: "sw5-document-template", body: `{}`,
			seed: func(data *domain.Bootstrap, actor domain.User) {
				documentSeed(data, actor)
				teamID = data.Teams[0].ID
				data.DocumentTemplates[0].TeamID = teamID
			},
			handler: func(s *server, w http.ResponseWriter, r *http.Request) {
				value := " " + teamID + " "
				if clear {
					value = ""
				}
				body, _ := json.Marshal(map[string]string{"teamId": value, "name": "After"})
				r.Body = io.NopCloser(strings.NewReader(string(body)))
				s.updateDocumentTemplate(w, r)
			},
			otherKeys: []store.RecordKey{{Kind: "documentTemplates", ID: "sw5-other-document-template"}},
		})
	}
	scaleP21M3PointDifferential(t, operations)
}

func TestScaleP21SW5SettingsPointFailures(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		target := idgen.New("sw5-errors-")
		reset := func(large bool) {
			t.Helper()
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw5.reset_errors", "", nil, func(data *domain.Bootstrap) error {
				data.IssueTemplates = []domain.IssueTemplate{{ID: target, Scope: "team", VisibilityTeamID: data.Teams[0].ID}}
				content := "before"
				if large {
					content = strings.Repeat("large target ", 30000)
				}
				data.DocumentTemplates = []domain.DocumentTemplate{{ID: target, Name: "Before", Content: content}}
				data.IntegrationConnections = []domain.IntegrationConnection{{ID: target, Provider: "slack", Name: "Before", Config: map[string]string{"retained": "keep"}}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, body, provider string
				handler              func(*server, http.ResponseWriter, *http.Request)
				status               int
			}{
				{"team-template-private-visibility", `{}`, "", (*server).deleteWorkspaceIssueTemplate, http.StatusNotFound},
				{"integration-wrong-provider", `{"name":"After"}`, "github", (*server).updateIntegration, http.StatusNotFound},
				{"integration-secret-input", `{"config":{"clientSecret":"fixture-secret"}}`, "slack", (*server).updateIntegration, http.StatusUnprocessableEntity},
			} {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset(false)
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					r.SetPathValue("id", target)
					r.SetPathValue("provider", spec.provider)
					r.Body = io.NopCloser(strings.NewReader(spec.body))
					spec.handler(&server{store: source}, w, r)
					if w.Code != spec.status {
						t.Fatalf("%s: status=%d body=%s", spec.name, w.Code, w.Body.String())
					}
					outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false), scaleP21M2Canonical(t, repo, workspace,
						store.RecordKey{Kind: "issueTemplates", ID: target}, store.RecordKey{Kind: "documentTemplates", ID: target}, store.RecordKey{Kind: "integrationConnections", ID: target})})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("%s differs: %s", spec.name, scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		})
		reset(false)
		before := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "documentTemplates", ID: target})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r, w := f.request(f.data.Viewer, nil, map[string]string{"name": "After"}), httptest.NewRecorder()
		r.SetPathValue("id", target)
		f.probe.SetWebhookSink(func(string, domain.DomainEvent) { t.Error("cancelled mutation published a webhook") })
		f.probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { t.Error("cancelled mutation published realtime") })
		(&server{store: f.probe}).updateDocumentTemplate(w, r.WithContext(ctx))
		if w.Code < 400 {
			t.Fatal("cancelled mutation succeeded")
		}
		after := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "documentTemplates", ID: target})
		if !reflect.DeepEqual(before, after) {
			t.Fatal("cancelled mutation changed canonical target")
		}
		reset(true)
		hash := scaleP21M3Fingerprint(t, repo, workspace, "documentTemplates")
		for _, handler := range []func(*server, http.ResponseWriter, *http.Request){(*server).updateDocumentTemplate, (*server).deleteDocumentTemplate} {
			r, w := f.request(f.data.Viewer, nil, map[string]string{"name": "After"}), httptest.NewRecorder()
			r.SetPathValue("id", target)
			handler(&server{store: f.probe}, w, r)
			if w.Code < 400 || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) {
				t.Fatalf("large target must fail explicitly before mutation: %d %s", w.Code, w.Body.String())
			}
			if scaleP21M3Fingerprint(t, repo, workspace, "documentTemplates") != hash {
				t.Fatal("budget rejection changed canonical target")
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("settings point failures decoded complete metadata")
		}
	}
}
