package main

import (
	"net/http"
	"testing"

	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

func TestScaleP21M3CodingSettingsDifferential(t *testing.T) {
	seed := func(data *domain.Bootstrap, _ domain.User) {
		data.WorkspaceSettings.CodingAgentSettings = domain.CodingAgentSettings{}
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags[coding.FeatureFlag] = false
	}
	handler := func(s *server, w http.ResponseWriter, r *http.Request) {
		// This endpoint only needs a non-nil service and its policy config;
		// no runner, sandbox or background reconciler is started.
		s.coding = &codingServices{service: new(coding.Service), config: appconfig.CodingConfig{Provider: appconfig.CodingProviderFake, Regions: []string{"eu"}}}
		s.updateCodingSettings(w, r)
	}
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{
		{
			name: "coding-settings", kind: "workspaceSettings", body: `{"enabled":true,"harness":"codex","pinnedRegion":"eu","reasoningEffort":"high","loopRepositoryAccess":"selected","loopRepositories":["env1","env1"," "]}`,
			seed: seed, handler: handler,
		},
		{
			name: "coding-settings-invalid", kind: "workspaceSettings", body: `{"pinnedRegion":"mars"}`,
			seed: seed, handler: handler,
		},
	})
}
