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
