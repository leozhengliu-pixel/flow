package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Workspace-level Loops settings: trusted sources and external triggers live
// here (Linear keeps them out of the loop editor), together with whether this
// server can search the web.

type loopTrustedSourceOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // integration | appUser
}

type loopConfigResponse struct {
	WebSearchAvailable      bool                      `json:"webSearchAvailable"`
	WebSearchProvider       string                    `json:"webSearchProvider"`
	AgentWebSearch          bool                      `json:"agentWebSearch"`
	CodeAccessAvailable     bool                      `json:"codeAccessAvailable"`
	ExternalLoopTriggers    bool                      `json:"externalLoopTriggers"`
	TrustedSourcesMode      string                    `json:"trustedSourcesMode"`
	TrustedSourcesAllowlist []string                  `json:"trustedSourcesAllowlist"`
	TrustedSourceOptions    []loopTrustedSourceOption `json:"trustedSourceOptions"`
}

var integrationSourceLabels = map[string]string{"slack": "Slack", "email": "Email", "intercom": "Intercom", "zendesk": "Zendesk", "front": "Front", "github": "GitHub", "gitlab": "GitLab", "jira": "Jira", "sentry": "Sentry", "discord": "Discord", "teams": "Microsoft Teams"}

func (s *server) loopConfig(data domain.Bootstrap) loopConfigResponse {
	settings := data.WorkspaceSettings
	response := loopConfigResponse{
		WebSearchAvailable: s.webSearchAvailable(), WebSearchProvider: s.webSearchProviderName(), AgentWebSearch: settings.AgentWebSearch,
		CodeAccessAvailable:  settings.FeatureSettings.RepositoryAccess != nil && settings.FeatureSettings.RepositoryAccess.AllowAutomationAccess,
		ExternalLoopTriggers: settings.ExternalLoopTriggers, TrustedSourcesMode: firstNonEmpty(settings.TrustedSourcesMode, "none"),
		TrustedSourcesAllowlist: slices.Clone(settings.TrustedSourcesAllowlist), TrustedSourceOptions: []loopTrustedSourceOption{},
	}
	if response.TrustedSourcesAllowlist == nil {
		response.TrustedSourcesAllowlist = []string{}
	}
	add := func(option loopTrustedSourceOption) {
		if !slices.ContainsFunc(response.TrustedSourceOptions, func(item loopTrustedSourceOption) bool { return item.Key == option.Key }) {
			response.TrustedSourceOptions = append(response.TrustedSourceOptions, option)
		}
	}
	add(loopTrustedSourceOption{Key: "integration:email", Label: "Email", Kind: "integration"})
	for _, connection := range data.IntegrationConnections {
		provider := strings.ToLower(strings.TrimSpace(connection.Provider))
		if provider == "" || connection.Status != "connected" {
			continue
		}
		label := integrationSourceLabels[provider]
		if label == "" {
			label = firstNonEmpty(connection.Name, provider)
		}
		add(loopTrustedSourceOption{Key: "integration:" + provider, Label: label, Kind: "integration"})
	}
	for _, member := range data.Members {
		if member.Role == "app" && member.Status != "suspended" && member.Status != "removed" {
			add(loopTrustedSourceOption{Key: "appUser:" + member.User.ID, Label: firstNonEmpty(member.User.Name, member.User.DisplayName, member.User.ID), Kind: "appUser"})
		}
	}
	// Keys already allowed stay listed even if their integration was removed.
	for _, key := range response.TrustedSourcesAllowlist {
		kind, value, _ := strings.Cut(key, ":")
		add(loopTrustedSourceOption{Key: key, Label: value, Kind: kind})
	}
	return response
}

func (s *server) getLoopConfig(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, s.loopConfig(data))
}

type loopSettingsInput struct {
	ExternalLoopTriggers    *bool     `json:"externalLoopTriggers,omitempty"`
	TrustedSourcesMode      *string   `json:"trustedSourcesMode,omitempty"`
	TrustedSourcesAllowlist *[]string `json:"trustedSourcesAllowlist,omitempty"`
	AgentWebSearch          *bool     `json:"agentWebSearch,omitempty"`
}

// normalizeTrustedSources validates and de-duplicates trusted source keys.
func normalizeTrustedSources(keys []string) ([]string, error) {
	result := []string{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		kind, value, found := strings.Cut(key, ":")
		if !found || strings.TrimSpace(value) == "" || kind != "integration" && kind != "appUser" {
			return nil, fmt.Errorf("invalid trusted source %q (use integration:<source> or appUser:<userId>)", key)
		}
		if kind == "integration" {
			key = "integration:" + strings.ToLower(strings.TrimSpace(value))
		}
		if !slices.Contains(result, key) {
			result = append(result, key)
		}
	}
	if len(result) > 100 {
		return nil, fmt.Errorf("at most 100 trusted sources")
	}
	return result, nil
}

func validTrustedSourcesMode(mode string) bool {
	return mode == "" || mode == "none" || mode == "allowlist"
}

func (s *server) updateLoopSettings(w http.ResponseWriter, r *http.Request) {
	var input loopSettingsInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.TrustedSourcesMode != nil && !validTrustedSourcesMode(*input.TrustedSourcesMode) {
		writeError(w, http.StatusBadRequest, "trustedSourcesMode must be none or allowlist")
		return
	}
	var allowlist []string
	if input.TrustedSourcesAllowlist != nil {
		var err error
		if allowlist, err = normalizeTrustedSources(*input.TrustedSourcesAllowlist); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	var updated domain.Bootstrap
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "workspace_preferences.updated", "workspace", input, func(data *domain.Bootstrap) error {
		settings := &data.WorkspaceSettings
		if input.ExternalLoopTriggers != nil {
			settings.ExternalLoopTriggers = *input.ExternalLoopTriggers
		}
		if input.TrustedSourcesMode != nil {
			settings.TrustedSourcesMode = firstNonEmpty(*input.TrustedSourcesMode, "none")
		}
		if input.TrustedSourcesAllowlist != nil {
			settings.TrustedSourcesAllowlist = allowlist
		}
		if input.AgentWebSearch != nil {
			settings.AgentWebSearch = *input.AgentWebSearch
		}
		settings.UpdatedAt = time.Now().UTC()
		appendAudit(data, "updated", "workspace_settings", "loops", map[string]any{"fields": "loop settings"})
		updated = *data
		return nil
	})
	respondMutation(w, err, http.StatusOK, s.loopConfig(updated))
}
