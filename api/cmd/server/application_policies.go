package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type applicationPolicy struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url,omitempty"`
	OwnerID   string    `json:"ownerId"`
	Shared    bool      `json:"shared"`
	Status    string    `json:"status"`
	Scopes    []string  `json:"scopes,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func applicationPolicies(data *domain.Bootstrap) []applicationPolicy {
	items := []applicationPolicy{}
	raw, _ := json.Marshal(data.Settings["applicationPolicies"])
	_ = json.Unmarshal(raw, &items)
	if items == nil {
		items = []applicationPolicy{}
	}
	return items
}

func setApplicationPolicies(data *domain.Bootstrap, items []applicationPolicy) {
	if data.Settings == nil {
		data.Settings = map[string]any{}
	}
	data.Settings["applicationPolicies"] = items
}

func applicationApproved(data *domain.Bootstrap, clientID string, scopes []string) bool {
	if !data.WorkspaceSettings.ReviewThirdPartyApplications {
		return true
	}
	for _, item := range applicationPolicies(data) {
		if item.Kind == "oauth" && item.ID == clientID && item.Status == "approved" {
			return !slices.ContainsFunc(scopes, func(scope string) bool { return !slices.Contains(item.Scopes, scope) })
		}
	}
	return false
}

func (s *server) listApplicationPolicies(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	items := applicationPolicies(&data)
	items = slices.DeleteFunc(items, func(item applicationPolicy) bool {
		return !s.applicationPolicyAdmin(r, data) && !item.Shared && item.OwnerID != data.Viewer.ID
	})
	writeJSON(w, http.StatusOK, items)
}

func (s *server) applicationPolicyAdmin(r *http.Request, data domain.Bootstrap) bool {
	if s.authDisabled {
		return true
	}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && !apiKeyHasScope(key, "admin") {
		return false
	}
	return workspaceAdminRole(data.ViewerRole)
}

func (s *server) saveApplicationPolicy(w http.ResponseWriter, r *http.Request) {
	var input applicationPolicy
	if !decodeJSON(w, r, &input) {
		return
	}
	view := s.workspaceData(r)
	admin := s.applicationPolicyAdmin(r, view)
	if input.Kind != "mcp" && input.Kind != "oauth" || strings.TrimSpace(input.Name) == "" || len(input.Name) > 200 {
		writeError(w, 400, "invalid application")
		return
	}
	if input.Kind == "mcp" {
		input.URL = strings.TrimRight(strings.TrimSpace(input.URL), "/")
		if !integrationEndpointSafe(r.Context(), input.URL, s.authDisabled) {
			writeError(w, 400, "MCP server must use a public HTTPS endpoint")
			return
		}
		if !view.WorkspaceSettings.MCPConnectorsEnabled {
			writeError(w, 403, "MCP connectors are disabled")
			return
		}
	}
	if !admin && (input.Kind == "oauth" || input.Shared || input.Status == "approved" || input.Status == "rejected") {
		writeError(w, 403, "Administrator approval required")
		return
	}
	if input.ID == "" {
		id, err := randomSecret("mcp_")
		if err != nil {
			writeError(w, 500, "Could not create connector")
			return
		}
		input.ID = id
	}
	if len(input.ID) > 200 {
		writeError(w, 400, "invalid application id")
		return
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "application_policy.updated", input.ID, nil, func(data *domain.Bootstrap) error {
		items := applicationPolicies(data)
		index := slices.IndexFunc(items, func(item applicationPolicy) bool { return item.ID == input.ID })
		input.OwnerID = view.Viewer.ID
		if index >= 0 {
			previous := items[index]
			if previous.Kind != input.Kind || !admin && previous.OwnerID != view.Viewer.ID {
				return store.ErrAuthForbidden
			}
			input.OwnerID = previous.OwnerID
			// Approvals cannot be carried over to another endpoint by its owner.
			if !admin && previous.URL == input.URL {
				input.Status = previous.Status
			}
		}
		if !slices.Contains([]string{"approved", "rejected", "pending"}, input.Status) {
			input.Status = "pending"
		}
		input.UpdatedAt = time.Now().UTC()
		if index >= 0 {
			items[index] = input
		} else {
			if len(items) >= 100 {
				return errInvalid
			}
			items = append(items, input)
		}
		setApplicationPolicies(data, items)
		return nil
	})
	respondMutation(w, err, http.StatusOK, input)
}

func (s *server) deleteApplicationPolicy(w http.ResponseWriter, r *http.Request) {
	view := s.workspaceData(r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "application_policy.updated", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		items := applicationPolicies(data)
		index := slices.IndexFunc(items, func(item applicationPolicy) bool { return item.ID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		if !s.applicationPolicyAdmin(r, view) && (items[index].OwnerID != view.Viewer.ID || items[index].Kind == "oauth") {
			return store.ErrAuthForbidden
		}
		setApplicationPolicies(data, slices.Delete(items, index, index+1))
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}
