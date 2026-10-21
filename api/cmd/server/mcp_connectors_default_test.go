package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"flow/api/internal/domain"
)

// A new workspace starts with MCP connectors on, a settings page that saves the whole object keeps the choice, and
// HIPAA mode always forces them off.
func TestMCPConnectorsDefaultOnAndTriState(t *testing.T) {
	_, handler := newLoopTestServer(t, &fakeLoopProvider{})
	read := func() domain.WorkspaceSettings {
		return requestJSON[domain.WorkspaceSettings](t, handler, http.MethodGet, "/api/workspace/preferences", nil, http.StatusOK)
	}
	patch := func(body map[string]any) domain.WorkspaceSettings {
		return requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", body, http.StatusOK)
	}
	whole := func() map[string]any {
		out := map[string]any{}
		raw, _ := json.Marshal(read())
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		delete(out, "updatedAt")
		return out
	}

	settings := read()
	if settings.MCPConnectors == nil || !*settings.MCPConnectors || !settings.MCPConnectorsEnabled || !settings.MCPConnectorsActive() {
		t.Fatalf("new workspace: choice=%v mirror=%v active=%v", settings.MCPConnectors, settings.MCPConnectorsEnabled, settings.MCPConnectorsActive())
	}

	body := whole()
	body["preventGuestAgents"] = true
	settings = patch(body)
	if settings.MCPConnectors == nil || !*settings.MCPConnectors || !settings.MCPConnectorsActive() {
		t.Fatalf("whole-object save changed the choice: %v", settings.MCPConnectors)
	}

	settings = patch(map[string]any{"mcpConnectors": false})
	if settings.MCPConnectors == nil || *settings.MCPConnectors || settings.MCPConnectorsEnabled || settings.MCPConnectorsActive() {
		t.Fatalf("explicit off = %+v", settings)
	}
	body = whole()
	body["preventGuestAgents"] = false
	settings = patch(body)
	if settings.MCPConnectors == nil || *settings.MCPConnectors || settings.MCPConnectorsActive() {
		t.Fatalf("whole-object echo changed an explicit off: %v", settings.MCPConnectors)
	}

	// An older client that sends the mirror still works when it changes the value.
	settings = patch(map[string]any{"mcpConnectorsEnabled": true})
	if settings.MCPConnectors == nil || !*settings.MCPConnectors || !settings.MCPConnectorsActive() {
		t.Fatalf("legacy on = %+v", settings)
	}

	// HIPAA mode forces connectors off without touching the admin's choice.
	settings = patch(map[string]any{"hipaaCompliance": true})
	if settings.MCPConnectors == nil || !*settings.MCPConnectors || settings.MCPConnectorsActive() {
		t.Fatalf("hipaa: choice=%v active=%v", settings.MCPConnectors, settings.MCPConnectorsActive())
	}
	settings = patch(map[string]any{"hipaaCompliance": false})
	if !settings.MCPConnectorsActive() {
		t.Fatal("connectors did not return after HIPAA mode was turned off")
	}
}

func TestMCPConnectorsActiveResolution(t *testing.T) {
	off, on := false, true
	cases := []struct {
		name     string
		settings domain.WorkspaceSettings
		want     bool
	}{
		{"never set", domain.WorkspaceSettings{}, true},
		{"older stored false is not a choice", domain.WorkspaceSettings{MCPConnectorsEnabled: false}, true},
		{"explicit on", domain.WorkspaceSettings{MCPConnectors: &on}, true},
		{"explicit off", domain.WorkspaceSettings{MCPConnectors: &off}, false},
		{"hipaa beats never set", domain.WorkspaceSettings{HIPAACompliance: true}, false},
		{"hipaa beats explicit on", domain.WorkspaceSettings{HIPAACompliance: true, MCPConnectors: &on}, false},
	}
	for _, item := range cases {
		if got := item.settings.MCPConnectorsActive(); got != item.want {
			t.Errorf("%s: got %v want %v", item.name, got, item.want)
		}
	}
	item := applicationPolicy{Kind: "mcp", Shared: true, Status: "approved"}
	if !connectorAllowed(domain.WorkspaceSettings{}, item, "u") || connectorAllowed(domain.WorkspaceSettings{HIPAACompliance: true}, item, "u") {
		t.Fatal("connectorAllowed must follow the default-on setting and HIPAA")
	}
}
