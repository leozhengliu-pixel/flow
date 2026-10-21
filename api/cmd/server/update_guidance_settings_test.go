package main

import (
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// Workspace-wide initiative and project update guidance is trimmed, kept next to the other feature settings and capped.
func TestUpdateGuidanceFeatureSettings(t *testing.T) {
	_, handler := newLoopTestServer(t, &fakeLoopProvider{})
	patch := func(fields map[string]any, status int) domain.WorkspaceSettings {
		return requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": fields}, status)
	}
	settings := patch(map[string]any{"initiativeUpdateGuidance": "  Lead with risks.  ", "projectUpdateGuidance": "Keep it short."}, http.StatusOK)
	if settings.FeatureSettings.InitiativeUpdateGuidance != "Lead with risks." || settings.FeatureSettings.ProjectUpdateGuidance != "Keep it short." {
		t.Fatalf("guidance = %q / %q", settings.FeatureSettings.InitiativeUpdateGuidance, settings.FeatureSettings.ProjectUpdateGuidance)
	}
	// Changing one leaves the other alone.
	settings = patch(map[string]any{"projectUpdateGuidance": ""}, http.StatusOK)
	if settings.FeatureSettings.ProjectUpdateGuidance != "" || settings.FeatureSettings.InitiativeUpdateGuidance != "Lead with risks." {
		t.Fatalf("after clearing project guidance = %q / %q", settings.FeatureSettings.InitiativeUpdateGuidance, settings.FeatureSettings.ProjectUpdateGuidance)
	}
	patch(map[string]any{"initiativeUpdateGuidance": strings.Repeat("x", 8001)}, http.StatusBadRequest)
}

// The agent writes updates following the workspace's update guidance.
func TestUpdateGuidanceReachesAgentPrompt(t *testing.T) {
	data := domain.Bootstrap{Workspace: domain.Workspace{Name: "Acme", URLKey: "acme"}}
	data.WorkspaceSettings.FeatureSettings.ProjectUpdateGuidance = "Keep project updates to three bullets."
	data.WorkspaceSettings.FeatureSettings.InitiativeUpdateGuidance = "Lead initiative updates with risks."
	prompt := workspaceAgentSystemPrompt(data, nil, nil)
	if !strings.Contains(prompt, "When writing a project update:\nKeep project updates to three bullets.") || !strings.Contains(prompt, "When writing an initiative update:\nLead initiative updates with risks.") {
		t.Fatalf("prompt lacks update guidance:\n%s", prompt)
	}
	data.WorkspaceSettings.FeatureSettings.ProjectUpdateGuidance, data.WorkspaceSettings.FeatureSettings.InitiativeUpdateGuidance = "", ""
	if strings.Contains(workspaceAgentSystemPrompt(data, nil, nil), "When writing a") {
		t.Fatal("empty update guidance still adds a section")
	}
}
