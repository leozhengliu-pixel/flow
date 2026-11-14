package main

import (
	"encoding/json"
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

func TestScaleP21SW20JiraRemoteInstalledSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		service := &server{store: repo}
		completed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		for _, spec := range []struct {
			name, provider, status, lastError string
		}{
			{"first-provider", "jira\x00", "connected", ""},
			{"literal-status", "jira", "connected\x00", ""},
			{"literal-last-error", "jira", "configured", "\x00"},
		} {
			t.Run(spec.name, func(t *testing.T) {
				firstID, secondID := idgen.New("sw20-jira-"), idgen.New("sw20-jira-")
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "integrationConnections"), workspace, "scale_p21_sw20.jira_source", "", nil, func(data *domain.Bootstrap) error {
					data.IntegrationConnections = []domain.IntegrationConnection{
						{ID: firstID, Provider: spec.provider, Status: spec.status, LastError: spec.lastError, OAuthAuthorized: true, OAuthCompletedAt: &completed},
						{ID: secondID, Provider: "jira", Status: "disconnected"},
					}
					return nil
				}); err != nil {
					t.Fatal("accepted trusted producer", err)
				}
				warm, found := repo.WorkspaceMetadata(workspace)
				if !found || len(warm.IntegrationConnections) != 2 || jiraConnected(jiraConnection(&warm)) {
					t.Fatal("exact installed disconnected witness changed")
				}
				hydrated, err := f.probe.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{
					Fields: []string{"integrationConnections"},
				})
				canonical := hydrated.Data
				if err != nil || !jiraConnected(jiraConnection(&canonical)) {
					t.Fatal("canonical connected witness changed", err)
				}
				f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
					for _, endpoint := range []struct {
						path, wantError string
						handler         func(*server, http.ResponseWriter, *http.Request)
					}{
						{"/api/jira/projects", "Connect Jira OAuth before listing projects. Live project discovery needs a completed OAuth token.", (*server).listJiraRemoteProjects},
						{"/api/jira/projects/project/statuses", "Failed to load Jira statuses. Connect Jira OAuth first.", (*server).listJiraRemoteStatuses},
					} {
						request := f.request(user, key, nil)
						request.Method, request.URL.Path = http.MethodGet, endpoint.path
						request.SetPathValue("projectId", " project ")
						response := httptest.NewRecorder()
						scaleP21M3Call(service, response, request, endpoint.handler)
						var body map[string]any
						if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body["error"] != endpoint.wantError || body["status"] != "unavailable" {
							t.Fatal("actual handler lost exact installed decision", response.Code, response.Body.String())
						}
						if strings.HasSuffix(endpoint.path, "/statuses") && body["projectId"] != "project" {
							t.Fatal("project ID normalization changed", body)
						}
					}
				})
				request := f.request(f.data.Viewer, nil, map[string]any{"jiraProjectId": idgen.New("remote-project-"), "teamId": f.public})
				response := httptest.NewRecorder()
				service.createJiraLink(response, request)
				if response.Code != http.StatusConflict {
					t.Fatal("actual creator did not use exact installed connection", response.Code, response.Body.String())
				}
				t.Log("installed first-provider/connected decision differs from canonical SQL; both remote endpoints and create retain the installed source")
			})
		}
	}
}

func TestScaleP21SW20JiraLinksInstalledSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		link := domain.JiraLink{
			ID: idgen.New("sw20-jira-link-"), JiraProjectID: "project\x00id", JiraProjectName: "warm\x00name",
			TeamID: f.public, SyncDirection: "bidirectional",
			StatusMap: map[string]string{"Open\x00": "state\x00", "Closed": ""},
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "jiraLinks"), workspace, "scale_p21_sw20.jira_links_source", "", nil, func(data *domain.Bootstrap) error {
			data.JiraLinks = []domain.JiraLink{link}
			return nil
		}); err != nil {
			t.Fatal("accepted trusted producer", err)
		}
		hydrated, err := f.probe.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{
			Read: []store.RecordKey{{Kind: "jiraLinks", ID: link.ID}},
		})
		canonical := hydrated.Data
		if err != nil || len(canonical.JiraLinks) != 1 || canonical.JiraLinks[0].JiraProjectName != "warmname" || reflect.DeepEqual(canonical.JiraLinks[0], link) {
			t.Fatal("canonical source witness changed", err)
		}
		service := &server{store: repo}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			request := f.request(user, key, nil)
			request.Method, request.URL.Path = http.MethodGet, "/api/jira/links"
			response := httptest.NewRecorder()
			scaleP21M3Call(service, response, request, (*server).listJiraLinks)
			var actual []domain.JiraLink
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &actual) != nil || !reflect.DeepEqual(actual, []domain.JiraLink{link}) {
				t.Fatal("actual list lost exact installed body or changed credential projection", response.Code, response.Body.String())
			}
		})
		t.Log("listJiraLinks returns exact installed link bodies across the principal/key matrix; canonical streaming alone cannot reproduce them")
	}
}
