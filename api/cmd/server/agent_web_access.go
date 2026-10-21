package main

import (
	"encoding/json"

	"flow/api/internal/domain"
)

// reconcileAgentWebAccess decides Flow Agent's web access after a workspace preferences patch. Only a patch that
// changes the setting records an explicit choice: settings pages save the whole settings object, so an unchanged
// agentWebSearch / agentWebSearchDisabled echoed back must not turn "never set" (default on) into an explicit value.
// Clients send agentWebAccess; agentWebSearchDisabled (the first default-on client) and agentWebSearch (the opt-in
// era) still work when they change the value.
func reconcileAgentWebAccess(input *domain.WorkspaceSettings, previous domain.WorkspaceSettings, patch map[string]json.RawMessage) {
	_, hasAccess := patch["agentWebAccess"]
	_, hasDisabled := patch["agentWebSearchDisabled"]
	_, hasLegacy := patch["agentWebSearch"]
	switch {
	case hasAccess && input.AgentWebAccess != nil:
		input.SetAgentWebAccess(*input.AgentWebAccess)
	case hasDisabled && input.AgentWebSearchDisabled != previous.AgentWebSearchDisabled:
		input.SetAgentWebAccess(!input.AgentWebSearchDisabled)
	case hasLegacy && input.AgentWebSearch != previous.AgentWebSearch:
		input.SetAgentWebAccess(input.AgentWebSearch)
	default:
		input.AgentWebAccess = previous.AgentWebAccess
		input.AgentWebSearch = previous.AgentWebSearch
		input.AgentWebSearchDisabled = previous.AgentWebSearchDisabled
	}
}

// reconcileMCPConnectors decides Flow Agent's MCP connector switch after a workspace preferences patch, like
// reconcileAgentWebAccess: only a patch that changes the setting records an explicit choice, so settings pages that echo
// the whole object (with mcpConnectorsEnabled as they last read it) cannot turn "never set" (on) into an explicit value.
// Clients send mcpConnectors; mcpConnectorsEnabled (the older client) still works when it changes the value.
func reconcileMCPConnectors(input *domain.WorkspaceSettings, previous domain.WorkspaceSettings, patch map[string]json.RawMessage) {
	_, hasChoice := patch["mcpConnectors"]
	_, hasMirror := patch["mcpConnectorsEnabled"]
	resolved := previous.MCPConnectors == nil || *previous.MCPConnectors
	switch {
	case hasChoice && input.MCPConnectors != nil:
		input.SetMCPConnectors(*input.MCPConnectors)
	case hasMirror && input.MCPConnectorsEnabled != resolved:
		input.SetMCPConnectors(input.MCPConnectorsEnabled)
	default:
		input.MCPConnectors = previous.MCPConnectors
		input.MCPConnectorsEnabled = resolved
	}
}
