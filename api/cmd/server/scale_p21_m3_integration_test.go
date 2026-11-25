package main

import (
	"net/http"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21M3IntegrationErrorDifferential(t *testing.T) {
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{
		{
			name: "oauth-integration-error", kind: "integrationConnections", id: "m3-connection", body: `{}`,
			seed: func(data *domain.Bootstrap, _ domain.User) {
				data.IntegrationConnections = []domain.IntegrationConnection{
					{ID: "m3-connection", Provider: "github", Status: "configured", LastError: "before", Config: map[string]string{"retained": "keep"}},
					{ID: "m3-other-connection", Provider: "gitlab", Status: "connected", Config: map[string]string{"retained": "other"}},
				}
			},
			handler: func(s *server, w http.ResponseWriter, r *http.Request) {
				persistOAuthIntegrationError(s, r, "github", r.PathValue("id"), "after")
				w.WriteHeader(http.StatusNoContent)
			},
		},
	})
}

func TestScaleP21M3JiraLinkDifferential(t *testing.T) {
	seed := func(data *domain.Bootstrap, _ domain.User) {
		data.JiraLinks = []domain.JiraLink{
			{ID: "m3-jira", JiraProjectID: "target", TeamID: data.Teams[0].ID, SyncDirection: "bidirectional"},
			{ID: "m3-other-jira", JiraProjectID: "other", TeamID: data.Teams[0].ID, SyncDirection: "unidirectional", StatusMap: map[string]string{"Retained": ""}},
		}
	}
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{
		{
			name: "jira-link-update", kind: "jiraLinks", id: "m3-jira", body: `{"syncDirection":"unidirectional","statusMap":{"Open":""}}`,
			seed: seed, handler: (*server).updateJiraLink,
			otherKeys: []store.RecordKey{{Kind: "jiraLinks", ID: "m3-other-jira"}},
		},
		{
			name: "jira-link-noop", kind: "jiraLinks", id: "m3-jira", body: `{"syncDirection":"bidirectional"}`,
			seed: seed, handler: (*server).updateJiraLink,
			otherKeys: []store.RecordKey{{Kind: "jiraLinks", ID: "m3-other-jira"}},
		},
		{
			name: "jira-link-delete", kind: "jiraLinks", id: "m3-jira", body: `{}`,
			seed: seed, handler: (*server).deleteJiraLink,
			otherKeys: []store.RecordKey{{Kind: "jiraLinks", ID: "m3-other-jira"}},
		},
	})
}
