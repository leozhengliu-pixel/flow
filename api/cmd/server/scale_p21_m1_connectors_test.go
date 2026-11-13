package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21M1ConnectorDiscoveryDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	var calls atomic.Int32
	var failing atomic.Bool
	upstream := fakeRuntimeConnector(t, &calls, &failing)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		public, private, child := idgen.New("m1-public-"), idgen.New("m1-private-"), idgen.New("m1-child-")
		policies := []applicationPolicy{
			{ID: "m1-shared", Name: "Shared", Kind: "mcp", Status: "approved", Shared: true, URL: upstream.URL},
			{ID: "m1-owned", Name: "Owned", Kind: "mcp", Status: "approved", OwnerID: f.data.Viewer.ID, URL: upstream.URL},
			{ID: "m1-rejected", Name: "Rejected", Kind: "mcp", Status: "rejected", Shared: true, URL: upstream.URL},
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
			data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
			data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private}
			data.Settings["applicationPolicies"] = policies
			data.Projects = append(data.Projects, domain.Project{ID: "m1-unrelated", Description: strings.Repeat("unrelated ", 40000)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		principals := []domain.User{f.data.Viewer}
		for _, spec := range []struct {
			name, role string
			teams      []string
		}{
			{"admin", "admin", []string{private}},
			{"member-private-outsider", "member", []string{public}},
			{"guest-explicit", "guest", []string{public}},
			{"inherited", "member", []string{private}},
		} {
			_, user := scaleP03Join(t, f, spec.name, idgen.New("m1-connector-")+"@example.test", spec.role, spec.teams...)
			principals = append(principals, user)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		legacy, bounded := &server{store: repo, authDisabled: true}, &server{store: probe, authDisabled: true}
		for _, s := range []*server{legacy, bounded} {
			s.agent.ToolsEnabled, s.agent.WriteTools = true, true
		}
		for _, principal := range principals {
			for _, restriction := range []string{"session", "all", "selected-empty", "selected-set"} {
				t.Run(principal.ID+"/"+restriction, func(t *testing.T) {
					ctx := context.WithValue(t.Context(), authUserContextKey{}, principal)
					if restriction != "session" {
						key := domain.APIKey{ID: "m1-key", TeamRestriction: "all", Scopes: []string{"read", "write"}}
						if strings.HasPrefix(restriction, "selected") {
							key.TeamRestriction, key.TeamIDs = "selected", []string{}
							if restriction == "selected-set" {
								key.TeamIDs = []string{public}
							}
						}
						ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
					}
					for _, selection := range []struct {
						kind string
						ids  []string
					}{
						{store.AgentRunKindChat, nil},
						{store.AgentRunKindLoop, []string{"m1-shared"}},
						{store.AgentRunKindLoop, nil},
					} {
						grant := &agentRunGrant{run: store.AgentRun{WorkspaceKey: workspace, Kind: selection.kind}, principal: agentRunPrincipal{UserID: principal.ID, Connectors: selection.ids}}
						left, leftErr := legacy.discoverAgentRunConnectors(ctx, grant)
						right, rightErr := bounded.discoverAgentRunConnectors(ctx, grant)
						if leftErr != nil || rightErr != nil || !reflect.DeepEqual(left, right) {
							t.Fatalf("connector selection differs: %#v/%#v errors=%v/%v", left, right, leftErr, rightErr)
						}
						want := 1
						if selection.kind == store.AgentRunKindLoop && len(selection.ids) == 0 {
							want = 0
						} else if selection.kind == store.AgentRunKindChat && principal.ID == f.data.Viewer.ID {
							want = 2
						}
						if len(right) != want {
							t.Fatalf("owner/shared/rejected selection: got=%d want=%d", len(right), want)
						}
					}
				})
			}
		}
		// Revocation is a connector-policy change, independent of team content.
		// Replayed discovery must use the current policy rather than an old run.
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.revoke", "", nil, func(data *domain.Bootstrap) error {
			off := false
			data.WorkspaceSettings.MCPConnectors = &off
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		grant := &agentRunGrant{run: store.AgentRun{WorkspaceKey: workspace, Kind: store.AgentRunKindChat}, principal: agentRunPrincipal{UserID: f.data.Viewer.ID}}
		for _, phase := range []string{"queued", "replay"} {
			for _, s := range []*server{legacy, bounded} {
				tools, err := s.discoverAgentRunConnectors(t.Context(), grant)
				if err != nil || len(tools) != 0 {
					t.Fatalf("%s reused revoked connector policy: %v", phase, err)
				}
			}
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.large_policy", "", nil, func(data *domain.Bootstrap) error {
			large := make([]applicationPolicy, 1500)
			for i := range large {
				large[i] = applicationPolicy{ID: idgen.New("m1-policy-"), Name: strings.Repeat("Policy ", 20), Kind: "mcp", Status: "approved", Shared: true, URL: upstream.URL}
			}
			data.Settings["applicationPolicies"] = large
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if tools, err := bounded.discoverAgentRunConnectors(t.Context(), grant); err != nil || len(tools) != 0 {
			t.Fatal("disabled connectors unnecessarily loaded oversized policy")
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.enable", "", nil, func(data *domain.Bootstrap) error {
			on := true
			data.WorkspaceSettings.MCPConnectors = &on
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := bounded.discoverAgentRunConnectors(t.Context(), grant); !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatalf("oversized connector policy was not an explicit budget error: %v", err)
		}
		for _, s := range []*server{legacy, bounded} {
			empty := *grant
			empty.run.Kind = store.AgentRunKindLoop
			if tools, err := s.discoverAgentRunConnectors(t.Context(), &empty); err != nil || len(tools) != 0 {
				t.Fatal("empty loop selection unnecessarily loaded policy")
			}
			s.agent.ToolsEnabled = false
			if tools, err := s.discoverAgentRunConnectors(t.Context(), grant); err != nil || len(tools) != 0 {
				t.Fatal("disabled tools unnecessarily loaded policy")
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("connector discovery decoded a complete workspace")
		}
		if calls.Load() != 0 {
			t.Fatal("discovery executed a connector tool")
		}
	}
}
