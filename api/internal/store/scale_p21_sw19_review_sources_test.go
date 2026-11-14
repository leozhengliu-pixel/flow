package store

import (
	"slices"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW19ReviewSourceTrustedMutationHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	connected := func(data domain.Bootstrap) bool {
		return slices.ContainsFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
			return item.Provider == domain.CodeHostGitHub && item.Status != "disconnected"
		})
	}
	for _, spec := range []struct {
		name, provider, status string
		warm, canonical        bool
	}{
		{"provider", "git\x00hub", "configured", false, true},
		{"status", "github", "dis\x00connected", true, false},
	} {
		t.Run(spec.name, func(t *testing.T) {
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw19.trusted_source", "", nil, func(data *domain.Bootstrap) error {
				data.IntegrationConnections = []domain.IntegrationConnection{{ID: "sw19-trusted-source", Provider: spec.provider, Status: spec.status}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			warm, found := f.WorkspaceMetadata(workspace)
			if !found || warm.IntegrationConnections[0].Provider != spec.provider || warm.IntegrationConnections[0].Status != spec.status {
				t.Fatal("trusted typed producer no longer preserves its installed fields")
			}
			canonical, found, err := view.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{Kind: "integrationConnections", ID: "sw19-trusted-source"}}})
			if err != nil || !found || len(canonical.IntegrationConnections) != 1 {
				t.Fatal("read canonical witness", found, err)
			}
			if connected(warm) != spec.warm || connected(canonical) != spec.canonical {
				t.Fatal("installed/canonical decision witness changed")
			}
			t.Logf("trusted mutation %s warm=%t canonical=%t", spec.name, spec.warm, spec.canonical)
		})
	}
}
