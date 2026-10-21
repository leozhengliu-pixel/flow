package store

import (
	"testing"

	"flow/api/internal/domain"
)

// A workspace saved before the explicit MCP connector choice existed keeps the value it had: a stored "off" may be an
// admin's decision and is never turned on by loading it. New workspaces start with connectors on.
func TestMCPConnectorsStoredChoiceIsKept(t *testing.T) {
	for _, stored := range []bool{false, true} {
		data := domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{MCPConnectorsEnabled: stored}}
		normalizeParity(&data)
		if data.WorkspaceSettings.MCPConnectors == nil || *data.WorkspaceSettings.MCPConnectors != stored || data.WorkspaceSettings.MCPConnectorsEnabled != stored {
			t.Fatalf("stored %v: choice=%v mirror=%v", stored, data.WorkspaceSettings.MCPConnectors, data.WorkspaceSettings.MCPConnectorsEnabled)
		}
	}
	defaults := defaultWorkspaceSettings()
	if defaults.MCPConnectors == nil || !*defaults.MCPConnectors || !defaults.MCPConnectorsEnabled {
		t.Fatalf("new workspace: choice=%v mirror=%v", defaults.MCPConnectors, defaults.MCPConnectorsEnabled)
	}
}
