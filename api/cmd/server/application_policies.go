package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type applicationPolicy struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	URL     string   `json:"url,omitempty"`
	OwnerID string   `json:"ownerId"`
	Shared  bool     `json:"shared"`
	Status  string   `json:"status"`
	Scopes  []string `json:"scopes,omitempty"`
	// CodingSessions exposes an approved, shared MCP connector to coding
	// sessions through Flow's MCP gateway (coding_mcp_gateway.go). Only
	// workspace admins set it.
	CodingSessions bool `json:"codingSessions,omitempty"`
	// ToolApproval is "always" when Flow Agent asks before every call to this
	// MCP connector's tools. Empty (the default) runs them all automatically.
	ToolApproval string `json:"toolApproval,omitempty"`
	// DisplayName overrides Name in Flow's UI and agent tool descriptions.
	// Empty shows Name, the connector's default name.
	DisplayName string `json:"displayName,omitempty"`
	// Tools are the tool names the connector offered the last time its owner
	// connected to it (POST /api/application-policies/{id}/connect). Only the
	// server sets them.
	Tools []string `json:"tools,omitempty"`
	// ReadOnlyTools are the tools the connector marked read-only (MCP
	// readOnlyHint) the last time it was connected. Only the server sets them.
	ReadOnlyTools []string `json:"readOnlyTools,omitempty"`
	// ToolAccess overrides, per tool name, whether Flow treats a connector
	// tool as read-only ("read") or as a tool that may change data ("write"),
	// whatever its readOnlyHint says: the annotation is the connector's own
	// claim. A tool not listed follows its annotation. It decides whether a
	// loop that may not change anything runs the tool, and whether an agent's
	// answer counts the call as a change. Only workspace admins set it.
	ToolAccess  map[string]string `json:"toolAccess,omitempty"`
	ConnectedAt *time.Time        `json:"connectedAt,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// label is the connector's name as people see it.
func (p applicationPolicy) label() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Name
}

const connectorToolApprovalAlways = "always"

// The ToolAccess values: treat the tool as read-only, or as a write.
const (
	connectorToolRead  = "read"
	connectorToolWrite = "write"
)

// maxConnectorToolAccess bounds the overrides one connector keeps.
const maxConnectorToolAccess = maxConnectorToolNames

// validConnectorToolAccess reports whether overrides name tools and say read or write.
func validConnectorToolAccess(access map[string]string) bool {
	if len(access) > maxConnectorToolAccess {
		return false
	}
	for name, value := range access {
		if strings.TrimSpace(name) == "" || len(name) > 256 || value != connectorToolRead && value != connectorToolWrite {
			return false
		}
	}
	return true
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
	for _, app := range data.OAuthApplications {
		if app.ClientID == clientID {
			return !slices.ContainsFunc(scopes, func(scope string) bool { return !slices.Contains(app.Scopes, scope) })
		}
	}
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
	if input.Kind != "mcp" {
		input.CodingSessions = false
	}
	if input.Kind != "mcp" || input.ToolApproval != connectorToolApprovalAlways {
		input.ToolApproval = ""
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.Kind != "mcp" {
		input.DisplayName = ""
	}
	if len(input.DisplayName) > 200 {
		writeError(w, 400, "invalid application")
		return
	}
	input.Tools, input.ReadOnlyTools, input.ConnectedAt = nil, nil, nil
	if input.Kind != "mcp" || len(input.ToolAccess) == 0 {
		input.ToolAccess = nil
	}
	if admin && !validConnectorToolAccess(input.ToolAccess) {
		writeError(w, 400, `toolAccess must map tool names to "read" or "write"`)
		return
	}
	if input.Kind == "mcp" {
		input.URL = strings.TrimSpace(input.URL)
		if err := integrationEndpointError(r.Context(), input.URL, s.authDisabled, outboundTrustedPrivate); err != nil {
			writeError(w, 400, "MCP server must use an HTTPS endpoint Flow may reach: "+err.Error())
			return
		}
		if !view.WorkspaceSettings.MCPConnectorsActive() {
			writeError(w, 403, "MCP connectors are disabled")
			return
		}
	}
	if !admin && (input.Kind == "oauth" || input.Shared || input.CodingSessions || input.Status == "approved" || input.Status == "rejected") {
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
		previousAccess := map[string]string(nil)
		if index >= 0 {
			previous := items[index]
			if previous.Kind != input.Kind || !admin && previous.OwnerID != view.Viewer.ID {
				return store.ErrAuthForbidden
			}
			input.OwnerID = previous.OwnerID
			// The tool inventory belongs to the endpoint it was read from.
			if previous.URL == input.URL {
				input.Tools, input.ReadOnlyTools, input.ConnectedAt = previous.Tools, previous.ReadOnlyTools, previous.ConnectedAt
				previousAccess = previous.ToolAccess
			}
		}
		if !admin {
			// Only an admin decides how Flow treats a connector's tools: an owner's save keeps the admin's
			// decision (dropped with the tool inventory when the owner points the connector elsewhere).
			input.ToolAccess = maps.Clone(previousAccess)
			// Approvals cannot be carried over to another endpoint by its owner.
			if index >= 0 && items[index].URL == input.URL {
				input.Status = items[index].Status
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
	var removed applicationPolicy
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "application_policy.updated", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		items := applicationPolicies(data)
		index := slices.IndexFunc(items, func(item applicationPolicy) bool { return item.ID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		if !s.applicationPolicyAdmin(r, view) && (items[index].OwnerID != view.Viewer.ID || items[index].Kind == "oauth") {
			return store.ErrAuthForbidden
		}
		removed = items[index]
		setApplicationPolicies(data, slices.Delete(items, index, index+1))
		return nil
	})
	if err == nil && removed.Kind == "mcp" {
		err = s.store.DeleteConnectorSecret(r.Context(), connectorSecretID(workspaceKey(r), connectorOwner(removed, view.Viewer.ID), removed.ID))
	}
	respondMutation(w, err, http.StatusNoContent, nil)
}
