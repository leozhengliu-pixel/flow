package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"flow/api/internal/domain"
)

// Web access is tri-state: never set (on), explicitly on, explicitly off. Settings pages save the whole settings
// object, so echoing the stored values back must not turn "never set" into an explicit choice.
func TestAgentWebAccessIsTriState(t *testing.T) {
	_, handler := newLoopTestServer(t, &fakeLoopProvider{})
	read := func() domain.WorkspaceSettings {
		return requestJSON[domain.WorkspaceSettings](t, handler, http.MethodGet, "/api/workspace/preferences", nil, http.StatusOK)
	}
	webAccess := func() any {
		return requestJSON[map[string]any](t, handler, http.MethodGet, "/api/workspace/loop-settings", nil, http.StatusOK)["agentWebSearch"]
	}

	// Never set: on, and the stored record has no explicit value.
	settings := read()
	if settings.AgentWebAccess != nil || !settings.AgentWebAccessEnabled() || webAccess() != true {
		t.Fatalf("unset web access = %v enabled=%v", settings.AgentWebAccess, settings.AgentWebAccessEnabled())
	}
	// Saving another setting with the whole object (the Security page) keeps it unset.
	whole := map[string]any{}
	raw, _ := json.Marshal(settings)
	if err := json.Unmarshal(raw, &whole); err != nil {
		t.Fatal(err)
	}
	whole["preventGuestAgents"] = true
	delete(whole, "updatedAt")
	settings = requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", whole, http.StatusOK)
	if settings.AgentWebAccess != nil || !settings.AgentWebAccessEnabled() {
		t.Fatalf("whole-object save made web access explicit: %v", settings.AgentWebAccess)
	}

	// Explicitly off, and a later whole-object echo keeps it off.
	settings = requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"agentWebAccess": false}, http.StatusOK)
	if settings.AgentWebAccess == nil || *settings.AgentWebAccess || settings.AgentWebSearch || !settings.AgentWebSearchDisabled || webAccess() != false {
		t.Fatalf("explicit off = %+v", settings)
	}
	whole = map[string]any{}
	raw, _ = json.Marshal(read())
	if err := json.Unmarshal(raw, &whole); err != nil {
		t.Fatal(err)
	}
	whole["preventGuestAgents"] = false
	delete(whole, "updatedAt")
	settings = requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", whole, http.StatusOK)
	if settings.AgentWebAccess == nil || *settings.AgentWebAccess || webAccess() != false {
		t.Fatalf("whole-object save turned web access back on: %v", settings.AgentWebAccess)
	}

	// Explicitly on through the older default-on client (agentWebSearchDisabled), then off through Loops settings.
	settings = requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"agentWebSearch": true, "agentWebSearchDisabled": false}, http.StatusOK)
	if settings.AgentWebAccess == nil || !*settings.AgentWebAccess || webAccess() != true {
		t.Fatalf("explicit on = %+v", settings.AgentWebAccess)
	}
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/workspace/loop-settings", map[string]any{"agentWebSearch": false}, http.StatusOK)
	if settings = read(); settings.AgentWebAccess == nil || *settings.AgentWebAccess || !settings.AgentWebSearchDisabled {
		t.Fatalf("loops toggle off = %+v", settings.AgentWebAccess)
	}
}

func TestAgentWebAccessEnabledResolution(t *testing.T) {
	on, off := true, false
	for _, test := range []struct {
		name     string
		settings domain.WorkspaceSettings
		want     bool
	}{
		{"never set", domain.WorkspaceSettings{}, true},
		{"never set, legacy opt-in flag false", domain.WorkspaceSettings{AgentWebSearch: false}, true},
		{"explicit off", domain.WorkspaceSettings{AgentWebAccess: &off}, false},
		{"explicit on", domain.WorkspaceSettings{AgentWebAccess: &on, AgentWebSearchDisabled: true}, true},
		{"older client turned it off", domain.WorkspaceSettings{AgentWebSearchDisabled: true}, false},
		{"HIPAA", domain.WorkspaceSettings{AgentWebAccess: &on, HIPAACompliance: true}, false},
	} {
		if got := test.settings.AgentWebAccessEnabled(); got != test.want {
			t.Errorf("%s: enabled = %v, want %v", test.name, got, test.want)
		}
	}
}
