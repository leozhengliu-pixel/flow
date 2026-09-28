package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type jiraRemoteProject struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type jiraRemoteStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category,omitempty"`
}

func jiraConnection(data *domain.Bootstrap) *domain.IntegrationConnection {
	index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
		return item.Provider == "jira"
	})
	if index < 0 {
		return nil
	}
	return &data.IntegrationConnections[index]
}

func jiraConnected(connection *domain.IntegrationConnection) bool {
	if connection == nil {
		return false
	}
	if connection.Status == "connected" {
		return true
	}
	return connection.Status == "configured" && strings.TrimSpace(connection.OAuthAccessToken) != "" && connection.OAuthCompletedAt != nil && connection.LastError == ""
}

func (s *server) listJiraLinks(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	links := data.JiraLinks
	if links == nil {
		links = []domain.JiraLink{}
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *server) createJiraLink(w http.ResponseWriter, r *http.Request) {
	var input struct {
		JiraProjectID   string            `json:"jiraProjectId"`
		JiraProjectKey  string            `json:"jiraProjectKey"`
		JiraProjectName string            `json:"jiraProjectName"`
		TeamID          string            `json:"teamId"`
		SyncDirection   string            `json:"syncDirection"`
		StatusMap       map[string]string `json:"statusMap"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.JiraProjectID = strings.TrimSpace(input.JiraProjectID)
	input.TeamID = strings.TrimSpace(input.TeamID)
	input.SyncDirection = normalizeJiraSyncDirection(input.SyncDirection)
	if input.JiraProjectID == "" {
		writeError(w, http.StatusUnprocessableEntity, "Please select a Jira project.")
		return
	}
	if input.TeamID == "" {
		writeError(w, http.StatusUnprocessableEntity, "Please select a team.")
		return
	}
	var created domain.JiraLink
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "jira.link_created", input.JiraProjectID, input, func(data *domain.Bootstrap) error {
		if !jiraConnected(jiraConnection(data)) {
			return errConflict
		}
		if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == input.TeamID }) {
			return errInvalid
		}
		if slices.ContainsFunc(data.JiraLinks, func(link domain.JiraLink) bool { return link.JiraProjectID == input.JiraProjectID }) {
			return errConflict
		}
		now := time.Now().UTC()
		created = domain.JiraLink{
			ID:              fmt.Sprintf("jira_link_%d", now.UnixNano()),
			JiraProjectID:   input.JiraProjectID,
			JiraProjectKey:  strings.TrimSpace(input.JiraProjectKey),
			JiraProjectName: strings.TrimSpace(input.JiraProjectName),
			TeamID:          input.TeamID,
			SyncDirection:   input.SyncDirection,
			StatusMap:       input.StatusMap,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if created.StatusMap == nil {
			created.StatusMap = map[string]string{}
		}
		data.JiraLinks = append(data.JiraLinks, created)
		return nil
	})
	if err == errConflict {
		writeError(w, http.StatusConflict, "Jira must be connected before creating a sync link, or this project is already linked")
		return
	}
	if err == errInvalid {
		writeError(w, http.StatusUnprocessableEntity, "Please select a team.")
		return
	}
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateJiraLink(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var input struct {
		SyncDirection *string           `json:"syncDirection"`
		StatusMap     map[string]string `json:"statusMap"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.JiraLink
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "jira.link_updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.JiraLinks, func(link domain.JiraLink) bool { return link.ID == id })
		if index < 0 {
			return errNotFound
		}
		link := data.JiraLinks[index]
		if input.SyncDirection != nil {
			link.SyncDirection = normalizeJiraSyncDirection(*input.SyncDirection)
		}
		if input.StatusMap != nil {
			link.StatusMap = input.StatusMap
		}
		link.UpdatedAt = time.Now().UTC()
		data.JiraLinks[index] = link
		updated = link
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteJiraLink(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "jira.link_deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.JiraLinks)
		data.JiraLinks = slices.DeleteFunc(data.JiraLinks, func(link domain.JiraLink) bool { return link.ID == id })
		if before == len(data.JiraLinks) {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

// listJiraRemoteProjects returns live Jira projects when OAuth tokens exist.
// Without secrets/tokens it returns an honest empty payload + error message
// (never fake success projects).
func (s *server) listJiraRemoteProjects(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	connection := jiraConnection(&data)
	if !jiraConnected(connection) {
		writeJSON(w, http.StatusOK, map[string]any{
			"projects": []jiraRemoteProject{},
			"error":    "Connect Jira OAuth before listing projects. Live project discovery needs a completed OAuth token.",
			"status":   "unavailable",
		})
		return
	}
	if strings.TrimSpace(connection.OAuthAccessToken) == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"projects": []jiraRemoteProject{},
			"error":    "Jira OAuth token is not available on the server. Configure FLOW_INTEGRATION_JIRA_* secrets and reconnect.",
			"status":   "unavailable",
		})
		return
	}
	// Live Atlassian Cloud project fetch is deferred until deployment secrets
	// and site/cloud-id resolution are available. Prefer empty over fabricated.
	writeJSON(w, http.StatusOK, map[string]any{
		"projects": []jiraRemoteProject{},
		"error":    "Live Jira project listing is not enabled in this deployment. Save a sync link with a known project id, or configure Atlassian API access.",
		"status":   "unavailable",
	})
}

func (s *server) listJiraRemoteStatuses(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("projectId"))
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "A Jira project ID is required to fetch project statuses.")
		return
	}
	data := s.workspaceData(r)
	connection := jiraConnection(&data)
	if !jiraConnected(connection) || strings.TrimSpace(connection.OAuthAccessToken) == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"statuses":  []jiraRemoteStatus{},
			"error":     "Failed to load Jira statuses. Connect Jira OAuth first.",
			"status":    "unavailable",
			"projectId": projectID,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"statuses":  []jiraRemoteStatus{},
		"error":     "Failed to load Jira statuses. Live status fetch is not enabled in this deployment. The link can still be created; statuses sync when names match.",
		"status":    "unavailable",
		"projectId": projectID,
	})
}

func normalizeJiraSyncDirection(value string) string {
	switch strings.TrimSpace(value) {
	case "unidirectional", "jira_to_flow", "jira-to-linear":
		return "unidirectional"
	case "legacyUnidirectional":
		return "legacyUnidirectional"
	default:
		return "bidirectional"
	}
}

func validateJiraConnectConfig(config map[string]string) error {
	mode := strings.TrimSpace(config["mode"])
	if mode == "" {
		mode = "cloud"
		config["mode"] = mode
	}
	switch mode {
	case "cloud":
		return nil
	case "custom_personal":
		for _, key := range []string{"authorizationURL", "tokenURL", "clientID", "redirectURI"} {
			if strings.TrimSpace(config[key]) == "" {
				return fmt.Errorf("%s is required for custom Jira OAuth", key)
			}
		}
		if err := integrationEndpoint(config["authorizationURL"]); err != nil {
			return err
		}
		if err := integrationEndpoint(config["tokenURL"]); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("jira mode must be cloud or custom_personal")
	}
}

// ensureJiraLinksCleared drops sync links when the Jira connection is removed.
func ensureJiraLinksCleared(data *domain.Bootstrap, provider string) {
	if provider != "jira" {
		return
	}
	data.JiraLinks = nil
}

